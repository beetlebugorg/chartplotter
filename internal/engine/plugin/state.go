package plugin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// state.go persists per-plugin state to <dataDir>/plugins.json: enabled flag,
// granted capabilities, pinned publisher key, and per-plugin settings (spec §2). It
// mirrors server.connectionsStore — an in-memory map mirrored to a JSON file behind a
// mutex, best-effort writes — but the per-entry struct is richer than nmea.Source.

// PluginRecord is one plugins.json entry. It records install/grant state, not the
// manifest (the manifest lives with the unpacked archive on disk).
type PluginRecord struct {
	ID          string         `json:"id"`
	Version     string         `json:"version"` // the active unpacked version
	Enabled     bool           `json:"enabled"`
	Grants      []Capability   `json:"grants,omitempty"`      // user-granted subset of the manifest's caps
	Config      map[string]any `json:"config,omitempty"`      // per-plugin settings round-tripped as `config`
	ForceNative bool           `json:"forceNative,omitempty"` // prefer the native entry over wasm (§2)
	PinnedKey   string         `json:"pinnedKey,omitempty"`   // TOFU publisher key fingerprint (Phase 3)
}

// clone returns a deep-enough copy for handing out without exposing the stored map.
func (r *PluginRecord) clone() PluginRecord {
	cp := *r
	cp.Grants = append([]Capability(nil), r.Grants...)
	if r.Config != nil {
		cp.Config = make(map[string]any, len(r.Config))
		for k, v := range r.Config {
			cp.Config[k] = v
		}
	}
	return cp
}

// stateStore is the plugins.json-backed set of PluginRecords.
type stateStore struct {
	mu      sync.Mutex
	path    string
	records map[string]*PluginRecord
}

func newStateStore(path string) *stateStore {
	s := &stateStore{path: path, records: map[string]*PluginRecord{}}
	s.load()
	return s
}

func (s *stateStore) load() {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	var list []*PluginRecord
	if json.Unmarshal(b, &list) != nil {
		return
	}
	for _, r := range list {
		if r != nil && r.ID != "" {
			s.records[r.ID] = r
		}
	}
}

// save writes the current records as a sorted JSON array; caller holds the lock.
func (s *stateStore) save() {
	list := make([]*PluginRecord, 0, len(s.records))
	for _, r := range s.records {
		list = append(list, r)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
	b, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(s.path), 0o755)
	_ = os.WriteFile(s.path, b, 0o644) // best-effort, matching connectionsStore
}

func (s *stateStore) list() []PluginRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]PluginRecord, 0, len(s.records))
	for _, r := range s.records {
		out = append(out, r.clone())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (s *stateStore) get(id string) (PluginRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r, ok := s.records[id]; ok {
		return r.clone(), true
	}
	return PluginRecord{}, false
}

// put upserts a record and persists.
func (s *stateStore) put(r PluginRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := r.clone()
	s.records[r.ID] = &cp
	s.save()
}

// mutate applies fn to the stored record for id (if present) under the lock, then
// persists. Returns the updated copy.
func (s *stateStore) mutate(id string, fn func(*PluginRecord)) (PluginRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.records[id]
	if !ok {
		return PluginRecord{}, false
	}
	fn(r)
	s.save()
	return r.clone(), true
}

func (s *stateStore) remove(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.records[id]; !ok {
		return false
	}
	delete(s.records, id)
	s.save()
	return true
}
