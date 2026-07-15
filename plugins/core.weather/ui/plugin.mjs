// Wind (GRIB) — UI half. Self-contained: it loads dynamically from the plugin
// archive (/plugins/core.weather/ui/plugin.mjs) and uses only the ctx handles, no app
// imports. It fetches the wind grid the host-side plugin published
// (/plugins/<id>/serve/wind.json) and animates it as moving streamlines on a canvas
// over the chart — the "grid, not tiles" model: the render is entirely client-side.
//
// This is the use-at-your-own-risk tier: a particle field needs raw projection, so it
// uses ctx.map (accepting the same contract the built-ins live with) rather than the
// declarative layers API.

const PARTICLES = 3000;
const MAX_AGE = 90; // frames before a particle respawns
const STEP = 0.35; // screen px per (m/s) per frame
const FADE = 0.94; // trail persistence (higher = longer tails)

// Wind-speed colour ramp (m/s → colour), light→strong.
const RAMP = [
  [0, "#6ea8d8"], [5, "#78c07a"], [10, "#e8d24a"],
  [15, "#e79a3c"], [22, "#dc5a3c"], [32, "#b03060"],
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
    // Panning/zooming reprojects every frame; clear the trail canvas so old trails
    // don't smear at stale screen positions.
    this._onMove = () => this._c2d && this._c2d.clearRect(0, 0, canvas.width, canvas.height);
    map.on("movestart", this._onMove);
    map.on("zoomstart", this._onMove);

    // Show/hide from the Layers control — hiding stops the animation but keeps the
    // wind data loaded (the "visual only" contract, distinct from disabling the
    // plugin). onVisible fires immediately with the persisted state.
    ctx.overlays.register({
      id: "wind",
      title: "Wind streamlines",
      group: "Wind",
      onVisible: (v) => this._setOn(v),
    });

    this._mountSlider();
    await this._loadDoc();
    this._seed();
    // The animation is driven by _setOn (from the overlay's persisted state,
    // registered above). If it started before the grid loaded, it now has data.
  }

  async _loadDoc() {
    try {
      const url = `${this.ctx.assets}plugins/${this.ctx.plugin.id}/serve/wind.json`;
      const doc = await (await fetch(url, { cache: "no-store" })).json();
      if (!doc.header || !doc.steps || !doc.steps.length) return;
      this._doc = doc;
      this._setStep(0); // build the initial grid from the first forecast step
      this._buildSlider();
    } catch (e) {
      this.ctx.plugin.log("warn", "wind doc load failed", e);
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
  }

  // Bilinear-sample the wind at (lng,lat); returns [u,v] or null if off-grid.
  _sample(lng, lat) {
    const g = this._grid;
    if (!g) return null;
    const fx = (lng - g.lo1) / g.dx;
    const fy = (g.la1 - lat) / g.dy; // la1 is the north edge; rows go south
    if (fx < 0 || fy < 0 || fx > g.nx - 1 || fy > g.ny - 1) return null;
    const x0 = Math.floor(fx), y0 = Math.floor(fy);
    const x1 = Math.min(x0 + 1, g.nx - 1), y1 = Math.min(y0 + 1, g.ny - 1);
    const tx = fx - x0, ty = fy - y0;
    const at = (arr, x, y) => arr[y * g.nx + x];
    const bil = (arr) =>
      at(arr, x0, y0) * (1 - tx) * (1 - ty) + at(arr, x1, y0) * tx * (1 - ty) +
      at(arr, x0, y1) * (1 - tx) * ty + at(arr, x1, y1) * tx * ty;
    return [bil(g.u), bil(g.v)];
  }

  _seed() {
    this._particles = [];
    for (let i = 0; i < PARTICLES; i++) this._particles.push(this._spawn());
  }

  _spawn() {
    const g = this._grid;
    if (!g) return { lng: 0, lat: 0, age: 9999 };
    return {
      lng: g.lo1 + Math.random() * (g.lo2 - g.lo1),
      lat: g.la2 + Math.random() * (g.la1 - g.la2),
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

  _setOn(on) {
    this._on = on;
    if (this._canvas) this._canvas.style.display = on ? "" : "none";
    this._syncSlider();
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
    c.lineWidth = 1.2;

    const map = this.ctx.map;
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
      const spd = Math.hypot(wind[0], wind[1]);
      c.strokeStyle = rampColor(spd);
      c.beginPath();
      c.moveTo(a.x, a.y);
      c.lineTo(nx, ny);
      c.stroke();
      p.lng = b.lng;
      p.lat = b.lat;
      p.age++;
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
    if (this._onMove) {
      map.off("movestart", this._onMove);
      map.off("zoomstart", this._onMove);
    }
    if (this._canvas) this._canvas.remove();
  }
}

// rampColor maps a wind speed (m/s) to a colour along RAMP.
function rampColor(spd) {
  let lo = RAMP[0], hi = RAMP[RAMP.length - 1];
  for (let i = 0; i < RAMP.length - 1; i++) {
    if (spd >= RAMP[i][0] && spd <= RAMP[i + 1][0]) {
      lo = RAMP[i];
      hi = RAMP[i + 1];
      break;
    }
  }
  return hi[1]; // stepped ramp is plenty for streamlines; avoids per-pixel lerp cost
}
