// Merge several `bake --manifest` charts-index files into one, by concatenating
// their `districts` arrays, so a single <chart-plotter> on the docs test-harness page
// holds both the Chart 1 and S-64 pre-baked band archives at once.
//
//   node docs/scripts/merge-manifests.mjs <out.json> <in1.json> <in2.json> …
import { readFileSync, writeFileSync } from "node:fs";

const [out, ...ins] = process.argv.slice(2);
if (!out || !ins.length) {
  console.error("usage: merge-manifests.mjs <out.json> <in...>");
  process.exit(1);
}
const districts = [];
let aux = "";
for (const f of ins) {
  const m = JSON.parse(readFileSync(f, "utf8"));
  for (const d of m.districts || []) districts.push(d);
  if (m.aux && !aux) aux = m.aux;
}
const merged = aux ? { districts, aux } : { districts };
writeFileSync(out, JSON.stringify(merged, null, 2));
console.log(`merge-manifests: ${districts.length} archive(s) → ${out}`);
