package sim

import (
	"strings"
	"testing"

	"github.com/beetlebugorg/chartplotter/internal/engine/nmea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The simulator's own-ship sentences must parse back through our parser, and its
// AIS sentences must frame as valid !AIVDM. (Full AIS decode round-trip lives in
// the nmea package, where the AIS store's feeder is accessible.)
func TestSim_OwnShipRoundTrip(t *testing.T) {
	s := New(Options{Lat: 38.978, Lon: -76.478, Course: 45, Speed: 6, Targets: 5, Collision: true, Seed: 1})
	s.Step(10)

	vs := &nmea.VesselState{}
	p := &nmea.Parser{}
	for _, ln := range s.OwnSentences() {
		sent, err := nmea.ParseSentence(ln)
		require.NoError(t, err, "own-ship sentence must frame: %s", ln)
		p.Apply(sent, vs)
	}
	require.NotNil(t, vs.Navigation.Position)
	assert.InDelta(t, s.Own.Lat, vs.Navigation.Position.Lat, 1e-3)
	assert.InDelta(t, s.Own.Lon, vs.Navigation.Position.Lon, 1e-3)
	require.NotNil(t, vs.Navigation.HeadingTrue)
	require.NotNil(t, vs.Navigation.SOG)
	require.NotNil(t, vs.Environment.Depth.BelowTransducer)
}

func TestSim_CourseVariation(t *testing.T) {
	s := New(Options{Lat: 38.978, Lon: -76.478, Course: 45, Speed: 6, Targets: 6, Seed: 1})
	start := make([]float64, len(s.Targets))
	for i, tg := range s.Targets {
		start[i] = tg.Course
	}
	for range 180 { // 3 simulated minutes
		s.Step(1)
	}
	changed := 0
	for i, tg := range s.Targets {
		d := tg.Course - start[i]
		for d > 180 {
			d -= 360
		}
		for d < -180 {
			d += 360
		}
		if d < 0 {
			d = -d
		}
		if d > 5 { // turned or steered toward a waypoint
			changed++
		}
	}
	assert.Positive(t, changed, "some targets should change course (turns / route-following)")
}

func TestSim_AISFrames(t *testing.T) {
	s := New(Options{Lat: 38.978, Lon: -76.478, Targets: 4, Seed: 2})
	pos := s.AISPositions()
	assert.Len(t, pos, 4)
	for _, ln := range append(pos, s.AISStatics()...) {
		assert.True(t, strings.HasPrefix(ln, "!AIVDM"), "AIS line: %s", ln)
		_, err := nmea.ParseSentence(ln)
		require.NoError(t, err, "AIS sentence must frame (checksum ok): %s", ln)
	}
}
