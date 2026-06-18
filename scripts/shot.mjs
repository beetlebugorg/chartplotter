// Headless screenshot of a chartplotter URL — e.g. a "Share this view" link
// (<origin>/#share) — so a change can be eyeballed without a desktop browser.
//
// The frontend is a wasm app that downloads cells and bakes tiles asynchronously,
// so we wait for the network to go idle and then a short settle delay before
// grabbing the frame. Console errors are forwarded to stderr to surface boot
// failures.
//
// Usage: node scripts/shot.mjs <url> [out.png] [width] [height] [settle-ms] [click-selector]
// click-selector (optional) is clicked after load — pierces open shadow DOM, so
// e.g. "#inspect-toggle" opens the Inspect panel before the frame is grabbed.
// Resolves playwright-core from wherever it's installed (e.g. promptfoo's deps).

import { createRequire } from "node:module";
import { execSync } from "node:child_process";

const require = createRequire(import.meta.url);

function findPlaywright() {
  // Try a normal resolve first, then known global install locations.
  try { return require("playwright-core"); } catch {}
  try {
    const root = execSync("npm root -g", { encoding: "utf8" }).trim();
    return require(`${root}/promptfoo/node_modules/playwright-core`);
  } catch {}
  throw new Error("playwright-core not found — install it or adjust scripts/shot.mjs");
}

function findChromium() {
  for (const p of ["/usr/bin/chromium", "/usr/bin/chromium-browser", "/usr/bin/google-chrome", "/usr/bin/chrome"]) {
    try { execSync(`test -x ${p}`); return p; } catch {}
  }
  return undefined; // let playwright use its bundled browser
}

const [url, out = "/tmp/chartplotter-shot.png", w = "1400", h = "900", settle = "6000", click = ""] = process.argv.slice(2);
if (!url) {
  console.error("usage: node scripts/shot.mjs <url> [out.png] [width] [height] [settle-ms]");
  process.exit(2);
}

const { chromium } = findPlaywright();

const browser = await chromium.launch({
  executablePath: findChromium(),
  args: ["--no-sandbox", "--hide-scrollbars"],
});
const page = await browser.newPage({ viewport: { width: +w, height: +h }, deviceScaleFactor: 1 });
page.on("console", (m) => { if (m.type() === "error") console.error("[page error]", m.text()); });
page.on("pageerror", (e) => console.error("[pageerror]", e.message));

try {
  await page.goto(url, { waitUntil: "networkidle", timeout: 60000 });
} catch (e) {
  console.error("[nav]", e.message, "— continuing to screenshot anyway");
}
await page.waitForTimeout(+settle); // let the wasm baker finish a couple of tile passes
if (click) {
  try { await page.locator(click).first().click({ timeout: 5000 }); await page.waitForTimeout(800); }
  catch (e) { console.error("[click]", click, e.message); }
}
await page.screenshot({ path: out });
await browser.close();
console.log(`wrote ${out} (${w}x${h}, settle ${settle}ms)`);
