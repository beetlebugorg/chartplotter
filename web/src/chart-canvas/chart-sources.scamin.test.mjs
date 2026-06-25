// Tests the in-place SCAMIN bucket re-gate (_reapplyScaminMinzooms): on latitude
// drift it must setLayerZoomRange only the per-value "#sm<scamin>" bucket layers,
// to scaminDisplayZoom(scamin, lat), preserving each layer's maxzoom — and never
// touch non-bucket layers (no full style rebuild → no flicker).
// Run: node --test web/src/chart-canvas/chart-sources.scamin.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import { ChartSources, scaminDisplayZoom } from "./chart-sources.mjs";

function mockMap(lat, layers) {
  const calls = [];
  return {
    calls,
    getCenter: () => ({ lat }),
    getStyle: () => ({ layers }),
    setLayerZoomRange: (id, min, max) => calls.push({ id, min, max }),
  };
}

test("re-gates only #sm bucket layers, to the latitude-adjusted minzoom", () => {
  const lat = 38.97;
  const layers = [
    { id: "point_symbols@chesapeake-harbour#sm30000" },
    { id: "text@chesapeake-harbour#sm12000", maxzoom: 12 }, // capped band keeps its maxzoom
    { id: "point_symbols@chesapeake-harbour#no" },          // always-from-floor bucket — untouched
    { id: "areas@chesapeake-harbour" },                     // non-bucket — untouched
    { id: "lines-solid" },                                   // unrelated layer
  ];
  const map = mockMap(lat, layers);
  const cs = new ChartSources({ assets: "", getMap: () => map, rebuild: () => { throw new Error("must not rebuild"); } });
  cs._reapplyScaminMinzooms();

  // Only the two #sm layers were re-gated.
  assert.equal(map.calls.length, 2);
  const byId = Object.fromEntries(map.calls.map((c) => [c.id, c]));
  assert.ok(byId["point_symbols@chesapeake-harbour#sm30000"]);
  assert.ok(byId["text@chesapeake-harbour#sm12000"]);
  // Minzoom equals the build-time formula.
  assert.equal(byId["point_symbols@chesapeake-harbour#sm30000"].min, scaminDisplayZoom(30000, lat));
  assert.equal(byId["text@chesapeake-harbour#sm12000"].min, scaminDisplayZoom(12000, lat));
  // Capped layer keeps its maxzoom; uncapped gets the MapLibre max (24).
  assert.equal(byId["text@chesapeake-harbour#sm12000"].max, 12);
  assert.equal(byId["point_symbols@chesapeake-harbour#sm30000"].max, 24);
  // The applied latitude is recorded so the next drift compares against it.
  assert.equal(cs._scaminLat, lat);
});

test("minzoom shifts with latitude (cos-lat), proving the re-gate is needed", () => {
  // Higher latitude → different display zoom for the same SCAMIN; the values must
  // differ, else there'd be nothing to re-gate.
  assert.notEqual(scaminDisplayZoom(30000, 10), scaminDisplayZoom(30000, 60));
});
