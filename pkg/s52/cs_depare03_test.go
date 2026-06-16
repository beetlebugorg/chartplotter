package s52

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDEPARE03_ShallowArea(t *testing.T) {
	lib := &Library{}
	mariner := &MarinerSettings{
		SafetyContour:  30.0,
		ShallowContour: 10.0,
		DeepContour:    100.0,
	}

	ctx := NewCSContext(map[string]interface{}{
		"DRVAL1": 5.0,
		"DRVAL2": 10.0,
	}, "", nil, mariner)

	result, err := NewDEPARE03(ctx, lib).Execute()

	require.NoError(t, err)
	require.NotEmpty(t, result)

	// Should have area color instruction
	ac, ok := result[0].(*ACInstruction)
	require.True(t, ok)
	assert.Equal(t, "DEPVS", ac.Color, "Depth < 10m should be very shallow (DEPVS)")
}

func TestDEPARE03_SafetyContourCrossing(t *testing.T) {
	lib := &Library{}
	mariner := &MarinerSettings{
		SafetyContour:  30.0,
		ShallowContour: 10.0,
		DeepContour:    100.0,
	}

	ctx := NewCSContext(map[string]interface{}{
		"DRVAL1": 25.0,
		"DRVAL2": 35.0,
	}, "", nil, mariner)

	result, err := NewDEPARE03(ctx, lib).Execute()

	require.NoError(t, err)
	require.NotEmpty(t, result)

	// First instruction should be area color
	ac, ok := result[0].(*ACInstruction)
	require.True(t, ok)
	assert.Equal(t, "DEPMS", ac.Color, "DRVAL1 < safety contour should be shallow")

	// Should have line style (QUAPOS01 determines if solid or dashed)
	foundLS := false
	for _, instr := range result {
		if _, ok := instr.(*LSInstruction); ok {
			foundLS = true
			break
		}
	}
	assert.True(t, foundLS, "Should have line style instruction")
}

func TestDEPARE03_DredgedArea(t *testing.T) {
	lib := &Library{}
	mariner := &MarinerSettings{
		SafetyContour:  30.0,
		ShallowContour: 10.0,
		DeepContour:    100.0,
	}

	ctx := NewCSContext(map[string]interface{}{
		"DRVAL1": 15.0,
		"DRVAL2": 20.0,
		"OBJNAM": "Dredged",
	}, "", nil, mariner)

	result, err := NewDEPARE03(ctx, lib).Execute()

	require.NoError(t, err)
	require.NotEmpty(t, result)
}
