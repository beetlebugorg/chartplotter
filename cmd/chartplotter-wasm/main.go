//go:build js && wasm

// chartplotter-wasm exposes the native ENC baker to the browser as a real-time
// MVT tile source. JS hands it the raw cell .000 bytes once (cpBakeLoad); the
// renderer then asks for individual tiles on demand (cpBakeTile z,x,y), each
// baked fresh from the in-memory Baker. No pre-baked .pmtiles — tiles are
// generated as the map requests them, so any z/x/y is available immediately
// (e.g. throwaway coarse-zoom tiles for a single-band download).
package main

import (
	"syscall/js"
	"time"

	"github.com/beetlebugorg/chartplotter/internal/engine/bake"
	"github.com/beetlebugorg/chartplotter/internal/engine/baker"
	"github.com/beetlebugorg/chartplotter/internal/engine/tile"
)

var (
	session *baker.Session
	scratch bake.TileScratch
	dirty   bool // a cell was added since the last emit-index build
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
	dirty = false
	return js.ValueOf(map[string]any{"ok": true})
}

// cpBakeAddCell(name, bytes) — parse one cell (Uint8Array) and add it to the
// current session. Cheap relative to a full reload: the heavy emit-index rebuild
// is deferred to the next cpBakeTile. Returns { ok, name, ms } or { ok:false,
// name, error }.
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
	if err := session.AddCellBytes(name, buf); err != nil {
		return js.ValueOf(map[string]any{"ok": false, "name": name, "error": err.Error()})
	}
	dirty = true
	return js.ValueOf(map[string]any{"ok": true, "name": name, "ms": time.Since(start).Milliseconds()})
}

// cpBakeTile(z, x, y) — bake one MVT tile from the current session. Returns a
// Uint8Array (the gzip-less MVT body) or null when the tile is empty. Rebuilds
// the emit index first if a cell was added since the last bake.
func cpBakeTile(_ js.Value, args []js.Value) any {
	if session == nil {
		return js.Null()
	}
	if dirty {
		session.Baker.BuildEmitIndex(baker.MVTExtent, baker.MVTBuffer)
		dirty = false
	}
	coord := tile.TileCoord{
		Z: uint32(args[0].Int()),
		X: uint32(args[1].Int()),
		Y: uint32(args[2].Int()),
	}
	data := session.Baker.EmitTileInto(coord, baker.MVTExtent, baker.MVTBuffer, &scratch)
	if data == nil {
		return js.Null()
	}
	dst := js.Global().Get("Uint8Array").New(len(data))
	js.CopyBytesToJS(dst, data)
	return dst
}

func main() {
	js.Global().Set("cpBakeReset", js.FuncOf(cpBakeReset))
	js.Global().Set("cpBakeAddCell", js.FuncOf(cpBakeAddCell))
	js.Global().Set("cpBakeTile", js.FuncOf(cpBakeTile))
	js.Global().Set("cpBakeReady", js.ValueOf(true))
	select {} // keep the instance alive for callbacks
}
