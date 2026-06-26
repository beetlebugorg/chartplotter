// Render the IHO S-64 ENC test dataset's rendering pages with OUR implementation,
// one PNG per test, for diffing against the S-64 reference plots (testdata/"S-64
// Ed 3.0.3_EN_Clean_Final.pdf"). Mirrors scripts/preslib-chart1.mjs: spec mode
// (chrome-free), each page framed to its cell at the cell's compilation scale.
//
// Unlike PresLib Chart 1 (one all-symbology plot), S-64 tests vary the MARINER
// settings per test — most importantly §3.1 ENC Display renders the SAME area at
// the Base / Standard / Other display categories. The S-64 setup uses safety
// contour 10 m / safety depth 10 m, symbolized boundaries, metres.
//
// Usage: node scripts/s64-pages.mjs <baseURL> <outDir> [settleMs]
import { createRequire } from "node:module";
import { execSync } from "node:child_process";
import { mkdirSync } from "node:fs";
const require = createRequire(import.meta.url);
function findPlaywright() {
  try { return require("playwright-core"); } catch {}
  const root = execSync("npm root -g", { encoding: "utf8" }).trim();
  return require(`${root}/promptfoo/node_modules/playwright-core`);
}
function findChromium() {
  for (const p of ["/usr/bin/chromium", "/usr/bin/chromium-browser", "/usr/bin/google-chrome", "/usr/bin/chrome"]) {
    try { execSync(`test -x ${p}`); return p; } catch {}
  }
  return undefined;
}

const [baseURL = "http://127.0.0.1:8101", outDir = "/tmp/s64-pages", settle = "8000"] = process.argv.slice(2);

const M_PER_PX_Z0 = 78271.516964020485;
const PX_PITCH_M = 0.00026458;
const zoomForScale = (scale, lat) => Math.log2(M_PER_PX_Z0 * Math.cos((lat * Math.PI) / 180) / (PX_PITCH_M * scale));
const spanPx = (metres, scale) => Math.max(1, Math.round(metres / scale / PX_PITCH_M));

// S-64 standard mariner setup: safety contour/depth 10 m, symbolized boundaries,
// metres, all categories on (per-page overrides below). Mirrors DEFAULT_MARINER keys.
const S64 = {
  displayBase: true, displayStandard: true, displayOther: true,
  dataQuality: true, depthUnit: "m",
  shallowContour: 2, safetyContour: 10, safetyDepth: 10, deepContour: 30,
  showFullSectorLines: false, boundaryStyle: "symbolized", simplifiedPoints: false,
};
const merge = (o) => ({ ...S64, ...o });

// One page per row: the S-64 test section (the reference figure to diff against),
// a slug, the cell's bounds [W,S,E,N] + compilation scale (from parsing the cells),
// the colour scheme, and the mariner overrides for that test.
const HARBOR = 25000;
const PAGES = [
  // §3.1 ENC Display — same area at the three display categories (every object class).
  { section: "3.1 Base",     slug: "AA5DBASE", b: [9.833, 10.0, 10.0, 10.167],   cscl: 60000, mariner: merge({ displayStandard: false, displayOther: false, dataQuality: false }) },
  { section: "3.1 Standard", slug: "AA5STNDR", b: [10.0, 10.0, 10.167, 10.167],  cscl: 70000, mariner: merge({ displayOther: false, dataQuality: false }) },
  { section: "3.1 Other",    slug: "AA5OTHER", b: [10.167, 10.0, 10.333, 10.167], cscl: 60000, mariner: merge({}) },
  // §3.6 Display Priorities (overlapping object draw order).
  { section: "3.6 DisplayPriorities", slug: "2J5X0001", b: [61.333, -32.375, 61.4, -32.333], cscl: HARBOR, mariner: merge({}) },
  // §3.7 Overlap / §3.7.7 Scale minimum / §3.3 Settings / §3.2 Invalid object.
  { section: "3.7 Overlap",      slug: "GB3OVRLP", b: [60.6, -32.5, 61.1, -32.2],     cscl: 90000, mariner: merge({}) },
  { section: "3.7.7 ScaleMin",   slug: "AA3SCAMN", b: [60.267, -32.633, 60.767, -32.317], cscl: 90000, mariner: merge({}) },
  { section: "3.3 Settings",     slug: "GB4X0001", b: [61.333, -32.633, 61.5, -32.317], cscl: 52000, mariner: merge({}) },
  { section: "3.2 InvalidObject", slug: "AA3INVOB", b: [-104.75, 39.333, -104.5, 39.5], cscl: 50000, mariner: merge({}) },
  // §3.4 Non-official data (new producer codes).
  { section: "3.4 NonOfficial",  slug: "1B5X01NE", b: [60.967, -32.533, 61.0, -32.45], cscl: HARBOR, mariner: merge({}) },
  { section: "3.4 NewProducer",  slug: "IC3NEWPC", b: [60.0, -30.5, 60.1, -30.4],    cscl: 90000, mariner: merge({}) },
  // §2.1.1 Power Up — the GB region overview (band-4 cell covering the GB5X tiles).
  { section: "2.1.1 PowerUp",    slug: "GB4X0000", b: [60.767, -32.633, 61.333, -32.317], cscl: 52000, mariner: merge({}) },
  // §5/6/7 detection tests (Colorado): nav hazards, special conditions, safety contour.
  { section: "5.0 NavHazards",   slug: "AA3NAVHZ", b: [-105.0, 39.833, -104.75, 40.0], cscl: 75000, mariner: merge({}) },
  { section: "5.0 Overview",     slug: "AA2OVRVU", b: [-105.5, 39.167, -104.167, 40.167], cscl: 350000, mariner: merge({}) },
  { section: "6.0 SpecialConditions", slug: "AA3ARSPC", b: [-105.0, 39.667, -104.75, 39.833], cscl: 90000, mariner: merge({}) },
  { section: "7.0 SafetyContour", slug: "AA3SAFCO", b: [-105.0, 39.5, -104.75, 39.667], cscl: 90000, mariner: merge({}) },
];
// NOTE: §3.9 Polar (AA1NPOL*) is omitted — those cells sit at 85–90°N, beyond the
// Web-Mercator limit, so they can't be displayed. §3.8.5 AML non-ENC cells are
// omitted — their long underscored names aren't ENC cell names (not baked).

mkdirSync(outDir, { recursive: true });
const { chromium } = findPlaywright();
const browser = await chromium.launch({ executablePath: findChromium(), args: ["--no-sandbox", "--hide-scrollbars"] });

for (const p of PAGES) {
  const [w, s, e, n] = p.b;
  const lat = (s + n) / 2;
  const center = [(w + e) / 2, lat];
  const zoom = zoomForScale(p.cscl, lat);
  const lonM = (e - w) * 111320 * Math.cos((lat * Math.PI) / 180);
  const latM = (n - s) * 110574;
  const width = spanPx(lonM, p.cscl), height = spanPx(latM, p.cscl);

  const page = await browser.newPage({ viewport: { width, height }, deviceScaleFactor: 1 });
  page.on("pageerror", (err) => console.error(`[${p.section}]`, err.message));
  await page.addInitScript((a) => {
    localStorage.setItem("chartplotter:scheme", "day");
    localStorage.setItem("chartplotter:basemap", "coastline");
    localStorage.setItem("chartplotter:enc-agreement", "1");
    localStorage.setItem("chartplotter:mariner", JSON.stringify(a.mariner));
    localStorage.setItem("chartplotter:view", JSON.stringify({ center: a.center, zoom: a.zoom }));
  }, { mariner: p.mariner, center, zoom });
  try { await page.goto(baseURL + "/?prod&spec", { waitUntil: "domcontentloaded", timeout: 45000 }); }
  catch (err) { console.error(`[${p.section}] nav: ${err.message} — continuing`); }
  await page.waitForTimeout(+settle);
  const file = `${outDir}/${p.section.replace(/[ .]/g, "_")}-${p.slug}.png`;
  await page.screenshot({ path: file });
  console.log(`wrote ${file} (${width}x${height} @ 1:${p.cscl})`);
  await page.close();
}
await browser.close();
