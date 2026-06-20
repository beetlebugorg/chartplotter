# Server-side migration: bake + serve in Go, retire the wasm baker

Status: Phases 1–2 done. Owner: see git. Supersedes the in-browser baking model.

## Decision

Move all ENC baking server-side (native Go), serve tiles from the existing Go
server, and **delete the in-browser wasm baker**. Keep Go/wasm on the client only
for selective shared pure-compute (S-52 symbology, S-57 decode), not UI.

Rationale: the prebaked national charts are already baked by the native CLI; the
wasm baker existed solely for no-upload in-browser import, which we are dropping.
Deleting it removes the RAM-survival complexity (uint32 fixed-point geometry,
per-band streaming, dropped overzoom buffer) and the recurring CLI-vs-wasm
divergence (ENC-update application, TXTDSC/PICREP aux files).

A Go HTTP server already exists: `cmd/chartplotter serve` → `internal/engine/server`
(embeds `web/`, `/api/proxy` NOAA relay, debug `/api/tile/{z}/{x}/{y}` that bakes a
single MVT tile server-side via the pure-Go pipeline). The migration expands this.

## Tile serving

Support three backends behind one interface, selected per tile set:

```
type TileSource interface {
    Tile(z uint8, x, y uint32) ([]byte, error) // nil, nil == blank/missing
    Meta() TileMeta                             // bounds, minzoom, maxzoom, gzipped
}
```

- **dynamic** — bake-on-demand from cached ENC cells (generalize `serveTile` /
  `baker.Session.EmitTileInto`), with an LRU/disk tile cache.
- **pmtiles** — read a prebaked `.pmtiles` archive (NEW reader; the package is
  writer-only today). Phase 1.
- **mbtiles** — read a `.mbtiles` SQLite archive (NEW; pure-Go `modernc.org/sqlite`,
  no cgo).

Endpoint: `GET /tiles/{set}/{z}/{x}/{y}.mvt`. The client points MapLibre directly at
it (`{type:"vector", tiles:["/tiles/{set}/{z}/{x}/{y}.mvt"]}`) — HTTP tiles need no
custom protocol, so `pmtiles-source.mjs`'s `pmtiles://` shim is retired for hosted
use. Static-CDN pmtiles (client range reads) can remain as a serverless option.

## Phases

1. ✅ **Tile-source foundation (additive).** Done. `internal/engine/pmtiles`
   gained a random-access `Reader`; new `internal/engine/tilesource` holds the
   `TileSource` interface (`TileMeta` aliased to `pmtiles.TileMeta` so the reader
   satisfies it with no adapter) and all three backends — `pmtiles` (Reader),
   `mbtiles` (`OpenMBTiles`, pure-Go `modernc.org/sqlite`, TMS y-flip, gunzips
   stored tiles), and `dynamic` (`NewDynamic`: builds a Baker from cached cells +
   bounded LRU of baked tiles). The server gained a `tileSets` registry
   (`tilesets.go`), filesystem discovery of `<cache>/tiles/*.{pmtiles,mbtiles}` at
   startup, the lazy `dynamic` set from the ENC_ROOT cache, and the endpoints
   (`tileserve.go`): `GET /tiles/{set}/{z}/{x}/{y}[.mvt]` (raw MVT, gzip on
   Accept-Encoding, 204 blank / 404 unknown set), `GET /tiles/{set}.json`
   (TileJSON), `GET /tiles/` (set list). Backends return decompressed MVT; the
   handler gzips on the wire. Breaks nothing — the wasm baker path is untouched.
2. ✅ **Server-side import/bake.** Done (`import.go`). `POST /api/import?set=NAME`
   takes an uploaded exchange-set zip (raw body or multipart `file`) — or, with no
   body, the cells already in the ENC_ROOT cache (`?cells=` narrows) — and bakes it
   natively with the CLI's baker (`BuildBakerWithUpdates` → `BakeToPMTiles`,
   `?updates=0` to skip .001+, `?overzoom=1`). Baking runs as a background job
   (returns `202 {job,set}`); the client polls `GET /api/import/status?job=ID`
   (`state`/`done`/`total`/`percent`/`cells`/`error`). On success the archive is
   written atomically to `<cache>/tiles/NAME.pmtiles` and registered, so
   `/tiles/NAME/…` serves it immediately; aux files are stashed under
   `<cache>/aux/NAME/` for Phase 4. `/api/proxy` is kept (still used by the wasm
   path until Phase 3). Updates/aux are handled inline (the "trivial here" payoff).
3. **Delete the wasm baker surface.** `cmd/chartplotter-wasm`, `web/wasm-tiles-worker.js`,
   `web/wasm-tiles.mjs`, OPFS bake path (`chart-store.mjs`), in-browser import UI in
   `chartplotter-app.mjs`. ~6 files, ~16 call sites. The big simplification.
4. **TXTDSC/PICREP via API.** `GET /api/feature-file/{name}` off disk, TIFF→PNG on
   the fly. Supersedes the `aux.zip` bundle for server deploys (aux.zip stays correct
   for pure-static-CDN deploys — see [[txtdsc-aux-files]]).
5. **Selective client Go/wasm.** Compile `pkg/s52` + `pkg/s57` to a small wasm for
   the pick-report/portrayal decode, killing JS reimplementation drift. Pure-compute
   only; UI/map/DOM stay JS.

## Reusable Go (server + future client-wasm)

Pure-compute, safe to share: `pkg/{s57,s52,iso8211,geo}`,
`internal/engine/{portrayal,bake,baker,tile,mvt}`. Do NOT share `internal/engine/{server,catalog}`.
