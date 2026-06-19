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

// TestTOPMAR01_CoLocatedPlatform exercises the S-52 floating-vs-rigid rule from
// the co-located object class: a buoy → floating symbol, a beacon → rigid.
func TestTOPMAR01_CoLocatedPlatform(t *testing.T) {
	lib := &Library{}
	sym := func(adj []AdjacentObject) string {
		ctx := NewCSContext(map[string]interface{}{"TOPSHP": 1}, "Point",
			&SpatialContext{AdjacentObjects: adj}, nil)
		ctx.ObjectClass = "TOPMAR"
		ins, err := NewTOPMAR01(ctx, lib).Execute()
		require.NoError(t, err)
		require.Len(t, ins, 1)
		return ins[0].(*SYInstruction).SymbolID
	}
	// TOPSHP 1 → floating TOPMAR02 / rigid TOPMAR22.
	assert.Equal(t, "TOPMAR02", sym([]AdjacentObject{{ObjectClass: "BOYLAT"}}), "buoy → floating")
	assert.Equal(t, "TOPMAR22", sym([]AdjacentObject{{ObjectClass: "BCNLAT"}}), "beacon → rigid")
	assert.Equal(t, "TOPMAR02", sym([]AdjacentObject{
		{ObjectClass: "MORFAC", Attributes: map[string]interface{}{"CATMOR": 7}}}), "mooring buoy → floating")
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
