# S-57 → S-101 portrayal migration — status

**Branch:** `worktree-s101-dai-conversion` (worktree at `.claude/worktrees/s101-dai-conversion`). Main has been merged in (commit `920ef40`). ~31 commits this effort.

**Goal:** retire the embedded S-52 DAI presentation library and portray S-57 ENC data using the IHO **S-101 Portrayal Catalogue** natively — its SVG symbols, XML line styles / area fills, colour profile, and **Lua rules** — driven by an S-57→S-101 bridge. Where S-101 lacks coverage, render an obvious placeholder (`QUESMRK1`) or author our own. (Earlier plan: `specs/s101-portrayal-backport.md`; coverage matrix: `specs/s101-coverage-matrix.md`. This file supersedes them as the live status.)

## How to run it

Vendored catalogues (siblings of the repo, NOT committed — licence unconfirmed for the IHO DRAFT content):
- Portrayal catalogue: `/home/jcollins/Projects/s101-portrayal-catalogue/PortrayalCatalog` (Symbols/*.svg + *SvgStyle.css, LineStyles, AreaFills, ColorProfiles/colorProfile.xml, Rules/*.lua)
- Feature catalogue: `/home/jcollins/Projects/s101-feature-catalogue/S-101FC/FeatureCatalogue.xml`

```
# bake one cell both ways (S-52 default vs S-101) from the same binary:
go build -o /tmp/cp ./cmd/chartplotter
/tmp/cp bake -o s52.pmtiles testdata/US4MD81M.000                 # S-52
/tmp/cp bake --s101 $PC --s101-fc $FC -o s101.pmtiles testdata/US4MD81M.000   # S-101

# emit the S-101 client assets (colortables/linestyles/sprite/patterns):
/tmp/cp emit-assets --s101 $PC <outdir>

# serve so the chart library bakes S-101 server-side (the live demo):
/tmp/cp serve --s101 $PC --s101-fc $FC --host 0.0.0.0 --port 8080 \
  --cache /tmp/cp-s101-cache --data /tmp/cp-s101-data
```
**Always reuse the same `--cache`/`--data` dirs** or imported charts vanish on restart (`--data` = source ENC, `--cache` = baked tiles scanned on startup). The `--s101` flags are transitional until the catalogue is embedded.

Diagnostics: `go test ./internal/engine/baker -run TestS101Diag -v` prints per-cell portrayal success/error tally + grouped error messages (the main triage tool).

## Architecture / data flow

```
S-57 cell ─> bake.Baker.AddCell ─(BatchPortrayer.Begin: one engine pass per cell)─>
  internal/engine/s101.Engine  (gopher-lua + real S-101 framework: S100Scripting,
     PortrayalModel, PortrayalAPI, Default, main + a Lua "spatial glue" we inject)
   • S-57→S-101 bridge: pkg/s100/fc parses FeatureCatalogue.xml; <alias> gives the
     S-57 6-char acronym ⇒ FeatureCodeForS57/AttrCodeForS57 (no conversion table).
   • host (s101/host.go): HostGet*TypeCodes/Info from fc; per-feature data from the
     adapted feature; featureName complex attr from OBJNAM; spatial associations
     built in Lua glue (one per feature; surfaces resolve to one exterior-ring curve).
   • emits a flat instruction stream per feature.
   ─> pkg/s100/instructions: ParseStream + Reduce ⇒ []DrawCommand (state folded in)
   ─> internal/engine/portrayal.LowerS101(cmd, geometry, catalog) ⇒ engine Primitive
      (FillPolygon/StrokeLine/LinePattern/PatternFill/SymbolCall/DrawText)
   ─> existing baker projection/MVT/PMTiles  ─> existing MapLibre client
assets: pkg/s100/symbols (SVG flatten+rasterize) + assets/sprites_s101 (sprite atlas),
        assets/patterns_s101 (area-fill pattern atlas), assets/emit_s101 (colortables
        from colorProfile, linestyles from LineStyles). All atlases packed ≤4096 (Chrome).
```
Seam: `bake.Baker.portrayer` (internal field, `SetPortrayer`; `BatchPortrayer.Begin/End` per cell). `baker.UseS101Catalog(dir, fcPath)` sets it for all server bakes. S-52 path is untouched (nil portrayer) and still the default.

## What works (committed, tested)

- Full pipeline end-to-end: a real NOAA cell bakes through S-101 to tiles; the server bakes S-101 on chart-library import; renders in Chrome **and** Firefox.
- **~94% of features portray** (6925/7334 on US4MD81M); de-risk + each layer unit-tested.
- Symbols: correct size/placement; rotate about their pivot (icon canvas centred on pivot). Sprite atlas 2048-wide (under Chrome's 4096 `MAX_TEXTURE_SIZE`).
- Areas + lines: depth areas, coastline, depth contours render (the spatial-topology glue unblocked the line/area rules).
- Area patterns: shallow-water cross-hatch, dredged, vegetation, quality overlays (pattern atlas).
- Colours byte-identical to S-52; line styles incl. composites.
- Text labels (`TextInstruction`→`DrawText`) + feature **names** (OBJNAM→`featureName`) + light characteristics, via `PortrayFeatureName`/`ProcessNauticalInformation`.
- Performance: batch-per-cell with a fresh Lua state per cell (~1s/cell, bounded memory; was tens of GB / "forever" when per-feature).
- Errored features are **suppressed** (not QUESMRK1-flooded); only genuinely-unmapped object classes get the `QUESMRK1` placeholder.

## Known issues / in progress (prioritised)

1. **Display category is wrong → quality overlay always shows + everything mis-categorised.** `portrayal/s101build.go` sets `DisplayCategory: s52DisplayStandard` where `const s52DisplayStandard = 1`, but `bake.catRank` switches on the real `s52.DisplayBase=6 / DisplayStandard=7 / DisplayOther=8` (default→Other). So **every S-101 feature bakes as cat=Other(2)**, and the data-quality overlay (M_QUAL, viewing group **90010**) isn't separated → it renders unconditionally and "always shows."
   **Fix (do NOT hardcode a threshold — use the real groups):** derive the display category from the S-101 **viewing group** the rule emits (captured per `DrawCommand.ViewingGroup`), using the authoritative S-101 viewing-group→display-category mapping (find it in the S-101 product spec / portrayal catalogue; S-52 PresLib §ViewingGroups is the analogue). Bake the viewing group onto the feature too, and use the real `s52.Display*` values (6/7/8) so `catRank` works. Quality/CATZOC (9xxxx) and other optional groups → hidden by default.
2. **Area patterns are "cut off."** `assets/patterns_s101.go seamlessTile` clips the wrapped symbol at the v1×v2 tile edges for some fills — the tile must fully contain the stamped symbol + wrap neighbours (revisit the cell size / neighbour offsets; consider centring vs lattice-origin placement, and honoring the v2.x stagger).
3. **~387 features still error (~5%)** — see TestS101Diag:
   - `OBSTRN07`/`WRECKS05` hard-require a depth (`valueOfSounding`←VALSOU `or` `defaultClearanceDepth`); S-57 features without VALSOU error (179). S-57→S-101 semantic gap — decide a default/sentinel or treat no-depth as danger.
   - WRECKS **true recursion** → stack overflow (101; bigger gopher-lua stack did NOT help, so it's a real cycle in the WRECKS rule chain — trace it).
   - `SpanOpening`/bridges index a nil sub-attribute (51); `inTheWater` invalid-attribute (3); invalid primitive type (53).
4. **`rot_north`** — all S-101 symbols baked screen-up (viewport-aligned). True-north marks (ORIENT-driven: recommended tracks, etc.) should rotate with the chart — wire the `RotationTrueNorth`→`rot_north` distinction (S-52 PresLib §9.2 ROT case 3).
5. **Single-pass only** — no plain/symbolized boundary or simplified/paper-symbol variants (S-101 handles these via context params; the bake emits one pass tagged bnd/pts=common).

## Remaining work / next steps

- (1) display-category-from-viewing-group, then (2) pattern cut-off — both directly visible.
- (3) the errored features (obstruction/wreck depth + WRECKS recursion are navigationally important).
- (4) `rot_north`; (5) multi-pass.
- **Embed** the catalogue under `go:embed` (loaders are already `fs.FS`-ready: `catalog.LoadFS`, `fc.LoadBytes`, `s101.NewEngineFS`, `assets.*FS`), then swap `os.DirFS`→`embed.FS`. **Gated on confirming the IHO catalogue licence permits redistribution.**
- **Delete S-52** (`pkg/s52`, 24 `cs_*.go`, `preslib`, `lookup.go`) once S-101 is at parity — verify against the `s52-portrayal-baseline` tag (commit `f363cea`) with `magick compare`.

## Resume notes

- Both S-52 and S-101 paths build from one binary (replace-then-delete); tag `s52-portrayal-baseline` preserves the S-52 renderer for diffing.
- `CreateScaledDecimal`/`ScaledDecimal` are in-framework (S100Scripting.lua), not host-provided. `HostFeatureGetSimpleAttribute` returns an array of value strings decoded by `valueType`. `HostGetFeatureTypeInfo` needs `AttributeBindings[code].UpperMultiplicity`; `HostGetSimpleAttributeTypeInfo` needs `.ValueType`.
- The S-101 LateralBuoy rule formats its name as `"by %s"` (a DRAFT-catalogue quirk we faithfully reproduce — not a bug).
- gopher-lua is pure-Go Lua 5.1; all 216 rules compile. Screenshot harness: `scripts/shot-s64.mjs` (playwright+chromium); chromium can crash on huge fill meshes at mid-zoom — zoom in or use Firefox.
