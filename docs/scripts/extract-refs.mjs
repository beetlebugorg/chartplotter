// Extract the official reference-plot pages from the IHO PDFs into per-page JPEGs the
// docs test-harness page (TestHarness.js) shows beside the live render.
//
//   node docs/scripts/extract-refs.mjs        (run from the repo root)
//
// For every scenario we render a WINDOW of pages around its (best-guess) refPage, so
// the page's ◀/▶ nudge can slide to the exact plot without re-running this. Output →
// docs/static/harness/refs/<pdf>/p<page>.jpg (gitignored, cached: existing pages
// skipped). Needs pdftoppm (poppler). A missing source PDF is a warning, not a failure
// — the chart1 PDF lives in the sibling ../chartplotter-specs repo.
import { execFileSync } from "node:child_process";
import { existsSync, mkdirSync } from "node:fs";
import { SCENARIOS, REF_PDFS } from "../src/components/testharness/scenarios.js";

const OUT = "docs/static/harness/refs";
const WINDOW = 12; // pages on each side of refPage (covers printed-vs-PDF page offsets)
const DPI = 110;

function havePdftoppm() {
  try { execFileSync("pdftoppm", ["-v"], { stdio: "ignore" }); return true; }
  catch { return false; }
}
if (!havePdftoppm()) {
  console.error("extract-refs: pdftoppm (poppler) not found — install it to generate reference crops.");
  process.exit(1);
}

const pagesByPdf = new Map();
for (const s of SCENARIOS) {
  if (!s.refPage) continue;
  const set = pagesByPdf.get(s.pdf) || new Set();
  for (let p = Math.max(1, s.refPage - WINDOW); p <= s.refPage + WINDOW; p++) set.add(p);
  pagesByPdf.set(s.pdf, set);
}

let wrote = 0, skipped = 0, missing = 0;
for (const [pdf, pages] of pagesByPdf) {
  const src = REF_PDFS[pdf];
  if (!src || !existsSync(src)) {
    console.warn(`extract-refs: ${pdf} PDF not found (${src}) — skipping ${pages.size} page(s).`);
    missing += pages.size;
    continue;
  }
  const dir = `${OUT}/${pdf}`;
  mkdirSync(dir, { recursive: true });
  for (const p of [...pages].sort((a, b) => a - b)) {
    const stem = `${dir}/p${p}`;
    if (existsSync(`${stem}.jpg`)) { skipped++; continue; }
    try {
      execFileSync("pdftoppm", ["-jpeg", "-r", String(DPI), "-f", String(p), "-l", String(p), "-singlefile", src, stem], { stdio: "ignore" });
      wrote++;
    } catch (e) {
      console.warn(`extract-refs: failed page ${p} of ${pdf}: ${e.message}`);
    }
  }
  console.log(`extract-refs: ${pdf} → ${dir} (${pages.size} page(s) in window)`);
}
console.log(`extract-refs: wrote ${wrote}, cached ${skipped}, missing-pdf ${missing}.`);
