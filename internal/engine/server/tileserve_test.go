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
	"github.com/beetlebugorg/chartplotter/internal/engine/tilesource"
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
	// No ?g generation token → revalidate-always (live/dynamic set semantics).
	if cc := resp.Header.Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("tile without ?g: cache-control got %q, want no-cache", cc)
	}

	// A ?g generation token → the tile URL is content-addressed, so cache it
	// immutably (baked-pack semantics; the client busts by a new ?g on re-bake).
	req, _ = http.NewRequest("GET", ts.URL+"/tiles/charts/8/10/20.mvt?g=1699999999", nil)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if cc := resp.Header.Get("Cache-Control"); cc != "public, max-age=31536000, immutable" {
		t.Errorf("tile with ?g: cache-control got %q, want immutable", cc)
	}
	// A zero token is treated as no generation (not immutable).
	resp, _ = http.Get(ts.URL + "/tiles/charts/8/10/20.mvt?g=0")
	resp.Body.Close()
	if cc := resp.Header.Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("tile with ?g=0: cache-control got %q, want no-cache", cc)
	}

	// The .mvt suffix is optional.
	resp, _ = http.Get(ts.URL + "/tiles/charts/8/10/20")
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("no-suffix tile: got %d, want 200", resp.StatusCode)
	}

	// A blank/missing tile → 204, and it still carries the cache header (an empty
	// tile is content-addressed per ?g just like a full one — cache the ocean).
	resp, _ = http.Get(ts.URL + "/tiles/charts/8/0/0.mvt?g=1699999999")
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("missing tile: got %d, want 204", resp.StatusCode)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "public, max-age=31536000, immutable" {
		t.Errorf("empty tile with ?g: cache-control got %q, want immutable", cc)
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

// The TileJSON `engine` field distinguishes BAKE-TIME truth from the running
// binary: a pack reports the engine commit stamped into its .enginever sidecar
// when it was baked; a pack without the sidecar (baked before stamping) reports
// "pre-stamp"; a DYNAMIC set (no pack path — plugin tiles) generates tiles in
// the running binary, so it reports the build's own EngineCommit.
func TestServeTileJSONEngineStamp(t *testing.T) {
	dir := t.TempDir()
	writeTestPMTiles(t, dir, "stamped", 8, 10, 20)
	writeTestPMTiles(t, dir, "legacy", 8, 10, 20)
	// Bake-time stamp for one pack only (what writeAndRegister/bakeBundleTile57 write).
	stamped := filepath.Join(tilesDir(dir), "stamped.pmtiles")
	if err := os.WriteFile(stamped+engineVerExt, []byte("abc123def\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	srv := New(dir, dir, dir, false)
	srv.EngineCommit = "fff999000" // the RUNNING binary's engine
	// A dynamic set: registered without a pack path (plugin tiles).
	live, err := tilesource.Open(filepath.Join(tilesDir(dir), "legacy.pmtiles"))
	if err != nil {
		t.Fatal(err)
	}
	srv.RegisterTileSet("live57", live)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	engineOf := func(set string) string {
		t.Helper()
		resp, err := http.Get(ts.URL + "/tiles/" + set + ".json")
		if err != nil {
			t.Fatal(err)
		}
		got, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		var tj struct {
			Engine string `json:"engine"`
		}
		if err := json.Unmarshal(got, &tj); err != nil {
			t.Fatalf("tilejson parse: %v (%s)", err, got)
		}
		return tj.Engine
	}
	if got := engineOf("stamped"); got != "abc123def" {
		t.Errorf("stamped pack engine: got %q, want the bake-time sidecar commit", got)
	}
	if got := engineOf("legacy"); got != "pre-stamp" {
		t.Errorf("legacy pack engine: got %q, want \"pre-stamp\"", got)
	}
	if got := engineOf("live57"); got != "fff999000" {
		t.Errorf("live set engine: got %q, want the running binary's EngineCommit", got)
	}
}

// An MLT archive (the tile57 default bake format; PMTiles header tile_type 6)
// must advertise `"encoding":"mlt"` in its TileJSON — the hint maplibre-gl
// propagates onto the vector source to select its native MLT decoder — and its
// tiles (bytes-verbatim, no transcode) go out as application/octet-stream since
// MLT has no registered media type.
func TestServeTileSetMLT(t *testing.T) {
	dir := t.TempDir()
	body := writeTestPMTiles(t, dir, "mltcharts", 8, 10, 20)
	// The Go pmtiles Builder always writes tile_type MVT (it only bakes legacy
	// archives); flip header byte 99 to 6 (MLT) — what a tile57 MLT bake stores.
	path := filepath.Join(tilesDir(dir), "mltcharts.pmtiles")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw[99] = 6
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	ts := httptest.NewServer(New(dir, dir, dir, false))
	defer ts.Close()

	resp, _ := http.Get(ts.URL + "/tiles/mltcharts.json")
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var tj struct {
		Format   string `json:"format"`
		Encoding string `json:"encoding"`
	}
	if err := json.Unmarshal(got, &tj); err != nil {
		t.Fatalf("tilejson parse: %v (%s)", err, got)
	}
	if tj.Encoding != "mlt" || tj.Format != "mlt" {
		t.Errorf(`tilejson encoding/format: got %q/%q, want "mlt"/"mlt" (%s)`, tj.Encoding, tj.Format, got)
	}

	req, _ := http.NewRequest("GET", ts.URL+"/tiles/mltcharts/8/10/20.mvt", nil)
	req.Header.Set("Accept-Encoding", "identity")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	tile, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("tile status: got %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/octet-stream" {
		t.Errorf("MLT tile content-type: got %q, want application/octet-stream", ct)
	}
	if !bytes.Equal(tile, body) {
		t.Errorf("MLT tile body must serve verbatim: got %q, want %q", tile, body)
	}
}
