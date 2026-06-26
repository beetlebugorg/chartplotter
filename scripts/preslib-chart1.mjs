// Render every panel of the S-52 PresLib "ECDIS Chart 1" (PresLib e4.0.0 Part I
// §16, document pages 238–253) with OUR implementation, one PNG per reference
// page, so the result can be diffed against the spec's reference plots.
//
// The 14 ECDIS-Chart-1 cells tile the chart in a 4-wide grid; each cell IS one
// reference panel and its name encodes the panel letters (AA5C1CDE → panel
// "C,D,E"). The PANELS table below maps each reference page to the cell it
// portrays, framed at the cells' 1:14 000 compilation scale (≈ z14.2, where one
// cell fills the screen — the scale the legend was drawn for).
//
// Usage:
//   node scripts/preslib-chart1.mjs <baseURL> <outDir> [settleMs]
// The baseURL must already serve the imported Chart-1 pack (see
// scripts/preslib-chart1.sh, which sets that up, runs this, and tears it down).
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

// One reference page per row: the document page number (the figure to diff
// against), a slug for the filename, the cell center [lng,lat] + framing zoom,
// and the colour scheme. PANEL_Z frames a single ~3.3 km cell with ~15% margin
// on every side, so the panel clears the app chrome (status bar / controls) and
// nothing is clipped; the overview frames the whole 14-cell chart with margin.
const PANEL_Z = 13.9;
const PANELS = [
  { page: 238, slug: "overview",        c: [-5.0669, 15.0668], z: 12.0, scheme: "day" },
  { page: 239, slug: "info-AB1",        c: [-5.11545, 15.11405], z: PANEL_Z, scheme: "day" },
  { page: 240, slug: "info-AB2",        c: [-5.08295, 15.11405], z: PANEL_Z, scheme: "day" },
  { page: 241, slug: "natural-CDE",     c: [-5.05035, 15.11405], z: PANEL_Z, scheme: "day" },
  { page: 242, slug: "port-FOO",        c: [-5.01785, 15.11405], z: PANEL_Z, scheme: "day" },
  { page: 243, slug: "depths-HIO",      c: [-5.11545, 15.08250], z: PANEL_Z, scheme: "day" },
  { page: 244, slug: "seabed-JKL",      c: [-5.08295, 15.08250], z: PANEL_Z, scheme: "day" },
  { page: 245, slug: "traffic-MOO",     c: [-5.05035, 15.08250], z: PANEL_Z, scheme: "day" },
  { page: 246, slug: "special-NOO",     c: [-5.01785, 15.08250], z: PANEL_Z, scheme: "day" },
  { page: 247, slug: "aids-PRS",        c: [-5.11545, 15.05095], z: PANEL_Z, scheme: "day" },
  { page: 248, slug: "buoys-QO1",       c: [-5.08290, 15.05095], z: PANEL_Z, scheme: "day" },
  { page: 250, slug: "topmarks-QO2",    c: [-5.05030, 15.05095], z: PANEL_Z, scheme: "day" },
  { page: 251, slug: "newobj-vais-MNS", c: [-5.11545, 15.01940], z: PANEL_Z, scheme: "day" },
  { page: 252, slug: "colourtest-WOO-day",  c: [-5.01785, 15.05095], z: PANEL_Z, scheme: "day" },
  { page: 253, slug: "colourtest-WOO-dusk", c: [-5.01785, 15.05095], z: PANEL_Z, scheme: "dusk" },
];

mkdirSync(outDir, { recursive: true });
const { chromium } = findPlaywright();
const browser = await chromium.launch({ executablePath: findChromium(), args: ["--no-sandbox", "--hide-scrollbars"] });

for (const p of PANELS) {
  const page = await browser.newPage({ viewport: { width: 1000, height: 1000 }, deviceScaleFactor: 1 });
  page.on("pageerror", (e) => console.error(`[page ${p.page}]`, e.message));
  await page.addInitScript((a) => {
    localStorage.setItem("chartplotter:scheme", a.scheme);
    localStorage.setItem("chartplotter:basemap", "coastline");
    localStorage.setItem("chartplotter:enc-agreement", "1");
    localStorage.setItem("chartplotter:view", JSON.stringify({ center: a.c, zoom: a.z }));
  }, p);
  try { await page.goto(baseURL + "/?prod", { waitUntil: "domcontentloaded", timeout: 45000 }); }
  catch (e) { console.error(`[page ${p.page}] nav: ${e.message} — continuing`); }
  await page.waitForTimeout(+settle);
  const out = `${outDir}/page-${p.page}-${p.slug}.png`;
  await page.screenshot({ path: out });
  console.log(`wrote ${out}`);
  await page.close();
}
await browser.close();
