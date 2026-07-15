package plugin

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/beetlebugorg/chartplotter/internal/engine/nmea"
	"github.com/stretchr/testify/require"
)

func TestManifestValidate(t *testing.T) {
	valid := `{"manifestVersion":1,"id":"org.example.foo","name":"Foo","version":"1.2.0",` +
		`"apiVersion":1,"entry":{"wasm":"plugin.wasm"}}`
	m, err := ParseManifest([]byte(valid))
	require.NoError(t, err)
	require.Equal(t, "org.example.foo", m.ID)

	bad := []string{
		`{"manifestVersion":2,"id":"org.example.foo","version":"1.0.0","apiVersion":1,"entry":{"wasm":"p"}}`,
		`{"manifestVersion":1,"id":"BadID","version":"1.0.0","apiVersion":1,"entry":{"wasm":"p"}}`,
		`{"manifestVersion":1,"id":"org.example.foo","version":"nope","apiVersion":1,"entry":{"wasm":"p"}}`,
		`{"manifestVersion":1,"id":"org.example.foo","version":"1.0.0","apiVersion":9,"entry":{"wasm":"p"}}`,
		`{"manifestVersion":1,"id":"org.example.foo","version":"1.0.0","apiVersion":1,"entry":{}}`, // no entry point
	}
	for _, b := range bad {
		_, err := ParseManifest([]byte(b))
		require.Error(t, err, b)
	}
}

func TestInstallVerifiesHashesAndRejectsCore(t *testing.T) {
	dir := t.TempDir()
	wasm := []byte("\x00asm fake module bytes")
	sum := sha256.Sum256(wasm)
	man := map[string]any{
		"manifestVersion": 1, "id": "org.example.foo", "name": "Foo", "version": "1.0.0",
		"apiVersion": 1, "entry": map[string]any{"wasm": "plugin.wasm"},
		"files": map[string]string{"plugin.wasm": "sha256:" + hex.EncodeToString(sum[:])},
	}
	manBytes, _ := json.Marshal(man)
	good := writeZip(t, dir, "good.zip", map[string][]byte{"plugin.json": manBytes, "plugin.wasm": wasm})

	pluginsDir := filepath.Join(dir, "plugins")
	m, err := Install(good, pluginsDir, InstallOptions{})
	require.NoError(t, err)
	require.Equal(t, "org.example.foo", m.ID)
	_, err = os.Stat(filepath.Join(pluginsDir, "org.example.foo", "1.0.0", "plugin.wasm"))
	require.NoError(t, err)

	// Tamper: manifest hash no longer matches → verify fails.
	tampered := writeZip(t, dir, "bad.zip", map[string][]byte{"plugin.json": manBytes, "plugin.wasm": []byte("different")})
	_, err = Install(tampered, pluginsDir, InstallOptions{})
	require.ErrorContains(t, err, "hash mismatch")

	// core.* rejected unless AllowCore.
	coreMan := map[string]any{"manifestVersion": 1, "id": "core.foo", "name": "C", "version": "1.0.0",
		"apiVersion": 1, "entry": map[string]any{"wasm": "plugin.wasm"},
		"files": map[string]string{"plugin.wasm": "sha256:" + hex.EncodeToString(sum[:])}}
	coreBytes, _ := json.Marshal(coreMan)
	coreZip := writeZip(t, dir, "core.zip", map[string][]byte{"plugin.json": coreBytes, "plugin.wasm": wasm})
	_, err = Install(coreZip, pluginsDir, InstallOptions{})
	require.ErrorContains(t, err, "reserved")
	_, err = Install(coreZip, pluginsDir, InstallOptions{AllowCore: true})
	require.NoError(t, err)
}

func TestMatchHostAllow(t *testing.T) {
	cases := []struct {
		patterns  []string
		host      string
		port      int
		wantAllow bool
	}{
		{[]string{"sk.local:3000"}, "sk.local", 3000, true},
		{[]string{"sk.local:3000"}, "sk.local", 3001, false},
		{[]string{"sk.local"}, "sk.local", 9999, true},
		{[]string{"*.tile.example.com"}, "a.tile.example.com", 443, true},
		{[]string{"*.tile.example.com"}, "tile.example.com", 443, true},
		{[]string{"*.tile.example.com"}, "evil.com", 443, false},
		{[]string{"192.168.1.10:2000"}, "192.168.1.10", 2000, true},
		{nil, "anything", 80, false},
	}
	for _, c := range cases {
		require.Equal(t, c.wantAllow, matchHostAllow(c.patterns, c.host, c.port), c)
	}
}

func TestVesselPublishDeltasMapping(t *testing.T) {
	store := &nmea.Store{}
	n, err := store.PublishDeltas("pluginX", []nmea.Delta{
		{Path: "navigation.sog", Value: json.RawMessage(`6.2`)},
		{Path: "navigation.position", Value: json.RawMessage(`{"lat":48.1,"lon":11.5}`)},
		{Path: "not.a.real.path", Value: json.RawMessage(`1`)},
	})
	require.Equal(t, 2, n)
	require.ErrorContains(t, err, "unknown vessel path")

	snap := store.Snapshot()
	require.NotNil(t, snap.Navigation.SOG)
	require.InDelta(t, 6.2, *snap.Navigation.SOG, 1e-9)
	require.NotNil(t, snap.Navigation.Position)
	require.InDelta(t, 48.1, snap.Navigation.Position.Lat, 1e-9)
	require.Equal(t, "pluginX", store.Provenance()["navigation.sog"])
}

func TestParseBytes(t *testing.T) {
	require.Equal(t, int64(10<<20), parseBytes("10MB"))
	require.Equal(t, int64(512<<10), parseBytes("512KB"))
	require.Equal(t, int64(1024), parseBytes("1024"))
	require.Equal(t, int64(2048), parseBytes("2048B"))
}

// writeZip builds a zip archive at dir/name from files and returns its path.
func writeZip(t *testing.T, dir, name string, files map[string][]byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	require.NoError(t, err)
	defer f.Close()
	zw := zip.NewWriter(f)
	for n, b := range files {
		w, err := zw.Create(n)
		require.NoError(t, err)
		_, err = w.Write(b)
		require.NoError(t, err)
	}
	require.NoError(t, zw.Close())
	return path
}
