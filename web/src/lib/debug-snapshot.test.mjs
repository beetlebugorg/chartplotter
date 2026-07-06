import { test } from "node:test";
import assert from "node:assert/strict";
import { viewSnapshot, gatesSnapshot, featureSnapshot, featureDebugSnapshot } from "./debug-snapshot.mjs";

// A minimal MapLibre-shaped stub: camera getters + a style with one SCAMIN-gated
// layer, one overscale (oscl) layer and one ungated layer. (smax is retired:
// cross-band occlusion is baked geometry, not a client gate.)
function fakeMap() {
  return {
    getCenter: () => ({ lng: -76.4701234, lat: 38.9698765 }),
    getZoom: () => 14.03125,
    getBearing: () => 0,
    getStyle: () => ({
      layers: [
        { id: "plain", type: "fill" }, // no filter at all
        { id: "ungated", type: "line", filter: ["==", ["get", "class"], "DEPCNT"] },
        {
          id: "point_symbols",
          type: "symbol",
          filter: [">=", ["coalesce", ["get", "scamin"], 99999999], 21998.5],
        },
        { id: "overscale", type: "fill", filter: [">", ["coalesce", ["get", "oscl"], 0], 11999.6] },
      ],
    }),
  };
}

test("viewSnapshot — compact camera; null without a map", () => {
  const v = viewSnapshot(fakeMap());
  assert.deepEqual(v, { center: [-76.470123, 38.969876], zoom: 14.031, bearing: 0 });
  assert.equal(viewSnapshot(null), null);
});

test("gatesSnapshot — one entry per scamin/oscl-gated layer, denoms rounded", () => {
  const g = gatesSnapshot(fakeMap());
  assert.deepEqual(Object.keys(g).sort(), ["overscale", "point_symbols"]);
  assert.deepEqual(g.point_symbols, { scamin: 21999, oscl: null });
  assert.deepEqual(g.overscale, { scamin: null, oscl: 12000 });
  assert.deepEqual(gatesSnapshot(null), {}); // no map → empty, not a throw
});

test("featureSnapshot — reduces a queried feature to source/layer/geometry/properties", () => {
  const f = {
    source: "chart-harbour", sourceLayer: "point_symbols",
    geometry: { type: "Point", coordinates: [-76.47, 38.97] },
    properties: { class: "BOYLAT", s57: '{"CATLAM":"2"}' },
    layer: { id: "point_symbols" }, // dropped: not part of the transportable identity
  };
  assert.deepEqual(featureSnapshot(f), {
    source: "chart-harbour", sourceLayer: "point_symbols",
    geometry: f.geometry, properties: f.properties,
  });
});

test("featureDebugSnapshot — the pick-report copy shape { when, view, feature, gates }", () => {
  const f = { source: "chart", sourceLayer: "point_symbols", geometry: { type: "Point", coordinates: [0, 0] }, properties: { class: "LIGHTS" } };
  const snap = featureDebugSnapshot(fakeMap(), f);
  assert.deepEqual(Object.keys(snap), ["when", "view", "feature", "gates"]);
  assert.ok(!isNaN(Date.parse(snap.when)), `when parses: ${snap.when}`);
  assert.equal(snap.view.zoom, 14.031);
  assert.equal(snap.feature.properties.class, "LIGHTS");
  assert.ok(snap.gates.point_symbols);
  // Round-trips through JSON (what the button actually copies).
  const back = JSON.parse(JSON.stringify(snap, null, 2));
  assert.deepEqual(back, snap);
});
