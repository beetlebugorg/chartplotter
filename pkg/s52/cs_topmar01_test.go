package s52

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTOPMAR01_ConePointUp(t *testing.T) {
	ctx := NewCSContext(map[string]interface{}{
		"TOPSHP": 1, // Cone, point up
	}, "", nil, nil)

	result, err := NewTOPMAR01(ctx, &Library{}).Execute()

	require.NoError(t, err)
	require.Len(t, result, 1)

	sy, ok := result[0].(*SYInstruction)
	require.True(t, ok)
	assert.Equal(t, "TOPMAR02", sy.SymbolID, "Should use floating topmark for cone")
}

func TestTOPMAR01_RigidStructure(t *testing.T) {
	ctx := NewCSContext(map[string]interface{}{
		"TOPSHP": 1, // Cone, point up
		"BCNSHP": 5, // Beacon shape present = rigid structure
	}, "", nil, nil)

	result, err := NewTOPMAR01(ctx, &Library{}).Execute()

	require.NoError(t, err)
	require.Len(t, result, 1)

	sy, ok := result[0].(*SYInstruction)
	require.True(t, ok)
	assert.Equal(t, "TOPMAR22", sy.SymbolID, "Should use rigid topmark for beacon")
}

func TestTOPMAR01_NoTOPSHP(t *testing.T) {
	ctx := NewCSContext(map[string]interface{}{}, "", nil, nil)

	result, err := NewTOPMAR01(ctx, &Library{}).Execute()

	require.NoError(t, err)
	require.Len(t, result, 1)

	sy, ok := result[0].(*SYInstruction)
	require.True(t, ok)
	assert.Equal(t, "QUESMRK1", sy.SymbolID, "No TOPSHP should return question mark symbol")
}
