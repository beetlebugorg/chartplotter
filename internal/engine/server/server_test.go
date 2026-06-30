package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServeStaticAndRange(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>hi</html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	body := []byte("0123456789abcdef")
	if err := os.WriteFile(filepath.Join(dir, "data.pmtiles"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(New(dir, dir, dir, false))
	defer ts.Close()

	// Root serves index.html.
	resp, _ := http.Get(ts.URL + "/")
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(got) != "<html>hi</html>" {
		t.Errorf("index: got %q", got)
	}

	// Range request → 206 with the requested slice + content-range + CORS.
	req, _ := http.NewRequest("GET", ts.URL+"/data.pmtiles", nil)
	req.Header.Set("Range", "bytes=4-7")
	resp, _ = http.DefaultClient.Do(req)
	got, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent {
		t.Errorf("range status: got %d", resp.StatusCode)
	}
	if string(got) != "4567" {
		t.Errorf("range body: got %q", got)
	}
	if cr := resp.Header.Get("Content-Range"); cr != "bytes 4-7/16" {
		t.Errorf("content-range: got %q", cr)
	}
	if resp.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("missing CORS header")
	}
}

// The 100%-wasm path: /api/cell serves a cached raw cell; bad names 400; an
// uncached cell with no NOAA url 502.
func TestServeCell(t *testing.T) {
	dir := t.TempDir()
	cell := []byte("S57-CELL-BYTES")
	cp := filepath.Join(dir, "ENC_ROOT", "US5MD1MC", "US5MD1MC.000")
	if err := os.MkdirAll(filepath.Dir(cp), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cp, cell, 0o644); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(New(dir, dir, dir, false))
	defer ts.Close()

	resp, _ := http.Get(ts.URL + "/api/cell/US5MD1MC")
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(got) != string(cell) {
		t.Errorf("cached cell: status %d body %q", resp.StatusCode, got)
	}

	resp, _ = http.Get(ts.URL + "/api/cell/bad!name")
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("bad cell name: got %d, want 400", resp.StatusCode)
	}

	resp, _ = http.Get(ts.URL + "/api/cell/US9NOPE") // uncached, no ?url
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("uncached no-url: got %d, want 502", resp.StatusCode)
	}
}

func TestAPIHealthAndHostCheck(t *testing.T) {
	dir := t.TempDir()
	ts := httptest.NewServer(New(dir, dir, dir, false)) // loopback-only
	defer ts.Close()

	resp, _ := http.Get(ts.URL + "/api/health")
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	// /api/health also advertises server capabilities (version, bakeEngines), so
	// match the liveness marker rather than an exact body.
	if !strings.Contains(string(got), `"ok":true`) {
		t.Errorf("health: got %q", got)
	}

	// A non-local Host on /api must be rejected (DNS-rebind defence).
	req, _ := http.NewRequest("GET", ts.URL+"/api/health", nil)
	req.Host = "evil.com"
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("non-local host: got %d, want 403", resp.StatusCode)
	}
}

// TestShareAndUpload covers the "share my view" round-trip: a snapshot POST is
// returned verbatim by GET, an uploaded cell lands in the ENC_ROOT cache so a
// later GET /api/cell serves it, and a fresh Server reloads the snapshot from
// disk (share.json persistence).
func TestShareAndUpload(t *testing.T) {
	dir := t.TempDir()
	ts := httptest.NewServer(New(dir, dir, dir, false))
	defer ts.Close()

	// No snapshot yet → 404.
	resp, _ := http.Get(ts.URL + "/api/share")
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("empty share: got %d, want 404", resp.StatusCode)
	}

	// POST a snapshot, then GET it back verbatim.
	snap := `{"view":{"center":[-76.49,38.97],"zoom":14.2},"cells":[{"n":"US5MD1MC"}]}`
	resp, _ = http.Post(ts.URL+"/api/share", jsonCT, strings.NewReader(snap))
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("post share: got %d", resp.StatusCode)
	}
	resp, _ = http.Get(ts.URL + "/api/share")
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(got) != snap {
		t.Errorf("share round-trip: got %q", got)
	}

	// PUT a cell, then GET /api/cell/<NAME> serves the uploaded bytes (no url).
	cell := []byte("RAW-S57-BYTES")
	req, _ := http.NewRequest("PUT", ts.URL+"/api/cell/US5MD1MC", strings.NewReader(string(cell)))
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("put cell: got %d", resp.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(dir, "ENC_ROOT", "US5MD1MC", "US5MD1MC.000")); err != nil {
		t.Errorf("uploaded cell not cached: %v", err)
	}
	resp, _ = http.Get(ts.URL + "/api/cell/US5MD1MC")
	got, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(got) != string(cell) {
		t.Errorf("get uploaded cell: got %q", got)
	}

	// A bad cell name on PUT is rejected.
	req, _ = http.NewRequest("PUT", ts.URL+"/api/cell/bad..name", strings.NewReader("x"))
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("bad upload name: got %d, want 400", resp.StatusCode)
	}

	// A fresh Server over the same cache dir reloads the snapshot from share.json.
	ts2 := httptest.NewServer(New(dir, dir, dir, false))
	defer ts2.Close()
	resp, _ = http.Get(ts2.URL + "/api/share")
	got, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(got) != snap {
		t.Errorf("share persistence: got %q", got)
	}
}
