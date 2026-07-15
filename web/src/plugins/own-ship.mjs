// own-ship — the vessel's own position, a builtin plugin (core.own-ship). It renders
// the OpenBridge own-ship glyph (a DOM marker), a dashed COG/SOG predictor and a
// solid heading line, follows the vessel (break-out + a re-centre chip), and shows a
// GPS-freshness pill. It is a pure consumer of the plugin ctx: no raw map/plotter —
// the glyph is a ctx.markers handle, the vectors are ctx.layers (self-healing across
// style rebuilds), and the camera/follow + wheel-zoom anchor go through ctx.camera.
//
// This module reads exactly like a plugin an author would write against ctx; it is
// registered as a builtin by the shell (web/src/chartplotter.mjs) through PluginHost.
//
//   • a heading-rotated glyph marker (survives style reloads). Rotates by true
//     heading (HDT), or COG when there's no heading sensor.
//   • a geographic course/speed predictor (dashed) + a solid heading line.
//
// Follow: the camera keeps the vessel centred until the user pans/rotates, which
// breaks follow and shows a "Re-centre" chip; tapping it resumes following.

import { OWN_SHIP_MARKER, CENTER_ICON } from "../lib/openbridge-icons.mjs";
import { fmtLatLon } from "./target-info.mjs";

// GPS freshness thresholds. A position that hasn't advanced for STALE_MS is shown
// greyed (sensor hiccup); past LOST_MS it's "GPS lost" and the vectors drop. The
// boat is never removed on a dropout — it freezes at the last known fix.
const STALE_MS = 6000;
const LOST_MS = 20000;

const CHIP_STYLE = `
  #ownship-recenter {
    position: absolute; left: 50%; transform: translateX(-50%);
    bottom: calc(var(--botbar-h, 0px) + 78px); z-index: 7;
    display: inline-flex; align-items: center; gap: 7px;
    padding: 8px 14px 8px 11px; border-radius: 22px; cursor: pointer;
    font: 600 13px/1 system-ui, sans-serif;
    color: var(--ui-accent-text, #fff); background: var(--ui-accent, #2f81f7);
    border: 1px solid var(--ui-accent, #2f81f7);
    box-shadow: 0 3px 14px rgba(0,0,0,.28);
    transition: transform .08s, filter .12s;
  }
  #ownship-recenter:hover { filter: brightness(1.07); }
  #ownship-recenter:active { transform: translateX(-50%) scale(.95); }
  #ownship-recenter svg { width: 17px; height: 17px; display: block; }
  #ownship-recenter[hidden] { display: none; }
  #ownship-gps {
    position: absolute; left: 50%; transform: translateX(-50%);
    top: calc(var(--topbar-h, 0px) + 12px); z-index: 7;
    display: inline-flex; align-items: center; gap: 7px;
    padding: 6px 13px; border-radius: 20px; pointer-events: none;
    font: 600 12px/1 system-ui, sans-serif; color: #1a1300;
    box-shadow: 0 3px 14px rgba(0,0,0,.28);
  }
  #ownship-gps::before {
    content: ""; width: 8px; height: 8px; border-radius: 50%; background: currentColor;
  }
  #ownship-gps.stale { background: #f5b301; }
  #ownship-gps.lost { background: #e5484d; color: #fff; }
  #ownship-gps[hidden] { display: none; }
`;

const EMPTY = { type: "FeatureCollection", features: [] };

export default class OwnShip {
  constructor(ctx, { predictMin = 6 } = {}) {
    this.ctx = ctx;
    this._predictMin = predictMin;
    this._added = false;
    this._centered = false; // recenter once on the first fix so the boat is on-screen
    this._follow = true; // keep the vessel centred until the user pans away
    this._fix = null; // latest {lng, lat, courseDeg}
    this._last = null; // last predictor GeoJSON
    this._renderLng = null; this._renderLat = null; this._renderRot = 0; // current drawn pose
    this._predict = null;   // {course, sog} for the predictor, from the latest fix
    this._poseRAF = 0;
    this._lastFixTs = 0;
    this._freshWall = 0;
    this._posKey = null;
    this._fixClock = 0;
    this._gps = "none";     // none | live | stale | lost
    this._cleanup = [];
  }

  start() {
    const ctx = this.ctx;

    // The own-ship glyph: a rotated DOM marker (survives style reloads).
    this._marker = ctx.markers.add("own-ship", { rotationAlignment: "map", anchor: "center" });
    this._marker.setStyle("pointer-events:auto;cursor:pointer;will-change:transform");
    this._marker.setHTML(OWN_SHIP_MARKER);
    this._marker.onClick((e) => { e.stopPropagation(); this._select(e); });

    // Vectors: COG/SOG predictor (dashed) + heading line (solid), each a casing +
    // line pair sharing one source, in the overlay z-band (beneath S-52 labels).
    // The layer host re-adds + reseeds these across style rebuilds.
    this._predLayer = ctx.layers.add("predictor", {
      band: "overlay",
      layers: [
        { type: "line", layout: { "line-cap": "round" }, paint: { "line-color": "#fff", "line-width": 4, "line-opacity": 0.9 } },
        { type: "line", layout: { "line-cap": "round" }, paint: { "line-color": "#16324f", "line-width": 1.8, "line-dasharray": [2, 1.8] } },
      ],
    });
    this._headLayer = ctx.layers.add("heading", {
      band: "overlay",
      layers: [
        { type: "line", layout: { "line-cap": "round" }, paint: { "line-color": "#fff", "line-width": 4, "line-opacity": 0.9 } },
        { type: "line", layout: { "line-cap": "round" }, paint: { "line-color": "#16324f", "line-width": 1.8 } },
      ],
    });

    // Floating chrome: the re-centre chip + the GPS status pill (theme vars inherit).
    const mount = ctx.hud.mount("own-ship");
    const style = document.createElement("style");
    style.textContent = CHIP_STYLE;
    mount.appendChild(style);
    const btn = document.createElement("button");
    btn.id = "ownship-recenter";
    btn.type = "button";
    btn.title = "Centre on vessel";
    btn.hidden = true;
    btn.innerHTML = `${CENTER_ICON}<span>Re-centre</span>`;
    btn.onclick = () => { this._setFollow(true); this._recenter(); };
    mount.appendChild(btn);
    this._chip = btn;
    const pill = document.createElement("div");
    pill.id = "ownship-gps";
    pill.hidden = true;
    mount.appendChild(pill);
    this._gpsChip = pill;

    // Pan/rotate (real gestures only — the ctx guards on originalEvent) break follow.
    ctx.camera.onGesture(() => this._setFollow(false));

    // Wheel-zoom anchor: keep the vessel fixed while zooming when we're following.
    ctx.camera.registerFollowAnchor(() => (this._follow && this._fix) ? [this._fix.lng, this._fix.lat] : null);

    // GPS-freshness watchdog (ages the fix independent of the feed cadence).
    this._gpsTimer = setInterval(() => this._tickGps(), 1000);

    // Live vessel data (≤4 Hz coalesced).
    ctx.vessel.subscribe((s) => this._update(s));
    this._update(ctx.vessel.get());
  }

  _setFollow(on) {
    this._follow = on;
    this._syncChip();
  }

  _tickGps() {
    if (!this._freshWall || !this._fix) return;
    const age = Date.now() - this._freshWall;
    const next = age > LOST_MS ? "lost" : age > STALE_MS ? "stale" : "live";
    if (next !== this._gps) this._applyGps(next);
  }

  // Reflect GPS freshness: grey/fade the glyph, drop the vectors once not live, show
  // the status pill. Never removes the boat — it stays frozen at the last fix.
  _applyGps(status) {
    this._gps = status;
    this._marker.element.style.filter =
      status === "lost" ? "grayscale(1) opacity(.4)"
      : status === "stale" ? "grayscale(1) opacity(.6)"
      : "";
    if (status !== "live") this._clearVectors();
    if (this._gpsChip) {
      const lost = status === "lost";
      this._gpsChip.hidden = status === "live" || status === "none";
      this._gpsChip.className = lost ? "lost" : status === "stale" ? "stale" : "";
      this._gpsChip.textContent = lost ? "GPS lost" : "Position stale";
    }
  }

  _syncChip() {
    if (this._chip) this._chip.hidden = this._follow || !this._fix;
  }

  // Animate the camera back to the vessel and resume following.
  _recenter() {
    if (!this._fix) return;
    this.ctx.camera.easeTo({ center: [this._fix.lng, this._fix.lat], duration: 500 });
    this.ctx.camera.follow(this._fix);
  }

  _update(s) {
    const nav = s && s.navigation;
    const pos = nav && nav.position;
    if (!pos || typeof pos.lat !== "number") {
      if (!this._fix) this._hide();
      return;
    }

    const key = pos.lat.toFixed(6) + "," + pos.lon.toFixed(6);
    const clock = nav.datetime ? Date.parse(nav.datetime) : 0;
    if (key !== this._posKey || (clock && clock !== this._fixClock) || !this._freshWall) {
      this._freshWall = Date.now();
      if (this._gps !== "live") this._applyGps("live");
    }
    this._posKey = key;
    if (clock) this._fixClock = clock;

    const lng = pos.lon;
    const lat = pos.lat;
    // Heading priority: true (HDT) → magnetic + variation → COG (noisy at low speed).
    const magTrue = num(nav.headingMagnetic) != null
      ? num(nav.headingMagnetic) + (num(nav.magneticVariation) ?? 0)
      : null;
    const heading = num(nav.headingTrue) ?? magTrue ?? num(nav.cogTrue) ?? 0;
    const course = num(nav.cogTrue) ?? num(nav.headingTrue) ?? magTrue;
    const sog = num(nav.sog) ?? 0;
    this._fix = { lng, lat, courseDeg: typeof course === "number" ? course : heading, headingDeg: heading };

    this._predict = (sog > 0.2 && typeof course === "number") ? { course, sog } : null;

    // Tween the drawn pose from where it is now to this fix, sized to the inter-fix
    // gap so it finishes about when the next fix arrives. First fix / teleport /
    // reduced-motion → snap.
    const now = Date.now();
    const gap = this._lastFixTs ? now - this._lastFixTs : 0;
    this._lastFixTs = now;
    let dur = 0;
    if (this._renderLng != null && gap > 0 && gap < 2500 && !reduceMotion()) {
      dur = clamp(gap, 200, 1200);
      const a = this.ctx.camera.project([this._renderLng, this._renderLat]);
      const b = this.ctx.camera.project([lng, lat]);
      if (Math.hypot(b.x - a.x, b.y - a.y) > 400) dur = 0; // big jump → snap
    }
    this._animatePose(lng, lat, heading, dur);

    // Bring the boat on-screen on the first fix regardless of camera mode.
    if (!this._centered) {
      this._centered = true;
      const z = this.ctx.camera.getZoom();
      this.ctx.camera.easeTo({ center: [lng, lat], zoom: z < 10 ? 13 : z, duration: 700 });
    } else if (this._follow) {
      // Keep centred; throttle so bursts of fixes don't stack camera eases.
      const t = Date.now();
      if (t - (this._lastFollow || 0) > 250) {
        this._lastFollow = t;
        this.ctx.camera.follow(this._fix);
      }
    }
    this._syncChip();
  }

  // Draw the vessel at an exact pose: the rotated marker plus the predictor + heading
  // line rebuilt from THIS (possibly interpolated) position. Records the pose so the
  // next fix can tween from it.
  _renderPose(lng, lat, rot) {
    this._marker.setLngLat([lng, lat]).setRotation(rot);
    this._added = true;
    const live = this._gps === "live" || this._gps === "none";
    let data = EMPTY;
    if (live && this._predict) {
      const end = destination(lat, lng, this._predict.course, this._predict.sog * (this._predictMin / 60));
      data = seg([lng, lat], end);
    }
    let hdata = EMPTY;
    if (live) {
      const hlen = this._predict ? this._predict.sog * (this._predictMin / 60) : 0.3;
      hdata = seg([lng, lat], destination(lat, lng, rot, hlen));
    }
    this._last = data; this._lastH = hdata;
    this._predLayer.setData(data);
    this._headLayer.setData(hdata);
    this._renderLng = lng; this._renderLat = lat; this._renderRot = rot;
  }

  _clearVectors() {
    this._last = EMPTY; this._lastH = EMPTY;
    if (this._predLayer) this._predLayer.setData(EMPTY);
    if (this._headLayer) this._headLayer.setData(EMPTY);
  }

  // Tween the drawn pose to the new fix over `dur` ms (linear). Bearing takes the
  // shortest angular path. dur<=0 (or no prior pose) snaps.
  _animatePose(toLng, toLat, toRot, dur) {
    if (this._poseRAF) { cancelAnimationFrame(this._poseRAF); this._poseRAF = 0; }
    const fromLng = this._renderLng, fromLat = this._renderLat, fromRot = this._renderRot;
    if (dur <= 0 || fromLng == null) { this._renderPose(toLng, toLat, toRot); return; }
    const dRot = ((toRot - fromRot + 540) % 360) - 180;
    const start = Date.now();
    const step = () => {
      const t = Math.min(1, (Date.now() - start) / dur);
      this._renderPose(fromLng + (toLng - fromLng) * t, fromLat + (toLat - fromLat) * t, fromRot + dRot * t);
      this._poseRAF = t < 1 ? requestAnimationFrame(step) : 0;
    };
    this._poseRAF = requestAnimationFrame(step);
  }

  // Tap → info picker with the vessel's live nav data.
  _select(e) {
    if (!this._fix) return;
    const nav = (this.ctx.vessel.get() || {}).navigation || {};
    const rows = [["Position", fmtLatLon(this._fix.lat, this._fix.lng)]];
    const hdg = num(nav.headingTrue) ??
      (num(nav.headingMagnetic) != null ? num(nav.headingMagnetic) + (num(nav.magneticVariation) ?? 0) : null);
    if (hdg != null) rows.push(["Heading", Math.round(hdg) + "°T"]);
    if (num(nav.cogTrue) != null) rows.push(["COG", Math.round(nav.cogTrue) + "°T"]);
    if (num(nav.sog) != null) rows.push(["SOG", this.ctx.units.format("speed", nav.sog)]);
    if (num(nav.speedThroughWater) != null) rows.push(["STW", this.ctx.units.format("speed", nav.speedThroughWater)]);
    this.ctx.callout.show({ title: "Own ship", rows, x: e.clientX, y: e.clientY });
  }

  _hide() {
    if (this._poseRAF) { cancelAnimationFrame(this._poseRAF); this._poseRAF = 0; }
    this._renderLng = null; this._renderLat = null;
    if (this._marker) this._marker.hide();
    this._added = false;
    this._fix = null;
    this._freshWall = 0; this._posKey = null; this._fixClock = 0;
    this._gps = "none";
    if (this._marker) this._marker.element.style.filter = "";
    if (this._gpsChip) this._gpsChip.hidden = true;
    this._clearVectors();
    this._syncChip();
  }

  // The host tears down layers, gesture/anchor listeners, the mount, and the vessel
  // subscription (all ctx-tracked); we own the marker + timers/RAF.
  destroy() {
    if (this._poseRAF) cancelAnimationFrame(this._poseRAF);
    if (this._gpsTimer) clearInterval(this._gpsTimer);
    if (this._marker) this._marker.remove();
  }
}

// seg builds a one-segment LineString FeatureCollection from a→b.
function seg(a, b) {
  return { type: "FeatureCollection", features: [{ type: "Feature", geometry: { type: "LineString", coordinates: [a, b] } }] };
}

// num coerces a finite number or returns null (so `?? fallback` works and 0 is kept).
function num(v) {
  return typeof v === "number" && isFinite(v) ? v : null;
}

function clamp(v, lo, hi) {
  return Math.max(lo, Math.min(hi, v));
}

// Honour the OS "reduce motion" setting: callers snap instead of tweening.
function reduceMotion() {
  return typeof matchMedia === "function" && matchMedia("(prefers-reduced-motion: reduce)").matches;
}

// destination computes the lat/lon reached from (lat,lon) along bearingDeg for
// distNm nautical miles on a great circle.
function destination(lat, lon, bearingDeg, distNm) {
  const R = 3440.065; // earth radius in nm
  const br = (bearingDeg * Math.PI) / 180;
  const d = distNm / R;
  const lat1 = (lat * Math.PI) / 180;
  const lon1 = (lon * Math.PI) / 180;
  const lat2 = Math.asin(Math.sin(lat1) * Math.cos(d) + Math.cos(lat1) * Math.sin(d) * Math.cos(br));
  const lon2 = lon1 + Math.atan2(Math.sin(br) * Math.sin(d) * Math.cos(lat1), Math.cos(d) - Math.sin(lat1) * Math.sin(lat2));
  return [(lon2 * 180) / Math.PI, (lat2 * 180) / Math.PI];
}
