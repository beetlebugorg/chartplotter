package nmea

import (
	"context"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// scriptedDial returns a DialFunc that yields the fixture bytes on every dial
// (then EOF, forcing a reconnect) and counts how many times it was called.
func scriptedDial(t *testing.T, data string) (DialFunc, *atomic.Int64) {
	t.Helper()
	var dials atomic.Int64
	return func(ctx context.Context, host string, port int) (io.ReadCloser, error) {
		dials.Add(1)
		return io.NopCloser(strings.NewReader(data)), nil
	}, &dials
}

func fixtureBytes(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("testdata/sample.nmea")
	require.NoError(t, err)
	return string(b)
}

func testManager(t *testing.T, dial DialFunc) *Manager {
	t.Helper()
	m := NewManager(context.Background(), &Store{}, ManagerOpts{
		Parser:     fixedParser(),
		Dial:       dial,
		Backoff:    time.Millisecond, // reconnect fast in tests
		MaxBackoff: time.Millisecond,
		StaleAfter: 50 * time.Millisecond,
	})
	t.Cleanup(m.Close)
	return m
}

func TestManager_IngestAndStatus(t *testing.T) {
	dial, _ := scriptedDial(t, fixtureBytes(t))
	m := testManager(t, dial)

	m.Apply(Source{ID: "mux", Name: "Multiplexer", Transport: TransportTCPClient,
		Host: "10.0.0.20", Port: 2000, Protocol: "nmea0183", Direction: "in", Enabled: true})

	// The store should fill from the fixture, and status should report the
	// sentence/talker sets seen.
	require.Eventually(t, func() bool {
		return m.Store().Snapshot().Navigation.Position != nil
	}, 2*time.Second, 5*time.Millisecond)

	st, ok := m.Status("mux")
	require.True(t, ok)
	assert.Contains(t, st.Sentences, "RMC")
	assert.Contains(t, st.Sentences, "MWV")
	assert.Contains(t, st.Talkers, "GP")
	assert.Contains(t, st.Talkers, "II")
	assert.Equal(t, 0, st.Errors)
}

func TestManager_Reconnects(t *testing.T) {
	dial, dials := scriptedDial(t, fixtureBytes(t))
	m := testManager(t, dial)
	m.Apply(Source{ID: "mux", Transport: TransportTCPClient, Host: "h", Port: 1, Enabled: true})

	// Each dialed stream EOFs immediately, so the runner must redial repeatedly.
	require.Eventually(t, func() bool { return dials.Load() >= 3 }, 2*time.Second, 5*time.Millisecond)
}

func TestManager_DisabledSourceRunsNothing(t *testing.T) {
	var dials atomic.Int64
	m := testManager(t, func(ctx context.Context, host string, port int) (io.ReadCloser, error) {
		dials.Add(1)
		return io.NopCloser(strings.NewReader("")), nil
	})
	m.Apply(Source{ID: "off", Transport: TransportTCPClient, Host: "h", Port: 1, Enabled: false})

	st, ok := m.Status("off")
	require.True(t, ok)
	assert.Equal(t, StateDisabled, st.State)
	time.Sleep(20 * time.Millisecond)
	assert.Equal(t, int64(0), dials.Load(), "disabled source must not dial")
}

func TestManager_CountsParseErrors(t *testing.T) {
	// One good sentence + one corrupt checksum.
	data := "$IIMTW,18.6,C\r\n$GPRMC,123519,A,4807.038,N,01131.000,E,022.4,084.4,230394,003.1,W*00\r\n"
	dial, _ := scriptedDial(t, data)
	m := testManager(t, dial)
	m.Apply(Source{ID: "s", Transport: TransportTCPClient, Host: "h", Port: 1, Enabled: true})

	require.Eventually(t, func() bool {
		st, _ := m.Status("s")
		return st.Errors >= 1
	}, 2*time.Second, 5*time.Millisecond)
}

func TestManager_RemoveStops(t *testing.T) {
	dial, dials := scriptedDial(t, fixtureBytes(t))
	m := testManager(t, dial)
	m.Apply(Source{ID: "s", Transport: TransportTCPClient, Host: "h", Port: 1, Enabled: true})
	require.Eventually(t, func() bool { return dials.Load() >= 1 }, 2*time.Second, 5*time.Millisecond)

	m.Remove("s")
	_, ok := m.Status("s")
	assert.False(t, ok)

	// After removal, dialing must stop climbing.
	settled := dials.Load()
	time.Sleep(30 * time.Millisecond)
	assert.LessOrEqual(t, dials.Load()-settled, int64(2), "dialing should cease after Remove")
}

func TestManager_RawTap(t *testing.T) {
	var mu sync.Mutex
	var raw []string
	m := NewManager(context.Background(), &Store{}, ManagerOpts{
		Parser:     fixedParser(),
		Backoff:    time.Millisecond,
		MaxBackoff: time.Millisecond,
		StaleAfter: 50 * time.Millisecond,
		Dial: func(ctx context.Context, host string, port int) (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader("$IIMTW,18.6,C\r\n")), nil
		},
		OnRaw: func(id, line string) {
			mu.Lock()
			raw = append(raw, id+"|"+line)
			mu.Unlock()
		},
	})
	t.Cleanup(m.Close)
	m.Apply(Source{ID: "s", Transport: TransportTCPClient, Host: "h", Port: 1, Enabled: true})

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(raw) >= 1
	}, 2*time.Second, 5*time.Millisecond)
	mu.Lock()
	assert.Contains(t, raw[0], "s|$IIMTW,18.6,C")
	mu.Unlock()
}
