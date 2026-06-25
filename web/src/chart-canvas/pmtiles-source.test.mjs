// MultiArchive must union the packs' published SCAMIN manifests so the client
// builds the per-value bucket layers at load (no per-zoom tile discovery → no
// style-rebuild flicker). Run: node --test web/src/chart-canvas/pmtiles-source.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import { MultiArchive } from "./pmtiles-source.mjs";

test("MultiArchive unions the packs' published SCAMIN sets, sorted + deduped", () => {
  const ma = new MultiArchive();
  ma.addOpened({ minZoom: 0, maxZoom: 14, bounds: null, scamin: [30000, 12000] });
  ma.addOpened({ minZoom: 0, maxZoom: 16, bounds: null, scamin: [12000, 90000] });
  assert.deepEqual(ma.scamin, [12000, 30000, 90000]);
});

test("MultiArchive tolerates packs without a SCAMIN manifest (older archive)", () => {
  const ma = new MultiArchive();
  ma.addOpened({ minZoom: 0, maxZoom: 14, bounds: null }); // no .scamin
  assert.deepEqual(ma.scamin, []);
});
