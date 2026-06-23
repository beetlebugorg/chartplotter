// s52-style.mjs — pure S-52 style / palette / filter helpers.
//
// These are the colour-token resolvers, SEABED01 / depth shading, and the
// client-side display-portrayal filters (display category, boundary style,
// point style, sector-leg length, text groups, …) extracted VERBATIM from
// <chart-canvas>. They were instance methods that read `this._colortables
// [this._active]` (the active scheme's palette), `this._active` (the scheme
// name), and `this._mariner` (the settings object); here those become explicit
// `palette` / `active` / `mariner` parameters so the functions are pure.
// <chart-canvas> keeps thin delegators that pass `this._palette()` / `this.
// _active` / `this._mariner` in, so no external call site changes.

export const FALLBACK = "#ff00ff";
export const FONT = ["Noto Sans Regular"];
export const M_TO_FT = 3.280839895; // depth-unit conversion (metric ↔ imperial)
// S-57 meta objects whose boundary draws as a region/coverage line (nautical
// publication, nav-system, coverage, compilation scale). These are administrative
// indicators (S-52 PresLib gives M_NPUB a line only as a pick-report hint); they
// read as "cell boundaries", so they get their own gate (mariner.showMetaBounds),
// off by default, rather than riding the "Other" display category. M_QUAL is NOT
// here — it has its own "Data quality" (CATZOC) toggle.
export const META_BOUND_CLASSES = ["M_NPUB", "M_NSYS", "M_COVR", "M_CSCL"];

// -- colour --------------------------------------------------------------
// Resolve a single S-52 colour token for the active scheme (concrete value,
// not an expression) — used for basemap layers whose colour is fixed.
export function token(name, fallback, palette) {
  return (palette || {})[name] || fallback;
}
export function seaColor(palette) { return token("DEPDW", "#93aebb", palette); }   // deep water / sea backdrop
export function landColor(palette) { return token("LANDA", "#e0d9b8", palette); }  // S-52 land area
export function coastColor(palette) { return token("CSTLN", "#5a5a44", palette); } // coastline stroke

export function colorExpr(prop, fallback, palette) {
  return colorMatch(["coalesce", ["get", prop], ""], fallback, palette);
}

// Resolve a colour-token-valued expression to an RGB for the active scheme.
export function colorMatch(tokenExpr, fallback, palette) {
  const t = palette || {};
  const m = ["match", tokenExpr];
  let n = 0;
  for (const tok in t) { m.push(tok, t[tok]); n++; }
  m.push(fallback || FALLBACK);
  return n ? m : (fallback || FALLBACK);
}

// Legible chart-text colour. S-52's dusk/night palettes dim the text inks
// (CHBLK/CHGRD) to near-black, which is unreadable on the equally dark scheme
// — a halo can't help because the glyph *body* itself vanishes. So at
// dusk/night we render text in a bright neutral (legibility over strict
// night-vision dimming, per user request) and pair it with a dark halo
// (textHaloColor). Day keeps the per-feature S-52 ink (so coloured labels
// stay semantic) over a light halo.
export function textColor(active, palette) {
  if (active === "day") return colorExpr("color_token", "#000000", palette);
  return active === "night" ? "#aab7bf" : "#dde7ec";
}
// Backing that contrasts with textColor: light under day's dark inks, dark
// under the bright dusk/night ink. Applied to ALL text — the old bake gated
// the halo to ≥10 px glyphs, leaving small labels bare.
export function textHaloColor(active) {
  return active === "day" ? "rgba(255,255,255,0.9)" : "rgba(0,0,0,0.85)";
}
// Contour (depth) labels: S-52 CHGRD by day, bright neutral at dusk/night so
// they stay legible like the rest of the chart text.
export function contourLabelColor(active, palette) {
  if (active === "day") return token("CHGRD", "#5a5a44", palette);
  return active === "night" ? "#aab7bf" : "#dde7ec";
}

// SEABED01 (S-52 §13.2.15) as a data-driven expression: a depth area's
// DRVAL1/DRVAL2 vs the mariner's shallow/safety/deep contours → a depth
// colour token. Done client-side so dragging the contours is an instant
// restyle, not a re-bake. Deepest band first (the spec cascade's last match
// wins → first match in a `case`). `>= X && > X` on both bounds per the spec.
export function seabedTokenExpr(mariner) {
  const m = mariner;
  const shc = m.shallowContour ?? 2, sfc = m.safetyContour ?? 10, dpc = m.deepContour ?? 30;
  const d1 = ["coalesce", ["get", "drval1"], -1];
  const d2 = ["coalesce", ["get", "drval2"], 0];
  const band = (x) => ["all", [">=", d1, x], [">", d2, x]];
  if (m.fourShadeWater === false) {
    return ["case", band(sfc), "DEPDW", band(0), "DEPVS", "DEPIT"];
  }
  return ["case",
    band(dpc), "DEPDW",
    band(sfc), "DEPMD",
    band(shc), "DEPMS",
    band(0), "DEPVS",
    "DEPIT"];
}

// Fill colour for the `areas` layer: depth areas (carry drval1) shade live via
// SEABED01; everything else uses its baked colour token.
export function areasFillColor(palette, mariner) {
  return ["case",
    ["has", "drval1"], colorMatch(seabedTokenExpr(mariner), undefined, palette),
    colorExpr("color_token", undefined, palette)];
}

// SHALLOW_PATTERN filter: depth areas on the shallow side of the live safety
// contour — SEABED01's SHALLOW flag, i.e. NOT (drval1 ≥ SFC && drval2 > SFC).
export function shallowPatternFilter(mariner) {
  const sfc = mariner.safetyContour ?? 10;
  return ["all",
    ["has", "drval1"],
    ["!", ["all", [">=", ["get", "drval1"], sfc], [">", ["coalesce", ["get", "drval2"], ["get", "drval1"]], sfc]]]];
}

// Safety-contour line (DEPARE03, client-side): the DEPSC-emphasised edge is
// approximated by the outline of any depth area whose [DRVAL1, DRVAL2) range
// straddles the live safety contour (drval1 < SFC ≤ drval2) — the same
// area-level approximation the engine used to bake, now a filter so moving
// the safety contour restyles instantly with no re-bake.
export function safetyContourFilter(mariner) {
  const sfc = mariner.safetyContour ?? 10;
  return ["all",
    ["has", "drval1"],
    ["<", ["get", "drval1"], sfc],
    [">=", ["coalesce", ["get", "drval2"], ["get", "drval1"]], sfc]];
}

// SAFCON01 (S-52 §13.2.13): the depth-contour value label. Drawn client-side
// along DEPCNT lines from the baked VALDCO (whole metres, or whole feet when
// the mariner picks imperial units), shown only when "contour labels" is on.
export function contourLabelField(mariner) {
  const v = mariner.depthUnit === "ft"
    ? ["round", ["*", ["get", "valdco"], M_TO_FT]]
    : ["round", ["get", "valdco"]];
  return ["case", ["has", "valdco"], ["to-string", v], ""];
}

// SNDFRM04 (S-52 §13.2.16): a sounding ≤ the live safety depth uses the bold
// SOUNDS glyphs, else the faint SOUNDG glyphs — picked client-side from the
// baked depth + both name variants. Falls back to the baked names if a tile
// predates the variants. In imperial mode the metres glyphs can't be reused
// (the number changes), so synthesize a `snd:` image name from the numeric
// depth + palette; `registerImage` builds the converted glyph composite.
export function soundingsIconImage(mariner) {
  const sd = mariner.safetyDepth ?? 10;
  if (mariner.depthUnit === "ft") {
    const pal = ["case", ["<=", ["coalesce", ["get", "depth"], 0], sd], "S", "G"];
    // Key by deci-metres (a stable integer) so MapLibre caches one image per
    // distinct depth/palette rather than per float-string.
    const dm = ["to-string", ["round", ["*", ["coalesce", ["get", "depth"], 0], 10]]];
    return ["case", ["has", "depth"], ["concat", "snd:ft:", pal, ":", dm], ["get", "symbol_names"]];
  }
  return ["case",
    ["has", "sym_s"],
    ["case", ["<=", ["coalesce", ["get", "depth"], 0], sd], ["get", "sym_s"], ["get", "sym_g"]],
    ["get", "symbol_names"]];
}

// OBSTRN06/WRECKS05 (S-52 §13.2.6/§13.2.20): a danger symbol carries its
// VALSOU + the deep-water variant. The baked `symbol_name` is the dangerous
// (DANGER01) variant; when the depth is DEEPER than the live safety contour
// swap to the less-prominent `sym_deep` (DANGER02). Picked client-side so the
// safety contour no longer re-bakes. Non-danger symbols use `symbol_name`.
export function pointSymbolImage(mariner) {
  const sfc = mariner.safetyContour ?? 10;
  return ["case",
    ["all", ["has", "sym_deep"], [">", ["coalesce", ["get", "danger_depth"], 0], sfc]],
    ["get", "sym_deep"],
    ["get", "symbol_name"]];
}

// The dotted CHBLK foul boundary (OBSTRN/WRECKS) is shown only where the
// feature's VALSOU is at/above the live safety contour — a danger.
export function dangerBoundaryFilter(mariner) {
  const sfc = mariner.safetyContour ?? 10;
  return ["all", ["has", "danger_depth"], ["<=", ["get", "danger_depth"], sfc]];
}

// Display category (S-52 §10.3.4), client-side + MULTI-SELECT: every feature
// is baked with its category rank `cat` (0=base,1=standard,2=other); the
// mariner independently toggles each, so this is a membership test, not a
// cumulative level. Missing `cat` (stale tile) defaults to standard.
export function categoryFilter(mariner) {
  const m = mariner;
  const en = [];
  if (m.displayBase !== false) en.push(0);
  if (m.displayStandard !== false) en.push(1);
  if (m.displayOther === true) en.push(2);
  // Isolated dangers (ISODGR01, S-52 UDWHAZ05): the mariner picks their display
  // category — DisplayBase (0, always shown; the default) or, when "isolated
  // dangers in shallow water" is on, Standard (1). The symbol is the marker;
  // VALSOU dangers became DANGER01 (live danger_depth swap), so ISODGR01 here
  // is exactly the isolated-danger set. Every other feature uses its baked cat.
  const isoCat = m.showIsolatedDangersShallow ? 1 : 0;
  const cat = ["case", ["==", ["get", "symbol_name"], "ISODGR01"], isoCat, ["coalesce", ["get", "cat"], 1]];
  const inCat = ["in", cat, ["literal", en]];
  // The M_QUAL data-quality overlay (CATZOC DQUAL* area patterns + boundary)
  // is baked display-category Other, so enabling Other dumped it on top of
  // everything — too cluttered. Decouple it into its own `dataQuality` toggle:
  // quality features show IFF dataQuality (independent of Other), and are
  // excluded from the normal category membership so Other no longer carries it.
  const isQual = ["==", ["get", "class"], "M_QUAL"];
  return m.dataQuality
    ? ["any", isQual, ["all", inCat, ["!", isQual]]]
    : ["all", inCat, ["!", isQual]];
}

// Boundary symbolization (S-52 §8.6.1), client-side: each primitive is baked
// with a `bnd` tag — 2 = style-independent (always shown), 0 = plain-boundary
// only, 1 = symbolized-boundary only. Show common (2) + the active style.
// Missing `bnd` (non-area / stale tile) defaults to common. Default to
// SYMBOLIZED (rank 1) per the IMO/S-52 default (the engine also bakes
// SymbolizedBoundaries=true by default); plain only when explicitly chosen.
// Symbolized is the variant that carries the embedded LC line symbols (e.g.
// RESARE's EMAREMG1), so a plain default hid every complex-line symbol.
export function boundaryFilter(mariner) {
  const rank = mariner.boundaryStyle === "plain" ? 0 : 1;
  return ["in", ["coalesce", ["get", "bnd"], 2], ["literal", [2, rank]]];
}

// Point-symbol style (S-52 §11.2.2), client-side: point features that resolve
// differently under the simplified vs paper-chart LUP tables are baked twice,
// tagged `pts` — 2 = style-independent (always shown), 0 = paper-chart, 1 =
// simplified. Show common (2) + the active style. Missing `pts` (non-point /
// identical-in-both / stale tile) defaults to common. Default PAPER (rank 0)
// per the engine default (SimplifiedPoints=false).
export function pointStyleFilter(mariner) {
  const rank = mariner.simplifiedPoints ? 1 : 0;
  return ["in", ["coalesce", ["get", "pts"], 2], ["literal", [2, rank]]];
}

// Light sector leg length (S-52 LIGHTS06 note 1), client-side: each sector
// light's legs are baked twice, tagged `sleg` — 0 = the 25 mm short leg
// (default, avoids clutter), 1 = the full VALNMR nominal-range leg. Arcs/rings
// are untagged (coalesce 2 → always shown). Show common (2) + the active
// length. Default SHORT (rank 0) per the engine (ShowFullLengthSectorLines=false).
export function sectorLegFilter(mariner) {
  const rank = mariner.showFullSectorLines ? 1 : 0;
  return ["in", ["coalesce", ["get", "sleg"], 2], ["literal", [2, rank]]];
}

// Combine a layer's intrinsic (base) filter with the live category +
// boundary-style filters (the two client-side portrayal axes baked as
// per-feature `cat`/`bnd`).
export function combineFilters(base, mariner) {
  const parts = ["all", categoryFilter(mariner), boundaryFilter(mariner), pointStyleFilter(mariner), sectorLegFilter(mariner)];
  // Meta-object coverage/region boundary lines are gated separately from the
  // "Other" display category (mariner.showMetaBounds, off by default), since
  // they read as cell boundaries and aren't useful alongside other "Other" data.
  if (!mariner.showMetaBounds) parts.push(["!", ["in", ["get", "class"], ["literal", META_BOUND_CLASSES]]]);
  if (base) parts.push(base);
  return parts;
}

// S-52 PresLib §14.5 text-group selection. Each text feature carries the baked
// `tgrp` tag (the DISPLAY param of its TX/TE, §14.4); the mariner toggles which
// groups are visible, independent of display category. Returns a MapLibre filter
// expression selecting the enabled groups (false = hide all). Light descriptions
// (group 23) are the LIGHTS layer's own toggle (showLightDescriptions); a stray
// non-light group-23 label is folded in here too.
export function textGroupFilter(mariner) {
  const m = mariner;
  const g = ["coalesce", ["get", "tgrp"], -1];
  const named = ["match", g, [21, 26, 29], true, false]; // §14.5 Names
  const clauses = [];
  if (m.textImportant !== false) clauses.push(["==", g, 11]);     // §14.5 Important text
  if (m.textNames !== false) clauses.push(named);
  if (m.showLightDescriptions !== false) clauses.push(["==", g, 23]); // Light description
  // Other: everything not already claimed above (incl. missing tgrp = -1, so
  // text in tiles baked before tgrp existed stays visible when "Other" is on).
  if (m.textOther !== false) clauses.push(["all", ["!=", g, 11], ["!=", g, 23], ["match", g, [21, 26, 29], false, true]]);
  return clauses.length ? ["any", ...clauses] : false;
}

// Raster-paint adjustment for the OSM basemap per active colour scheme. The
// public OSM tiles are a bright daytime street map; at dusk/night we dim and
// desaturate them (marine night-vision) so the underlay doesn't blow out the
// dark S-52 palette. Day = identity. All four keys are always returned so
// setScheme can restore defaults when switching back to day.
export function osmRasterPaint(active) {
  if (active === "night") return { "raster-brightness-max": 0.32, "raster-saturation": -0.55, "raster-contrast": 0.08, "raster-opacity": 0.9 };
  if (active === "dusk") return { "raster-brightness-max": 0.66, "raster-saturation": -0.3, "raster-contrast": 0, "raster-opacity": 1 };
  return { "raster-brightness-max": 1, "raster-saturation": 0, "raster-contrast": 0, "raster-opacity": 1 };
}
