package server

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/beetlebugorg/chartplotter/internal/engine/baker"
)

// cellIndex is a small, persistent name→bounding-box index over the cached source
// cells (<dataDir>/ENC_ROOT/<CELL>/<CELL>.000). It lets the server answer "where
// is cell X" and "which installed cells are active" without re-parsing thousands
// of cells on every request: each cell's header is read ONCE (the bbox cached to
// <dataDir>/cells-index.json), then queries hit the in-memory map. Kept
// deliberately simple — a flat JSON map, not a database; the data is tiny (a few
// floats per cell) and read-mostly.
type cellIndex struct {
	mu      sync.RWMutex
	bbox    map[string][4]float64 // cell stem → [W,S,E,N]
	path    string                // cells-index.json
	encRoot string                // <dataDir>/ENC_ROOT
	built   bool                  // backfill scan finished
}

func newCellIndex(dataDir string) *cellIndex {
	ci := &cellIndex{
		bbox:    map[string][4]float64{},
		path:    filepath.Join(dataDir, "cells-index.json"),
		encRoot: filepath.Join(dataDir, "ENC_ROOT"),
	}
	if data, err := os.ReadFile(ci.path); err == nil {
		_ = json.Unmarshal(data, &ci.bbox)
	}
	return ci
}

// get returns a cell's [W,S,E,N] bounds if indexed.
func (ci *cellIndex) get(name string) ([4]float64, bool) {
	ci.mu.RLock()
	defer ci.mu.RUnlock()
	b, ok := ci.bbox[name]
	return b, ok
}

// snapshot returns a copy of the current index (sorted names + their bboxes).
func (ci *cellIndex) snapshot() ([]string, map[string][4]float64) {
	ci.mu.RLock()
	defer ci.mu.RUnlock()
	names := make([]string, 0, len(ci.bbox))
	out := make(map[string][4]float64, len(ci.bbox))
	for n, b := range ci.bbox {
		names = append(names, n)
		out[n] = b
	}
	sort.Strings(names)
	return names, out
}

func (ci *cellIndex) save() {
	ci.mu.RLock()
	data, err := json.Marshal(ci.bbox)
	ci.mu.RUnlock()
	if err != nil {
		return
	}
	tmp := ci.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return
	}
	_ = os.Rename(tmp, ci.path)
}

// rebuild re-opens the backfill (e.g. after an import added new cached cells) and
// indexes any not already present. Run in a goroutine.
func (ci *cellIndex) rebuild() {
	ci.mu.Lock()
	ci.built = false
	ci.mu.Unlock()
	ci.build()
}

// build backfills the index by reading every cached cell's header once. Runs in a
// background goroutine (started once) so it never blocks a request; queries see
// the index grow as it fills, and it's a no-op after the first complete pass.
func (ci *cellIndex) build() {
	ci.mu.Lock()
	if ci.built {
		ci.mu.Unlock()
		return
	}
	ci.built = true // claim the build; reset only if the scan can't start
	ci.mu.Unlock()

	entries, err := os.ReadDir(ci.encRoot)
	if err != nil {
		ci.mu.Lock()
		ci.built = false
		ci.mu.Unlock()
		return
	}
	present := make(map[string]bool, len(entries))
	added := 0
	for _, e := range entries {
		if !e.IsDir() || !isCellName(e.Name()) {
			continue
		}
		name := e.Name()
		present[name] = true
		if _, ok := ci.get(name); ok {
			continue // already indexed (forget() drops a re-imported cell so it re-parses)
		}
		data, err := os.ReadFile(filepath.Join(ci.encRoot, name, name+".000"))
		if err != nil {
			continue
		}
		chart, err := baker.ParseCellBytes(name, data)
		if err != nil {
			continue
		}
		b := chart.Bounds()
		ci.mu.Lock()
		ci.bbox[name] = [4]float64{b.MinLon, b.MinLat, b.MaxLon, b.MaxLat}
		ci.mu.Unlock()
		added++
		if added%200 == 0 {
			ci.save() // periodic checkpoint for a long backfill
		}
	}
	// Reconcile: drop entries for cells no longer on disk (removed packs/cells), so
	// the index never reports a chart that isn't installed anymore.
	removed := 0
	ci.mu.Lock()
	for name := range ci.bbox {
		if !present[name] {
			delete(ci.bbox, name)
			removed++
		}
	}
	ci.mu.Unlock()
	if added > 0 || removed > 0 {
		ci.save()
		log.Printf("cell index: +%d / -%d cell bound(s) → %s", added, removed, ci.path)
	}
}

// forget drops cells from the index so the next build re-parses them — used when
// an import re-caches a cell whose bounds may have changed.
func (ci *cellIndex) forget(names []string) {
	ci.mu.Lock()
	for _, n := range names {
		delete(ci.bbox, n)
	}
	ci.mu.Unlock()
}
