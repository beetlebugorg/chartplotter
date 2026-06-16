package s57

import (
	"testing"
)

func TestGetAttributeSchema(t *testing.T) {
	tests := []struct {
		name        string
		objectClass string
		wantNil     bool
		checkAttrs  []string
	}{
		{
			name:        "DEPARE - depth area",
			objectClass: "DEPARE",
			wantNil:     false,
			checkAttrs:  []string{"DRVAL1", "DRVAL2"},
		},
		{
			name:        "LIGHTS - navigation lights",
			objectClass: "LIGHTS",
			wantNil:     false,
			checkAttrs:  []string{"CATLIT", "COLOUR"},
		},
		{
			name:        "Unknown object class",
			objectClass: "UNKNOWN",
			wantNil:     true,
			checkAttrs:  []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			schema := GetAttributeSchema(tt.objectClass)

			if tt.wantNil {
				if schema != nil {
					t.Errorf("Expected nil schema for %s", tt.objectClass)
				}
				return
			}

			if schema == nil {
				t.Fatalf("Expected schema for %s, got nil", tt.objectClass)
			}

			// Check essential attributes are present
			essentialMap := make(map[string]bool)
			for _, attr := range schema.Essential {
				essentialMap[attr] = true
			}

			for _, checkAttr := range tt.checkAttrs {
				if !essentialMap[checkAttr] {
					t.Errorf("Expected %s in essential attributes, not found", checkAttr)
				}
			}

			t.Logf("%s schema: %d essential, %d conditional, %d array",
				tt.objectClass,
				len(schema.Essential),
				len(schema.Conditional),
				len(schema.ArrayAttributes))
		})
	}
}

func TestGetEssentialAttributes(t *testing.T) {
	tests := []struct {
		name         string
		objectClass  string
		wantContains []string
	}{
		{
			name:         "DEPARE",
			objectClass:  "DEPARE",
			wantContains: []string{"DRVAL1", "DRVAL2"},
		},
		{
			name:         "Unknown - returns defaults",
			objectClass:  "UNKNOWN",
			wantContains: []string{"OBJL"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			attrs := GetEssentialAttributes(tt.objectClass)

			if len(attrs) == 0 {
				t.Error("Expected non-empty attribute list")
			}

			attrMap := make(map[string]bool)
			for _, attr := range attrs {
				attrMap[attr] = true
			}

			for _, want := range tt.wantContains {
				if !attrMap[want] {
					t.Errorf("Expected %s in essential attributes", want)
				}
			}
		})
	}
}

func TestIsArrayAttribute(t *testing.T) {
	tests := []struct {
		name          string
		objectClass   string
		attributeName string
		want          bool
	}{
		{
			name:          "SOUNDG DEPTHS - is array",
			objectClass:   "SOUNDG",
			attributeName: "DEPTHS",
			want:          true,
		},
		{
			name:          "BOYLAT COLOUR - is array",
			objectClass:   "BOYLAT",
			attributeName: "COLOUR",
			want:          true,
		},
		{
			name:          "DEPARE DRVAL1 - not array",
			objectClass:   "DEPARE",
			attributeName: "DRVAL1",
			want:          false,
		},
		{
			name:          "Unknown object class",
			objectClass:   "UNKNOWN",
			attributeName: "ANYTHING",
			want:          false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsArrayAttribute(tt.objectClass, tt.attributeName)
			if got != tt.want {
				t.Errorf("IsArrayAttribute(%s, %s) = %v, want %v",
					tt.objectClass, tt.attributeName, got, tt.want)
			}
		})
	}
}
