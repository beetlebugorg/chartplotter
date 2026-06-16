package iso8211

import (
	"testing"
)

// TestDirectoryEntries tests directory entry parsing
func TestDirectoryEntries(t *testing.T) {
	// Given: ISO 8211 file
	filepath := "../../testdata/US4MD81M.000"

	parser, err := Open(filepath)
	if err != nil {
		t.Fatalf("Failed to open file: %v", err)
	}
	defer parser.Close()

	file, err := parser.Parse()
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	// When: Examining DDR directory entries
	// Then: All entries have valid fields
	if len(file.DDR.Directory) == 0 {
		t.Error("Expected non-empty DDR directory")
	}
	for i, entry := range file.DDR.Directory {
		if entry.Tag == "" {
			t.Errorf("DDR directory entry %d: tag must not be empty", i)
		}
		if entry.Length <= 0 {
			t.Errorf("DDR directory entry %d: length must be positive, got %d", i, entry.Length)
		}
		if entry.Position < 0 {
			t.Errorf("DDR directory entry %d: position must be non-negative, got %d", i, entry.Position)
		}
	}

	// And: Data record directory entries are valid
	for i, record := range file.Records {
		if len(record.Directory) == 0 {
			t.Errorf("Record %d has no directory entries", i)
		}
		for j, entry := range record.Directory {
			if entry.Tag == "" {
				t.Errorf("Record %d directory entry %d: tag must not be empty", i, j)
			}
			if entry.Length <= 0 {
				t.Errorf("Record %d directory entry %d: length must be positive, got %d", i, j, entry.Length)
			}
			if entry.Position < 0 {
				t.Errorf("Record %d directory entry %d: position must be non-negative, got %d", i, j, entry.Position)
			}
		}
	}

	// And: Data record fields match directory tags
	for i, record := range file.Records {
		for _, dirEntry := range record.Directory {
			if _, exists := record.Fields[dirEntry.Tag]; !exists {
				t.Errorf("Record %d: directory entry %s not found in fields", i, dirEntry.Tag)
			}
		}

		// And: All fields have corresponding directory entries
		for tag := range record.Fields {
			found := false
			for _, dirEntry := range record.Directory {
				if dirEntry.Tag == tag {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("Record %d: field %s has no directory entry", i, tag)
			}
		}
	}
}
