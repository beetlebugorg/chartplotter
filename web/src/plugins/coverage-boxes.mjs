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

import { BAND_COLOR, BAND_MINZOOM } from "../lib/bands.mjs";

const BANDS = ["general", "coastal", "approach", "harbor", "berthing"];
const MIN_BOX_PX = 26; // a box never renders smaller than this on screen

export class CoverageBoxes {
  constructor({ map, visible }) {
    this.map = map;
    this._visible = visible !== false;
    this._raw = [];
    this._raf = 0;
    // The boxes have a per-zoom minimum on-screen size, so re-grow them as the zoom
    // changes. Hooked once (the map persists across style rebuilds); rAF-throttled.
    this._onZoom = () => { if (this._raf) return; this._raf = requestAnimationFrame(() => { this._raf = 0; this._apply(); }); };
    map.on("zoom", this._onZoom);
  }

  // (Re)create the source + coverage layers. Idempotent — safe to call after every
  // setStyle (which wipes them).
  addLayers() {
    const map = this.map;
    if (!map.getSource("inst-bounds")) map.addSource("inst-bounds", { type: "geojson", data: { type: "FeatureCollection", features: [] } });
    const vis = this._visible ? "visible" : "none";
    // Per-band fill/line, auto-hidden at the band's native min zoom (maxzoom) where
    // the real chart takes over.
    for (const band of BANDS) {
      const mz = BAND_MINZOOM[band];
      const f = ["==", ["get", "band"], band];
      if (!map.getLayer(`inst-fill-${band}`)) map.addLayer({ id: `inst-fill-${band}`, type: "fill", source: "inst-bounds", maxzoom: mz, filter: f, layout: { visibility: vis }, paint: { "fill-color": BAND_COLOR[band], "fill-opacity": 0.06 } });
      if (!map.getLayer(`inst-line-${band}`)) map.addLayer({ id: `inst-line-${band}`, type: "line", source: "inst-bounds", maxzoom: mz, filter: f, layout: { visibility: vis }, paint: { "line-color": BAND_COLOR[band], "line-width": 1.1, "line-opacity": 0.85 } });
      // Cell-name label at the footprint centroid, so on a blank (no-basemap) map you
      // can SEE which chart is where (and tap it). Capped at the band's render zoom
      // (same as the fill/line) so it vanishes once the chart itself draws — not
      // obtrusive when you're already there. Decluttered (drops on overlap).
      if (!map.getLayer(`inst-label-${band}`)) map.addLayer({ id: `inst-label-${band}`, type: "symbol", source: "inst-bounds", maxzoom: mz, filter: f, layout: {
        visibility: vis,
        "symbol-placement": "point",
        "text-field": ["coalesce", ["get", "name"], ""],
        "text-font": ["Noto Sans Regular"],
        "text-size": 12,
        "text-allow-overlap": false,
        "text-optional": true,
      }, paint: {
        "text-color": BAND_COLOR[band],
        "text-halo-color": "#ffffff",
        "text-halo-width": 1.8,
      } });
    }
    // Always-on footprint outline (NOT maxzoom-capped): when SCAMIN suppresses every
    // feature in a cell the tiles render blank, so keep a thin dashed outline at ALL
    // zooms — BOLD when scaled far out (the box is a tiny shape then), subtle once
    // zoomed in so it doesn't fight the chart symbology.
    if (!map.getLayer("inst-outline")) map.addLayer({ id: "inst-outline", type: "line", source: "inst-bounds", layout: { visibility: vis }, paint: {
      "line-color": "#3a9bdc",
      "line-dasharray": [4, 3],
      "line-width": ["interpolate", ["linear"], ["zoom"], 2, 2.2, 8, 1.6, 13, 1, 16, 0.8],
      "line-opacity": ["interpolate", ["linear"], ["zoom"], 2, 0.95, 8, 0.8, 13, 0.55, 16, 0.4],
    } });
    this._apply(); // restore data after a style rebuild
  }

  // Replace the coverage features. Raw (true) footprints are kept; _apply() pushes
  // them with the per-zoom minimum size.
  setFeatures(raw) { this._raw = raw || []; this._apply(); }

  _apply() {
    const src = this.map.getSource("inst-bounds");
    if (!src) return;
    src.setData({ type: "FeatureCollection", features: this._raw.map((f) => minSizeBox(this.map, f, MIN_BOX_PX)) });
  }

  setVisible(on) {
    this._visible = !!on;
    const map = this.map, vis = on ? "visible" : "none";
    for (const band of BANDS) for (const pre of ["inst-fill-", "inst-line-", "inst-label-"]) if (map.getLayer(pre + band)) map.setLayoutProperty(pre + band, "visibility", vis);
    if (map.getLayer("inst-outline")) map.setLayoutProperty("inst-outline", "visibility", vis);
  }

  // A coverage box under `point` that we're zoomed OUT of (its chart detail hasn't
  // kicked in) — so a tap should fly there rather than open a pick report. null when
  // hidden, nothing under the point, or already at the chart's detail zoom.
  boxAt(point) {
    const map = this.map;
    if (!this._visible) return null;
    const ids = ["inst-outline", ...BANDS.map((b) => `inst-fill-${b}`)].filter((id) => map.getLayer(id));
    const hit = map.queryRenderedFeatures(point, { layers: ids })[0];
    if (!hit) return null;
    const band = hit.properties && hit.properties.band;
    if (map.getZoom() >= (BAND_MINZOOM[band] || 12)) return null; // already at detail → let the pick run
    return hit;
  }

  // Fly to a box's chart at the zoom where its detail renders. The box may be the
  // min-size marker, so frame the TRUE footprint (from _raw) and ensure we cross the
  // band's render zoom (a tiny cell fitted to the viewport would stop short).
  flyTo(f) {
    const map = this.map;
    const name = f.properties && f.properties.name;
    const real = this._raw.find((r) => r.properties && r.properties.name === name) || f;
    const ring = real.geometry.coordinates[0];
    let w = Infinity, s = Infinity, e = -Infinity, n = -Infinity;
    for (const [x, y] of ring) { w = Math.min(w, x); e = Math.max(e, x); s = Math.min(s, y); n = Math.max(n, y); }
    const need = (BAND_MINZOOM[(f.properties && f.properties.band)] || 12) + 1;
    const cam = map.cameraForBounds([[w, s], [e, n]], { padding: 80 });
    const zoom = Math.min(18, Math.max(cam ? cam.zoom : need, need));
    if (map.getMaxZoom() < zoom) map.setMaxZoom(zoom); // don't let the departure cap clamp the fly short
    map.flyTo({ center: cam ? cam.center : [(w + e) / 2, (s + n) / 2], zoom, duration: 1200 });
  }

  // Hit-test + fly in one call; returns true if it handled the tap.
  tapFlyTo(point) {
    const f = this.boxAt(point);
    if (!f) return false;
    this.flyTo(f);
    return true;
  }

  destroy() {
    this.map.off("zoom", this._onZoom);
    if (this._raf) cancelAnimationFrame(this._raf);
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
