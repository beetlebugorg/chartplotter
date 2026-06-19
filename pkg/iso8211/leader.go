package iso8211

import (
	"fmt"
	"io"
	"strconv"
)

// parseLeader parses a 24-byte ISO 8211 leader structure
// IHO S-57 Part 3 Annex A.2.2.1 (DDR leader - Table A.1) and A.2.2.2 (DR leader - Table A.3)
func (p *Parser) parseLeader() (*Leader, error) {
	// ISO 8211 leader is exactly 24 bytes
	buf := make([]byte, 24)
	startOffset := p.offset

	n, err := io.ReadFull(p.getReader(), buf)
	if err != nil {
		if err == io.EOF && n == 0 {
			return nil, io.EOF
		}
		return nil, err
	}
	if n != 24 {
		return nil, fmt.Errorf("leader must be 24 bytes, got %d", n)
	}
	p.offset += 24

	// Trailing padding after the last record — a leader of only spaces and/or
	// null bytes — is end-of-records, not a malformed record. Signal EOF so the
	// data-record loop stops cleanly (some S-57 producers pad the file end).
	blank := true
	for _, b := range buf {
		if b != 0x00 && b != 0x20 {
			blank = false
			break
		}
	}
	if blank {
		return nil, io.EOF
	}

	leader := &Leader{}

	// Parse record length (positions 0-4, 5 ASCII digits)
	recordLength, err := parseASCIIInt(buf[0:5])
	if err != nil {
		return nil, NewParseError(startOffset, "record length", err)
	}
	leader.RecordLength = recordLength

	// A record length of 0 means this isn't a real record: some S-57 producers
	// pad the file after the last record with spaces/zeros (which parse to 0).
	// Signal end-of-records so the data-record loop stops cleanly instead of
	// failing leader validation. (A genuine empty file errors in parseDDR.)
	if recordLength == 0 {
		return nil, io.EOF
	}

	// Parse interchange level (position 5, 1 byte)
	leader.InterchangeLevel = buf[5]

	// Parse leader identifier (position 6, 1 byte)
	// 'L' for DDR, 'D' for DR
	leader.LeaderIdentifier = buf[6]

	// Parse inline code extension (position 7, 1 byte)
	leader.InlineCodeExtension = buf[7]

	// Parse version number (position 8, 1 byte)
	leader.VersionNumber = buf[8]

	// Parse application indicator (position 9, 1 byte)
	leader.ApplicationIndicator = buf[9]

	// Parse field control length (positions 10-11, 2 ASCII digits)
	fieldControlLength, err := parseASCIIInt(buf[10:12])
	if err != nil {
		return nil, NewParseError(startOffset+10, "field control length", err)
	}
	leader.FieldControlLength = fieldControlLength

	// Parse field area start (positions 12-16, 5 ASCII digits)
	fieldAreaStart, err := parseASCIIInt(buf[12:17])
	if err != nil {
		return nil, NewParseError(startOffset+12, "field area start", err)
	}
	leader.FieldAreaStart = fieldAreaStart

	// Parse extended character set (positions 17-19, 3 bytes)
	copy(leader.ExtendedCharSet[:], buf[17:20])

	// Parse size of field length (position 20, 1 ASCII digit)
	sizeOfFieldLength, err := parseASCIIInt(buf[20:21])
	if err != nil {
		return nil, NewParseError(startOffset+20, "size of field length", err)
	}
	leader.SizeOfFieldLength = sizeOfFieldLength

	// Parse size of field position (position 21, 1 ASCII digit)
	sizeOfFieldPosition, err := parseASCIIInt(buf[21:22])
	if err != nil {
		return nil, NewParseError(startOffset+21, "size of field position", err)
	}
	leader.SizeOfFieldPosition = sizeOfFieldPosition

	// Parse reserved (position 22, 1 byte)
	leader.Reserved = buf[22]

	// Parse size of field tag (position 23, 1 ASCII digit)
	sizeOfFieldTag, err := parseASCIIInt(buf[23:24])
	if err != nil {
		return nil, NewParseError(startOffset+23, "size of field tag", err)
	}
	leader.SizeOfFieldTag = sizeOfFieldTag

	// Validate leader
	if err := validateLeader(leader); err != nil {
		return nil, NewParseError(startOffset, "leader validation", err)
	}

	return leader, nil
}

// parseASCIIInt parses ASCII digits to an integer
// ISO 8211 uses ASCII digits for numeric fields (spaces represent zeros)
// Per IHO S-57 Part 3 Annex A Table A.1/A.3: fields use ASCII spaces, not null bytes
func parseASCIIInt(buf []byte) (int, error) {
	// Check for null bytes - indicates corrupted or non-ISO 8211 file
	for _, b := range buf {
		if b == 0 {
			return 0, fmt.Errorf("invalid ASCII integer %q: contains null bytes (file corrupted or not ISO 8211 format)", buf)
		}
	}

	// Convert to string
	s := string(buf)

	// Check if all spaces - treat as zero per spec
	allSpaces := true
	for _, c := range s {
		if c != ' ' {
			allSpaces = false
			break
		}
	}
	if allSpaces {
		return 0, nil
	}

	// Parse the integer
	val, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("invalid ASCII integer %q: %w", s, err)
	}
	return val, nil
}

// validateLeader validates the leader structure
func validateLeader(leader *Leader) error {
	// Record length must be positive and at least 24 bytes (leader size)
	if leader.RecordLength < 24 {
		return fmt.Errorf("record length must be >= 24, got %d", leader.RecordLength)
	}

	// Leader identifier must be 'L' (DDR) or 'D' (DR)
	if leader.LeaderIdentifier != 'L' && leader.LeaderIdentifier != 'D' {
		return fmt.Errorf("leader identifier must be 'L' or 'D', got %c", leader.LeaderIdentifier)
	}

	// Field area start must be at least after leader (24 bytes)
	if leader.FieldAreaStart < 24 {
		return fmt.Errorf("field area start must be >= 24, got %d", leader.FieldAreaStart)
	}

	// Size fields must be reasonable (typically 1-9)
	if leader.SizeOfFieldLength < 1 || leader.SizeOfFieldLength > 9 {
		return fmt.Errorf("size of field length must be 1-9, got %d", leader.SizeOfFieldLength)
	}
	if leader.SizeOfFieldPosition < 1 || leader.SizeOfFieldPosition > 9 {
		return fmt.Errorf("size of field position must be 1-9, got %d", leader.SizeOfFieldPosition)
	}
	if leader.SizeOfFieldTag < 1 || leader.SizeOfFieldTag > 9 {
		return fmt.Errorf("size of field tag must be 1-9, got %d", leader.SizeOfFieldTag)
	}

	return nil
}
