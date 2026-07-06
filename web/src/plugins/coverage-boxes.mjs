// coverage-boxes.mjs — the installed-chart coverage overlay: a box/outline per
// installed pack (or cell) so you can see WHERE your charts are when zoomed out
// past their detail, with a per-zoom MINIMUM on-screen size so a tiny mid-ocean
// cell never shrinks to an invisible speck. Tap a box to fly to that chart at the
// zoom where its detail renders.
//
// Self-contained: owns the `inst-bounds` source + its coverage layers (per-band
// fill/line + the always-on outline) and a throttled `zoom` listener that re-grows
// the boxes. The shell owns the DATA — it computes the footprint features (from
// installed packs/cells) and pushes them in via setFeatures().
//
//   const cov = new CoverageBoxes({ map, visible });
//   cov.addLayers();              // (re)create source+layers — call after each setStyle
//   cov.setFeatures(rawFeats);    // [{properties:{name,band}, geometry:Polygon}]
//   if (cov.tapFlyTo(point)) …    // in the map click handler, before the pick report

import { BAND_COLOR } from "../lib/bands.mjs";

// Band → outline/label colour (a feature's `band` property; "" / unknown → blue).
const BAND_TINT = ["match", ["get", "band"],
  "overview", BAND_COLOR.overview, "general", BAND_COLOR.general, "coastal", BAND_COLOR.coastal,
  "approach", BAND_COLOR.approach, "harbor", BAND_COLOR.harbor, "berthing", BAND_COLOR.berthing,
  "#3a9bdc"];

const MIN_BOX_PX = 26; // a box never renders smaller than this on screen
const REGROW_MS = 200; // during a continuous zoom, re-grow the boxes at most 5×/s

export class CoverageBoxes {
  constructor({ map, visible }) {
    this.map = map;
    this._visible = visible !== false;
    this._raw = [];
    this._raf = 0;
    this._regrowT = 0;   // trailing re-grow timer (covers the end of a gesture)
    this._lastApply = 0; // performance.now() of the last actual setData
    this._lastSig = null; // signature of the last-pushed features (skip no-op setData)
    // The boxes have a per-zoom minimum on-screen size, so re-grow them as the zoom
    // changes. Hooked once (the map persists across style rebuilds). Throttled to
    // REGROW_MS with a trailing apply: the old per-frame (rAF) cadence made every
    // animation frame of a zoom gesture setData() this source, and each setData
    // re-tiles the geojson in a worker AND reloads every inst-bounds tile across
    // its 11 layers (5 of them symbol layers → re-placement). Measured on a
    // continuous z8→16 easeTo, that was ~70-85% of ALL tile churn in the gesture
    // (~230-410 tile reloads vs ~120 real chart loads) — the map got choppier the
    // faster it rendered. _apply() also skips identical output, so the common
    // zoomed-in case (every footprint ≥ MIN_BOX_PX) does no work at all.
    this._onZoom = () => {
      if (!this._visible || this._raf || this._regrowT) return;
      const wait = this._lastApply + REGROW_MS - performance.now();
      if (wait > 0) { this._regrowT = setTimeout(() => { this._regrowT = 0; this._apply(); }, wait); return; }
      this._raf = requestAnimationFrame(() => { this._raf = 0; this._apply(); });
    };
    map.on("zoom", this._onZoom);
  }

  // (Re)create the source + coverage layers. Idempotent — safe to call after every
  // setStyle (which wipes them).
  addLayers() {
    const map = this.map;
    if (!map.getSource("inst-bounds")) map.addSource("inst-bounds", { type: "geojson", data: { type: "FeatureCollection", features: [] } });
    this._lastSig = null; // a rebuilt style starts from an empty source — must re-push
    const vis = this._visible ? "visible" : "none";
    // A band-coloured dashed outline per cell footprint, at ALL zooms (no fill/tint), so
    // you can see which chart owns where even while zoomed into the detail. Subtle when
    // zoomed in so it doesn't fight the chart symbology; bolder scaled far out.
    if (!map.getLayer("inst-outline")) map.addLayer({ id: "inst-outline", type: "line", source: "inst-bounds", layout: { visibility: vis }, paint: {
      "line-color": BAND_TINT,
      "line-dasharray": [4, 3],
      "line-width": ["interpolate", ["linear"], ["zoom"], 3, 2.2, 8, 1.4, 13, 1, 16, 0.8],
      "line-opacity": ["interpolate", ["linear"], ["zoom"], 3, 0.95, 8, 0.85, 13, 0.7, 16, 0.55],
    } });
    // Cell-name label at each footprint centroid, band-coloured, kept at ALL zooms so you
    // can read WHICH chart covers a point even at the detail zoom — the "which cells are
    // which" debug view. Decluttered (drops on overlap) so a dense view stays legible.
    if (!map.getLayer("inst-name")) map.addLayer({ id: "inst-name", type: "symbol", source: "inst-bounds", layout: {
      visibility: vis,
      "symbol-placement": "point",
      "text-field": ["coalesce", ["get", "name"], ""],
      "text-font": ["Noto Sans Regular"],
      "text-size": 11,
      "text-allow-overlap": false,
      "text-optional": true,
    }, paint: {
      "text-color": BAND_TINT,
      "text-halo-color": "#ffffff",
      "text-halo-width": 1.8,
    } });
    this._apply(); // restore data after a style rebuild
  }

  // Replace the coverage features. De-dups on the cell name/band signature (moveend +
  // idle both refresh, and idle can fire repeatedly with the same winning cells); the
  // per-zoom min-box growth is handled by the zoom hook + _apply, so a name/band
  // signature is enough to skip a redundant re-tile.
  setFeatures(raw) {
    raw = raw || [];
    const sig = raw.length + "#" + raw.map((f) => `${(f.properties && f.properties.name) || ""}:${(f.properties && f.properties.band) || ""}`).join(",");
    if (sig === this._featSig) return;
    this._featSig = sig;
    this._raw = raw;
    this._lastSig = null;
    this._apply();
  }

  _apply() {
    const src = this.map.getSource("inst-bounds");
    if (!src) return;
    const features = this._raw.map((f) => minSizeBox(this.map, f, MIN_BOX_PX));
    // Skip the setData when the grown output is unchanged. minSizeBox returns the
    // RAW feature object itself whenever the footprint is already big enough on
    // screen, so the signature only stringifies the (few, usually zero) boxed
    // geometries — zoomed in past the point where every footprint ≥ MIN_BOX_PX,
    // a whole zoom gesture is a no-op here.
    let sig = "";
    for (let i = 0; i < features.length; i++) if (features[i] !== this._raw[i]) sig += i + ":" + JSON.stringify(features[i].geometry.coordinates) + ";";
    if (this._lastSig !== null && sig === this._lastSig) return;
    this._lastSig = sig;
    this._lastApply = performance.now();
    src.setData({ type: "FeatureCollection", features });
  }

  setVisible(on) {
    this._visible = !!on;
    const map = this.map, vis = on ? "visible" : "none";
    for (const id of ["inst-outline", "inst-name"]) if (map.getLayer(id)) map.setLayoutProperty(id, "visibility", vis);
    if (on) this._apply(); // re-grow: the zoom hook is idle while hidden
  }

  destroy() {
    this.map.off("zoom", this._onZoom);
    if (this._raf) cancelAnimationFrame(this._raf);
    if (this._regrowT) clearTimeout(this._regrowT);
  }
}

// Return f unchanged if its footprint is already ≥ minPx on screen; else a copy
// whose polygon is a minPx box centred on the same point (built in screen space via
// project/unproject, so it's exactly minPx regardless of latitude/zoom).
function minSizeBox(map, f, minPx) {
  const ring = f.geometry && f.geometry.coordinates && f.geometry.coordinates[0];
  if (!ring) return f;
  let w = Infinity, s = Infinity, e = -Infinity, n = -Infinity;
  for (const [x, y] of ring) { w = Math.min(w, x); e = Math.max(e, x); s = Math.min(s, y); n = Math.max(n, y); }
  const tl = map.project([w, n]), br = map.project([e, s]);
  if (Math.abs(br.x - tl.x) >= minPx && Math.abs(br.y - tl.y) >= minPx) return f;
  const c = map.project([(w + e) / 2, (s + n) / 2]);
  const hw = Math.max(minPx, Math.abs(br.x - tl.x)) / 2, hh = Math.max(minPx, Math.abs(br.y - tl.y)) / 2;
  const a = map.unproject([c.x - hw, c.y - hh]); // W / N
  const b = map.unproject([c.x + hw, c.y + hh]); // E / S
  return { type: "Feature", properties: f.properties, geometry: { type: "Polygon", coordinates: [[[a.lng, a.lat], [b.lng, a.lat], [b.lng, b.lat], [a.lng, b.lat], [a.lng, a.lat]]] } };
}
