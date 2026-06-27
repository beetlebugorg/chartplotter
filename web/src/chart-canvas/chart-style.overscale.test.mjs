// Verifies the S-52 §10.1.10.2 overscale-pattern gate in buildChartLayers: the
// AP(OVERSC01) hatch (layer id "overscale@chart-<band>", fill-pattern pat:OVERSC01)
// is emitted for a band ONLY when a strictly-FINER band is present (a real chart-scale
// boundary). The finest band present is best-available data — plain zoom-in of it is
// the ×N-only case (§10.1.10.1), so it must get NO pattern.
// Run: node --test web/src/chart-canvas/chart-style.overscale.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import { buildChartLayers, PAT_PREFIX } from "./chart-style.mjs";

function overscaleLayers(bandsPresent) {
  return buildChartLayers({
    mariner: {}, palette: {}, atlasPpu: 0.08, osm: false, scheme: "day",
    server: false, serverSets: [], scaminValues: [], scaminLat: 0,
    bandsHidden: new Set(), bandsPresent: new Set(bandsPresent),
    ignoreScamin: true, sizeScale: 1,
  }).layers.filter((L) => L.id.startsWith("overscale@"));
}

test("coarser band gets the hatch only when a finer band is present", () => {
  const ids = overscaleLayers(["coastal", "harbor"]).map((L) => L.id);
  assert.ok(ids.includes("overscale@chart-coastal"), "coastal hatches (harbor is finer)");
  assert.ok(!ids.includes("overscale@chart-harbor"), "harbor is finest present — no hatch (×N only)");
});

test("the overscale layer paints the OVERSC01 fill-pattern over areas", () => {
  const L = overscaleLayers(["coastal", "harbor"]).find((x) => x.id === "overscale@chart-coastal");
  assert.ok(L, "coastal overscale layer exists");
  assert.equal(L.type, "fill");
  assert.equal(L["source-layer"], "areas");
  assert.equal(L.paint["fill-pattern"], PAT_PREFIX + "OVERSC01");
});

test("a single band present (best-available) never hatches", () => {
  assert.equal(overscaleLayers(["harbor"]).length, 0, "lone harbor: no pattern, ×N indication only");
});

test("no bands present (default) emits no overscale layers", () => {
  assert.equal(overscaleLayers([]).length, 0);
});
