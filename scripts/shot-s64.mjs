// Render an S-64 test cell headlessly with explicit mariner settings, for
// comparing our portrayal against the S-64 reference plots. Presets localStorage
// (mariner settings, day scheme, NOAA agreement) BEFORE the app boots, points the
// app at a single-cell catalog (?prod&catalog=…) which auto-frames the cell, then
// screenshots the clean map after the wasm baker settles.
//
// Usage: node scripts/shot-s64.mjs <url> <out.png> <marinerJSON> [w] [h] [settle-ms]
import { createRequire } from "node:module";
import { execSync } from "node:child_process";
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
const [url, out = "/tmp/s64.png", marinerJSON = "{}", w = "1100", h = "1100", settle = "9000", view = ""] = process.argv.slice(2);
// view = "lng,lat,zoom" to pin the camera; empty = let the app auto-frame.
const { chromium } = findPlaywright();
const browser = await chromium.launch({ executablePath: findChromium(), args: ["--no-sandbox", "--hide-scrollbars"] });
const page = await browser.newPage({ viewport: { width: +w, height: +h }, deviceScaleFactor: 1 });
page.on("console", (m) => { if (m.type() === "error") console.error("[page error]", m.text()); });
page.on("pageerror", (e) => console.error("[pageerror]", e.message));
const mariner = JSON.parse(marinerJSON);
const viewArr = view ? view.split(",").map(Number) : null;
await page.addInitScript((arg) => {
  localStorage.setItem("chartplotter:mariner", JSON.stringify(arg.m));
  localStorage.setItem("chartplotter:scheme", "day");
  localStorage.setItem("chartplotter:basemap", "coastline");
  localStorage.setItem("chartplotter:enc-agreement", "1");
  if (arg.v) localStorage.setItem("chartplotter:view", JSON.stringify({ center: [arg.v[0], arg.v[1]], zoom: arg.v[2] }));
  else localStorage.removeItem("chartplotter:view");
}, { m: mariner, v: viewArr });
try { await page.goto(url, { waitUntil: "networkidle", timeout: 60000 }); }
catch (e) { console.error("[nav]", e.message, "— continuing"); }
await page.waitForTimeout(+settle);
await page.screenshot({ path: out });
await browser.close();
console.log(`wrote ${out}`);
