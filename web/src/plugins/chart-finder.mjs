// chart-finder.mjs — off-screen chart pointers: edge chips pointing toward
// installed chart packs that aren't currently in view, so off-screen charts are
// always findable (and one tap flies you there at the zoom where they render).
//
// Self-contained: owns its overlay DOM + a single map "move" listener, throttled
// to one update per frame. Operates on PACKS (a few dozen at most), never cells,
// and only on the ones that are off-screen — then clusters by screen position and
// caps the count, so the number of chips stays small whatever the install size.
//
// The shell wires it with accessors (no app internals leak in here):
//   new ChartFinder({ host, map, getPacks, getUnits, labelFor, onPick, visible })
// where getPacks() → [{name, enabled, bands:[coarse→fine], bounds:[w,s,e,n]}].

import { format as fmtUnit } from "../lib/units.mjs";
import { BAND_COLOR } from "../lib/bands.mjs";

const MERGE_PX = 46; // chips closer than this on screen merge into one cluster
const CAP = 8;       // max chips shown (nearest clusters win)
const MARGIN = 30;   // keep chips this far inside the map edge

const esc = (s) => String(s).replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]));

export class ChartFinder {
  constructor(opts) {
    this.host = opts.host;
    this.map = opts.map;
    this.getPacks = opts.getPacks;
    this.getUnits = opts.getUnits || (() => ({}));
    this.labelFor = opts.labelFor || ((n) => n);
    this.onPick = opts.onPick || (() => {});
    this._visible = opts.visible !== false;
    this._raf = 0;
    this._onMove = () => this.schedule();
    this.map.on("move", this._onMove);
    this.update();
  }

  setVisible(on) { this._visible = !!on; this.update(); }

  // Cascading safe-area + bottom-bar tokens (px), read off the chip host (they
  // pierce shadow boundaries from the shell). --botbar-h already includes
  // env(safe-area-inset-bottom). Falls back to 0 on engines without the tokens.
  _insets() {
    const el = this.host;
    if (!el || !el.isConnected) return { top: 0, right: 0, bottom: 0, left: 0 };
    const px = (v) => { const n = parseFloat(v); return isFinite(n) ? n : 0; };
    const cs = getComputedStyle(el);
    return {
      top: px(cs.getPropertyValue("--sa-top")),
      right: px(cs.getPropertyValue("--sa-right")),
      bottom: px(cs.getPropertyValue("--botbar-h")),
      left: px(cs.getPropertyValue("--sa-left")),
    };
  }

  // Coalesce a flurry of move events into one update per animation frame.
  schedule() {
    if (this._raf) return;
    this._raf = requestAnimationFrame(() => { this._raf = 0; this.update(); });
  }

  destroy() {
    this.map.off("move", this._onMove);
    if (this._raf) cancelAnimationFrame(this._raf);
    this.host.replaceChildren();
  }

  update() {
    const host = this.host, map = this.map;
    if (!host || !map) return;
    if (!this._visible) { host.replaceChildren(); return; }
    const packs = (this.getPacks() || []).filter((p) => p.enabled && Array.isArray(p.bounds) && p.bounds.length === 4);
    if (!packs.length) { host.replaceChildren(); return; }

    const c = map.getContainer();
    const W = c.clientWidth, H = c.clientHeight;
    if (!W || !H) return;
    const cx = W / 2, cy = H / 2;
    // Per-side inset so chips never land under the notch / rounded corners / home
    // indicator / bottom tab bar. MARGIN is the base edge gap; the safe-area +
    // bottom-bar tokens (which cascade into the shell) add to it per side.
    const sa = this._insets();
    // Extra bottom reserve so edge chips clear the floating bottom chrome (the
    // centred data card + the corner button row), not just the safe-area inset.
    const mL = MARGIN + sa.left, mR = MARGIN + sa.right, mT = MARGIN + sa.top, mB = MARGIN + sa.bottom + 80;
    const v = map.getBounds();
    const vw = v.getWest(), vs = v.getSouth(), ve = v.getEast(), vn = v.getNorth();
    const ctr = map.getCenter();

    // One off-screen target per pack (its bbox doesn't intersect the viewport).
    const targets = [];
    for (const p of packs) {
      const [w, s, e, n] = p.bounds;
      if (!(w > ve || e < vw || s > vn || n < vs)) continue; // intersects view → visible, skip
      const pcy = (s + n) / 2, pcx = (w + e) / 2;
      targets.push({
        pack: p,
        bearing: bearing(ctr.lat, ctr.lng, pcy, pcx),
        distNm: haversineNm(ctr.lat, ctr.lng, pcy, pcx),
      });
    }
    if (!targets.length) { host.replaceChildren(); return; }

    // Place each on the screen edge, then merge nearby placements into clusters.
    targets.sort((a, b) => a.distNm - b.distNm); // nearest first → labels the cluster
    const clusters = [];
    for (const t of targets) {
      t.pos = edgePoint(cx, cy, t.bearing, W, H, mL, mR, mT, mB);
      const hit = clusters.find((cl) => Math.hypot(cl.pos.x - t.pos.x, cl.pos.y - t.pos.y) < MERGE_PX);
      if (hit) hit.items.push(t);
      else clusters.push({ pos: t.pos, bearing: t.bearing, items: [t] });
    }
    clusters.sort((a, b) => a.items[0].distNm - b.items[0].distNm);

    const units = this.getUnits();
    const frag = document.createDocumentFragment();
    for (const cl of clusters.slice(0, CAP)) {
      const near = cl.items[0], multi = cl.items.length > 1;
      const finest = near.pack.bands && near.pack.bands[near.pack.bands.length - 1];
      const chip = document.createElement("div");
      chip.className = "finder-chip";
      chip.style.left = cl.pos.x + "px";
      chip.style.top = cl.pos.y + "px";
      const dot = multi ? "" : `<span class="fc-band" style="background:${BAND_COLOR[finest] || "#888"}"></span>`;
      const name = multi ? `${cl.items.length} charts` : this.labelFor(near.pack.name);
      chip.innerHTML = `${arrowSvg(cl.bearing)}${dot}<span class="fc-name">${esc(name)}</span>` +
        `<span class="fc-dist">${esc(fmtUnit("distance", near.distNm, units))}</span>`;
      chip.title = cl.items.map((i) => this.labelFor(i.pack.name)).join(", ");
      chip.onclick = () => this.onPick(cl.items.map((i) => i.pack));
      frag.appendChild(chip);
    }
    host.replaceChildren(frag);
  }
}

// Initial compass bearing (deg, 0=N, 90=E) from point 1 to point 2.
function bearing(lat1, lon1, lat2, lon2) {
  const R = Math.PI / 180;
  const φ1 = lat1 * R, φ2 = lat2 * R, Δλ = (lon2 - lon1) * R;
  const y = Math.sin(Δλ) * Math.cos(φ2);
  const x = Math.cos(φ1) * Math.sin(φ2) - Math.sin(φ1) * Math.cos(φ2) * Math.cos(Δλ);
  return (Math.atan2(y, x) * 180 / Math.PI + 360) % 360;
}

// Great-circle distance in nautical miles.
function haversineNm(lat1, lon1, lat2, lon2) {
  const R = Math.PI / 180, NM = 3440.065;
  const dφ = (lat2 - lat1) * R, dλ = (lon2 - lon1) * R;
  const a = Math.sin(dφ / 2) ** 2 + Math.cos(lat1 * R) * Math.cos(lat2 * R) * Math.sin(dλ / 2) ** 2;
  return 2 * NM * Math.asin(Math.min(1, Math.sqrt(a)));
}

// Intersection of a ray from (cx,cy) at compass `bearing` with the inset screen
// rectangle — where the edge chip sits. Screen y is down; bearing 0 = up.
// Per-side margins (left/right/top/bottom) keep chips out of the safe-area
// insets (notch / rounded corners / home indicator / bottom bar).
function edgePoint(cx, cy, brg, W, H, mL, mR, mT, mB) {
  const r = brg * Math.PI / 180;
  const dx = Math.sin(r), dy = -Math.cos(r);
  let t = Infinity;
  if (dx > 1e-9) t = Math.min(t, (W - mR - cx) / dx);
  else if (dx < -1e-9) t = Math.min(t, (mL - cx) / dx);
  if (dy > 1e-9) t = Math.min(t, (H - mB - cy) / dy);
  else if (dy < -1e-9) t = Math.min(t, (mT - cy) / dy);
  if (!isFinite(t) || t < 0) t = 0;
  return { x: cx + dx * t, y: cy + dy * t };
}

// An up-pointing arrow rotated to the compass bearing (so it points at the chart).
function arrowSvg(brg) {
  return `<svg class="fc-arrow" viewBox="0 0 24 24" style="transform:rotate(${brg.toFixed(1)}deg)" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"><path d="M12 4v15M12 4l-5 6M12 4l5 6"/></svg>`;
}
