package baker

import (
	"bytes"
	"os"
	"testing"
)

// Some S-57 producers pad the file after the last record (spaces, nulls, or
// ASCII zeros). The ISO 8211 parser must treat a blank/zero-length leader as
// end-of-records instead of failing (the symptom: "record length must be >= 24,
// got 0" mid-parse on foreign cells like 2WBDK017).
func TestTrailingPaddingTolerated(t *testing.T) {
	data, err := os.ReadFile("../../../testdata/US4MD81M.000")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	base, err := ParseCellBytes("US4MD81M.000", data)
	if err != nil {
		t.Fatalf("base parse: %v", err)
	}
	pads := map[string][]byte{
		"spaces":      bytes.Repeat([]byte{' '}, 48),
		"nulls":       bytes.Repeat([]byte{0}, 48),
		"ascii-zeros": bytes.Repeat([]byte{'0'}, 48),
	}
	for name, pad := range pads {
		padded := append(append([]byte{}, data...), pad...)
		c, err := ParseCellBytes("US4MD81M.000", padded)
		if err != nil {
			t.Errorf("%s padding: parse failed: %v", name, err)
			continue
		}
		if c.FeatureCount() != base.FeatureCount() {
			t.Errorf("%s padding: feature count %d != base %d", name, c.FeatureCount(), base.FeatureCount())
		}
	}
}

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
