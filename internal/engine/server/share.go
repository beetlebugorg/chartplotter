package server

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// share.go implements the "share my view" path: the running webapp POSTs a
// snapshot (camera + installed cell list) which the server holds as the single
// latest snapshot, and a fresh browser opening <origin>/#share GETs it back to
// reconstruct the exact same scene. Cells the server doesn't already have cached
// (e.g. hand-imported, non-NOAA cells) are PUT into the same ENC_ROOT cache that
// GET /api/cell serves from, so the reconstructing browser can pull them by name.

// maxShareSnapshot caps a POSTed snapshot (camera + cell-name list — small JSON;
// cell BYTES go through PUT /api/cell, not here).
const maxShareSnapshot = 1 << 20 // 1 MiB

// maxCellUpload caps an uploaded raw .000 cell. NOAA base cells are well under
// this; the limit just stops an unbounded body.
const maxCellUpload = 64 << 20 // 64 MiB

// shareStore holds the single latest view snapshot in memory, mirrored to
// cacheDir/share.json so it survives a server restart. The zero value is usable.
type shareStore struct {
	mu   sync.RWMutex
	body []byte // raw snapshot JSON, or nil if none stored yet
	path string // cacheDir/share.json (set on first use)
}

// load reads the persisted snapshot from disk once, on first access.
func (s *shareStore) load(cacheDir string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.path != "" {
		return // already initialised
	}
	s.path = filepath.Join(cacheDir, "share.json")
	if b, err := os.ReadFile(s.path); err == nil {
		s.body = b
	}
}

func (s *shareStore) get() []byte {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.body
}

func (s *shareStore) set(body []byte) {
	s.mu.Lock()
	s.body = body
	path := s.path
	s.mu.Unlock()
	if path != "" {
		_ = os.WriteFile(path, body, 0o644) // best-effort persistence
	}
}

// serveShare handles GET /api/share (return the latest snapshot) and
// POST /api/share (store one). The snapshot is opaque JSON owned by the frontend.
func (s *Server) serveShare(w http.ResponseWriter, r *http.Request) {
	s.share.load(s.cacheDir)
	switch r.Method {
	case http.MethodGet:
		body := s.share.get()
		if body == nil {
			apiErr(w, http.StatusNotFound, "no shared view yet")
			return
		}
		w.Header().Set("Content-Type", jsonCT)
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(body)
	case http.MethodPost:
		body, err := io.ReadAll(io.LimitReader(r.Body, maxShareSnapshot+1))
		if err != nil {
			apiErr(w, http.StatusBadRequest, err.Error())
			return
		}
		if len(body) > maxShareSnapshot {
			apiErr(w, http.StatusRequestEntityTooLarge, "snapshot too large")
			return
		}
		s.share.set(body)
		w.Header().Set("Content-Type", jsonCT)
		io.WriteString(w, `{"ok":true}`)
	default:
		apiErr(w, http.StatusMethodNotAllowed, "GET or POST only")
	}
}

// uploadCell handles PUT /api/cell/<NAME>: store an uploaded raw S-57 base cell
// (.000) into the ENC_ROOT cache so a subsequent GET /api/cell/<NAME> serves it.
// This is how the share-publishing browser hands the server cells it can't fetch
// itself (hand-imported, non-NOAA cells). name is validated like serveCell.
func (s *Server) uploadCell(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/cell/"), ".000")
	if name == "" || !isCellName(name) {
		apiErr(w, http.StatusBadRequest, "bad cell name")
		return
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, maxCellUpload+1))
	if err != nil {
		apiErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(data) == 0 {
		apiErr(w, http.StatusBadRequest, "empty body")
		return
	}
	if len(data) > maxCellUpload {
		apiErr(w, http.StatusRequestEntityTooLarge, "cell too large")
		return
	}
	cpath := filepath.Join(s.cacheDir, "ENC_ROOT", name, name+".000")
	if err := os.MkdirAll(filepath.Dir(cpath), 0o755); err != nil {
		apiErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := os.WriteFile(cpath, data, 0o644); err != nil {
		apiErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", jsonCT)
	io.WriteString(w, `{"ok":true}`)
}
