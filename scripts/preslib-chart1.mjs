// Render every panel of the S-52 PresLib "ECDIS Chart 1" (PresLib e4.0.0 Part I
// §16, document pages 238–253) with OUR implementation, one PNG per reference
// page, for diffing against the spec's reference plots.
//
// Each of the 14 ECDIS-Chart-1 cells IS one reference panel (its name encodes the
// panel letters, e.g. AA5C1CDE → "C,D,E"). The PANELS table maps each reference
// page to its cell BOUNDS + compilation scale. We render in "spec mode" (?spec —
// no app chrome) and size the window so the cell fills it AT ITS COMPILATION SCALE
// (1:14 000 for the harbor pages, 1:60 000 for the overview): viewport_px =
// ground_metres / scale / pixel_pitch. So each page is captured full-screen at the
// scale the legend was drawn for, exactly like the reference figure.
//
// Usage: node scripts/preslib-chart1.mjs <baseURL> <outDir> [settleMs]
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

const [baseURL = "http://127.0.0.1:8101", outDir = "/tmp/preslib-chart1", settle = "8000"] = process.argv.slice(2);

// Display geometry, shared with the app (web/src/lib/util.mjs): the 512-tile
// metres-per-pixel at z0 and the 1/96-inch CSS reference pixel.
const M_PER_PX_Z0 = 78271.516964020485;
const PX_PITCH_M = 0.00026458;
const zoomForScale = (scale, lat) => Math.log2(M_PER_PX_Z0 * Math.cos((lat * Math.PI) / 180) / (PX_PITCH_M * scale));
const spanPx = (metres, scale) => Math.max(1, Math.round(metres / scale / PX_PITCH_M));

// page → cell: bounds [W,S,E,N] + compilation scale (CSCL). Bounds are the cells'
// data extents (AA4C1XMS = the 1:60 000 overview; AA5C1* = 1:14 000 harbor pages).
const HARBOR = 14000, OVERVIEW = 60000;
const PANELS = [
  { page: 238, slug: "overview",        b: [-5.135803, 15.00018, -4.997983, 15.133311], cscl: OVERVIEW, scheme: "day" },
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
  { page: 251, slug: "newobj-vais-MNS", b: [-5.1307, 15.0046, -5.1002, 15.0342], cscl: HARBOR, scheme: "day" },
  { page: 252, slug: "colourtest-WOO-day",  b: [-5.0331, 15.0362, -5.0026, 15.0657], cscl: HARBOR, scheme: "day" },
  { page: 253, slug: "colourtest-WOO-dusk", b: [-5.0331, 15.0362, -5.0026, 15.0657], cscl: HARBOR, scheme: "dusk" },
];

// Mariner display state matching the IHO PresLib reference plots: ALL symbology
// shown. Display category Other on (INFORM01 callouts, "other marks", magnetic
// variation…); data-quality overlay on (the CATZOC "quality of data" panels);
// metres (IHO depths, not NOAA feet); 25 mm short sector legs and symbolized
// boundaries (both the S-52 defaults the reference uses). Everything else
// (soundings, text groups, light descriptions) is default-on.
const MARINER = {
  displayBase: true, displayStandard: true, displayOther: true,
  dataQuality: true,
  depthUnit: "m",
  showContourLabels: true, // spec plots label every depth contour (the "0" drying line, "5", "10", "30")
  shallowContour: 5, safetyContour: 10, deepContour: 30, // the depth-shading demo's contours (DEPCNT VALDCO 0/5/10/30)
  highlightDateDependent: true, // show the CHDATD01 "d" markers (the date-dependency demo)
  dateDependent: false, // date filter off so the expired "End date 27-08-2014" object still shows
  showMetaBounds: true, // show meta-object boundaries — M_NSYS navigational-system-of-marks demo (page 248)
  showFullSectorLines: false,
  boundaryStyle: "symbolized",
  simplifiedPoints: false,
};

mkdirSync(outDir, { recursive: true });
const { chromium } = findPlaywright();
const browser = await chromium.launch({ executablePath: findChromium(), args: ["--no-sandbox", "--hide-scrollbars"] });

for (const p of PANELS) {
  const [w, s, e, n] = p.b;
  const lat = (s + n) / 2;
  const center = [(w + e) / 2, lat];
  const zoom = zoomForScale(p.cscl, lat);
  // Window sized to the cell at its compilation scale → the page fills it.
  const lonM = (e - w) * 111320 * Math.cos((lat * Math.PI) / 180);
  const latM = (n - s) * 110574;
  const width = spanPx(lonM, p.cscl), height = spanPx(latM, p.cscl);

  const page = await browser.newPage({ viewport: { width, height }, deviceScaleFactor: 1 });
  page.on("pageerror", (err) => console.error(`[page ${p.page}]`, err.message));
  await page.addInitScript((a) => {
    localStorage.setItem("chartplotter:scheme", a.scheme);
    localStorage.setItem("chartplotter:basemap", "coastline");
    localStorage.setItem("chartplotter:enc-agreement", "1");
    localStorage.setItem("chartplotter:mariner", JSON.stringify(a.mariner));
    localStorage.setItem("chartplotter:view", JSON.stringify({ center: a.center, zoom: a.zoom }));
  }, { scheme: p.scheme, center, zoom, mariner: MARINER });
  try { await page.goto(baseURL + "/?prod&spec", { waitUntil: "domcontentloaded", timeout: 45000 }); }
  catch (err) { console.error(`[page ${p.page}] nav: ${err.message} — continuing`); }
  await page.waitForTimeout(+settle);
  const out = `${outDir}/page-${p.page}-${p.slug}.png`;
  await page.screenshot({ path: out });
  console.log(`wrote ${out} (${width}x${height} @ 1:${p.cscl})`);
  await page.close();
}
await browser.close();
