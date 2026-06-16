package iso8211

import (
	"testing"
)

// TestLeaderStructure tests leader parsing for both DDR and DR
func TestLeaderStructure(t *testing.T) {
	// Given: ISO 8211 file
	filepath := "../../testdata/US4MD81M.000"

	parser, err := Open(filepath)
	if err != nil {
		t.Fatalf("Failed to open file: %v", err)
	}
	defer parser.Close()

	// When: Parsing file
	file, err := parser.Parse()
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	// Then: DDR leader has correct structure
	if file.DDR == nil || file.DDR.Leader == nil {
		t.Fatal("Expected non-nil DDR and leader")
	}
	if file.DDR.Leader.LeaderIdentifier != 'L' {
		t.Errorf("Expected DDR leader identifier 'L', got %c", file.DDR.Leader.LeaderIdentifier)
	}
	if file.DDR.Leader.RecordLength <= 24 {
		t.Errorf("Expected DDR record length > 24, got %d", file.DDR.Leader.RecordLength)
	}
	if file.DDR.Leader.FieldAreaStart <= 24 {
		t.Errorf("Expected field area start > 24, got %d", file.DDR.Leader.FieldAreaStart)
	}

	// And: All data record leaders have correct structure
	for i, record := range file.Records {
		if record.Leader == nil {
			t.Errorf("Record %d has nil leader", i)
			continue
		}
		if record.Leader.LeaderIdentifier != 'D' {
			t.Errorf("Record %d: expected leader identifier 'D', got %c", i, record.Leader.LeaderIdentifier)
		}
		if record.Leader.RecordLength <= 24 {
			t.Errorf("Record %d: expected record length > 24, got %d", i, record.Leader.RecordLength)
		}
	}
}

// TestLeaderValidation tests leader validation edge cases
func TestLeaderValidation(t *testing.T) {
	tests := []struct {
		name        string
		filepath    string
		expectError bool
	}{
		{
			name:        "Valid leader",
			filepath:    "../../testdata/US4MD81M.000",
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parser, err := Open(tt.filepath)
			if err != nil {
				if !tt.expectError {
					t.Fatalf("Unexpected error opening file: %v", err)
				}
				return
			}
			defer parser.Close()

			_, err = parser.Parse()
			if tt.expectError && err == nil {
				t.Error("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}
		})
	}
}

// TestCloseNilFile tests closing a parser with nil file
func TestCloseNilFile(t *testing.T) {
	parser, err := Open("../../testdata/US4MD81M.000")
	if err != nil {
		t.Fatalf("Failed to open file: %v", err)
	}

	// Close without parsing
	err = parser.Close()
	if err != nil {
		t.Errorf("Close should not error: %v", err)
	}

	// Close again should be safe
	err = parser.Close()
	if err != nil {
		t.Errorf("Second close should not error: %v", err)
	}
}
