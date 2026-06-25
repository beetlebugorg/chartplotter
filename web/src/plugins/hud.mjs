// hud-controller.mjs — the bottom-centre status readout (band · scale · zoom ·
// position) + its warning band, and the overscale zoom cap. Self-contained: owns
// the `move` listener that refreshes the readout; the shell drives updateZoomCap()
// from its moveend handler. Wired with accessors (no app internals leak in):
//   new HudController({ map, root, getInstalled, cellMeta, serverSetMetas,
//                       noChartsEnabled })
// root = the shadowRoot (for #cov-readout / #databox / #db-warn); cellMeta(name)
// → {s:scale, bb:[w,s,e,n]} | undefined; serverSetMetas() → [{band, bounds}].

import { bandForScale, bandForZoom, BANDS, BAND_COLOR, BAND_LABEL, BAND_MAXZOOM } from "../lib/bands.mjs";
import { scaleDenomPhysical, zoomForScalePhysical, fmtScale, fmtLatLon } from "../lib/util.mjs";

// Parse a user-typed scale into a denominator. Accepts "40000", "40,000",
// "1:40000", "1:40,000", "40k", "40 000". Returns 0 if it can't.
function parseScale(s) {
  if (!s) return 0;
  let t = String(s).trim().replace(/^1\s*:/, "");
  const k = /k$/i.test(t.replace(/[\s,]/g, ""));
  t = t.replace(/[\s,]/g, "").replace(/k$/i, "");
  let n = parseFloat(t);
  if (!isFinite(n) || n <= 0) return 0;
  if (k) n *= 1000;
  return Math.round(n);
}

const WARN_ICO = `<svg class="db-warn-ico" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 9v4M12 17h.01M10.3 3.9 1.8 18a2 2 0 0 0 1.7 3h17a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0Z"/></svg>`;

export class HudController {
  constructor(opts) {
    this.map = opts.map;
    this.root = opts.root;
    this.getInstalled = opts.getInstalled || (() => new Set());
    this.cellMeta = opts.cellMeta || (() => undefined);
    this.serverSetMetas = opts.serverSetMetas || (() => []);
    this.noChartsEnabled = opts.noChartsEnabled || (() => false);
    // Monitor's physical CSS-pixel pitch (mm), for the PHYSICAL (ruler-on-glass)
    // scale shown in the readout / overscale / go-to-scale. undefined → util's
    // default (CSS reference). The user calibrates it in settings.
    this.getPxPitch = opts.getPxPitch || (() => undefined);
    this.coverScale = 0; // finest covering chart's CSCL — the overscale ×n reference
    this.detentZoom = null; // finest covering band's overscale cap — the wheel-zoom detent (not a hard maxZoom)
    this._onMove = () => this.updateHud();
    this.map.on("move", this._onMove);
    this.updateHud();
    this.updateZoomCap();
    this._wireScaleInput();
  }

  destroy() {
    if (this.map) this.map.off("move", this._onMove);
    if (this._onDocClick) document.removeEventListener("pointerdown", this._onDocClick, true);
  }

  // Click the scale readout → a small popover to type a target scale and jump to
  // it. The readout's innerHTML is rebuilt every move, so the click is delegated
  // from the stable #cov-readout parent and the popover lives OUTSIDE it.
  _wireScaleInput() {
    const root = this.root;
    const pop = root.getElementById("scale-pop");
    const input = root.getElementById("scale-input");
    const go = root.getElementById("scale-go");
    const readout = root.getElementById("cov-readout");
    if (!pop || !input || !readout) return;

    const open = () => {
      const c = this.map.getCenter();
      input.value = String(Math.round(scaleDenomPhysical(this.map.getZoom(), c.lat, this.getPxPitch())));
      pop.hidden = false;
      input.focus();
      input.select();
    };
    const close = () => { pop.hidden = true; };
    const apply = () => {
      const denom = parseScale(input.value);
      if (denom > 0) {
        const c = this.map.getCenter();
        this.map.easeTo({ zoom: zoomForScalePhysical(denom, c.lat, this.getPxPitch()), duration: 300 });
      }
      close();
    };

    readout.addEventListener("click", (e) => {
      if (e.target.closest(".hud-scale")) { e.stopPropagation(); pop.hidden ? open() : close(); }
    });
    go.addEventListener("click", (e) => { e.stopPropagation(); apply(); });
    input.addEventListener("keydown", (e) => {
      if (e.key === "Enter") { e.preventDefault(); apply(); }
      else if (e.key === "Escape") { e.preventDefault(); close(); }
    });
    // Click elsewhere closes it.
    this._onDocClick = (e) => { if (!pop.hidden && !pop.contains(e.target) && !e.target.closest?.(".hud-scale")) close(); };
    document.addEventListener("pointerdown", this._onDocClick, true);
  }

  // Live readout: band · scale · zoom · position, with fixed-width fields (+
  // tabular-nums in CSS) so the bar doesn't reflow as digit counts change. The
  // warning band shows "no charts enabled" (outranks) or overscale ×n.
  updateHud() {
    const el = this.root.getElementById("cov-readout");
    if (!el || !this.map) return;
    const box = this.root.getElementById("databox"); if (box) box.hidden = false;
    const z = this.map.getZoom(), c = this.map.getCenter();
    const band = bandForZoom(z);
    // The READOUT shows the PHYSICAL scale (matches a ruler / other ENCs).
    const dispDenom = scaleDenomPhysical(z, c.lat, this.getPxPitch());
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
    // Overscale compares the TRUE on-screen scale against the cell's compilation
    // scale (CSCL) — both physical, real 1:N scales — so it reads the same physical
    // denominator as the readout (dispDenom). The engine is now on one physical
    // scale (no separate nominal coordinate), so ×n is literally how magnified the
    // survey data is on glass: a 1:45k cell viewed at 1:26k shows ×1.7, in-band or
    // not, which is the S-52 §10.1.10.1 intent.
    const f = this.coverScale && dispDenom < this.coverScale ? this.coverScale / dispDenom : 0;
    if (this.noChartsEnabled()) {
      warn.hidden = false;
      warn.innerHTML = WARN_ICO + `<span>No charts are enabled — turn one on in the Chart library</span>`;
    } else if (f >= 1.05) {
      // Show from ~×1.1 so the factor RAMPS (1.1 → 1.2 → …) as you zoom rather than
      // popping straight to ×1.2: the old 1.15 gate skipped the ×1.1 step entirely
      // (1.15 already rounds to "1.2", so the ×1.1 window had ~zero width).
      warn.hidden = false;
      warn.innerHTML = WARN_ICO + `<span>Overscale ×${f < 10 ? f.toFixed(1) : Math.round(f)} — data magnified beyond survey scale</span>`;
    } else {
      warn.hidden = true;
    }
  }

  // Overscale detent: the native-max zoom of the finest band whose chart actually
  // covers the view centre — the click where overscale begins. Exposed via
  // getDetentZoom() so WheelZoom stops zoom-in there briefly (then a sustained
  // scroll releases past it, on into overscale); it is NOT a hard maxZoom, so other
  // inputs can still reach the scale floor. Also records coverScale (the centre
  // chart's CSCL) for the overscale indication.
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
    // Detent right where overscale BEGINS for the covering chart: the zoom whose
    // displayed (physical) scale equals the chart's compilation scale, coverScale.
    // Zoom past it and dispDenom < coverScale → "Overscale ×N" (same test as the
    // warning above). Falls back to the band's native-max zoom when no per-chart
    // scale is known (e.g. server sets without the NOAA catalogue).
    this.detentZoom = finestScale
      ? zoomForScalePhysical(finestScale, c.lat, this.getPxPitch())
      : Math.min(18, BAND_MAXZOOM[band] || 9);
    this.updateHud();
  }

  // The current overscale detent zoom (finest covering band's cap), for WheelZoom.
  getDetentZoom() { return this.detentZoom; }
}
