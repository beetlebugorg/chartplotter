package nmea

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAIS_DecodePositionReport(t *testing.T) {
	s := NewAISStore(time.Minute)
	feed := s.feeder()
	// Verified single-fragment Class A position report (from go-ais's own tests).
	feed("!AIVDM,1,1,,B,23aDqDOP0S0:mk2Kv3Ip=wvpR>`<,0*3D")

	tgts := s.Snapshot()
	require.Len(t, tgts, 1)
	tg := tgts[0]
	t.Logf("decoded MMSI=%d lat=%.5f lon=%.5f class=%s", tg.MMSI, tg.Lat, tg.Lon, tg.Class)
	assert.NotZero(t, tg.MMSI, "MMSI must decode (proves the 6-bit payload parsed)")
	assert.Equal(t, "A", tg.Class)
	assert.True(t, tg.Lat >= -90 && tg.Lat <= 90 && (tg.Lat != 0 || tg.Lon != 0), "a position should decode")

	// Re-feeding the same target updates in place (no duplicate).
	feed("!AIVDM,1,1,,B,23aDqDOP0S0:mk2Kv3Ip=wvpR>`<,0*3D")
	assert.Len(t, s.Snapshot(), 1)
}

func TestAIS_DecodeStaticName(t *testing.T) {
	s := NewAISStore(time.Minute)
	// Canonical AIVDM type-24 (Class B static, part A) example — carries a vessel name.
	s.feeder()("!AIVDM,1,1,,A,H42O55i18tMET00000000000000,2*6D")
	tgts := s.Snapshot()
	require.Len(t, tgts, 1)
	t.Logf("decoded MMSI=%d name=%q type=%q", tgts[0].MMSI, tgts[0].Name, tgts[0].TypeName)
	assert.NotZero(t, tgts[0].MMSI)
	assert.NotEmpty(t, tgts[0].Name, "static report should yield a name")
}

func TestAIS_StaleEviction(t *testing.T) {
	s := NewAISStore(time.Minute)
	now := time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	s.feeder()("!AIVDM,1,1,,B,23aDqDOP0S0:mk2Kv3Ip=wvpR>`<,0*3D")
	require.Len(t, s.Snapshot(), 1)

	now = now.Add(2 * time.Minute) // past the 1-minute TTL
	assert.Len(t, s.Snapshot(), 0, "stale target evicted")
}
