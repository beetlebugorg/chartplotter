package baker

import (
	"os"
	"testing"
)

// A non-US S-57 cell (Netherlands, producer code "1R") must parse and bake just
// like a US cell — the producer code is alphanumeric, not always two letters.
// (The browser zip importer's cell-name regex was US-only; see web/zip-import.mjs.)
func TestForeignCellBakes(t *testing.T) {
	data, err := os.ReadFile("../../../testdata/1R7YM012.000")
	if err != nil {
		t.Fatalf("read foreign cell: %v", err)
	}
	b, ok, err := BuildBaker(map[string][]byte{"1R7YM012.000": data}, func(n string, e error) { t.Errorf("skip %s: %v", n, e) })
	if err != nil {
		t.Fatalf("build baker: %v", err)
	}
	if len(ok) != 1 {
		t.Fatalf("expected 1 cell parsed, got %d", len(ok))
	}
	// Coordinates must decode to the cell's real location (the Netherlands:
	// lon ≈ 5°E, lat ≈ 52.6°N), not a mis-scaled global spread.
	bb := b.Bounds()
	if bb.MinLon < 3 || bb.MaxLon > 7 || bb.MinLat < 51 || bb.MaxLat > 54 {
		t.Fatalf("bounds %v not in the Netherlands — coordinate decode wrong?", bb)
	}
	if pb := BakeToPMTiles(b, nil); pb.Count() == 0 {
		t.Fatal("baked no tiles")
	}
}
