package iso8211

import (
	"io"
	"io/fs"
)

// Parser provides methods to parse ISO 8211 binary files
//
// Based on ISO/IEC 8211:1994 standard.
// For S-57/S-52 implementation details, see IHO S-57 Part 3:
// https://iho.int/uploads/user/pubs/standards/s-57/31Main.pdf
type Parser struct {
	reader io.Reader // Underlying data reader
	closer io.Closer // Optional closer (for files)
	offset int64     // Current read offset
}

// NewParser creates a new ISO 8211 parser from an io.Reader
// Works with any reader: bytes, HTTP responses, etc.
// Use Open() or OpenFS() for filesystem-based parsing
func NewParser(r io.Reader) *Parser {
	return &Parser{
		reader: r,
		closer: nil,
		offset: 0,
	}
}

// Open opens an ISO 8211 file from the OS filesystem and returns a Parser
// The file will be closed when Parser.Close() is called
// Returns error if file cannot be opened
func Open(filepath string) (*Parser, error) {
	return OpenFS(OSFS(), filepath)
}

// OpenFS opens an ISO 8211 file from a custom io/fs.FS and returns a Parser.
// This allows using custom filesystem implementations (e.g. in-memory; see MemFS).
// The file will be closed when Parser.Close() is called.
// Returns error if file cannot be opened.
func OpenFS(fsys fs.FS, filepath string) (*Parser, error) {
	file, err := fsys.Open(filepath)
	if err != nil {
		return nil, err
	}

	return &Parser{
		reader: file,
		closer: file,
		offset: 0,
	}, nil
}

// Close closes the underlying reader if it implements io.Closer
// Can be called multiple times safely (idempotent)
// Safe to call even if reader doesn't need closing
func (p *Parser) Close() error {
	if p.closer != nil {
		err := p.closer.Close()
		p.closer = nil // Prevent double close
		return err
	}
	return nil
}

// getReader returns the underlying io.Reader
func (p *Parser) getReader() io.Reader {
	return p.reader
}

// Parse reads and parses the complete ISO 8211 file (DDR + all data records)
// Returns complete ISO8211File structure with DDR and all records
// Returns error if file is malformed or cannot be parsed
func (p *Parser) Parse() (*ISO8211File, error) {
	result := &ISO8211File{
		Records: make([]*DataRecord, 0),
	}

	// Parse DDR first (must be first record in file)
	ddr, err := p.parseDDR()
	if err != nil {
		return nil, err
	}
	result.DDR = ddr

	// Parse all data records
	for {
		dr, err := p.parseDataRecord(ddr)
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}
		result.Records = append(result.Records, dr)
	}

	return result, nil
}

// parseDDR parses the Data Descriptive Record (first record in file)
func (p *Parser) parseDDR() (*DataDescriptiveRecord, error) {
	// Parse leader
	leader, err := p.parseLeader()
	if err != nil {
		return nil, NewParseError(p.offset, "DDR leader", err)
	}

	// Validate this is a DDR
	if leader.LeaderIdentifier != 'L' {
		return nil, NewValidationError("LeaderIdentifier", string(leader.LeaderIdentifier), "expected 'L' for DDR")
	}

	// Parse directory
	directory, err := p.parseDirectory(leader)
	if err != nil {
		return nil, NewParseError(p.offset, "DDR directory", err)
	}

	// Read field area
	fieldAreaSize := leader.RecordLength - leader.FieldAreaStart
	fieldArea := make([]byte, fieldAreaSize)
	n, err := io.ReadFull(p.getReader(), fieldArea)
	if err != nil {
		return nil, NewParseError(p.offset, "DDR field area", err)
	}
	if n != fieldAreaSize {
		return nil, NewParseError(p.offset, "DDR field area", io.ErrUnexpectedEOF)
	}
	p.offset += int64(n)

	// Parse field controls from field area
	fieldControls, err := p.parseFieldControls(directory, fieldArea)
	if err != nil {
		return nil, NewParseError(p.offset, "DDR field controls", err)
	}

	return &DataDescriptiveRecord{
		Leader:        leader,
		Directory:     directory,
		FieldControls: fieldControls,
		FieldArea:     fieldArea,
	}, nil
}

// parseDataRecord parses a single data record
func (p *Parser) parseDataRecord(ddr *DataDescriptiveRecord) (*DataRecord, error) {
	// Parse leader
	leader, err := p.parseLeader()
	if err != nil {
		return nil, err
	}

	// Validate this is a DR
	if leader.LeaderIdentifier != 'D' {
		return nil, NewValidationError("LeaderIdentifier", string(leader.LeaderIdentifier), "expected 'D' for data record")
	}

	// Parse directory
	directory, err := p.parseDirectory(leader)
	if err != nil {
		return nil, NewParseError(p.offset, "data record directory", err)
	}

	// Read field area
	fieldAreaSize := leader.RecordLength - leader.FieldAreaStart
	fieldArea := make([]byte, fieldAreaSize)
	n, err := io.ReadFull(p.getReader(), fieldArea)
	if err != nil {
		return nil, NewParseError(p.offset, "data record field area", err)
	}
	if n != fieldAreaSize {
		return nil, NewParseError(p.offset, "data record field area", io.ErrUnexpectedEOF)
	}
	p.offset += int64(n)

	// Extract fields from field area
	fields, err := p.extractFields(directory, fieldArea)
	if err != nil {
		return nil, NewParseError(p.offset, "data record fields", err)
	}

	return &DataRecord{
		Leader:    leader,
		Directory: directory,
		Fields:    fields,
	}, nil
}
