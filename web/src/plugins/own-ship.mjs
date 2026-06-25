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

const SRC = "ownship-predictor";
const CASING = "ownship-predictor-casing";
const LINE = "ownship-predictor-line";

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
`;

const EMPTY = { type: "FeatureCollection", features: [] };

export class OwnShip {
  constructor({ map, plotter, vessel, host, onSelect, units, wheelZoom, predictMin = 6 } = {}) {
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

    this._el = document.createElement("div");
    this._el.style.cssText = "pointer-events:auto;cursor:pointer;will-change:transform";
    this._el.innerHTML = OWN_SHIP_MARKER;
    this._el.addEventListener("click", (e) => {
      e.stopPropagation(); // don't let the map's click handler dismiss the picker we're opening
      this._select(e);
    });

    this._chip = this._makeChip(host);

    // A user pan breaks follow; programmatic eases (our own recenters) don't fire
    // dragstart, so this only triggers on a real gesture.
    this._onDrag = () => this._setFollow(false);
    map.on("dragstart", this._onDrag);

    // While following, wheel-zoom anchors on the vessel rather than the cursor.
    // WheelZoom owns the wheel (detent + elastic floor); we just feed it the anchor
    // so it zooms around the boat whenever we're following a fix. null → cursor.
    if (wheelZoom) wheelZoom.setAnchorProvider(() => (this._follow && this._fix) ? [this._fix.lng, this._fix.lat] : null);

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

  _setFollow(on) {
    this._follow = on;
    this._syncChip();
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
  }

  _update(s) {
    const pos = s && s.navigation && s.navigation.position;
    if (!pos || typeof pos.lat !== "number") {
      this._hide();
      return;
    }
    const nav = s.navigation;
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
    let data = EMPTY;
    if (this._predict) {
      const end = destination(lat, lng, this._predict.course, this._predict.sog * (this._predictMin / 60));
      data = { type: "FeatureCollection", features: [{ type: "Feature", geometry: { type: "LineString", coordinates: [[lng, lat], end] } }] };
    }
    this._last = data;
    const src = this._map.getSource(SRC);
    if (src) src.setData(data);
    this._renderLng = lng; this._renderLat = lat; this._renderRot = rot;
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
    this._last = EMPTY;
    const src = this._map && this._map.getSource(SRC);
    if (src) src.setData(EMPTY);
    this._syncChip();
  }

  destroy() {
    if (this._poseRAF) cancelAnimationFrame(this._poseRAF);
    if (this._off) this._off();
    if (this._onStyle) this._map.off("style.load", this._onStyle);
    if (this._onDrag) this._map.off("dragstart", this._onDrag);
    if (this._marker) this._marker.remove();
    if (this._chip) this._chip.remove();
    this._plotter.removeOverlay([CASING, LINE], SRC);
  }
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
