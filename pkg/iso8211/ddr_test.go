package iso8211

import (
	"testing"
)

// TestDDRFieldControls tests DDR field control extraction
func TestDDRFieldControls(t *testing.T) {
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

	// Then: DDR contains field controls
	if file.DDR == nil {
		t.Fatal("Expected non-nil DDR")
	}
	if len(file.DDR.FieldControls) == 0 {
		t.Error("Expected non-empty field controls")
	}

	// And: Field control field (tag "0001") exists
	fc, exists := file.DDR.FieldControls["0001"]
	if !exists {
		t.Error("Expected field control with tag '0001'")
	}
	if fc == nil {
		t.Error("Expected non-nil field control for tag '0001'")
	}

	// And: Common field controls exist (field control field and data fields)
	expectedTags := []string{"0001", "DSID", "DSSI", "DSPM", "VRID"}
	for _, tag := range expectedTags {
		if _, exists := file.DDR.FieldControls[tag]; !exists {
			t.Errorf("Expected field control for tag %s", tag)
		}
	}

	// And: Field controls have proper structure
	for tag, fc := range file.DDR.FieldControls {
		if fc.Tag != tag {
			t.Errorf("Field control tag mismatch: expected %s, got %s", tag, fc.Tag)
		}
		if fc.FieldName == "" {
			t.Errorf("Field control %s has empty field name", tag)
		}
	}
}
