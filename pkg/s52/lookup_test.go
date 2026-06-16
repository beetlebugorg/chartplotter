package s52

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestLookupFeature_NoEntries tests lookup with empty lookup tables
func TestLookupFeature_NoEntries(t *testing.T) {
	lib := &Library{
		lookupTables: []*LookupTable{},
	}

	result := lib.LookupFeature("DEPARE", "A", nil, nil)
	if result != nil {
		t.Errorf("Expected nil for non-existent object class, got %v", result)
	}
}

// TestLookupFeature_SingleEntry tests lookup with single entry
func TestLookupFeature_SingleEntry(t *testing.T) {
	lib := &Library{
		lookupTables: []*LookupTable{
			{
				ObjectClass: "ACHARE",
				Attributes:  []AttributeCondition{},
				Instructions: []RawInstruction{
					{RawCommand: "SY(ACHARE51)"},
				},
			},
		},
	}

	result := lib.LookupFeature("ACHARE", "P", nil, nil)
	if result == nil || len(result.Instructions) == 0 {
		t.Errorf("Expected instruction, got %v", result)
	} else if result.Instructions[0].String() != "SY(ACHARE51)" {
		t.Errorf("Expected 'SY(ACHARE51)', got '%s'", result.Instructions[0].String())
	}
}

// TestLookupFeature_MultipleEntriesWithFailsafe tests failsafe fallback
func TestLookupFeature_MultipleEntriesWithFailsafe(t *testing.T) {
	lib := &Library{
		lookupTables: []*LookupTable{
			{
				ObjectClass: "DEPARE",
				Attributes: []AttributeCondition{
					{Attribute: "DRVAL1", Value: "10"},
				},
				Instructions: []RawInstruction{
					{RawCommand: "AC(DEPVS)"},
				},
			},
			{
				ObjectClass: "DEPARE",
				Attributes:  []AttributeCondition{}, // Failsafe
				Instructions: []RawInstruction{
					{RawCommand: "AC(DEPIT)"},
				},
			},
		},
	}

	// No attributes - should use failsafe
	result := lib.LookupFeature("DEPARE", "A", nil, nil)
	if result == nil || len(result.Instructions) == 0 {
		t.Errorf("Expected instruction, got %v", result)
	} else if result.Instructions[0].String() != "AC(DEPIT)" {
		t.Errorf("Expected failsafe 'AC(DEPIT)', got '%s'", result.Instructions[0].String())
	}

	// Wrong attributes - should use failsafe
	result = lib.LookupFeature("DEPARE", "A", map[string]interface{}{
		"DRVAL1": "20",
	}, nil)
	if result == nil || len(result.Instructions) == 0 {
		t.Errorf("Expected instruction, got %v", result)
	} else if result.Instructions[0].String() != "AC(DEPIT)" {
		t.Errorf("Expected failsafe 'AC(DEPIT)', got '%s'", result.Instructions[0].String())
	}

	// Matching attributes - should use specific entry
	result = lib.LookupFeature("DEPARE", "A", map[string]interface{}{
		"DRVAL1": "10",
	}, nil)
	if result == nil || len(result.Instructions) == 0 {
		t.Errorf("Expected instruction, got %v", result)
	} else if result.Instructions[0].String() != "AC(DEPVS)" {
		t.Errorf("Expected specific 'AC(DEPVS)', got '%s'", result.Instructions[0].String())
	}
}

// TestMatchesAttribute tests attribute matching logic
func TestMatchesAttribute(t *testing.T) {
	lib := &Library{}

	tests := []struct {
		name      string
		condition AttributeCondition
		attrs     map[string]interface{}
		expected  bool
	}{
		{
			name:      "Exact match",
			condition: AttributeCondition{Attribute: "CATACH", Value: "5"},
			attrs:     map[string]interface{}{"CATACH": "5"},
			expected:  true,
		},
		{
			name:      "Exact mismatch",
			condition: AttributeCondition{Attribute: "CATACH", Value: "5"},
			attrs:     map[string]interface{}{"CATACH": "3"},
			expected:  false,
		},
		{
			name:      "Any value (empty condition)",
			condition: AttributeCondition{Attribute: "CATACH", Value: ""},
			attrs:     map[string]interface{}{"CATACH": "5"},
			expected:  true,
		},
		{
			name:      "Any value but missing",
			condition: AttributeCondition{Attribute: "CATACH", Value: ""},
			attrs:     map[string]interface{}{},
			expected:  false,
		},
		{
			name:      "Placeholder for unknown",
			condition: AttributeCondition{Attribute: "CATACH", Value: "?"},
			attrs:     map[string]interface{}{},
			expected:  true,
		},
		{
			name:      "Placeholder but has value",
			condition: AttributeCondition{Attribute: "CATACH", Value: "?"},
			attrs:     map[string]interface{}{"CATACH": "5"},
			expected:  false,
		},
		{
			name:      "Multiple values - all match",
			condition: AttributeCondition{Attribute: "COLOUR", Value: "3,1"},
			attrs:     map[string]interface{}{"COLOUR": "3,1"},
			expected:  true,
		},
		{
			name:      "Multiple values - prefix match",
			condition: AttributeCondition{Attribute: "COLOUR", Value: "3,1"},
			attrs:     map[string]interface{}{"COLOUR": "3,1,4"},
			expected:  true,
		},
		{
			name:      "Multiple values - wrong order",
			condition: AttributeCondition{Attribute: "COLOUR", Value: "3,1"},
			attrs:     map[string]interface{}{"COLOUR": "1,3"},
			expected:  false,
		},
		{
			name:      "Multiple values - not enough",
			condition: AttributeCondition{Attribute: "COLOUR", Value: "3,1"},
			attrs:     map[string]interface{}{"COLOUR": "3"},
			expected:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := lib.matchesAttribute(tt.condition, tt.attrs)
			if result != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}
}

// TestListObjectClasses tests object class listing
func TestListObjectClasses(t *testing.T) {
	lib := &Library{
		lookupTables: []*LookupTable{
			{ObjectClass: "ACHARE"},
			{ObjectClass: "ACHARE"},
			{ObjectClass: "DEPARE"},
			{ObjectClass: "BOYLAT"},
		},
	}

	classes := lib.ListObjectClasses()

	if len(classes) != 3 {
		t.Errorf("Expected 3 unique classes, got %d", len(classes))
	}

	// Check all classes are present (order doesn't matter)
	found := make(map[string]bool)
	for _, class := range classes {
		found[class] = true
	}

	expected := []string{"ACHARE", "DEPARE", "BOYLAT"}
	for _, exp := range expected {
		if !found[exp] {
			t.Errorf("Expected to find class %s", exp)
		}
	}
}

// TestGetLookupEntry tests full lookup entry retrieval
func TestGetLookupEntry(t *testing.T) {
	lib := &Library{
		lookupTables: []*LookupTable{
			{
				ObjectClass:     "ACHARE",
				Attributes:      []AttributeCondition{{Attribute: "CATACH", Value: "5"}},
				DisplayPriority: 8,
				RadarOverlay:    "O",
				DisplayCategory: "Standard",
				ViewingGroup:    "",
				Instructions: []RawInstruction{
					{RawCommand: "SY(ACHARE51);AC(CHBRN)"},
				},
			},
		},
	}

	entry := lib.GetLookupEntry("ACHARE", map[string]interface{}{"CATACH": "5"})

	if entry == nil {
		t.Fatal("Expected entry, got nil")
	}

	if entry.ObjectClass != "ACHARE" {
		t.Errorf("Expected ObjectClass 'ACHARE', got '%s'", entry.ObjectClass)
	}

	if entry.Instruction != "SY(ACHARE51);AC(CHBRN)" {
		t.Errorf("Expected instruction 'SY(ACHARE51);AC(CHBRN)', got '%s'", entry.Instruction)
	}

	if entry.DisplayPriority != 8 {
		t.Errorf("Expected DisplayPriority 8, got %d", entry.DisplayPriority)
	}

	if entry.RadarPriority != "O" {
		t.Errorf("Expected RadarPriority 'O', got '%s'", entry.RadarPriority)
	}

	if len(entry.Attributes) != 1 {
		t.Errorf("Expected 1 attribute, got %d", len(entry.Attributes))
	}
}

// TestGetLookupEntry_NotFound tests missing lookup entry
func TestGetLookupEntry_NotFound(t *testing.T) {
	lib := &Library{
		lookupTables: []*LookupTable{},
	}

	entry := lib.GetLookupEntry("NONEXISTENT", nil)
	if entry != nil {
		t.Error("Expected nil for non-existent object class")
	}
}

// TestLibrary_LookupFeatureParsed - TODO: implement when createTestLibrary helper is added
// func TestLibrary_LookupFeatureParsed(t *testing.T) {
// 	lib := createTestLibrary()
//
// 	tests := []struct {
// 		name       string
// 		objClass   string
// 		attributes map[string]interface{}
// 		wantType   string
// 		wantCount  int
// 	}{
// 		{
// 			name:      "text instruction",
// 			objClass:  "BUAARE",
// 			attributes: map[string]interface{}{},
// 			wantType:  "*v1.TXInstruction",
// 			wantCount: 3, // Will be TX + something else
// 		},
// 	}
//
// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			result := lib.LookupFeature(tt.objClass, tt.attributes)
// 			if result == nil {
// 				t.Errorf("Expected instructions, got nil")
// 				return
// 			}
//
// 			if tt.wantCount > 0 {
// 				assert.GreaterOrEqual(t, len(result.Instructions), 1)
//
// 				// Check if we got at least one of expected type
// 				foundExpectedType := false
// 				for _, instr := range result.Instructions {
// 					typeName := fmt.Sprintf("%T", instr)
// 					if strings.Contains(typeName, "TXInstruction") {
// 						foundExpectedType = true
// 						break
// 					}
// 				}
// 				if tt.wantType != "" {
// 					assert.True(t, foundExpectedType, "expected to find type %s", tt.wantType)
// 				}
// 			}
// 		})
// 	}
// }

func TestSplitInstructions(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{
			name:  "single instruction",
			input: "SY(ACHARE51)",
			want:  []string{"SY(ACHARE51)"},
		},
		{
			name:  "compound instruction",
			input: "SY(ACHARE51);AC(CHBRN)",
			want:  []string{"SY(ACHARE51)", "AC(CHBRN)"},
		},
		{
			name:  "TX with text",
			input: "TX('text',1,2,3,'15110',0,0,CHBLK,21);AC(CHBRN)",
			want:  []string{"TX('text',1,2,3,'15110',0,0,CHBLK,21)", "AC(CHBRN)"},
		},
		{
			name:  "semicolon in quotes",
			input: "TX('foo;bar',1,2,3,'15110',0,0,CHBLK,21)",
			want:  []string{"TX('foo;bar',1,2,3,'15110',0,0,CHBLK,21)"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitInstructions(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}
