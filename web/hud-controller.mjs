// hud-controller.mjs — the bottom-centre status readout (band · scale · zoom ·
// position) + its warning band, and the overscale zoom cap. Self-contained: owns
// the `move` listener that refreshes the readout; the shell drives updateZoomCap()
// from its moveend handler. Wired with accessors (no app internals leak in):
//   new HudController({ map, root, getInstalled, cellMeta, serverSetMetas,
//                       noChartsEnabled })
// root = the shadowRoot (for #cov-readout / #databox / #db-warn); cellMeta(name)
// → {s:scale, bb:[w,s,e,n]} | undefined; serverSetMetas() → [{band, bounds}].

import { bandForScale, bandForZoom, BANDS, BAND_COLOR, BAND_LABEL, BAND_MAXZOOM, OVERSCALE_MARGIN } from "./bands.mjs";
import { scaleDenom, fmtScale, fmtLatLon } from "./util.mjs";

const WARN_ICO = `<svg class="db-warn-ico" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 9v4M12 17h.01M10.3 3.9 1.8 18a2 2 0 0 0 1.7 3h17a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0Z"/></svg>`;

export class HudController {
  constructor(opts) {
    this.map = opts.map;
    this.root = opts.root;
    this.getInstalled = opts.getInstalled || (() => new Set());
    this.cellMeta = opts.cellMeta || (() => undefined);
    this.serverSetMetas = opts.serverSetMetas || (() => []);
    this.noChartsEnabled = opts.noChartsEnabled || (() => false);
    this.coverScale = 0; // finest covering chart's CSCL — the overscale ×n reference
    this._onMove = () => this.updateHud();
    this.map.on("move", this._onMove);
    this.updateHud();
    this.updateZoomCap();
  }

  destroy() { if (this.map) this.map.off("move", this._onMove); }

  // Live readout: band · scale · zoom · position, with fixed-width fields (+
  // tabular-nums in CSS) so the bar doesn't reflow as digit counts change. The
  // warning band shows "no charts enabled" (outranks) or overscale ×n.
  updateHud() {
    const el = this.root.getElementById("cov-readout");
    if (!el || !this.map) return;
    const box = this.root.getElementById("databox"); if (box) box.hidden = false;
    const z = this.map.getZoom(), c = this.map.getCenter();
    const band = bandForZoom(z);
    const dispDenom = scaleDenom(z, c.lat);
    el.innerHTML =
      `<span class="hud-main"><span class="hud-dot" style="background:${BAND_COLOR[band]}"></span>` +
      `<span class="hud-band">${BAND_LABEL[band]}</span><span class="hud-sep">·</span>` +
      `<span class="hud-scale">1:${fmtScale(dispDenom)}</span><span class="hud-sep">·</span>` +
      `<span class="hud-z">z${z.toFixed(1)}</span><span class="hud-sep">·</span>` +
      `<span class="hud-coord">${fmtLatLon(c.lat, c.lng)}</span></span>`;
    // Overscale (S-52 §10.1.10.1): display scale larger than the chart's compilation
    // scale → data magnified beyond survey scale; show the ×n factor as a full-width
    // amber band. "No charts enabled" outranks it (nothing is drawing at all).
    const warn = this.root.getElementById("db-warn");
    if (!warn) return;
    const f = this.coverScale && dispDenom < this.coverScale ? this.coverScale / dispDenom : 0;
    if (this.noChartsEnabled()) {
      warn.hidden = false;
      warn.innerHTML = WARN_ICO + `<span>No charts are enabled — turn one on in the Chart library</span>`;
    } else if (f >= 1.15) {
      warn.hidden = false;
      warn.innerHTML = WARN_ICO + `<span>Overscale ×${f < 10 ? f.toFixed(1) : Math.round(f)} — data magnified beyond survey scale</span>`;
    } else {
      warn.hidden = true;
    }
  }

  // Overscale cap: limit zoom-IN to the finest band whose chart actually covers the
  // view centre (+ OVERSCALE_MARGIN) — past that, open water just enlarges blank
  // sea. Never drops below the current zoom (no camera yank). Also records
  // coverScale (the centre chart's CSCL) for the overscale indication.
  updateZoomCap() {
    const map = this.map;
    if (!map) return;
    const c = map.getCenter();
    let finest = -1;     // index into BANDS (coarse→fine)
    let finestScale = 0; // CSCL of the largest-scale (smallest-denom) chart covering the centre
    for (const n of this.getInstalled()) {
      const cell = this.cellMeta(n);
      if (!cell || typeof cell.s !== "number" || !Array.isArray(cell.bb) || cell.bb.length !== 4) continue;
      const [w, s, e, nN] = cell.bb;
      if (c.lng < w || c.lng > e || c.lat < s || c.lat > nN) continue;
      const idx = BANDS.indexOf(bandForScale(cell.s));
      if (idx > finest) finest = idx;
      if (!finestScale || cell.s < finestScale) finestScale = cell.s;
    }
    // Server mode: imported/inland sets aren't in the NOAA catalogue (cellMeta misses
    // them) — consult each installed tile set's own band + bounds so the finest set
    // covering the centre raises the cap to its band (e.g. inland berthing).
    for (const set of this.serverSetMetas()) {
      const bb = set.bounds;
      if (!Array.isArray(bb) || bb.length !== 4) continue;
      if (c.lng < bb[0] || c.lng > bb[2] || c.lat < bb[1] || c.lat > bb[3]) continue;
      const idx = BANDS.indexOf(set.band);
      if (idx > finest) finest = idx;
    }
    this.coverScale = finestScale;
    const band = finest >= 0 ? BANDS[finest] : "general";
    const target = Math.min(18, (BAND_MAXZOOM[band] || 9) + OVERSCALE_MARGIN);
    const cap = Math.max(target, map.getZoom()); // never below current zoom → no yank
    if (Math.abs(map.getMaxZoom() - cap) > 0.01) map.setMaxZoom(cap);
    this.updateHud();
  }
}
