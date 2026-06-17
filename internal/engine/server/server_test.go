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
	if err := os.WriteFile(filepath.Join(dir, ".cellcache-US4MD81M.000"), data, 0o644); err != nil {
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
	ts := httptest.NewServer(New(dir, false))
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
	ts := httptest.NewServer(New(dir, false))
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
	srv := New(dir, false) // loopback-only: non-local Host must be rejected
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
