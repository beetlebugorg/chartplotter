package server

import (
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

// tilesDir is the legacy flat archive directory: <cacheDir>/tiles.
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
// a name that doesn't end in a known band (a legacy merged set, or a non-banded
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
// the whole name is the district and band = "all" (a legacy merged set or a non-
// banded local import). The district is the API-facing pack name (/api/packs, the
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
		bands   []string
		enabled bool
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
		fmt.Fprintf(w, `{"name":%q,"enabled":%t,"bands":[`, d, p.enabled)
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
func (s *Server) serveCells(w http.ResponseWriter, r *http.Request) {
	entries, _ := os.ReadDir(filepath.Join(s.dataDir, "ENC_ROOT"))
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() && isCellName(e.Name()) {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	w.Header().Set("Content-Type", jsonCT)
	fmt.Fprint(w, `{"cells":[`)
	for i, n := range names {
		if i > 0 {
			fmt.Fprint(w, ",")
		}
		fmt.Fprintf(w, "%q", n)
	}
	fmt.Fprint(w, "]}")
}
