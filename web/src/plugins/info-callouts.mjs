// InfoCallouts gives the S-52 §10.6.1.1 callout markers — the box-on-a-leader
// symbols that float OFFSET from the feature — a PRECISE, layering-proof tap target.
// It covers both:
//   • SY(INFORM01)  "additional information available by cursor query"
//   • SY(CHDATD01)  "this object is a date-dependent object" (the timed-data marker)
//
// Each marker is a baked map symbol whose icon hit-quad is centred on the FEATURE, so
// MapLibre's fuzzy queryRenderedFeatures makes the whole symbol area tappable ("close
// enough") and symbol declutter/z-order makes some boxes un-pickable. This overlay
// instead drops a transparent, exactly-sized DOM pad on each visible box (a real
// clickable element, like the AIS-target Markers) — tapping it opens that feature's
// info, and tapping the feature itself is left to pick the feature.
//
// It is purely an INTERACTION layer: the baked sprite stays the visual box-on-leader;
// the pad is invisible and sits on top. Each callout follows its mariner toggle for
// free — when "Information callouts" / "Highlight date-dependent" is off the symbol
// isn't rendered, so queryRenderedFeatures returns none and no pads are placed.

// Per-callout box geometry, read off each symbol's SVG (the box rect relative to the
// sprite pivot = the feature), in mm: the box centre [+x right, -y up; SVG y is down]
// and the (square) box size. Used to place + size the pad over the rendered box.
const CALLOUTS = {
  // INFORM01.svg box rect x 9.93..14.88, y -15.05..-10.10 → up-right of the feature.
  INFORM01: { centreMM: [12.4, -12.6], sizeMM: 5.0, title: "Additional information — tap to view" },
  // CHDATD01.svg box rect x -15.16..-10.16, y 9.87..14.87 → down-left of the feature.
  CHDATD01: { centreMM: [-12.66, 12.37], sizeMM: 5.0, title: "Date-dependent feature — tap to view" },
};
const MIN_PAD_PX = 22; // touch-friendly floor, regardless of zoom-independent symbol size

export class InfoCallouts {
  constructor({ map, getSizeScale, atlasPpu = 0.08, onSelect } = {}) {
    this._map = map;
    this._getSizeScale = getSizeScale || (() => 1);
    this._atlasPpu = atlasPpu || 0.08;
    this._onSelect = onSelect; // (feature) => open its info
    this._markers = new Map(); // key -> { marker, el }
    this._timer = 0;
    this._refresh = this._refresh.bind(this);
    this._schedule = this._schedule.bind(this);
    map.on("moveend", this._schedule);
    map.on("idle", this._schedule);
    this._schedule();
  }

  _schedule() {
    clearTimeout(this._timer);
    this._timer = setTimeout(this._refresh, 120); // debounce the pan/zoom churn
  }

  // px the rendered icon occupies per sprite-mm: the atlas is 8 px/mm, scaled by the
  // feature's baked icon scale (scale/atlasPpu) and the physical-size multiplier. So
  // the pad tracks the ACTUAL rendered box even while the symbol size is calibrated.
  _pxPerMM(scale) {
    return 8 * ((scale || 0.0378) / this._atlasPpu) * this._getSizeScale();
  }

  _pointSymbolLayers() {
    try {
      return this._map.getStyle().layers
        .filter((l) => l.type === "symbol" && /point_symbols/.test(l.id))
        .map((l) => l.id);
    } catch {
      return [];
    }
  }

  _refresh() {
    const m = this._map;
    const layers = this._pointSymbolLayers();
    if (!layers.length) return; // no point-symbol layers yet → nothing to place, and
    // NEVER fall back to querying ALL layers (that walks basemap/overlay/no-data too).
    let feats = [];
    try {
      feats = m.queryRenderedFeatures({ layers })
        .filter((f) => f.properties && CALLOUTS[f.properties.symbol_name] && f.geometry && f.geometry.type === "Point");
    } catch {
      return;
    }
    const seen = new Set();
    for (const f of feats) {
      const p = f.properties;
      const spec = CALLOUTS[p.symbol_name];
      const key = p.symbol_name + "|" + (p.cell || "") + "|" + (p.class || "") + "|" + f.geometry.coordinates.join(",");
      if (seen.has(key)) continue;
      seen.add(key);
      const pxmm = this._pxPerMM(+p.scale);
      const off = [spec.centreMM[0] * pxmm, spec.centreMM[1] * pxmm];
      const size = Math.max(MIN_PAD_PX, spec.sizeMM * pxmm);
      let rec = this._markers.get(key);
      if (!rec) {
        const el = document.createElement("div");
        el.className = "info-callout-pad";
        el.title = spec.title;
        // Invisible by default (the baked S-52 box stays the visual); a faint ring on
        // hover/touch shows it's the live tap target. pointer-events auto so it owns
        // the click; box-sizing so the ring doesn't grow it.
        el.style.cssText =
          "box-sizing:border-box;border-radius:4px;cursor:pointer;pointer-events:auto;" +
          "background:transparent;border:2px solid transparent;transition:border-color .1s;";
        el.addEventListener("pointerenter", () => { el.style.borderColor = "var(--info-callout-hi,#cc3aa8)"; });
        el.addEventListener("pointerleave", () => { el.style.borderColor = "transparent"; });
        // Stop the click reaching the map so it doesn't also fire the cursor-pick.
        const open = (e) => { e.stopPropagation(); e.preventDefault(); if (this._onSelect) this._onSelect(rec ? rec.feat : f); };
        el.addEventListener("click", open);
        el.addEventListener("pointerdown", (e) => e.stopPropagation());
        const marker = new window.maplibregl.Marker({ element: el, anchor: "center", offset: off }).setLngLat(f.geometry.coordinates).addTo(m);
        rec = { marker, el, feat: f };
        this._markers.set(key, rec);
      } else {
        rec.feat = f;
        rec.marker.setOffset(off);
        rec.marker.setLngLat(f.geometry.coordinates);
      }
      rec.el.style.width = rec.el.style.height = `${Math.round(size)}px`;
    }
    for (const [k, rec] of this._markers) {
      if (!seen.has(k)) {
        rec.marker.remove();
        this._markers.delete(k);
      }
    }
  }

  destroy() {
    clearTimeout(this._timer);
    this._map.off("moveend", this._schedule);
    this._map.off("idle", this._schedule);
    for (const rec of this._markers.values()) rec.marker.remove();
    this._markers.clear();
  }
}
