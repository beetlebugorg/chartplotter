package s52

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLIGHTS06_RedLight(t *testing.T) {
	ctx := NewCSContext(map[string]interface{}{
		"COLOUR": 3, // Red
	}, "", nil, nil)

	result, err := NewLIGHTS06(ctx, &Library{}).Execute()

	require.NoError(t, err)
	require.NotEmpty(t, result)

	// Should have symbol instruction
	sy, ok := result[0].(*SYInstruction)
	require.True(t, ok)
	assert.Equal(t, "LIGHTS11", sy.SymbolID, "Red light should use LIGHTS11")
}

func TestLIGHTS06_GreenLight(t *testing.T) {
	ctx := NewCSContext(map[string]interface{}{
		"COLOUR": 4, // Green
	}, "", nil, nil)

	result, err := NewLIGHTS06(ctx, &Library{}).Execute()

	require.NoError(t, err)
	require.NotEmpty(t, result)

	sy, ok := result[0].(*SYInstruction)
	require.True(t, ok)
	assert.Equal(t, "LIGHTS12", sy.SymbolID, "Green light should use LIGHTS12")
}

func TestLIGHTS06_WhiteLight(t *testing.T) {
	ctx := NewCSContext(map[string]interface{}{
		"COLOUR": 1, // White
	}, "", nil, nil)

	result, err := NewLIGHTS06(ctx, &Library{}).Execute()

	require.NoError(t, err)
	require.NotEmpty(t, result)

	sy, ok := result[0].(*SYInstruction)
	require.True(t, ok)
	assert.Equal(t, "LIGHTS13", sy.SymbolID, "White light should use LIGHTS13")
}

func TestLIGHTS06_Floodlight(t *testing.T) {
	ctx := NewCSContext(map[string]interface{}{
		"CATLIT": 8, // Floodlight
	}, "", nil, nil)

	result, err := NewLIGHTS06(ctx, &Library{}).Execute()

	require.NoError(t, err)
	require.Len(t, result, 1)

	sy, ok := result[0].(*SYInstruction)
	require.True(t, ok)
	assert.Equal(t, "LIGHTS82", sy.SymbolID, "Floodlight should use LIGHTS82")
}

func TestLIGHTS06_DirectionalLight(t *testing.T) {
	ctx := NewCSContext(map[string]interface{}{
		"CATLIT": 1,    // Directional function
		"ORIENT": 45.0, // Bearing from seaward
		"COLOUR": 1,    // White
	}, "", nil, nil)

	result, err := NewLIGHTS06(ctx, &Library{}).Execute()

	require.NoError(t, err)
	require.NotEmpty(t, result)

	// Should have line instruction for directional bearing
	foundLS := false
	for _, instr := range result {
		if ls, ok := instr.(*LSInstruction); ok {
			assert.Equal(t, "DASH", ls.Style, "Directional light should have dashed bearing line")
			foundLS = true
			break
		}
	}
	assert.True(t, foundLS, "Directional light should have bearing line")

	// Should have symbol with rotation
	foundSY := false
	for _, instr := range result {
		if sy, ok := instr.(*SYInstruction); ok {
			assert.Equal(t, "LIGHTS13", sy.SymbolID, "White directional light")
			assert.NotEqual(t, 0.0, sy.Rotation, "Directional light should be rotated")
			foundSY = true
			break
		}
	}
	assert.True(t, foundSY, "Should have light symbol")
}

func TestLIGHTS06_SectorLight(t *testing.T) {
	ctx := NewCSContext(map[string]interface{}{
		"COLOUR": 3,     // Red
		"SECTR1": 270.0, // Sector start
		"SECTR2": 90.0,  // Sector end (wraps around)
	}, "", nil, nil)

	result, err := NewLIGHTS06(ctx, &Library{}).Execute()

	require.NoError(t, err)
	require.NotEmpty(t, result)

	// Should have symbol with rotation pointing toward sector midpoint
	sy, ok := result[0].(*SYInstruction)
	require.True(t, ok)
	assert.Equal(t, "LIGHTS11", sy.SymbolID, "Red sector light")
	assert.NotEqual(t, 0.0, sy.Rotation, "Sector light should be rotated")
}

func TestLIGHTS06_WithCharacteristic(t *testing.T) {
	ctx := NewCSContext(map[string]interface{}{
		"COLOUR": 1,   // White
		"LITCHR": 2,   // Flashing
		"SIGPER": 5.0, // 5 second period
	}, "", nil, nil)

	result, err := NewLIGHTS06(ctx, &Library{}).Execute()

	require.NoError(t, err)
	require.NotEmpty(t, result)

	// Should have symbol
	sy, ok := result[0].(*SYInstruction)
	require.True(t, ok)
	assert.Equal(t, "LIGHTS13", sy.SymbolID)

	// Should have text instruction for characteristic
	foundTX := false
	for _, instr := range result {
		if tx, ok := instr.(*TXInstruction); ok {
			assert.Contains(t, tx.Text, "Fl", "Should contain flashing characteristic")
			assert.Contains(t, tx.Text, "W", "Should contain white color")
			assert.Contains(t, tx.Text, "5s", "Should contain period")
			foundTX = true
			break
		}
	}
	assert.True(t, foundTX, "Should have characteristic text")
}
