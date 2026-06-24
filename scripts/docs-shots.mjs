// Reproducible, high-resolution screenshots for the documentation.
//
// Drives the live <chart-plotter> UI through its public API (setView,
// applyScheme, toggleSection) so every shot frames the SAME view and panel
// state each run — re-run this whenever the UI changes and the docs images
// regenerate. Captures at deviceScaleFactor 2 (retina) for crisp docs.
//
// It expects a chartplotter server already running with baked charts in its
// cache. `make docs-shots` starts one for you; to run by hand:
//   chartplotter serve --assets web --cache ~/.cache/chartplotter/s101 &
//   node scripts/docs-shots.mjs
//
// Usage: node scripts/docs-shots.mjs [baseURL] [outDir]
//   baseURL  default http://127.0.0.1:8080
//   outDir   default docs/static/img/ui

import { createRequire } from "node:module";
import { execSync } from "node:child_process";
import { mkdirSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const require = createRequire(import.meta.url);
const here = dirname(fileURLToPath(import.meta.url));
const repo = resolve(here, "..");

function findPlaywright() {
  try { return require("playwright-core"); } catch {}
  try {
    const root = execSync("npm root -g", { encoding: "utf8" }).trim();
    return require(`${root}/promptfoo/node_modules/playwright-core`);
  } catch {}
  throw new Error("playwright-core not found — install it or adjust scripts/docs-shots.mjs");
}
function findChromium() {
  for (const p of ["/usr/bin/chromium", "/usr/bin/chromium-browser", "/usr/bin/google-chrome", "/usr/bin/chrome"]) {
    try { execSync(`test -x ${p}`); return p; } catch {}
  }
  return undefined;
}

const baseURL = process.argv[2] || "http://127.0.0.1:8080";
const outDir = resolve(repo, process.argv[3] || "docs/static/img/ui");
mkdirSync(outDir, { recursive: true });

// Optionally capture only a subset, e.g. DOCS_SHOTS_ONLY=annapolis (comma list);
// empty = all. Each shot below sets up its own state, so any single one is safe
// to run alone.
const ONLY = (process.env.DOCS_SHOTS_ONLY || "").split(",").map((s) => s.trim()).filter(Boolean);
const want = (name) => ONLY.length === 0 || ONLY.includes(name);

// The featured view: Chesapeake Bay near Annapolis (NOAA District 5), the app's
// own built-in default. Change here and every shot re-frames together.
const VIEW = { lng: -76.4875, lat: 38.975, zoom: 12 };
const W = 1440, H = 960;

const { chromium } = findPlaywright();
const browser = await chromium.launch({ executablePath: findChromium(), args: ["--no-sandbox", "--hide-scrollbars"] });
const page = await browser.newPage({ viewport: { width: W, height: H }, deviceScaleFactor: 2 });
page.on("pageerror", (e) => console.error("[pageerror]", e.message));

// Resolve the app element and wait until MapLibre is loaded and still.
async function appReady() {
  await page.waitForFunction(() => {
    const a = document.querySelector("chart-plotter");
    return a && a._map && a._map.loaded();
  }, { timeout: 60000 });
}
// Settle until the map stops moving and a frame has rendered.
async function idle(settle = 1200) {
  await page.evaluate(() => new Promise((res) => {
    const m = document.querySelector("chart-plotter")._map;
    if (m.loaded() && !m.isMoving()) m.once("idle", res); else m.once("idle", res);
    setTimeout(res, 4000); // safety
  }));
  await page.waitForTimeout(settle);
}
async function frame() {
  await page.evaluate((v) => document.querySelector("chart-plotter").setView(v), VIEW);
  await idle();
}
async function scheme(name) {
  await page.evaluate((n) => document.querySelector("chart-plotter").applyScheme(n), name);
  await idle(800);
}
async function closeDrawer() {
  await page.evaluate(() => { const a = document.querySelector("chart-plotter"); if (a._drawerOpen()) a.closeDrawer(); });
  await page.waitForTimeout(400);
}
async function shot(name) {
  const path = `${outDir}/${name}.png`;
  await page.screenshot({ path });
  console.log(`wrote ${path}`);
}

try {
  await page.goto(baseURL, { waitUntil: "networkidle", timeout: 60000 });
} catch (e) {
  console.error("[nav]", e.message, "— continuing");
}
await appReady();
await scheme("day");
await frame();
await closeDrawer();

// 1) The clean chart view (hero), Day palette.
if (want("chart-day")) await shot("chart-day");

// 2) Day / Dusk / Night — the same view in each palette.
if (want("palette-dusk")) { await scheme("dusk"); await shot("palette-dusk"); }
if (want("palette-night")) { await scheme("night"); await shot("palette-night"); }
await scheme("day"); // back to day for the rest

// 3) The settings drawer (where you switch palette + mariner options).
if (want("settings")) {
  await page.evaluate(() => document.querySelector("chart-plotter").toggleSection("settings"));
  await page.waitForTimeout(700);
  await shot("settings");
  await closeDrawer();
}

// 4) The Chart Library / import panel.
if (want("chart-library")) {
  await page.evaluate(() => document.querySelector("chart-plotter").toggleSection("charts"));
  await page.waitForTimeout(900);
  await shot("chart-library");
  await closeDrawer();
}

// 5) Pick report — tap a charted feature near the center and capture the panel.
if (want("pick-report")) {
  await frame();
  const picked = await page.evaluate(() => {
    const a = document.querySelector("chart-plotter");
    const m = a._map;
    const c = { x: m.getCanvas().clientWidth / 2, y: m.getCanvas().clientHeight / 2 };
    // Find a rendered chart feature nearest center to guarantee a populated report.
    const feats = m.queryRenderedFeatures().filter((f) => /chart/i.test(f.source || ""));
    if (!feats.length) return false;
    a._pickReportAt(c, new MouseEvent("click"));
    return true;
  });
  if (picked) { await page.waitForTimeout(900); await shot("pick-report"); }
  else console.error("[pick] no chart features under center — skipped pick-report");
}

// 6) Close-up of Annapolis / the U.S. Naval Academy at 1:12,000. Captured at
// 1600x1200 device pixels (800x600 viewport at deviceScaleFactor 2); the make
// target downscales it for GitHub.
if (want("annapolis")) {
  await page.setViewportSize({ width: 800, height: 600 });
  await scheme("day");
  await page.evaluate(() => document.querySelector("chart-plotter").setView({ lat: 38.9825, lng: -76.4810, scale: 12000 }));
  // A big camera jump kicks off async tile loads; one idle can fire before the
  // harbor tiles arrive, so settle, nudge a repaint, and idle again.
  await idle(2500);
  await page.evaluate(() => document.querySelector("chart-plotter")._map.triggerRepaint());
  await idle(2500);
  await closeDrawer();
  await shot("annapolis");
}

await browser.close();
console.log("done");
