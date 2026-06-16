package s52

import (
	"testing"
)

func TestParseATTCFields(t *testing.T) {
	// Simulate the ATTC record from LU00830
	// ATTC   16CATLMK1<US>CONVIS1<US>
	data := []byte("CATLMK1\x1fCONVIS1\x1f")

	p := &Parser{}
	fields := p.parseFields(data)

	t.Logf("Number of fields: %d", len(fields))
	for i, field := range fields {
		t.Logf("Field[%d]: %q", i, field)
	}

	// Expected: 2 fields
	if len(fields) != 2 {
		t.Errorf("Expected 2 fields, got %d", len(fields))
	}

	if len(fields) >= 1 && fields[0] != "CATLMK1" {
		t.Errorf("Expected fields[0]='CATLMK1', got %q", fields[0])
	}

	if len(fields) >= 2 && fields[1] != "CONVIS1" {
		t.Errorf("Expected fields[1]='CONVIS1', got %q", fields[1])
	}
}

func TestParseATTCString(t *testing.T) {
	p := &Parser{}

	// Test single attribute
	conditions := p.parseATTCString("CATLMK1")
	t.Logf("Single attr result: %+v", conditions)
	if len(conditions) != 1 {
		t.Errorf("Expected 1 condition, got %d", len(conditions))
	}
	if len(conditions) > 0 && (conditions[0].Attribute != "CATLMK" || conditions[0].Value != "1") {
		t.Errorf("Unexpected condition: %+v", conditions[0])
	}

	// Test semicolon-separated attributes
	conditions = p.parseATTCString("BOYSHP2;COLOUR3,1")
	t.Logf("Semicolon separated result: %+v", conditions)
	if len(conditions) != 2 {
		t.Errorf("Expected 2 conditions, got %d", len(conditions))
	}
}
