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
  constructor({ map, plotter, vessel, host, predictMin = 6 } = {}) {
    this._map = map;
    this._plotter = plotter;
    this._vessel = vessel;
    this._predictMin = predictMin;
    this._marker = null;
    this._added = false;
    this._centered = false; // recenter once on the first fix so the boat is on-screen
    this._follow = true; // keep the vessel centred until the user pans away
    this._fix = null; // latest {lng, lat, courseDeg}
    this._zoomTakeover = false; // whether we've taken over the wheel from native scroll-zoom
    this._last = null; // last predictor GeoJSON (to restore after style reload)

    this._el = document.createElement("div");
    this._el.style.cssText = "pointer-events:none;will-change:transform";
    this._el.innerHTML = OWN_SHIP_MARKER;

    this._chip = this._makeChip(host);

    // A user pan breaks follow; programmatic eases (our own recenters) don't fire
    // dragstart, so this only triggers on a real gesture.
    this._onDrag = () => this._setFollow(false);
    map.on("dragstart", this._onDrag);

    // While following, zoom anchors on the vessel, not the cursor. We can't just
    // recentre each zoom frame — that cancels MapLibre's scroll-zoom animation and
    // makes zooming crawl. Instead, disable native scroll-zoom and handle the wheel
    // ourselves: an instant zoom around the boat (instant, so rapid scrolls add up).
    // Panning re-enables native cursor-anchored zoom until re-centre.
    this._canvas = map.getCanvasContainer();
    this._onWheel = (e) => {
      if (!this._follow || !this._fix) return;
      e.preventDefault();
      let d = e.deltaY;
      if (e.deltaMode === 1) d *= 28; // lines → ~pixels
      else if (e.deltaMode === 2) d *= 400; // pages
      const z = clamp(map.getZoom() - d * 0.006, map.getMinZoom(), map.getMaxZoom());
      map.easeTo({ zoom: z, around: [this._fix.lng, this._fix.lat], duration: 0 });
    };
    this._applyFollowZoom();

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
    this._applyFollowZoom();
    this._syncChip();
  }

  // Take over the wheel only while following AND we have a fix to anchor on;
  // otherwise leave native (cursor-anchored) scroll-zoom enabled — including before
  // the first fix, so chart browsing zooms normally. Idempotent.
  _applyFollowZoom() {
    if (!this._canvas) return;
    const takeover = this._follow && !!this._fix;
    if (takeover === this._zoomTakeover) return;
    this._zoomTakeover = takeover;
    if (takeover) {
      this._map.scrollZoom.disable();
      this._canvas.addEventListener("wheel", this._onWheel, { passive: false });
    } else {
      this._canvas.removeEventListener("wheel", this._onWheel, { passive: false });
      this._map.scrollZoom.enable();
    }
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
    this._fix = { lng, lat, courseDeg: typeof course === "number" ? course : heading };

    // Marker (heading-rotated, screen-fixed).
    if (!this._marker) {
      this._marker = new window.maplibregl.Marker({ element: this._el, rotationAlignment: "map", anchor: "center" });
    }
    this._marker.setLngLat([lng, lat]).setRotation(heading);
    if (!this._added) {
      this._marker.addTo(this._map);
      this._added = true;
    }

    // Course/speed predictor (geographic). Drawn only when actually moving.
    if (sog > 0.2 && typeof course === "number") {
      const end = destination(lat, lng, course, sog * (this._predictMin / 60));
      this._last = {
        type: "FeatureCollection",
        features: [{ type: "Feature", geometry: { type: "LineString", coordinates: [[lng, lat], end] } }],
      };
    } else {
      this._last = EMPTY;
    }
    const src = this._map.getSource(SRC);
    if (src) src.setData(this._last);

    // Bring the boat on-screen on the first fix regardless of camera mode.
    if (!this._centered) {
      this._centered = true;
      const z = this._map.getZoom();
      this._map.easeTo({ center: [lng, lat], zoom: z < 10 ? 13 : z, duration: 700 });
    } else if (this._follow) {
      // Keep centred (a no-op in the renderer's "free" mode).
      this._plotter.updateFollow(this._fix);
    }
    this._applyFollowZoom(); // now that we have a fix, take over the wheel if following
    this._syncChip();
  }

  _hide() {
    if (this._marker && this._added) {
      this._marker.remove();
      this._added = false;
    }
    this._fix = null;
    this._last = EMPTY;
    const src = this._map && this._map.getSource(SRC);
    if (src) src.setData(EMPTY);
    this._applyFollowZoom(); // no fix → hand the wheel back to native scroll-zoom
    this._syncChip();
  }

  destroy() {
    if (this._off) this._off();
    if (this._onStyle) this._map.off("style.load", this._onStyle);
    if (this._onDrag) this._map.off("dragstart", this._onDrag);
    if (this._canvas && this._onWheel) this._canvas.removeEventListener("wheel", this._onWheel, { passive: false });
    this._map.scrollZoom.enable();
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
