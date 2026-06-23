package nmea

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Real sentences captured from a B&G Zeus3 (issue: own-ship not appearing).
const bgCapture = `$IIXDR,C,,C,AIRTEMP,A,3.6,D,HEEL,A,1.9,D,TRIM,P,,B,BARO,A,-1.2,D,RUDDER*0A
$IIHDG,21.6,,,11.0,W*35
$GPGGA,122946,3857.5349,N,07628.4769,W,2,12,0.60,1,M,-33.7,M,,*5C
$GPGLC,9960,,,16126.29,A,27606.31,A,42689.85,A,58949.28,A,,*74
$GPGLL,3857.5349,N,07628.4769,W,122946,A,D*5F
$GPGSA,A,3,03,06,11,14,17,19,22,24,12,82,65,81,1.20,0.60,1.00*0D
$GPRMC,122946,A,3857.5349,N,07628.4769,W,0.2,310.0,230626,11.0,W,D*22
$GPVTG,307.0,T,318.0,M,0.4,N,0.7,K,A*2E
$GPZDA,122946,23,06,2026,04,00*47
$SDDBT,7.8,f,2.3,M,1.3,F*0A
$SDDPT,2.4,0.0,*7D
$SDMTW,26.6,C*06
$SDVHW,10.5,T,21.6,M,0.0,N,0.0,K*43
$WIMWD,321.2,T,332.2,M,0.0,N,0.0,M*58
$WIMWV,309.7,R,1.5,N,A*2A`

func TestRealWorld_BGZeus3(t *testing.T) {
	vs := &VesselState{}
	st, err := Replay(strings.NewReader(bgCapture), fixedParser(), vs)
	require.NoError(t, err)
	t.Logf("lines=%d parsed=%d errors=%d byType=%v", st.Lines, st.Parsed, st.Errors, st.ByType)

	// The crux: did a position land?
	require.NotNil(t, vs.Navigation.Position, "position must parse from RMC/GGA/GLL")
	assert.InDelta(t, 38.95891, vs.Navigation.Position.Lat, 1e-4)
	assert.InDelta(t, -76.474615, vs.Navigation.Position.Lon, 1e-4)

	require.NotNil(t, vs.Navigation.COGTrue)
	assert.InDelta(t, 307.0, *vs.Navigation.COGTrue, 1e-6) // VTG (307) arrives after RMC (310) — latest wins
	require.NotNil(t, vs.Navigation.HeadingMagnetic)
	assert.InDelta(t, 21.6, *vs.Navigation.HeadingMagnetic, 1e-6)
	require.NotNil(t, vs.Environment.Depth.BelowTransducer)
}
