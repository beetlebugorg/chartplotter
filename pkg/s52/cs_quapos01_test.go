package s52

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestQUAPOS01_GoodQuality(t *testing.T) {
	tests := []struct {
		name   string
		quapos int
	}{
		{"QUAPOS=1 (surveyed)", 1},
		{"QUAPOS=10 (precisely known)", 10},
		{"QUAPOS=11 (calculated)", 11},
		{"QUAPOS=0 (default/unknown)", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := NewCSContext(map[string]interface{}{
				"QUAPOS": tt.quapos,
			}, "", nil, nil)

			result, err := NewQUAPOS01(ctx).Execute()

			require.NoError(t, err)
			require.Len(t, result, 1)

			ls, ok := result[0].(*LSInstruction)
			require.True(t, ok)
			assert.Equal(t, "SOLD", ls.Style, "Good quality should use solid line")
		})
	}
}

func TestQUAPOS01_UncertainQuality(t *testing.T) {
	tests := []struct {
		name   string
		quapos int
	}{
		{"QUAPOS=2", 2},
		{"QUAPOS=3", 3},
		{"QUAPOS=4", 4},
		{"QUAPOS=5", 5},
		{"QUAPOS=6", 6},
		{"QUAPOS=7", 7},
		{"QUAPOS=8", 8},
		{"QUAPOS=9", 9},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := NewCSContext(map[string]interface{}{
				"QUAPOS": tt.quapos,
			}, "", nil, nil)

			result, err := NewQUAPOS01(ctx).Execute()

			require.NoError(t, err)
			require.Len(t, result, 1)

			ls, ok := result[0].(*LSInstruction)
			require.True(t, ok)
			assert.Equal(t, "DASH", ls.Style, "Uncertain quality should use dashed line")
		})
	}
}

func TestQUAPOS01_MissingAttribute(t *testing.T) {
	ctx := NewCSContext(map[string]interface{}{}, "", nil, nil)

	result, err := NewQUAPOS01(ctx).Execute()

	require.NoError(t, err)
	require.Len(t, result, 1)

	ls, ok := result[0].(*LSInstruction)
	require.True(t, ok)
	assert.Equal(t, "SOLD", ls.Style, "Missing QUAPOS should default to good quality")
}
