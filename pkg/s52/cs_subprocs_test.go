package s52

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSEABED01_DepthColors tests the depth color determination sub-procedure
func TestSEABED01_DepthColors(t *testing.T) {
	lib := &Library{}
	mariner := &MarinerSettings{
		ShallowContour: 10.0,
		SafetyContour:  30.0,
		DeepContour:    100.0,
	}

	tests := []struct {
		name      string
		drval1    float64
		drval2    float64
		wantColor string
	}{
		{"Very shallow (<10m)", 5.0, 6.0, "DEPVS"},
		{"Shallow (10-30m)", 15.0, 20.0, "DEPMS"},
		{"Medium (30-100m)", 50.0, 60.0, "DEPMD"},
		{"Deep (>=100m)", 150.0, 200.0, "DEPDW"},
		{"Intertidal (negative)", -1.0, 0.0, "DEPVS"},
		{"Exactly at shallow contour", 10.0, 15.0, "DEPMS"},
		{"Exactly at safety contour", 30.0, 35.0, "DEPMD"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			color := lib.csSEABED01(tt.drval1, tt.drval2, mariner)
			assert.Equal(t, tt.wantColor, color)
		})
	}
}

// TestSAFCON01_Label tests the safety contour label sub-procedure
func TestSAFCON01_Label(t *testing.T) {
	lib := &Library{}
	mariner := DefaultMarinerSettings()

	result, err := lib.csSAFCON01(30.0, mariner)

	require.NoError(t, err)
	require.Len(t, result, 1)

	tx, ok := result[0].(*TXInstruction)
	require.True(t, ok)
	assert.Equal(t, "30", tx.Text, "Should format depth as whole number")
	assert.Equal(t, "CHBLK", tx.Color, "Should use black color")
}

// TestRESCSP02_RestrictionPatterns tests the restriction symbol sub-procedure
func TestRESCSP02_RestrictionPatterns(t *testing.T) {
	lib := &Library{}
	mariner := DefaultMarinerSettings()

	tests := []struct {
		name       string
		restrn     interface{}
		wantSymbol string
	}{
		{"Anchoring prohibited (1)", 1, "ENTRES51"},
		{"Entry prohibited (7)", 7, "ENTRES61"},
		{"Diving prohibited (12)", 12, "ENTRES51"},
		{"No wake (15)", 15, "ENTRES51"},
		{"Multiple values - entry wins (7,1)", "7,1", "ENTRES61"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := lib.csRESCSP02(map[string]interface{}{
				"RESTRN": tt.restrn,
			}, mariner)

			require.NoError(t, err)
			if tt.wantSymbol == "" {
				assert.Empty(t, result)
			} else {
				require.Len(t, result, 1)
				sy, ok := result[0].(*SYInstruction)
				require.True(t, ok)
				assert.Equal(t, tt.wantSymbol, sy.SymbolID)
			}
		})
	}
}

// TestDEPVAL02_ReturnsUnknown tests that DEPVAL02 returns unknown without spatial data
func TestDEPVAL02_ReturnsUnknown(t *testing.T) {
	lib := &Library{}
	mariner := DefaultMarinerSettings()

	leastDepth, seabedDepth := lib.csDEPVAL02(map[string]interface{}{
		"WATLEV": 3,
		"EXPSOU": 1,
	}, mariner)

	// Without spatial data, should return unknown
	assert.Equal(t, -1.0, leastDepth, "Should return unknown least depth")
	assert.Equal(t, -1.0, seabedDepth, "Should return unknown seabed depth")
}

// TestUDWHAZ05_IsolatedDanger tests the isolated danger determination
func TestUDWHAZ05_IsolatedDanger(t *testing.T) {
	lib := &Library{}
	mariner := &MarinerSettings{
		SafetyContour:                     30.0,
		ShowIsolatedDangersInShallowWater: false,
	}

	// Underwater danger within safety contour
	showIsolated, priority, viewGroup := lib.csUDWHAZ05(20.0, map[string]interface{}{
		"WATLEV": 3, // Always underwater
	}, mariner)

	assert.True(t, showIsolated, "Should show isolated danger symbol")
	assert.Equal(t, 8, priority, "Should have display priority 8")
	assert.Equal(t, 14010, viewGroup, "Should be in DISPLAYBASE")

	// Danger deeper than safety contour
	showIsolated, _, _ = lib.csUDWHAZ05(35.0, map[string]interface{}{
		"WATLEV": 3,
	}, mariner)

	assert.False(t, showIsolated, "Depth > safety contour should not show isolated danger")

	// Above water danger
	showIsolated, _, _ = lib.csUDWHAZ05(20.0, map[string]interface{}{
		"WATLEV": 1, // Dries
	}, mariner)

	assert.False(t, showIsolated, "Above water danger should not show isolated danger symbol")
}

// TestQUAPNT02_LowAccuracy tests the low accuracy determination
func TestQUAPNT02_LowAccuracy(t *testing.T) {
	lib := &Library{}
	mariner := DefaultMarinerSettings()

	// Uncertain position
	showLowAcc := lib.csQUAPNT02(map[string]interface{}{
		"QUAPOS": 4, // Uncertain
	}, mariner)

	assert.True(t, showLowAcc, "QUAPOS=4 should show low accuracy")

	// Good quality position
	showLowAcc = lib.csQUAPNT02(map[string]interface{}{
		"QUAPOS": 1, // Surveyed
	}, mariner)

	assert.False(t, showLowAcc, "QUAPOS=1 should not show low accuracy")

	// Missing QUAPOS
	showLowAcc = lib.csQUAPNT02(map[string]interface{}{}, mariner)

	assert.False(t, showLowAcc, "Missing QUAPOS should not show low accuracy")
}
