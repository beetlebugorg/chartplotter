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
	// Cache policy first, so it rides the 204 too: an EMPTY tile at a given ?g is
	// as content-addressed as a full one (emptiness is baked into that generation),
	// and empty ocean tiles are the MAJORITY of a viewport's grid — caching those
	// 204s saves the most round-trips. Tiles are immutable per bake generation: the
	// ?g token in the URL (the pack archive's mtime) changes on every re-bake, so a
	// given tile URL always maps to identical bytes/emptiness and can cache forever.
	// The live/dynamic set carries no generation (?g absent or 0) and regenerates on
	// demand, so it stays no-cache. Keying off the token — not pack-vs-live plumbing
	// — ties the policy exactly to the content-addressing guarantee.
	if g := r.URL.Query().Get("g"); g != "" && g != "0" {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	if len(body) == 0 {
		w.WriteHeader(http.StatusNoContent) // blank/missing tile (still cacheable per ?g)
		return
	}
	// Tiles serve BYTES-VERBATIM in the set's stored encoding (no transcode).
	// MLT has no registered media type, so an .mvt URL carrying MLT bytes goes
	// out as application/octet-stream — MapLibre keys its decoder off the source
	// `encoding` hint (style/TileJSON), never the content type.
	if src.Meta().TileType == "mlt" {
		w.Header().Set("Content-Type", "application/octet-stream")
	} else {
		w.Header().Set("Content-Type", "application/vnd.mapbox-vector-tile")
	}
	// The backend returns decompressed tiles; gzip on the wire when the client asks,
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

// engineForSet reports the tile57 engine commit behind a set's tiles. A baked
// pack answers with BAKE-TIME truth — the commit stamped into its .enginever
// sidecar when it was baked ("pre-stamp" for packs baked before stamping) —
// while a live set (--tile57 / dynamic, no pack path) generates tiles on demand
// inside the RUNNING binary, so it answers with the build's own engine commit.
func (s *Server) engineForSet(name string) string {
	if p, ok := s.packPath(name); ok {
		if b, err := os.ReadFile(p + engineVerExt); err == nil {
			if v := strings.TrimSpace(string(b)); v != "" {
				return v
			}
		}
		return "pre-stamp"
	}
	if s.EngineCommit != "" {
		return s.EngineCommit
	}
	return "unknown"
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
	tilesURL := fmt.Sprintf("%s://%s/tiles/%s/{z}/{x}/{y}.mvt%s", scheme, r.Host, name, genQuery(s.packGen(name)))
	// SCAMIN manifest (from the archive metadata): the client builds one native-
	// minzoom bucket layer per value at load — no runtime probe/collect/setStyle.
	scaminJSON := ""
	if len(m.Scamin) > 0 {
		parts := make([]string, len(m.Scamin))
		for i, v := range m.Scamin {
			parts[i] = strconv.FormatUint(uint64(v), 10)
		}
		scaminJSON = `,"scamin":[` + strings.Join(parts, ",") + `]`
	}
	// Tile-encoding hint: an MLT set (the tile57 default bake format) advertises
	// `"encoding":"mlt"` — maplibre-gl (>=5.12) propagates the TileJSON field onto
	// the vector source and switches its worker to the native MLT decoder. MVT
	// sets emit nothing extra (the MapLibre default), keeping their TileJSON as-is.
	format, encodingJSON := "pbf", ""
	if m.TileType == "mlt" {
		format, encodingJSON = "mlt", `,"encoding":"mlt"`
	}
	w.Header().Set("Content-Type", jsonCT)
	w.Header().Set("Cache-Control", "no-cache")
	// `engine` is the tile57 engine commit behind this set's tiles (bake-time for
	// packs, the running binary for live sets — see engineForSet). The client's
	// attribution stamp reads it from this TileJSON, its one per-set metadata fetch.
	fmt.Fprintf(w,
		`{"tilejson":"3.0.0","scheme":"xyz","format":%q%s,"engine":%q,"tiles":[%q],"minzoom":%d,"maxzoom":%d,"bounds":[%g,%g,%g,%g],"center":[%g,%g,%d]%s}`,
		format, encodingJSON, s.engineForSet(name), tilesURL, m.MinZoom, m.MaxZoom, m.W, m.S, m.E, m.N,
		(m.W+m.E)/2, (m.S+m.N)/2, m.MinZoom, scaminJSON,
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

// writeMaybeGzip writes body with contentType, gzip-compressed on the wire when the
// client accepts it. Large JSON (the engine style is multi-MB) compresses ~10×, so this
// is worth it for /api/style.json and /api/style-diff. Sets Cache-Control:no-cache; the
// caller sets any CORS headers first.
func writeMaybeGzip(w http.ResponseWriter, r *http.Request, contentType string, body []byte) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-cache")
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
