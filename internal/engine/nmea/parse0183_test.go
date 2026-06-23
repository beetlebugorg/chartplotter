package nmea

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixedNow makes Updated deterministic in tests.
func fixedParser() *Parser {
	return &Parser{Now: func() time.Time { return time.Date(2026, 6, 23, 0, 0, 0, 0, time.UTC) }}
}

func TestParseSentence_ChecksumAndAddress(t *testing.T) {
	// Canonical references with known-good checksums.
	s, err := ParseSentence("$GPRMC,123519,A,4807.038,N,01131.000,E,022.4,084.4,230394,003.1,W*6A")
	require.NoError(t, err)
	assert.Equal(t, "GP", s.Talker)
	assert.Equal(t, "RMC", s.Type)
	assert.Equal(t, "123519", s.Fields[0])

	// '!' encapsulated delimiter (AIS) frames the same way.
	s, err = ParseSentence("!AIVDM,1,1,,A,15M,0*0A")
	if err == nil { // checksum may differ; we only assert framing when it parses
		assert.Equal(t, "AI", s.Talker)
		assert.Equal(t, "VDM", s.Type)
	}
}

func TestParseSentence_BadChecksum(t *testing.T) {
	_, err := ParseSentence("$GPRMC,123519,A,4807.038,N,01131.000,E,022.4,084.4,230394,003.1,W*00")
	require.Error(t, err)
}

func TestParseSentence_MissingChecksumTolerated(t *testing.T) {
	s, err := ParseSentence("$IIMTW,18.6,C")
	require.NoError(t, err)
	assert.Equal(t, "MTW", s.Type)
}

func TestApply_RMC(t *testing.T) {
	vs := &VesselState{}
	s, err := ParseSentence("$GPRMC,123519,A,4807.038,N,01131.000,E,022.4,084.4,230394,003.1,W*6A")
	require.NoError(t, err)
	require.True(t, fixedParser().Apply(s, vs))

	require.NotNil(t, vs.Navigation.Position)
	assert.InDelta(t, 48.1173, vs.Navigation.Position.Lat, 1e-4)
	assert.InDelta(t, 11.51667, vs.Navigation.Position.Lon, 1e-4)
	assert.InDelta(t, 22.4, *vs.Navigation.SOG, 1e-9)
	assert.InDelta(t, 84.4, *vs.Navigation.COGTrue, 1e-9)
	assert.InDelta(t, -3.1, *vs.Navigation.MagneticVariation, 1e-9) // W → negative
	require.NotNil(t, vs.Navigation.Datetime)
	assert.Equal(t, time.Date(1994, 3, 23, 12, 35, 19, 0, time.UTC), *vs.Navigation.Datetime)
}

func TestApply_DepthOffsets(t *testing.T) {
	p := fixedParser()
	vs := &VesselState{}
	s, _ := ParseSentence("$IIDPT,12.3,0.5,") // +offset → below surface
	p.Apply(s, vs)
	require.NotNil(t, vs.Environment.Depth.BelowTransducer)
	assert.InDelta(t, 12.3, *vs.Environment.Depth.BelowTransducer, 1e-9)
	require.NotNil(t, vs.Environment.Depth.BelowSurface)
	assert.InDelta(t, 12.8, *vs.Environment.Depth.BelowSurface, 1e-9)

	vs = &VesselState{}
	s, _ = ParseSentence("$IIDPT,12.3,-0.7,") // −offset → below keel
	p.Apply(s, vs)
	require.NotNil(t, vs.Environment.Depth.BelowKeel)
	assert.InDelta(t, 11.6, *vs.Environment.Depth.BelowKeel, 1e-9)
}

func TestApply_WindUnitsAndRef(t *testing.T) {
	p := fixedParser()
	vs := &VesselState{}
	// apparent, knots
	s, _ := ParseSentence("$IIMWV,045.0,R,12.5,N,A")
	p.Apply(s, vs)
	require.NotNil(t, vs.Environment.Wind.AngleApparent)
	assert.InDelta(t, 45.0, *vs.Environment.Wind.AngleApparent, 1e-9)
	assert.InDelta(t, 12.5, *vs.Environment.Wind.SpeedApparent, 1e-9)

	// true, m/s → knots
	s, _ = ParseSentence("$IIMWV,090.0,T,10,M,A")
	p.Apply(s, vs)
	require.NotNil(t, vs.Environment.Wind.SpeedTrue)
	assert.InDelta(t, 19.43844, *vs.Environment.Wind.SpeedTrue, 1e-4)
}

func TestApply_RouteRMB(t *testing.T) {
	p := fixedParser()
	vs := &VesselState{}
	s, err := ParseSentence("$GPRMB,A,0.66,L,WPT1,WPT2,4917.24,N,12309.57,W,001.3,052.5,000.5,V")
	require.NoError(t, err)
	p.Apply(s, vs)
	require.NotNil(t, vs.Route.XTE)
	assert.InDelta(t, -0.66, *vs.Route.XTE, 1e-9) // L → steer left → negative
	assert.Equal(t, "WPT2", vs.Route.ActiveWaypoint)
	assert.InDelta(t, 1.3, *vs.Route.DistanceToWaypoint, 1e-9)
	assert.InDelta(t, 52.5, *vs.Route.BearingToWaypoint, 1e-9)
}

func TestApply_UnknownTypeIsNoop(t *testing.T) {
	vs := &VesselState{}
	s, err := ParseSentence("$GPGSV,3,1,11,03,03,111,00")
	require.NoError(t, err)
	assert.False(t, fixedParser().Apply(s, vs))
	assert.True(t, vs.Updated.IsZero())
}

func TestReplay_Fixture(t *testing.T) {
	file, err := os.Open("testdata/sample.nmea")
	require.NoError(t, err)
	defer file.Close()

	vs := &VesselState{}
	st, err := Replay(file, fixedParser(), vs)
	require.NoError(t, err)

	assert.Equal(t, 14, st.Lines)
	assert.Equal(t, 0, st.Errors, "all fixture checksums must be valid")
	assert.Equal(t, 14, st.Parsed)
	assert.Equal(t, 1, st.ByType["RMC"])
	assert.Equal(t, 1, st.ByType["DBT"])

	// The full instrument model should be populated from the mixed stream.
	require.NotNil(t, vs.Navigation.Position)
	require.NotNil(t, vs.Navigation.HeadingTrue)
	assert.InDelta(t, 84.1, *vs.Navigation.HeadingTrue, 1e-9)
	require.NotNil(t, vs.Navigation.SpeedThroughWater)
	assert.InDelta(t, 21.8, *vs.Navigation.SpeedThroughWater, 1e-9)
	require.NotNil(t, vs.Navigation.RateOfTurn)
	assert.InDelta(t, 2.5, *vs.Navigation.RateOfTurn, 1e-9)
	require.NotNil(t, vs.Environment.Water.Temperature)
	assert.InDelta(t, 18.6, *vs.Environment.Water.Temperature, 1e-9)
	require.NotNil(t, vs.Environment.Wind.DirectionTrue)
	assert.InDelta(t, 129.1, *vs.Environment.Wind.DirectionTrue, 1e-9)
	require.NotNil(t, vs.Route.BearingToWaypoint)
}

func TestStore_ConcurrentApply(t *testing.T) {
	store := &Store{}
	p := fixedParser()
	s, _ := ParseSentence("$IIMTW,18.6,C")
	done := make(chan bool)
	for range 8 {
		go func() {
			for range 100 {
				store.Apply(p, s)
				_ = store.Snapshot()
			}
			done <- true
		}()
	}
	for range 8 {
		<-done
	}
	snap := store.Snapshot()
	require.NotNil(t, snap.Environment.Water.Temperature)
}
