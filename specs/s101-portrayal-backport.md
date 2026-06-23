# S-101 portrayal backport — retire the S-52 DAI

Status: **plan / not started.** Branch: `s101-dai-conversion`.

## North star

**Delete the embedded S-52 presentation library (`pkg/s52/preslib/PresLib_e4.0.4.dai`, 469 KB) and portray our S-57 ENC data using the IHO S-101 Portrayal Catalogue natively** — its SVG symbols, XML line styles / area fills, colour profile, and Lua rules — consumed *as-is*, not transpiled into a replacement DAI. Where S-101 does not cover an S-57 use case we currently support, render an **obvious magenta placeholder** keyed by the missing identifier (so a render diff lights up the gap), and over time **author our own** catalogue entries to fill those gaps.

The original ask was "convert into a custom DAI file." We are *not* doing that — producing another DAI was only a means to the real end (kill the DAI). Consuming S-101 directly is cleaner and is what the rest of this plan targets. A custom `.dai` is kept on the table only as a fallback if direct SVG consumption proves intractable (it should not).

Source catalogue: <https://github.com/iho-ohi/S-101_Portrayal-Catalogue/tree/main/PortrayalCatalog> (productId `S-101`, version `2.1.0-DRAFT`).

## What the DAI gives us today (must be replaced)

`pkg/s52/parser.go` → in-memory structs, consumed downstream by the baker:

| DAI record | Struct | Downstream consumer | S-101 replacement |
|---|---|---|---|
| `CCIE` colours (day/dusk/night, CIE xyL + sRGB) | `ColorDefinition` | `internal/engine/assets/colortables.go` → `colortables.json` | `ColorProfiles/colorProfile.xml` (same model) |
| `SYMB/SYMD/SVCT` (HPGL vectors, 0.01 mm) | `Symbol` | `assets/sprites.go` (HPGL→SVG/raster atlas) | `Symbols/*.svg` (~500+, already mm-based SVG) |
| `LNST/LIND/LVCT` | `Linestyle` | `assets/linestyles.go` → `linestyles.json` | `LineStyles/*.xml` (~90) |
| `PATD/PVCT` | `Pattern` | pattern fills in baker | `AreaFills/*.xml` (~26) |
| `LUPT/ATTC/INST` (object class + attrs → `SY/LC/LS/AC/AP/TX/CS`) | `LookupTable` | `pkg/s52/lookup.go` | **S-101 `Rules/*.lua`** (no LUPT exists in S-101) |
| `CS` conditional symbology (24 × `pkg/s52/cs_*.go`) | hand-written Go | `pkg/s52/cs_dispatcher.go` | **S-101 `Rules/*.lua`** |

The key asymmetry: S-101 has **no lookup table and no separate CSP files** — *all* portrayal decisions (which is our LUPT *and* our 24 CSPs combined) live in Lua, dispatched `feature.Code → require(Code) → _G[Code](...)`.

## S-101 catalogue inventory (the sources)

- `portrayal_catalogue.xml` — index; registers every symbol/colour/etc. by `id`, `fileName`, `fileType`, `fileFormat`.
- `Symbols/*.svg` — `baseProfile="tiny"`, `width/height/viewBox` in **mm** (float), pivot at origin `(0,0)` (a `class="pivotPoint layout"` circle). Colours via **CSS class tokens**: `fXXXXX` = fill with colour token `XXXXX`, `sXXXXX` = stroke, `f0` = no/transparent fill. `stroke-width` inline in mm. Day/dusk/night selected by swapping CSS (`daySvgStyle.css` / `duskSvgStyle.css` / `nightSvgStyle.css`).
- `LineStyles/*.xml` (`S100LineStyle/5.2`) — `intervalLength`, `pen` (`width` + colour token), repeated `dash` (`start`,`length`), `symbol` refs at `position` along the interval.
- `AreaFills/*.xml` (`S100AreaFill/5.2`) — `symbolFill` → `symbol reference` + basis vectors `v1`/`v2` (tile spacing/orientation), `areaCRS`.
- `ColorProfiles/colorProfile.xml` (`S100ColorProfile/5.1`) — `<colors>` token dictionary + three `<palette>` (day/dusk/night), each `<item token>` with `<cie><xyL>` and `<srgb>`. **Near-identical to our `CCIE` model.**
- `Rules/*.lua` (250+) — `main.lua` (dispatch), `S100Scripting.lua` + `PortrayalModel.lua` + `PortrayalAPI.lua` (host boundary), per-feature-class rules. Classic Lua **5.1** (metatables, `rawget`, no `//`/`goto`/bitwise — confirmed from `S100Scripting.lua`).

### Host contract (confirmed by running it — `cmd/lua-portray-test`)

Reverse-engineered from `main.lua`/`PortrayalModel.lua`/`PortrayalAPI.lua` and verified end-to-end (the `Rapids` rule runs and emits the correct stream). The host (Go) must provide:

**Entry sequence:**
1. Register mariner settings: build an `array:ContextParameter` of `PortrayalCreateContextParameter(name, type, default)` and call `PortrayalInitializeContextParameters(array)` — this *itself* builds the global `portrayalContext` via `PortrayalModel.CreatePortrayalContext()`.
2. `CreatePortrayalContext()` calls `HostGetFeatureIDs()` → per id `HostFeatureGetCode(id)` → `CreateFeature(id, code)` → adds to `FeaturePortrayalItems`.
3. `PortrayalMain(featureIDs)` → per item: `require(feature.Code)`; `_G[feature.Code](feature, featurePortrayal, contextParameters)`; result flows back via `HostPortrayalEmit(featureRef, instructionString, observed)`.

**Feature model:** `CreateFeature` returns a table with a lazy `__index` metatable: `feature.PrimitiveType` is derived from `feature:GetSpatialAssociation().SpatialType` (`HostFeatureGetSpatialAssociations`); attribute reads resolve via `HostFeatureGetSimpleAttribute`/`HostFeatureGetComplexAttributeCount`. Enums (`PrimitiveType`, `SpatialType`, `Orientation`, `Interpolation`) are canonical tables compared by identity — host-built spatial associations must reference the *same* `SpatialType.X` table.

**Host callbacks (~30):** type introspection (`HostGet{Feature,Information,SimpleAttribute,ComplexAttribute,Role,...}TypeCodes/Info`), dataset access (`HostGetFeatureIDs`, `HostFeatureGet{Code,SimpleAttribute,ComplexAttributeCount,SpatialAssociations,AssociatedFeatureIDs,AssociatedInformationIDs}`), spatial (`HostGetSpatial`, `HostSpatialGetAssociated{Feature,Information}IDs`), information types (`HostInformationTypeGet*`, `HostGetSimpleAttribute`, `HostGetComplexAttributeCount`), plus `HostPortrayalEmit`, `HostDebuggerEntry`, and a `Debug` table (`StartPerformance`/`StopPerformance`/`Trace`/`Break`/`FirstChanceError`/`ResetPerformance`). The slice stubs introspection to no-ops and implements only `HostGetFeatureIDs`/`HostFeatureGetCode`/`HostFeatureGetSpatialAssociations`; the production host backs introspection with the S-101 feature catalogue (part of Workstream E).

**Output = the instruction stream we parse into primitives.** Verified emissions: `ViewingGroup:32050;DrawingPriority:9;DisplayPlane:UnderRadar`, `LineStyle:_simple_,,0.96,CHGRD`, `LineInstruction:_simple_`, `ColorFill:CHGRD`, `PointInstruction:<sym>`, `NullInstruction`. This stream is the D→primitive seam.

### The Lua ⇄ host boundary (confirmed from source)

Rules emit a **flat string instruction stream** into `featurePortrayal:AddInstructions(str)`. Observed grammar:

```
PointInstruction:<symbolId>        AreaPlacement:VisibleParts
LinePlacement:Relative,0.5         LocalOffset:0,0
ViewingGroup:X,90022               DrawingPriority:24
DisplayPlane:OverRadar             Date:start,end / TimeValid:closedInterval
```

Plus `SimpleLineStyle('dash',0.32,'CHGRF')`, `AddSpatialReference()`, `ClearGeometry()`, `GetFlattenedSpatialAssociations()`. Host-provided callbacks the VM must implement: `HostGetFeatureTypeInfo`, `HostGet{Simple,Complex}AttributeTypeInfo`, `HostGetRoleTypeCodes`, `CreateScaledDecimal`, `Encode/DecodeDEFString`, `HostDebuggerEntry`. Feature objects expose `ID`, `Code`, `PrimitiveType`, camelCase attribute accessors, `GetInformationAssociation()`.

This instruction stream maps almost 1:1 onto our existing `InstructionSet` (`SY/LC/LS/AC/AP/TX`) + `Primitive` layer — so Lua slots in exactly where `lookup.go` + `cs_dispatcher.go` sit today.

## Target architecture

```
S-57 cell (our parser, unchanged)
   │
   ▼
[NEW] S-57 → S-101 feature/attribute bridge      pkg/s100/bridge
   │   OBJL ACHARE → Code "Anchorage"; DRVAL1 → depthRangeMinimumValue; enum remaps
   ▼
[NEW] Lua portrayal engine (gopher-lua)          pkg/s100/portrayal
   │   main.lua dispatch → Rules/<Code>.lua → AddInstructions(stream)
   │   host implements FeaturePortrayal + HostGet* + ScaledDecimal
   ▼
[NEW] instruction-stream parser → InstructionSet  (reuse existing primitive layer)
   │   PointInstruction → SymbolCall, LinePlacement → LinePattern, ... ViewingGroup/DrawingPriority
   ▼
existing Primitive layer  (internal/engine/portrayal)  ── unchanged
   │   resolve symbol/line/fill IDs + colours against:
   │     [NEW] pkg/s100/catalog : SVG symbols, XML linestyles, XML areafills, colorProfile
   ▼
existing baker → MVT/PMTiles + sprite atlas + colortables.json  ── mostly unchanged
   │   [CHANGED] assets/sprites.go rasterizes SVG directly (was HPGL→SVG)
   ▼
existing MapLibre client  ── unchanged
```

Net: replace the **front** of the pipeline (DAI parse + LUPT + Go CSPs) with **S-101 catalog load + Lua engine + bridge**; keep the **back** (primitive resolution, sprite/colortable/linestyle asset emit, tile baking, client).

## Workstreams

### A. Colour profile → colortables (lowest risk, do first)
- Parse `colorProfile.xml` into the same shape `assets/colortables.go` expects (token → {day,dusk,night} sRGB).
- Validate by diffing the generated `colortables.json` against today's DAI-derived one. Should be ~identical (S-101 colours derive from S-52). Differences = first gap report.

### B. SVG symbols → sprite atlas
- New `pkg/s100/catalog`: load + parse `Symbols/*.svg` (paths, circle/ellipse/rect/line/poly, CSS classes).
- New Go SVG rasterizer dependency (candidate: `github.com/srwiley/oksvg`+`rasterx`, or `resvg` via cgo — evaluate; CSS-class resolution likely needs a small preprocessing pass we write, since `oksvg` won't resolve `fCHYLW`/`sCHBLK` classes against a palette).
- Resolve CSS colour classes → palette colour → concrete RGBA per scheme; rasterize at our `DefaultPxPerSymbolUnit`. Emit per-scheme atlas (or one atlas + runtime tint, matching current behaviour — check `assets/sprites.go`).
- Map SVG pivot (origin) → sprite anchor. viewBox → sprite bbox.
- `assets/sprites.go` changes from "HPGL→SVG/raster" to "SVG→raster" — the SVG half already exists internally, so this is a shortening, not a new capability.

### C. Line styles + area fills → existing asset emitters
- `LineStyles/*.xml` → `linestyles.json` shape (`assets/linestyles.go`): `intervalLength`→period, `dash` list→dash array, `pen`→width+colour, `symbol` refs→placed sub-symbols.
- `AreaFills/*.xml` → pattern fills: `symbol reference` + `v1`/`v2` tile basis → spacing/orientation the baker already understands.

### D. Lua portrayal engine (the hard part)
- Add `github.com/yuin/gopher-lua` (pure Go, Lua 5.1 — matches S-101; works under `GOOS=js/wasm` since pure Go). Verify no rule uses 5.2+ features (spot-check passed for `S100Scripting.lua`).
- Embed `Rules/*.lua` via `go:embed`.
- **Reverse-engineer the full host API** from `PortrayalModel.lua` / `PortrayalAPI.lua` / `S100Scripting.lua` (enumerate every `Host*` and `featurePortrayal:*` call) — this is step 1 of D and the chief unknown.
- Implement host objects in Go: `FeaturePortrayal` (collect `AddInstructions` stream, geometry refs, viewing group / drawing priority / display plane), feature accessor backed by the bridge, `contextParameters` ← our mariner settings (`SafetyContour`, `ShallowContour`, …), `HostGet*` catalogue introspection backed by `pkg/s100/catalog`, `CreateScaledDecimal`.
- Write the instruction-stream parser: `AddInstructions` strings → our `InstructionSet`/`Primitive`.

### E. S-57 → S-101 feature/attribute bridge
- Largest mechanical piece. Map S-57 6-char acronyms → S-101 camelCase: object class `OBJL` → `feature.Code` (e.g. `ACHARE`→`Anchorage`), attributes (`DRVAL1`→`depthRangeMinimumValue`, `CATACH`→…), and enumerated value remaps. Source of truth: **IHO "S-57 to S-101 conversion guidance"** (verify exact published table).
- Build as data tables (generated, not hand-typed where possible). Any S-57 class/attribute with **no S-101 mapping → placeholder + logged gap** (see below).

## Gap handling — "obvious test data" + "make our own"

Two mechanisms:
1. **Placeholder primitive.** A loud sentinel (magenta filled box with `!`, label = the missing identifier) emitted whenever: a referenced symbol/line/fill id is absent from the catalogue; a `feature.Code` has no Lua rule; the bridge can't map a class/attribute; or a Lua rule errors. Tagged so screenshots/`magick compare` reveal exactly what's unportrayed. Plus a gen-time `--strict` report enumerating every gap (untranslatable SVG feature, unmapped attribute, missing rule).
2. **Own catalogue entries.** A local overlay dir (`assets/portrayal-overrides/`) holding our own SVG/XML/Lua that shadows or extends S-101 by id — this is how we "make our own" for genuine S-101 gaps (raster-only DAI symbols, S-57 use cases S-101 dropped, etc.) without forking the upstream catalogue.

## Verification

- Keep the S-52 DAI render as the **baseline oracle** for the whole migration (do not delete until parity). Use the existing harness (`verify-bake-render-locally`, `scripts/shot-s64.mjs`, S-64 cells) to bake the same cells both ways and `magick compare`. Gaps appear as magenta; regressions as diff pixels.
- Gate the new path behind a flag (e.g. `--portrayal=s101`) so the DAI path stays default until parity. Delete DAI + `pkg/s52/cs_*.go` + `lookup.go` only in the final phase.

## Phase 1 result (done)

Vendored the catalogue (sibling `../../../s101-portrayal-catalogue`, pinned at clone) and built `cmd/portrayal-inventory`, which loads our embedded DAI via the real parser and cross-references it against the S-101 catalogue. Output: `specs/s101-coverage-matrix.md`. **Artwork coverage is ~99%:**

| Family | DAI defs | covered by S-101 | gaps |
|---|---|---|---|
| Symbols | 532 | 526 | **6** (`BOYLAT34 DISMAR03 DISMAR04 EMNEWOB1 NEWOBJ01 SISTAT02`) |
| Line styles | 55 | 52 | **3** (`LOWACC01 LOWACC11 NEWOBJ01`) |
| Patterns / area fills | 25 | 25 | **0** |
| Colours | 67 | 67 | **0** |
| Object classes | 175 (S-57 acronyms) | — | needs bridge vs 216 S-101 rule codes |

S-101 also adds 198 symbols + 12 line styles we don't have. Takeaway: **the artwork is essentially free; the real work is the bridge (E) and Lua engine (D), not the symbols.** The 9 artwork gaps are the first "make our own" / placeholder candidates.

## De-risk results (done)

- **gopher-lua viability (Workstream D, the biggest unknown): PASS.** `cmd/lua-smoke` compiled all **216** `Rules/*.lua` with `github.com/yuin/gopher-lua` v1.1.2 (pure-Go Lua 5.1) — zero parse failures. No 5.2+ constructs anywhere; the VM choice is safe. (Compile-only; runtime host-API behaviour is Workstream D proper.)
- **Colour seam (Workstream A): PASS, exact.** `cmd/s101-color-diff` compared all 201 token×scheme cells (67 colours × Day/Dusk/Night); the S-101 `colorProfile.xml` sRGB is **byte-identical** to our DAI CIE→sRGB output. Colour profile is a clean drop-in; **0 gaps**.
- **Lua engine vertical slice (Workstream D): PASS, end-to-end.** `cmd/lua-portray-test` loads the *real* framework (`S100Scripting`+`PortrayalModel`+`PortrayalAPI`) and runs the real `Rapids` rule driven entirely by Go host callbacks. The feature's `PrimitiveType` resolves correctly from a Go-supplied spatial association, mariner context parameters register, and all three geometry branches emit the exact expected instruction stream (Point→`NullInstruction`, Curve→`LineStyle`+`LineInstruction`, Surface→`ColorFill`). This proves rules don't just *compile* — they *execute correctly* against our host and produce the D→primitive instruction grammar. Full host contract documented above.
- **SVG rasterization + CSS-class colour resolution (Workstream B): PASS, pure Go.** `cmd/svg-raster-test` flattens an S-101 symbol (resolve `<?xml-stylesheet?>` CSS classes → inline `style`, strip `.layout` debug boxes) and rasterizes with `srwiley/oksvg`+`rasterx` (pure Go, wasm-safe). Output matches the `librsvg` reference. Element set is tiny — only `path/rect/circle/line/g`, **no text/gradients/use/images**. Two oksvg defects found + worked around in the flattener: (1) it ignores a non-zero `viewBox` origin → normalize to `0 0 W H` + wrap content in `translate(-minX -minY)`; (2) it applies `stroke-width` in device px without scaling by the draw transform → pre-multiply `stroke-width` by the px/mm scale. No cgo/external rasterizer needed. Caveats for the production flattener: also scale `stroke-width` carried in `style`/CSS or inherited from a parent `<g>` (corpus uses presentation attrs); honour the CSS cascade where an inline presentation attr coexists with a class (e.g. `CBLARE52`).

## Phasing

1. ~~**Inventory & coverage matrix**~~ — **DONE** (Phase 1 result above; `cmd/portrayal-inventory` → `specs/s101-coverage-matrix.md`).
2. ~~**Colours** (A)~~ — **DONE** (exact match, `cmd/s101-color-diff`). Remaining: wire `colorProfile.xml` into `assets/colortables.go` as the source (currently still DAI-backed).
3. ~~**SVG symbols** (B) — rasterizer de-risk~~ — **DONE** (`cmd/svg-raster-test`; oksvg+rasterx, CSS resolution, matches librsvg). Remaining: production flattener + wire into `assets/sprites.go` for all 724 symbols + sprite atlas/anchors.
4. ~~**Line styles + area fills** (C)~~ — **DONE** (`pkg/s100/catalog`: parses all 64 `LineStyles/*.xml` incl. `compositeLineStyle` multi-component, and all 25 `AreaFills/*.xml` symbolFill into Go structs; latin1 charset handled; spot- + bulk-tested). Remaining: lower these onto the engine's `linestyles.json`/pattern emitters.
5. **Static-artwork checkpoint** — render via a *temporary* keep-our-LUPT path to prove the artwork is correct independent of Lua.
6. ~~**Lua host API** (D, step 1) — enumerate + stub host interface; get a real rule running~~ — **DONE** (vertical slice: `cmd/lua-portray-test` runs `Rapids` end-to-end via Go host; contract documented). Remaining: back introspection with the feature catalogue; `PortrayalMain` over many features + `HostPortrayalEmit`.
6b. ~~**Instruction-stream → draw commands** (D→primitive seam, parser half)~~ — **DONE**. `pkg/s100/instructions`: tokenizer + state-folding reducer (`ViewingGroup/DrawingPriority/DisplayPlane/LocalOffset/Rotation/LinePlacement` modifiers; `PointInstruction/LineInstruction/ColorFill/AreaFillReference/TextInstruction/NullInstruction` draws; inline `LineStyle:_simple_` capture; unknown kinds surfaced as gaps). Unit-tested on the real `Rapids` streams and wired live into `cmd/lua-portray-test` (Lua emit → parse → `DrawCommand`s). Remaining: lower `DrawCommand` → engine `Primitive` (resolve symbol/line/fill/colour against the loaded catalogue).
7. **Bridge** (E) — S-57→S-101 features/attributes.
8. **Wire Lua as the lookup+CSP stage** — instruction-stream → primitives; placeholders live.
9. **Full-cell visual diff** vs S-52 baseline; iterate on gaps; author overrides.
10. **Decommission** — remove DAI, 24 `cs_*.go`, `lookup.go`; flip default.

## Risks & open decisions

- **Lua version / completeness:** gopher-lua is 5.1 + partial 5.2; S-101 rules *appear* 5.1-clean but 250 files need a compile-load smoke test early (phase 6).
- **Bridge completeness:** the S-57↔S-101 mapping is large and the published guidance may not cover every NOAA attribute; unmapped → placeholder (acceptable, visible).
- **SVG rasterizer fidelity & CSS classes:** no off-the-shelf Go rasterizer resolves S-100 CSS colour classes — we write that resolution pass. Evaluate `oksvg`/`rasterx` vs `resvg`-cgo (cgo hurts the wasm/cross-build story — `make xbuild`, `wasm-baker-build`).
- **wasm/binary size:** embedding Lua rules + SVGs + bridge tables; pure-Go gopher-lua keeps wasm viable but watch size.
- **Licensing:** confirm the IHO catalogue's licence permits vendoring/redistribution before embedding.
- **DRAFT catalogue:** S-101 PC is `2.1.0-DRAFT`; pin a commit.

## Files (new unless noted)

- `pkg/s100/catalog/` — load SVG/XML/colourProfile, `HostGet*` backing.
- `pkg/s100/portrayal/` — gopher-lua engine, host objects, instruction-stream parser.
- `pkg/s100/bridge/` — S-57→S-101 feature/attribute/enum maps (generated tables).
- `cmd/` ingest/inventory tool — coverage matrix + `--strict` gap report.
- `assets/portrayal-overrides/` — our own SVG/XML/Lua for gaps.
- *changed:* `internal/engine/assets/sprites.go` (SVG→raster), `colortables.go`, `linestyles.go`; baker portrayal entry point; `cmd/chartplotter/main.go` (flag, drop `preslib.DAI`).
- *deleted (final phase):* `pkg/s52/preslib/*.dai`, `pkg/s52/cs_*.go`, `pkg/s52/lookup.go`.
```
