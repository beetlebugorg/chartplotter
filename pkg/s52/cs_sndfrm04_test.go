package s52

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSNDFRM04_BasicDepthFormatting tests the 6 depth formatting algorithms
func TestSNDFRM04_BasicDepthFormatting(t *testing.T) {
	lib := &Library{}
	mariner := &MarinerSettings{
		SafetyDepth: 28.0,
	}

	tests := []struct {
		name        string
		depth       float64
		wantPrefix  string
		wantSymbols []string
	}{
		{
			name:        "Algorithm 1: depth 3.6m shallow with fraction",
			depth:       3.6,
			wantPrefix:  "SOUNDS",                         // <= safety depth
			wantSymbols: []string{"SOUNDS13", "SOUNDS56"}, // "3" and ".6"
		},
		{
			name:        "Algorithm 1: depth 9.9m shallow",
			depth:       9.9,
			wantPrefix:  "SOUNDS",
			wantSymbols: []string{"SOUNDS19", "SOUNDS59"}, // "9" and ".9"
		},
		{
			name:        "Algorithm 1: depth 5.0m no fraction",
			depth:       5.0,
			wantPrefix:  "SOUNDS",
			wantSymbols: []string{"SOUNDS15"}, // "5" only, no fraction
		},
		{
			name:        "Algorithm 2: depth 26.7m shallow with fraction",
			depth:       26.7,
			wantPrefix:  "SOUNDS",
			wantSymbols: []string{"SOUNDS22", "SOUNDS16", "SOUNDS57"}, // "2", "6", ".7"
		},
		{
			name:        "Algorithm 2: depth 10.5m",
			depth:       10.5,
			wantPrefix:  "SOUNDS",
			wantSymbols: []string{"SOUNDS21", "SOUNDS10", "SOUNDS55"}, // "1", "0", ".5"
		},
		{
			name:        "Algorithm 3: depth 31m (boundary, no fraction allowed)",
			depth:       31.0,
			wantPrefix:  "SOUNDG",                         // > safety depth
			wantSymbols: []string{"SOUNDG13", "SOUNDG01"}, // "3", "1"
		},
		{
			name:        "Algorithm 3: depth 47m",
			depth:       47.0,
			wantPrefix:  "SOUNDG",
			wantSymbols: []string{"SOUNDG14", "SOUNDG07"}, // "4", "7"
		},
		{
			name:        "Algorithm 3: depth 99m",
			depth:       99.0,
			wantPrefix:  "SOUNDG",
			wantSymbols: []string{"SOUNDG19", "SOUNDG09"}, // "9", "9"
		},
		{
			name:        "Algorithm 4: depth 234m",
			depth:       234.0,
			wantPrefix:  "SOUNDG",
			wantSymbols: []string{"SOUNDG22", "SOUNDG13", "SOUNDG04"}, // "2", "3", "4"
		},
		{
			name:        "Algorithm 4: depth 100m (boundary)",
			depth:       100.0,
			wantPrefix:  "SOUNDG",
			wantSymbols: []string{"SOUNDG21", "SOUNDG10", "SOUNDG00"}, // "1", "0", "0"
		},
		{
			name:        "Algorithm 4: depth 999m",
			depth:       999.0,
			wantPrefix:  "SOUNDG",
			wantSymbols: []string{"SOUNDG29", "SOUNDG19", "SOUNDG09"}, // "9", "9", "9"
		},
		{
			name:        "Algorithm 5: depth 2345m with small last digit",
			depth:       2345.0,
			wantPrefix:  "SOUNDG",
			wantSymbols: []string{"SOUNDG22", "SOUNDG13", "SOUNDG04", "SOUNDG45"}, // "2", "3", "4", small "5"
		},
		{
			name:        "Algorithm 5: depth 1000m (boundary)",
			depth:       1000.0,
			wantPrefix:  "SOUNDG",
			wantSymbols: []string{"SOUNDG21", "SOUNDG10", "SOUNDG00", "SOUNDG40"}, // "1", "0", "0", small "0"
		},
		{
			name:        "Algorithm 6: depth 12345m with small last digit",
			depth:       12345.0,
			wantPrefix:  "SOUNDG",
			wantSymbols: []string{"SOUNDG31", "SOUNDG22", "SOUNDG13", "SOUNDG04", "SOUNDG45"}, // "1", "2", "3", "4", small "5"
		},
		{
			name:        "Algorithm 6: depth 10000m (boundary)",
			depth:       10000.0,
			wantPrefix:  "SOUNDG",
			wantSymbols: []string{"SOUNDG31", "SOUNDG20", "SOUNDG10", "SOUNDG00", "SOUNDG40"}, // "1", "0", "0", "0", small "0"
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			attrs := map[string]interface{}{"DEPTH": tt.depth}
			instructions, err := lib.executeCS("SOUNDG03", NewCSContext(attrs, "", nil, mariner))

			require.NoError(t, err)
			require.Len(t, instructions, len(tt.wantSymbols), "Expected %d symbols", len(tt.wantSymbols))

			// Check that we got SY instructions with correct symbol IDs
			for i, instr := range instructions {
				sy, ok := instr.(*SYInstruction)
				require.True(t, ok, "Expected SY instruction at index %d", i)
				assert.Equal(t, tt.wantSymbols[i], sy.SymbolID, "Symbol mismatch at index %d", i)
			}
		})
	}
}

// TestSNDFRM04_DryingHeight tests negative depths (drying heights)
func TestSNDFRM04_DryingHeight(t *testing.T) {
	lib := &Library{}
	mariner := &MarinerSettings{
		SafetyDepth: 28.0,
	}

	tests := []struct {
		name        string
		depth       float64
		wantSymbols []string
	}{
		{
			name:        "Drying height -1.2m",
			depth:       -1.2,
			wantSymbols: []string{"SOUNDSA1", "SOUNDS11", "SOUNDS52"}, // indicator, "1", ".2"
		},
		{
			name:        "Drying height -0.5m",
			depth:       -0.5,
			wantSymbols: []string{"SOUNDSA1", "SOUNDS10", "SOUNDS55"}, // indicator, "0", ".5"
		},
		{
			name:        "Drying height -3.0m",
			depth:       -3.0,
			wantSymbols: []string{"SOUNDSA1", "SOUNDS13"}, // indicator, "3"
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			attrs := map[string]interface{}{"DEPTH": tt.depth}
			instructions, err := lib.executeCS("SOUNDG03", NewCSContext(attrs, "", nil, mariner))

			require.NoError(t, err)
			require.Len(t, instructions, len(tt.wantSymbols))

			for i, instr := range instructions {
				sy, ok := instr.(*SYInstruction)
				require.True(t, ok)
				assert.Equal(t, tt.wantSymbols[i], sy.SymbolID)
			}
		})
	}
}

// TestSNDFRM04_QualityIndicators tests TECSOU, QUASOU, STATUS, QUAPOS attributes
func TestSNDFRM04_QualityIndicators(t *testing.T) {
	lib := &Library{}
	mariner := &MarinerSettings{
		SafetyDepth: 28.0,
	}

	tests := []struct {
		name             string
		depth            float64
		attrs            map[string]interface{}
		wantHasIndicator string // B1 or C2
	}{
		{
			name:             "TECSOU=4 (found by diver)",
			depth:            5.0,
			attrs:            map[string]interface{}{"DEPTH": 5.0, "TECSOU": 4},
			wantHasIndicator: "SOUNDSB1",
		},
		{
			name:             "TECSOU=6 (swept by wire drag)",
			depth:            10.0,
			attrs:            map[string]interface{}{"DEPTH": 10.0, "TECSOU": 6},
			wantHasIndicator: "SOUNDSB1",
		},
		{
			name:             "QUASOU=3 (unreliable)",
			depth:            5.0,
			attrs:            map[string]interface{}{"DEPTH": 5.0, "QUASOU": 3},
			wantHasIndicator: "SOUNDSC2",
		},
		{
			name:             "QUASOU=5 (unreliable)",
			depth:            5.0,
			attrs:            map[string]interface{}{"DEPTH": 5.0, "QUASOU": 5},
			wantHasIndicator: "SOUNDSC2",
		},
		{
			name:             "STATUS=18 (uncertain)",
			depth:            5.0,
			attrs:            map[string]interface{}{"DEPTH": 5.0, "STATUS": 18},
			wantHasIndicator: "SOUNDSC2",
		},
		{
			name:             "QUAPOS=2 (uncertain position)",
			depth:            5.0,
			attrs:            map[string]interface{}{"DEPTH": 5.0, "QUAPOS": 2},
			wantHasIndicator: "SOUNDSC2",
		},
		{
			name:             "QUAPOS=1 (surveyed - no indicator)",
			depth:            5.0,
			attrs:            map[string]interface{}{"DEPTH": 5.0, "QUAPOS": 1},
			wantHasIndicator: "", // No indicator for good quality
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			instructions, err := lib.executeCS("SOUNDG03", NewCSContext(tt.attrs, "", nil, mariner))
			require.NoError(t, err)

			if tt.wantHasIndicator == "" {
				// Should NOT have B1 or C2 indicators
				for _, instr := range instructions {
					sy, ok := instr.(*SYInstruction)
					require.True(t, ok)
					assert.NotContains(t, sy.SymbolID, "B1")
					assert.NotContains(t, sy.SymbolID, "C2")
				}
			} else {
				// Should have the indicator
				found := false
				for _, instr := range instructions {
					sy, ok := instr.(*SYInstruction)
					require.True(t, ok)
					if sy.SymbolID == tt.wantHasIndicator {
						found = true
						break
					}
				}
				assert.True(t, found, "Expected to find indicator symbol %s", tt.wantHasIndicator)
			}
		})
	}
}

// TestSNDFRM04_SafetyDepthThreshold tests SOUNDS vs SOUNDG prefix selection
func TestSNDFRM04_SafetyDepthThreshold(t *testing.T) {
	lib := &Library{}
	mariner := &MarinerSettings{
		SafetyDepth: 28.0,
	}

	tests := []struct {
		name       string
		depth      float64
		wantPrefix string
	}{
		{
			name:       "Exactly at safety depth (28m) - should be SOUNDS",
			depth:      28.0,
			wantPrefix: "SOUNDS",
		},
		{
			name:       "Just below safety depth (27.9m) - should be SOUNDS",
			depth:      27.9,
			wantPrefix: "SOUNDS",
		},
		{
			name:       "Just above safety depth (28.1m) - should be SOUNDG",
			depth:      28.1,
			wantPrefix: "SOUNDG",
		},
		{
			name:       "Deep (100m) - should be SOUNDG",
			depth:      100.0,
			wantPrefix: "SOUNDG",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			attrs := map[string]interface{}{"DEPTH": tt.depth}
			instructions, err := lib.executeCS("SOUNDG03", NewCSContext(attrs, "", nil, mariner))

			require.NoError(t, err)
			require.NotEmpty(t, instructions)

			// Check first symbol has correct prefix
			sy, ok := instructions[0].(*SYInstruction)
			require.True(t, ok)
			assert.Contains(t, sy.SymbolID, tt.wantPrefix, "Symbol should start with %s", tt.wantPrefix)
		})
	}
}

// TestSNDFRM04_TruncationNotRounding tests that depths are truncated, not rounded
func TestSNDFRM04_TruncationNotRounding(t *testing.T) {
	lib := &Library{}
	mariner := &MarinerSettings{
		SafetyDepth: 28.0,
	}

	tests := []struct {
		name        string
		depth       float64
		wantSymbols []string
		desc        string
	}{
		{
			name:        "47.9m truncates to 47, not rounds to 48",
			depth:       47.9,
			wantSymbols: []string{"SOUNDG14", "SOUNDG07"}, // "4", "7" not "4", "8"
			desc:        "Depth >= 31m should truncate fractions",
		},
		{
			name:        "99.9m truncates to 99",
			depth:       99.9,
			wantSymbols: []string{"SOUNDG19", "SOUNDG09"}, // "9", "9"
			desc:        "Should not round up to 100",
		},
		{
			name:        "234.9m truncates to 234",
			depth:       234.9,
			wantSymbols: []string{"SOUNDG22", "SOUNDG13", "SOUNDG04"}, // "2", "3", "4"
			desc:        "Large depths also truncate",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			attrs := map[string]interface{}{"DEPTH": tt.depth}
			instructions, err := lib.executeCS("SOUNDG03", NewCSContext(attrs, "", nil, mariner))

			require.NoError(t, err)
			require.Len(t, instructions, len(tt.wantSymbols), tt.desc)

			for i, instr := range instructions {
				sy, ok := instr.(*SYInstruction)
				require.True(t, ok)
				assert.Equal(t, tt.wantSymbols[i], sy.SymbolID)
			}
		})
	}
}

// TestSNDFRM04_NoDepth tests behavior when depth attribute is missing
func TestSNDFRM04_NoDepth(t *testing.T) {
	lib := &Library{}
	mariner := &MarinerSettings{
		SafetyDepth: 28.0,
	}

	attrs := map[string]interface{}{} // No depth
	instructions, err := lib.executeCS("SOUNDG03", NewCSContext(attrs, "", nil, mariner))

	require.NoError(t, err)
	assert.Empty(t, instructions, "Should return empty list when no depth")
}
