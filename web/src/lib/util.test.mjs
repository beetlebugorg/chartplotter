import { test } from "node:test";
import assert from "node:assert/strict";
import { parseLatLon, fmtLatLon } from "./util.mjs";

const near = (a, b, eps = 1e-3) => Math.abs(a - b) <= eps;

test("parseLatLon — degrees-decimal-minutes with hemispheres (the spec example)", () => {
  const r = parseLatLon("32°29.66’S, 060°55.86’E");
  assert.ok(r);
  assert.ok(near(r.lat, -(32 + 29.66 / 60)), `lat ${r.lat}`);
  assert.ok(near(r.lng, 60 + 55.86 / 60), `lng ${r.lng}`);
});

test("parseLatLon — plain apostrophe, no comma, fmtLatLon's own ′ output", () => {
  assert.ok(near(parseLatLon("32 29.66'S 060 55.86'E").lat, -32.49433));
  const r = parseLatLon("39°27.6′N 104°39.6′W");
  assert.ok(near(r.lat, 39 + 27.6 / 60));
  assert.ok(near(r.lng, -(104 + 39.6 / 60)));
});

test("parseLatLon — decimal degrees, signed and lettered", () => {
  assert.deepEqual(roundLL(parseLatLon("-32.4943, 60.931")), { lat: -32.4943, lng: 60.931 });
  assert.deepEqual(roundLL(parseLatLon("32.4943 S 60.931 E")), { lat: -32.4943, lng: 60.931 });
  assert.deepEqual(roundLL(parseLatLon("38.97 -76.47")), { lat: 38.97, lng: -76.47 });
});

test("parseLatLon — degrees-minutes-seconds", () => {
  const r = parseLatLon("32 29 40 S  60 55 52 E");
  assert.ok(near(r.lat, -(32 + 29 / 60 + 40 / 3600)));
  assert.ok(near(r.lng, 60 + 55 / 60 + 52 / 3600));
});

test("parseLatLon — round-trips through fmtLatLon", () => {
  for (const [lat, lng] of [[38.978, -76.478], [-32.4943, 60.931], [0, 0], [-33.86, 151.21]]) {
    const r = parseLatLon(fmtLatLon(lat, lng));
    assert.ok(r && near(r.lat, lat, 0.02) && near(r.lng, lng, 0.02), `${lat},${lng} -> ${JSON.stringify(r)}`);
  }
});

test("parseLatLon — rejects non-coordinates and out-of-range", () => {
  for (const bad of ["", "spa creek", "US5MD1MC", "32", "hello world", "200, 60", "45, 999", "abc, def"]) {
    assert.equal(parseLatLon(bad), null, `should reject: ${JSON.stringify(bad)}`);
  }
});

function roundLL(r) {
  return r && { lat: Math.round(r.lat * 1e4) / 1e4, lng: Math.round(r.lng * 1e4) / 1e4 };
}
