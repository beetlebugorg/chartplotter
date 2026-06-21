# Web frontend architecture

The browser app is being componentized into three layers so each piece is
independently testable and so overlay **plugins** (own-ship GPS, AIS) can be
added without touching the renderer.

```
┌──────────────────────────────────────────────────────────────┐
│ <chart-plotter-app>            the shell — composes everything │
│   • chrome: corner buttons, drawer, bottom data card, scheme   │
│   • wires events between the base widget and the components     │
│                                                                 │
│   ┌───────────────────┐   ┌───────────────┐   ┌──────────────┐ │
│   │  <chart-plotter>  │   │ <pick-report> │   │  plugins:    │ │
│   │  BASE renderer    │   │  (done)       │   │ <own-ship>   │ │
│   │  clean public API │   │               │   │ <ais-overlay>│ │
│   └───────────────────┘   └───────────────┘   └──────────────┘ │
│                                                                 │
│   ┌──────────────────────────────────────────────────────────┐ │
│   │ <chart-downloader>  catalog / download / import / store    │ │
│   └──────────────────────────────────────────────────────────┘ │
└──────────────────────────────────────────────────────────────┘
```

## Layer 1 — `<chart-plotter>` (base renderer)

The stable contract everything else builds on. It renders S-52 ENC tiles
(baked in-browser by the wasm engine) and exposes **only** the surface below.
Nothing outside reaches into private (`_`) fields or MapLibre internals.

| Group | Methods |
|---|---|
| Charts | `setArchive` `addArchive` `addArchives` `loadRegions` `replaceBand` `loadArchiveUrl` `loadStoreCells` `listCharts` |
| Display | `setScheme` `setMariner` `setBasemap` |
| Tiles | `refresh` `flushTiles` |
| Introspection | `realtimeStats` `realtimeCoverage` `cellBounds` |
| Overlays | `get map` · `overlayBeforeId` · `addOverlayLayer(layer,{belowLabels})` · `removeOverlay(ids,source)` |
| Camera | `setCameraMode("free"\|"north-up"\|"course-up")` · `updateFollow({lng,lat,courseDeg})` · `clearFollow` |
| Events | `ready{map}` · `bake-activity{inflight}` · `cell-status{name,status,info}` |

Status: data/renderer boundary is **sealed** — the former reach-ins
(`_rtCache`, `map.style.sourceCaches`) are now behind `flushTiles()`; the
overlay + camera APIs are added. The `map` handle is intentionally public:
overlays legitimately add their own sources/layers through it.

## Layer 2 — feature components & plugins

Each is its own module with its own shadow DOM, themed via the inherited
`--ui-*` CSS custom properties, talking to the shell via events — never reaching
into the renderer.

- **`<pick-report>`** (`pick-report.mjs`, done) — the ECDIS cursor-pick report
  (S-52 PresLib §10.8). The working template for the plugin pattern: shell hands
  it the picked feature stack; it decodes against `s57-catalogue.json`, renders,
  drags, auto-places; emits `pick-feature` (→ map highlight) and `pick-close`.
- **`ChartDownloader`** (`chart-downloader.mjs`, in progress) — the chart
  discovery/acquisition domain. **Increment 1 done:** the NOAA catalogue +
  hosted-archive manifest load and the per-district helpers (`loadCatalog`,
  `districtCellNames`/`districtStat`/`districtZipUrl`/`allEncsUrl`) now live here;
  the shell holds `this._dl` and proxies `_catalog`/`_byName`/`_districts`/
  `_catalogDate` onto it (so its many readers are untouched). **Next increments:**
  download/extract (`_downloadPack`/`_bulkExtractZip`/`_downloadPerCell`/
  `_uninstallPack`), import (`openFiles`/`importSelected`/`rebakeArchive`), the
  NOAA-agreement gate, and server-provision task polling. The OPFS `ChartStore`
  and the `_installed` set stay shell-owned (shared with rendering/coverage); the
  downloader reads `installed` through a getter and will emit a "cells changed"
  signal so the shell re-registers cells with the renderer.

### Plugin contract (own-ship, AIS)

A plugin is a small element/module that:
1. On the renderer's `ready` event, takes the `map` handle.
2. Adds its **own namespaced** source + layers (`ownship-*`, `ais-*`) via
   `plotter.addOverlayLayer(spec)` (on top by default; `{belowLabels:true}` to
   slot beneath chart labels). Hundreds of features → a GeoJSON source updated
   with `source.setData()`; thousands (dense AIS) → a custom WebGL layer.
3. Runs its **own data loop** — own-ship from Geolocation/serial NMEA (~1 Hz),
   AIS from a WebSocket (~secondly) — and pushes updates to its source.
4. For tracking, drives the camera with `plotter.setCameraMode(...)` +
   `plotter.updateFollow({lng,lat,courseDeg})` rather than poking the map
   transform directly.
5. Reacts to scheme changes (day/dusk/night) and calls `removeOverlay` on teardown.

A shared `MapOverlay` base class (`map-overlay.mjs`, planned) will standardize
`attachMap` / namespaced add+remove / `dispose` / scheme handling.

## Layer 3 — `<chart-plotter-app>` (shell)

Owns the chrome (corner round buttons, drawer, bottom data card, search, modals) and composes the
base + components, forwarding events between them. Target: shrink it to
orchestration once the downloader and plugins are extracted.

## Staged migration

1. ✅ Extract `<pick-report>`; establish the plugin pattern.
2. ✅ Seal `<chart-plotter>`'s interface (`flushTiles`, overlay + camera API, this doc).
3. ◑ Extract chart downloading into `ChartDownloader` — increment 1 (catalogue/
   discovery core) done; download/import/agreement/task-polling to follow.
4. ☐ `MapOverlay` base + first `<own-ship>` plugin (proves the contract end-to-end).
5. ☐ `<ais-overlay>`.
6. ☐ Trim the shell to composition + chrome.
