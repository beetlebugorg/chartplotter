package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/beetlebugorg/chartplotter/internal/engine/tilesource"
)

// tilesDir is the flat archive directory: <cacheDir>/tiles.
func tilesDir(cacheDir string) string { return filepath.Join(cacheDir, "tiles") }

// tileSets is the server's registry of named tile sets (set name → backend). It is
// safe for concurrent use: the HTTP handler reads under an RLock while discovery /
// future imports register under a write lock.
type tileSets struct {
	mu sync.RWMutex
	m  map[string]tilesource.TileSource
}

func newTileSets() *tileSets { return &tileSets{m: map[string]tilesource.TileSource{}} }

// register adds (or replaces) a set. A replaced backend is closed.
func (ts *tileSets) register(name string, src tilesource.TileSource) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if old, ok := ts.m[name]; ok {
		_ = tilesource.Close(old)
	}
	ts.m[name] = src
}

// RegisterTileSet registers (or replaces) a tile set under name, served at
// /tiles/{name}/… exactly like a prebaked archive. It is exposed so an alternate
// backend — e.g. the optional libtile57 live source compiled in under
// -tags tile57 — can publish a set the same way discovery registers .pmtiles
// packs. A replaced backend is closed.
func (s *Server) RegisterTileSet(name string, src tilesource.TileSource) {
	s.sets.register(name, src)
}

// remove unregisters and closes the set named name. Reports whether it existed.
func (ts *tileSets) remove(name string) bool {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if old, ok := ts.m[name]; ok {
		_ = tilesource.Close(old)
		delete(ts.m, name)
		return true
	}
	return false
}

// get returns the set named name, or (nil, false).
func (ts *tileSets) get(name string) (tilesource.TileSource, bool) {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	src, ok := ts.m[name]
	return src, ok
}

// names returns the registered set names, sorted.
func (ts *tileSets) names() []string {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	out := make([]string, 0, len(ts.m))
	for n := range ts.m {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// closeAll closes every registered backend (file handles / DBs).
func (ts *tileSets) closeAll() {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	for _, src := range ts.m {
		_ = tilesource.Close(src)
	}
	ts.m = map[string]tilesource.TileSource{}
}

// isSetName accepts a safe single path component for a set name: letters, digits,
// '-', '_', '.' (but no separators or traversal).
func isSetName(s string) bool {
	if s == "" || len(s) > 64 || s == "." || s == ".." {
		return false
	}
	for _, c := range s {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
			return false
		}
	}
	return !strings.Contains(s, "..")
}

// handleDeleteSet unregisters a tile set and deletes its baked files from the cache
// (the regenerable pmtiles + aux.zip). The SOURCE cells in the data store are left
// intact — uninstalling a pack frees the baked cache, not the safe source. The set
// stops appearing in /tiles/ and rendering immediately.
func (s *Server) handleDeleteSet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		apiErr(w, http.StatusMethodNotAllowed, "DELETE only")
		return
	}
	set := r.URL.Query().Get("set")
	if !isSetName(set) {
		apiErr(w, http.StatusBadRequest, "bad set name")
		return
	}
	// ?set= is a PROVIDER — a full uninstall drops the baked bundle (cache) AND the
	// whole ENC_ROOT source tree (data), every district. To remove a single district
	// (keeping the rest), the client calls DELETE /api/district instead.
	s.dropProviderSet(providerOf(set))
	w.Header().Set("Content-Type", jsonCT)
	io.WriteString(w, `{"ok":true}`)
}

// handleDeleteDistrict removes ONE district from a provider (DELETE
// /api/district?provider=&district=): it deletes the district's ENC_ROOT subfolder
// and re-bakes the provider from what remains (dropping the provider set entirely if
// that was its last district). Delete reclaims disk; re-download to restore. The
// re-bake runs as a background job the client follows via /api/import/status.
func (s *Server) handleDeleteDistrict(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		apiErr(w, http.StatusMethodNotAllowed, "DELETE only")
		return
	}
	provider := providerOf(r.URL.Query().Get("provider"))
	district := r.URL.Query().Get("district")
	if !isSetName(provider) || !isDistrictName(district) {
		apiErr(w, http.StatusBadRequest, "need provider + district")
		return
	}
	if _, err := os.Stat(s.districtDir(provider, district)); err != nil {
		apiErr(w, http.StatusNotFound, "no such district")
		return
	}
	job := s.imports.create(provider)
	go func() {
		s.bakeMu.Lock()
		defer s.bakeMu.Unlock()
		if err := os.RemoveAll(s.districtDir(provider, district)); err != nil {
			s.imports.update(job.ID, func(j *importJob) { j.State = "error"; j.Err = err.Error() })
			return
		}
		s.auxIdx.invalidate() // the district's aux content is gone — re-index /aux
		if s.bakeProvider(job.ID, provider) {
			s.imports.update(job.ID, func(j *importJob) { j.State = "done" })
		}
	}()
	w.Header().Set("Content-Type", jsonCT)
	w.WriteHeader(http.StatusAccepted)
	fmt.Fprintf(w, `{"ok":true,"job":%q}`, job.ID)
}

// handlePacks lists every installed PROVIDER (GET /api/packs) with its enabled state,
// geographic bounds, extracted metadata, and its installed DISTRICTS (the ENC_ROOT
// subfolder names — the district→cell map is the folder listing itself). One entry per
// provider; districts are download/delete units under it, not separately toggled.
func (s *Server) handlePacks(w http.ResponseWriter, r *http.Request) {
	// The installable unit is the provider: union of providers with a baked bundle and
	// providers with an ENC_ROOT on disk (a download that hasn't finished baking yet).
	seen := map[string]bool{}
	for _, name := range s.packNames() {
		seen[providerOf(name)] = true
	}
	for _, prov := range s.installedProviders() {
		seen[prov] = true
	}
	// Runtime-registered sets with no disk pack (e.g. the live runtime compositor) — so a set
	// that only lives in the registry still surfaces as a first-class, toggleable map layer.
	// Its bounds/metadata come from the TileJSON (src.Meta()), not a disk pack.
	for _, name := range s.sets.names() {
		seen[providerOf(name)] = true
	}
	providers := make([]string, 0, len(seen))
	for p := range seen {
		providers = append(providers, p)
	}
	sort.Strings(providers)

	w.Header().Set("Content-Type", jsonCT)
	fmt.Fprint(w, `{"packs":[`)
	for i, prov := range providers {
		if i > 0 {
			fmt.Fprint(w, ",")
		}
		fmt.Fprintf(w, `{"name":%q,"enabled":%t`, prov, !s.prefs.isDisabled(prov))
		meta, hasMeta := s.readSetMeta(prov)
		// Coverage bounds, so the client marks "you have this chart here" even while a set is
		// DISABLED. A standalone archive reads them off the file; a live runtime-compositor
		// provider (no disk archive) takes them from the registry when enabled, else from its
		// meta sidecar — surfaced first-class from provider structure, not a pack path.
		if path, ok := s.packPath(prov); ok {
			if src, err := tilesource.Open(path); err == nil {
				m := src.Meta()
				_ = tilesource.Close(src)
				fmt.Fprintf(w, `,"bounds":[%g,%g,%g,%g]`, m.W, m.S, m.E, m.N)
			}
		} else if src, live := s.sets.get(prov); live {
			m := src.Meta()
			fmt.Fprintf(w, `,"bounds":[%g,%g,%g,%g]`, m.W, m.S, m.E, m.N)
		} else if hasMeta && len(meta.BBox) == 4 {
			fmt.Fprintf(w, `,"bounds":[%g,%g,%g,%g]`, meta.BBox[0], meta.BBox[1], meta.BBox[2], meta.BBox[3])
		}
		// Extracted metadata (title/agency/scale range/counts/imported date), from the
		// <provider>.meta.json sidecar. Cells omitted here — GET /api/pack/<provider>.
		if m := meta; hasMeta {
			if m.Title != "" {
				fmt.Fprintf(w, `,"title":%q`, m.Title)
			}
			if m.Agency != "" {
				fmt.Fprintf(w, `,"agency":%q`, m.Agency)
			}
			if m.CellCount > 0 {
				fmt.Fprintf(w, `,"cellCount":%d`, m.CellCount)
			}
			if m.ScaleMin > 0 {
				fmt.Fprintf(w, `,"scaleMin":%d,"scaleMax":%d`, m.ScaleMin, m.ScaleMax)
			}
			if m.Imported != "" {
				fmt.Fprintf(w, `,"imported":%q`, m.Imported)
			}
		}
		// Installed districts (ENC_ROOT subfolder names) so the client can mark which of
		// a provider's districts are present and offer per-district download/delete.
		fmt.Fprint(w, `,"districts":[`)
		for j, d := range s.providerDistricts(prov) {
			if j > 0 {
				fmt.Fprint(w, ",")
			}
			fmt.Fprintf(w, "%q", d)
		}
		fmt.Fprint(w, "]}")
	}
	fmt.Fprint(w, "]}")
}

// handlePackDetail returns the full extracted metadata for one provider, including the
// per-cell list (GET /api/pack/<provider>). 404s when the provider has no metadata
// sidecar (e.g. baked before metadata extraction existed).
func (s *Server) handlePackDetail(w http.ResponseWriter, r *http.Request) {
	const prefix = "/api/pack/"
	name := strings.TrimPrefix(r.URL.Path, prefix)
	if name == "" || !isSetName(name) {
		apiErr(w, http.StatusBadRequest, "bad pack name")
		return
	}
	m, ok := s.readSetMeta(providerOf(name))
	if !ok {
		apiErr(w, http.StatusNotFound, "no metadata for pack")
		return
	}
	w.Header().Set("Content-Type", jsonCT)
	_ = json.NewEncoder(w).Encode(m)
}

// handleSetEnabled shows or hides a PROVIDER on the map (POST /api/set/enable|disable
// ?set=<provider>). The baked data is kept; disabling just unregisters it so
// /tiles/{provider} stops serving + the client stops rendering it. Persists to prefs.
func (s *Server) handleSetEnabled(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		apiErr(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	set := r.URL.Query().Get("set")
	if !isSetName(set) {
		apiErr(w, http.StatusBadRequest, "bad set name")
		return
	}
	provider := providerOf(set)
	enable := strings.HasSuffix(r.URL.Path, "/enable")
	s.prefs.setDisabled(provider, !enable)
	if enable {
		if _, live := s.sets.get(provider); !live {
			if path, ok := s.packPath(provider); ok {
				// Standalone/overlay archive: re-open the disk pack.
				if src, err := tilesource.Open(path); err == nil {
					s.sets.register(provider, src)
				} else {
					apiErr(w, http.StatusInternalServerError, err.Error())
					return
				}
			} else if c, err := s.openLiveComposer(provider); err != nil {
				apiErr(w, http.StatusInternalServerError, err.Error())
				return
			} else if c != nil {
				// Live runtime-compositor provider (no disk pack): re-open the compositor
				// from its kept per-cell archives + partition sidecar.
				s.sets.register(provider, c)
			}
		}
	} else {
		s.sets.remove(provider)
	}
	w.Header().Set("Content-Type", jsonCT)
	fmt.Fprintf(w, `{"ok":true,"set":%q,"enabled":%t}`, provider, enable)
}

// serveCells returns the names of cells currently in the server's per-pack cells/
// source store. The client uses this so its installed-set (and the persisted baked
// sets) survive a page reload — the cells live server-side in the XDG data dir.
// The "cells" array is every cached cell name (back-compat: the installed list).
// "bbox" maps each INDEXED cell to its [W,S,E,N] footprint (fills in as the background
// index backfills), so the client can search a cell by name and fly to it. With
// ?active=1 the result is restricted to cells whose footprint overlaps an ENABLED
// pack — i.e. charts actually on the map right now (and only those that are indexed,
// since an un-indexed cell has no footprint to test or fly to).
func (s *Server) serveCells(w http.ResponseWriter, r *http.Request) {
	active := r.URL.Query().Get("active") == "1"
	var inPack map[string]bool // cells baked into enabled packs (exact, from manifests)
	var legacy [][4]float64    // bounds of enabled packs WITHOUT a manifest (bbox fallback)
	if active {
		inPack, legacy = s.enabledPackCells()
	}
	_, idx := s.cellIdx.snapshot()
	stems := s.cachedCellStems()
	names := make([]string, 0, len(stems))
	boxes := make(map[string][4]float64)
	for _, n := range stems {
		box, has := idx[n]
		if active {
			// Active = actually baked into an enabled pack. Prefer the exact per-pack
			// cell manifest; fall back to bbox-overlap only for legacy packs without
			// one (so a globe-spanning import doesn't drag in every cached cell).
			if !(inPack[n] || (has && bboxOverlapsAny(box, legacy))) {
				continue
			}
		}
		names = append(names, n)
		if has {
			boxes[n] = box
		}
	}
	sort.Strings(names)
	w.Header().Set("Content-Type", jsonCT)
	_ = json.NewEncoder(w).Encode(struct {
		Cells []string              `json:"cells"`
		BBox  map[string][4]float64 `json:"bbox"`
	}{names, boxes})
}

// enabledPackCells reports which cells are "active" (on the map). It returns (a) the
// union of cell stems recorded for each ENABLED pack that has a cell manifest (written
// at bake time by writeSetCells) — the exact installed set — and (b) the [W,S,E,N] of
// each enabled pack with NO manifest (a legacy pack baked before per-pack cell
// tracking), for the ?active filter to fall back to bbox-overlap on. Re-baking a
// legacy pack (re-import) writes its manifest and moves it onto the exact path.
func (s *Server) enabledPackCells() (map[string]bool, [][4]float64) {
	cells := map[string]bool{}
	var legacy [][4]float64
	for _, name := range sortedKeys(s.packs) {
		if s.prefs.isDisabled(name) {
			continue
		}
		if stems, ok := s.setCells(name); ok {
			for _, st := range stems {
				cells[st] = true
			}
			continue
		}
		if src, err := tilesource.Open(s.packs[name]); err == nil {
			m := src.Meta()
			_ = tilesource.Close(src)
			legacy = append(legacy, [4]float64{m.W, m.S, m.E, m.N})
		}
	}
	// Live runtime-compositor providers (no disk pack): their cells come from the SAME
	// per-provider cell manifest (writeSetCells at import), keyed provider-centrically —
	// so a live provider's charts count as "on the map" for ?active just like a pack's.
	for _, prov := range s.installedProviders() {
		if s.prefs.isDisabled(prov) {
			continue
		}
		if _, isPack := s.packPath(prov); isPack {
			continue // already covered above
		}
		if stems, ok := s.setCells(prov); ok {
			for _, st := range stems {
				cells[st] = true
			}
		}
	}
	return cells, legacy
}

// bboxOverlapsAny reports whether [W,S,E,N] box intersects any of the rects.
func bboxOverlapsAny(b [4]float64, rects [][4]float64) bool {
	for _, r := range rects {
		if b[0] <= r[2] && b[2] >= r[0] && b[1] <= r[3] && b[3] >= r[1] {
			return true
		}
	}
	return false
}
