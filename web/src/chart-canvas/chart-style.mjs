// chart-style.mjs — PURE MapLibre chart-layer builder.
//
// Extracted from <chart-canvas> (chart-canvas.mjs): the S-52 chart layer/style
// BUILDING — area/line/pattern/symbol/sounding/text templates, the per-band /
// per-server-set expansion, the SCAMIN-bucket layers, and the bookkeeping maps
// the element's live updaters read. This is a PURE function: it takes the
// element's resolved state and RETURNS the layers plus three bookkeeping maps
// (layerBase / variants / layerVis). It never touches `this` or the DOM, so the
// data flow is one-way: element state → buildChartLayers(params) → returned maps
// → element assigns them. The live updaters (applyFeatureFilters, setBaseFilter,
// _eachLayer, _setVis, setBandVisible, setScheme, setMariner) stay on the element
// and keep reading this._layerBase / this._variants / this._layerVis exactly as
// before.
import * as S52 from "./s52-style.mjs";
import { FONT } from "./s52-style.mjs";
import {
  CHART_BANDS, BAND_DISPLAY_MIN, scaminDisplayZoom, SCAMIN_BUCKET_LAYERS,
} from "./chart-sources.mjs";

// Fill-pattern (AP) images live under this id prefix so they never collide with
// point-symbol (SY) images of the SAME PresLib name. Several names are BOTH a
// point symbol and an area fill pattern (QUESMRK1, AIRARE02, FSHFAC03, MARCUL02):
// e.g. an unknown object is SY(QUESMRK1) — a 26×46 "?" mark — while an unknown
// AREA could be AP(QUESMRK1) — a 178×392 tiled "?" fill. MapLibre keys images by a
// single id, so without this prefix the pattern atlas cell hijacked the symbol
// (styleimagemissing fires before registerAllSymbols → pattern won, first-wins),
// rendering the point "?" as a stretched fragment. Symbols keep their bare names.
export const PAT_PREFIX = "pat:";

// -- layer template helpers (palette/mariner threaded as params) -------------
function iconSizeForScale(atlasPpu) {
  return ["/", ["coalesce", ["get", "scale"], atlasPpu], atlasPpu];
}
// Complex (symbolised) linestyles are tessellated in the BAKER per zoom: the
// baked complex_lines layer carries the dash "on" segments as real geometry
// (so they're crisp and phase-locked at every zoom — no pattern stretch), and
// the embedded marks (chevron/anchor/"!") ride the normal point_symbols layer.
// So here the dashes are just a plain solid stroke coloured by color_token
// (which restyles live for Day/Dusk/Night).
function complexLineLayers(palette) {
  return [{
    id: "complex-lines", type: "line", source: "chart", "source-layer": "complex_lines",
    paint: { "line-color": S52.colorExpr("color_token", undefined, palette), "line-width": ["coalesce", ["get", "width_px"], 1] },
  }];
}
// S-52 halign/valign → a DATA-DRIVEN MapLibre text-anchor. text-anchor became a
// property-function (zoom-and-feature) property, so all general text now rides ONE
// collidable layer instead of nine per-anchor sublayers. That matters for
// DECLUTTERING: MapLibre keeps a single collision index but places symbols
// layer-by-layer, so nine sublayers made a label's survival depend on WHICH anchor
// it drew in (text-center-* always beat text-right-*), not on importance. One layer
// + symbol-sort-key lets S-52 text priority decide who survives, globally.
const TEXT_ANCHOR = (function () {
  // middle/baseline/center valigns collapse to the "center" row; top/bottom keep.
  const vrow = ["match", ["coalesce", ["get", "valign"], "middle"], "top", "top", "bottom", "bottom", "center"];
  const key = ["concat", vrow, "|", ["coalesce", ["get", "halign"], "center"]];
  return ["match", key,
    "center|left", "left", "center|right", "right", "center|center", "center",
    "top|center", "top", "bottom|center", "bottom",
    "top|left", "top-left", "top|right", "top-right",
    "bottom|left", "bottom-left", "bottom|right", "bottom-right",
    "center"]; // default: dead-centre
})();

// Collision priority (S-52 §14.4 text grouping / S-100 Part 9 text placement):
// LOWER sort-key = placed FIRST = wins. Rank by the baked `tgrp` (DISPLAY param) so
// important text (11) outranks geographic/feature names (21/26/29), which outrank
// descriptive text (nature of seabed 25, magnetic variation 27, heights, …). Within
// a tier the LARGER label wins (subtract font size) — a dense approach (Annapolis)
// then thins to the navigationally important labels instead of an anchor-order
// lottery. Tiers are spaced 50 apart so font size only ever breaks WITHIN-tier ties.
const TEXT_SORT_KEY = ["-",
  ["match", ["coalesce", ["get", "tgrp"], -1],
    11, 0,             // important text
    [21, 26, 29], 100, // geographic / feature names
    23, 50,            // light description (a stray non-light group-23 label)
    150],              // descriptive / other / unknown
  ["coalesce", ["get", "font_size_px"], 10]];

function textLayers(mariner, palette) {
  // LIGHTS characteristic text is drawn by its OWN always-on layer (see the
  // "light-text" layer in buildLayers) so it can't be decluttered behind a
  // verbose name label — exclude it from the general (collidable) text layer.
  const notLight = ["!=", ["get", "class"], "LIGHTS"];
  return [{
    id: "text", type: "symbol", source: "chart", "source-layer": "text",
    filter: ["all", notLight, ["!=", ["get", "class"], "NEWOBJ"], S52.textGroupFilter(mariner)],
    layout: {
      "text-field": ["coalesce", ["get", "text"], ""], "text-font": FONT,
      "text-size": ["coalesce", ["get", "font_size_px"], 11],
      "text-anchor": TEXT_ANCHOR,
      // S-52 TX/TE labels are single-line; MapLibre's default text-max-width (10 em)
      // wrapped longer labels (e.g. "Information about chart display (A,B)") onto a
      // second line that then collided and dropped. A wide max-width keeps each label
      // on one line, matching the spec plots.
      "text-max-width": 40,
      "symbol-sort-key": TEXT_SORT_KEY,
      "text-allow-overlap": false, "text-optional": true,
      visibility: "visible",
    },
    paint: {
      // Legible at dusk/night (bright ink + dark halo) — see textColor.
      "text-color": S52.textColor(active, palette),
      "text-halo-color": S52.textHaloColor(active),
      "text-halo-width": 1.4,
      "text-halo-blur": 0.5,
    },
  }, {
    // Producer-placed text (NEWOBJ + SYMINS TX/TE, S-52 §10.3.3.8 — e.g. the PresLib
    // "ECDIS Chart 1" legend captions): the producer's EXPLICIT instruction, which
    // must always render. Two-line captions ("restricted area," + "anchoring
    // prohibited") are TWO separate point features stacked one line apart; the
    // general collidable layer above declutters them and drops the lower line. Honour
    // them on their OWN always-on layer (text-allow-overlap), mirroring "light-text".
    // text-max-width 40 is unchanged, so genuine single-line labels still never wrap.
    id: "placed-text", type: "symbol", source: "chart", "source-layer": "text",
    filter: ["all", ["==", ["get", "class"], "NEWOBJ"], S52.textGroupFilter(mariner)],
    layout: {
      "text-field": ["coalesce", ["get", "text"], ""], "text-font": FONT,
      "text-size": ["coalesce", ["get", "font_size_px"], 11],
      "text-anchor": TEXT_ANCHOR,
      "text-max-width": 40,
      "text-allow-overlap": true, "text-ignore-placement": true,
      visibility: "visible",
    },
    paint: {
      "text-color": S52.textColor(active, palette),
      "text-halo-color": S52.textHaloColor(active),
      "text-halo-width": 1.4,
      "text-halo-blur": 0.5,
    },
  }];
}
// Multiply every PIXEL-VALUED size property by `k` so S-52 features render at
// their true physical size on THIS screen. The baker emits sizes (icon `scale`,
// `width_px`, `font_size_px`) as if 1 CSS px = 1 typographic point (0.35278 mm /
// 72 DPI); but a CSS px is 1/96 in (0.2645 mm) — and the actual screen may differ
// again. The element computes k = 0.35278 / pxPitch and passes it in, scaling
// icons/lines/text/halos together (line-dasharray is in line-width units, so it
// scales for free). Only sizes are touched — colours/filters/placement are unchanged.
function _scaleSizes(layers, k) {
  if (!(k > 0) || Math.abs(k - 1) < 1e-6) return layers;
  const mul = (v) => (v == null ? v : ["*", k, v]);
  for (const L of layers) {
    if (L.layout) {
      if (L.layout["icon-size"] != null) L.layout["icon-size"] = mul(L.layout["icon-size"]);
      if (L.layout["text-size"] != null) L.layout["text-size"] = mul(L.layout["text-size"]);
    }
    if (L.paint) {
      if (L.paint["line-width"] != null) L.paint["line-width"] = mul(L.paint["line-width"]);
      if (L.paint["text-halo-width"] != null) L.paint["text-halo-width"] = mul(L.paint["text-halo-width"]);
    }
  }
  return layers;
}

function buildLayers(mariner, palette, atlasPpu, osm, sizeScale) {
  // Over an OSM basemap (raster or vector), let its detailed land show through:
  // drop the chart's own land fills so OSM land isn't painted over. Filter by
  // colour token, not class, so it catches LNDARE (LANDA) AND built-up land
  // BUAARE (CHBRN) and any other land-coloured area. (No-data hatch hidden too —
  // see buildStyle.)
  const notLand = ["match", ["get", "color_token"], ["LANDA", "CHBRN"], false, true];
  const base = [
    // Paint area fills in S-52 display-priority order (DrawingPriority, baked as
    // draw_prio: DEPARE=3, LNDARE=12…) so a higher-priority fill draws ON TOP — e.g.
    // land over water. Real ENCs tile their areas (no overlap, so order is moot), but
    // a cell with OVERLAPPING areas (the PresLib "ECDIS Chart 1" inset: one deep-water
    // polygon under the shallow water + land) would otherwise paint last-in-tile on
    // top, hiding land/shallow under deep water. Tiebreak by -drval1 so shallower
    // water draws over deeper within the same priority.
    { id: "areas", type: "fill", source: "chart", "source-layer": "areas", ...(osm ? { filter: notLand } : {}),
      layout: { "fill-sort-key": ["-", ["*", ["coalesce", ["get", "draw_prio"], 0], 1000], ["coalesce", ["get", "drval1"], 0]] },
      paint: { "fill-color": S52.areasFillColor(palette, mariner) } },
    { id: "area_patterns", type: "fill", source: "chart", "source-layer": "area_patterns", paint: { "fill-pattern": ["concat", PAT_PREFIX, ["coalesce", ["get", "pattern_name"], ""]] } },
    // SHALLOW_PATTERN (SEABED01, client-side): DIAMOND1 over depth areas on
    // the shallow side of the live safety contour, shown only when the
    // mariner toggle is on. Filter/visibility update on safetyContour /
    // shallowPattern — no re-bake.
    { id: "shallow-pattern", type: "fill", source: "chart", "source-layer": "areas", filter: S52.shallowPatternFilter(mariner), layout: { visibility: mariner.shallowPattern ? "visible" : "none" }, paint: { "fill-pattern": PAT_PREFIX + "DIAMOND1" } },
    { id: "lines-solid", type: "line", source: "chart", "source-layer": "lines", filter: ["==", ["coalesce", ["get", "dash"], "solid"], "solid"], paint: { "line-color": S52.colorExpr("color_token", undefined, palette), "line-width": ["coalesce", ["get", "width_px"], 1] } },
    { id: "lines-dashed", type: "line", source: "chart", "source-layer": "lines", filter: ["==", ["get", "dash"], "dashed"], paint: { "line-color": S52.colorExpr("color_token", undefined, palette), "line-width": ["coalesce", ["get", "width_px"], 1], "line-dasharray": [4, 3] } },
    { id: "lines-dotted", type: "line", source: "chart", "source-layer": "lines", filter: ["all", ["==", ["get", "dash"], "dotted"], ["!", ["has", "danger_depth"]]], paint: { "line-color": S52.colorExpr("color_token", undefined, palette), "line-width": ["coalesce", ["get", "width_px"], 1], "line-dasharray": [1, 2] } },
    // LIGHTS06 sector figure (coloured arcs / OUTLW backing / dashed legs) — its
    // OWN source-layer so it can be SCAMIN-bucketed (see SCAMIN_BUCKET_LAYERS)
    // without dragging every coastline/contour into per-SCAMIN variants. Styling
    // mirrors lines-solid/lines-dashed (the sector tessellation emits only solid
    // and dashed runs); sleg/category/boundary gating rides combineFilters as before.
    { id: "sector-lines-solid", type: "line", source: "chart", "source-layer": "sector_lines", filter: ["==", ["coalesce", ["get", "dash"], "solid"], "solid"], paint: { "line-color": S52.colorExpr("color_token", undefined, palette), "line-width": ["coalesce", ["get", "width_px"], 1] } },
    { id: "sector-lines-dashed", type: "line", source: "chart", "source-layer": "sector_lines", filter: ["==", ["get", "dash"], "dashed"], paint: { "line-color": S52.colorExpr("color_token", undefined, palette), "line-width": ["coalesce", ["get", "width_px"], 1], "line-dasharray": [4, 3] } },
    // OBSTRN/WRECKS dotted foul boundary (client-side): shown only when the
    // feature's VALSOU is ≤ the live safety contour. Filter updates on
    // safetyContour — no re-bake. Excluded from lines-dotted above.
    { id: "danger-boundary", type: "line", source: "chart", "source-layer": "lines", filter: S52.dangerBoundaryFilter(mariner), paint: { "line-color": S52.token("CHBLK", "#000000", palette), "line-width": 2, "line-dasharray": [1, 2] } },
    // Safety-contour line (DEPARE03, client-side): a heavier DEPSC outline of
    // depth areas straddling the live safety contour, drawn over the plain
    // DEPCN contour lines. Filter updates on safetyContour — no re-bake.
    { id: "safety-contour", type: "line", source: "chart", "source-layer": "areas", filter: S52.safetyContourFilter(mariner), paint: { "line-color": S52.token("DEPSC", "#3a6a8a", palette), "line-width": 2 } },
    // Chart scale boundaries (DATCVR §10.1.9.1): a CHGRD line where the
    // navigational purpose changes, baked into the scale_boundaries layer.
    // Standard display, on by default; toggled via mariner.showScaleBoundaries.
    { id: "scale-boundaries", type: "line", source: "chart", "source-layer": "scale_boundaries", layout: { visibility: mariner.showScaleBoundaries === false ? "none" : "visible" }, paint: { "line-color": S52.colorExpr("color_token", undefined, palette), "line-width": ["coalesce", ["get", "width_px"], 1.5] } },
  ];
  const top = [
    // Point symbols split by ROTATION REFERENCE FRAME (S-52 6.1.1 §3.1.6 / PresLib
    // Part I §9.2 ROT). icon-rotation-alignment is a per-layer constant, not data-
    // driven, so the same `point_symbols` source-layer feeds two layers:
    //   • screen-up (default): no rotation, or a literal angle like a light flare
    //     (ROT 1/2 — rotated about the pivot relative to the TOP OF THE SCREEN).
    //     These must NOT turn with the chart → icon-rotation-alignment "viewport".
    //   • true-north: rotation from an S-57 attribute (ORIENT) or an edge tangent
    //     (ROT 3, tagged rot_north at bake) — must turn WITH the chart to stay
    //     aligned to true north → icon-rotation-alignment "map".
    // The north layer carries _baseId "point_symbols" so every restyle/toggle keyed
    // on that id (icon-image swap, category/point-style filters, band caps) hits both.
    { id: "point_symbols", type: "symbol", source: "chart", "source-layer": "point_symbols", filter: ["!=", ["coalesce", ["get", "rot_north"], 0], 1], layout: { "icon-image": S52.pointSymbolImage(mariner), "icon-size": iconSizeForScale(atlasPpu), "icon-rotate": ["coalesce", ["get", "rotation_deg"], 0], "icon-rotation-alignment": "viewport", "icon-allow-overlap": true, "icon-ignore-placement": true, "symbol-z-order": "source" } },
    { id: "point_symbols-north", _baseId: "point_symbols", type: "symbol", source: "chart", "source-layer": "point_symbols", filter: ["==", ["coalesce", ["get", "rot_north"], 0], 1], layout: { "icon-image": S52.pointSymbolImage(mariner), "icon-size": iconSizeForScale(atlasPpu), "icon-rotate": ["coalesce", ["get", "rotation_deg"], 0], "icon-rotation-alignment": "map", "icon-allow-overlap": true, "icon-ignore-placement": true, "symbol-z-order": "source" } },
    // Spot soundings — an individually-selectable "Other" item per S-52/IMO
    // (default on). A plain visibility toggle on showSoundings.
    { id: "soundings", type: "symbol", source: "chart", "source-layer": "soundings", layout: { "icon-image": S52.soundingsIconImage(mariner), "icon-size": iconSizeForScale(atlasPpu), "icon-allow-overlap": false, visibility: mariner.showSoundings === false ? "none" : "visible" } },
    // Contour labels (SAFCON01, client-side): VALDCO along DEPCNT lines,
    // toggled by the mariner's "contour labels" setting — no re-bake.
    { id: "contour-labels", type: "symbol", source: "chart", "source-layer": "lines",
      filter: ["all", ["==", ["get", "class"], "DEPCNT"], ["has", "valdco"]],
      layout: { "symbol-placement": "line-center", "text-field": S52.contourLabelField(mariner), "text-font": FONT, "text-size": 10, "text-max-angle": 30, "text-allow-overlap": false, "text-optional": true, visibility: mariner.showContourLabels ? "visible" : "none" },
      paint: { "text-color": S52.contourLabelColor(active, palette), "text-halo-color": S52.textHaloColor(active), "text-halo-width": 1.2 } },
    // Dredged-area depth label (S-52 row 47, client-side): DRVAL1 placed at the
    // DRGARE centroid, in the chosen depth unit. The baker drops the rule's
    // fixed-metres "%gm" text so this tracks depthUnit (same pattern as
    // contour-labels). Reads the `areas` source-layer → SCAMIN-cloned below.
    { id: "drgare-labels", type: "symbol", source: "chart", "source-layer": "areas",
      filter: ["all", ["==", ["get", "class"], "DRGARE"], ["has", "drval1"]],
      layout: { "text-field": S52.drgareLabelField(mariner), "text-font": FONT, "text-size": 10, "text-allow-overlap": false, "text-optional": true },
      // FontColor:CHBLK per the S-101 catalogue DredgedArea rule. NOT textColor:
      // this layer reads the `areas` source-layer, whose `color_token` is the
      // dredged-area FILL colour, so textColor painted the depth value that same
      // light shade (white-on-white). The CHBLK token resolves per palette (light
      // ink at dusk/night), so it stays legible in every scheme.
      paint: { "text-color": S52.token("CHBLK", "#000000", palette), "text-halo-color": S52.textHaloColor(active), "text-halo-width": 1.2 } },
    // Light characteristics (LIGHTS06 TX, e.g. "Fl(1)R 3s 4.2m") — their own
    // layer. It precedes the feature-name layers in the style order, so MapLibre
    // places light text FIRST: it wins collisions against plain name labels (a name
    // can never hide a light) WITHOUT light text being exempt from placement. It
    // still collides against OTHER light text — S-100 Part 9 text placement says
    // drop labels you can't place, so a navaid-dense approach (Annapolis) thins to a
    // readable set instead of an unreadable stack of overlapping characteristics.
    { id: "light-text", type: "symbol", source: "chart", "source-layer": "text",
      filter: ["==", ["get", "class"], "LIGHTS"],
      layout: { "text-field": ["coalesce", ["get", "text"], ""], "text-font": FONT,
        "text-size": ["coalesce", ["get", "font_size_px"], 10], "text-anchor": "top", "text-offset": [0, 0.4],
        // Left-justify so a merged multi-line light label's lines align on their
        // left edge (e.g. stacked "Mo(U)W 20s 50m 17M" / "Mo(U)R 20s 50m 15M").
        "text-justify": "left",
        // Within the light layer, the brighter/larger characteristic wins a
        // collision (bigger font → smaller sort-key → placed first).
        "symbol-sort-key": ["-", 0, ["coalesce", ["get", "font_size_px"], 10]],
        "text-allow-overlap": false, "text-optional": true,
        // Light descriptions (LIGHTS06 characteristics) — individually
        // selectable per S-52 (default on); toggled by showLightDescriptions.
        visibility: mariner.showLightDescriptions === false ? "none" : "visible" },
      paint: { "text-color": S52.textColor(active, palette), "text-halo-color": S52.textHaloColor(active), "text-halo-width": 1.4, "text-halo-blur": 0.5 } },
  ];
  // Template chart layers (source "chart" is a placeholder rewritten per band
  // by expandChartLayers). Their `filter` is the intrinsic (base) filter.
  // Scale all pixel sizes to true physical size BEFORE the SCAMIN split so the
  // *_scamin clones (which share these layout/paint objects) inherit it.
  const tmpl = _scaleSizes(base.concat(complexLineLayers(palette), top, textLayers(mariner, palette)), sizeScale);
  // SCAMIN AREA/LINE split: each template layer reading one of the four area/line
  // source-layers is IMMEDIATELY FOLLOWED BY a clone reading the matching
  // "<sl>_scamin" source-layer (id "<id>-scamin"). The original now only ever
  // carries no-SCAMIN features (its source-layer is NOT in SCAMIN_BUCKET_LAYERS,
  // so expandChartLayers leaves it single, always-in-band). The clone's _scamin
  // source-layer IS in the set, so expandChartLayers buckets it into per-SCAMIN
  // fractional-minzoom variants — that's what makes a SCAMIN area/line disappear
  // past its 1:N scale. Adjacency preserves draw order. The clone tags _baseId =
  // the original id so its band/bucket variants register in _variants under the
  // ORIGINAL base id — every live restyle/visibility/filter update (setScheme's
  // setIf, _eachLayer, setBaseFilter) that targets the original id automatically
  // also hits the clone, so SCAMIN features restyle/toggle identically. (e.g.
  // contour-labels for DEPCNT, which now live in lines_scamin; safety-contour /
  // shallow-pattern reading areas_scamin; danger-boundary reading lines_scamin.)
  const SCAMIN_SRC = new Set(["areas", "area_patterns", "lines", "complex_lines"]);
  const withScamin = [];
  for (const L of tmpl) {
    withScamin.push(L);
    const sl = L["source-layer"];
    if (SCAMIN_SRC.has(sl)) {
      withScamin.push({ ...L, id: L.id + "-scamin", "source-layer": sl + "_scamin", _baseId: L.id });
    }
  }
  return withScamin;
}

// What's capped to a coarse band's max (so it isn't drawn at a finer zoom): LINES
// and pattern (hatch) FILLS — the marks that visibly duplicate a finer band's
// coast/contour/boundary as offset strokes — PLUS `point_symbols`, because the
// chevron/anchor/"!" marks embedded in complex line styles ride that layer: cap
// them WITH their line, or (where a coarse band overzooms a finer band's gap, e.g.
// open water at the approach band) the line is cut but the chevrons float on their
// own. Base area fills (solid depth/land colour), SOUNDINGS and TEXT keep
// overzooming: the base fill is the continuous gap-fill (a finer fill draws over
// it), and soundings/text are their own layers that read fine overscale. Where a
// finer band exists it supplies its own symbols at the band boundary, so capping
// the coarse copies there is seamless.
function _capsAtBand(L) {
  // Both point-symbol layers (screen-up + true-north split, same source-layer) cap
  // with their band: the complex-line chevron/anchor/"!" marks ride the true-north
  // copy, the plain marks the screen copy.
  const isPointSym = L.id === "point_symbols" || L._baseId === "point_symbols";
  return L.type === "line" || isPointSym || (L.type === "fill" && L.paint && L.paint["fill-pattern"] !== undefined);
}

// Invisible "probe" layers that force the SPARSE sub-band tiles (where SCAMIN
// features float below their band min) to load at all zooms, so the per-SCAMIN
// value set can be collected (querySourceFeatures only sees LOADED tiles, and a
// tile loads only if some visible layer needs it). They render nothing. Without
// them, the bucket layers can't exist until their tiles load, but those tiles
// won't load until the buckets exist — a deadlock at sub-band zooms.
function _pushScaminProbes(out, server) {
  // Server mode gets its SCAMIN values from the baked manifest, so it needs no
  // probes (these were the per-frame cost: minzoom-0 layers processed at every
  // zoom to force sub-band tiles to load for runtime collection). Only the prebaked
  // (pmtiles) path, which still discovers values from tiles, needs them.
  if (server) return;
  const srcs = CHART_BANDS.filter((b) => b.slug !== "all").map((b) => "chart-" + b.slug);
  for (const src of srcs) {
    for (const sl of SCAMIN_BUCKET_LAYERS) {
      out.push({ id: "scaminprobe-" + src + "-" + sl, source: src, "source-layer": sl, type: "circle", minzoom: 0, filter: ["has", "scamin"], paint: { "circle-radius": 0, "circle-opacity": 0, "circle-stroke-width": 0 } });
    }
  }
}

// Append the S-52 overscale pattern AP(OVERSC01) for a band's source, shown only
// above the band's native max (where the chart is grossly enlarged, ≥ ~×2 its
// compilation scale). Inserted right after the band's base fill so a finer band's
// opaque fill covers it — the hatch survives only on coarse-only overscale patches.
// S-52 §10.1.10.2; display priority 3, viewing group 21030.
//
// `finerPresent` is the spec gate: emit ONLY when a finer band is loaded, so a real
// chart-scale boundary exists and this band can show through a finer band's hole
// (grossly overscaled → pattern). When this band IS the finest available, plain
// zoom-in is "deliberate overscale of best-available" and must show ONLY the ×N
// overscale indication, never the pattern (§10.1.10.1) — so we emit nothing and the
// HUD ×N stands alone. No-op for the merged "all" set (no per-band layering).
function _pushOverscale(out, source, band, layerVis, showOverscale, bandsHidden, finerPresent) {
  const nm = CHART_BANDS.find((b) => b.slug === band);
  if (!nm || band === "all" || nm.max >= 18 || !finerPresent) return;
  const id = "overscale@" + source;
  const vis = showOverscale === false ? "none" : "visible";
  layerVis[id] = vis;
  out.push({
    id,
    type: "fill",
    source,
    "source-layer": "areas",
    minzoom: nm.max + 1,
    layout: { visibility: bandsHidden.has(band) ? "none" : vis },
    paint: { "fill-pattern": PAT_PREFIX + "OVERSC01" },
  });
}

// Build a chart variant's layout, folding band on/off into the template's
// intended visibility and recording that intent for later restore — so a style
// rebuild (basemap/server-set swap) keeps a turned-off band off.
function _variantLayout(L, band, id, bandsHidden, layerVis) {
  const vis = (L.layout && L.layout.visibility) || "visible";
  layerVis[id] = vis;
  return { ...(L.layout || {}), visibility: bandsHidden.has(band) ? "none" : vis };
}

// Active scheme branch ("day"/"dusk"/"night"). The palette encodes the active
// scheme's colour TABLE, but the text/contour colour helpers (S52.textColor /
// textHaloColor / contourLabelColor) ALSO key off the scheme NAME to pick the
// day-vs-dark text ink (a light/dark legibility branch, not a colour lookup) —
// exactly as the element did with this._active. Set by buildChartLayers from the
// passed `scheme` before any template helper runs, so the build is byte-identical.
let active = "day";

// PURE entry point. Builds the full expanded chart layer set for the current
// element state and RETURNS it plus the three bookkeeping maps the element's
// live updaters read. No `this`, no DOM, no side effects on its inputs.
export function buildChartLayers({
  mariner, palette, atlasPpu, osm,            // osm = the boolean from this._osmBasemap()
  scheme,                                     // active scheme branch ("day"/"dusk"/"night") = this._active
  server, serverSets, scaminValues, scaminLat, // chart-source state (already resolved)
  bandsHidden,                                 // Set (this._bandsHidden)
  bandsPresent = new Set(),                    // Set of band slugs that have data — gates the overscale pattern
  ignoreScamin,                                // DEBUG: drop the per-SCAMIN display gate (show everything in-band)
  sizeScale = 1,                               // px→true-physical feature-size multiplier (0.35278/pxPitch); see _scaleSizes
  pxPitch,                                     // calibrated CSS-pixel pitch (mm) → SCAMIN gates on the true physical scale
}) {
  active = scheme || "day";
  const layerBase = {}, variants = {}, layerVis = {};
  const tmpl = buildLayers(mariner, palette, atlasPpu, osm, sizeScale);
  const out = [];
  // Overscale-pattern gate (S-52 §10.1.10.2): a band gets the AP(OVERSC01) hatch only
  // when a strictly-FINER band is present in the loaded set — i.e. a real chart-scale
  // boundary exists for it to show through. The finest band present is the
  // best-available data, so its plain zoom-in is the ×N-only case (§10.1.10.1).
  const _bandRank = (slug) => CHART_BANDS.findIndex((b) => b.slug === slug);
  const _presentRanks = [...bandsPresent]
    .filter((slug) => slug && slug !== "all")
    .map(_bandRank)
    .filter((i) => i >= 0);
  const _finestPresentRank = _presentRanks.length ? Math.max(..._presentRanks) : -1;
  // Emit the hatch for `slug` only when this band is itself present AND a strictly
  // finer band is also present (the pmtiles path iterates ALL bands, so the present
  // check matters). The finest present band never qualifies — it's best-available.
  const finerBandPresent = (slug) => {
    const r = _bandRank(slug);
    return r >= 0 && bandsPresent.has(slug) && r < _finestPresentRank;
  };
  // Group each base template layer with the *_scamin clone that _withScamin placed
  // immediately after it (tagged _baseId), so the pair expands TOGETHER per band
  // below — both fill paths iterate group-outer, band-mid, member-inner. Expanding
  // them as two independent template entries (every band's plain `areas`, THEN every
  // band's `areas_scamin`) let a COARSE band's SCAMIN area — e.g. a coastal BUAARE —
  // stack ABOVE a FINER band's plain fill (a harbor DEPARE), so coastal docks painted
  // over harbor water and read as land. Grouping keeps cross-band coarse→fine intact
  // within each fill tier while still drawing all fills below lines/symbols/text.
  const groups = [];
  for (const L of tmpl) {
    if (L._baseId && groups.length) groups[groups.length - 1].push(L);
    else groups.push([L]);
  }
  // Server mode: one source per active per-band set (chart-<district>-<band>).
  // Iterate template-outer, set-inner — and serverSets is ordered coarse→fine —
  // so the global draw order is by S-52 class (all fills, then lines, then symbols,
  // then text), with finer bands' fills over coarser ones. Each band's source
  // overzooms above its own max (from its TileJSON), so a coarse-only spot (open
  // water) is filled by the general/overview source instead of blanking. As in the
  // pmtiles path, overview/general LINE + pattern layers are capped at their band
  // max so the coarse marks don't bleed into a finer band's zooms. Variant id is
  // "<id>@<set>" so scheme/mariner updates by base id hit every set's copy.
  if (server) {
    const lat = scaminLat;
    for (const group of groups) {
      for (const set of serverSets) {
        for (const L of group) {
        const base = L.filter ?? null;
        const dmin = BAND_DISPLAY_MIN[set.band];
        const capped = (set.band === "overview" || set.band === "general") && _capsAtBand(L);
        // mk pushes one variant of L for this set — same shape as the pmtiles path's
        // mk, keyed by set name. Stores its base filter (for live re-combine) and a
        // native minzoom, and mirrors the coarse-band maxzoom cap.
        const mk = (suffix, baseFilter, minzoom) => {
          const id = L.id + "@" + set.name + suffix;
          layerBase[id] = baseFilter;
          // Register under the ORIGINAL base id for a *_scamin clone (L._baseId),
          // so every restyle/toggle keyed on the original id reaches the clone too.
          (variants[L._baseId || L.id] ||= []).push(id);
          const { _baseId, ...tmplL } = L; // _baseId is internal — keep it out of the MapLibre layer
          const v = { ...tmplL, id, source: "chart-" + set.name, filter: S52.combineFilters(baseFilter, mariner), layout: _variantLayout(L, set.band, id, bandsHidden, layerVis) };
          if (minzoom != null) v.minzoom = minzoom; // band appears at its scale, not the baked floor
          if (capped) v.maxzoom = CHART_BANDS.find((b) => b.slug === set.band).max;
          out.push(v);
        };
        const and = (extra) => (base ? ["all", base, extra] : extra);
        // SCAMIN buckets, BAKED MANIFEST: one native fractional-minzoom layer per
        // distinct SCAMIN value (from the set's TileJSON `scamin`, published by the
        // baker), so a feature shows exactly from its 1:N scale in both directions;
        // features lacking SCAMIN take the `#no` variant. The values are known at
        // load — NO runtime probe / querySourceFeatures / setStyle (the per-frame
        // cost the manifest removes). MapLibre flips each bucket on its zoom crossing
        // natively (zero JS/frame). The per-band archive is FLOOR-GATED at bake, so
        // tile CONTENT controls appearance: client layers need no band minzoom.
        const scaminVals = set.scamin || [];
        if (!ignoreScamin && SCAMIN_BUCKET_LAYERS.has(L["source-layer"]) && scaminVals.length) {
          // Only materialize a per-value bucket where the SCAMIN cutoff zoom is
          // ABOVE this set's source floor (set.min). The set's tiles don't load
          // below set.min, so any SCAMIN whose cutoff is ≤ set.min shows from the
          // floor regardless — fold those into the `#no` (always-from-floor) bucket
          // with the no-SCAMIN features. Cuts the bucket count from "every distinct
          // SCAMIN" to "only values that hide above the band's own start" — a large
          // reduction for the fine bands (most of their SCAMIN sit at ~band scale,
          // so they collapse) and especially `text` (9 anchor templates × set ×
          // value). NOT quantized → SCAMIN is still honoured exactly.
          const floor = set.min || 0;
          const lowVals = [], hiVals = [];
          for (const sc of scaminVals) (scaminDisplayZoom(sc, lat, pxPitch) <= floor + 1e-6 ? lowVals : hiVals).push(sc);
          const noFilter = lowVals.length
            ? ["any", ["!", ["has", "scamin"]], ["in", ["get", "scamin"], ["literal", lowVals]]]
            : ["!", ["has", "scamin"]];
          mk("#no", and(noFilter), undefined);
          for (const sc of hiVals) {
            mk("#sm" + sc, and(["==", ["get", "scamin"], sc]), scaminDisplayZoom(sc, lat, pxPitch));
          }
        } else {
          mk("", base, undefined);
        }
        void dmin; // band display-min superseded by bake floor-gating (kept for ref)
        // Right after this band's base depth/land fill, drop the S-52 overscale
        // pattern AP(OVERSC01) for the zooms where the band is GROSSLY overscale
        // (display scale ≥ ~×2 its compilation scale → above its native max). It's
        // interleaved per band, so a finer band's opaque fill covers it where finer
        // data exists — the hatch is left only on the coarse-only (overscale) patches
        // such as open water shown enlarged. S-52 §10.1.10.2.
        if (L.id === "areas") _pushOverscale(out, "chart-" + set.name, set.band, layerVis, undefined, bandsHidden, finerBandPresent(set.band));
        }
      }
    }
    _pushScaminProbes(out, server);
    return { layers: out, layerBase, variants, layerVis };
  }
  // Iterate TEMPLATE-outer, band-inner so the global draw order is by S-52
  // class (all bands' fills, then all bands' lines, then all symbols, then all
  // text) rather than per-band stacks. Band-outer order put a finer band's area
  // FILLS above a coarser band's point SYMBOLS, so a coarse-scale light/beacon
  // that overzoomed past its band got buried under the finer chart's depth-area
  // fill the moment you zoomed in — it "disappeared". Keeping bands coarse→fine
  // WITHIN each class preserves best-available (finer fill covers coarser fill),
  // while symbols/text now always sit above every band's fills.
  const lat = scaminLat;
  for (const group of groups) {
    for (const band of CHART_BANDS) {
      for (const L of group) {
      const base = L.filter ?? null;
      const dmin = BAND_DISPLAY_MIN[band.slug];
      const capped = (band.slug === "overview" || band.slug === "general") && _capsAtBand(L);
      // mk pushes one variant: id, the per-layer base filter it should re-combine
      // from (stored in layerBase so category/boundary toggles re-apply), and a
      // native minzoom. The maxzoom cap (coarse band can't bleed into fine zoom)
      // is mirrored from the unbucketed path.
      const mk = (suffix, baseFilter, minzoom) => {
        const id = L.id + "@" + band.slug + suffix;
        layerBase[id] = baseFilter;
        // Register under the ORIGINAL base id for a *_scamin clone (L._baseId),
        // so every restyle/toggle keyed on the original id reaches the clone too.
        (variants[L._baseId || L.id] ||= []).push(id);
        const { _baseId, ...tmplL } = L; // _baseId is internal — keep it out of the MapLibre layer
        const v = { ...tmplL, id, source: "chart-" + band.slug, filter: S52.combineFilters(baseFilter, mariner), layout: _variantLayout(L, band.slug, id, bandsHidden, layerVis) };
        if (minzoom != null) v.minzoom = minzoom;
        if (capped) v.maxzoom = band.max;
        out.push(v);
      };
      const and = (extra) => (base ? ["all", base, extra] : extra);
      // SCAMIN buckets (point symbols / soundings): MapLibre's native fractional
      // layer minzoom does the exact-scale gating with ZERO per-zoom work — a
      // feature with SCAMIN 1:N shows precisely from display scale 1:N, in BOTH
      // directions, crossing bands down to that scale. One bucket per distinct
      // SCAMIN value (collected from the tiles). Out-of-zoom buckets are skipped by
      // MapLibre for free, so the extra layers cost nothing at runtime. Features
      // WITHOUT SCAMIN take the band-gated `#no` variant. Other layers: one variant.
      if (!ignoreScamin && SCAMIN_BUCKET_LAYERS.has(L["source-layer"]) && scaminValues && scaminValues.length) {
        // Only bucket SCAMIN values whose cutoff is ABOVE this band's display floor
        // (dmin) — values at/below dmin show from the floor anyway (the band isn't
        // displayed below it), so fold them into the dmin-floored `#no` bucket. Cuts
        // the layer count without quantizing (see the server path for the rationale).
        const floor = dmin || 0;
        const lowVals = [], hiVals = [];
        for (const sc of scaminValues) (scaminDisplayZoom(sc, lat, pxPitch) <= floor + 1e-6 ? lowVals : hiVals).push(sc);
        const noFilter = lowVals.length
          ? ["any", ["!", ["has", "scamin"]], ["in", ["get", "scamin"], ["literal", lowVals]]]
          : ["!", ["has", "scamin"]];
        mk("#no", and(noFilter), dmin || undefined);
        for (const sc of hiVals) {
          mk("#sm" + sc, and(["==", ["get", "scamin"], sc]), scaminDisplayZoom(sc, lat, pxPitch));
        }
      } else {
        mk("", base, dmin || undefined);
      }
      if (L.id === "areas") _pushOverscale(out, "chart-" + band.slug, band.slug, layerVis, undefined, bandsHidden, finerBandPresent(band.slug));
    }
    }
  }
  _pushScaminProbes(out, server);
  return { layers: out, layerBase, variants, layerVis };
}

