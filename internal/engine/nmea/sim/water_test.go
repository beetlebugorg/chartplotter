package sim

import (
	"math/rand"
	"os"
	"testing"

	tile57 "github.com/beetlebugorg/tile57/bindings/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWaterMask_Synthetic(t *testing.T) {
	// A single square "depth area" ring [lon,lat] from (0,0) to (1,1).
	m := newWaterMask([][][2]float64{{{0, 0}, {1, 0}, {1, 1}, {0, 1}, {0, 0}}})
	require.NotNil(t, m)
	assert.True(t, m.IsWater(0.5, 0.5), "inside")
	assert.False(t, m.IsWater(0.5, 1.5), "outside (east)")
	assert.False(t, m.IsWater(-0.1, 0.5), "outside (south)")
	la, lo, ok := m.Sample(rand.New(rand.NewSource(1)))
	require.True(t, ok)
	assert.True(t, m.IsWater(la, lo), "sampled point is water")
}

func TestWaterMask_RealCell(t *testing.T) {
	// The repo ships a Chesapeake test cell — build a mask and confirm sampled
	// traffic lands in navigable depth areas.
	data, err := os.ReadFile("../../../../testdata/US4MD81M.000")
	if err != nil {
		t.Skip("test cell not available")
	}
	src, err := tile57.OpenChartBytes(data)
	require.NoError(t, err)
	defer src.Close()
	feats, err := src.Features("DEPARE", "DRGARE")
	require.NoError(t, err)
	m := NewWaterMask(feats, 2)
	require.NotNil(t, m, "cell should yield navigable depth areas")
	t.Logf("depth-area polygons: %d  bounds lat[%.3f,%.3f] lon[%.3f,%.3f]", len(m.polys), m.minLat, m.maxLat, m.minLon, m.maxLon)

	rng := rand.New(rand.NewSource(7))
	for range 20 {
		la, lo, ok := m.Sample(rng)
		require.True(t, ok, "should find a water point")
		assert.True(t, m.IsWater(la, lo))
	}
}
