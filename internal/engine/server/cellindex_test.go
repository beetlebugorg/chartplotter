package server

import (
	"os"
	"path/filepath"
	"testing"
)

// TestBBoxOverlapsAny — the ?active overlap test.
func TestBBoxOverlapsAny(t *testing.T) {
	world := [4]float64{-180, -90, 180, 90}
	cell := [4]float64{-5.13, 15.0, -5.0, 15.13}
	if !bboxOverlapsAny(cell, [][4]float64{world}) {
		t.Error("cell should overlap the world pack")
	}
	far := [4]float64{100, -40, 120, -20}
	if bboxOverlapsAny(cell, [][4]float64{far}) {
		t.Error("cell should NOT overlap a disjoint pack")
	}
	if bboxOverlapsAny(cell, nil) {
		t.Error("no packs ⇒ not active")
	}
}

// TestCellIndexBuild — backfill reads a cached cell's header once and records its
// bounds; a reload from disk sees the same.
func TestCellIndexBuild(t *testing.T) {
	const cell = "US4MD81M"
	data, err := os.ReadFile("../../../testdata/" + cell + ".000")
	if err != nil {
		t.Skipf("testdata cell absent: %v", err)
	}
	dir := t.TempDir()
	cdir := filepath.Join(dir, "ENC_ROOT", cell)
	if err := os.MkdirAll(cdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cdir, cell+".000"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	ci := newCellIndex(dir)
	ci.build()
	bb, ok := ci.get(cell)
	if !ok {
		t.Fatal("cell not indexed after build")
	}
	if !(bb[0] < bb[2] && bb[1] < bb[3]) {
		t.Errorf("degenerate bounds %v", bb)
	}
	// Persisted: a fresh index loads the same bounds without re-parsing.
	if bb2, ok := newCellIndex(dir).get(cell); !ok || bb2 != bb {
		t.Errorf("reload mismatch: %v vs %v (ok=%v)", bb2, bb, ok)
	}
}
