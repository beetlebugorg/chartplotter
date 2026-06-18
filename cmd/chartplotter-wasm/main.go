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
	theBaker *bake.Baker
	scratch  bake.TileScratch
)

// cpBakeLoad(cells) — cells is a JS object { "<cellName>": Uint8Array(.000 bytes) }.
// Parses + indexes them into a Baker. Returns { ok, names:[…], ms } (or { ok:false,
// error }). Replaces any previously-loaded set.
func cpBakeLoad(_ js.Value, args []js.Value) any {
	start := time.Now()
	obj := args[0]
	keys := js.Global().Get("Object").Call("keys", obj)
	cells := make(map[string][]byte, keys.Length())
	for i := 0; i < keys.Length(); i++ {
		name := keys.Index(i).String()
		u8 := obj.Get(name)
		buf := make([]byte, u8.Get("length").Int())
		js.CopyBytesToGo(buf, u8)
		cells[name] = buf
	}

	b, names, err := baker.BuildBaker(cells, nil)
	if err != nil {
		return js.ValueOf(map[string]any{"ok": false, "error": err.Error()})
	}
	b.BuildEmitIndex(baker.MVTExtent, baker.MVTBuffer)
	theBaker = b

	jsNames := make([]any, len(names))
	for i, n := range names {
		jsNames[i] = n
	}
	return js.ValueOf(map[string]any{
		"ok":    true,
		"names": jsNames,
		"ms":    time.Since(start).Milliseconds(),
	})
}

// cpBakeTile(z, x, y) — bake one MVT tile from the loaded Baker. Returns a
// Uint8Array (the gzip-less MVT body) or null when the tile is empty.
func cpBakeTile(_ js.Value, args []js.Value) any {
	if theBaker == nil {
		return js.Null()
	}
	coord := tile.TileCoord{
		Z: uint32(args[0].Int()),
		X: uint32(args[1].Int()),
		Y: uint32(args[2].Int()),
	}
	data := theBaker.EmitTileInto(coord, baker.MVTExtent, baker.MVTBuffer, &scratch)
	if data == nil {
		return js.Null()
	}
	dst := js.Global().Get("Uint8Array").New(len(data))
	js.CopyBytesToJS(dst, data)
	return dst
}

func main() {
	js.Global().Set("cpBakeLoad", js.FuncOf(cpBakeLoad))
	js.Global().Set("cpBakeTile", js.FuncOf(cpBakeTile))
	js.Global().Set("cpBakeReady", js.ValueOf(true))
	select {} // keep the instance alive for callbacks
}
