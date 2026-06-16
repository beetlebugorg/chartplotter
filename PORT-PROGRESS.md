# chartplotter-go — port progress & continuation plan

A Go port of the Zig `chartplotter` marine chart engine. This file is the
**handoff/continuation doc**: read it first, then continue from "Remaining work".

- **Reference (source of truth):** `../chartplotter` (Zig). When the reused Go
  code diverges from the Zig, **port the Zig** — it is authoritative.
- **Reuse source:** `../chartplotter-original` (older Go/Wails impl) — `pkg/s52`,
  `pkg/s57`, `pkg/iso8211`, `internal/s57/parser` were copied/modernized from here.
- **Module:** `github.com/beetlebugorg/chartplotter-go`, Go 1.26.
- **Build/test:** `make build` → `bin/chartplotter`; `make test`; `go vet ./...`;
  `gofmt -l .` must be clean. CI mirrors these (`.github/workflows/ci.yml`).
- **Golden test cell:** `testdata/US4MD81M.000` (+ `.001/.002/.003` updates).
  Embedded PresLib DAI: `pkg/s52/preslib/PresLib_e4.0.0.dai`.
- `AGENTS.md` rule (inherited): S-52/S-57/ISO-8211 are spec-driven — read the
  spec / the Zig before changing rendering behaviour; don't invent.

## Deliberate architecture decisions (already settled with the user)

1. **All tile generation is a Go backend task** (CLI/server), NOT in-browser
   WASM. The browser only renders pre-baked tiles. (TinyGo WASM is reserved for
   the optional in-browser file-import path, Phase 9.)
2. **Go emits tiles exactly like the Zig version**: Primitive geometry + S-52
   colour **tokens** (never RGB); MapLibre renders them; Day/Dusk/Night is a
   client restyle from `colortables.json`.
3. **Bake once, never re-bake**: mariner settings (depth shading, soundings,
   contours, danger swap) are applied client-side from baked attributes.
4. **Single-archive "spec_display" bake** (the provisioned model): per-feature
   display z-min from SCAMIN, down-fill to z0 bounded by best-available
   suppression. (NOT the per-band district path.)
5. Frontend (`web/`) is **copied** from the Zig project; the in-browser-bake
   modules were dropped. It already expects `/api/provision` + `/api/tasks`
   (see `../chartplotter/chartplotter/CHARTS-UI-SPEC.md`).

## Layout

```
pkg/geo            LatLon/Point/BoundingBox            (port of geo.zig)
pkg/iso8211        ISO 8211 decoder                    (reused, verbatim)
pkg/s57            S-57 cell model + spatial query     (reused)
internal/s57/parser  ISO8211->features/geom/topology/updates (reused; embeds s57attributes.csv)
pkg/s52            PresLib: DAI parse, lookup, CSP, colours (reused 15K + my edits)
internal/engine/
  portrayal/       S-52 expand -> lat/lon Primitive IR (port of portrayal/*.zig)
  tile/            web-mercator project + clip         (port of tile.zig)
  mvt/             MVT 2.1 encoder + pbf               (port of mvt.zig/pbf.zig)
  pmtiles/         PMTiles v3 writer                   (port of pmtiles.zig)
  bake/            Baker: cells -> tiles -> archive    (port of bake.zig + sectorlights.zig)
  assets/          colortables.json (done) + linestyles/sprites (TODO)
cmd/chartplotter/  kong CLI (version, emit-assets; rest TODO)
web/               copied MapLibre frontend (renders pre-baked tiles)
testdata/          golden ENC cell + updates
```

## What's DONE (Phases 0–5 complete; commits are phase-tagged)

- **geo, iso8211, s57, s52** — reused/ported, tests pass. Golden cell → 7334
  features.
- **s52 colour conversion aligned to Zig** (`pkg/s52/colors.go`): dropped the
  Illuminant-C→D65 Bradford adaptation, clamp linear before gamma, round-half-up.
  All 67 tokens × day/dusk/night match the Zig `colortables.json` exactly.
- **`pkg/s52` LookupFeatureRaw** (`pkg/s52/lookup.go`): returns matched LUPT
  instructions WITHOUT expanding CS, so the portrayal walk drives CS dispatch +
  per-point soundings itself (Zig-faithful). `selectInstruction` + `makeInstructionSet`
  factored out; existing lookup behaviour preserved.
- **`pkg/s52` TE labels** (`types.go`/`instructions.go`): `TextInstruction` now
  carries `Format` + `FormatAttrs`; `parseTE` no longer drops the printf format.
- **Phase 4 portrayal** (`internal/engine/portrayal/`):
  - `primitive.go` — the Primitive IR (interface union: FillPolygon, StrokeLine,
    SymbolCall, PatternFill, LinePattern, DrawText, SectorLight) + Dash/HAlign/VAlign
    + `DefaultPxPerSymbolUnit = 0.01/0.35278`.
  - `build.go` — `BuildFeature` = lookup(raw) + recursive CS walk + instruction
    emit, mirroring `emitInstructions`. Soundings re-run per point with injected
    `DEPTH`. `formatSubstitute` (TE printf, S-52 §8.3.3.3). `applyDangerDepth`:
    OBSTRN/WRECKS with VALSOU → DANGER01 + danger_depth + deep DANGER02
    (reproduces obstrn06/wrecks05 net output; Go CSPs baked ISODGR01 instead).
    `mapVJust` matches dai.zig (2 centre, 3 top, else bottom).
  - Golden cell: 7341 feats → 7221 portrayed, full primitive mix.
- **Phase 5 tile engine:**
  - `tile/tile.go` — projection (matches viewport.zig mercator-Y), `RangeForBbox`,
    Sutherland–Hodgman `Clipper`, Liang–Barsky `ClipLine`/`ClipLinePhased`. 9 tests.
  - `mvt/` — `pbf.go` (varint/packed/fixed32) + `mvt.go` (TileBuilder/LayerBuilder,
    geometry command stream, string-value interning). Layers + attribute keys
    match the frontend schema exactly (see below). Round-trip test.
  - `pmtiles/pmtiles.go` — v3 header, Hilbert `ZxyToTileID`, content dedup,
    leaf-page sharding, deterministic tile-id-ordered output. `WriteArchive`/`Finish`.
    3 ported tests.
  - `bake/bake.go` — `Baker`: `AddCell` (band from CompilationScale, SCAMIN z-min
    via `specZMin`/`scaminZoom`, native bands), `EmitTile` (norm-world bbox reject
    → project/clip/quantize → route to layer; **best-available suppression**
    `anyCoarserOverlaps`), `BakePMTiles`, sounding **grouping** (consecutive
    same-anchor digits → one `soundings` feature w/ joined names + sym_s/sym_g),
    **sector tessellation** (`expandSector` per zoom → lines layer: dashed legs +
    OUTLW-backed coloured arc/ring; port of sectorlights.zig). MVT extent 4096,
    buffer 64. End-to-end: Annapolis → valid PMTiles v3, 357 tiles, all 7 layers.
- **Phase 6 (partial):** `internal/engine/assets/colortables.go` generates
  `colortables.json` (verified == Zig). `cmd/chartplotter emit-assets DIR` wired
  (`assets/emit.go`). `chartplotter version` works.

### MVT tile schema (THE frontend contract — do not change keys)

Layers + per-feature attributes (colour is always a string token):
- `areas` (polygon): `color_token, class, draw_prio, cat, bnd`
- `area_patterns` (polygon): `pattern_name, class, draw_prio, cat, bnd`
- `lines` (line): `class, color_token, width_px(int), dash, cat, bnd` (+ sector
  legs/arcs route here)
- `complex_lines` (line): `class, linestyle_name, cat, bnd`
- `point_symbols` (point): `class, symbol_name, rotation_deg, scale, offset_x,
  offset_y, halo_color_token, halo_width, draw_prio, cat, bnd` (+ `danger_depth,
  sym_deep` when sounded)
- `soundings` (point): `class, symbol_names, scale, draw_prio, cat, bnd` (+
  `depth, sym_s, sym_g` when depth known)
- `text` (point): `class, text, font_size_px, color_token, halign, valign,
  offset_x, offset_y, halo_color_token, halo_width, draw_prio, cat, bnd`

PMTiles metadata `vector_layers` lists exactly these 7 ids.

## REMAINING WORK

### Phase 6 finish — asset generation
Zig refs: `../chartplotter/chartplotter/src/{linestyles.zig, sprites.zig, png.zig}`
+ `portrayal/symbol_render.zig` (HP-GL interpreter). Reference outputs to diff
against: `web/{linestyles.json, sprite.json, sprite.png, patterns.json, patterns.png}`.

1. **`linestyles.json`** → `internal/engine/assets/linestyles.go`.
   Format: `{NAME: {period_px, dash:[on,off,...], color_token, width_px,
   symbols:[{o,n,r}]}}`. Walk each PresLib linestyle's HP-GL ops over one period:
   `period_px = BBoxWidth * DefaultPxPerSymbolUnit`; collect PD "on" runs (px from
   bbox left), SC symbol placements (offset px, name, rotation tenths×0.1), first
   PD pen colour token, stroke width. Dash starts with an "on" run (leading 0 if
   it opens with a gap), even length. Reuse: `pkg/s52` `Linestyle.VectorCommands`
   (already parsed) or `parseHPGLtoPrimitives` (`pkg/s52/symbols.go`). The pen
   token comes from the SP pen index → `Linestyle.Colors`/LCRF mapping — verify
   against `linestyles.zig analyze()`.

2. **Sprite atlas** `sprite.png` + `sprite.json` → `internal/engine/assets/sprites.go`.
   Format: `{"_meta":{px_per_unit:0.08,width,height}, NAME:{x,y,w,h,pivot_x,pivot_y}}`.
   Steps: for every PresLib symbol, get its `Primitives` (`Library.GetSymbol` →
   `parseHPGLtoPrimitives`), rasterize to an RGBA image at `px_per_unit = 0.08`
   (with supersampling for AA), compute tight bbox + pivot (the symbol's pivot in
   px), shelf-pack into a 512-wide atlas, PNG-encode (Go `image/png`). Symbol pen
   colours ARE baked into the Day atlas (the client tints via the style); use the
   day colour table. Needs a small image render surface (AA line/polygon/circle)
   — chartplotter-original has only SVG/PDF surfaces, so write a software one
   (consider `golang.org/x/image/vector` for AA fills + a stroke helper). Mirror
   `sprites.zig` for sizing/pivot/packing; diff JSON keys+pivots vs `web/sprite.json`.

3. **Pattern atlas** `patterns.png` + `patterns.json` → same module/approach,
   `Library.GetPattern`. Patterns also carry `spacing_x/spacing_y/staggered`
   (used by the area-pattern fill) — check `sprites.zig writePatterns`.

4. Add each to `assets.Emit` (`internal/engine/assets/emit.go`) so `emit-assets`
   writes all six files.

### Phase 7 — CLI + server
Zig ref: `../chartplotter/chartplotter/src/main.zig`. API spec:
`../chartplotter/chartplotter/CHARTS-UI-SPEC.md` §3. Add kong subcommands in
`cmd/chartplotter/main.go`:

1. **`emit-pmtiles OUT.pmtiles CELL.000...`** — easiest; parse cells
   (`s57.Parse`), `bake.New()`, `AddCell` each, `BakePMTiles(4096, 64)`,
   `WriteArchive` to the file. Deliver this FIRST (lets you eyeball real archives).
2. **`bake-zip OUT.pmtiles IN.zip`** — extract `.000` from a NOAA zip
   (`archive/zip`), bake all into one archive.
3. **`catalog-json IN.xml OUT.json`** — port `chartcat.zig`: NOAA `ENCProdCat.xml`
   → compact `catalog.json` (`{cells:[{n,l,s,e,u,d,z,zs,bb,cg,rg}]}`). Reference
   output already shipped: `web/catalog.json`. Source XML: copy
   `../chartplotter/chartplotter/web/ENCProdCat.xml` if needed.
4. **`emit-basemap[-pmtiles] IN.b OUT`** — port `basemap.zig` (GSHHG shoreline →
   GeoJSON / coastline PMTiles). Lower priority.
5. **`bake-districts` / `fetch-districts`** — per-NOAA-region archives +
   `charts-index.json` manifest. The per-band district path; lower priority for
   the single-archive product.
6. **`provision DIR CELL...`** — download named cells from NOAA (server-side, no
   CORS), bake into `charts-user.pmtiles` + write `charts-user.json` sidecar
   (`{cells:[...], bounds:[W,S,E,N]}`). Cache downloads at `<dir>/.cellcache-<CELL>.000`.
7. **`serve [--host 127.0.0.1] [--port 8080] [--assets DIR]`** — static file host
   with Range/206 support, plus the API in CHARTS-UI-SPEC §3:
   - `POST /api/provision {cells:[...]}` → start/no-op a SINGLE background bake job
     (download missing cells → bake → write `charts-user.{pmtiles,json}`), return
     `{ok:true, task:<id>}` immediately (+ `busy:true` if one runs).
   - `GET /api/tasks` → `{task:<id>, kind:"provision", status, phase, done, total,
     cell, cells, error}` or `{task:null}`. Client polls ~750 ms.
   - `DELETE /api/charts` → delete `charts-user.{pmtiles,json}`.
   - Loopback Host-check unless bound remote.
   Use stdlib `net/http`; one goroutine for the job + a mutex-guarded task struct.
   Port the CLI progress dashboard (`progress.zig`) with lipgloss only if wanted.

### Phase 8 — frontend wiring + end-to-end
- Point `web/` at the Go server; ensure `chartplotter.mjs`/`chartplotter-app.mjs`
  load the PMTiles via the pmtiles:// protocol from `charts-user.pmtiles` and the
  assets (`sprite.*`, `colortables.json`, `linestyles.json`, `patterns.*`,
  `glyphs/`, `catalog.json`) the Go `serve` hosts.
- The in-browser wasm-bake modules were already dropped on copy; confirm no dead
  imports remain (`chart-engine.mjs`, `engine-client.mjs`, `chart-worker.mjs` are
  NOT in `web/`).
- Verify end-to-end: `chartplotter serve` → boot → region list → Download region →
  server bakes → pill progress via `/api/tasks` → manifest reload → chart renders
  with working Day/Dusk/Night restyle. Cross-check vs the Zig demo on Annapolis +
  a VA region. Use the `verify`/`run` skills or drive a headless browser.

### Phase 9 — optional/later
TinyGo WASM in-browser import-bake; GSHHG basemap polish; Docusaurus docs;
`dist` cross-compile; hosted per-band district packs.

## Cross-impl validation (do once Phase 7 `emit-pmtiles` exists)
Tile-equivalence harness vs the Zig (methodology in
`../chartplotter/specs/bake-pipeline-v2.md` §7): build Zig
`chartplotter --emit-pmtiles` and Go `chartplotter emit-pmtiles` on the same
cell; decode both archives; match features by (layer, attribute set); assert
geometry within ≤1 MVT unit at extent. This will surface any remaining portrayal
divergences (the reused Go CSPs vs the Zig). Also visually diff Annapolis.

## Reconciliation gotchas (already handled — don't regress)
- Colour: NO Bradford adaptation; round-half-up. (`pkg/s52/colors.go`)
- Soundings: grouped in `AddCell`; per-point DEPTH injected in `BuildFeature`.
- Danger: OBSTRN/WRECKS+VALSOU forced to DANGER01/DANGER02 in `applyDangerDepth`.
- Sectors: bearings reversed +180 (from seaward); screen-px sized; emitted per
  zoom into `lines` (NOT a separate layer the frontend doesn't read).
- specZMin floats non-SCAMIN/DISPLAYBASE features to z0 — bounded by
  best-available suppression, so single-cell bakes show harbour detail at low
  zoom (correct; a coarser overlapping cell would suppress it).
