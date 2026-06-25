// wheel-zoom.mjs — owns scroll-wheel zoom for the chart, replacing MapLibre's
// native scrollZoom so the two overscale limits feel right:
//
//   • Overscale detent — zooming IN stops dead at the onset of overscale (the zoom
//     whose display scale equals the covering chart's compilation scale, supplied
//     by the HUD as getDetentZoom). The whole scroll AND its momentum tail are
//     absorbed, so a "throw" can't punch through; to continue into overscale you
//     scroll AGAIN (a fresh gesture releases it). Re-arms once you zoom back below.
//
//   • Floor — zooming IN stops at the 1:MIN_DETAIL_SCALE scale floor (the map's
//     real maxZoom, set with a FLOOR_GIVE sliver of headroom). A hard scroll
//     over-pulls a hair past it and settles back: a stop with a little give, not
//     a wall that springs back.
//
// We accumulate onto our OWN target zoom and jump the camera to it each event.
// Reading map.getZoom() instead would crawl: easeTo({duration:0}) applies on the
// next render frame, so several wheel events fired between frames (every trackpad
// gesture) all read the same stale zoom and only the last step per frame survives.
// A private target adds up every event regardless of frame timing. A fresh gesture
// (after a quiet gap, or after pinch/flyTo moved the camera) re-bases on the live
// zoom.
//
// Anchoring is injected, not pushed: getAnchor() returns the geographic point to
// keep fixed while zooming (e.g. the vessel when a follow plugin is active), or
// null for cursor-anchored. The shell composes it from whatever plugins contribute
// (see ChartPlotter), so plugins never reach into WheelZoom — symmetric with
// getDetent/getFloor.
//
//   const wz = new WheelZoom({ map, getDetent: () => hud.getDetentZoom(),
//                              getFloor: () => floorZoom(), getAnchor: () => anchor() });

import { FLOOR_GIVE } from "../lib/util.mjs";

const ZOOM_RATE = 0.009;         // zoom levels per wheel-pixel of deltaY
const GESTURE_GAP_MS = 200;      // quiet time that ends a gesture (next event re-bases on live zoom)
const SETTLE_DELAY_MS = 40;      // fixed delay from first floor contact before springing back
const SETTLE_MS = 120;           // spring-back ease length
const ELASTIC = 0.28;            // fraction of a step applied once past the floor (damped over-pull)

export class WheelZoom {
  constructor({ map, getDetent, getFloor, getAnchor } = {}) {
    this._map = map;
    this._getDetent = getDetent || (() => null);
    this._getAnchor = getAnchor || (() => null); // [lng,lat] to keep fixed while zooming, or null → cursor
    // The live 1:MIN_DETAIL_SCALE zoom floor (rest cap). Computed PER EVENT, not read
    // from map.getMaxZoom() — that's only re-applied on moveend, so mid-gesture it can
    // be stale-high (initial z18, or a value a flyTo left raised) and we'd sail past
    // the floor and only snap back on moveend (the "bounce"). A live floor stops dead.
    this._getFloor = getFloor || (() => map.getMaxZoom() - FLOOR_GIVE);
    this._target = null;        // accumulated target zoom; null until the first gesture
    this._lastTs = 0;           // timestamp of the previous wheel event (gesture detection)
    this._detentArmed = true;   // true below the detent; cleared once a fresh gesture releases it
    this._detentParked = false; // currently stopped AT the detent (absorbing a scroll + its momentum)
    this._floorLatched = false; // gave the one elastic over-pull this floor contact → now hard-stop
    this._settleT = 0;          // pending floor settle-back (fixed delay, not reset by momentum)

    this._canvas = map.getCanvasContainer();
    this._onWheel = (e) => this._wheel(e);
    map.scrollZoom.disable();   // we own the wheel now
    this._canvas.addEventListener("wheel", this._onWheel, { passive: false });
  }

  destroy() {
    if (this._canvas) this._canvas.removeEventListener("wheel", this._onWheel, { passive: false });
    if (this._settleT) { clearTimeout(this._settleT); this._settleT = 0; }
    if (this._map && this._map.scrollZoom) this._map.scrollZoom.enable();
  }

  _wheel(e) {
    e.preventDefault();
    const map = this._map;
    let d = e.deltaY;
    if (e.deltaMode === 1) d *= 28;        // lines → ~pixels
    else if (e.deltaMode === 2) d *= 400;  // pages
    if (!d) return;

    const min = map.getMinZoom();
    const floor = this._getFloor();        // live scale floor (resting cap)
    const hardMax = floor + FLOOR_GIVE;    // the give is the elastic over-pull headroom
    const zoomingIn = d < 0;
    const step = -d * ZOOM_RATE;           // + zoom-in / − zoom-out

    // A fresh gesture starts after a quiet gap (or the camera moved under us via
    // pinch/flyTo). Re-base the target on the live zoom; within a gesture, accumulate.
    const newGesture = this._target == null || (e.timeStamp - this._lastTs) > GESTURE_GAP_MS;
    if (newGesture) this._target = map.getZoom();
    this._lastTs = e.timeStamp;

    const from = this._target;             // where we are on our own clock
    let next = from + step;

    // Re-arm the detent / clear the floor latch whenever we've dropped back below them.
    const detent = this._getDetent();
    if (detent != null && from < detent - 0.05) { this._detentArmed = true; this._detentParked = false; }
    if (from < floor - 1e-3) this._floorLatched = false;

    if (zoomingIn) {
      // Hard detent at overscale onset: the whole scroll AND its momentum tail are
      // stopped dead here — a "throw" can't punch through. To continue into
      // overscale you scroll AGAIN: a fresh gesture while parked releases it.
      if (detent != null && detent < floor && this._detentArmed && from <= detent + 1e-3 && next > detent) {
        if (this._detentParked && newGesture) { this._detentArmed = false; this._detentParked = false; }
        else { next = detent; this._detentParked = true; }
      }
      // Floor: one small damped over-pull that springs back, then a hard stop for
      // the rest of this contact. Latching keeps a trackpad's momentum tail from
      // re-pulling (buzz) or pushing the spring-back out by ~a second.
      if (next > floor) {
        if (this._floorLatched) { next = floor; }
        else { next = Math.min(hardMax, floor + step * ELASTIC); this._floorLatched = true; this._scheduleSettle(floor); }
      }
    }

    next = Math.max(min, Math.min(next, hardMax));
    this._target = next;
    if (Math.abs(next - map.getZoom()) < 1e-4) return;

    const around = this._getAnchor() || this._cursorLngLat(e);
    map.easeTo({ zoom: next, around, duration: 0 });
  }

  // Spring the elastic over-pull back to the floor on a SHORT FIXED delay from the
  // first contact — not reset by later events, so a trackpad's momentum tail can't
  // hold the spring-back open for ~a second.
  _scheduleSettle(floor) {
    if (this._settleT) return;
    this._settleT = setTimeout(() => {
      this._settleT = 0;
      if (this._map.getZoom() > floor + 1e-3) { this._target = floor; this._map.easeTo({ zoom: floor, duration: SETTLE_MS }); }
    }, SETTLE_DELAY_MS);
  }

  _cursorLngLat(e) {
    const r = this._canvas.getBoundingClientRect();
    return this._map.unproject([e.clientX - r.left, e.clientY - r.top]);
  }
}
