// OwnShip — renders the vessel's own position from the live NMEA0183 feed,
// follows it (with break-out + a re-centre chip), and drives the camera. It is a
// pure consumer of VesselStateStore and renders nothing the chart owns.
//
//   • a heading-rotated SVG Marker (screen-fixed, survives style reloads) — the
//     OpenBridge own-ship glyph (haloed). Rotates by true heading (HDT), or COG
//     when there's no heading sensor.
//   • a geographic course/speed predictor: a dashed line from the vessel along
//     COG, length = SOG × predictMin. A GeoJSON line layer, re-added after style
//     rebuilds.
//
// Follow: by default the camera keeps the vessel centred (the renderer boots in
// north-up, so updateFollow recentres each fix). Panning the map breaks follow
// and shows a "Re-centre" chip; tapping it resumes following. Adding a layer
// before the style is loaded throws, so layer creation is deferred to style.load.

import { OWN_SHIP_MARKER, CENTER_ICON } from "../lib/openbridge-icons.mjs";
import { fmtLatLon } from "./target-info.mjs";
import { format } from "../lib/units.mjs";

const SRC = "ownship-predictor";       // COG/SOG vector (dashed)
const CASING = "ownship-predictor-casing";
const LINE = "ownship-predictor-line";
const HSRC = "ownship-heading";         // heading line (solid, the vessel's nose)
const HCASING = "ownship-heading-casing";
const HLINE = "ownship-heading-line";

// GPS freshness thresholds. A position that hasn't advanced for STALE_MS is shown
// greyed (sensor hiccup); past LOST_MS it's "GPS lost" and the vectors drop. The
// boat is never removed on a dropout — it freezes at the last known fix, which is
// the safe behaviour underway (a vanishing boat reads as "no hazard here").
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

export class OwnShip {
  constructor({ map, plotter, vessel, host, onSelect, units, predictMin = 6 } = {}) {
    this._map = map;
    this._plotter = plotter;
    this._vessel = vessel;
    this._onSelect = onSelect; // tap → info picker
    this._units = units; // () => mariner prefs, for the SOG/STW unit (live)
    this._predictMin = predictMin;
    this._marker = null;
    this._added = false;
    this._centered = false; // recenter once on the first fix so the boat is on-screen
    this._follow = true; // keep the vessel centred until the user pans away
    this._fix = null; // latest {lng, lat, courseDeg}
    this._last = null; // last predictor GeoJSON (to restore after style reload)
    // Smooth display: the rendered pose is interpolated fix→fix (in step with the
    // camera ease in <chart-canvas>) so the boat glides instead of hopping each fix.
    this._renderLng = null; this._renderLat = null; this._renderRot = 0; // current drawn pose
    this._predict = null;   // {course, sog} for the predictor, from the latest fix
    this._poseRAF = 0;      // in-flight pose tween
    this._lastFixTs = 0;    // ms of the previous fix (to size the tween to the gap)
    // GPS freshness: _freshWall is the wall-clock of the last position that actually
    // advanced (moved, or its fix clock ticked). The watchdog ages it into
    // live/stale/lost so a frozen feed is caught even when other sentences (depth,
    // wind) keep the snapshot — and thus onChange — flowing.
    this._freshWall = 0;
    this._posKey = null;    // last "lat,lon" seen, to spot a moving fix
    this._fixClock = 0;     // last navigation.datetime (ms), to spot a stationary-but-live fix
    this._gps = "none";     // none | live | stale | lost

    this._el = document.createElement("div");
    this._el.style.cssText = "pointer-events:auto;cursor:pointer;will-change:transform";
    this._el.innerHTML = OWN_SHIP_MARKER;
    this._el.addEventListener("click", (e) => {
      e.stopPropagation(); // don't let the map's click handler dismiss the picker we're opening
      this._select(e);
    });

    this._chip = this._makeChip(host);
    this._gpsChip = this._makeGpsChip(host);

    // Pan and rotate both mean "I want to set my own view", so both break follow;
    // pinch/wheel-zoom keeps it (vessel-anchored). We guard on originalEvent so only
    // real user gestures count — our own programmatic eases (recenter, and the
    // per-fix bearing hold in course-/head-up) fire rotate events WITHOUT one.
    this._onGesture = (e) => { if (!e || e.originalEvent) this._setFollow(false); };
    map.on("dragstart", this._onGesture);
    map.on("rotatestart", this._onGesture);

    // GPS-freshness watchdog: ages the last fix into live/stale/lost independent of
    // the feed cadence (a frozen GPS still pushes depth/wind deltas, so we can't
    // wait on onChange to notice the position stopped).
    this._gpsTimer = setInterval(() => this._tickGps(), 1000);

    // Defer layer creation until the style is ready (see _ensureLayers); subscribe
    // first so a not-yet-loaded style can't abort the constructor before we do.
    this._onStyle = () => this._ensureLayers();
    map.on("style.load", this._onStyle);
    this._ensureLayers();

    this._off = vessel.onChange((s) => this._update(s));
    this._update(vessel.state);
  }

  _makeChip(host) {
    if (!host) return null;
    const style = document.createElement("style");
    style.textContent = CHIP_STYLE;
    host.appendChild(style);
    const btn = document.createElement("button");
    btn.id = "ownship-recenter";
    btn.type = "button";
    btn.title = "Centre on vessel";
    btn.hidden = true;
    btn.innerHTML = `${CENTER_ICON}<span>Re-centre</span>`;
    btn.onclick = () => {
      this._setFollow(true);
      this._recenter();
    };
    host.appendChild(btn);
    return btn;
  }

  // A non-interactive status pill (top-centre) shown only when the fix goes
  // stale/lost. Mirrors the recenter chip's host-mounted pattern.
  _makeGpsChip(host) {
    if (!host) return null;
    const el = document.createElement("div");
    el.id = "ownship-gps";
    el.hidden = true;
    host.appendChild(el);
    return el;
  }

  _setFollow(on) {
    this._follow = on;
    this._syncChip();
  }

  // Watchdog tick: age the last fresh fix into live/stale/lost and reflect it.
  _tickGps() {
    if (!this._freshWall || !this._fix) return;
    const age = Date.now() - this._freshWall;
    const next = age > LOST_MS ? "lost" : age > STALE_MS ? "stale" : "live";
    if (next !== this._gps) this._applyGps(next);
  }

  // Reflect GPS freshness: grey/fade the glyph, drop the vectors once not live, and
  // show the status pill. Never removes the boat — it stays frozen at the last fix.
  _applyGps(status) {
    this._gps = status;
    this._el.style.filter =
      status === "lost" ? "grayscale(1) opacity(.4)"
      : status === "stale" ? "grayscale(1) opacity(.6)"
      : "";
    if (status !== "live") this._clearVectors(); // a frozen heading/COG vector would mislead
    if (this._gpsChip) {
      const lost = status === "lost";
      this._gpsChip.hidden = status === "live" || status === "none";
      this._gpsChip.className = lost ? "lost" : status === "stale" ? "stale" : "";
      this._gpsChip.textContent = lost ? "GPS lost" : "Position stale";
    }
  }

  // Plugin contract (consumed by WheelZoom via the shell): the geographic point
  // wheel-zoom should keep fixed while zooming — the vessel while we're following
  // a fix, else null so it falls back to cursor-anchored zoom.
  zoomAnchor() {
    return (this._follow && this._fix) ? [this._fix.lng, this._fix.lat] : null;
  }

  _syncChip() {
    if (this._chip) this._chip.hidden = this._follow || !this._fix;
  }

  // Animate the camera back to the vessel and resume following.
  _recenter() {
    if (!this._fix) return;
    this._map.easeTo({ center: [this._fix.lng, this._fix.lat], duration: 500 });
    this._plotter.updateFollow(this._fix);
  }

  // (Re)create the predictor source + layers — also after a style rebuild, which
  // drops plugin sources/layers (the DOM Marker survives). Adding a source/layer
  // before the style loads throws, so defer until it's ready; style.load calls back.
  _ensureLayers() {
    const map = this._map;
    if (!map.isStyleLoaded()) return; // style.load will call this again
    if (!map.getSource(SRC)) {
      map.addSource(SRC, { type: "geojson", data: this._last || EMPTY });
    }
    this._plotter.addOverlayLayer(
      { id: CASING, type: "line", source: SRC, layout: { "line-cap": "round" },
        paint: { "line-color": "#fff", "line-width": 4, "line-opacity": 0.9 } },
      { belowLabels: true },
    );
    this._plotter.addOverlayLayer(
      { id: LINE, type: "line", source: SRC, layout: { "line-cap": "round" },
        paint: { "line-color": "#16324f", "line-width": 1.8, "line-dasharray": [2, 1.8] } },
      { belowLabels: true },
    );
    // Heading line (HDT, the vessel's nose): SOLID, distinct from the dashed COG
    // vector. The gap between the two is what reveals being set by current/wind.
    if (!map.getSource(HSRC)) {
      map.addSource(HSRC, { type: "geojson", data: this._lastH || EMPTY });
    }
    this._plotter.addOverlayLayer(
      { id: HCASING, type: "line", source: HSRC, layout: { "line-cap": "round" },
        paint: { "line-color": "#fff", "line-width": 4, "line-opacity": 0.9 } },
      { belowLabels: true },
    );
    this._plotter.addOverlayLayer(
      { id: HLINE, type: "line", source: HSRC, layout: { "line-cap": "round" },
        paint: { "line-color": "#16324f", "line-width": 1.8 } },
      { belowLabels: true },
    );
  }

  _update(s) {
    const nav = s && s.navigation;
    const pos = nav && nav.position;
    if (!pos || typeof pos.lat !== "number") {
      // No position in the feed. If we've never had one, there's nothing to show.
      // If we had a fix, keep it frozen and let the watchdog age it to lost — don't
      // make the boat disappear.
      if (!this._fix) this._hide();
      return;
    }

    // Freshness: the fix is "fresh" if it moved, or its own clock (RMC/ZDA datetime)
    // advanced — the latter catches a healthy GPS at anchor. Either way, stamp the
    // wall clock the watchdog ages.
    const key = pos.lat.toFixed(6) + "," + pos.lon.toFixed(6);
    const clock = nav.datetime ? Date.parse(nav.datetime) : 0;
    if (key !== this._posKey || (clock && clock !== this._fixClock) || !this._freshWall) {
      this._freshWall = Date.now();
      if (this._gps !== "live") this._applyGps("live");
    }
    this._posKey = key;
    if (clock) this._fixClock = clock;

    // Self-heal the vector sources/layers each fix (~1 Hz, idempotent). The plugin
    // is built on the canvas `ready` event — after the initial `style.load` has
    // already fired — and the chart rebuilds its style with setStyle({diff:false}),
    // which drops plugin layers. Relying on the style.load listener alone left the
    // sources never (re)added, so the predictor + heading line never drew.
    this._ensureLayers();

    const lng = pos.lon;
    const lat = pos.lat;
    // Heading priority: true heading (HDT) → magnetic heading + variation (most
    // boats send HDG, not HDT) → COG. COG is last because it's noisy at low speed,
    // which made the marker spin far more than the vessel actually turns.
    const magTrue = num(nav.headingMagnetic) != null
      ? num(nav.headingMagnetic) + (num(nav.magneticVariation) ?? 0)
      : null;
    const heading = num(nav.headingTrue) ?? magTrue ?? num(nav.cogTrue) ?? 0;
    const course = num(nav.cogTrue) ?? num(nav.headingTrue) ?? magTrue;
    const sog = num(nav.sog) ?? 0;
    this._fix = { lng, lat, courseDeg: typeof course === "number" ? course : heading, headingDeg: heading };

    // Predictor params for this fix; the dashed line geometry is rebuilt from the
    // *interpolated* position each tween frame (in _renderPose) so it tracks too.
    this._predict = (sog > 0.2 && typeof course === "number") ? { course, sog } : null;

    // Tween the drawn pose (position + heading + predictor) from where it is now to
    // this fix, sized to the gap since the last fix (linear) so it finishes about
    // when the next fix arrives — gliding in step with the camera ease in
    // <chart-canvas> rather than hopping. First fix / teleport / reduced-motion → snap.
    const now = Date.now();
    const gap = this._lastFixTs ? now - this._lastFixTs : 0;
    this._lastFixTs = now;
    let dur = 0;
    if (this._renderLng != null && gap > 0 && gap < 2500 && !reduceMotion()) {
      dur = clamp(gap, 200, 1200);
      const a = this._map.project([this._renderLng, this._renderLat]);
      const b = this._map.project([lng, lat]);
      if (Math.hypot(b.x - a.x, b.y - a.y) > 400) dur = 0; // big jump → snap, don't crawl
    }
    this._animatePose(lng, lat, heading, dur);

    // Bring the boat on-screen on the first fix regardless of camera mode.
    if (!this._centered) {
      this._centered = true;
      const z = this._map.getZoom();
      this._map.easeTo({ center: [lng, lat], zoom: z < 10 ? 13 : z, duration: 700 });
    } else if (this._follow) {
      // Keep centred (a no-op in the renderer's "free" mode). Throttle so bursts
      // of fixes don't stack camera eases (which makes MapLibre throw "already
      // running"); the recentre ease is short and 1 Hz fixes pass straight through.
      const t = Date.now();
      if (t - (this._lastFollow || 0) > 250) {
        this._lastFollow = t;
        this._plotter.updateFollow(this._fix);
      }
    }
    this._syncChip();
  }

  // Draw the vessel at an exact pose: the heading-rotated Marker plus the
  // course/speed predictor rebuilt from THIS (possibly interpolated) position so
  // the dashed line's origin tracks the boat. Records the drawn pose so the next
  // fix can tween from it.
  _renderPose(lng, lat, rot) {
    if (!this._marker) {
      this._marker = new window.maplibregl.Marker({ element: this._el, rotationAlignment: "map", anchor: "center" });
    }
    this._marker.setLngLat([lng, lat]).setRotation(rot);
    if (!this._added) { this._marker.addTo(this._map); this._added = true; }
    // Vectors are dropped while the fix is stale/lost (a frozen vector misleads).
    const live = this._gps === "live" || this._gps === "none";
    // COG/SOG vector (dashed): along course, length = SOG × predictMin.
    let data = EMPTY;
    if (live && this._predict) {
      const end = destination(lat, lng, this._predict.course, this._predict.sog * (this._predictMin / 60));
      data = seg([lng, lat], end);
    }
    // Heading line (solid): along the rendered heading (`rot`). Matches the COG
    // vector's length under way so the crab angle is visible; a short fixed line at
    // rest so the bow is still shown at anchor.
    let hdata = EMPTY;
    if (live) {
      const hlen = this._predict ? this._predict.sog * (this._predictMin / 60) : 0.3;
      hdata = seg([lng, lat], destination(lat, lng, rot, hlen));
    }
    this._last = data; this._lastH = hdata;
    const src = this._map.getSource(SRC);
    if (src) src.setData(data);
    const hsrc = this._map.getSource(HSRC);
    if (hsrc) hsrc.setData(hdata);
    this._renderLng = lng; this._renderLat = lat; this._renderRot = rot;
  }

  // Clear both vector sources (used when the fix goes stale/lost).
  _clearVectors() {
    this._last = EMPTY; this._lastH = EMPTY;
    const src = this._map && this._map.getSource(SRC);
    if (src) src.setData(EMPTY);
    const hsrc = this._map && this._map.getSource(HSRC);
    if (hsrc) hsrc.setData(EMPTY);
  }

  // Tween the drawn pose to the new fix over `dur` ms (linear). Bearing takes the
  // shortest angular path. dur<=0 (or no prior pose) snaps. A new fix mid-tween
  // cancels and re-tweens from the current interpolated pose.
  _animatePose(toLng, toLat, toRot, dur) {
    if (this._poseRAF) { cancelAnimationFrame(this._poseRAF); this._poseRAF = 0; }
    const fromLng = this._renderLng, fromLat = this._renderLat, fromRot = this._renderRot;
    if (dur <= 0 || fromLng == null) { this._renderPose(toLng, toLat, toRot); return; }
    const dRot = ((toRot - fromRot + 540) % 360) - 180; // shortest path, no 360° backspin
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
    if (!this._onSelect || !this._fix) return;
    const nav = (this._vessel.state || {}).navigation || {};
    const rows = [["Position", fmtLatLon(this._fix.lat, this._fix.lng)]];
    const hdg = num(nav.headingTrue) ??
      (num(nav.headingMagnetic) != null ? num(nav.headingMagnetic) + (num(nav.magneticVariation) ?? 0) : null);
    if (hdg != null) rows.push(["Heading", Math.round(hdg) + "°T"]);
    if (num(nav.cogTrue) != null) rows.push(["COG", Math.round(nav.cogTrue) + "°T"]);
    const u = (this._units && this._units()) || null;
    if (num(nav.sog) != null) rows.push(["SOG", format("speed", nav.sog, u)]);
    if (num(nav.speedThroughWater) != null) rows.push(["STW", format("speed", nav.speedThroughWater, u)]);
    this._onSelect({ title: "Own ship", rows, x: e.clientX, y: e.clientY });
  }

  _hide() {
    if (this._poseRAF) { cancelAnimationFrame(this._poseRAF); this._poseRAF = 0; }
    this._renderLng = null; this._renderLat = null; // next fix snaps, not crawls from a stale pose
    if (this._marker && this._added) {
      this._marker.remove();
      this._added = false;
    }
    this._fix = null;
    this._freshWall = 0; this._posKey = null; this._fixClock = 0;
    this._gps = "none";
    this._el.style.filter = "";
    if (this._gpsChip) this._gpsChip.hidden = true;
    this._clearVectors();
    this._syncChip();
  }

  destroy() {
    if (this._poseRAF) cancelAnimationFrame(this._poseRAF);
    if (this._gpsTimer) clearInterval(this._gpsTimer);
    if (this._off) this._off();
    if (this._onStyle) this._map.off("style.load", this._onStyle);
    if (this._onGesture) { this._map.off("dragstart", this._onGesture); this._map.off("rotatestart", this._onGesture); }
    if (this._marker) this._marker.remove();
    if (this._chip) this._chip.remove();
    if (this._gpsChip) this._gpsChip.remove();
    this._plotter.removeOverlay([CASING, LINE], SRC);
    this._plotter.removeOverlay([HCASING, HLINE], HSRC);
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
