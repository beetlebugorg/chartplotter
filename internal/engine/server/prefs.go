package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

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

// scanPacks walks the cache and returns every baked pack file keyed by set name
// (basename sans extension) → path. Includes the provider trees plus legacy tiles/.
func scanPacks(cacheDir string) map[string]string {
	out := map[string]string{}
	_ = filepath.WalkDir(cacheDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".pmtiles") && !strings.HasSuffix(path, ".mbtiles") {
			return nil
		}
		name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		if isSetName(name) {
			out[name] = path
		}
		return nil
	})
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
