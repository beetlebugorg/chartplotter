# Review findings — consolidated punch list

Performance + spec-correctness review of the tile pipeline and frontend, 2026-06-17
(commit `59f34b2`). Severity is impact-on-this-app, not abstract. Each row links to
the subsystem doc with full context. "Confirmed by repro" = verified by building a
throwaway probe, not by inspection alone.

## High priority

| # | Cat | Where | Issue | Fix |
|---|-----|-------|-------|-----|
| H1 | correctness | `bake.go:566` ([tiles §3](tile-pipeline.md)) | **Up-direction best-available suppression is tile-global, not geometry-aware** — drops a coarse feature whenever any finer cell *touches the tile*, even where it doesn't cover the feature. Root of the disappearing-light bug. *Confirmed by repro.* The shipped frontend fix masks the symptom; baker logic is still latent. | Add `anyFinerOverlaps(eligible, r)` mirroring `anyCoarserOverlaps`; gate the up path on AABB overlap instead of `finestNat`. |
| H2 | perf | `lookup.go:38` ([portrayal §3](portrayal.md)) | **LUPT lookup is O(N) over all tables per feature**, called 3–4× per area feature in `BuildFeaturePasses`. Dominant avoidable bake cost. | Index `map[objClass][]*LookupTable` once at load; reuse the built pass in the two-pass diff. |
| H3 | perf | `bake.go:536` ([tiles §4](tile-pipeline.md)) | **`EmitTileInto` scans all `b.prims` per tile** → whole-bake O(#tiles × #prims), no spatial index. | Build an inverted `tile→[]primIdx` index during `TileCoords` (tile ranges already computed); iterate only on-tile prims. |
| H4 | correctness | `lookup.go:161` ([portrayal §2](portrayal.md)) | **LUPT picks the *first* attribute match, not the *most-specific*** (S-52 §10.3.3 requires greatest matched-condition count). Iteration is alpha-by-record order, so a less-specific entry can shadow a more-specific one. | Score candidates by matched-condition count; pick max; tie-break by file order. |
| H5 | perf | `chartplotter.mjs:822,928` ([frontend §2](frontend.md)) | **~540 live GL layers** (≈77 templates × 7 bands), dominated by one `lc-line-*` layer per linestyle, each `combineFilters`-wrapped and re-evaluated per feature/frame. | `line-dasharray` is **not** data-driven, so can't collapse to one layer — instead **dedupe linestyles by distinct (dash,width)** and avoid fanning every template across all 7 bands. |

## Medium priority

| # | Cat | Where | Issue | Fix |
|---|-----|-------|-------|-----|
| M1 | correctness | `lookup.go:207` | `matchesAttribute` does **positional** comma-split + `fmt.Sprintf("%v")`; mis-handles list attrs (order-sensitive; `[]interface{}`→`"[3 1]"`). | Parse both sides to value sets, compare membership. |
| M2 | spec | `cs_subprocs.go:11` | **SEABED01 never emits DEPIT (intertidal) or NODTA**; shallow→DEPMS, DRVAL2 discarded. | Add intertidal/no-data bands; honour DRVAL2. |
| M3 | perf | `build.go:324` | `csAttrs` **clones the full attr map per SOUNDG point** (thousands/cell). | Carry depth out-of-band; don't copy the map. |
| M4 | spec | `chartplotter.mjs:866` | **No `symbol-sort-key`** → collision drop order is arbitrary, not S-52 display-priority. | Set `symbol-sort-key` from a baked priority/`scale`. |
| M5 | spec | `chartplotter.mjs:949` + baker | **Symbols overzoom to map max with no SCAMIN gate** → can show finer than S-52 SCAMIN. | Confirm the baker enforces SCAMIN z-min (it stamps it); if gaps, add a `scamin`-vs-zoom filter. |
| M6 | spec | `declutter.go` (whole) | **Ad-hoc declutter, not S-52 SHOWTEXT**, and not wired into the build.go Primitive path. | Align with §8.3 or document as non-conformant; confirm its consumer. |
| M7 | maint | `chartplotter-app.mjs:2045` | **No `disconnectedCallback`**; permanent `window`/`map`/`shadowRoot` listeners leak if the element is re-added. | Add `disconnectedCallback`; remove bound handlers. |
| M8 | spec | `mvt.go:86,145` | **Ring winding may be spec-inverted** (standard vs surveyor shoelace in Y-down space). Relative orientation is consistent, so renders fine in MapLibre. | Validate with a strict consumer (Tippecanoe/`vt2geojson`); flip the orientation test only if confirmed. |

## Low priority

| # | Cat | Where | Issue | Fix |
|---|-----|-------|-------|-----|
| L1 | perf | `bake.go:814,571`; `mvt.go:218` | Per-feature/ring allocs each tile (`quantizeRing`, `outRings`/`paths`, per-tile tag-index rebuild of identical attrs). | Reuse scratch in `TileScratch`; cache the interned tag array per prim. |
| L2 | perf | `mvt.go:78,93` | `encodePolygon/encodeLines` use un-presized `[]uint32`. | Presize `~1+3·verts`. |
| L3 | perf | `bake.go:742` | Down-fill suppression O(eligible²) worst case on stacked tiles. | Bucket eligible by band / cache coarsest AABB. |
| L4 | correctness | `bake.go:597` | Point in-bounds reject drops symbols in the render-buffer zone; edge symbols pop at seams. | Reject against the buffered rect like polygons/lines. |
| L5 | perf | `chartplotter-app.mjs:230` | `_updateHud` writes `innerHTML` every move frame, unthrottled. | rAF-throttle or coalesce to `moveend`. |
| L6 | spec | `cs_lights06.go:60` | Floodlight/strip-light (CATLIT 8/11/9) drop characteristic text/sector. | Append characteristic text after the short-circuit. |
| L7 | maint | `chartplotter-app.mjs:1272` | `refreshBoxes()` no-op called 4×; legacy `focusChart`/`_showChartPill` live. | Remove dead code. |
| L8 | perf | `cs_soundg03.go:178` | `fmt.Sprintf` per sounding digit × thousands of points. | Precompute digit→symbol-name table. |
| L9 | correctness | `cs_subprocs.go:180` | `csDEPVAL02` `leastDepth` hardcoded −1 → dead `else if` branch. | Remove dead branch or implement. |

## Suggested sequencing

1. **H1** (suppression overlap gate) — closes the disappearing-feature bug at the
   source; pairs naturally with **H3** (spatial index), since both touch
   `EmitTileInto`'s prim iteration.
2. **H2 + H4 + M1** — all in `lookup.go`; one focused pass fixes the dominant bake
   hot path *and* two S-52 correctness gaps (build the class index, score by
   specificity, set-membership attribute match).
3. **H5** — frontend layer-count reduction (dedupe linestyles by dash); largest
   runtime-FPS win, independent of the backend work.
4. Medium spec items (**M2/M4/M5/M6**) as conformance polish; low items
   opportunistically.

## What's confirmed correct (don't "fix")

MVT command/zigzag/extent/version encoding; key-value interning; LIGHTS06 135°
all-round flare orientation (verified vs sprite pivots); LIGHTS colour→symbol
table; SNDFRM04 truncation + SOUNDS/SOUNDG split; SY rotation literal-vs-attribute
parsing; two-pass boundary-style mechanism; xyY→sRGB colour conversion (documented
D65 deviation); template-outer/band-inner draw order; SEABED01 depth cascade; light
`icon-rotate` from `rotation_deg`; the always-on light-text layer; no `setStyle`
churn (`refresh()` uses `setTiles`); guarded+cleared poll interval; batched
`addArchives`; PMTiles Range enforcement + antimeridian longitude skip.
