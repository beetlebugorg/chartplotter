package iso8211

import (
	"bytes"
	"os"
	"testing"

	"github.com/spf13/afero"
)

// TestInvalidFilePath tests that invalid file paths return an error
func TestInvalidFilePath(t *testing.T) {
	// Given: A non-existent file path
	parser, err := Open("/nonexistent/path/file.000")

	// Then: Error returned on creation
	if err == nil {
		t.Error("Expected error for non-existent file path, got nil")
	}
	if parser != nil {
		t.Error("Expected nil parser for non-existent file path")
	}
}

// TestOpenFS tests opening a file with custom afero filesystem
func TestOpenFS(t *testing.T) {
	// Given: File data in an in-memory filesystem
	filepath := "chart.000"
	data, err := os.ReadFile("../../testdata/US4MD81M.000")
	if err != nil {
		t.Fatalf("Failed to read test file: %v", err)
	}

	fs := afero.NewMemMapFs()
	err = afero.WriteFile(fs, filepath, data, 0644)
	if err != nil {
		t.Fatalf("Failed to write to memory fs: %v", err)
	}

	// When: Opening with OpenFS
	parser, err := OpenFS(fs, filepath)
	if err != nil {
		t.Fatalf("Failed to open with OpenFS: %v", err)
	}
	defer parser.Close()

	// Then: File parses successfully
	file, err := parser.Parse()
	if err != nil {
		t.Errorf("Parse failed: %v", err)
	}
	if file == nil {
		t.Error("Expected non-nil file")
		return
	}
	if file.DDR == nil {
		t.Error("Expected non-nil DDR")
	}
	if len(file.Records) == 0 {
		t.Error("Expected at least one record")
	}
}

// TestNewParserFromBytes tests creating a parser from byte slice
func TestNewParserFromBytes(t *testing.T) {
	// Given: Read an ISO 8211 file into memory
	filepath := "../../testdata/US4MD81M.000"
	data, err := os.ReadFile(filepath)
	if err != nil {
		t.Fatalf("Failed to read test file: %v", err)
	}

	// When: Creating parser from bytes
	parser := NewParser(bytes.NewReader(data))

	// Then: Parser created successfully
	if parser == nil {
		t.Error("Expected non-nil parser")
	}
	if parser.reader == nil {
		t.Error("Expected non-nil parser.reader")
	}
	if parser.closer != nil {
		t.Error("Expected nil closer for byte parser")
	}
}

// TestNewReaderFromBytesEmpty tests creating a parser from empty byte slice
func TestNewReaderFromBytesEmpty(t *testing.T) {
	// Given: Empty byte slice
	data := []byte{}

	// When: Creating parser from empty bytes
	parser := NewParser(bytes.NewReader(data))

	// Then: Parser created (parsing will fail later)
	if parser == nil {
		t.Error("Expected non-nil parser even for empty data")
	}
}

// TestParseFromBytes tests parsing ISO 8211 data from byte slice
func TestParseFromBytes(t *testing.T) {
	// Given: ISO 8211 file data in memory
	filepath := "../../testdata/US4MD81M.000"
	data, err := os.ReadFile(filepath)
	if err != nil {
		t.Fatalf("Failed to read test file: %v", err)
	}

	parser := NewParser(bytes.NewReader(data))
	defer parser.Close()

	// When: Parsing from bytes
	file, err := parser.Parse()

	// Then: File parses successfully
	if err != nil {
		t.Errorf("Parse from bytes failed: %v", err)
	}
	if file == nil {
		t.Error("Expected non-nil file")
		return
	}
	if file.DDR == nil {
		t.Error("Expected non-nil DDR")
	}
	if len(file.Records) == 0 {
		t.Error("Expected at least one record")
	}
}

// TestParseFromBytesMatchesFile tests that byte and file parsing produce identical results
func TestParseFromBytesMatchesFile(t *testing.T) {
	// Given: Same ISO 8211 data from file and bytes
	filepath := "../../testdata/US4MD81M.000"

	// Parse from file
	fileParser, err := Open(filepath)
	if err != nil {
		t.Fatalf("Failed to open file: %v", err)
	}
	defer fileParser.Close()

	fileResult, err := fileParser.Parse()
	if err != nil {
		t.Fatalf("Failed to parse from file: %v", err)
	}

	// Parse from bytes
	data, err := os.ReadFile(filepath)
	if err != nil {
		t.Fatalf("Failed to read test file: %v", err)
	}

	byteParser := NewParser(bytes.NewReader(data))
	defer byteParser.Close()

	byteResult, err := byteParser.Parse()
	if err != nil {
		t.Fatalf("Failed to parse from bytes: %v", err)
	}

	// Then: Results should match
	if len(fileResult.Records) != len(byteResult.Records) {
		t.Errorf("Record count mismatch: file=%d, bytes=%d",
			len(fileResult.Records), len(byteResult.Records))
	}

	// Compare DDR leaders
	if fileResult.DDR.Leader.RecordLength != byteResult.DDR.Leader.RecordLength {
		t.Error("DDR RecordLength mismatch between file and byte parsing")
	}
	if fileResult.DDR.Leader.LeaderIdentifier != byteResult.DDR.Leader.LeaderIdentifier {
		t.Error("DDR LeaderIdentifier mismatch between file and byte parsing")
	}
}

// TestByteParserClose tests that closing byte parser doesn't error
func TestByteParserClose(t *testing.T) {
	// Given: Byte parser
	data := []byte{1, 2, 3}
	parser := NewParser(bytes.NewReader(data))

	// When: Closing byte parser
	err := parser.Close()

	// Then: No error (idempotent no-op for byte parsers)
	if err != nil {
		t.Errorf("Close should not error for byte parser: %v", err)
	}

	// And: Multiple closes are safe
	err = parser.Close()
	if err != nil {
		t.Errorf("Second close should not error: %v", err)
	}
}

// TestParseISO8211File tests parsing an ISO 8211 file
func TestParseISO8211File(t *testing.T) {
	// Given: ISO 8211 file
	filepath := "../../testdata/US4MD81M.000"

	parser, err := Open(filepath)
	if err != nil {
		t.Fatalf("Failed to open file: %v", err)
	}
	defer parser.Close()

	// When: Parsing file
	file, err := parser.Parse()

	// Then: File parses successfully
	if err != nil {
		t.Errorf("Parse failed: %v", err)
	}
	if file == nil {
		t.Error("Expected non-nil file")
		return
	}
	if file.DDR == nil {
		t.Error("Expected non-nil DDR")
	}
	if len(file.Records) == 0 {
		t.Error("Expected at least one record")
	}
}
