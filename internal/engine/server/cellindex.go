package server

import (
	"encoding/json"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	tile57 "github.com/beetlebugorg/tile57/bindings/go"
)

// cellIndex is a small, persistent name→bounding-box index over the cached source
// cells — every .000 under a per-pack (or loose) cells/ dir in the data dir. It lets
// the server answer "where is cell X" and "which installed cells are active" without
// re-parsing thousands of cells on every request: each cell's header is read ONCE (the
// bbox cached to <dataDir>/cells-index.json), then queries hit the in-memory map. Kept
// deliberately simple — a flat JSON map, not a database; the data is tiny (a few
// floats per cell) and read-mostly.
type cellIndex struct {
	mu       sync.RWMutex
	cond     *sync.Cond            // broadcast when a scan finishes (for wait())
	bbox     map[string][4]float64 // cell stem → [W,S,E,N]
	path     string                // cells-index.json
	root     string                // <dataDir> — walked for cells under any .../cells/ dir
	scanning bool                  // a scan goroutine is running
	dirty    bool                  // a (re)build was requested during a scan → scan again
}

func newCellIndex(dataDir string) *cellIndex {
	ci := &cellIndex{
		bbox: map[string][4]float64{},
		path: filepath.Join(dataDir, "cells-index.json"),
		root: dataDir,
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

// isSourceCellFile reports whether a walked path is an installed SOURCE cell — a .000
// under a provider ENC_ROOT tree (<PROVIDER>/ENC_ROOT/<district>/…) or directly in a
// loose cells/ dir (the /api/cell proxy + share-published cells). It keys the cell
// index + the installed-cell listing.
func isSourceCellFile(path, name string) bool {
	if !isBaseCell(name) {
		return false
	}
	if filepath.Base(filepath.Dir(path)) == "cells" {
		return true
	}
	sep := string(filepath.Separator)
	return strings.Contains(path, sep+"ENC_ROOT"+sep)
}

// scan reads every cached cell's header once (bbox cached so repeat scans skip the
// already-indexed) and reconciles: drops index entries for cells no longer on disk.
// Source cells live under each provider's ENC_ROOT tree (<PROVIDER>/ENC_ROOT/<district>/)
// plus loose/cells/, so the scan walks the data dir for every such .000.
func (ci *cellIndex) scan() {
	present := map[string]bool{}
	added := 0
	_ = filepath.WalkDir(ci.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if n := d.Name(); n == "tiles" || n == "assets" {
				return fs.SkipDir // regenerable bundle output — never holds source cells
			}
			return nil
		}
		if !isSourceCellFile(path, d.Name()) {
			return nil // only source .000 files (ENC_ROOT trees + loose cells/)
		}
		name := strings.TrimSuffix(d.Name(), ".000")
		if !isCellName(name) {
			return nil
		}
		present[name] = true
		if _, ok := ci.get(name); ok {
			return nil // already indexed (forget() drops a re-imported cell so it re-parses)
		}
		src, err := tile57.Open(path)
		if err != nil {
			return nil
		}
		infos, err := src.Cells()
		src.Close()
		if err != nil || len(infos) == 0 {
			return nil
		}
		ci.mu.Lock()
		ci.bbox[name] = infos[0].BBox
		ci.mu.Unlock()
		added++
		if added%200 == 0 {
			ci.save() // periodic checkpoint for a long backfill
		}
		return nil
	})
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

// cachedCellStems returns the de-duplicated stems of every cell cached on disk (any
// .000 under a .../cells/ dir in the data dir) — the installed cell list for /api/cells,
// independent of whether the background index has read each cell's bbox yet.
func (s *Server) cachedCellStems() []string {
	seen := map[string]bool{}
	_ = filepath.WalkDir(s.dataDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if n := d.Name(); n == "tiles" || n == "assets" {
				return fs.SkipDir // regenerable bundle output — never holds source cells
			}
			return nil
		}
		if !isSourceCellFile(path, d.Name()) {
			return nil
		}
		if stem := strings.TrimSuffix(d.Name(), ".000"); isCellName(stem) {
			seen[stem] = true
		}
		return nil
	})
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	return out
}

// migrateLegacyENCRoot moves cells from the RETIRED flat <dataDir>/ENC_ROOT cache into
// the loose-cell dir (flattened) on the first start after the per-pack-cells migration,
// so an existing install's downloaded cells aren't orphaned — they stay searchable
// (/api/cells) and serveable (/api/cell). One-time: ENC_ROOT is removed afterwards.
// Best-effort; per-pack cells now live under <provider>/<pack>/cells/, not here.
func migrateLegacyENCRoot(dataDir string) {
	root := filepath.Join(dataDir, "ENC_ROOT")
	entries, err := os.ReadDir(root)
	if err != nil {
		return // no legacy cache
	}
	dst := filepath.Join(dataDir, "loose", "cells")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return
	}
	moved := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		files, _ := os.ReadDir(filepath.Join(root, e.Name()))
		for _, f := range files {
			if f.IsDir() {
				continue
			}
			to := filepath.Join(dst, f.Name())
			if _, err := os.Stat(to); err == nil {
				continue // already present (a same-named loose cell / prior run)
			}
			if os.Rename(filepath.Join(root, e.Name(), f.Name()), to) == nil {
				moved++
			}
		}
	}
	_ = os.RemoveAll(root)
	if moved > 0 {
		log.Printf("migrated %d legacy ENC_ROOT cell file(s) → %s", moved, dst)
	}
}

// migrateProviderEncRoot upgrades the RETIRED per-district-pack layout to the
// provider-enc-root model on first start: each <data>/<PROVIDER>/<PACK>/cells/ becomes
// a district subfolder <data>/<PROVIDER>/ENC_ROOT/<pack>/, and the stale per-pack baked
// bundles under the cache are dropped so scanPacks doesn't mis-register them (the
// provider re-bakes from its migrated ENC_ROOT — see rebakeMissingProviders). One-time,
// best-effort: a provider already on the new layout (an ENC_ROOT present, no cells/
// packs) is left untouched, so this is a no-op on a fresh or already-migrated install.
func migrateProviderEncRoot(dataDir, cacheDir string) {
	providers, err := os.ReadDir(dataDir)
	if err != nil {
		return
	}
	for _, pe := range providers {
		if !pe.IsDir() {
			continue
		}
		provider := pe.Name()
		if provider == "loose" || provider == "ENC_ROOT" || provider == "tiles" || provider == "assets" {
			continue // not a provider tree
		}
		provDir := filepath.Join(dataDir, provider)
		packs, err := os.ReadDir(provDir)
		if err != nil {
			continue
		}
		moved := 0
		for _, pk := range packs {
			if !pk.IsDir() || pk.Name() == "ENC_ROOT" {
				continue
			}
			cellsDir := filepath.Join(provDir, pk.Name(), "cells")
			if fi, err := os.Stat(cellsDir); err != nil || !fi.IsDir() {
				continue // not an old per-pack cells/ dir
			}
			dst := filepath.Join(provDir, "ENC_ROOT", strings.ToLower(pk.Name()))
			if os.MkdirAll(dst, 0o755) != nil {
				continue
			}
			files, _ := os.ReadDir(cellsDir)
			for _, f := range files {
				if f.IsDir() {
					continue
				}
				to := filepath.Join(dst, f.Name())
				if _, err := os.Stat(to); err == nil {
					continue // already present (a shared boundary cell from another district)
				}
				if os.Rename(filepath.Join(cellsDir, f.Name()), to) == nil {
					moved++
				}
			}
			_ = os.RemoveAll(filepath.Join(provDir, pk.Name())) // drop the old pack tree (data side)
		}
		if moved == 0 {
			continue
		}
		// Drop stale per-pack baked bundles so scanPacks can't mis-derive a provider from
		// <cache>/<PROVIDER>/<PACK>/tiles/chart.pmtiles. In single-dir mode the RemoveAll
		// above already took them (they sat inside the pack tree). In split mode, clear the
		// old-layout cache tree here; the current-layout <cache>/<PROVIDER>/tiles bundle, if
		// any, is left (a re-bake overwrites it).
		if cacheDir != dataDir {
			if cpacks, err := os.ReadDir(filepath.Join(cacheDir, provider)); err == nil {
				for _, cp := range cpacks {
					if cp.IsDir() && cp.Name() != "tiles" && cp.Name() != "assets" {
						_ = os.RemoveAll(filepath.Join(cacheDir, provider, cp.Name()))
					}
				}
			}
		}
		log.Printf("migrated provider %q to ENC_ROOT layout (%d cell file(s))", provider, moved)
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
