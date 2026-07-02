// Tests engineStamp — the attribution-corner ENGINE-COMMIT stamp built from the
// active server sets' TileJSON `engine` fields: one muted commit when every set
// agrees; a per-pack "label:commit" list flagged mixed (minority groups marked ✱)
// when they differ (a partially re-baked cache); null (stamp hidden) when no set
// reports an engine (pmtiles mode / an older server).
// Run: node --test web/src/chart-canvas/chart-sources.engine-stamp.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import { engineStamp } from "./chart-sources.mjs";

test("all sets agree → the single commit, not mixed", () => {
  const s = engineStamp([
    { name: "noaa-d5-coastal", engine: "abc123def" },
    { name: "noaa-d5-harbor", engine: "abc123def" },
    { name: "noaa-d7-approach", engine: "abc123def" },
  ]);
  assert.ok(s);
  assert.equal(s.text, "abc123def");
  assert.equal(s.mixed, false);
  // Tooltip lists every set's full detail.
  assert.match(s.title, /noaa-d5-coastal: abc123def/);
  assert.match(s.title, /noaa-d7-approach: abc123def/);
});

test("differing engines → per-pack groups, majority first, minority marked", () => {
  const s = engineStamp([
    { name: "noaa-d5-coastal", engine: "abc123def" },
    { name: "noaa-d5-harbor", engine: "abc123def" },
    { name: "noaa-d7-approach", engine: "def456abc" },
  ]);
  assert.ok(s);
  assert.equal(s.mixed, true);
  // Majority (d5 ×2) leads unmarked; the disagreeing d7 group carries the ✱.
  assert.equal(s.text, "d5:abc123def d7:def456abc✱");
  assert.match(s.title, /✱ differs/);
});

test("band suffixes collapse to one pack label per group", () => {
  const s = engineStamp([
    { name: "noaa-d5-coastal", engine: "aaa" },
    { name: "noaa-d5-harbor", engine: "aaa" },
    { name: "noaa-d5-berthing", engine: "bbb" }, // one band re-baked by a newer engine
  ]);
  assert.equal(s.mixed, true);
  assert.equal(s.text, "d5:aaa d5:bbb✱");
});

test("live tile57 set and pre-stamp packs keep their labels/values", () => {
  const s = engineStamp([
    { name: "tile57", engine: "abc123def" },       // live set → running binary's commit
    { name: "ienc-coastal", engine: "pre-stamp" }, // legacy pack without the sidecar
  ]);
  assert.equal(s.mixed, true);
  // Non-noaa names keep their pack name as the label.
  assert.ok(s.text.includes("tile57:abc123def"));
  assert.ok(s.text.includes("ienc:pre-stamp✱"));
});

test("no engine info anywhere → null (stamp hidden)", () => {
  assert.equal(engineStamp([]), null);
  assert.equal(engineStamp(null), null);
  // Older server: metas exist but carry no engine field.
  assert.equal(engineStamp([{ name: "noaa-d5-coastal" }, { name: "noaa-d5-harbor", engine: "" }]), null);
});
