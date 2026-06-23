package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/beetlebugorg/chartplotter/internal/engine/nmea"
)

// connections.go wires the NMEA0183 connection manager (internal/engine/nmea)
// into the HTTP server. Unlike share/settings (opaque frontend JSON), connection
// configs are TYPED (the manager needs them), so this owns their schema and
// persists them to <dataDir>/connections.json (dataDir, not cacheDir — they must
// survive a baked-tile cache wipe). Each parsed sentence flows into one shared
// vessel-state Store, streamed to every screen via /api/vessel/stream; raw
// sentences fan out to /api/connections/<id>/raw for the wiring sniffer.

// maxConnBody caps a POSTed/PUT connection config — small JSON.
const maxConnBody = 64 << 10

// initNMEA builds the shared store + manager, loads persisted connections, and
// starts a live runner for each. Called once from New.
func (s *Server) initNMEA() {
	s.vessel = &nmea.Store{}
	s.rawHub = newRawHub()
	s.conns = &connectionsStore{path: filepath.Join(s.dataDir, "connections.json")}
	s.conns.load()
	s.nmeaMgr = nmea.NewManager(context.Background(), s.vessel, nmea.ManagerOpts{
		OnRaw: s.rawHub.publish,
	})
	for _, src := range s.conns.list() {
		s.nmeaMgr.Apply(src)
	}
}

// --- persistence -----------------------------------------------------------

// connectionsStore holds the configured Sources in memory, mirrored to
// connections.json. It owns id assignment. The zero value (with path set) is usable.
type connectionsStore struct {
	mu      sync.Mutex
	path    string
	sources []nmea.Source
	seq     int
}

func (c *connectionsStore) load() {
	c.mu.Lock()
	defer c.mu.Unlock()
	b, err := os.ReadFile(c.path)
	if err != nil {
		return
	}
	_ = json.Unmarshal(b, &c.sources)
}

// save writes the current list; caller holds the lock.
func (c *connectionsStore) save() {
	b, err := json.MarshalIndent(c.sources, "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(c.path), 0o755)
	_ = os.WriteFile(c.path, b, 0o644) // best-effort persistence
}

func (c *connectionsStore) list() []nmea.Source {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]nmea.Source, len(c.sources))
	copy(out, c.sources)
	return out
}

func (c *connectionsStore) get(id string) (nmea.Source, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, src := range c.sources {
		if src.ID == id {
			return src, true
		}
	}
	return nmea.Source{}, false
}

// add assigns an id, appends, persists, and returns the stored source.
func (c *connectionsStore) add(src nmea.Source) nmea.Source {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seq++
	src.ID = fmt.Sprintf("conn-%d-%d", time.Now().Unix(), c.seq)
	c.sources = append(c.sources, src)
	c.save()
	return src
}

// update replaces the config for id (keeping the id), persists, and returns it.
func (c *connectionsStore) update(id string, src nmea.Source) (nmea.Source, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.sources {
		if c.sources[i].ID == id {
			src.ID = id
			c.sources[i] = src
			c.save()
			return src, true
		}
	}
	return nmea.Source{}, false
}

func (c *connectionsStore) remove(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.sources {
		if c.sources[i].ID == id {
			c.sources = append(c.sources[:i], c.sources[i+1:]...)
			c.save()
			return true
		}
	}
	return false
}

// --- raw-sentence fan-out (sniffer) ----------------------------------------

// rawHub broadcasts raw sentences to per-connection SSE subscribers. Sends are
// non-blocking (a slow sniffer drops lines, never stalls the read loop).
type rawHub struct {
	mu   sync.Mutex
	subs map[int]rawSub
	seq  int
}

type rawSub struct {
	srcID string // only receive lines from this connection
	ch    chan string
}

func newRawHub() *rawHub { return &rawHub{subs: map[int]rawSub{}} }

// publish matches nmea.ManagerOpts.OnRaw; it is called from each runner's read loop.
func (h *rawHub) publish(srcID, line string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, sub := range h.subs {
		if sub.srcID != srcID {
			continue
		}
		select {
		case sub.ch <- line:
		default: // subscriber is behind — drop this line
		}
	}
}

func (h *rawHub) subscribe(srcID string) (int, chan string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.seq++
	tok := h.seq
	ch := make(chan string, 256)
	h.subs[tok] = rawSub{srcID: srcID, ch: ch}
	return tok, ch
}

func (h *rawHub) unsubscribe(tok int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.subs, tok)
}

// --- HTTP: CRUD ------------------------------------------------------------

// connectionDTO is a connection plus its live status, the unit the UI lists.
type connectionDTO struct {
	Source nmea.Source       `json:"source"`
	Status nmea.SourceStatus `json:"status"`
}

func (s *Server) dto(src nmea.Source) connectionDTO {
	st, ok := s.nmeaMgr.Status(src.ID)
	if !ok {
		st = nmea.SourceStatus{State: nmea.StateDisabled}
	}
	return connectionDTO{Source: src, Status: st}
}

// serveConnections handles GET /api/connections (list + status) and
// POST /api/connections (create one).
func (s *Server) serveConnections(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		srcs := s.conns.list()
		out := make([]connectionDTO, 0, len(srcs))
		for _, src := range srcs {
			out = append(out, s.dto(src))
		}
		writeJSON(w, map[string]any{"ok": true, "connections": out})
	case http.MethodPost:
		src, err := decodeSource(r)
		if err != nil {
			apiErr(w, http.StatusBadRequest, err.Error())
			return
		}
		stored := s.conns.add(src)
		s.nmeaMgr.Apply(stored)
		writeJSON(w, map[string]any{"ok": true, "connection": s.dto(stored)})
	default:
		apiErr(w, http.StatusMethodNotAllowed, "GET or POST only")
	}
}

// serveConnection handles a single connection: GET/PUT/DELETE /api/connections/<id>,
// and the SSE sniffer at /api/connections/<id>/raw.
func (s *Server) serveConnection(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/connections/")
	id, tail, _ := strings.Cut(rest, "/")
	if !isConnID(id) {
		apiErr(w, http.StatusBadRequest, "bad connection id")
		return
	}
	if tail == "raw" {
		s.serveConnectionRaw(w, r, id)
		return
	}
	if tail != "" {
		apiErr(w, http.StatusNotFound, "unknown endpoint")
		return
	}
	switch r.Method {
	case http.MethodGet:
		src, ok := s.conns.get(id)
		if !ok {
			apiErr(w, http.StatusNotFound, "no such connection")
			return
		}
		writeJSON(w, map[string]any{"ok": true, "connection": s.dto(src)})
	case http.MethodPut, http.MethodPatch:
		src, err := decodeSource(r)
		if err != nil {
			apiErr(w, http.StatusBadRequest, err.Error())
			return
		}
		stored, ok := s.conns.update(id, src)
		if !ok {
			apiErr(w, http.StatusNotFound, "no such connection")
			return
		}
		s.nmeaMgr.Apply(stored) // restart the runner with the new config
		writeJSON(w, map[string]any{"ok": true, "connection": s.dto(stored)})
	case http.MethodDelete:
		if !s.conns.remove(id) {
			apiErr(w, http.StatusNotFound, "no such connection")
			return
		}
		s.nmeaMgr.Remove(id)
		writeJSON(w, map[string]any{"ok": true})
	default:
		apiErr(w, http.StatusMethodNotAllowed, "GET, PUT, or DELETE")
	}
}

// decodeSource reads + validates a connection config from the request body,
// applying v1 defaults (tcp-client / nmea0183 / in).
func decodeSource(r *http.Request) (nmea.Source, error) {
	var src nmea.Source
	if err := json.NewDecoder(io.LimitReader(r.Body, maxConnBody)).Decode(&src); err != nil {
		return nmea.Source{}, fmt.Errorf("bad JSON: %w", err)
	}
	if src.Transport == "" {
		src.Transport = nmea.TransportTCPClient
	}
	if src.Transport != nmea.TransportTCPClient {
		return nmea.Source{}, fmt.Errorf("unsupported transport %q (v1: tcp-client)", src.Transport)
	}
	if src.Protocol == "" {
		src.Protocol = "nmea0183"
	}
	if src.Direction == "" {
		src.Direction = "in"
	}
	if strings.TrimSpace(src.Host) == "" {
		return nmea.Source{}, fmt.Errorf("host required")
	}
	if src.Port < 1 || src.Port > 65535 {
		return nmea.Source{}, fmt.Errorf("port must be 1–65535")
	}
	src.ID = "" // server assigns / path supplies
	return src, nil
}

// --- HTTP: SSE streams -----------------------------------------------------

// serveConnectionsStream pushes the full status map whenever a badge changes.
func (s *Server) serveConnectionsStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := sseStart(w)
	if !ok {
		return
	}
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	last := ""
	for {
		b, _ := json.Marshal(map[string]any{"statuses": s.nmeaMgr.Statuses()})
		if line := string(b); line != last {
			last = line
			fmt.Fprintf(w, "data: %s\n\n", line)
			flusher.Flush()
		}
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		}
	}
}

// serveConnectionRaw streams raw sentences from one connection as they arrive —
// the wiring sniffer. Lines are escaped into a JSON string per SSE event.
func (s *Server) serveConnectionRaw(w http.ResponseWriter, r *http.Request, id string) {
	flusher, ok := sseStart(w)
	if !ok {
		return
	}
	tok, ch := s.rawHub.subscribe(id)
	defer s.rawHub.unsubscribe(tok)
	// Keep-alive so proxies don't time the connection out when a source is quiet.
	keep := time.NewTicker(15 * time.Second)
	defer keep.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case line := <-ch:
			b, _ := json.Marshal(line)
			fmt.Fprintf(w, "data: %s\n\n", b)
			flusher.Flush()
		case <-keep.C:
			fmt.Fprint(w, ": keep-alive\n\n")
			flusher.Flush()
		}
	}
}

// isConnID accepts the ids we mint (conn-<unix>-<seq>) — a safe path component.
func isConnID(s string) bool {
	if s == "" || len(s) > 40 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}
