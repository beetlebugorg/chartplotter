// Verifies the true-physical feature-size scaling: buildChartLayers({ sizeScale })
// must multiply every pixel-valued size (icon-size / text-size / line-width /
// text-halo-width) by sizeScale, and leave them untouched when sizeScale == 1.
// Run: node --test web/src/chart-canvas/chart-style.sizescale.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import { buildChartLayers } from "./chart-style.mjs";

function build(sizeScale) {
  return buildChartLayers({
    mariner: {}, palette: {}, atlasPpu: 0.08, osm: false, scheme: "day",
    server: false, serverSets: [], scaminValues: [], scaminLat: 0,
    bandsHidden: new Set(), ignoreScamin: true, sizeScale,
  }).layers;
}

// A size expression wrapped by _scaleSizes looks like ["*", k, <original>].
function wrappedBy(v, k) {
  return Array.isArray(v) && v[0] === "*" && v[1] === k;
}

test("sizeScale wraps icon-size / text-size / line-width / halo with [*, k, …]", () => {
  const k = 1.3338;
  const layers = build(k);
  const lineSolid = layers.find((L) => (L._baseId || L.id || "").startsWith("lines-solid") || L.id?.startsWith("lines-solid@"));
  const pointSym = layers.find((L) => L.id?.startsWith("point_symbols@"));
  const text = layers.find((L) => L.id?.startsWith("text@"));

  assert.ok(lineSolid, "a lines-solid variant exists");
  assert.ok(wrappedBy(lineSolid.paint["line-width"], k), "line-width scaled");
  assert.ok(pointSym, "a point_symbols variant exists");
  assert.ok(wrappedBy(pointSym.layout["icon-size"], k), "icon-size scaled");
  assert.ok(text, "a text variant exists");
  assert.ok(wrappedBy(text.layout["text-size"], k), "text-size scaled");
  assert.ok(wrappedBy(text.paint["text-halo-width"], k), "text-halo-width scaled");
});

test("sizeScale == 1 leaves sizes untouched (no [*, 1, …] wrapper)", () => {
  const layers = build(1);
  const lineSolid = layers.find((L) => L.id?.startsWith("lines-solid@"));
  assert.ok(lineSolid, "a lines-solid variant exists");
  // Original line-width is ["coalesce", ["get","width_px"], 1] — NOT a "*" wrapper.
  assert.equal(lineSolid.paint["line-width"][0], "coalesce");
});
