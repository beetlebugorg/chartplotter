package s52

import (
	"testing"
)

func TestParseSCRF(t *testing.T) {
	tests := []struct {
		name     string
		scrf     string
		expected map[rune]string
	}{
		{
			name: "Single role",
			scrf: "CCHBLK",
			expected: map[rune]string{
				'C': "CHBLK",
			},
		},
		{
			name: "Two roles",
			scrf: "CCHBLKJCHMGF",
			expected: map[rune]string{
				'C': "CHBLK",
				'J': "CHMGF",
			},
		},
		{
			name: "Multiple roles",
			scrf: "ACHBLKBCHMGFCCHGRD",
			expected: map[rune]string{
				'A': "CHBLK",
				'B': "CHMGF",
				'C': "CHGRD",
			},
		},
		{
			name:     "Empty string",
			scrf:     "",
			expected: map[rune]string{},
		},
		{
			name:     "Incomplete entry (too short)",
			scrf:     "ACHB",
			expected: map[rune]string{},
		},
		{
			name: "Exact length (6 chars)",
			scrf: "ACHBLK",
			expected: map[rune]string{
				'A': "CHBLK",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseSCRF(tt.scrf)

			// Check if maps are equal
			if len(result) != len(tt.expected) {
				t.Errorf("ParseSCRF() returned %d entries, expected %d", len(result), len(tt.expected))
				return
			}

			for role, colorToken := range tt.expected {
				if result[role] != colorToken {
					t.Errorf("ParseSCRF() for role %c = %s, expected %s", role, result[role], colorToken)
				}
			}
		})
	}
}

// TestParseSCRF_RealExamples tests with actual SCRF strings from S-52 DAI file
func TestParseSCRF_RealExamples(t *testing.T) {
	// Load the library to get real SCRF strings
	lib, err := LoadLibrary("../../testdata/PresLib_e4.0.0.dai")
	if err != nil {
		t.Skipf("Skipping real examples test: %v", err)
		return
	}

	// Test with a known symbol
	symbol, err := lib.GetSymbol("ACHARE51")
	if err != nil {
		t.Skipf("Symbol ACHARE51 not found: %v", err)
		return
	}

	// Parse the SCRF
	colorMap := ParseSCRF(symbol.ColorRef)

	// Verify it returns a map (exact contents depend on the symbol)
	if len(colorMap) == 0 && symbol.ColorRef != "" {
		t.Errorf("ParseSCRF returned empty map for non-empty SCRF: %s", symbol.ColorRef)
	}

	// Log what we got for debugging
	t.Logf("Symbol %s SCRF: %s", symbol.ID, symbol.ColorRef)
	t.Logf("Parsed color map: %+v", colorMap)
}
