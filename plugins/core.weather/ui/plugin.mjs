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

    await this._loadGrid();
    this._seed();
    // The animation is driven by _setOn (from the overlay's persisted state,
    // registered above). If it started before the grid loaded, it now has data.
  }

  async _loadGrid() {
    try {
      const url = `${this.ctx.assets}plugins/${this.ctx.plugin.id}/serve/wind.json`;
      const doc = await (await fetch(url, { cache: "no-store" })).json();
      const u = doc.find((r) => r.header.parameterNumber === 2);
      const v = doc.find((r) => r.header.parameterNumber === 3);
      if (!u || !v) return;
      const h = u.header;
      this._grid = {
        nx: h.nx, ny: h.ny, lo1: h.lo1, la1: h.la1, dx: h.dx, dy: h.dy,
        lo2: h.lo2, la2: h.la2, u: u.data, v: v.data,
      };
    } catch (e) {
      this.ctx.plugin.log("warn", "wind grid load failed", e);
    }
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

  _setOn(on) {
    this._on = on;
    if (this._canvas) this._canvas.style.display = on ? "" : "none";
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
