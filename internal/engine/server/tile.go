package server

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/beetlebugorg/chartplotter/internal/engine/bake"
	"github.com/beetlebugorg/chartplotter/internal/engine/baker"
	"github.com/beetlebugorg/chartplotter/internal/engine/pmtiles"
	"github.com/beetlebugorg/chartplotter/internal/engine/tile"
)

// serveTile bakes ONE MVT tile on demand from cached raw cells — the hittable URL
// behind the tile-debugger's "inspect this tile" button. It re-bakes server-side
// with the SAME baker the browser runs (NewSession → AddCellBytes → EmitTileInto,
// no prebuilt index — exactly the wasm cpBakeTile path), so the precise bytes for
// a z/x/y can be pulled with curl and fed to any MVT inspector.
//
//	GET /api/tile/{z}/{x}/{y}[?cells=US2EC02M,US5MD1MC]
//
// With ?cells it bakes from just those cached cells (what the app loaded for the
// tile); without it, from every cell in the ENC_ROOT cache. By default it returns
// the raw (un-gzipped) MVT body; with ?format=pmtiles it wraps that single tile in
// a valid PMTiles v3 archive (so it loads in pmtiles.io / any PMTiles viewer — a
// bare .pbf trips "wrong magic number"). 204 when the tile is empty, 400/404 on
// bad input / nothing cached. CORS-open so a remote viewer can fetch it.
func (s *Server) serveTile(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if r.Method != http.MethodGet {
		apiErr(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	z, x, y, ok := parseTilePath(r.URL.Path)
	if !ok {
		apiErr(w, http.StatusBadRequest, "path must be /api/tile/{z}/{x}/{y}")
		return
	}
	names := s.tileCells(r.URL.Query().Get("cells"))
	if len(names) == 0 {
		apiErr(w, http.StatusNotFound, "no cached cells to bake (PUT cells first, or pass ?cells=)")
		return
	}
	sess, err := baker.NewSession()
	if err != nil {
		apiErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	added := 0
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(s.cacheDir, "ENC_ROOT", name, name+".000"))
		if err != nil {
			continue // skip cells that aren't in the cache
		}
		if err := sess.AddCellBytes(name, data); err == nil {
			added++
		}
	}
	if added == 0 {
		apiErr(w, http.StatusNotFound, "none of the requested cells are cached")
		return
	}
	var scratch bake.TileScratch
	body := sess.Baker.EmitTileInto(tile.TileCoord{Z: z, X: x, Y: y}, baker.MVTExtent, baker.MVTBuffer, &scratch)
	if len(body) == 0 {
		w.WriteHeader(http.StatusNoContent) // baked clean: no features in this tile
		return
	}
	base := strconv.FormatUint(uint64(z), 10) + "-" + strconv.FormatUint(uint64(x), 10) + "-" + strconv.FormatUint(uint64(y), 10)
	w.Header().Set("Cache-Control", "no-cache")
	if r.URL.Query().Get("format") == "pmtiles" {
		// Wrap the one tile in a minimal PMTiles archive so PMTiles viewers accept
		// it. Buffer first so a write error surfaces before the status is sent.
		pb := pmtiles.New()
		pb.AddTile(uint8(z), x, y, body)
		var buf bytes.Buffer
		if err := pb.WriteArchive(&buf); err != nil {
			apiErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", "attachment; filename=\""+base+".pmtiles\"")
		w.Write(buf.Bytes())
		return
	}
	w.Header().Set("Content-Type", "application/vnd.mapbox-vector-tile")
	w.Header().Set("Content-Disposition", "inline; filename=\""+base+".pbf\"")
	w.Write(body)
}

// parseTilePath pulls z/x/y out of /api/tile/{z}/{x}/{y}.
func parseTilePath(p string) (z, x, y uint32, ok bool) {
	parts := strings.Split(strings.Trim(strings.TrimPrefix(p, "/api/tile/"), "/"), "/")
	if len(parts) != 3 {
		return 0, 0, 0, false
	}
	zi, e1 := strconv.ParseUint(parts[0], 10, 32)
	xi, e2 := strconv.ParseUint(parts[1], 10, 32)
	yi, e3 := strconv.ParseUint(parts[2], 10, 32)
	if e1 != nil || e2 != nil || e3 != nil || zi > 24 {
		return 0, 0, 0, false
	}
	return uint32(zi), uint32(xi), uint32(yi), true
}

// tileCells resolves which cells to bake: the explicit ?cells=A,B,C list (filtered
// to valid cell names), or — when empty — every cell in the ENC_ROOT cache.
func (s *Server) tileCells(csv string) []string {
	if strings.TrimSpace(csv) != "" {
		var out []string
		for _, n := range strings.Split(csv, ",") {
			if n = strings.TrimSpace(n); isCellName(n) {
				out = append(out, n)
			}
		}
		return out
	}
	entries, err := os.ReadDir(filepath.Join(s.cacheDir, "ENC_ROOT"))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() && isCellName(e.Name()) {
			out = append(out, e.Name())
		}
	}
	return out
}
