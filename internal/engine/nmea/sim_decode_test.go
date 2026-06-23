package nmea

import (
	"testing"
	"time"

	"github.com/beetlebugorg/chartplotter/internal/engine/nmea/sim"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The simulator's encoded AIS must decode back to real targets through our store
// (encode → AIVDM → decode round-trip), exercising the whole AIS path end to end.
func TestSimAIS_DecodeRoundTrip(t *testing.T) {
	s := sim.New(sim.Options{Lat: 38.978, Lon: -76.478, Course: 45, Speed: 6, Targets: 5, Collision: true, Seed: 1})
	s.Step(5)

	store := NewAISStore(time.Minute)
	feed := store.feeder()
	for _, ln := range s.AISPositions() {
		feed(ln)
	}
	for _, ln := range s.AISStatics() {
		feed(ln)
	}

	tgts := store.Snapshot()
	require.Len(t, tgts, 5)
	for _, tg := range tgts {
		assert.NotZero(t, tg.MMSI)
		assert.True(t, tg.Lat != 0 || tg.Lon != 0, "target %d should have a position", tg.MMSI)
		assert.NotEmpty(t, tg.Name, "target %d should have a name", tg.MMSI)
		assert.Contains(t, []string{"A", "B"}, tg.Class)
	}
}
