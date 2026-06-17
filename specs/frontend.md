# Frontend rendering (MapLibre GL)

Files: `web/chartplotter.mjs` (the `<chart-plotter>` map component),
`web/chartplotter-app.mjs` (the `<chart-plotter-app>` shell),
`web/chart-store.mjs`, `web/pmtiles-source.mjs`, `web/zip-import.mjs`.

## 1. Architecture

### Tiles into MapLibre — no tile server

Each NOAA band registers a custom MapLibre protocol `chart-<slug>://` at boot
(`chartplotter.mjs:179`), backed by a per-band `MultiArchive` of opened PMTiles
(`_bands[slug]`). `registerPmtilesProtocol` (`pmtiles-source.mjs:260`) strips a
leading cache-bust `{v}` segment, parses trailing `z/x/y`, and returns merged MVT
bytes (or a blank `ArrayBuffer(0)`). Opening an archive reads only the header +
root dir; tiles stream by viewport via HTTP Range or `Blob.slice`, leaf dirs fetch
on demand + FIFO-cache, and missing tiles are remembered in `_misses` to avoid
re-probing.

### Per-band source model

`CHART_BANDS` (`chartplotter.mjs:46`) = 7 sources: `overview/general/coastal/
approach/harbor/berthing` each clamped to `[min,max]`, plus `all` (0–18). MapLibre
overzooms above each source's `maxzoom`. A merged/bandless archive (upload,
`charts-user.pmtiles`) is **fanned** across all 6 real bands (`_fanBands`,
`chartplotter.mjs:525`) so a coarse-only region still overzooms per S-52 overscale
instead of blanking above its global max.

### Layer expansion — draw order (recently fixed)

`expandChartLayers` (`chartplotter.mjs:928`) iterates **template-outer,
band-inner**, so the global draw order is by **S-52 class** (all bands' fills → all
lines → all symbols → all text), with bands kept coarse→fine *within* each class.
This is **correct**: a finer fill still covers a coarser fill (best-available),
while symbols/text always sit above *every* band's fills. The prior band-outer
order buried an overzoomed coarse-band point symbol under a finer band's
depth-area fill — the "disappearing light/beacon" bug (see
[tile-pipeline.md §3](tile-pipeline.md)). The per-band symbol zoom cap was also
removed so symbols overzoom to map max like fills.

### Day/Dusk/Night

`setScheme` (`chartplotter.mjs:459`) re-applies paint/colour across every band
variant via `_variantIds` with **zero re-tiling** — colour is never baked.

### Client-side (no-rebake) symbology

All implemented as data-driven GL expressions/filters over baked attributes, so
mariner changes are instant restyles: SEABED01 depth shading
(`areasFillColor`, `286`), SHALLOW_PATTERN (`322`), safety-contour line
(DEPARE03 approx, `334`), danger dotted boundary (`388`), contour labels
(SAFCON01, `345`), the always-on light-text layer (`910`), soundings incl.
synthesized imperial glyphs (`358`), DANGER01↔02 swap (`378`), and the
display-category / boundary-symbolization filters (`397`).

## 2. Performance

### Layer count is the headline

Templates ≈ **8 base + (one `lc-line-<name>` per linestyle) + 1 `lc-marks` +
4 top + 9 text**. The per-linestyle line layers dominate — on the order of ~55,
so ~77 templates total. `expandChartLayers` multiplies templates × **7 bands** →
**~540 live chart layers** plus ~20 basemap/overlay/inspector layers. MapLibre
walks every layer per frame, and each chart layer's filter is `combineFilters`-
wrapped in `["all", categoryFilter, boundaryFilter, base]`, re-evaluated per
feature (`chartplotter.mjs:431/947`). This is the single biggest frontend cost.

**Important constraint on the fix:** MapLibre's `line-dasharray` is **not a
data-driven property** — it can't be a per-feature expression. That's *why* there
is one `lc-line-*` layer per linestyle (each with a constant dash), and why the
embedded *symbols* were already collapsed into one data-driven `lc-marks`
(`complexLineLayers`, `chartplotter.mjs`). So you cannot collapse the line layers
into a single data-driven layer the way `lines-solid/dashed/dotted` works for the 3
simple dashes. The realistic wins:
1. **Dedupe linestyles by distinct `(dasharray, width)`** — many of the ~55 share
   the same dash geometry; group them and filter by `in` over the member names.
   This can cut the line-layer count several-fold.
2. **Don't fan every template across all 7 bands** where overzoom from a single
   source would suffice (the merged `charts-user` archive is already fanned; the
   per-band split mainly matters when genuinely separate per-band archives exist).

### Good — no churn

- `setStyle` is **never** called after boot. `refresh()` bumps `_ver` and calls
  `src.setTiles(...)` per band (`chartplotter.mjs:491`) — sprites/patterns survive.
- `addArchives` batches multi-archive adds and refreshes **once**
  (`chartplotter.mjs:579`).
- Mariner updates touch only affected layers and avoid re-setting layout props
  unless required (e.g. soundings `icon-image` only on safetyDepth/unit changes,
  `chartplotter.mjs:644`).
- The 750 ms task poll patches pill text / button `disabled` only, deliberately
  avoiding a full `renderCharts()` to prevent flicker
  (`chartplotter-app.mjs:558`). The only `setInterval` is guarded against
  double-start and cleared on every terminal status (`459/464/472`).

### Watch items

- **`_updateHud` writes `innerHTML` on every `map` `move` frame**, unthrottled
  (`chartplotter-app.mjs:230/243`). Throttle via rAF or coalesce to `moveend`.
- **Inspector**: `_inspectAt` runs `queryRenderedFeatures` + a per-feature
  `JSON.stringify` key on every `mousemove` while inspect mode is unlocked
  (`chartplotter-app.mjs:1013`) — gated, acceptable. `_captureArea` runs
  **49 `querySourceFeatures`** (7 sources × 7 layers) over all loaded tiles on
  SHIFT+drag release (`chartplotter-app.mjs:1050`) — one-shot, capped at 80 cards.
- **No `disconnectedCallback`** on either custom element; a permanent global
  `window` keydown handler (`chartplotter-app.mjs:2045`) plus `map`/`shadowRoot`
  listeners close over `this` and aren't torn down. Benign for a singleton, leaks
  if the element is removed/re-added.
- **`_applyArchives` re-opens every archive on each manifest apply** with
  `?t=Date.now()` cache-busting (`chartplotter-app.mjs:337`) — intentional to dodge
  stale 304s.

## 3. S-52 / spec correctness in the GL style

- **SEABED01 depth cascade** (`seabedTokenExpr`, `295`): DRVAL1/DRVAL2 band test,
  deepest-first `case` (first-match = spec last-match-wins), with the
  `fourShadeWater===false` two-shade collapse — **correct**. `safetyContourFilter`
  is a documented *area-level* DEPARE03 approximation (not a true contour trace).
  Defaults `shc 2 / sfc 10 / dpc 20` match common S-52 defaults.
- **Light flare rotation** `icon-rotate: coalesce(rotation_deg,0)` with
  `icon-rotation-alignment:"map"` (`899`) — **correct**, driven by the baked angle.
- **Text/declutter**: general text uses `text-allow-overlap:false` +
  `text-optional:true` (labels drop rather than collide); LIGHTS is excluded from
  collidable layers and given an always-on `light-text` layer so nav data never
  declutters away (`859/910`) — good practice. **Gap:** there is **no
  `symbol-sort-key`** anywhere, so collision drop order is arbitrary (placement
  order) rather than S-52 display-priority-ranked. Add `symbol-sort-key` from a
  baked priority/`scale`.
- **SCAMIN**: not handled in the GL style — visibility is purely the band
  `[min,max]` + overzoom. Since symbols now overzoom to map max, a feature with a
  baked SCAMIN can appear finer than S-52 intends. Confirm the **baker** enforces
  SCAMIN (it stamps z-min from SCAMIN); if not, add a `scamin`-vs-zoom filter.
- **Dusk/night text** deliberately deviates from strict S-52 night dimming —
  bright neutral ink on a dark halo for legibility (`273`). Documented, intentional
  (per user request); a purist ECDIS would object. Acceptable.
- **Symbol sizing** is spec-grounded: `iconSizeForScale = scale/_atlasPpu` (`808`),
  `FEATURE_SCALE = 0.01/0.35278` converts S-52 0.01 mm units to px at 96 dpi.

## 4. Code quality

- `buildLayers`/`expandChartLayers` is intricate but well-commented; the
  per-linestyle line layers are the main perf+maintainability smell.
- Shadow-DOM CSS reach is handled correctly (drag/inspect boxes are inline-styled
  because the map lives in the *inner* component's shadow root).
- Hardcoded relative endpoints (`api/charts`, `api/provision`, OSM URL) — fine for
  the single-server deploy, not configurable.
- Dedupe+rank+`JSON.stringify`-key logic repeats across `_inspectAt`,
  `_captureArea`, `_showInspectArea` — extractable. `_fanBands` loop duplicated
  across `addArchive`/`replaceBand`/`addArchives`.
- `refreshBoxes()` is a no-op stub still called 4×; `focusChart`/`_showChartPill`
  flagged legacy but live — dead-ish code.
- PMTiles reader is a clean v3 implementation: rejects compressed archives, refuses
  a 200 to a Range request, and skips longitude for global-bbox Pacific districts
  (antimeridian fix, `pmtiles-source.mjs:197`).
