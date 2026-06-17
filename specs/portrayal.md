# S-52 portrayal engine

Files: `internal/engine/portrayal/build.go`, `pkg/s52/*` (lookup, conditional
symbology, colours, instructions).

## 1. Architecture — class+geometry+attributes → instructions → primitives

`BuildFeature` (`build.go:54`):

1. Map S-57 geometry to a LUPT code P/L/A (`geometryCode`, `build.go:548`).
2. `lib.LookupFeatureRaw(objClass, geomCode, attrs, mariner)` (`lookup.go:104`)
   runs `selectInstruction` (`lookup.go:34`): filter all lookup tables by object
   class + geometry + table name/mariner mode (`matchesTableName`, `lookup.go:329`
   — PLAIN vs SYMBOLIZED_BOUNDARIES, SIMPLIFIED vs PAPER_CHART, LINES), collect
   attribute candidates + a no-attribute failsafe, then `findFirstAttributeMatch`
   (`lookup.go:161`). Returns parsed instructions plus DPRI/DISC/RPRI metadata.
3. The **walker** (`build.go:261`) walks the instruction list:
   AC→`FillPolygon`, AP→`PatternFill`, LS→`StrokeLine`, LC→`LinePattern`,
   SY→`SymbolCall` (rotation as a literal or from an attribute via `RotationAttr`,
   `build.go:284`), TX/TE→`DrawText`, SECTOR→`SectorLight`, CS→recursive
   `ExecuteCS` dispatch bounded by `maxCSPDepth=4` (`build.go:307`).
4. CS dispatch (`cs_dispatcher.go:50`, name→procedure). Procedures emit ordinary
   instructions, walked recursively.
5. `BuildFeaturePasses` (`build.go:117`) produces **1 pass** (bnd=2) for
   non-areas / style-invariant areas, or **2 passes** (plain bnd=0 + symbolized
   bnd=1) when the lookups differ or route through RESARE04 — this is what lets
   the frontend toggle boundary symbolization live without a re-bake.
6. SOUNDG is special-cased (`build.go:73`): one instruction-set run per coordinate
   with per-point DEPTH injected via `csAttrs`. `applyDangerDepth` (`build.go:187`)
   post-processes OBSTRN/WRECKS to a DANGER01/02 pair carrying VALSOU so the
   client swaps the symbol against the live safety contour.

Primitives are lat/lon and viewport-independent; the baker assigns z-min from
SCAMIN/CSCL and stamps the `bnd` tag.

## 2. S-52 conformance

### Confirmed correct

- **Display priority / category / radar** extracted from the fixed LUPT ID
  (`parser.go:578`) and propagated (`lookup.go:248`); DISC → DISPLAYBASE / STANDARD
  / OTHER (`lookup.go:361`), with a deliberate SOUNDG OTHER→STANDARD promotion.
- **LIGHTS06 flare selection** by COLOUR (`cs_lights06.go:207`): W→LIGHTS13,
  R→LIGHTS11, G→LIGHTS12, Y/O→LIGHTS13. Matches the S-52 table.
- **LIGHTS06 all-round default flare orientation = 135°** (`cs_lights06.go:140`) is
  **correct**, cross-checked against `web/sprite.json`: LIGHTS11/12/13 are 26×64
  with `pivot_y ≈ 59.93` (base anchor), so the flare points **up** natively; the
  client applies map-aligned clockwise `icon-rotate`, so 135° aims the flare
  down-right, clear of the upper-right characteristic label. Directional uses
  `ORIENT+180`, sectored uses sector-midpoint+180 (`cs_lights06.go:144/147`).
- **SOUNDG03 / SNDFRM04** (`cs_soundg03.go`): subdivision algorithms 1–6, SOUNDS
  (≤ safety depth, bold) vs SOUNDG (deep, faint) prefix split, truncation with an
  `+1e-6` FP guard (documents a prior X.95 bug). TECSOU/QUASOU/STATUS/QUAPOS
  modifier symbols per S-52.
- **Colour tables** day/dusk/night parsed from DAI CCIE (`colors.go:100`), cached;
  token→RGB via xyY→XYZ→linear→gamma. *Documented deliberate deviation*: no
  Illuminant-C→D65 Bradford adaptation, clamp-before-gamma — trades strict
  colorimetry for atlas pixel-stability (`colors.go:1`).
- **SY rotation literal-vs-attribute parsing** (`instructions.go:398`); two-pass
  boundary-style mechanism; RESARE04 / SYMINS02 structure.

### Deviations / gaps

- **LUPT match is "first", not "most-specific"** (`lookup.go:161`,
  *high/correctness*). `findFirstAttributeMatch` returns the **first** candidate
  whose attributes all match; the comment claims "per S-52 spec" but S-52 §10.3.3
  requires the entry with the **greatest number of matching attribute conditions**
  to win. Iteration order is the tables sorted alphabetically by raw LUPT record
  string (≈ DAI serial order), so a less-specific entry that sorts earlier can
  shadow a more-specific one (e.g. BOY*/BCN* with CAT+COLOUR combos). **Fix:** score
  candidates by matched-condition count, pick the max, tie-break by file order.
- **`matchesAttribute` list handling is fragile** (`lookup.go:207`,
  *med/correctness*). For a comma "AND" expectation it does **positional**
  comparison after splitting both sides on `,` — but S-52 list-attribute matching
  is **set membership**, so COLOUR `"1,3"` vs encoded `"3,1"` mis-matches; and
  `fmt.Sprintf("%v", actualValue)` on a `[]interface{}` yields `"[3 1]"`, never
  matching. **Fix:** parse both to value sets and compare membership.
- **SEABED01 omits DEPIT / NODTA** (`cs_subprocs.go:11`, *med/spec*). Returns only
  DEPVS/DEPMS/DEPMD/DEPDW; intertidal (drying, negative DRVAL1) is clamped to −1
  and coloured DEPVS instead of the distinct **DEPIT** band, and DRVAL2 is computed
  then discarded so the fill is driven by DRVAL1 alone. **Fix:** add intertidal /
  no-data bands; honour DRVAL2.
- **`declutter.go` is an ad-hoc engine, not S-52 SHOWTEXT** (*med/spec*). Priority
  by a hardcoded `ViewGroup`, 8 cardinal fallback positions, a 0.6×fontsize
  char-width heuristic. It does not implement S-52 §8.3 text grouping/priority and
  is **not wired into the build.go Primitive path** (it operates on a separate
  `RenderPrimitive` type). Treat as non-conformant / clarify its consumer.
- **Floodlight/strip-light short-circuit drops characteristic text**
  (`cs_lights06.go:60`, *low/spec*): CATLIT 8/11 and 9 return the symbol only, with
  no characteristic text or sector even when present.
- **`csDEPVAL02` dead branch** (`cs_subprocs.go:180`, *low*): `leastDepth` is
  hardcoded −1 so `else if leastDepth > 0` is unreachable.
- **Light-characteristic text styling** (`cs_lights06.go:275`) produces
  `Fl(1)R 3s 4.3m 5M`; the space-before-colour appears only when there's no group,
  a small inconsistency vs INT-1 conventions. Functionally fine.

## 3. Performance

- **`selectInstruction` is O(N) over *all* lookup tables per feature**
  (`lookup.go:38`) — full linear scan of hundreds of entries for every feature,
  and `LookupFeatureRaw` is called **3–4× per area feature** in
  `BuildFeaturePasses` (`build.go:123/136/137`, plain + plain-again + symbolized +
  diff). **Biggest win: index `map[objClass][]*LookupTable` once at load** and
  reuse the already-built pass in the diff.
- **`matchesAttribute` allocates per condition**: `fmt.Sprintf("%v")` +
  `strings.Split` on every candidate × condition (`lookup.go:207`).
- **`csAttrs` clones the whole attribute map per SOUNDG point** (`build.go:324`) —
  thousands of map allocations for a dense sounding cell. Carry depth out-of-band.
- **`instructionSetsDiffer` stringifies every instruction** to compare per area
  feature (`build.go:172`).
- **`fmt.Sprintf` per sounding digit** in SNDFRM04 (`cs_soundg03.go:178`) ×
  thousands of points; precompute a digit→symbol-name table.
