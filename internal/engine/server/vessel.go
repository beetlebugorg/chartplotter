package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// vessel.go exposes the shared NMEA0183 vessel state. GET /api/vessel returns a
// snapshot; /api/vessel/stream pushes coalesced deltas over SSE so every screen
// stays in sync with one long-lived connection instead of polling. Rendering
// (own-ship marker, AIS, instrument HUD) is the client's job — this only serves
// the model.

// serveVessel returns the current vessel-state snapshot as JSON.
func (s *Server) serveVessel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		apiErr(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	writeJSON(w, s.vessel.Snapshot())
}

// serveVesselStream streams vessel-state over SSE. A snapshot is emitted on
// connect and then whenever it changes, coalesced on a ~4 Hz tick so a chatty
// feed (10 Hz+) doesn't flood every screen.
func (s *Server) serveVesselStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := sseStart(w)
	if !ok {
		return
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	var last time.Time // VesselState.Updated of the last emitted snapshot
	first := true
	for {
		snap := s.vessel.Snapshot()
		if first || !snap.Updated.Equal(last) {
			first = false
			last = snap.Updated
			b, _ := json.Marshal(snap)
			fmt.Fprintf(w, "data: %s\n\n", b)
			flusher.Flush()
		}
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		}
	}
}

// serveAIS returns the current AIS target list as JSON.
func (s *Server) serveAIS(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		apiErr(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	writeJSON(w, map[string]any{"ok": true, "targets": s.nmeaMgr.AIS().Snapshot()})
}

// serveAISStream streams the AIS target list over SSE, emitting on connect and
// whenever the target set changes (cheap version-counter check on a ~1 Hz tick).
func (s *Server) serveAISStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := sseStart(w)
	if !ok {
		return
	}
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	ais := s.nmeaMgr.AIS()
	var lastVer uint64
	first := true
	for {
		if v := ais.Version(); first || v != lastVer {
			first = false
			lastVer = v
			b, _ := json.Marshal(map[string]any{"targets": ais.Snapshot()})
			fmt.Fprintf(w, "data: %s\n\n", b)
			flusher.Flush()
		}
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		}
	}
}

// sseStart writes the standard text/event-stream headers and returns the
// Flusher, or reports an error and returns ok=false if streaming is unsupported.
func sseStart(w http.ResponseWriter) (http.Flusher, bool) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		apiErr(w, http.StatusInternalServerError, "streaming unsupported")
		return nil, false
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	return flusher, true
}

// writeJSON marshals v as a JSON response (best-effort; marshal errors are rare
// for the small DTOs here and surface as an empty body).
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", jsonCT)
	w.Header().Set("Cache-Control", "no-cache")
	b, err := json.Marshal(v)
	if err != nil {
		apiErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Write(b)
}
