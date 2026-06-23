package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/beetlebugorg/chartplotter/internal/engine/nmea"
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

// Collision thresholds: a target is "dangerous" when its closest approach is
// within cpaDangerNm AND that approach is in the future within tcpaDangerMin.
const (
	cpaDangerNm   = 0.5
	tcpaDangerMin = 12
)

// aisDTO is an AIS target enriched with the collision geometry against own-ship.
type aisDTO struct {
	nmea.AISTarget
	CpaNm   *float64 `json:"cpaNm,omitempty"`
	TcpaMin *float64 `json:"tcpaMin,omitempty"`
	Danger  bool     `json:"danger,omitempty"`
}

// aisDTOs builds the AIS list with CPA/TCPA/danger computed against the current
// own-ship fix (only when own-ship has position + course + speed and the target
// has a course + speed).
func (s *Server) aisDTOs() []aisDTO {
	targets := s.nmeaMgr.AIS().Snapshot()
	nav := s.vessel.Snapshot().Navigation
	haveOwn := nav.Position != nil && nav.SOG != nil && (nav.COGTrue != nil || nav.HeadingTrue != nil)
	var oLat, oLon, oCog, oSog float64
	if haveOwn {
		oLat, oLon, oSog = nav.Position.Lat, nav.Position.Lon, *nav.SOG
		if nav.COGTrue != nil {
			oCog = *nav.COGTrue
		} else {
			oCog = *nav.HeadingTrue
		}
	}
	out := make([]aisDTO, 0, len(targets))
	for _, t := range targets {
		d := aisDTO{AISTarget: t}
		if haveOwn && t.COG != nil && t.SOG != nil {
			if cpa, tcpa, ok := nmea.CPA(oLat, oLon, oCog, oSog, t.Lat, t.Lon, *t.COG, *t.SOG); ok {
				cpaV, tcpaV := cpa, tcpa
				d.CpaNm, d.TcpaMin = &cpaV, &tcpaV
				d.Danger = cpa < cpaDangerNm && tcpa >= 0 && tcpa <= tcpaDangerMin
			}
		}
		out = append(out, d)
	}
	return out
}

// serveAIS returns the current AIS target list (with collision geometry) as JSON.
func (s *Server) serveAIS(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		apiErr(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	writeJSON(w, map[string]any{"ok": true, "targets": s.aisDTOs()})
}

// serveAISStream streams the AIS list over SSE, re-emitting when the target set
// changes OR own-ship moves (CPA is relative to own-ship, so it must refresh as
// we move) — checked on a 1 Hz tick.
func (s *Server) serveAISStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := sseStart(w)
	if !ok {
		return
	}
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	ais := s.nmeaMgr.AIS()
	var lastVer uint64
	var lastUpd time.Time
	first := true
	for {
		v, upd := ais.Version(), s.vessel.Snapshot().Updated
		if first || v != lastVer || !upd.Equal(lastUpd) {
			first, lastVer, lastUpd = false, v, upd
			b, _ := json.Marshal(map[string]any{"targets": s.aisDTOs()})
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
