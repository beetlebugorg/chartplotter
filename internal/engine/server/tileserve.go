package server

import (
	"compress/gzip"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/beetlebugorg/chartplotter/internal/engine/tilesource"
)

// serveTileSet serves MVT tiles (and a TileJSON descriptor) from a registered
// tile set. The client points MapLibre straight at the tile template — HTTP tiles
// need no custom protocol:
//
//	GET /tiles/{set}/{z}/{x}/{y}[.mvt]   one tile (raw MVT; 204 if blank)
//	GET /tiles/{set}.json                a TileJSON descriptor (bounds, zooms)
//
// {set} is a registered prebaked archive (basename of a .pmtiles/.mbtiles under
// <cache>/tiles) or the reserved "dynamic" set baked on demand from cached cells.
// CORS-open so a static-hosted client can fetch from a different origin.
func (s *Server) serveTileSet(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if r.Method != http.MethodGet {
		apiErr(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/tiles/")

	// /tiles/ — list registered set names (the lazy "dynamic" set is omitted until
	// it has been built).
	if rest == "" {
		w.Header().Set("Content-Type", jsonCT)
		fmt.Fprintf(w, `{"sets":[`)
		for i, n := range s.sets.names() {
			if i > 0 {
				fmt.Fprint(w, ",")
			}
			fmt.Fprintf(w, "%q", n)
		}
		fmt.Fprint(w, "]}")
		return
	}

	// /tiles/{set}.json — TileJSON descriptor (no further path segments).
	if name, ok := strings.CutSuffix(rest, ".json"); ok && !strings.Contains(name, "/") {
		s.serveTileJSON(w, r, name)
		return
	}

	set, z, x, y, ok := parseTileSetPath(rest)
	if !ok {
		apiErr(w, http.StatusBadRequest, "path must be /tiles/{set}/{z}/{x}/{y}[.mvt]")
		return
	}
	src, ok := s.lookupSet(set)
	if !ok {
		apiErr(w, http.StatusNotFound, "unknown tile set")
		return
	}
	body, err := src.Tile(uint8(z), x, y)
	if err != nil {
		apiErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if len(body) == 0 {
		w.WriteHeader(http.StatusNoContent) // blank/missing tile
		return
	}
	w.Header().Set("Content-Type", "application/vnd.mapbox-vector-tile")
	w.Header().Set("Cache-Control", "no-cache")
	// The backend returns decompressed MVT; gzip on the wire when the client asks,
	// to claw back the size advantage prebaked archives store the tiles with.
	if acceptsGzip(r) {
		w.Header().Set("Content-Encoding", "gzip")
		w.WriteHeader(http.StatusOK)
		zw := gzip.NewWriter(w)
		zw.Write(body)
		zw.Close()
		return
	}
	w.Write(body)
}

// lookupSet resolves a set name to a registered backend.
func (s *Server) lookupSet(name string) (tilesource.TileSource, bool) {
	return s.sets.get(name)
}

// serveTileJSON returns a minimal TileJSON 3.0 descriptor for a set, so a client
// can configure its MapLibre source with `url: "/tiles/{set}.json"`.
func (s *Server) serveTileJSON(w http.ResponseWriter, r *http.Request, name string) {
	src, ok := s.lookupSet(name)
	if !ok {
		apiErr(w, http.StatusNotFound, "unknown tile set")
		return
	}
	m := src.Meta()
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	// Bake GENERATION token stamped into the tile URL — the source archive's mtime,
	// which changes every time the set is re-baked (writeAndRegister renames a fresh
	// file into place). The client re-fetches this TileJSON (it's no-cache) after a
	// re-bake, gets a new ?g, and its tile URLs change — so the browser/MapLibre tile
	// caches are bypassed by content, not a fragile client-side counter. serveTile
	// ignores the query, so ?g is purely a cache key.
	gen := int64(0)
	if p, ok := s.packPath(name); ok {
		if fi, err := os.Stat(p); err == nil {
			gen = fi.ModTime().UnixNano()
		}
	}
	tilesURL := fmt.Sprintf("%s://%s/tiles/%s/{z}/{x}/{y}.mvt?g=%d", scheme, r.Host, name, gen)
	w.Header().Set("Content-Type", jsonCT)
	w.Header().Set("Cache-Control", "no-cache")
	fmt.Fprintf(w,
		`{"tilejson":"3.0.0","scheme":"xyz","format":"pbf","tiles":[%q],"minzoom":%d,"maxzoom":%d,"bounds":[%g,%g,%g,%g],"center":[%g,%g,%d]}`,
		tilesURL, m.MinZoom, m.MaxZoom, m.W, m.S, m.E, m.N,
		(m.W+m.E)/2, (m.S+m.N)/2, m.MinZoom,
	)
}

// parseTileSetPath pulls set/z/x/y out of "{set}/{z}/{x}/{y}[.mvt]" (the path with
// the /tiles/ prefix already removed). The .mvt / .pbf extension is optional.
func parseTileSetPath(rest string) (set string, z, x, y uint32, ok bool) {
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) != 4 {
		return "", 0, 0, 0, false
	}
	set = parts[0]
	if !isSetName(set) {
		return "", 0, 0, 0, false
	}
	last := parts[3]
	if i := strings.IndexByte(last, '.'); i >= 0 { // strip .mvt / .pbf
		ext := last[i:]
		if ext != ".mvt" && ext != ".pbf" {
			return "", 0, 0, 0, false
		}
		last = last[:i]
	}
	zi, e1 := strconv.ParseUint(parts[1], 10, 32)
	xi, e2 := strconv.ParseUint(parts[2], 10, 32)
	yi, e3 := strconv.ParseUint(last, 10, 32)
	if e1 != nil || e2 != nil || e3 != nil || zi > 24 {
		return "", 0, 0, 0, false
	}
	return set, uint32(zi), uint32(xi), uint32(yi), true
}

// acceptsGzip reports whether the client advertised gzip in Accept-Encoding.
func acceptsGzip(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept-Encoding"), "gzip")
}
