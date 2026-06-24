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

The stable contract everything else builds on. It renders S-101 ENC tiles
baked **server-side** (the client imports cells via `POST /api/import` and the
server bakes them into tiles under `/tiles`; the browser only renders pre-baked
tiles). It exposes **only** the surface below. Nothing outside reaches into
private (`_`) fields or MapLibre internals.

| Group | Methods |
|---|---|
| Charts | `setArchive` `addArchive` `addArchives` `loadRegions` `replaceBand` `loadArchiveUrl` |
| Display | `setScheme` `setMariner` `setBasemap` |
| Tiles | `refresh` `flushTiles` |
| Overlays | `get map` · `overlayBeforeId` · `addOverlayLayer(layer,{belowLabels})` · `removeOverlay(ids,source)` |
| Camera | `setCameraMode("free"\|"north-up"\|"course-up")` · `updateFollow({lng,lat,courseDeg})` · `clearFollow` |
| Events | `ready{map}` |

Status: data/renderer boundary is **sealed** — the former reach-ins
(`_rtCache`, `map.style.sourceCaches`) are now behind `flushTiles()`; the
overlay + camera APIs are added. The `map` handle is intentionally public:
overlays legitimately add their own sources/layers through it.

`<chart-canvas>` (the renderer, 1952 → ~1030 lines) is now decomposed into
collaborator modules under `chart-canvas/`, with the public API unchanged
(thin delegators): `s52-style.mjs` (pure palette/filter/symbol-image
expressions), `sprite-builder.mjs` (glyph/symbol ImageData synthesis),
`chart-sources.mjs` (PMTiles/server source + archive + SCAMIN-discovery
state), and `chart-style.mjs` — a **pure** `buildChartLayers(...)` that
RETURNS `{layers, layerBase, variants, layerVis}`; the element assigns the
bookkeeping and the live-updaters read it (so the SCAMIN/variant spine has no
hidden in-place mutation). The element keeps lifecycle, the live restyle/
filter/visibility updaters, the camera/overlay API, basemap, and the
`buildStyle()` orchestrator.

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

**Two-file convention.** Each component is split into LOGIC + CHROME: `<name>.mjs`
(the element/controller — state, deps, lifecycle, events, render orchestration)
+ `<name>.view.mjs` (`export const STYLE` + pure, stateless markup builders —
no `this`, no DOM, no deps; `(args) → HTML string`). `chart-library`,
`settings-dialog`, and `dev-tools` all follow this; `chart-library` is the
reference.

## The settings panel: a contribution host (implemented)

The settings panel is `<settings-dialog>` (`web/src/components/settings-dialog.mjs`),
a HOST that owns no settings — it renders whatever is registered in a
`SettingsRegistry`. So the app's display settings AND every plugin's settings
share one panel with one look, and a plugin gets space in the global panel
without the dialog knowing it exists.

- **`SettingsRegistry`** (`web/src/app/settings-registry.mjs`): `register(contribution)`
  / `unregister` / `tabs()` / `forTab()` / `onChange()`. A contribution is a
  declarative descriptor `{ id, tab:{id,label}|"<id>", group?, order, items|()=>items,
  get(k,def), set(k,v), render?(host,ctx) }`. `items` (array or function) are
  rendered with the shared control library; `render(host)` is an escape hatch for
  custom UI (the dev tools use it).
- **Control types** (in `settings-dialog.view.mjs`): `toggle`, `segmented`,
  `multi` (independent bools + locked items, e.g. Detail level), `number`
  (+ unit + display `transform:{toView,fromView}`), `select`. `transform` lets a
  control DISPLAY one value while STORING another (Point symbols paper↔bool;
  depths feet↔metres).
- **`SettingsStore`** (`web/src/app/settings-store.mjs`): the one persistence
  layer — a single blob mirrored to localStorage + POSTed to `/api/settings`
  (debounced), addressed by namespace. `ns("core")` = top-level keys
  (backward-compatible with the old flat blob); plugins nest under their id. (The
  CORE display settings currently still persist through the shell's existing
  apply methods; `SettingsStore` is wired and ready for plugin namespaces.)
- **Core settings** (`web/src/app/core-settings.mjs`): `coreSettingsContributions(app)`
  registers the app's own display settings (General/Text/Units/Depths/Advanced)
  as five contributions whose `get/set` wrap the shell's existing
  `applyScheme`/`applyBasemap`/`applyMariner`/app-toggle methods — so persistence
  + apply are unchanged; only the UI is now a contribution.

**Dev tools as the first plugin-style contributor.** `DevTools`
(`web/src/components/dev-tools.mjs`) is a plain controller (like the map
controllers) that self-registers a contribution on the Advanced tab via the
`render(host)` escape hatch. It owns only two tools — "Rebuild all charts"
(rebake) and the Feature inspector (S-52 attribute inspection + its map
listeners). The old 7-section dev panel (share/coverage/bands/cell-footprints/
tile-debugger/refresh) was pitched. This validates the contribution model:
adding a settings section needs **no edits to `<settings-dialog>`**.

## Staged migration

1. ✅ Extract `<pick-report>`; establish the plugin pattern.
2. ✅ Seal `<chart-plotter>`'s interface (`flushTiles`, overlay + camera API, this doc).
3. ✅ Extract chart downloading: `ChartDownloader` (discovery) + `ChartService`
   (server jobs/registry) + `NotificationCenter` (progress bus) + the
   `<chart-library>` element (browse/download/import/agreement). Map picker removed.
4. ✅ Settings as a contribution host: `SettingsRegistry` + `SettingsStore` +
   `<settings-dialog>` + `core-settings` contributions; `<dev-tools>` slimmed to a
   contributor. Shell 4729 → 3020 lines.
5. ☐ `MapOverlay` base + first `<own-ship>` plugin (proves the camera/overlay +
   settings-contribution + notify contracts end-to-end).
6. ☐ `<ais-overlay>`.
7. ☐ Trim the shell to composition + chrome.
