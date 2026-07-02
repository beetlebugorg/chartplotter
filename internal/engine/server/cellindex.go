package server

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"

	tile57 "github.com/beetlebugorg/tile57/bindings/go"
)

// cellIndex is a small, persistent name→bounding-box index over the cached source
// cells (<dataDir>/ENC_ROOT/<CELL>/<CELL>.000). It lets the server answer "where
// is cell X" and "which installed cells are active" without re-parsing thousands
// of cells on every request: each cell's header is read ONCE (the bbox cached to
// <dataDir>/cells-index.json), then queries hit the in-memory map. Kept
// deliberately simple — a flat JSON map, not a database; the data is tiny (a few
// floats per cell) and read-mostly.
type cellIndex struct {
	mu       sync.RWMutex
	cond     *sync.Cond            // broadcast when a scan finishes (for wait())
	bbox     map[string][4]float64 // cell stem → [W,S,E,N]
	path     string                // cells-index.json
	encRoot  string                // <dataDir>/ENC_ROOT
	scanning bool                  // a scan goroutine is running
	dirty    bool                  // a (re)build was requested during a scan → scan again
}

func newCellIndex(dataDir string) *cellIndex {
	ci := &cellIndex{
		bbox:    map[string][4]float64{},
		path:    filepath.Join(dataDir, "cells-index.json"),
		encRoot: filepath.Join(dataDir, "ENC_ROOT"),
	}
	ci.cond = sync.NewCond(&ci.mu)
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

// build kicks the initial backfill; rebuild requests a fresh pass after the cache
// changed (import added cells, a set was deleted). Both funnel through kick().
func (ci *cellIndex) build()   { ci.kick() }
func (ci *cellIndex) rebuild() { ci.kick() }

// kick ensures the index is (re)scanned. Single-flight with a dirty re-run: if a
// scan is already running it just marks the index dirty so that scan loops once
// more when it finishes — so a (re)build requested mid-scan is never lost (the old
// built-flag reset/claim could drop a concurrent reindex, leaving the index stale).
func (ci *cellIndex) kick() {
	ci.mu.Lock()
	ci.dirty = true
	if ci.scanning {
		ci.mu.Unlock()
		return
	}
	ci.scanning = true
	ci.mu.Unlock()
	go ci.run()
}

func (ci *cellIndex) run() {
	for {
		ci.mu.Lock()
		ci.dirty = false
		ci.mu.Unlock()
		ci.scan()
		ci.mu.Lock()
		if !ci.dirty { // nothing changed during the scan — done
			ci.scanning = false
			ci.cond.Broadcast() // wake any wait()ers
			ci.mu.Unlock()
			return
		}
		ci.mu.Unlock() // a (re)build arrived mid-scan — scan again
	}
}

// wait blocks until no scan is in flight — for tests and any caller that needs the
// index settled. kick() sets scanning before it returns, so a build()/rebuild()
// immediately followed by wait() always observes the in-flight scan and its re-runs.
func (ci *cellIndex) wait() {
	ci.mu.Lock()
	for ci.scanning {
		ci.cond.Wait()
	}
	ci.mu.Unlock()
}

// scan reads every cached cell's header once (bbox cached so repeat scans skip the
// already-indexed) and reconciles: drops index entries for cells no longer on disk.
func (ci *cellIndex) scan() {
	entries, err := os.ReadDir(ci.encRoot)
	if err != nil {
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
		src, err := tile57.Open(filepath.Join(ci.encRoot, name, name+".000"))
		if err != nil {
			continue
		}
		infos, err := src.Cells()
		src.Close()
		if err != nil || len(infos) == 0 {
			continue
		}
		ci.mu.Lock()
		ci.bbox[name] = infos[0].BBox
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
