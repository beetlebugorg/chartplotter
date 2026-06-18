# Spec: MapLibre tile-debugger plugin

A small, debug-only MapLibre plugin for diagnosing **vector tiles that are
delivered but don't render** — the failure mode that cost us a long debugging
session on the realtime `cp://` baker.

## Motivation (the bug it would have caught instantly)

A General cell rendered a permanent blank tile (`7/35/53`, offshore Cape
Canaveral, z7). We eventually proved, by hand, via `map.style.sourceCaches`:

```
tile 7/35/53: state="loaded", hasData=true, rawBytes=null, buckets=[]
```

i.e. **MapLibre marked the tile loaded but parsed it into zero buckets** — it was
holding the *empty* tile from a request that fired before the baker session
existed, while the later full re-bake (121 842 bytes, logged as delivered) was
applied to a superseded request and dropped.

The hard part was that every isolated layer looked fine: the Go baker baked the
tile correctly, the worker reported `empty=false`, the `cp://` protocol logged
`121842 bytes delivered`, MapLibre fired no `error`. Only by cross-referencing
**what the protocol delivered** against **what MapLibre actually retained per
tile** did the discrepancy show up. The plugin's job is to make that
cross-reference a glance, live, for every tile — instead of one-off console
probes.

## Goal

Surface, per visible tile of a chosen source, the full lifecycle in one view:
**requested → delivered bytes → parsed buckets/features → tile state → rendered**,
and flag any mismatch (delivered-but-empty, loaded-but-no-buckets, stuck across a
version bump).

## MVP features

1. **Tile status overlay.** Like `map.showTileBoundaries`, but each tile box is
   labelled `z/x/y` + a status badge and colour:
   - green = loaded with ≥1 bucket (rendering)
   - red = `loaded` but `buckets:[]` / `rawBytes` falsy ← the bug signature
   - amber = loading; grey = errored/aborted.
2. **Delivery vs parse mismatch list.** A panel listing tiles where
   `deliveredBytes > 0 && bucketCount === 0`, or state flips empty→loaded→empty
   across version bumps. This is the single most valuable readout.
3. **Click a tile → detail.** Show: `tileID` (canonical z/x/y, `overscaledZ`,
   `wrap`, key), `state`, `latestRawTileData.byteLength`, per-**source-layer**
   feature counts (from `tile.latestFeatureIndex` / `querySourceFeatures` scoped
   to the tile), per-**style-layer** bucket presence, and the **protocol-delivered
   byte count** for that `z/x/y` (see hook below).
4. **Lifecycle log.** Timestamped `dataloading` / `data` / `error` events for the
   source, per tileID — so "empty load, then full load discarded" is visible as a
   sequence.

## How it hooks MapLibre

Read-only where possible; one optional wrap to capture delivery.

- **Tile state:** `map.style.sourceCaches[sourceId]._tiles` → each `Tile` has
  `.tileID.canonical {z,x,y}`, `.tileID.overscaledZ`, `.state`, `.hasData()`,
  `.buckets` (object keyed by style-layer id), `.latestRawTileData`,
  `.latestFeatureIndex.rawTileData`. (Internal fields — pin a MapLibre version.)
- **Events:** `map.on("dataloading"|"data"|"error", e => …)` filtered to
  `e.sourceId === sourceId`, reading `e.tile.tileID` / `e.tile.state`.
- **Delivery bytes (the key correlation):** wrap the tile provider. For an
  `addProtocol` source like `cp://`, wrap the protocol callback to record
  `delivered[z/x/y] = result.data.byteLength` before returning. For HTTP sources,
  a `transformRequest` / fetch tap. This is what let us see "delivered 121 842 but
  tile kept 0".
- **Rendered check (optional):** `map.queryRenderedFeatures(tileCenterPx, {layers})`
  vs `querySourceFeatures` — rendered-empty-but-source-nonempty = a style/paint
  issue rather than a delivery one.

## UI

A MapLibre `IControl` (so `map.addControl(new TileDebugger({source:"chart"}))`)
with: a toggle for the overlay, the mismatch list, and a detail panel on tile
click. No build step — a single ES module, importable like the other `web/*.mjs`.

```js
map.addControl(new TileDebugger({ source: "chart", layers: ["areas","soundings","lines"] }));
```

## Integration with this app

- Lives in `web/` as `tile-debugger.mjs`; wired behind the existing **Dev panel**
  (`chartplotter-app.mjs`) next to "MapLibre tile boundaries" / "Log tile baking".
- Complements, not replaces, the current debug hooks: `cpSetTileDiag` (worker
  bake counts, forwarded to the page console), the `[cp deliver]` log, and the
  `[maplibre] tile … state=…` events. The plugin's delivery-wrap can subsume the
  `[cp deliver]` log.
- Reuses the `cp://` namespace/version (`_ver`) so it can show which **tile
  version** a stuck tile is pinned to.

## Non-goals

- Not shipped to end users; a dev/diagnostic surface only.
- Not a feature inspector (we have one); this is about **tile lifecycle &
  delivery integrity**, not S-57 attributes.
- Not MapLibre-version-agnostic: it reads private `sourceCache` internals, so it
  targets the vendored `web/vendor/maplibre-gl.js` and is pinned to it.

## Definition of done

Loading the known-bad `#share` (single cell `US2EC02M`, z7) with the plugin on:
the `7/35/53` box is **red**, the mismatch list shows `7/35/53: delivered=121842
bytes, buckets=0`, and the detail panel shows `state=loaded, rawBytes=null` — i.e.
the plugin points straight at the stuck empty tile with zero manual probing.
