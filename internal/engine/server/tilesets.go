package server

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/beetlebugorg/chartplotter/internal/engine/tilesource"
)

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

// discoverArchives registers every prebaked .pmtiles / .mbtiles archive found
// directly under dir as a tile set named by its basename (sans extension). It is
// best-effort: a file that fails to open is logged and skipped. Returns the count
// registered.
func (ts *tileSets) discoverArchives(dir string) int {
	n := 0
	for _, ext := range []string{"*.pmtiles", "*.mbtiles"} {
		matches, _ := filepath.Glob(filepath.Join(dir, ext))
		for _, path := range matches {
			name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
			if !isSetName(name) {
				log.Printf("tilesets: skip %q (invalid set name)", path)
				continue
			}
			src, err := tilesource.Open(path)
			if err != nil {
				log.Printf("tilesets: skip %q: %v", path, err)
				continue
			}
			ts.register(name, src)
			m := src.Meta()
			log.Printf("tilesets: registered %q from %s (z%d-%d)", name, filepath.Base(path), m.MinZoom, m.MaxZoom)
			n++
		}
	}
	return n
}

// tilesDir is the directory scanned for prebaked archives: <cacheDir>/tiles.
func tilesDir(cacheDir string) string { return filepath.Join(cacheDir, "tiles") }

// discoverTree walks dir recursively and registers every *.pmtiles as a tile set
// named by its basename (e.g. <cache>/NOAA/D17/noaa-d17.pmtiles → set "noaa-d17").
// Best-effort; returns the count registered.
func (ts *tileSets) discoverTree(dir string) int {
	n := 0
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".pmtiles") {
			return nil
		}
		name := strings.TrimSuffix(filepath.Base(path), ".pmtiles")
		if !isSetName(name) {
			return nil
		}
		src, e := tilesource.Open(path)
		if e != nil {
			log.Printf("tilesets: skip %q: %v", path, e)
			return nil
		}
		ts.register(name, src)
		m := src.Meta()
		log.Printf("tilesets: registered %q from %s (z%d-%d)", name, path, m.MinZoom, m.MaxZoom)
		n++
		return nil
	})
	return n
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
