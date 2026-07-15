// Wind (GRIB) — UI half. Self-contained: it loads dynamically from the plugin
// archive (/plugins/core.weather/ui/plugin.mjs) and uses only the ctx handles, no app
// imports. It fetches the wind grid the host-side plugin published
// (/plugins/<id>/serve/wind.json) and animates it as moving streamlines on a canvas
// over the chart — the "grid, not tiles" model: the render is entirely client-side.
//
// This is the use-at-your-own-risk tier: a particle field needs raw projection, so it
// uses ctx.map (accepting the same contract the built-ins live with) rather than the
// declarative layers API.

// Density is scaled to the viewport in _seed (not a fixed count) so a zoomed-in view
// isn't a solid mat of lines; this is the target per ~1M screen px.
const DENSITY = 900; // particles per megapixel of map
const MAX_PARTICLES = 1600; // hard cap on big screens
const MAX_AGE = 100; // frames before a particle respawns
const STEP = 0.16; // screen px per (m/s) per frame (lower → slower, calmer motion)
const FADE = 0.92; // trail persistence (lower → shorter, lighter tails)
const LINE_WIDTH = 1.3;

// Wind-speed colour ramp (m/s → colour), interpolated. A cool→warm scale that reads
// on day/dusk/night charts: calm blues, moderate greens/gold, strong oranges/reds,
// gale magenta.
const RAMP = [
  [0, "#5b9bd5"], [4, "#4fb0c6"], [8, "#5ec4a0"],
  [12, "#8ecf6a"], [16, "#e6c84b"], [21, "#ef9a3d"],
  [27, "#e5623c"], [34, "#c23b6b"], [45, "#8e3ca0"],
];

export default class WindOverlay {
  constructor(ctx) {
    this.ctx = ctx;
    this._grid = null;
    this._doc = null;
    this._particles = [];
    this._raf = 0;
    this._on = true;
  }

  async start() {
    const ctx = this.ctx;
    // The canvas overlay, above the map GL canvas but click-through.
    const map = ctx.map;
    const container = map.getContainer();
    const canvas = document.createElement("canvas");
    canvas.style.cssText = "position:absolute;inset:0;pointer-events:none;z-index:3;";
    container.appendChild(canvas);
    this._canvas = canvas;
    this._c2d = canvas.getContext("2d");
    this._resize();
    this._onResize = () => this._resize();
    map.on("resize", this._onResize);
    // Only clear on ZOOM (the projection scale changes, so trails would stretch).
    // Do NOT clear on pan: the follow camera eases the map on every fix, and clearing
    // there made the whole field flicker each time — re-projecting per frame already
    // tracks a pan, and the trail fade cleans up any brief smear.
    this._onZoom = () => this._c2d && this._c2d.clearRect(0, 0, this._cw, this._ch);
    map.on("zoomstart", this._onZoom);

    // Show/hide from the Layers control AND the on-map control below — both drive the
    // same registry entry, so they stay in sync. Hiding stops the animation but keeps
    // the wind data loaded (visual-only, distinct from disabling the plugin).
    this._layer = ctx.overlays.register({
      id: "wind",
      title: "Wind streamlines",
      group: "Wind",
      onVisible: (v) => this._setOn(v),
    });

    this._mountSlider();
    this._mountControl();
    await this._loadDoc();
    this._seed();
    // The animation is driven by _setOn (from the overlay's persisted state,
    // registered above). If it started before the grid loaded, it now has data.
  }

  async _loadDoc() {
    try {
      const url = `${this.ctx.assets}plugins/${this.ctx.plugin.id}/serve/wind.bin`;
      const buf = await (await fetch(url, { cache: "no-store" })).arrayBuffer();
      const doc = parseWindBin(buf);
      if (!doc) return;
      this._doc = doc;
      this._setStep(0); // build the initial grid from the first forecast step
      this._buildSlider();
    } catch (e) {
      this.ctx.plugin.log("warn", "wind grid load failed", e);
    }
  }

  // _setStep sets the active wind grid from a fractional step position, linearly
  // interpolating u/v between the two bracketing forecast steps.
  _setStep(frac) {
    const d = this._doc;
    if (!d) return;
    const n = d.steps.length;
    frac = Math.max(0, Math.min(n - 1, frac));
    const i0 = Math.floor(frac), i1 = Math.min(i0 + 1, n - 1), t = frac - i0;
    const s0 = d.steps[i0], s1 = d.steps[i1];
    const len = s0.u.length;
    const u = new Array(len), v = new Array(len);
    for (let k = 0; k < len; k++) {
      u[k] = s0.u[k] * (1 - t) + s1.u[k] * t;
      v[k] = s0.v[k] * (1 - t) + s1.v[k] * t;
    }
    const h = d.header;
    this._grid = { nx: h.nx, ny: h.ny, lo1: h.lo1, la1: h.la1, lo2: h.lo2, la2: h.la2, dx: h.dx, dy: h.dy, u, v };
    this._hour = Math.round(s0.hour * (1 - t) + s1.hour * t);
    if (this._label) this._label.textContent = "+" + this._hour + "h";
    this._updateReadout(); // wind at the vessel changes with the forecast step
  }

  // Bilinear-sample the wind at (lng,lat); returns [u,v] or null if off-grid. Handles
  // global grids expressed in 0–360° longitude (GFS) as well as −180..180.
  _sample(lng, lat) {
    const g = this._grid;
    if (!g) return null;
    const global = g.nx * g.dx >= 359; // spans the whole planet → longitude wraps
    let fx = (lng - g.lo1) / g.dx;
    if (global) fx = ((fx % g.nx) + g.nx) % g.nx; // wrap (e.g. lon -76 → 284 on a 0–360 grid)
    const fy = (g.la1 - lat) / g.dy; // la1 is the north edge; rows go south
    if (fy < 0 || fy > g.ny - 1) return null;
    if (!global && (fx < 0 || fx > g.nx - 1)) return null;
    const x0 = Math.floor(fx), y0 = Math.floor(fy);
    const x1 = global ? (x0 + 1) % g.nx : Math.min(x0 + 1, g.nx - 1);
    const y1 = Math.min(y0 + 1, g.ny - 1);
    const tx = fx - x0, ty = fy - y0;
    const at = (arr, x, y) => arr[y * g.nx + x];
    const bil = (arr) =>
      at(arr, x0, y0) * (1 - tx) * (1 - ty) + at(arr, x1, y0) * tx * (1 - ty) +
      at(arr, x0, y1) * (1 - tx) * ty + at(arr, x1, y1) * tx * ty;
    return [bil(g.u), bil(g.v)];
  }

  _seed() {
    const megapixels = (this._cw * this._ch) / 1e6 || 1;
    const n = Math.min(MAX_PARTICLES, Math.max(250, Math.round(DENSITY * megapixels)));
    this._particles = [];
    for (let i = 0; i < n; i++) this._particles.push(this._spawn());
  }

  _spawn() {
    if (!this._grid) return { lng: 0, lat: 0, age: 9999 };
    // Spawn within the current viewport so particles are visible whatever the grid's
    // extent (a global GFS field would otherwise scatter them across the planet).
    const b = this.ctx.map.getBounds();
    return {
      lng: b.getWest() + Math.random() * (b.getEast() - b.getWest()),
      lat: b.getSouth() + Math.random() * (b.getNorth() - b.getSouth()),
      age: Math.floor(Math.random() * MAX_AGE),
    };
  }

  // _mountSlider builds the forecast time scrubber (hidden until data loads / the
  // overlay is shown). Mounted in the shell chrome; theme vars inherit.
  _mountSlider() {
    const hud = this.ctx.hud.mount("wind-time");
    hud.innerHTML = `<style>
      .wt{position:absolute;left:50%;transform:translateX(-50%);bottom:calc(var(--botbar-h,0px) + 92px);
        z-index:6;display:none;align-items:center;gap:10px;padding:7px 14px;border-radius:20px;
        background:var(--ui-surface,#161b22);border:1px solid var(--ui-border,#30363d);
        box-shadow:0 3px 14px rgba(0,0,0,.28);color:var(--ui-text,#e6edf3);font:600 12px/1 system-ui,sans-serif;}
      .wt input{width:180px;accent-color:var(--ui-accent,#2f81f7);}
      .wt .lbl{min-width:46px;text-align:right;}
    </style><div class="wt"><span>🌬</span><input type="range" min="0" max="0" value="0"><span class="lbl">+0h</span></div>`;
    this._sliderWrap = hud.querySelector(".wt");
    this._slider = hud.querySelector("input");
    this._label = hud.querySelector(".lbl");
    this._slider.addEventListener("input", () => this._setStep(Number(this._slider.value) / 10));
  }

  _buildSlider() {
    if (!this._slider || !this._doc) return;
    this._slider.max = String((this._doc.steps.length - 1) * 10); // ×10 for smooth interpolation
    this._slider.value = "0";
    this._syncSlider();
  }

  _syncSlider() {
    if (this._sliderWrap) {
      this._sliderWrap.style.display = this._on && this._doc && this._doc.steps.length > 1 ? "flex" : "none";
    }
  }

  // One on-map control that is both the enable/disable toggle AND the live wind
  // readout: click to turn the overlay on/off; while on it shows the actual wind at
  // the vessel (speed in the mariner's units + compass direction it blows FROM, with
  // an arrow pointing where it blows). Kept in sync with the Layers control via the
  // shared registry.
  _mountControl() {
    const hud = this.ctx.hud.mount("wind-control");
    hud.innerHTML = `<style>
      .wc{position:absolute;right:calc(12px + env(safe-area-inset-right,0px));top:calc(var(--topbar-h,0px) + 60px);
        z-index:6;display:flex;align-items:center;gap:7px;padding:6px 12px;border-radius:16px;cursor:pointer;
        background:var(--ui-surface,#161b22);border:1px solid var(--ui-border,#30363d);
        color:var(--ui-text,#e6edf3);font:600 12px/1 system-ui,sans-serif;box-shadow:0 3px 14px rgba(0,0,0,.28);
        -webkit-user-select:none;user-select:none;}
      .wc.off{opacity:.65;}
      .wc .arrow{display:inline-block;font-size:13px;line-height:1;}
    </style><div class="wc off" id="c"><span>🌬</span><span class="arrow" id="ar" hidden>↓</span><span id="v">Wind</span></div>`;
    this._ctl = hud.querySelector("#c");
    this._ctlVal = hud.querySelector("#v");
    this._ctlArrow = hud.querySelector("#ar");
    this._ctl.addEventListener("click", () => this._layer.toggle());
    this.ctx.vessel.subscribe(() => this._updateReadout());
  }

  _updateReadout() {
    if (!this._ctl) return;
    this._ctl.classList.toggle("off", !this._on);
    const w = this._on ? this._sample(this._readoutPos().lng, this._readoutPos().lat) : null;
    if (!w) {
      this._ctlVal.textContent = "Wind";
      this._ctlArrow.hidden = true;
      return;
    }
    const spdKn = Math.hypot(w[0], w[1]) * 1.94384; // m/s → knots
    const from = (Math.atan2(-w[0], -w[1]) * 180 / Math.PI + 360) % 360; // meteorological "from"
    this._ctlArrow.hidden = false;
    this._ctlArrow.style.transform = `rotate(${from}deg)`; // ↓ (south) rotated to blow-toward
    this._ctlVal.textContent = `${this.ctx.units.format("wind", spdKn)} ${Math.round(from)}° ${compass(from)}`;
  }

  _readoutPos() {
    const v = this.ctx.vessel.get();
    const p = v && v.navigation && v.navigation.position;
    if (p && typeof p.lat === "number") return { lng: p.lon, lat: p.lat };
    const c = this.ctx.map.getCenter(); // no fix → wind at the map centre
    return { lng: c.lng, lat: c.lat };
  }

  _setOn(on) {
    this._on = on;
    if (this._canvas) this._canvas.style.display = on ? "" : "none";
    this._syncSlider();
    this._updateReadout();
    if (on) this._start();
    else this._stop();
  }

  _start() {
    if (this._raf) return; // already animating
    const step = () => {
      this._raf = requestAnimationFrame(step);
      this._frame();
    };
    this._raf = requestAnimationFrame(step);
  }

  _stop() {
    if (this._raf) cancelAnimationFrame(this._raf);
    this._raf = 0;
    if (this._c2d) this._c2d.clearRect(0, 0, this._cw, this._ch);
  }

  _frame() {
    const c = this._c2d;
    if (!c || !this._grid) return;
    const W = this._cw, H = this._ch;
    // Fade previous frame (leaves fading tails).
    c.globalCompositeOperation = "destination-in";
    c.fillStyle = `rgba(0,0,0,${FADE})`;
    c.fillRect(0, 0, W, H);
    c.globalCompositeOperation = "source-over";
    c.lineCap = "round";

    const map = this.ctx.map;
    // Pass 1: advect every particle and collect its screen segment (flat array of
    // x0,y0,x1,y1,speed to avoid per-frame allocations).
    const segs = this._segs || (this._segs = []);
    segs.length = 0;
    for (const p of this._particles) {
      const wind = this._sample(p.lng, p.lat);
      if (!wind || p.age > MAX_AGE) {
        Object.assign(p, this._spawn());
        continue;
      }
      const a = map.project([p.lng, p.lat]);
      // Move in screen space for zoom-independent visual speed; v is northward → up.
      const nx = a.x + wind[0] * STEP;
      const ny = a.y - wind[1] * STEP;
      const b = map.unproject([nx, ny]);
      segs.push(a.x, a.y, nx, ny, Math.hypot(wind[0], wind[1]));
      p.lng = b.lng;
      p.lat = b.lat;
      p.age++;
    }
    // Pass 2a: a subtle dark casing (one batched stroke) so streamlines read on LIGHT
    // charts without looking heavy.
    c.strokeStyle = "rgba(0,0,0,0.4)";
    c.lineWidth = LINE_WIDTH + 1;
    c.beginPath();
    for (let i = 0; i < segs.length; i += 5) {
      c.moveTo(segs[i], segs[i + 1]);
      c.lineTo(segs[i + 2], segs[i + 3]);
    }
    c.stroke();
    // Pass 2b: bright coloured cores (visible on DARK charts) on top.
    c.lineWidth = LINE_WIDTH;
    for (let i = 0; i < segs.length; i += 5) {
      c.strokeStyle = rampColor(segs[i + 4]);
      c.beginPath();
      c.moveTo(segs[i], segs[i + 1]);
      c.lineTo(segs[i + 2], segs[i + 3]);
      c.stroke();
    }
  }

  _resize() {
    const map = this.ctx.map;
    const el = map.getContainer();
    const dpr = window.devicePixelRatio || 1;
    this._cw = el.clientWidth;
    this._ch = el.clientHeight;
    this._canvas.width = this._cw * dpr;
    this._canvas.height = this._ch * dpr;
    this._canvas.style.width = this._cw + "px";
    this._canvas.style.height = this._ch + "px";
    this._c2d.setTransform(dpr, 0, 0, dpr, 0, 0);
  }

  destroy() {
    if (this._raf) cancelAnimationFrame(this._raf);
    const map = this.ctx.map;
    if (this._onResize) map.off("resize", this._onResize);
    if (this._onZoom) map.off("zoomstart", this._onZoom);
    if (this._canvas) this._canvas.remove();
  }
}

// rampColor maps a wind speed (m/s) to a colour, linearly interpolated between the two
// bracketing RAMP stops for smooth transitions. Interp results are memoised per
// integer m/s so it stays cheap across thousands of particles per frame.
const rampCache = new Map();
function rampColor(spd) {
  const key = Math.round(spd);
  let c = rampCache.get(key);
  if (c) return c;
  let lo = RAMP[0], hi = RAMP[RAMP.length - 1];
  for (let i = 0; i < RAMP.length - 1; i++) {
    if (spd >= RAMP[i][0] && spd <= RAMP[i + 1][0]) {
      lo = RAMP[i];
      hi = RAMP[i + 1];
      break;
    }
  }
  const t = hi[0] === lo[0] ? 0 : Math.max(0, Math.min(1, (spd - lo[0]) / (hi[0] - lo[0])));
  c = lerpHex(lo[1], hi[1], t);
  rampCache.set(key, c);
  return c;
}

// parseWindBin decodes the plugin's binary wind blob (see encodeWindBin, Go side):
// a small aligned header then, per forecast step, an hour and zero-copy Float32 u/v.
function parseWindBin(buf) {
  const dv = new DataView(buf);
  if (dv.getUint8(0) !== 0x57 || dv.getUint8(1) !== 0x47 || dv.getUint8(2) !== 0x52 || dv.getUint8(3) !== 0x44) {
    return null; // not "WGRD"
  }
  const nx = dv.getUint32(8, true), ny = dv.getUint32(12, true), nSteps = dv.getUint32(16, true);
  const lo1 = dv.getFloat32(20, true), la1 = dv.getFloat32(24, true);
  const dx = dv.getFloat32(28, true), dy = dv.getFloat32(32, true);
  const np = nx * ny;
  let o = 36;
  const steps = [];
  for (let s = 0; s < nSteps; s++) {
    const hour = dv.getInt32(o, true);
    o += 4;
    const u = new Float32Array(buf, o, np);
    o += np * 4;
    const v = new Float32Array(buf, o, np);
    o += np * 4;
    steps.push({ hour, u, v });
  }
  const lo2 = lo1 + (nx - 1) * dx, la2 = la1 - (ny - 1) * dy;
  return { header: { nx, ny, lo1, la1, lo2, la2, dx, dy }, steps };
}

// compass maps a bearing (deg) to an 8-point label.
function compass(deg) {
  return ["N", "NE", "E", "SE", "S", "SW", "W", "NW"][Math.round(deg / 45) % 8];
}

// lerpHex blends two #rrggbb colours.
function lerpHex(a, b, t) {
  const pa = parseInt(a.slice(1), 16), pb = parseInt(b.slice(1), 16);
  const r = Math.round(((pa >> 16) & 255) * (1 - t) + ((pb >> 16) & 255) * t);
  const g = Math.round(((pa >> 8) & 255) * (1 - t) + ((pb >> 8) & 255) * t);
  const bl = Math.round((pa & 255) * (1 - t) + (pb & 255) * t);
  return `rgb(${r},${g},${bl})`;
}
