package server

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// bakeVerExt is the sidecar that records the build version that baked a pack
// (<pack>.pmtiles.bakever), so startup can flag a cache baked by an older binary.
const bakeVerExt = ".bakever"

// engineVerExt is the sidecar that records the tile57 ENGINE commit that baked a
// pack (<pack>.pmtiles.enginever) — bake-time truth, distinct from the running
// binary's own engine commit. The set's TileJSON reports it so the client can
// stamp the map with the engine behind the visible tiles (and flag a mixed-bake
// cache). A pack without the sidecar predates stamping → "pre-stamp".
const engineVerExt = ".enginever"

// ReportStaleCache logs a loud warning for any served pack whose recorded build
// version (its <pack>.bakever sidecar) differs from the running binary — the
// stale-cache trap, where `make serve` keeps serving tiles baked by older
// baker/portrayal code. Call AFTER s.Version is set (serve.go), not in New().
func (s *Server) ReportStaleCache() {
	if s.Version == "" {
		return // dev build with no version stamp — can't compare
	}
	names := s.packNames()
	var stale []string
	for _, set := range names {
		p, ok := s.packPath(set)
		if !ok {
			continue
		}
		got, _ := os.ReadFile(p + bakeVerExt)
		if strings.TrimSpace(string(got)) != s.Version {
			stale = append(stale, set)
		}
	}
	if len(stale) == 0 {
		return
	}
	log.Printf("⚠️  STALE CACHE: %d/%d served pack(s) were NOT baked by this binary (%s) — they predate the current baker/portrayal code and may be missing features (re-bake to update): %s",
		len(stale), len(names), s.Version, strings.Join(stale, ", "))
}

// Pack enable/disable state is SERVER-side (the client only talks to our API): a
// disabled pack stays baked on disk but is not registered, so it isn't served at
// /tiles/{set} and doesn't render. The state persists in <data>/prefs.json so it
// survives restarts and is shared across browsers/devices.

// prefs is the persisted server preferences.
type prefs struct {
	mu       sync.Mutex
	path     string          // <dataDir>/prefs.json
	Disabled map[string]bool `json:"disabled"` // set name → hidden from the map
}

func loadPrefs(dataDir string) *prefs {
	p := &prefs{path: filepath.Join(dataDir, "prefs.json"), Disabled: map[string]bool{}}
	if b, err := os.ReadFile(p.path); err == nil {
		var on struct {
			Disabled map[string]bool `json:"disabled"`
		}
		if json.Unmarshal(b, &on) == nil && on.Disabled != nil {
			p.Disabled = on.Disabled
		}
	}
	return p
}

func (p *prefs) isDisabled(set string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.Disabled[set]
}

// setDisabled records a set's disabled state and persists. Returns the new state.
func (p *prefs) setDisabled(set string, off bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if off {
		p.Disabled[set] = true
	} else {
		delete(p.Disabled, set)
	}
	if b, err := json.Marshal(map[string]any{"disabled": p.Disabled}); err == nil {
		_ = os.MkdirAll(filepath.Dir(p.path), 0o755)
		_ = os.WriteFile(p.path, b, 0o644)
	}
}

// scanPacks discovers STANDALONE tile archives — prebaked .pmtiles/.mbtiles hand-dropped
// into the flat <cache>/tiles dir — keyed set name (the file basename) → path. This is the
// narrow overlay path for hand-placed archives.
//
// It scans ONLY the flat tiles dir, never the whole cache: a live runtime-compositor
// provider owns <cache>/<PROVIDER>/tiles/*.pmtiles + partition.tpart, and those per-cell
// archives are compositor INPUTS discovered provider-centrically by registerLiveProviders —
// never scavenged here (else every cell would surface as its own phantom set, and an
// interrupted import that hasn't saved its partition yet would leak cells as packs).
func scanPacks(cacheDir string) map[string]string {
	out := map[string]string{}
	dir := tilesDir(cacheDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".pmtiles") && !strings.HasSuffix(name, ".mbtiles") {
			continue
		}
		set := strings.TrimSuffix(name, filepath.Ext(name))
		if isSetName(set) {
			out[set] = filepath.Join(dir, name)
		}
	}
	return out
}

// -- concurrency-safe access to s.packs (bake goroutine vs request handlers) ----

func (s *Server) packAdd(set, path string) {
	s.packsMu.Lock()
	defer s.packsMu.Unlock()
	s.packs[set] = path
}

func (s *Server) packDel(set string) {
	s.packsMu.Lock()
	defer s.packsMu.Unlock()
	delete(s.packs, set)
}

func (s *Server) packPath(set string) (string, bool) {
	s.packsMu.Lock()
	defer s.packsMu.Unlock()
	p, ok := s.packs[set]
	return p, ok
}

// packGen is a baked pack's cache-bust generation token — its archive mtime in
// unix-nanos, which changes every time the set is re-baked (a fresh file is
// renamed into place). 0 for a live/dynamic set (no pack file). Both the
// TileJSON and the engine-style source URL stamp this as ?g so a given tile URL
// is content-addressed and safe to cache immutably (see serveTile).
func (s *Server) packGen(set string) int64 {
	if p, ok := s.packPath(set); ok {
		if fi, err := os.Stat(p); err == nil {
			return fi.ModTime().UnixNano()
		}
	}
	// Live runtime-compositor provider (no disk pack): the live.gen file's mtime, bumped on each
	// completed import — so its tiles are content-addressed by ?g exactly like a baked pack's.
	if fi, err := os.Stat(s.liveGenPath(set)); err == nil {
		return fi.ModTime().UnixNano()
	}
	return 0
}

// genQuery renders a packGen token as a tile-URL query suffix: "?g=<n>" for a
// real (nonzero) generation, "" for a live set (so its URL stays token-free and
// serveTile keeps it no-cache).
func genQuery(gen int64) string {
	if gen == 0 {
		return ""
	}
	return fmt.Sprintf("?g=%d", gen)
}

func (s *Server) packNames() []string {
	s.packsMu.Lock()
	defer s.packsMu.Unlock()
	return sortedKeys(s.packs)
}

// sortedKeys returns m's keys sorted (for stable JSON listings).
func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
