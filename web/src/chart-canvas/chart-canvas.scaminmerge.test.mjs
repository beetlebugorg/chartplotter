// Tests the ?scaminmerge A/B toggle on <chart-canvas>: in merged mode the client
// asks the engine for its zoom-expression SCAMIN gate (scaminMerge=1, NOT
// scaminFilterGate) and DISABLES the whole client SCAMIN injection path
// (_scaminUpdate / _scaminApplySettled / _scaminForceWhenReady early-return, so no
// setFilter / source reload fires on zoom). Normal mode is unchanged.
// Run: node --test web/src/chart-canvas/chart-canvas.scaminmerge.test.mjs
//
// <chart-canvas> extends HTMLElement and calls customElements.define at load, so we
// shim the two custom-element globals before importing — the module is otherwise
// node-safe (maplibre is a lazy dynamic import). We drive the pure methods via
// prototype.call(stub) so no DOM/map is constructed.
import test from "node:test";
import assert from "node:assert/strict";

globalThis.HTMLElement = globalThis.HTMLElement || class {};
globalThis.customElements = globalThis.customElements || { define() {} };

const { ChartCanvas } = await import("./chart-canvas.mjs");
const proto = ChartCanvas.prototype;

// Minimal `this` for _marinerQuery: it reads _mariner (an object of settings, all
// omitted here so every key is skipped), _active, _engineSet, the three SCAMIN
// flags, and _featureSizeScale().
function marinerStub(over) {
  return Object.assign({
    _mariner: {},
    _active: "day",
    _engineSet: "tile57",
    _ignoreScamin: false,
    _scaminMerged: false,
    _scaminGate: true,
    _featureSizeScale: () => 1,
  }, over);
}

test("_marinerQuery: merged mode sends scaminMerge=1 and NOT scaminFilterGate", () => {
  const q = new URLSearchParams(proto._marinerQuery.call(marinerStub({ _scaminMerged: true })));
  assert.equal(q.get("scaminMerge"), "1");
  assert.equal(q.get("scaminFilterGate"), null);
  assert.equal(q.get("set"), "tile57");
});

test("_marinerQuery: normal (non-merged) mode sends scaminFilterGate=1 and NOT scaminMerge", () => {
  const q = new URLSearchParams(proto._marinerQuery.call(marinerStub({ _scaminMerged: false, _scaminGate: true })));
  assert.equal(q.get("scaminFilterGate"), "1");
  assert.equal(q.get("scaminMerge"), null);
});

test("_scaminForceWhenReady: merged mode is a no-op (never calls _scaminUpdate)", () => {
  let updates = 0;
  const stub = {
    _scaminMerged: true,
    _map: { isStyleLoaded: () => true, once: () => { throw new Error("must not defer"); } },
    _scaminUpdate: () => { updates++; },
  };
  proto._scaminForceWhenReady.call(stub);
  assert.equal(updates, 0, "merged mode must not inject a cutoff");
});

test("_scaminForceWhenReady: normal mode DOES inject the cutoff (calls _scaminUpdate(true))", () => {
  const args = [];
  const stub = {
    _scaminMerged: false,
    _map: { isStyleLoaded: () => true },
    _scaminLayersCache: {}, _chartLayerIdsCache: {},
    _scaminUpdate: (force) => { args.push(force); },
  };
  proto._scaminForceWhenReady.call(stub);
  assert.deepEqual(args, [true], "normal mode re-injects the live cutoff");
});

test("_scaminUpdate: merged mode early-returns before any setFilter (even with all other guards open)", () => {
  let setFilters = 0;
  // All the NON-merged guards are deliberately satisfied, so _scaminMerged is the
  // ONLY thing that can stop the injection loop.
  const stub = {
    _scaminMerged: true,
    _scaminGate: true,
    _engineMode: true,
    _map: {
      isStyleLoaded: () => true,
      getZoom: () => 13, getCenter: () => ({ lat: 38.9 }),
      getLayer: () => ({}), getFilter: () => ["all"],
      setFilter: () => { setFilters++; },
    },
    _pxPitch: undefined,
    _engineScaminValues: [30000, 12000],
    _scaminGatedLayers: () => { throw new Error("must not scan gated layers in merged mode"); },
  };
  proto._scaminUpdate.call(stub, true);
  assert.equal(setFilters, 0, "merged mode must issue zero setFilter (the zoom-expression self-gates)");
});

test("_scaminApplySettled: merged mode schedules no settle apply", async () => {
  let scheduled = false;
  const realSetTimeout = globalThis.setTimeout;
  globalThis.setTimeout = (fn, d) => { scheduled = true; return realSetTimeout(fn, d); };
  try {
    proto._scaminApplySettled.call({ _scaminMerged: true, _scaminApplyT: 0 }, 120);
    assert.equal(scheduled, false, "merged mode must not arm the settle timer");
  } finally {
    globalThis.setTimeout = realSetTimeout;
  }
});
