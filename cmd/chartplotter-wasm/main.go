//go:build js && wasm

// chartplotter-wasm exposes the native ENC baker to the browser as a real-time
// MVT tile source. JS hands it the raw cell .000 bytes once (cpBakeLoad); the
// renderer then asks for individual tiles on demand (cpBakeTile z,x,y), each
// baked fresh from the in-memory Baker. No pre-baked .pmtiles — tiles are
// generated as the map requests them, so any z/x/y is available immediately
// (e.g. throwaway coarse-zoom tiles for a single-band download).
package main

import (
	"encoding/json"
	"fmt"
	"syscall/js"
	"time"

	"github.com/beetlebugorg/chartplotter/internal/engine/bake"
	"github.com/beetlebugorg/chartplotter/internal/engine/baker"
	"github.com/beetlebugorg/chartplotter/internal/engine/tile"
)

var (
	session *baker.Session
	scratch bake.TileScratch
)

// cpBakeReset() — start a fresh, empty baker session. Cells are then streamed in
// one at a time via cpBakeAddCell so the worker can yield between (large) cells
// instead of blocking on the whole set. Returns { ok } or { ok:false, error }.
func cpBakeReset(_ js.Value, _ []js.Value) any {
	s, err := baker.NewSession()
	if err != nil {
		return js.ValueOf(map[string]any{"ok": false, "error": err.Error()})
	}
	session = s
	return js.ValueOf(map[string]any{"ok": true})
}

// cpBakeAddCell(name, bytes) — parse one cell (Uint8Array) and add it to the
// current session. Tiles bake by full-scan over the loaded prims (no prebuilt
// emit index), so a coarse cell can overzoom into higher zooms where no finer
// cell covers. Returns { ok, name, ms } or { ok:false, name, error }.
func cpBakeAddCell(_ js.Value, args []js.Value) any {
	start := time.Now()
	name := args[0].String()
	if session == nil {
		if s, err := baker.NewSession(); err == nil {
			session = s
		} else {
			return js.ValueOf(map[string]any{"ok": false, "name": name, "error": err.Error()})
		}
	}
	u8 := args[1]
	buf := make([]byte, u8.Get("length").Int())
	js.CopyBytesToGo(buf, u8)
	bb, err := session.AddCellBytes(name, buf)
	if err != nil {
		return js.ValueOf(map[string]any{"ok": false, "name": name, "error": err.Error()})
	}
	return js.ValueOf(map[string]any{
		"ok": true, "name": name, "ms": time.Since(start).Milliseconds(),
		"bounds": []any{bb.MinLon, bb.MinLat, bb.MaxLon, bb.MaxLat}, // [W,S,E,N] — locate the cell
	})
}

// cpCellBounds(name, bytes) — parse one cell and return its footprint WITHOUT
// adding it to the session (cheap; used to locate/frame an uploaded cell before
// it bakes). Returns { ok, bounds:[w,s,e,n] } or { ok:false, error }.
func cpCellBounds(_ js.Value, args []js.Value) any {
	name := args[0].String()
	u8 := args[1]
	buf := make([]byte, u8.Get("length").Int())
	js.CopyBytesToGo(buf, u8)
	chart, err := baker.ParseCellBytes(name, buf)
	if err != nil {
		return js.ValueOf(map[string]any{"ok": false, "error": err.Error()})
	}
	bb := chart.Bounds()
	return js.ValueOf(map[string]any{
		"ok": true, "bounds": []any{bb.MinLon, bb.MinLat, bb.MaxLon, bb.MaxLat},
		"scale": int(chart.CompilationScale()), // CSCL → the app picks a detail zoom
	})
}

// cpBakeTile(z, x, y) — bake one MVT tile from the current session by full-scan
// over the loaded prims (the lazy loader keeps that set small). Returns a
// Uint8Array (the gzip-less MVT body) or null when the tile is empty.
func cpBakeTile(_ js.Value, args []js.Value) any {
	coord := tile.TileCoord{
		Z: uint32(args[0].Int()),
		X: uint32(args[1].Int()),
		Y: uint32(args[2].Int()),
	}
	if session == nil {
		if bake.TileDiag != nil {
			bake.TileDiag(fmt.Sprintf("tile %d/%d/%d: requested before baker session ready (empty)", coord.Z, coord.X, coord.Y))
		}
		return js.Null()
	}
	data := session.Baker.EmitTileInto(coord, baker.MVTExtent, baker.MVTBuffer, &scratch)
	if data == nil {
		return js.Null()
	}
	dst := js.Global().Get("Uint8Array").New(len(data))
	js.CopyBytesToJS(dst, data)
	return dst
}

// cpCoverage() — GeoJSON FeatureCollection (string) of every loaded cell's
// M_COVR data-coverage polygon (properties.cell = name). The debug overlay draws
// it to show where cells ACTUALLY have data (vs their bounding box), so nodata
// inside a polygon is a bug and nodata outside every polygon is a real gap.
func cpCoverage(_ js.Value, _ []js.Value) any {
	if session == nil {
		return js.ValueOf("")
	}
	feats := make([]map[string]any, 0)
	for _, cc := range session.Baker.Coverage() {
		feats = append(feats, map[string]any{
			"type":       "Feature",
			"properties": map[string]any{"cell": cc.Cell},
			"geometry":   map[string]any{"type": "Polygon", "coordinates": cc.Rings},
		})
	}
	out, err := json.Marshal(map[string]any{"type": "FeatureCollection", "features": feats})
	if err != nil {
		return js.ValueOf("")
	}
	return js.ValueOf(string(out))
}

// cpSetTileDiag(on) — toggle per-tile bake diagnostics. When on, each cpBakeTile
// logs `tile z/x/y: eligible=.. suppDown=.. suppUp=.. emptyGeom=.. empty=..` to
// the worker console, so a tile that bakes empty reveals where it lost its prims.
func cpSetTileDiag(_ js.Value, args []js.Value) any {
	if len(args) > 0 && args[0].Truthy() {
		bake.TileDiag = func(s string) {
			// Forward to the main thread (via the worker's cpDiag) so the line shows
			// in the page console, not just the worker context; fall back to println.
			if d := js.Global().Get("cpDiag"); d.Type() == js.TypeFunction {
				d.Invoke(s)
			} else {
				println("[baketile]", s)
			}
		}
	} else {
		bake.TileDiag = nil
	}
	return js.Undefined()
}

func main() {
	js.Global().Set("cpBakeReset", js.FuncOf(cpBakeReset))
	js.Global().Set("cpBakeAddCell", js.FuncOf(cpBakeAddCell))
	js.Global().Set("cpCellBounds", js.FuncOf(cpCellBounds))
	js.Global().Set("cpBakeTile", js.FuncOf(cpBakeTile))
	js.Global().Set("cpCoverage", js.FuncOf(cpCoverage))
	js.Global().Set("cpSetTileDiag", js.FuncOf(cpSetTileDiag))
	js.Global().Set("cpBakeReady", js.ValueOf(true))
	select {} // keep the instance alive for callbacks
}
