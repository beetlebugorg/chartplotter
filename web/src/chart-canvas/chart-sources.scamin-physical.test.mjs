// Verifies SCAMIN gating is on the TRUE physical display scale (S-57 B.1 §2.2.7 /
// S-52 Display Scale), not a fixed web pixel: a SCAMIN 1:N feature must become
// visible exactly when the screen reads 1:N — at the (calibrated) pixel pitch.
// Run: node --test web/src/chart-canvas/chart-sources.scamin-physical.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import { scaminDisplayZoom } from "./chart-sources.mjs";
import { scaleDenomPhysical, DEFAULT_PX_PITCH_MM } from "../lib/util.mjs";

test("a SCAMIN 1:N feature's cutoff zoom reads exactly 1:N on the physical scale", () => {
  const lat = 38.97; // Annapolis
  for (const scamin of [17999, 21999, 29999, 44999]) {
    for (const pitch of [DEFAULT_PX_PITCH_MM, 0.254, 0.20]) {
      const z = scaminDisplayZoom(scamin, lat, pitch);
      const denomAtCutoff = scaleDenomPhysical(z, lat, pitch);
      // The displayed scale at the cutoff zoom equals the SCAMIN value (≤0.1% slack).
      assert.ok(Math.abs(denomAtCutoff - scamin) / scamin < 1e-3,
        `scamin ${scamin} @ pitch ${pitch}: cutoff reads 1:${Math.round(denomAtCutoff)}`);
    }
  }
});

test("finer pixel pitch pushes the cutoff to a HIGHER zoom (feature hides earlier zooming out)", () => {
  const lat = 38.97;
  const coarse = scaminDisplayZoom(17999, lat, 0.2645); // CSS reference
  const fine = scaminDisplayZoom(17999, lat, 0.20);     // dense screen
  assert.ok(fine > coarse, `fine pitch ${fine} should exceed coarse ${coarse}`);
});

test("no pitch arg falls back to the CSS-reference pixel", () => {
  const lat = 38.97;
  assert.equal(scaminDisplayZoom(17999, lat), scaminDisplayZoom(17999, lat, DEFAULT_PX_PITCH_MM));
});
