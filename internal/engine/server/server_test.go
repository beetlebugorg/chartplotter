package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const goldenCell = "../../../testdata/US4MD81M.000"

// TestProvisionCoreFromCache bakes the golden cell entirely offline by staging
// it as a cellcache entry, exercising the catalog→download(cache)→bake→manifest
// path without any network access.
func TestProvisionCoreFromCache(t *testing.T) {
	data, err := os.ReadFile(goldenCell)
	if err != nil {
		t.Skipf("golden cell unavailable: %v", err)
	}
	dir := t.TempDir()
	// catalog.json with the cell + a (never-used) URL; the cache short-circuits.
	cat := `{"date":"x","cells":[{"n":"US4MD81M","z":"http://0.0.0.0/never.zip"}]}`
	if err := os.WriteFile(filepath.Join(dir, "catalog.json"), []byte(cat), 0o644); err != nil {
		t.Fatal(err)
	}
	// Stage the cell in the canonical ALL_ENCs.zip layout (ENC_ROOT/<CELL>/<CELL>.000).
	encDir := filepath.Join(dir, "ENC_ROOT", "US4MD81M")
	if err := os.MkdirAll(encDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(encDir, "US4MD81M.000"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := ProvisionCore(dir, []string{"US4MD81M"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Cells != 1 || res.Tiles == 0 {
		t.Fatalf("unexpected result: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(dir, "charts-user.pmtiles")); err != nil {
		t.Errorf("charts-user.pmtiles not written: %v", err)
	}
	man, err := os.ReadFile(filepath.Join(dir, "charts-user.json"))
	if err != nil {
		t.Fatalf("charts-user.json not written: %v", err)
	}
	var m struct {
		Cells  []string  `json:"cells"`
		Bounds []float64 `json:"bounds"`
	}
	if err := json.Unmarshal(man, &m); err != nil {
		t.Fatalf("manifest invalid: %v", err)
	}
	if len(m.Cells) != 1 || m.Cells[0] != "US4MD81M" || len(m.Bounds) != 4 {
		t.Errorf("manifest wrong: %+v", m)
	}
	// Bounds must be the cell's real extent, not the degenerate full-world bbox
	// (the z0 spec-display tile would otherwise make them global).
	if m.Bounds[0] <= -179 || m.Bounds[2] >= 179 || m.Bounds[1] <= -84 || m.Bounds[3] >= 84 {
		t.Errorf("bounds look like the whole world, expected the cell extent: %v", m.Bounds)
	}
}

// TestProvisionCoreLegacyCache verifies the pre-ENC_ROOT flat cache
// (dir/.cellcache-<CELL>.000) is still honoured so an upgrade doesn't force a
// re-download of cells already on disk.
func TestProvisionCoreLegacyCache(t *testing.T) {
	data, err := os.ReadFile(goldenCell)
	if err != nil {
		t.Skipf("golden cell unavailable: %v", err)
	}
	dir := t.TempDir()
	cat := `{"date":"x","cells":[{"n":"US4MD81M","z":"http://0.0.0.0/never.zip"}]}`
	if err := os.WriteFile(filepath.Join(dir, "catalog.json"), []byte(cat), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".cellcache-US4MD81M.000"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := ProvisionCore(dir, []string{"US4MD81M"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Cells != 1 || res.Tiles == 0 {
		t.Fatalf("legacy-cache provision failed: %+v", res)
	}
}

// DELETE /api/charts must remove the map-selected (cell-list) bake too, not just
// region archives — otherwise "remove all" leaves charts on disk.
func TestDeleteRemovesUserBake(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{userPMTiles, userManifest} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(`{"x":1}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ts := httptest.NewServer(New("", dir, false))
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/charts", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	for _, n := range []string{userPMTiles, userManifest} {
		if _, err := os.Stat(filepath.Join(dir, n)); !os.IsNotExist(err) {
			t.Errorf("%s still present after DELETE /api/charts (err=%v)", n, err)
		}
	}
}

func TestDebugEndpoint(t *testing.T) {
	dir := t.TempDir()
	srv := New("", dir, false)
	srv.Version = "test-1.2.3"
	ts := httptest.NewServer(srv)
	defer ts.Close()

	// GET before any client push: server state present, client null.
	resp, err := http.Get(ts.URL + "/api/debug")
	if err != nil {
		t.Fatal(err)
	}
	var d struct {
		Server map[string]any  `json:"server"`
		Client json.RawMessage `json:"client"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
		t.Fatalf("decode debug: %v", err)
	}
	resp.Body.Close()
	if d.Server["version"] != "test-1.2.3" {
		t.Errorf("server.version = %v", d.Server["version"])
	}
	if string(d.Client) != "null" {
		t.Errorf("client should be null before a push, got %s", d.Client)
	}

	// POST a client snapshot, then GET echoes it back.
	snap := `{"inspect":{"selected":{"properties":{"class":"BOYLAT","cell":"US4MD81M"}}}}`
	if _, err := http.Post(ts.URL+"/api/debug", "application/json", strings.NewReader(snap)); err != nil {
		t.Fatal(err)
	}
	resp, _ = http.Get(ts.URL + "/api/debug")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `"US4MD81M"`) || !strings.Contains(string(body), `"BOYLAT"`) {
		t.Errorf("debug GET did not echo client snapshot: %s", body)
	}

	// Non-JSON POST is rejected.
	resp, _ = http.Post(ts.URL+"/api/debug", "application/json", strings.NewReader("not json"))
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("non-JSON snapshot: status %d, want 400", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestServeStaticAndRange(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>hi</html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	body := []byte("0123456789abcdef")
	if err := os.WriteFile(filepath.Join(dir, "data.pmtiles"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(New(dir, dir, false))
	defer ts.Close()

	// Root serves index.html.
	resp, _ := http.Get(ts.URL + "/")
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(got) != "<html>hi</html>" {
		t.Errorf("index: got %q", got)
	}

	// Range request → 206 with the requested slice + content-range.
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

func TestAPIEndpoints(t *testing.T) {
	dir := t.TempDir()
	ts := httptest.NewServer(New(dir, dir, false))
	defer ts.Close()

	// /api/tasks with no job → {"task":null}.
	resp, _ := http.Get(ts.URL + "/api/tasks")
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.TrimSpace(string(got)) != `{"task":null}` {
		t.Errorf("tasks: got %q", got)
	}

	// /api/health → ok.
	resp, _ = http.Get(ts.URL + "/api/health")
	got, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.TrimSpace(string(got)) != `{"ok":true}` {
		t.Errorf("health: got %q", got)
	}

	// POST /api/provision with bad cell → 400.
	resp, _ = http.Post(ts.URL+"/api/provision", "application/json", strings.NewReader(`{"cells":["bad!"]}`))
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("bad cell: got %d", resp.StatusCode)
	}

	// DELETE /api/charts → ok (absent files are fine).
	req, _ := http.NewRequest("DELETE", ts.URL+"/api/charts", nil)
	resp, _ = http.DefaultClient.Do(req)
	got, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.TrimSpace(string(got)) != `{"ok":true}` {
		t.Errorf("delete charts: got %q", got)
	}
}

func TestAPIHostCheck(t *testing.T) {
	dir := t.TempDir()
	srv := New(dir, dir, false) // loopback-only: non-local Host must be rejected
	ts := httptest.NewServer(srv)
	defer ts.Close()

	req, _ := http.NewRequest("GET", ts.URL+"/api/tasks", nil)
	req.Host = "evil.com"
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("non-local host: got %d, want 403", resp.StatusCode)
	}
}
