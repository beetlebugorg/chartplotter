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

## The Charts-library boundary (implemented)

The "install + manage charts" domain is a self-contained custom element,
`<chart-library>` (`web/src/components/chart-library.mjs`), mounted by the shell
INSIDE the drawer's `#charts-body` (the charts section; the shell keeps the
drawer/tab chrome). It owns the 3-pane browse UI, search, the detail-pane mini
preview map, the agreement gate, the download QUEUE + EXECUTION, and the
User-Charts local-file import. It is themed via inherited `--ui-*` custom
properties (same pattern as `<pick-report>`).

Decisions (locked):
- **`<chart-canvas>` takes baked tilesets, not cells/parsed charts.** Its sole
  render-reconcile entry is `setServerSets(activeBandNames)`; it knows nothing
  about packs/providers/downloads. The shell's `_renderInstalledSets()` is the
  reconcile loop (GET `/api/packs` → flatten enabled per-band → `setServerSets`).
- **The server (`/api/packs`) is the installed-set source of truth.** No shared
  in-memory installed-set state between library and shell.
- **Download execution lives in `<chart-library>`** (queue + server fetch/bake),
  via injected deps; the shell no longer owns `_runDownloadPack` etc.
- **The main-map cell picker was deleted** (the mini preview map is the only
  chart-picking surface now). `_chartsMode` and its readers went with it.

Contracts:
- Injected via `configure({ dl, api, notify, store, assets, prod })`:
  `dl` = `ChartDownloader` (catalogue discovery), `api` = `ChartService`
  (`web/src/data/chart-service.mjs` — import/bake jobs + pack registry + set
  management + the SSE job-progress protocol), `notify` = `NotificationCenter`
  (`web/src/app/notification-center.mjs` — `task()`/`info|warn|error`),
  `store` = `ChartStore` (OPFS local import).
- Public methods the shell calls: `show(provider)`, `refresh()`, `get busy`,
  `teardownPreview()`, plus reach-ins for the agreement modal
  (`_showAgreement`/`_resolveAgreement`/`agreementOpen`).
- Events it dispatches (bubbles + composed): **`charts-changed`** (→ shell runs
  `_renderInstalledSets()` to reconcile the canvas), **`chart-focus {bounds}`**
  (→ shell flies the canvas), **`chart-import-archive {file}`** (→ shell does the
  plotter-coupled `.pmtiles` client-side add).
- All job progress is posted to `notify` (task handles); the library never
  touches shell DOM. `ChartService` + `NotificationCenter` are also used by the
  shell itself, so there is ONE job-poll/progress implementation, reusable by
  future plugins (`<own-ship>`/`<ais-overlay>`).

Result: the shell dropped ~850 lines (4688 → 3834); the domain is ~1026 lines in
its own element. The shell now keeps the canvas reconcile, the drawer/tab chrome,
the notification chrome, and the data-component instances.

## Staged migration

1. ✅ Extract `<pick-report>`; establish the plugin pattern.
2. ✅ Seal `<chart-plotter>`'s interface (`flushTiles`, overlay + camera API, this doc).
3. ✅ Extract chart downloading: `ChartDownloader` (discovery) + `ChartService`
   (server jobs/registry) + `NotificationCenter` (progress bus) + the
   `<chart-library>` element (browse/download/import/agreement). Map picker removed.
4. ☐ `MapOverlay` base + first `<own-ship>` plugin (proves the contract end-to-end).
5. ☐ `<ais-overlay>`.
6. ☐ Trim the shell to composition + chrome.
