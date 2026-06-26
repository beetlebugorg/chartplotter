package server

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
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

// bandSlugs is the fixed COARSE→FINE band order. A registered tile SET is named
// "<district>-<slug>" (one archive per nav-purpose band, so coarse-band-only areas
// keep tiles above the merged archive's single maxzoom). "all" is the catch-all for
// a name that doesn't end in a known band (a merged set, or a non-banded
// import). bandOrder ranks a slug for the sorted /api/packs listing.
var bandSlugs = []string{"overview", "general", "coastal", "approach", "harbor", "berthing"}

func bandOrder(slug string) int {
	for i, s := range bandSlugs {
		if s == slug {
			return i
		}
	}
	return len(bandSlugs) // "all" sorts last
}

// splitSet splits a registered set name into its logical district + band. If the
// name ends in "-<knownband>", district = the prefix and band = that slug; otherwise
// the whole name is the district and band = "all" (a merged set or a non-banded
// local import). The district is the API-facing pack name (/api/packs, the
// enable/disable/delete ?set=); band is the per-archive suffix.
func splitSet(name string) (district, band string) {
	for _, slug := range bandSlugs {
		if suf := "-" + slug; strings.HasSuffix(name, suf) && len(name) > len(suf) {
			return name[:len(name)-len(suf)], slug
		}
	}
	return name, "all"
}

// setsForDistrict returns every registered set name belonging to district d (its
// merged form "d" plus each band-set "d-<slug>"), so enable/disable/delete can fan
// out across a district's per-band archives. Looks at both the live registry and the
// on-disk pack list (a disabled band-set isn't registered but still has a pack).
func (s *Server) setsForDistrict(d string) []string {
	seen := map[string]bool{}
	for _, n := range s.packNames() {
		if dist, _ := splitSet(n); dist == d {
			seen[n] = true
		}
	}
	for _, n := range s.sets.names() {
		if dist, _ := splitSet(n); dist == d {
			seen[n] = true
		}
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
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
	// ?set= is the DISTRICT — fan out the delete across every one of its band-sets
	// (the merged "set" form plus each "set-<slug>" archive).
	for _, name := range s.setsForDistrict(set) {
		s.sets.remove(name)
		s.packDel(name)
		s.prefs.setDisabled(name, false) // drop any stale disabled flag
		dir := s.setDir(name)
		_ = os.Remove(filepath.Join(dir, name+".pmtiles"))
		_ = os.Remove(filepath.Join(dir, name+".aux.zip"))
		_ = os.Remove(dir) // best-effort: drop the pack dir if now empty
	}
	// Drop the district's metadata sidecar (<district>.meta.json), which lives in
	// the district's own setDir, separate from the per-band dirs above.
	mdir := s.setDir(set)
	_ = os.Remove(filepath.Join(mdir, set+setMetaExt))
	_ = os.Remove(mdir)
	s.auxIdx.invalidate() // a district's companion aux.zip is gone — re-index /api/aux
	w.Header().Set("Content-Type", jsonCT)
	io.WriteString(w, `{"ok":true}`)
}

// handlePacks lists every installed pack with its enabled state, so the client can
// show disabled packs (kept on disk, hidden from the map) for management. A pack is a
// logical DISTRICT (e.g. "noaa-d5"); its per-band archives ("noaa-d5-general", …) are
// grouped into ONE entry with the bands it produced, coarse→fine. A district is
// enabled iff ANY of its band-sets is enabled (enable/disable fan out to all bands,
// so they move together — see handleSetEnabled).
func (s *Server) handlePacks(w http.ResponseWriter, r *http.Request) {
	// Group registered band-sets by district, collecting each district's bands and
	// whether any band is enabled (not disabled).
	type pack struct {
		bands      []string
		enabled    bool
		w, s, e, n float64
		hasBounds  bool
	}
	byDistrict := map[string]*pack{}
	var order []string // first-seen district order, then re-sorted below
	for _, name := range s.packNames() {
		d, band := splitSet(name)
		p := byDistrict[d]
		if p == nil {
			p = &pack{}
			byDistrict[d] = p
			order = append(order, d)
		}
		p.bands = append(p.bands, band)
		if !s.prefs.isDisabled(name) {
			p.enabled = true
		}
		// Union each band-set's geographic bounds so the client can outline a pack's
		// coverage even while it's DISABLED (its tiles aren't served, but the boundary
		// still marks "you have this chart here, currently off"). Read straight from
		// the archive on disk — disabled packs aren't in the live set registry.
		if path, ok := s.packPath(name); ok {
			if src, err := tilesource.Open(path); err == nil {
				m := src.Meta()
				_ = tilesource.Close(src)
				if !p.hasBounds {
					p.w, p.s, p.e, p.n, p.hasBounds = m.W, m.S, m.E, m.N, true
				} else {
					p.w, p.s = math.Min(p.w, m.W), math.Min(p.s, m.S)
					p.e, p.n = math.Max(p.e, m.E), math.Max(p.n, m.N)
				}
			}
		}
	}
	sort.Strings(order)
	w.Header().Set("Content-Type", jsonCT)
	fmt.Fprint(w, `{"packs":[`)
	for i, d := range order {
		if i > 0 {
			fmt.Fprint(w, ",")
		}
		p := byDistrict[d]
		sort.Slice(p.bands, func(a, b int) bool { return bandOrder(p.bands[a]) < bandOrder(p.bands[b]) })
		fmt.Fprintf(w, `{"name":%q,"enabled":%t`, d, p.enabled)
		if p.hasBounds {
			fmt.Fprintf(w, `,"bounds":[%g,%g,%g,%g]`, p.w, p.s, p.e, p.n)
		}
		// Extracted per-pack metadata (title/agency/scale range/counts/imported date),
		// from the <pack>.meta.json sidecar written at import. Cells are omitted from
		// the list view — fetch GET /api/pack/<name> for the full per-cell detail.
		if m, ok := s.readSetMeta(d); ok {
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
		fmt.Fprint(w, `,"bands":[`)
		for j, band := range p.bands {
			if j > 0 {
				fmt.Fprint(w, ",")
			}
			fmt.Fprintf(w, "%q", band)
		}
		fmt.Fprint(w, "]}")
	}
	fmt.Fprint(w, "]}")
}

// handlePackDetail returns the full extracted metadata for one pack, including the
// per-cell list (GET /api/pack/<name>). 404s when the pack has no metadata sidecar
// (e.g. baked before metadata extraction existed, or a built-in pack).
func (s *Server) handlePackDetail(w http.ResponseWriter, r *http.Request) {
	const prefix = "/api/pack/"
	name := strings.TrimPrefix(r.URL.Path, prefix)
	if name == "" || !isSetName(name) {
		apiErr(w, http.StatusBadRequest, "bad pack name")
		return
	}
	m, ok := s.readSetMeta(name)
	if !ok {
		apiErr(w, http.StatusNotFound, "no metadata for pack")
		return
	}
	w.Header().Set("Content-Type", jsonCT)
	_ = json.NewEncoder(w).Encode(m)
}

// handleSetEnabled shows or hides a pack on the map (POST /api/set/enable|disable
// ?set=NAME). The baked data is kept; disabling just unregisters it so /tiles/{set}
// stops serving + the client stops rendering it. Persists to prefs.
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
	enable := strings.HasSuffix(r.URL.Path, "/enable")
	// ?set= is the DISTRICT — fan out to every one of its band-sets so a district's
	// per-band archives toggle together. Disabled state persists per band-set (so it
	// survives a restart), keyed by the registered set name, not the district.
	for _, name := range s.setsForDistrict(set) {
		s.prefs.setDisabled(name, !enable)
		if enable {
			if path, ok := s.packPath(name); ok {
				if _, live := s.sets.get(name); !live {
					if src, err := tilesource.Open(path); err == nil {
						s.sets.register(name, src)
					} else {
						apiErr(w, http.StatusInternalServerError, err.Error())
						return
					}
				}
			}
		} else {
			s.sets.remove(name)
		}
	}
	w.Header().Set("Content-Type", jsonCT)
	fmt.Fprintf(w, `{"ok":true,"set":%q,"enabled":%t}`, set, enable)
}

// serveCells returns the names of cells currently in the server's ENC_ROOT source
// store. The client uses this so its installed-set (and the persisted baked sets)
// survive a page reload — the cells live server-side in the XDG data dir.
// serveCells returns the installed source cells. The "cells" array is every
// cached cell name (back-compat: the installed list). "bbox" maps each INDEXED
// cell to its [W,S,E,N] footprint (fills in as the background index backfills),
// so the client can search a cell by name and fly to it. With ?active=1 the
// result is restricted to cells whose footprint overlaps an ENABLED pack — i.e.
// charts actually on the map right now (and only those that are indexed, since an
// un-indexed cell has no footprint to test or fly to).
func (s *Server) serveCells(w http.ResponseWriter, r *http.Request) {
	active := r.URL.Query().Get("active") == "1"
	var enabled [][4]float64
	if active {
		enabled = s.enabledPackBounds()
	}
	_, idx := s.cellIdx.snapshot()
	entries, _ := os.ReadDir(filepath.Join(s.dataDir, "ENC_ROOT"))
	names := make([]string, 0, len(entries))
	boxes := make(map[string][4]float64)
	for _, e := range entries {
		if !e.IsDir() || !isCellName(e.Name()) {
			continue
		}
		n := e.Name()
		box, has := idx[n]
		if active && (!has || !bboxOverlapsAny(box, enabled)) {
			continue
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

// enabledPackBounds is each enabled pack's [W,S,E,N] (read from its archive),
// used by the ?active filter to test which cells are currently on the map.
func (s *Server) enabledPackBounds() [][4]float64 {
	var out [][4]float64
	for _, name := range sortedKeys(s.packs) {
		if s.prefs.isDisabled(name) {
			continue
		}
		if src, err := tilesource.Open(s.packs[name]); err == nil {
			m := src.Meta()
			_ = tilesource.Close(src)
			out = append(out, [4]float64{m.W, m.S, m.E, m.N})
		}
	}
	return out
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
