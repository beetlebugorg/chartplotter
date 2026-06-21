package server

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/beetlebugorg/chartplotter/internal/engine/pmtiles"
)

// writeTestPMTiles drops a prebaked archive with one tile at z/x/y into
// <dir>/tiles/<name>.pmtiles and returns the tile body.
func writeTestPMTiles(t *testing.T, dir, name string, z uint8, x, y uint32) []byte {
	t.Helper()
	body := []byte("test-mvt-body")
	b := pmtiles.New()
	b.AddTile(z, x, y, body)
	b.SetBounds(-76.5, 38.9, -76.3, 39.1)
	if err := os.MkdirAll(tilesDir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tilesDir(dir), name+".pmtiles"), b.Finish(), 0o644); err != nil {
		t.Fatal(err)
	}
	return body
}

func TestServeTileSet(t *testing.T) {
	dir := t.TempDir()
	body := writeTestPMTiles(t, dir, "charts", 8, 10, 20)

	ts := httptest.NewServer(New(dir, dir, dir, false))
	defer ts.Close()

	// A present tile → 200 with the MVT body and the vector-tile content type.
	req, _ := http.NewRequest("GET", ts.URL+"/tiles/charts/8/10/20.mvt", nil)
	req.Header.Set("Accept-Encoding", "identity") // ask for un-gzipped so we can compare bytes
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("tile status: got %d, want 200", resp.StatusCode)
	}
	if !bytes.Equal(got, body) {
		t.Errorf("tile body: got %q, want %q", got, body)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/vnd.mapbox-vector-tile" {
		t.Errorf("content-type: got %q", ct)
	}
	if resp.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("missing CORS header")
	}

	// The .mvt suffix is optional.
	resp, _ = http.Get(ts.URL + "/tiles/charts/8/10/20")
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("no-suffix tile: got %d, want 200", resp.StatusCode)
	}

	// A blank/missing tile → 204.
	resp, _ = http.Get(ts.URL + "/tiles/charts/8/0/0.mvt")
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("missing tile: got %d, want 204", resp.StatusCode)
	}

	// An unknown set → 404.
	resp, _ = http.Get(ts.URL + "/tiles/nope/8/10/20.mvt")
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown set: got %d, want 404", resp.StatusCode)
	}

	// Bad coords → 400.
	resp, _ = http.Get(ts.URL + "/tiles/charts/8/10")
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("bad path: got %d, want 400", resp.StatusCode)
	}
}

// TestServeTileGzip checks the wire gzip path round-trips to the same MVT body.
func TestServeTileGzip(t *testing.T) {
	dir := t.TempDir()
	body := writeTestPMTiles(t, dir, "charts", 8, 10, 20)
	ts := httptest.NewServer(New(dir, dir, dir, false))
	defer ts.Close()

	// Go's transport transparently decodes gzip unless we set Accept-Encoding
	// ourselves; do so to inspect the encoded response.
	req, _ := http.NewRequest("GET", ts.URL+"/tiles/charts/8/10/20.mvt", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.Header.Get("Content-Encoding") != "gzip" {
		t.Fatalf("content-encoding: got %q, want gzip", resp.Header.Get("Content-Encoding"))
	}
	zr, err := gzip.NewReader(resp.Body)
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	got, _ := io.ReadAll(zr)
	if !bytes.Equal(got, body) {
		t.Errorf("gunzipped body: got %q, want %q", got, body)
	}
}

func TestServeTileJSONAndList(t *testing.T) {
	dir := t.TempDir()
	writeTestPMTiles(t, dir, "charts", 8, 10, 20)
	ts := httptest.NewServer(New(dir, dir, dir, false))
	defer ts.Close()

	// TileJSON descriptor.
	resp, _ := http.Get(ts.URL + "/tiles/charts.json")
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("tilejson status: got %d", resp.StatusCode)
	}
	var tj struct {
		TileJSON string    `json:"tilejson"`
		Tiles    []string  `json:"tiles"`
		MinZoom  int       `json:"minzoom"`
		MaxZoom  int       `json:"maxzoom"`
		Bounds   []float64 `json:"bounds"`
	}
	if err := json.Unmarshal(got, &tj); err != nil {
		t.Fatalf("tilejson parse: %v (%s)", err, got)
	}
	if tj.MaxZoom != 8 || len(tj.Tiles) != 1 {
		t.Errorf("tilejson: %+v", tj)
	}
	if len(tj.Bounds) != 4 || tj.Bounds[0] > -76.4 {
		t.Errorf("tilejson bounds: %v", tj.Bounds)
	}

	// Set listing.
	resp, _ = http.Get(ts.URL + "/tiles/")
	got, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	var list struct {
		Sets []string `json:"sets"`
	}
	if err := json.Unmarshal(got, &list); err != nil {
		t.Fatalf("list parse: %v (%s)", err, got)
	}
	if len(list.Sets) != 1 || list.Sets[0] != "charts" {
		t.Errorf("set list: %v", list.Sets)
	}
}
