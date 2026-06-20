package tilesource

import (
	"bytes"
	"compress/gzip"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// writeMBTiles builds a minimal .mbtiles archive at path with one gzipped vector
// tile and the standard metadata rows, then returns the path.
func writeMBTiles(t *testing.T, z uint8, x, tmsRow uint32, body []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.mbtiles")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	stmts := []string{
		`CREATE TABLE metadata (name text, value text)`,
		`CREATE TABLE tiles (zoom_level integer, tile_column integer, tile_row integer, tile_data blob)`,
		`INSERT INTO metadata VALUES ('minzoom','3'),('maxzoom','12'),('format','pbf'),('bounds','-76.5,38.9,-76.3,39.1')`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("exec %q: %v", s, err)
		}
	}
	if _, err := db.Exec(`INSERT INTO tiles VALUES (?,?,?,?)`, z, x, tmsRow, body); err != nil {
		t.Fatalf("insert tile: %v", err)
	}
	return path
}

func gzipBytes(t *testing.T, b []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	zw.Write(b)
	zw.Close()
	return buf.Bytes()
}

func TestMBTilesReadAndFlip(t *testing.T) {
	plain := []byte("mvt-body-for-z5-x1-y2")
	// XYZ y=2 at z=5 stores at TMS row (1<<5)-1-2 = 29.
	path := writeMBTiles(t, 5, 1, 29, gzipBytes(t, plain))

	src, err := OpenMBTiles(path)
	if err != nil {
		t.Fatalf("OpenMBTiles: %v", err)
	}
	defer src.Close()

	got, err := src.Tile(5, 1, 2)
	if err != nil {
		t.Fatalf("Tile: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("Tile = %q, want decompressed %q", got, plain)
	}
	// A tile that isn't present reads as blank (nil, nil).
	if got, err := src.Tile(5, 9, 9); err != nil || got != nil {
		t.Fatalf("missing tile = (%q, %v), want (nil, nil)", got, err)
	}

	m := src.Meta()
	if m.MinZoom != 3 || m.MaxZoom != 12 {
		t.Fatalf("zoom = [%d,%d], want [3,12]", m.MinZoom, m.MaxZoom)
	}
	if !m.Gzipped {
		t.Fatalf("Gzipped = false, want true (format=pbf)")
	}
	if d := m.W - -76.5; d > 1e-9 || d < -1e-9 {
		t.Fatalf("W = %v, want -76.5", m.W)
	}
	if d := m.N - 39.1; d > 1e-9 || d < -1e-9 {
		t.Fatalf("N = %v, want 39.1", m.N)
	}
}

// TestMBTilesPlainTile checks an un-gzipped tile body passes through unchanged.
func TestMBTilesPlainTile(t *testing.T) {
	plain := []byte("not-gzipped")
	path := writeMBTiles(t, 0, 0, 0, plain)
	src, err := OpenMBTiles(path)
	if err != nil {
		t.Fatalf("OpenMBTiles: %v", err)
	}
	defer src.Close()
	got, err := src.Tile(0, 0, 0)
	if err != nil {
		t.Fatalf("Tile: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("Tile = %q, want %q", got, plain)
	}
}

// TestOpenDispatch checks Open picks the backend by extension and that both
// archive backends satisfy TileSource.
func TestOpenDispatch(t *testing.T) {
	path := writeMBTiles(t, 1, 0, 1, []byte("x"))
	src, err := Open(path)
	if err != nil {
		t.Fatalf("Open(.mbtiles): %v", err)
	}
	defer Close(src)
	if _, ok := src.(*MBTiles); !ok {
		t.Fatalf("Open returned %T, want *MBTiles", src)
	}
	if _, err := Open("nope.txt"); err == nil {
		t.Fatalf("Open(.txt) = nil error, want unrecognised-archive error")
	}
}
