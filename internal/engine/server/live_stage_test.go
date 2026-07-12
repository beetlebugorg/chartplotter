package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/beetlebugorg/chartplotter/internal/engine/tilesource"
)

// TestEngineRebakeStagesAndSwapsLast: an engine change re-bakes into the staging tree
// (tiles.next) while the previously baked archives stay in place, and replaces them only
// as the LAST step, after the whole bake succeeded — a staged bake that produces nothing
// leaves the served tiles (and their stamp) untouched.
func TestEngineRebakeStagesAndSwapsLast(t *testing.T) {
	cell, err := os.ReadFile("../../../testdata/US5MD1MC.000")
	if err != nil {
		t.Skipf("testdata cell absent: %v", err)
	}
	cacheDir, dataDir := t.TempDir(), t.TempDir()
	s := New(t.TempDir(), cacheDir, dataDir, false, "engine-B")

	// The provider's source ENC_ROOT holds the real cell.
	src := filepath.Join(s.districtDir("noaa", "d5"), "US5MD1MC.000")
	if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, cell, 0o644); err != nil {
		t.Fatal(err)
	}

	// A previously served tree, baked by another engine build.
	tiles := s.liveCellsDir("noaa")
	old := filepath.Join(tiles, "d5", "OLD.pmtiles")
	if err := os.MkdirAll(filepath.Dir(old), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(old, []byte("previous archive"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tiles, ".enginever"), []byte("engine-A"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A staged bake over an ENC root with no cells produces nothing: the served
	// tree must survive, stamp and all, and the staging tree must be cleaned up.
	n, ts, err := s.prepareLiveProvider("job-empty", t.TempDir(), "noaa")
	if err != nil {
		t.Fatalf("empty staged bake errored: %v", err)
	}
	if n != 0 || ts != nil {
		t.Fatalf("empty staged bake: n=%d src=%v, want 0/nil", n, ts)
	}
	if _, err := os.Stat(old); err != nil {
		t.Error("failed staged bake dropped the served tiles")
	}
	if b, _ := os.ReadFile(filepath.Join(tiles, ".enginever")); string(b) != "engine-A" {
		t.Errorf("served tree re-stamped by a failed staged bake: %q", b)
	}
	if _, err := os.Stat(tiles + stagingSuffix); !os.IsNotExist(err) {
		t.Error("empty staging tree not cleaned up")
	}

	// The real staged bake fills tiles.next and swaps it into place — only now do
	// the old archives disappear, replaced by the fresh tree with the new stamp.
	n, ts, err = s.prepareLiveProvider("job-real", s.encRootDir("noaa"), "noaa")
	if err != nil || n == 0 || ts == nil {
		t.Fatalf("staged re-bake: n=%d err=%v", n, err)
	}
	defer func() { _ = tilesource.Close(ts) }()
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Error("old archive survived the swap")
	}
	if _, err := os.Stat(filepath.Join(tiles, "d5", "US5MD1MC.pmtiles")); err != nil {
		t.Errorf("swapped tree missing the fresh archive: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(tiles, ".enginever")); string(b) != "engine-B" {
		t.Errorf("swapped tree stamp = %q, want engine-B", b)
	}
	if _, err := os.Stat(tiles + stagingSuffix); !os.IsNotExist(err) {
		t.Error("staging tree left behind after the swap")
	}
}
