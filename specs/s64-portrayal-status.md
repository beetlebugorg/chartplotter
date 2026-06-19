# S-64 Portrayal Conformance — Status

Working audit of our S-52 portrayal against the IHO **S-64 ENC test dataset**
(`testdata/S-64_ENC_Unencrypted_TDS.zip`) using its reference plots in
`testdata/S-64 Ed 3.0.3_EN_Clean_Final.pdf` as the source of truth.

**Render→compare loop:** bake a cell → `web/<name>.pmtiles` + `<name>.json`, serve
`?prod&catalog=/<name>.json`, screenshot with `scripts/shot-s64.mjs` (presets
mariner settings + camera), diff against the PDF plot; confirm root causes with
live `queryRenderedFeatures` probes before editing. Branch: `s64-portrayal-fixes`.

## Fixed (committed, verified)

| Test area | Fix | Commit |
|---|---|---|
| 3.1 all | **Text groups** (PresLib §14.4/§14.5): per-group toggles (Important 11 / Names 21,26,29 / Light 23 / Other), baked `tgrp` tag, client filter. All text was showing in Display Base. | `feat(s52): text groups` |
| 3.1.2/3.1.3 | **Object codes 160=TS_FEB, 163=NEWOBJ**: table stopped at WRECKS(159); tidal streams + NEWOBJ rendered QUESMRK. 161/162 stay QUESMRK (correct unknown-object test). | `fix(s57): map object codes 160/163` |
| 3.1.2/3.1.3 | **SYMINS02 TX/TE parse**: was emitting the raw param list as the label; now delegates to main parseTX/parseTE → "V-AIS"/"TEMP". | `fix(s52): SYMINS02 parses TX/TE` |
| 3.1.2 | **printf zero-pad width**: route bearing `%03.0lf` → "090 deg" not "90 deg". | `fix(s52): honour printf zero-pad` |
| 3.2.1 a) | **SY/AP image-id collision**: four PresLib names are BOTH a point symbol and an area fill pattern (QUESMRK1, AIRARE02, FSHFAC03, MARCUL02). MapLibre keys images by one id, and `styleimagemissing` (which fired before `registerAllSymbols`, first-wins) routed the name to `registerPattern` whenever `patterns.json` had it — so the 178×392 tiled-"?" pattern hijacked the 26×46 SY(QUESMRK1) mark, rendering every unknown-object "?" as a stretched off-position fragment. Fix: namespace pattern images under a `pat:` id prefix (fill-pattern exprs add it; symbols keep bare names). Client-only — the bake already carried QUESMRK1. | `fix(web): namespace AP patterns` |

## Verified correct (NOT bugs — checked against spec/data, left alone)

- ICEARE fill = `AC(NODTA)` (grey) — matches the `.dai` LUPT.
- UNSARE fill = `AC(NODTA);AP(NODATA03);LS(SOLD,2,CHGRD)` — matches the `.dai`
  exactly (the 5.0 cell's grey dashed band; despite `cs_unsare01.go` being a shim,
  the output is spec-correct).
- DEPDW deep water `#c9edff` (pale blue) — correct CIE→sRGB of the spec value
  (the "white" label is the S-52 colour name; computed colour is pale blue).
- `EMAREMG1/EMCTNAR1/EMAREGR1/EMTIDIN1` symbols — the constituent glyphs of the
  `CTNARE51`/`TIDINF51` complex linestyles (`SC...` in the `LIND` vector).
- DRGARE dredged area = depth fill + DRGARE01 dot pattern — correct.
- **Safety-contour banding responds** (7.0): pixel-sampled DRGARE 5m / DEPARE
  10-30m = DEPMD (167,217,251) at safety contour 0 → DEPMS (130,202,255) at ≥11;
  DRGARE 42m stays DEPDW (>30 deep contour). The shade shift is subtle to the eye
  but correct. (Trust pixel samples over eyeballing here.)
- Basemap graticule dashes over water in GB4X0001 — basemap, not a chart feature
  (all-chart-layer query returns only DEPARE at those pixels).
- Centred area symbols (restricted/traffic) render one-per-area at the centroid.

## Verified-correct cells (no portrayal bugs found beyond the fixes above)

- **3.1 Base / Standard / Other** — match the references after the 4 fixes.
- **5.0 Navigational Hazards** (AA3NAVHZ) — wrecks (WRECKS01/04/05), obstructions
  (DANGER01/OBSTRN11/OBSTRN01), platforms, UNSARE band, clearance text. No QUESMRK.
  (ISODGR not emitted here — the dangers sit in unsurveyed water, where the
  isolated-danger mark is *permitted* not required per the 5.0 note.)
- **6.0 Special Conditions** (AA3ARSPC) — restricted/special-condition areas render
  magenta-dashed symbolized boundaries + centred type symbols (caution/info/entry/
  fishery), matching the 6.1 plot. RESCSP02/RESARE04 working. No QUESMRK.
- **7.0 Safety Contour** (AA3SAFCO) — depth banding tracks the safety contour
  (above). The DEPSC safety-contour *line* is the one item not positively
  confirmed (thin; client draws it from straddling DEPARE — see audit).
- Objective QUESMRK sweep: zero unknown symbols in safco/arspc/navhz/gb4.
- **3.2 Invalid Object** (AA3INVOB) — after the SY/AP id-collision fix above, the
  three unknown-class objects (OBJL_500 area / 501 line / 502 point) each show a
  clear magenta SY(QUESMRK1) "?" at Display Category Other, safety contour 0 —
  satisfying the test's stated pass criterion (QUESMRK1 displayed for point/line/
  area). Portrayal (`BuildFeature`) and bake were already correct (42 QUESMRK1 in
  the baked tiles); the bug was purely the client pattern-hijack. Known-class
  objects (WRECKS/OBSTRN/RESARE/SILTNK/CBLSUB/BOYCAR/TOPMAR) render their normal
  symbology. Caveat below.

## In progress / remaining

- **3.6 Display Priority** (2J4X0001, 2J5X0001/2, GB4X0001) — area draw-order
  (lower-over-higher), scale/overscale boundary line styles, CSP-priority objects,
  centred symbols. Pure rendering; cells at 32°S 61°E.
- **5.0 / 6.0 / 7.0** detection tests (AA3NAVHZ, AA3ARSPC, AA3SAFCO + AA2OVRVU) —
  static chart portrayal is checkable (depth banding vs safety contour, danger
  symbols, restricted-area symbology); the pink **route-crossing alert** highlight
  needs route planning the app does not implement (out of scope for render).
- **3.2 unknown line/area default geometry** (minor): the reference plot draws the
  unknown LINE as a magenta dashed line and the unknown AREA as a dashed symbolized
  boundary *in addition to* the centred "?". Our `unknownObjectBuild` (S-52 §10.1.1)
  emits only the SY(QUESMRK1) point for every geometry — so the "?" is correct but
  the line/boundary shape isn't drawn. Doesn't fail the stated QUESMRK1 criterion;
  a completeness refinement (emit a default LS dashed line / boundary for line/area
  unknowns) if pursued.
- **3.2.1 b)** (GB5X01NE + GB4X0000 base, Display Standard, safety contour 10) and
  **3.2.2** pick report — not yet rendered/checked.
- **3.4 Non-Official Data**, **3.7 Overlap**, **3.9 Polar**, **4.6 Accuracy** —
  not yet reviewed.
