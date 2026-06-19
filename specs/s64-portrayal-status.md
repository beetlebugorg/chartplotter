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

## Verified correct (NOT bugs — checked against spec, left alone)

- ICEARE fill = `AC(NODTA)` (grey) — matches the `.dai` LUPT.
- DEPDW deep water `#c9edff` (pale blue) — correct CIE→sRGB of the spec value
  (the "white" label is the S-52 colour name; computed colour is pale blue).
- `EMAREMG1/EMCTNAR1/EMAREGR1/EMTIDIN1` symbols — the constituent glyphs of the
  `CTNARE51`/`TIDINF51` complex linestyles (`SC...` in the `LIND` vector).
- DRGARE dredged area = DEPDW fill + DRGARE01 dot pattern — correct.

## In progress / remaining

- **3.6 Display Priority** (2J4X0001, 2J5X0001/2, GB4X0001) — area draw-order
  (lower-over-higher), scale/overscale boundary line styles, CSP-priority objects,
  centred symbols. Pure rendering; cells at 32°S 61°E.
- **5.0 / 6.0 / 7.0** detection tests (AA3NAVHZ, AA3ARSPC, AA3SAFCO + AA2OVRVU) —
  static chart portrayal is checkable (depth banding vs safety contour, danger
  symbols, restricted-area symbology); the pink **route-crossing alert** highlight
  needs route planning the app does not implement (out of scope for render).
- **3.2 Invalid Object**, **3.4 Non-Official Data**, **3.7 Overlap**, **3.9 Polar**,
  **4.6 Accuracy** — not yet reviewed.
