package sim

import (
	"math"
	"testing"

	"github.com/beetlebugorg/chartplotter/internal/engine/nmea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// parseSim parses the given sentences into a fresh VesselState through the real
// NMEA parser (the path the server uses).
func parseSim(t *testing.T, lines []string) *nmea.VesselState {
	t.Helper()
	vs := &nmea.VesselState{}
	p := &nmea.Parser{}
	for _, ln := range lines {
		sent, err := nmea.ParseSentence(ln)
		require.NoErrorf(t, err, "sentence must frame: %s", ln)
		p.Apply(sent, vs)
	}
	return vs
}

// Every registered scenario must resolve and yield runnable Options.
func TestScenarios_Registry(t *testing.T) {
	require.NotEmpty(t, scenarios)
	for _, sc := range scenarios {
		got, ok := ScenarioByName(sc.Name)
		require.Truef(t, ok, "scenario %q should resolve", sc.Name)
		assert.Equal(t, sc.Name, got.Name)
		s := New(sc.Options(1, nil))
		assert.NotZero(t, s.Own.Lat)
		assert.NotZero(t, s.Own.Lon)
	}
	_, ok := ScenarioByName("nope")
	assert.False(t, ok)
}

// A scenario with an own-ship route steers own-ship toward its first waypoint
// (course points roughly down-route, and the boat makes progress that way).
func TestScenarios_OwnRouteSteers(t *testing.T) {
	sc, ok := ScenarioByName("harbor")
	require.True(t, ok)
	require.NotEmpty(t, sc.Route)
	s := New(sc.Options(1, nil))

	wp := sc.Route[0]
	startBrg := bearing(s.Own.Lat, s.Own.Lon, wp[0], wp[1])
	assert.InDelta(t, startBrg, s.Own.Course, 1.0, "opening course should point at the first waypoint")

	startDist := dist(s.Own.Lat, s.Own.Lon, wp[0], wp[1])
	for range 60 { // one simulated minute
		s.Step(1)
	}
	assert.Less(t, dist(s.Own.Lat, s.Own.Lon, wp[0], wp[1]), startDist, "own-ship should close on the waypoint")
}

// The sailing scenario must make both COG and the heading-vs-COG gap vary widely
// over a few minutes (the case the heading line exists to show).
func TestScenarios_SailingVaries(t *testing.T) {
	sc, ok := ScenarioByName("sailing")
	require.True(t, ok)
	require.True(t, sc.Sailing)
	s := New(sc.Options(1, nil))

	var cogMin, cogMax, gapMin, gapMax = 360.0, -360.0, 360.0, -360.0
	for range 240 { // four simulated minutes
		s.Step(1)
		nav := parseSim(t, s.NavSentences()).Navigation
		require.NotNil(t, nav.COGTrue)
		require.NotNil(t, nav.HeadingTrue)
		cog := *nav.COGTrue
		gap := math.Mod(*nav.HeadingTrue-cog+540, 360) - 180 // signed heading−COG
		cogMin, cogMax = math.Min(cogMin, cog), math.Max(cogMax, cog)
		gapMin, gapMax = math.Min(gapMin, gap), math.Max(gapMax, gap)
	}
	assert.Greater(t, cogMax-cogMin, 60.0, "COG should tack across a wide arc")
	assert.Greater(t, gapMax-gapMin, 20.0, "heading−COG (leeway) should swing, and flip sign")
	assert.Less(t, gapMin, 0.0)
	assert.Greater(t, gapMax, 0.0)
}

// --drop-gps's mechanism: env sentences must be position-free, nav sentences carry
// the position. (The command withholds NavSentences to simulate signal loss.)
func TestScenarios_NavEnvSplit(t *testing.T) {
	s := New(Options{Lat: 38.978, Lon: -76.478, Course: 45, Speed: 6, Seed: 1})

	env := parseSim(t, s.EnvSentences())
	assert.Nil(t, env.Navigation.Position, "env sentences must not carry a position")
	require.NotNil(t, env.Environment.Depth.BelowTransducer, "env sentences carry depth")

	nav := parseSim(t, s.NavSentences())
	require.NotNil(t, nav.Navigation.Position, "nav sentences carry the position fix")
}
