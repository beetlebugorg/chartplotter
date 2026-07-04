package server

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// serveOSM proxies the OpenStreetMap raster basemap through this server.
//
// The browser can't set the User-Agent that the OSM tile usage policy requires
// (User-Agent is a forbidden header for fetch/XHR), so a direct
// tile.openstreetmap.org request from the page gets a 403. The server fetches
// the tile with a compliant, app-identifying UA — which is also the correct
// way to be a good OSM citizen: one identified client, server-side cached.
//
// Route: GET /osm/{z}/{x}/{y}.png  → https://tile.openstreetmap.org/{z}/{x}/{y}.png
func (s *Server) serveOSM(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/osm/")
	z, x, y, ok := parseOSMPath(rest)
	if !ok {
		apiErr(w, http.StatusBadRequest, "path must be /osm/{z}/{x}/{y}.png")
		return
	}

	upstream := fmt.Sprintf("https://tile.openstreetmap.org/%d/%d/%d.png", z, x, y)
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, upstream, nil)
	if err != nil {
		apiErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// OSM's policy: a valid, identifying User-Agent (an app name + contact/URL).
	req.Header.Set("User-Agent", s.osmUserAgent())
	req.Header.Set("Referer", "https://github.com/beetlebugorg/chartplotter")

	resp, err := osmClient.Do(req)
	if err != nil {
		apiErr(w, http.StatusBadGateway, "osm fetch: "+err.Error())
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// Surface the upstream status (403/429/…) so the cause is visible.
		apiErr(w, http.StatusBadGateway, "osm upstream "+strconv.Itoa(resp.StatusCode))
		return
	}
	w.Header().Set("Content-Type", "image/png")
	// The z/x/y URL is immutable content; let the browser cache it a day.
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.WriteHeader(http.StatusOK)
	io.Copy(w, resp.Body)
}

// osmUserAgent identifies this app to OSM per their tile usage policy.
func (s *Server) osmUserAgent() string {
	v := s.Version
	if v == "" {
		v = "dev"
	}
	return "chartplotter/" + v + " (+https://github.com/beetlebugorg/chartplotter)"
}

var osmClient = &http.Client{Timeout: 15 * time.Second}

// parseOSMPath pulls z/x/y out of "{z}/{x}/{y}[.png]".
func parseOSMPath(rest string) (z, x, y int, ok bool) {
	if i := strings.IndexByte(rest, '?'); i >= 0 {
		rest = rest[:i]
	}
	parts := strings.Split(rest, "/")
	if len(parts) != 3 {
		return 0, 0, 0, false
	}
	last := parts[2]
	if i := strings.IndexByte(last, '.'); i >= 0 {
		if ext := last[i:]; ext != ".png" {
			return 0, 0, 0, false
		}
		last = last[:i]
	}
	var err1, err2, err3 error
	z, err1 = strconv.Atoi(parts[0])
	x, err2 = strconv.Atoi(parts[1])
	y, err3 = strconv.Atoi(last)
	if err1 != nil || err2 != nil || err3 != nil || z < 0 || z > 22 {
		return 0, 0, 0, false
	}
	return z, x, y, true
}
