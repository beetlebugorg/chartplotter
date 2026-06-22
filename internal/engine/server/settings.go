package server

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
)

// settings.go persists the frontend's DISPLAY settings (colour scheme, basemap,
// mariner/S-52 toggles, the cell-boundary toggle, …) server-side, so they survive
// a restart and are SHARED across every browser/screen pointed at this server —
// the app's "the server holds the state, screens stay in sync" model. The blob is
// opaque JSON owned by the frontend (same contract as the share snapshot); the
// server only stores and returns it. Mirrored to <dataDir>/client-settings.json
// (dataDir, not cacheDir — settings must survive a baked-tile cache wipe).

// maxSettings caps a POSTed settings blob — display settings are small JSON.
const maxSettings = 1 << 20 // 1 MiB

// settingsStore holds the latest display-settings blob in memory, mirrored to
// <dataDir>/client-settings.json so it survives a restart. The zero value is usable.
type settingsStore struct {
	mu   sync.RWMutex
	body []byte // raw settings JSON, or nil if none stored yet
	path string // <dataDir>/client-settings.json (set on first use)
}

// load reads the persisted settings from disk once, on first access.
func (s *settingsStore) load(dataDir string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.path != "" {
		return // already initialised
	}
	s.path = filepath.Join(dataDir, "client-settings.json")
	if b, err := os.ReadFile(s.path); err == nil {
		s.body = b
	}
}

func (s *settingsStore) get() []byte {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.body
}

func (s *settingsStore) set(body []byte) {
	s.mu.Lock()
	s.body = body
	path := s.path
	s.mu.Unlock()
	if path != "" {
		_ = os.MkdirAll(filepath.Dir(path), 0o755)
		_ = os.WriteFile(path, body, 0o644) // best-effort persistence
	}
}

// serveSettings handles GET /api/settings (return the stored blob, or `{}` when
// none) and POST /api/settings (store one). The blob is opaque JSON owned by the
// frontend; the server merges nothing and validates only the size.
func (s *Server) serveSettings(w http.ResponseWriter, r *http.Request) {
	s.settings.load(s.dataDir)
	switch r.Method {
	case http.MethodGet:
		body := s.settings.get()
		w.Header().Set("Content-Type", jsonCT)
		w.Header().Set("Cache-Control", "no-cache")
		if body == nil {
			io.WriteString(w, `{}`) // no settings stored yet → empty object
			return
		}
		w.Write(body)
	case http.MethodPost:
		body, err := io.ReadAll(io.LimitReader(r.Body, maxSettings+1))
		if err != nil {
			apiErr(w, http.StatusBadRequest, err.Error())
			return
		}
		if len(body) > maxSettings {
			apiErr(w, http.StatusRequestEntityTooLarge, "settings too large")
			return
		}
		s.settings.set(body)
		w.Header().Set("Content-Type", jsonCT)
		io.WriteString(w, `{"ok":true}`)
	default:
		apiErr(w, http.StatusMethodNotAllowed, "GET or POST only")
	}
}
