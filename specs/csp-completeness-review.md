# S-52 Conditional Symbology Procedure (CSP) Completeness Review

_Last reviewed: 2026-06-19. **Verified against the official IHO S-52 Presentation
Library Edition 4.0 Conditional Symbology Procedures** PDF
(`../chartplotter-specs/s52/specs/Conditional_Symbology_Procedures.pdf`, 124 pp).
Page numbers below are that document's printed page numbers._

An audit of the Go reimplementation of S-52 CSPs in `pkg/s52/cs_*.go` for
completeness/correctness. Every finding was cross-checked against the actual
flowcharts; verdicts are **CONFIRMED** (code matches the gap as described),
**REFUTED** (code is wrong in a way the first pass under- or over-stated), or
**REFINED** (correct direction, corrected detail).

> **Edition note (correction to an earlier draft):** the codebase's CSP versions
> — UDWHAZ**05**, RESCSP**02**, SYMINS**02**, WRECKS**05**, OBSTRN**07**,
> LIGHTS**06**, TOPMAR**01** — **exactly match PresLib 4.0**. There is **no
> version lag**. The 21 CSPs in the spec are: CLRLIN01, DATCVR02, DEPARE03,
> DEPCNT03, DEPVAL02, LEGLIN03, LIGHTS06, OBSTRN07, QUAPOS01, QUALIN01, QUAPNT02,
> RESARE04, RESTRN01, RESCSP02, SAFCON01, SLCONS04, SEABED01, SNDFRM04, SOUNDG03,
> SYMINS02, TOPMAR01, UDWHAZ05, WRECKS05.

## Progress (2026-06-19)

**Fixed** (verified, tested, wasm rebuilt):
- ✅ **RESCSP02** rewritten as the 4-family cascade (ENTRES/ACHRES/FSHRES/CTYARE/
  INFARE/RSRDEF) — fixes cable areas (CBLARE) wrongly showing the entry symbol;
  also handles slice-typed RESTRN. (`TestRESCSP02_Families`)
- ✅ **RESTRN01** — removed the non-spec boundary line (it's a signpost → RESCSP02
  only); every dispatching LUPT already draws its own outline.
- ✅ **WRECKS05** DANGER01/02 — dropped the fabricated `SafetyDepth/2` split
  (now `VALSOU <= SAFETY_DEPTH → DANGER01` else DANGER02).
- ✅ **OBSTRN07** danger test `<` → `<=`.
- ✅ **SOUNDG03/SNDFRM04** — removed the invented "suppress feet fractions < 20 ft"
  rule; capped the `C2` unreliable decoration at 2 (QUASOU∪STATUS combined, QUAPOS
  separate) instead of stacking 3×.
- ✅ **LIGHTS06** sector arc colour — now keyed on the COLOUR **set** per the
  combination table (white+red→LITRD, etc.); multi-colour sectors no longer
  collapse to magenta.
- ✅ **TOPMAR01** — corrected TOPSHP 32 → TOPMAR10/TOPMAR30 (verified against the
  spec table; the earlier audit's TOPSHP-14 claim was a misread — 14 was correct).
- ✅ **SpatialContext is now populated at bake time** — a per-cell depth-area index
  (`internal/engine/bake/spatial.go`, point-in-polygon incl. holes) resolves the
  depth area(s) underlying each hazard and feeds `CSContext.Spatial` through
  `BuildFeaturePasses`. This unblocked:
  - ✅ **UDWHAZ05** — the isolated-danger over-trigger is fixed: a hazard is
    ISODGR01 only when an underlying DEPARE/DRGARE has `DRVAL1 >= SAFETY_CONTOUR`
    (in safe water). Falls back to the old conservative "show it" when no topology.
  - ✅ **DEPVAL02** — derives LEAST/SEABED depth from the shoalest underlying
    DRVAL1 (UNSARE → unknown), instead of always returning unknown.
- ✅ **TOPMAR01 float/rigid (P0 #4)** — the cell index now also holds co-located
  platform point aids; `determinePlatformType` follows S-52 (floating iff a
  co-located floating platform — BOY*/LITFLT/LITVES/MORFAC CATMOR=7 — else rigid),
  falling back to the BCNSHP heuristic only when no co-located platform is indexed.
- ✅ **WRECKS ISODGR01 (P0 #2) — resolved by decision.** The live DANGER01/02 swap
  (baked `danger_depth`, client-swapped against the live safety contour) is the
  *intended, mariner-responsive* design for VALSOU dangers — better than a static
  baked ISODGR01, and forcing ISODGR01 to win would over-show it wherever spatial
  context is absent. The genuine defect (UDWHAZ05 flagging *every* underwater
  hazard isolated) is fixed above, which corrects the VALSOU-**less** ISODGR01
  path. A fully-live ISODGR01 (bake the surrounding `DRVAL1`, let the client pick
  ISODGR01 vs DANGER01/02 live) is recorded as optional future work, not a bug.

- ✅ **OBSTRN07 Continuation A (point, P1 #7)** — implemented faithfully from the
  Figure 12 flowchart: QUAPNT02 low-accuracy, UDWHAZ05 → ISODGR01, then the
  symbol + sounding selection (UWTROC `WATLEV==3`→UWTROC03 else UWTROC04; OBSTRN
  CATOBS/WATLEV → OBSTRN01/11/03; sounded dangers → DANGER01/02/03), with
  **SNDFRM04 sounding glyphs** (not the old text label) and LOWACC01. This
  **corrects the earlier UWTROC commit** (it keyed on VALSOU, not WATLEV).
  - `applyDangerDepth` was refactored from a **symbol-replacer** into a **tagger**:
    it now only tags the DANGER01/02 pair with `danger_depth`+`sym_deep` (live
    swap) and leaves soundings / ISODGR01 / OBSTRN11 / DANGER03 intact — which
    also restored sounded WRECKS' depth glyphs (previously dropped). Client
    unchanged. Line/area OBSTRN keep the prior DANGER01/02 path until
    Continuations B/C land.
  - **UDWHAZ05 fallback flipped:** ISODGR01 now shows only when deep surrounding
    water is *confirmed*; otherwise a sounded hazard falls to the live DANGER01/02
    swap (consistent with the P0 #2 decision), rather than the earlier
    conservative "always show ISODGR01".

**Still open** — see backlog below: **OBSTRN07 Continuations B/C** (line/area
geometry — fill/boundary/FOULAR01), LIGHTS06 LITVIS, QUALIN01 (per-edge QUAPOS —
needs spatial *components*, a different index), SAFCON01 glyphs, DATCVR02.

## Architecture context (why several gaps don't affect the web client)

The bake pipeline runs the Go CSPs **once with default mariner settings**
(`internal/engine/baker/baker.go`) and emits per-feature tags; the
mariner-responsive **depth portrayal is re-implemented client-side** in
`web/chartplotter.mjs`. For depth banding / contours the **client is closer to
spec than the Go CSPs**, so a Go gap there may have no visible web effect (but
affects baked defaults and any non-web consumer). Each depth finding notes this.

## Inventory

**Implemented & matching the spec's procedure set (13 main + 7 sub):** DEPARE03,
DEPCNT03, LIGHTS06, OBSTRN07, QUAPOS01, RESARE04, RESTRN01, SLCONS04, SOUNDG03,
SYMINS02, TOPMAR01, WRECKS05 + SEABED01, SAFCON01, SNDFRM04, DEPVAL02, UDWHAZ05,
QUAPNT02, RESCSP02.

**Genuinely-missing, relevant chart-feature CSPs:**

| Missing | Priority | Note |
|---|---|---|
| **DATCVR02** | High | Data-coverage / no-data overfill + M_COVR. Core ENC-display correctness; relates to the tracked z7 tile-hole work. |
| **QUALIN01** | High | Per-edge quality-of-line — the procedure QUAPOS01 is *supposed to delegate to* for low-accuracy lines (`LC(LOWACC21)`). Not implemented. |

**Out of scope (need own-ship/route/AIS/radar/cursor inputs this app lacks):**
CLRLIN01, LEGLIN03 — correctly absent. (LITDSN is now a *narrative* in PresLib
Part I, not a flowchart in this CSP doc — see Lights.)

**`cs_unsare01.go` is not a real CSP.** PresLib 4.0 has **no UNSARE01 procedure**;
UNSARE is symbolised by the plain look-up table (an unconditional fill, and a
Group-1 adjacency input to DEPARE03). The Go file is a synthesized shim — fine as
a LUT alias, but it should not be presented as a spec CSP, and `NODTA`/`NODATA03`
should be checked against the symbol library, not the CSP doc.

---

## Cross-cutting themes (read first)

1. **Spatial topology is never consumed** — the single highest-value structural
   gap. `CSContext` exposes `SpatialContext` (`cs_context.go:21,113-121`) but
   **UDWHAZ05, DEPVAL02, QUAPOS01/QUALIN01, QUAPNT02, SLCONS04**, and the
   DEPARE03/DEPCNT03 contour-edge logic all read object-level attributes only.
   This forces the stubs and over-triggers below. **[CODE]**
2. **Danger symbology is split and partly fought over.** The spec keeps danger
   logic *inside* OBSTRN07/WRECKS05 Continuation A (UDWHAZ → ISODGR01, then
   DANGER01/02/03 by WATLEV+VALSOU, then SNDFRM04). This codebase instead lets a
   portrayal post-step `applyDangerDepth` (`internal/engine/portrayal/build.go`)
   **overwrite** the CSP symbols with DANGER01/02 for any VALSOU feature —
   clobbering WRECKS05's ISODGR01 and the OBSTRN07 symbol choice. **[CODE]**
3. **The two restriction selectors disagree** and *both* are wrong vs spec — see
   RESCSP02 / RESARE04. **[CODE+SPEC]**
4. **Doc comments are unreliable** — several cite page/figure numbers that don't
   match, and describe behaviour the code doesn't implement (RESTRN01 "AP
   patterns", SLCONS04 "per-segment QUAPOS"). Treat code as source of truth.

---

## Depth & seabed

### SEABED01 — depth-band colour — code **REFUTED**
- Spec (Fig 32, p.86-88) bands on **both bounds**: `DRVAL1 >= X && DRVAL2 > X`
  for each contour X; a spanning area takes the shallower colour. Code cascades on
  **DRVAL1 only** and dead-stores drval2 (`cs_subprocs.go:21` `_ = drval2`). **Med.**
- Spec emits **`DEPIT`** for intertidal (DRVAL1<0) and supports a **`TWO_SHADES`**
  2/4-shade mode (default on). Code never emits DEPIT (returns DEPVS) and is
  hardwired 4-shade. Token set is **DEPIT, DEPVS, DEPMS, DEPMD, DEPDW** (correct).
- _Mitigation:_ the client `seabedTokenExpr` (`chartplotter.mjs:322-337`) does
  both-bounds + DEPIT + the four/two-shade toggle correctly → no web impact;
  matters for baked defaults / non-web consumers.

### DEPCNT03 + the safety-contour line — code **REFUTED** (misplaced logic)
- Spec: **DEPCNT03 always draws `LS(SOLD,1,DEPCN)`** (Fig 4/5, p.14-16). The
  heavier **safety-contour highlight `LS(SOLD,2,DEPSC)` belongs to DEPARE03's
  edge loop** (p.11-12), found by the safe/unsafe DEPARE adjacency, using "the
  selected safety contour, **or the next deeper contour if it isn't available**"
  (p.7). Code puts a width-2 DEPSC branch *in DEPCNT03* keyed on exact float
  equality `VALDCO == SafetyContour` (`cs_depcnt03.go:24,46-61`) — wrong
  procedure **and** misses the next-deeper fallback. **High** for the Go path
  (mitigated on web: the client draws DEPSC from straddling DEPARE areas).

### SAFCON01 — contour label — code **REFUTED** (fundamental)
- Spec (Fig 29, p.77-80) returns a **list of `SAFCON` glyph symbols** (position-
  coded digits, **with fraction glyphs for depths < 31 m**), drawn upright at the
  edge midpoint — **not** a `TX()` text instruction. Code emits a single
  `TXInstruction` whole-metre label (`cs_subprocs.go:48-72`). **Med.**

### SOUNDG03 / SNDFRM04 — soundings
- **CONFIRMED:** SOUNDS(bold)/SOUNDG(faint) prefix is chosen vs **SAFETY_DEPTH**
  (`<=` → SOUNDS), matching code (`cs_soundg03.go:119-125`). Digit-position glyph-
  class scheme (`10/20/30/40/50/00`) **matches** the spec (p.92-95).
- **REFUTED — invented rule:** the code's "suppress fractional foot soundings
  below 20 ft" (`cs_soundg03.go:173`) has **no spec basis** (the doc is metres-
  only; fractions are shown for depth < 31 m regardless of unit). **Med.**
- **REFUTED — stacking:** spec applies the unreliable `…C2` decoration at most
  twice (once for QUASOU∪STATUS combined, once for QUAPOS); code runs three
  independent appends and can stack `…C2` **3×** (`cs_soundg03.go:137-160`). Merge
  the QUASOU/STATUS branches. **Low-Med.**

### DEPARE03 — depth-area fill / shallow pattern
- **[SPEC, Low]** shallow-pattern keyed off the wrong contour/bound
  (`cs_depare03.go:56-60`); client `shallowPatternFilter` is correct → no web
  impact. Safety-edge is an area-outline approximation (documented `TODO`,
  unused `HasAdjacentObjects()`).

---

## Dangers & obstructions

### OBSTRN07 — **CONFIRMED** geometry gap + **REFINED** details
- **Geometry-blind (High):** spec splits on geometry (Fig 11, p.35): Point →
  Continuation A; **Line → Continuation B** (per-edge `LS(DOTT/DASH,2,CHBLK)`,
  `LC(LOWACC41/31)`, ISODGR01 at midpoint); **Area → Continuation C** (p.45:
  `AC(DEPVS)` + **`AP(FOULAR01)`** + boundary line + ISODGR01 at centre). Code
  emits a point symbol only, any geometry (`cs_obstrn07.go:42-57`).
- **Sounding via text label, not SNDFRM04 (Med):** spec Continuation A calls
  **`SNDFRM04(DEPTH_VALUE)`** (p.40, like WRECKS05), so QUASOU/TECSOU modifiers
  apply. Code uses a hand-rolled `TXInstruction` (`depthLabelInstruction`).
- **Danger threshold REFINED:** the DANGER01/02/03 + sounding arm tests
  `VALSOU <= SAFETY DEPTH` (p.39-40) — code's `SafetyDepth` target is **right**,
  but uses `<` not `<=` (`cs_obstrn07.go:91`). The *isolated-danger* test is a
  separate UDWHAZ05 decision vs **SAFETY_CONTOUR**.
- **UWTROC fix REFUTED as implemented** — see the dedicated note at the end. Spec
  Continuation A no-VALSOU UWTROC rule is **`WATLEV==3 → UWTROC03, else
  UWTROC04`** (p.39); our recent commit keys on `VALSOU<=0`.

### WRECKS05 — **REFUTED** detail + **CONFIRMED** clobber
- **DANGER01 vs DANGER02 (REFUTED):** spec uses a **single** test
  `VALSOU <= SAFETY DEPTH → DANGER01, else DANGER02` (p.117-118). The code's
  `SafetyDepth/2` split (`cs_wrecks05.go:125`) is **fabricated** — remove it. **Med.**
- **ISODGR01 clobbered (CONFIRMED, High):** WRECKS05 emits ISODGR01 for isolated-
  danger wrecks (`:60`), but `applyDangerDepth` replaces it with DANGER01/02 for
  any VALSOU wreck → the isolated-danger ring is lost. Decide a single owner.

### UDWHAZ05 — **CONFIRMED** over-trigger (High)
- Spec (Fig 39-40, p.105-108) loops underlying DEPARE/DRGARE and sets
  `DANGER=TRUE` **only if an underlying area has `DRVAL1 >= SAFETY_CONTOUR`** (the
  hazard sits in otherwise-safe water). Code skips the loop (`cs_subprocs.go:259-
  262`) and flags **every** underwater hazard ≤ safety contour as isolated →
  ISODGR01 over-triggers (and feeds the WRECKS clobber). Returned
  viewingGroup/priority are discarded by callers.

### DEPVAL02 — **CONFIRMED** no-op (Med)
- Spec (Fig 6, p.18-20) derives LEAST_DEPTH/SEABED_DEPTH from underlying Group-1
  areas (with the WATLEV/EXPSOU gate). Code always returns `(-1,-1)`
  (`cs_subprocs.go:180-211`) — wrecks/obstructions without VALSOU never derive a
  depth.

---

## Lights & topmarks

### LIGHTS06
- **Sector arc colour (CONFIRMED, High):** spec selects from a **COLOUR-set
  combination table** ({1,3}→LITRD, {1,4}→LITGN, {5,6}→LITYW, 3→LITRD, 4→LITGN,
  6/11→LITYW, 1→LITYW, else→**CHMGD**) (p.31). Code uses a **scalar** COLOUR
  (`cs_lights06.go:84-96`) → can't express the combinations; multi-colour sectors
  mis-render. The magenta default itself is **correct**.
- **LITVIS (CONFIRMED gap):** spec forces the sector style to `LS(DASH,1,CHBLK)`
  when `LITVIS == 7|8|3` (obscured / partially obscured / faint, p.31). Code never
  reads LITVIS.
- **Directional/leading light (CONFIRMED incomplete):** spec (p.25) draws
  `LS(DASH,1,CHBLK)` of **length = VALNMR along reversed ORIENT** (sea→light) plus
  a `%03.0lf deg` bearing label. Code emits a geometry-less `LSInstruction`
  (`:122-130`).
- **Sector leg/radius (REFINED — handled at bake):** spec default leg length is a
  fixed **25 mm** (VALNMR only on the mariner's "full sector lines" opt-in); the
  CSP passing `Radius=VALNMR` (`:102`) is moot because the **baker** now
  tessellates 25 mm short + VALNMR full legs (`sectorLegFullNorm`), so the
  rendered default is correct. Low priority.

### LITDSN (light-description string) — **UNVERIFIABLE here**
- PresLib 4.0 **removed the LITDSN C-code/flowchart** and moved the construction
  rules to a *narrative* in **Part I main doc** (p.27 note), which is **not in
  this CSP PDF**. The Morse/alternating/empty-label findings
  (`buildLightCharacteristic`, `cs_lights06.go:275-372`) therefore **need the
  Part I text** to adjudicate. The CSP doc does confirm the input set is
  `CATLIT, LITCHR, SIGGRP, COLOUR, SIGPER, HEIGHT, VALNMR, STATUS` — the code
  ignores CATLIT and STATUS.

### TOPMAR01 — **REFUTED** (float/rigid backwards) + table errors
- **Platform determination (REFUTED, High):** spec loops **co-located point
  objects** and sets floating **only** if one is `LITFLT/LITVES/BOY.../MORFAC
  (CATMOR=7)`; **default is RIGID** (p.101-104). Code defaults **floating** and
  uses a `BCNSHP`-present heuristic (`cs_topmar01.go:58-70`) — backwards, ignores
  co-located class. Rigid topmarks/daymarks without BCNSHP render with floating
  symbols.
- **Default QUESMRK1 (CONFIRMED correct)** for missing TOPSHP (p.101).
- **Table errors (CONFIRMED):** TOPSHP 14 floating → code `TOPMAR14`, spec
  `TOPMAR06`; TOPSHP 32 → code `TOPMAR08`/`TOPMAR28`, spec `TOPMAR10`/`TOPMAR30`
  (`cs_topmar01.go:90,108,144`). Re-derive the table from p.102-103.

---

## Areas, restrictions, shoreline

### RESCSP02 — **REFUTED** (root cause; High)
- Spec (Fig 28, p.71-76) is a **4-family cascade** selecting ENTRES / ACHRES /
  FSHRES / CTYARE / INFARE / RSRDEF, each with 51/61/71 variants by which other
  RESTRN codes (and CATREA) are present. Code emits **only ENTRES** and collapses
  anchoring+fishing to `ENTRES51` (`cs_subprocs.go:149-152`). Replace with the
  full cascade:
  - 7|8|14 → ENTRES (61 if also 1-6/13/16/17/23-27; 71 if also 9-12/15/18-22; else 51)
  - else 1|2 → ACHRES (61/71/51 by the same secondary sets)
  - else 3|4|5|6|24 → FSHRES (61/71/51)
  - else 13|16|17|23|25|26|27 → CTYARE71/51
  - else 9-12/15/18-22 → INFARE51; unknown → RSRDEF51
- Also ignores slice-typed RESTRN (`default:` at `:94` returns nil).

### RESARE04 — **REFINED** (structure correct)
- Spec confirms RESARE04 is a **separate** procedure that does **not** call
  RESCSP02 (p.69) — so the inline reimplementation is structurally **right**, and
  its 51/61/71 CATREA+RESTRN variant logic largely **matches** (p.59-66). Real
  deviation: the no-RESTRN/no-CATREA and unrecognised-CATREA default should be
  **`RSRDEF51`**, not `INFARE51` (`cs_resare04.go`). RESTRN 14 = entry is
  **correct** (CONFIRMED). **Low-Med.**

### RESTRN01 — **CONFIRMED** comment fiction + non-spec boundary
- Spec (Fig 27, p.69-70) is a **"signpost": it only calls RESCSP02 and exits** —
  **no boundary line, no AP patterns.** Code's `AP(RESTRN0x)` comment is fictional,
  and it appends a non-spec `LS(DASH,2,CHGRD)` boundary (`cs_restrn01.go:42,62`).
  Remove the boundary; fix the comment. Inherits all RESCSP02 defects. **Med.**

### SLCONS04 — **CONFIRMED** incomplete (Med-High)
- LS table (CONDTN/WATLEV/CATSLC 6/15/16) is **spec-faithful** (p.84). Missing:
  the **Point branch** (`QUAPNT02 → SY(LOWACC01)`), the **per-edge spatial loop**
  emitting **`LC(LOWACC21)`** for low-accuracy edges (`QUAPOS != 1/10/11`), and the
  area primitive (`cs_slcons04.go`, no `ctx.Spatial` use).

### QUAPOS01 — **REFUTED** (Med)
- Spec (Fig 16, p.48-49) **dispatches**: line → **QUALIN01**, point → **QUAPNT02
  → SY(LOWACC01)**. QUALIN01 (p.50-52) loops edges emitting **`LC(LOWACC21)`** for
  low-accuracy edges, else the normal `LS(SOLD,1,CSTLN)`. Code emits a whole-
  feature `LS(DASH,1,CHBLK)` (`cs_quapos01.go:33-39`) — wrong symbol, wrong
  colour, no per-edge walk, no delegation. Implement **QUALIN01**.

### QUAPNT02 — **REFINED** (value correct)
- `QUAPOS ∈ [2,9] → low-accuracy` is **correct** (p.55). Gaps: no per-component
  spatial loop (reads object-level only, `cs_subprocs.go:314-316`); the "show low
  accuracy" mariner gate is a hardcoded `true` (`:298-302`). Returns `LOWACC01`
  (the point symbol — distinct from QUALIN01's `LOWACC21` line). **Low-Med.**

### SYMINS02 — **CONFIRMED** (TX/TE stub)
- Structure (parse `SYMINS` → validate per geometry → default NEWOBJ symbology) is
  **spec-faithful** (Fig 36, p.98-99). Real gap: `parseTX` (`cs_symins02.go:192-
  204`) treats the paren body as literal text + fixed centre justification; spec
  TX()/TE() carry the full parameter list (text/attr, h/v just, spacing, font,
  x/y offset, colour, display). Parse the full list; drop malformed ones. **Med.**

---

## Prioritized backlog (spec-grounded)

**P0 — wrong/missing safety-relevant symbology, verified:**
1. **UDWHAZ05 over-triggers ISODGR01** — add the underlying-DEPARE loop
   (`DRVAL1 >= SAFETY_CONTOUR`). Root of the isolated-danger errors.
2. **WRECKS05 ISODGR01 clobbered by `applyDangerDepth`** — pick one owner of the
   danger symbology; stop the portrayal step overwriting CSP ISODGR01.
3. **RESCSP02 can't emit ACHRES/FSHRES/CTYARE/INFARE/RSRDEF** — replace the
   3-bucket collapse with the verified 4-family cascade (and reconcile RESARE04's
   default to RSRDEF51).
4. **TOPMAR01 float/rigid is backwards** — default RIGID; set floating only from a
   co-located LITFLT/LITVES/BOY*/MORFAC(CATMOR=7). Fix TOPSHP 14 & 32.
5. **LIGHTS06 sector colour** — evaluate COLOUR as a set against the combination
   table; add the **LITVIS 7/8/3 → dashed-black** override.

**P1 — visible deviations, verified:**
6. **WRECKS05 DANGER01/02** — drop the `SafetyDepth/2` split; use
   `VALSOU <= SAFETY_DEPTH`. OBSTRN07: change danger `<` to `<=`.
7. **OBSTRN07 geometry arms** (Line=Cont.B, Area=Cont.C `AC(DEPVS)+AP(FOULAR01)+
   boundary`) and **route obstruction soundings through SNDFRM04** (not a text
   label) so QUASOU/TECSOU apply.
8. **QUAPOS01 → implement QUALIN01** (per-edge `LC(LOWACC21)`) and the point →
   QUAPNT02 dispatch; same per-edge LOWACC for SLCONS04.
9. **SAFCON01 emits SAFCON glyph symbols** (incl. fractions < 31 m), not a TX
   label; **move the safety-contour highlight to DEPARE03** with the
   "selected-or-next-deeper" rule (DEPCNT03 draws plain DEPCN width-1).
10. **RESTRN01** — remove the non-spec boundary line; it should only call RESCSP02.
11. **Wire `SpatialContext`** into UDWHAZ05 / DEPVAL02 / QUAPOS01 / QUAPNT02 /
    SLCONS04 — the structural fix that unblocks #1, #7, #8, DEPVAL02.
12. **Implement DATCVR02** (data-coverage / no-data) — the clearest missing CSP.

**P2 — lower impact / needs the Part I doc:**
- SOUNDG03 feet-fraction heuristic (remove) + triple-`C2` stacking (cap at 2).
- SEABED01 both-bounds + DEPIT + TWO_SHADES (Go path only; client already correct).
- SYMINS02 full TX/TE parameter parsing.
- LITDSN string rules (Morse `Mo(letter)`, alternating, empty-on-absent) — needs
  **PresLib Part I** narrative (`pslb04_0_part1.pdf`); not in the CSP PDF.
- `cs_unsare01.go` — reclassify as a LUT alias, not a CSP; verify NODTA/NODATA03.

---

## Note on the OBSTRN07 / UWTROC commits (now resolved)

The early commits `fcd8144` (UWTROC symbols) and `80faf91` (centre depth label)
were superseded by the OBSTRN07 Continuation A implementation above: UWTROC now
keys on `WATLEV==3` per the Figure 12 flowchart, and point obstruction soundings
use SNDFRM04 glyphs rather than the `TX()` label. The legacy text-label path
remains only for line/area obstructions pending Continuations B/C.

## Verification provenance

All `CONFIRMED/REFUTED/REFINED` verdicts and page references were checked against
`Conditional_Symbology_Procedures.pdf` (PresLib 4.0). Items explicitly flagged
**unverifiable here** (LITDSN string rules) require `pslb04_0_part1.pdf`. The
`.dai` lookup tables (`pkg/s52/preslib/`) carry symbol/colour definitions but not
the procedure flowcharts.
