package plugin

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/beetlebugorg/chartplotter/internal/engine/nmea"
	"github.com/stretchr/testify/require"
)

// parity_test.go is the Phase-1 acceptance test (spec §12): the reference
// core.tcp-client WASM plugin, driven end-to-end through the real Manager → broker →
// wazero → tcp.connect pipeline, must reproduce the exact vessel/AIS state the
// built-in nmea parse path produces from the same sentences.

// sampleSentences: a representative instrument mix plus two AIS reports.
var sampleSentences = []string{
	"$GPRMC,123519,A,4807.038,N,01131.000,E,022.4,084.4,230394,003.1,W*6A",
	"$GPGGA,123519,4807.038,N,01131.000,E,1,08,0.9,545.4,M,46.9,M,,*47",
	"$GPVTG,084.4,T,081.6,M,022.4,N,041.5,K*43",
	"$HEHDT,274.07,T*03",
	"$IIDPT,4.1,0.5,*7F",
	"$IIMTW,17.5,C*1A",
	"!AIVDM,1,1,,B,23aDqDOP0S0:mk2Kv3Ip=wvpR>`<,0*3D",
	"!AIVDM,1,1,,A,H42O55i18tMET00000000000000,2*6D",
}

// builtinResult computes the vessel + AIS state the built-in runner's readLoop
// branch (nmea/source.go) would produce from lines.
func builtinResult(lines []string) (nmea.VesselState, []nmea.AISTarget) {
	store := &nmea.Store{}
	parser := &nmea.Parser{}
	ais := nmea.NewAISStore(0)
	feed := ais.Feeder()
	for _, line := range lines {
		s, err := nmea.ParseSentence(line)
		if err != nil {
			continue
		}
		if s.Type == "VDM" || s.Type == "VDO" {
			feed(line)
			continue
		}
		store.Apply(parser, s)
	}
	return store.Snapshot(), ais.Snapshot()
}

// testHost adapts the plugin broker's Host onto real nmea stores.
type testHost struct {
	vessel *nmea.Store
	ais    *nmea.AISStore
	mu     sync.Mutex
	raw    []string
	logs   []string
}

func (h *testHost) PublishVessel(source string, deltas []nmea.Delta) {
	h.vessel.PublishDeltas(source, deltas)
}
func (h *testHost) PublishAIS(source string, targets []nmea.AISTarget) {
	for _, t := range targets {
		h.ais.Upsert(t, source)
	}
}
func (h *testHost) PublishRaw(source string, lines []string) {
	h.mu.Lock()
	h.raw = append(h.raw, lines...)
	h.mu.Unlock()
}
func (h *testHost) UpdateStatus(id string, st PluginStatus) {
	h.mu.Lock()
	h.logs = append(h.logs, "status "+st.State+": "+st.Detail)
	h.mu.Unlock()
}
func (h *testHost) Log(id, level, msg string) {
	h.mu.Lock()
	h.logs = append(h.logs, "log ["+level+"] "+msg)
	h.mu.Unlock()
}

func TestTCPClientPluginParity(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping wasm parity test in -short mode")
	}
	wasmPath := buildPluginWasm(t)

	// Fake NMEA server: accept one connection, write every sentence, keep it open.
	addr, port := startNMEAServer(t, sampleSentences)

	// Lay out an installed, enabled plugin under a temp data dir.
	dataDir := t.TempDir()
	verDir := filepath.Join(dataDir, "plugins", "core.tcp-client", "1.0.0")
	require.NoError(t, os.MkdirAll(verDir, 0o755))
	wasmBytes, err := os.ReadFile(wasmPath)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(verDir, "plugin.wasm"), wasmBytes, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(verDir, "plugin.json"), []byte(testManifest), 0o644))

	stateJSON := `[{"id":"core.tcp-client","version":"1.0.0","enabled":true,` +
		`"grants":[{"cap":"vessel.write"},{"cap":"ais.write"},{"cap":"net.tcp-client","hosts":["127.0.0.1"]}],` +
		`"config":{"host":"127.0.0.1","port":` + strconv.Itoa(port) + `}}]`
	require.NoError(t, os.WriteFile(filepath.Join(dataDir, "plugins.json"), []byte(stateJSON), 0o644))

	host := &testHost{vessel: &nmea.Store{}, ais: nmea.NewAISStore(0)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mgr := NewManager(ctx, ManagerOpts{DataDir: dataDir, Host: host})
	defer mgr.Close()

	wantVessel, wantAIS := builtinResult(sampleSentences)

	// Poll until the host store converges (the plugin batches at ~75 ms).
	ok := false
	for i := 0; i < 60; i++ {
		if vesselJSON(host.vessel.Snapshot()) == vesselJSON(wantVessel) &&
			aisJSON(host.ais.Snapshot()) == aisJSON(wantAIS) {
			ok = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !ok {
		host.mu.Lock()
		t.Logf("plugin logs:\n%s", strings.Join(host.logs, "\n"))
		host.mu.Unlock()
		t.Logf("want vessel: %s", vesselJSON(wantVessel))
		t.Logf("got  vessel: %s", vesselJSON(host.vessel.Snapshot()))
		t.Logf("want ais: %s", aisJSON(wantAIS))
		t.Logf("got  ais: %s", aisJSON(host.ais.Snapshot()))
		t.Fatal("plugin state did not converge")
	}

	require.JSONEq(t, vesselJSON(wantVessel), vesselJSON(host.vessel.Snapshot()))
	require.JSONEq(t, aisJSON(wantAIS), aisJSON(host.ais.Snapshot()))

	// Raw sentences reached the sniffer path.
	host.mu.Lock()
	require.NotEmpty(t, host.raw, "expected raw sentences published to the sniffer")
	host.mu.Unlock()
	_ = addr
}

const testManifest = `{
  "manifestVersion": 1,
  "id": "core.tcp-client",
  "name": "TCP Client (NMEA0183)",
  "version": "1.0.0",
  "apiVersion": 1,
  "entry": { "wasm": "plugin.wasm" },
  "capabilities": [
    { "cap": "vessel.write" },
    { "cap": "ais.write" },
    { "cap": "net.tcp-client", "hosts": ["${config:host}"] }
  ]
}`

// buildPluginWasm compiles the reference plugin to wasip1 and returns the path.
func buildPluginWasm(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	require.NoError(t, err)
	out := filepath.Join(t.TempDir(), "plugin.wasm")
	cmd := exec.Command("go", "build", "-o", out, "./plugins/core.tcp-client")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm", "CGO_ENABLED=0")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build plugin wasm: %v\n%s", err, b)
	}
	return out
}

// startNMEAServer listens on a loopback port, writes every line to the first client,
// and holds the connection open until the test ends.
func startNMEAServer(t *testing.T, lines []string) (string, int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		_, _ = conn.Write([]byte(strings.Join(lines, "\r\n") + "\r\n"))
		// Hold open so onClose doesn't fire mid-test; closed by ln.Close via cleanup.
		buf := make([]byte, 256)
		for {
			if _, err := conn.Read(buf); err != nil {
				return
			}
		}
	}()
	_, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(portStr)
	return ln.Addr().String(), port
}

// vesselJSON marshals a VesselState for comparison, zeroing the non-deterministic
// Updated timestamp.
func vesselJSON(vs nmea.VesselState) string {
	vs.Updated = time.Time{}
	b, _ := json.Marshal(vs)
	return string(b)
}

// aisJSON marshals AIS targets for comparison, zeroing per-target LastSeen.
func aisJSON(targets []nmea.AISTarget) string {
	cp := make([]nmea.AISTarget, len(targets))
	copy(cp, targets)
	for i := range cp {
		cp[i].LastSeen = time.Time{}
	}
	b, _ := json.Marshal(cp)
	return string(b)
}
