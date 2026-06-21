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
	s.sets.remove(set)
	s.packDel(set)
	s.prefs.setDisabled(set, false) // drop any stale disabled flag
	dir := s.setDir(set)
	_ = os.Remove(filepath.Join(dir, set+".pmtiles"))
	_ = os.Remove(filepath.Join(dir, set+".aux.zip"))
	_ = os.Remove(dir) // best-effort: drop the pack dir if now empty
	w.Header().Set("Content-Type", jsonCT)
	io.WriteString(w, `{"ok":true}`)
}

// handlePacks lists every baked pack on disk with its enabled state, so the client
// can show disabled packs (kept on disk, hidden from the map) for management.
func (s *Server) handlePacks(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", jsonCT)
	fmt.Fprint(w, `{"packs":[`)
	for i, name := range s.packNames() {
		if i > 0 {
			fmt.Fprint(w, ",")
		}
		fmt.Fprintf(w, `{"name":%q,"enabled":%t}`, name, !s.prefs.isDisabled(name))
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
	s.prefs.setDisabled(set, !enable)
	if enable {
		if path, ok := s.packPath(set); ok {
			if _, live := s.sets.get(set); !live {
				if src, err := tilesource.Open(path); err == nil {
					s.sets.register(set, src)
				} else {
					apiErr(w, http.StatusInternalServerError, err.Error())
					return
				}
			}
		}
	} else {
		s.sets.remove(set)
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
