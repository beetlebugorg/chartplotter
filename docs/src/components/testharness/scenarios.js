// Scenario manifest for the docs test-harness page (TestHarness.js). Ported from
// the standalone harness's web/test-harness/scenarios.mjs, trimmed to the published
// VIEWER: no Node/extractor bits (REF_PDFS), and the S-64 §5/6/7 "detection" cells
// (nav hazards / special conditions / safety contour) are omitted — we don't claim to
// support those tests.
//
// Each scenario: { id, suite:"chart1"|"s64", title, b:[W,S,E,N], cscl,
//   scheme:"day"|"dusk", mariner:{…full settings}, pdf:"chart1"|"s64", refPage }.
// `mariner` is the COMPLETE settings object (applyMariner merges; we pass the whole
// thing). `refPage` is the reference-plot page in the matching PDF.

// Web-Mercator framing math (512-tile metres/px at z0, 1/96-inch CSS px).
export const M_PER_PX_Z0 = 78271.516964020485;
export const PX_PITCH_M = 0.00026458;
export const zoomForScale = (scale, lat) =>
  Math.log2((M_PER_PX_Z0 * Math.cos((lat * Math.PI) / 180)) / (PX_PITCH_M * scale));
export const spanPx = (metres, scale) => Math.max(1, Math.round(metres / scale / PX_PITCH_M));

// Center / zoom / pixel size of a scenario's cell at its compilation scale (so the
// cell fills a box sized width×height that we then CSS-scale to the pane).
export function framing(s) {
  const [w, sN, e, n] = s.b;
  const lat = (sN + n) / 2;
  const center = [(w + e) / 2, lat];
  const zoom = zoomForScale(s.cscl, lat);
  const lonM = (e - w) * 111320 * Math.cos((lat * Math.PI) / 180);
  const latM = (n - sN) * 110574;
  return { lat, center, zoom, width: spanPx(lonM, s.cscl), height: spanPx(latM, s.cscl) };
}

// ── Chart 1 (IHO PresLib "ECDIS Chart 1") ────────────────────────────────────
// One mariner state, ALL symbology shown — matches the PresLib reference plots.
export const CHART1_MARINER = {
  displayBase: true, displayStandard: true, displayOther: true,
  dataQuality: true,
  depthUnit: "m",
  showContourLabels: true,
  shallowContour: 5, safetyContour: 10, deepContour: 30,
  highlightDateDependent: true,
  dateDependent: false,
  showMetaBounds: true,
  showFullSectorLines: false,
  boundaryStyle: "symbolized",
  simplifiedPoints: false,
};
const c1merge = (o) => ({ ...CHART1_MARINER, ...o });

const HARBOR = 14000, OVERVIEW = 60000;
const CHART1_PANELS = [
  { page: 238, slug: "overview",        b: [-5.135803, 15.00018, -4.997983, 15.133311], cscl: OVERVIEW, scheme: "day", mariner: { showInformCallouts: true, dataQuality: false } },
  { page: 239, slug: "info-AB1",        b: [-5.1307, 15.0993, -5.1002, 15.1288], cscl: HARBOR, scheme: "day" },
  { page: 240, slug: "info-AB2",        b: [-5.0982, 15.0993, -5.0677, 15.1288], cscl: HARBOR, scheme: "day" },
  { page: 241, slug: "natural-CDE",     b: [-5.0656, 15.0992, -5.0351, 15.1288], cscl: HARBOR, scheme: "day" },
  { page: 242, slug: "port-FOO",        b: [-5.0331, 15.0993, -5.0026, 15.1288], cscl: HARBOR, scheme: "day" },
  { page: 243, slug: "depths-HIO",      b: [-5.1307, 15.0677, -5.1002, 15.0973], cscl: HARBOR, scheme: "day" },
  { page: 244, slug: "seabed-JKL",      b: [-5.0982, 15.0677, -5.0677, 15.0973], cscl: HARBOR, scheme: "day" },
  { page: 245, slug: "traffic-MOO",     b: [-5.0656, 15.0677, -5.0351, 15.0973], cscl: HARBOR, scheme: "day" },
  { page: 246, slug: "special-NOO",     b: [-5.0331, 15.0677, -5.0026, 15.0973], cscl: HARBOR, scheme: "day" },
  { page: 247, slug: "aids-PRS",        b: [-5.1307, 15.0362, -5.1002, 15.0657], cscl: HARBOR, scheme: "day" },
  { page: 248, slug: "buoys-QO1",       b: [-5.0982, 15.0362, -5.0676, 15.0657], cscl: HARBOR, scheme: "day" },
  { page: 250, slug: "topmarks-QO2",    b: [-5.0656, 15.0362, -5.0350, 15.0657], cscl: HARBOR, scheme: "day" },
  { page: 251, slug: "newobj-vais-MNS", b: [-5.1307, 15.0046, -5.1002, 15.0342], cscl: HARBOR, scheme: "day", mariner: { showInformCallouts: true } },
  { page: 252, slug: "colourtest-WOO-day",  b: [-5.0331, 15.0362, -5.0026, 15.0657], cscl: HARBOR, scheme: "day" },
  { page: 253, slug: "colourtest-WOO-dusk", b: [-5.0331, 15.0362, -5.0026, 15.0657], cscl: HARBOR, scheme: "dusk" },
];

// ── S-64 (IHO ENC test dataset) — VIEWER tests only ──────────────────────────
export const S64_MARINER = {
  displayBase: true, displayStandard: true, displayOther: true,
  dataQuality: true, depthUnit: "m",
  shallowContour: 2, safetyContour: 10, safetyDepth: 10, deepContour: 30,
  showFullSectorLines: false, boundaryStyle: "symbolized", simplifiedPoints: false,
};
const s64merge = (o) => ({ ...S64_MARINER, ...o });

// Reset baseline applied before each scenario's overrides (applyMariner MERGES, so a
// key one scenario sets and the next omits would otherwise bleed across the one
// long-lived widget). Mirrors the widget's DEFAULT_MARINER.
export const HARNESS_MARINER_RESET = {
  displayBase: true, displayStandard: true, displayOther: false,
  dataQuality: false, depthUnit: "ft",
  shallowContour: 2, safetyContour: 10, safetyDepth: 10, deepContour: 30,
  showContourLabels: false, highlightDateDependent: false, dateDependent: true,
  showMetaBounds: false, showFullSectorLines: false, showInformCallouts: false,
  boundaryStyle: "symbolized", simplifiedPoints: false,
};
export const effectiveMariner = (s) => ({ ...HARNESS_MARINER_RESET, ...s.mariner });

// Only the S-64 cells with a comparable full-cell reference SCREEN PLOT are kept:
// §3.1 Display (Base/Standard/Other) and §3.2 Invalid Objects. The behavioural /
// procedural tests (§2.1.1 power-up, §3.3 settings, §3.4 non-official, §3.6 display
// priorities, §3.7 overlap, §3.7.7 scale-minimum) have nothing to diff a render
// against, and IC3NEWPC isn't in the reference PDF at all — all dropped. The §5/6/7
// "detection" cells are unsupported; §3.9 Polar / §3.8.5 AML / §4 were never in scope.
const S64_PAGES = [
  // §3.1 ENC Display — same area at the three display categories (every object class).
  { section: "3.1 Base",     slug: "AA5DBASE", b: [9.833, 10.0, 10.0, 10.167],    cscl: 60000, refPage: 100, mariner: s64merge({ displayStandard: false, displayOther: false, dataQuality: false }) },
  { section: "3.1 Standard", slug: "AA5STNDR", b: [10.0, 10.0, 10.167, 10.167],   cscl: 70000, refPage: 101, mariner: s64merge({ displayOther: false, dataQuality: false }) },
  { section: "3.1 Other",    slug: "AA5OTHER", b: [10.167, 10.0, 10.333, 10.167], cscl: 60000, refPage: 103, mariner: s64merge({}) },
  // §3.2 Invalid objects (unknown class / attribute-driven special presentation).
  { section: "3.2 InvalidObject", slug: "AA3INVOB", b: [-104.75, 39.333, -104.5, 39.5], cscl: 50000, refPage: 107, mariner: s64merge({}) },
];

// ── Unified manifest ─────────────────────────────────────────────────────────
const slugify = (s) => s.replace(/[^a-z0-9]+/gi, "_").replace(/^_|_$/g, "");

export const SCENARIOS = [
  ...CHART1_PANELS.map((p) => ({
    id: `c1-${p.page}-${slugify(p.slug)}`,
    suite: "chart1",
    title: `Chart 1 · p${p.page} · ${p.slug}`,
    b: p.b, cscl: p.cscl, scheme: p.scheme,
    mariner: c1merge(p.mariner || {}),
    // The PresLib PDF's printed pages run +9 vs its internal index (front matter).
    pdf: "chart1", refPage: p.page + 9,
    slug: p.slug,
  })),
  ...S64_PAGES.map((p) => ({
    id: `s64-${slugify(p.section)}-${p.slug}`,
    suite: "s64",
    title: `S-64 · §${p.section} · ${p.slug}`,
    b: p.b, cscl: p.cscl, scheme: "day",
    mariner: p.mariner,
    pdf: "s64", refPage: p.refPage,
    slug: p.slug,
  })),
];

// Reference PDFs by `pdf` key, repo-root-relative. Used ONLY by the Node ref-plot
// extractor (docs/scripts/extract-refs.mjs); ignored in the browser bundle.
export const REF_PDFS = {
  chart1: "../chartplotter-specs/s52/specs/pslb04_0_part1.pdf",
  s64: "testdata/S-64 Ed 3.0.3_EN_Clean_Final.pdf",
};
