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
// isn't a solid mat of lines. Kept deliberately sparse — wind is a background hint,
// not a wall of streamlines.
const DENSITY = 170; // particles per megapixel of map
const MAX_PARTICLES = 420; // hard cap on big screens
const MAX_AGE = 110; // frames before a particle respawns
const STEP = 0.1; // screen px per (m/s) per frame on top of MIN_ADV
// MIN_ADV is a floor on the per-frame advance (px) once there is any real wind: a
// pure speed-proportional advance makes a 6 kt streak ~2 px — invisible. With the
// floor, light air draws short-but-visible drifting streaks; the speed term still
// makes strong wind race.
const MIN_ADV = 0.6;
// Streamline length tracks the wind: the trail keeps between TRAIL_MIN points (calm —
// a short stub) and TRAIL_MAX (TRAIL_REF m/s and above — a long comet), on a square-
// root curve so the low end doesn't collapse: ~16 px at 6 kt, ~45 px at 20 kt,
// ~80 px in gale. Length AND motion speed both read as wind strength.
const TRAIL_MIN = 6;
const TRAIL_MAX = 34;
const TRAIL_REF = 16; // m/s (~31 kt) at which a streamline reaches full length
const LINE_WIDTH = 2.2;

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
    // Pause the particle field entirely while ZOOMING — every skipped frame is a few
    // hundred projections the map renderer gets back, so the zoom stays fluid; the
    // field pops back the moment the gesture ends. (The clear also matters: the
    // projection scale changes, so anything left would stretch.) Do NOT pause on pan:
    // the follow camera eases the map on every fix, and re-projecting per frame
    // already tracks a pan without artifacts.
    this._onZoom = () => {
      this._zooming = true;
      if (this._c2d) this._c2d.clearRect(0, 0, this._cw, this._ch);
    };
    this._onZoomEnd = () => {
      this._zooming = false;
    };
    map.on("zoomstart", this._onZoom);
    map.on("zoomend", this._onZoomEnd);
    // The pill's arrow is drawn in the chart's frame — retarget it as the map turns
    // (course-/head-up follow rotates continuously).
    this._onRotate = () => {
      this._updateReadout();
      this._updateProbe(); // its arrow is drawn in the chart's frame too
    };
    map.on("rotate", this._onRotate);
    // Tap-to-probe: while the wind layer is on, a chart tap drops a marker reading
    // out the wind at that spot (tap the marker to dismiss). Registered through the
    // shell's tap-claim chain: returning true consumes the tap, so the ECDIS pick
    // report doesn't also fire; while the layer is off we pass, and taps behave as
    // if the plugin weren't there. (Fallback for older shells: plain map click,
    // unclaimed.)
    const onTap = (e) => {
      if (!this._on || !this._doc || !e.lngLat) return false;
      this._setProbe({ lng: e.lngLat.lng, lat: e.lngLat.lat });
      return true;
    };
    if (ctx.taps && ctx.taps.claim) {
      ctx.taps.claim(onTap);
    } else {
      this._onClick = onTap;
      map.on("click", this._onClick);
    }

    // Show/hide from the Layers control AND the on-map panel — both drive the same
    // registry entry, so they stay in sync. Hiding stops the animation but keeps the
    // wind data loaded (visual-only, distinct from disabling the plugin).
    this._layer = ctx.overlays.register({
      id: "wind",
      title: "Wind streamlines",
      group: "Wind",
      onVisible: (v) => this._setOn(v),
    });

    this._mountPanel();
    await this._loadDoc();
    this._seed();
    // Keep the forecast current: check every 10 min whether a newer model cycle
    // must exist and re-pull if so (the ↻ button does the same on demand).
    this._autoTimer = setInterval(() => this._autoRefresh(), 600000);
    // The animation is driven by _setOn (from the overlay's persisted state,
    // registered above). If it started before the grid loaded, it now has data.
  }

  async _loadDoc() {
    try {
      // The GFS base doc, the CONUS-wide HRRR look-around layer, and the optional
      // 3 km HRRR window (published only when the plugin has a sailing-area centre —
      // see _maybeSetCenter).
      const [base, hi, mid] = await Promise.all([
        this._fetchDoc("wind.bin"), this._fetchDoc("wind-hi.bin"), this._fetchDoc("wind-mid.bin"),
      ]);
      if (!base) return;
      this._doc = base;
      this._hi = hi;
      this._mid = mid;
      this._setStep(0); // build the initial grid from the first forecast step
      this._buildSlider();
      this._maybeSetCenter();
    } catch (e) {
      this.ctx.plugin.log("warn", "wind grid load failed", e);
    }
  }

  async _fetchDoc(name) {
    try {
      const r = await fetch(`${this.ctx.assets}plugins/${this.ctx.plugin.id}/serve/${name}`, { cache: "no-store" });
      if (!r.ok) return null;
      return parseWindBin(await r.arrayBuffer());
    } catch {
      return null;
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
    this._frac = frac;
    this._hour = s0.hour * (1 - t) + s1.hour * t;
    // The HRRR layers interpolated to the same valid time (3 km window + CONUS).
    this._hiGrid = this._buildDocGrid(this._hi);
    this._midGrid = this._buildDocGrid(this._mid);
    this._updateTimeLabel();
    this._updateReadout(); // conditions at the vessel change with the forecast step
    this._updateDetails();
  }

  // _buildDocGrid interpolates one HRRR doc's u/v at the base doc's current valid
  // time. Null when there is no doc or the valid time is outside its horizon —
  // sampling then falls through to the next layer.
  _buildDocGrid(hi) {
    const base = this._doc;
    if (!hi || !base || !Number.isFinite(hi.refMs) || !Number.isFinite(base.refMs)) return null;
    const lead = (base.refMs + (this._hour || 0) * 3600000 - hi.refMs) / 3600000;
    const s = hi.steps;
    if (!s.length || lead < s[0].hour - 0.75 || lead > s[s.length - 1].hour + 0.75) return null;
    let i0 = 0;
    while (i0 < s.length - 2 && lead > s[i0 + 1].hour) i0++;
    const i1 = Math.min(i0 + 1, s.length - 1);
    const span = s[i1].hour - s[i0].hour;
    const t = span > 0 ? Math.max(0, Math.min(1, (lead - s[i0].hour) / span)) : 0;
    const len = s[i0].u.length;
    const u = new Array(len), v = new Array(len);
    for (let k = 0; k < len; k++) {
      u[k] = s[i0].u[k] * (1 - t) + s[i1].u[k] * t;
      v[k] = s[i0].v[k] * (1 - t) + s[i1].v[k] * t;
    }
    const h = hi.header;
    return { nx: h.nx, ny: h.ny, lo1: h.lo1, la1: h.la1, lo2: h.lo2, la2: h.la2, dx: h.dx, dy: h.dy, u, v };
  }

  // _updateTimeLabel writes the active forecast's valid time (UTC + local) and lead
  // hours into the scrubber's readout.
  _updateTimeLabel() {
    const d = this._doc;
    if (!d || !this._label) return;
    const leadH = this._hour || 0;
    if (Number.isFinite(d.refMs)) {
      const valid = new Date(d.refMs + leadH * 3600000);
      this._label.textContent = fmtShortLocal(valid); // slim row: "Thu 14:00" (ship's clock)
      if (this._sublabel) this._sublabel.textContent = `${fmtLocalFull(valid)} · ${fmtZ(valid)}`;
    } else {
      this._label.textContent = `+${Math.round(leadH)}h`;
      if (this._sublabel) this._sublabel.textContent = "";
    }
    if (this._lead) {
      const near = Math.abs(leadH - this._nowLeadH()) < 1.5;
      this._lead.textContent = near ? "NOW" : `+${Math.round(leadH)}h`;
      this._lead.classList.toggle("now", near);
    }
  }

  // _nowLeadH is the lead time (hours from the cycle) that corresponds to the wall
  // clock right now — used to mark the "now" position on the scrubber.
  _nowLeadH() {
    const d = this._doc;
    if (!d || !Number.isFinite(d.refMs)) return 0;
    return (Date.now() - d.refMs) / 3600000;
  }

  // Bilinear-sample the wind at (lng,lat), best source first: the 3 km HRRR window
  // where the vessel sails, the ~15 km HRRR CONUS layer while looking around, the
  // GFS base offshore / beyond the HRRR horizon. Returns [u,v] or null off-grid.
  _sample(lng, lat) {
    for (const g of [this._hiGrid, this._midGrid, this._grid]) {
      if (!g) continue;
      const w = this._sampleGrid(g, lng, lat);
      if (w) return w;
    }
    return null;
  }

  // _sampleGrid bilinear-samples one interpolated u/v grid. Handles global grids
  // expressed in 0–360° longitude (GFS) as well as −180..180; NaN cells (the HRRR
  // window outside the model domain) count as off-grid.
  _sampleGrid(g, lng, lat) {
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
    const u = bil(g.u), v = bil(g.v);
    if (!Number.isFinite(u) || !Number.isFinite(v)) return null;
    return [u, v];
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
      trail: [], // geographic [lng,lat] history, re-projected each frame (map-locked)
    };
  }

  // _mountPanel builds the two HUD pieces: the top-centre weather card (forecast
  // time scrubber + detail tiles + per-step strip, hidden until data loads / the
  // overlay is shown) and the on-map toggle/readout pill. Mounted in the shell
  // chrome; theme vars inherit.
  _mountPanel() {
    const hud = this.ctx.hud.mount("wind-time");
    // The weather card docks TOP-CENTRE, below the topbar — clear of the crowded
    // bottom-centre chrome (data card, toasts, scale). It reads out the valid
    // date/time (UTC primary, local secondary) of the step under the scrubber
    // handle, the conditions at the vessel for that time (wind, gust, temperature,
    // cloud cover), and a per-step strip for the whole forecast series.
    hud.innerHTML = `<style>
      .wt{position:absolute;left:50%;transform:translateX(-50%);
        top:calc(var(--topbar-h,0px) + 10px);z-index:6;display:none;flex-direction:column;
        width:min(92vw,400px);box-sizing:border-box;padding:5px 9px 6px;border-radius:11px;
        background:var(--ui-surface,#161b22);border:1px solid var(--ui-border,#30363d);
        box-shadow:0 4px 16px rgba(0,0,0,.3);color:var(--ui-text,#e6edf3);
        font:12px/1.2 system-ui,-apple-system,sans-serif;}
      .wt .row{display:flex;align-items:center;gap:7px;}
      .wt .cap{font-size:13px;line-height:1;}
      .wt input{flex:1;height:3px;min-width:0;accent-color:var(--ui-accent,#2f81f7);cursor:pointer;}
      .wt .lead{min-width:34px;text-align:center;font-size:10px;font-weight:700;font-variant-numeric:tabular-nums;
        padding:2px 5px;border-radius:7px;background:var(--ui-border,#30363d);opacity:.85;}
      .wt .lead.now{background:var(--ui-accent,#2f81f7);color:#fff;opacity:1;}
      .wt .lbl{font-weight:700;font-size:11.5px;font-variant-numeric:tabular-nums;white-space:nowrap;}
      .wt .tgl{border:0;background:none;color:inherit;cursor:pointer;font:inherit;font-size:10px;
        opacity:.65;padding:2px 3px;line-height:1;}
      .wt .tgl:hover{opacity:1;}
      .wt .tgl.spin{animation:wtspin 1s linear infinite;}
      @keyframes wtspin{to{transform:rotate(360deg)}}
      .wt .body{display:none;}
      .wt.open .body{display:block;}
      .wt .sum{display:flex;gap:5px;align-items:center;justify-content:center;margin-top:4px;
        font-size:10.5px;font-variant-numeric:tabular-nums;white-space:nowrap;overflow:hidden;}
      .wt .sum b{font-weight:700;}
      .wt .sum .dim{opacity:.55;}
      .wt .sum .dar{display:inline-block;font-size:11px;}
      .wt .strip{display:flex;gap:2px;margin-top:6px;overflow-x:auto;scrollbar-width:thin;}
      .wt .cell{flex:1 1 0;min-width:34px;display:flex;flex-direction:column;align-items:center;gap:1px;
        padding:3px 1px;border-radius:6px;border:1px solid transparent;background:none;cursor:pointer;
        color:inherit;font:inherit;}
      .wt .cell:hover{background:rgba(127,127,127,.12);}
      .wt .cell.on{background:rgba(47,129,247,.16);border-color:var(--ui-accent,#2f81f7);}
      .wt .cell .ct{font-size:8.5px;font-weight:600;opacity:.6;font-variant-numeric:tabular-nums;white-space:nowrap;}
      .wt .cell .ca{font-size:11px;line-height:1;}
      .wt .cell .cs{font-size:10.5px;font-weight:700;font-variant-numeric:tabular-nums;}
      .wt .cell .cc{font-size:10.5px;line-height:1;}
      .wt .foot{margin-top:5px;font-size:9.5px;opacity:.6;text-align:center;font-variant-numeric:tabular-nums;}
    </style><div class="wt">
      <div class="row">
        <span class="cap" title="GFS weather">⛅</span>
        <span class="lead">NOW</span>
        <input type="range" min="0" max="0" value="0">
        <span class="lbl">—</span>
        <button class="tgl" id="rf" title="Refresh forecast">↻</button>
        <button class="tgl" id="fold" title="Forecast details">▼</button>
      </div>
      <div class="sum">
        <span class="dar" id="dwa">↓</span> <b id="dw">—</b> <span class="dim" id="dwd">—</span>
        <span class="dim">·</span> <span class="dim">G</span> <b id="dg">—</b>
        <span class="dim">·</span> <b id="dt">—</b>
        <span class="dim">·</span> <b id="dc">—</b>
      </div>
      <div class="body">
        <div class="strip" id="strip"></div>
        <div class="foot"><span class="sub">—</span> · <span id="dcw">—</span> · at <span id="at">vessel</span></div>
      </div>
    </div>`;
    this._sliderWrap = hud.querySelector(".wt");
    this._slider = hud.querySelector("input");
    this._label = hud.querySelector(".lbl");
    this._sublabel = hud.querySelector(".sub");
    this._lead = hud.querySelector(".lead");
    this._atLabel = hud.querySelector("#at");
    this._tiles = {
      windArrow: hud.querySelector("#dwa"), wind: hud.querySelector("#dw"), windDir: hud.querySelector("#dwd"),
      gust: hud.querySelector("#dg"), temp: hud.querySelector("#dt"),
      cloud: hud.querySelector("#dc"), cloudWord: hud.querySelector("#dcw"),
    };
    this._strip = hud.querySelector("#strip");
    this._slider.addEventListener("input", () => this._setStep(Number(this._slider.value) / 10));
    // Manual forecast refresh — bumps a config nonce the plugin watches, then polls
    // for the re-published series.
    this._rfBtn = hud.querySelector("#rf");
    this._rfBtn.addEventListener("click", () => this._refresh());

    // Details fold out on demand and the choice sticks — collapsed, the card is a
    // slim one-row scrubber that hides almost none of the chart.
    const tgl = hud.querySelector("#fold");
    const setOpen = (open) => {
      this._sliderWrap.classList.toggle("open", open);
      tgl.textContent = open ? "▲" : "▼";
      try { localStorage.setItem("core.weather.panelOpen", open ? "1" : ""); } catch { /* private mode */ }
    };
    let saved = "";
    try { saved = localStorage.getItem("core.weather.panelOpen") || ""; } catch { /* private mode */ }
    setOpen(saved === "1");
    tgl.addEventListener("click", () => setOpen(!this._sliderWrap.classList.contains("open")));

    // The on-map pill: enable/disable toggle AND live wind readout (speed in the
    // mariner's units + compass direction it blows FROM, with an arrow pointing
    // where it blows, in the chart's frame). Kept in sync with the Layers control
    // via the shared registry.
    const pill = this.ctx.hud.mount("wind-control");
    pill.innerHTML = `<style>
      .wc{position:absolute;right:calc(12px + env(safe-area-inset-right,0px));top:calc(var(--topbar-h,0px) + 60px);
        z-index:6;display:flex;align-items:center;gap:7px;padding:6px 12px;border-radius:16px;cursor:pointer;
        background:var(--ui-surface,#161b22);border:1px solid var(--ui-border,#30363d);
        color:var(--ui-text,#e6edf3);font:600 12px/1 system-ui,sans-serif;box-shadow:0 3px 14px rgba(0,0,0,.28);
        -webkit-user-select:none;user-select:none;}
      .wc.off{opacity:.65;}
      .wc .arrow{display:inline-block;font-size:13px;line-height:1;}
    </style><div class="wc off" id="c"><span>🌬</span><span class="arrow" id="ar" hidden>↓</span><span id="v">Wind</span></div>`;
    this._ctl = pill.querySelector("#c");
    this._ctlVal = pill.querySelector("#v");
    this._ctlArrow = pill.querySelector("#ar");
    this._ctl.addEventListener("click", () => this._layer.toggle());
    this.ctx.vessel.subscribe(() => {
      this._updateReadout();
      this._updateDetails();
      this._maybeSetCenter();
    });
  }

  // _maybeSetCenter keeps the plugin's HRRR window centred where the vessel actually
  // sails — or, without a GPS fix, where the user is LOOKING (map centre, once
  // zoomed in enough to mean a place rather than the whole coast). When that spot
  // has no high-res coverage, write the new centre into the plugin config — the
  // plugin hot-reloads it and refetches HRRR around it. Rate-limited; the window is
  // ~5°×6°, so in practice this fires once per cruising ground / area of interest.
  _maybeSetCenter() {
    if (this._centerWriting) return;
    if (!this._hasFix() && this.ctx.map.getZoom() < 8) return; // viewport too vague to anchor a window
    const pos = this._readoutPos();
    if (pos.lat < 21 || pos.lat > 49 || pos.lng < -128 || pos.lng > -62) return; // outside HRRR CONUS
    const h = this._hi && this._hi.header;
    if (h) {
      const cLat = h.la1 - ((h.ny - 1) * h.dy) / 2;
      const cLon = h.lo1 + ((h.nx - 1) * h.dx) / 2;
      if (Math.abs(pos.lat - cLat) < 1.5 && Math.abs(pos.lng - cLon) < 2) return; // window still fits
    }
    if (this._centerSetAt && Date.now() - this._centerSetAt < 600000) return;
    this._centerSetAt = Date.now();
    this._centerWriting = true;
    this._writeCenter(pos).finally(() => {
      this._centerWriting = false;
    });
  }

  async _writeCenter(pos) {
    try {
      const base = this.ctx.assets || "/";
      const id = this.ctx.plugin.id;
      const list = await (await fetch(`${base}api/plugins`)).json();
      const me = (list.plugins || []).find((p) => p.record && p.record.id === id);
      const config = {
        ...((me && me.record.config) || {}),
        hiLat: Math.round(pos.lat * 20) / 20,
        hiLon: Math.round(pos.lng * 20) / 20,
      };
      await fetch(`${base}api/plugins/${encodeURIComponent(id)}/config`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(config),
      });
      this.ctx.plugin.log("log", `HRRR window → ${config.hiLat},${config.hiLon}`);
      this._pollHi(20); // the plugin refetches HRRR (~a minute); pick it up when it lands
    } catch (e) {
      this.ctx.plugin.log("warn", "HRRR window update failed", e);
    }
  }

  // _refresh asks the plugin to re-pull the newest forecast cycles (GFS + HRRR) by
  // bumping a "refresh" nonce in its config — the plugin hot-reloads config, so no
  // restart — then polls for the re-published series.
  async _refresh() {
    if (this._refreshing) return;
    this._refreshing = true;
    if (this._rfBtn) this._rfBtn.classList.add("spin");
    try {
      const base = this.ctx.assets || "/";
      const id = this.ctx.plugin.id;
      const list = await (await fetch(`${base}api/plugins`)).json();
      const me = (list.plugins || []).find((p) => p.record && p.record.id === id);
      const config = { ...((me && me.record.config) || {}), refresh: String(Date.now()) };
      await fetch(`${base}api/plugins/${encodeURIComponent(id)}/config`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(config),
      });
      this._pollRefresh(20);
    } catch (e) {
      this.ctx.plugin.log("warn", "refresh failed", e);
      this._refreshDone();
    }
  }

  _refreshDone() {
    this._refreshing = false;
    if (this._rfBtn) this._rfBtn.classList.remove("spin");
  }

  // _pollRefresh waits for the plugin to publish a newer base doc (the last fetch of
  // its chain), then reloads all layers and re-centres the scrubber on "now". If no
  // newer cycle exists upstream the docs come back unchanged and polling just ends.
  _pollRefresh(remaining) {
    clearTimeout(this._rfTimer);
    if (remaining <= 0) {
      this._refreshDone();
      return;
    }
    this._rfTimer = setTimeout(async () => {
      const doc = await this._fetchDoc("wind.bin");
      if (doc && (!this._doc || doc.refMs !== this._doc.refMs)) {
        this._doc = doc;
        this._hi = await this._fetchDoc("wind-hi.bin");
        this._mid = await this._fetchDoc("wind-mid.bin");
        this._buildSlider(); // re-anchor on the new cycle's "now"
        this._refreshDone();
        return;
      }
      this._pollRefresh(remaining - 1);
    }, 15000);
  }

  // _autoRefresh (10-minutely) re-pulls when a materially newer cycle must exist:
  // GFS cycles 6-hourly (+~4 h publish lag), HRRR hourly. Rate-limited so a dry
  // upstream doesn't cause a fetch storm.
  _autoRefresh() {
    const now = Date.now();
    const age = (d) => (d && Number.isFinite(d.refMs) ? now - d.refMs : 0);
    const stale = age(this._doc) > 10.5 * 3600000 || (this._hi && age(this._hi) > 3 * 3600000);
    if (!stale) return;
    if (this._lastAuto && now - this._lastAuto < 1800000) return;
    this._lastAuto = now;
    this._refresh();
  }

  // _pollHi watches for a fresh wind-hi.bin after a window/config change (and picks
  // up the matching look-around layer alongside it).
  _pollHi(remaining) {
    clearTimeout(this._hiTimer);
    if (remaining <= 0) return;
    this._hiTimer = setTimeout(async () => {
      const doc = await this._fetchDoc("wind-hi.bin");
      const fresh = doc && (!this._hi ||
        doc.refMs !== this._hi.refMs ||
        doc.header.lo1 !== this._hi.header.lo1 || doc.header.la1 !== this._hi.header.la1);
      if (fresh) {
        this._hi = doc;
        this._mid = (await this._fetchDoc("wind-mid.bin")) || this._mid;
        this._setStep(this._frac || 0); // rebuild grids at the current time
        return;
      }
      this._pollHi(remaining - 1);
    }, 30000);
  }

  _buildSlider() {
    if (!this._slider || !this._doc) return;
    const steps = this._doc.steps;
    this._slider.max = String((steps.length - 1) * 10); // ×10 for smooth interpolation
    this._buildStrip();
    // Default to the CURRENT time (the forecast valid now), interpolated between steps.
    const nowH = Number.isFinite(this._doc.refMs) ? (Date.now() - this._doc.refMs) / 3600000 : 0;
    const frac = this._fracForHour(nowH);
    this._slider.value = String(Math.round(frac * 10));
    this._setStep(frac);
    this._syncSlider();
  }

  // _buildStrip creates one cell per forecast step (time, wind arrow, speed, cloud);
  // the live values are filled by _updateDetails. Clicking a cell jumps the scrubber.
  _buildStrip() {
    if (!this._strip) return;
    this._strip.innerHTML = "";
    this._cells = this._doc.steps.map((s, i) => {
      const cell = document.createElement("button");
      cell.className = "cell";
      cell.innerHTML = `<div class="ct">${this._stepLabel(s.hour)}</div>
        <div class="ca">·</div><div class="cs">—</div><div class="cc"></div>`;
      cell.addEventListener("click", () => {
        this._slider.value = String(i * 10);
        this._setStep(i);
      });
      this._strip.appendChild(cell);
      return cell;
    });
  }

  // _stepLabel is a step's short strip caption: LOCAL weekday + hour ("Thu 14"), or
  // the lead time when the cycle time is unknown.
  _stepLabel(hour) {
    const d = this._doc;
    if (!Number.isFinite(d.refMs)) return `+${hour}h`;
    const t = new Date(d.refMs + hour * 3600000);
    return `${WD[t.getDay()]} ${String(t.getHours()).padStart(2, "0")}`;
  }

  // _fracForHour maps a forecast lead (hours) to a fractional step index.
  _fracForHour(h) {
    const s = this._doc.steps;
    if (h <= s[0].hour) return 0;
    for (let i = 0; i < s.length - 1; i++) {
      if (h <= s[i + 1].hour) return i + (h - s[i].hour) / (s[i + 1].hour - s[i].hour);
    }
    return s.length - 1;
  }

  _syncSlider() {
    if (this._sliderWrap) {
      this._sliderWrap.style.display = this._on && this._doc && this._doc.steps.length > 0 ? "flex" : "none";
    }
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
    // ↓ (south) rotated to blow-toward, in the CHART's frame: when the map runs
    // course-/head-up its bearing rotates the world, so subtract it or the arrow
    // (like the streamlines before the same fix) points wrong on a rotated chart.
    this._ctlArrow.style.transform = `rotate(${from - this.ctx.map.getBearing()}deg)`;
    this._ctlVal.textContent = `${this.ctx.units.format("wind", spdKn)} ${Math.round(from)}° ${compass(from)}`;
  }

  // _updateDetails fills the card's tiles (conditions at the vessel for the scrubbed
  // time) and the per-step strip. Arrows here are data readouts, drawn north-up like
  // any forecast table — only on-chart glyphs follow the map's rotation.
  _updateDetails() {
    const t = this._tiles, d = this._doc;
    if (!t || !d) return;
    const pos = this._readoutPos();
    if (this._atLabel) {
      const src = this._hiGrid && this._sampleGrid(this._hiGrid, pos.lng, pos.lat) ? "HRRR 3 km"
        : this._midGrid && this._sampleGrid(this._midGrid, pos.lng, pos.lat) ? "HRRR 15 km"
          : "GFS 0.25°";
      this._atLabel.textContent = `${this._hasFix() ? "vessel" : "map centre"} (${src})`;
    }

    const w = this._sample(pos.lng, pos.lat);
    if (w) {
      const spdKn = Math.hypot(w[0], w[1]) * 1.94384;
      const from = (Math.atan2(-w[0], -w[1]) * 180 / Math.PI + 360) % 360;
      t.windArrow.style.transform = `rotate(${from}deg)`;
      t.wind.textContent = this.ctx.units.format("wind", spdKn);
      t.windDir.textContent = `${Math.round(from)}° ${compass(from)}`;
    } else {
      t.windArrow.style.transform = "";
      t.wind.textContent = "—";
      t.windDir.textContent = "—";
    }
    const frac = this._frac || 0;
    const gust = this._sampleAt("gust", frac, pos);
    t.gust.textContent = Number.isFinite(gust) ? this.ctx.units.format("wind", gust * 1.94384) : "—";
    const temp = this._sampleAt("temp", frac, pos);
    t.temp.textContent = Number.isFinite(temp) ? this.ctx.units.format("temp", temp) : "—";
    const cloud = this._sampleAt("cloud", frac, pos);
    t.cloud.textContent = Number.isFinite(cloud) ? `${cloudIcon(cloud)} ${Math.round(cloud)}%` : "—";
    t.cloudWord.textContent = Number.isFinite(cloud) ? cloudWord(cloud) : " ";

    if (this._cells) {
      const active = Math.round(frac);
      d.steps.forEach((s, i) => {
        const cell = this._cells[i];
        if (!cell) return;
        cell.classList.toggle("on", i === active);
        const validMs = Number.isFinite(d.refMs) ? d.refMs + s.hour * 3600000 : NaN;
        // Per-field: the HRRR layers first (matched by valid time), then this step.
        const val = (name) => {
          if (Number.isFinite(validMs)) {
            for (const doc of [this._hi, this._mid]) {
              const hv = this._docSample(doc, name, validMs, pos);
              if (Number.isFinite(hv)) return hv;
            }
          }
          return this._docArr(d, s[name], pos.lng, pos.lat);
        };
        const u = val("u"), v = val("v");
        const ar = cell.querySelector(".ca"), sp = cell.querySelector(".cs"), cl = cell.querySelector(".cc");
        if (Number.isFinite(u) && Number.isFinite(v)) {
          const from = (Math.atan2(-u, -v) * 180 / Math.PI + 360) % 360;
          const spdKn = Math.hypot(u, v) * 1.94384;
          ar.textContent = "↓";
          ar.style.transform = `rotate(${from}deg)`;
          sp.textContent = this.ctx.units.format("wind", spdKn).replace(/[^\d.\-]+$/, "").trim();
        } else {
          ar.textContent = "·";
          sp.textContent = "—";
        }
        const cc = val("cloud");
        cl.textContent = Number.isFinite(cc) ? cloudIcon(cc) : "";
        const g = val("gust");
        const tc = val("temp");
        cell.title = [
          Number.isFinite(u) && Number.isFinite(v)
            ? `Wind ${this.ctx.units.format("wind", Math.hypot(u, v) * 1.94384)} from ${Math.round((Math.atan2(-u, -v) * 180 / Math.PI + 360) % 360)}°`
            : null,
          Number.isFinite(g) ? `Gust ${this.ctx.units.format("wind", g * 1.94384)}` : null,
          Number.isFinite(tc) ? `Temp ${this.ctx.units.format("temp", tc)}` : null,
          Number.isFinite(cc) ? `Cloud ${Math.round(cc)}% (${cloudWord(cc)})` : null,
        ].filter(Boolean).join("\n");
      });
    }
    this._updateProbe(); // the probe reads the same scrubbed time
  }

  // _docArr bilinearly samples one raw step plane of a doc at (lng,lat); NaN when
  // the plane is absent or the point is off-grid.
  _docArr(doc, arr, lng, lat) {
    if (!arr || !doc) return NaN;
    const h = doc.header;
    const fx = (lng - h.lo1) / h.dx;
    const fy = (h.la1 - lat) / h.dy;
    if (fx < 0 || fx > h.nx - 1 || fy < 0 || fy > h.ny - 1) return NaN;
    const x0 = Math.floor(fx), y0 = Math.floor(fy);
    const x1 = Math.min(x0 + 1, h.nx - 1), y1 = Math.min(y0 + 1, h.ny - 1);
    const tx = fx - x0, ty = fy - y0;
    return arr[y0 * h.nx + x0] * (1 - tx) * (1 - ty) + arr[y0 * h.nx + x1] * tx * (1 - ty) +
      arr[y1 * h.nx + x0] * (1 - tx) * ty + arr[y1 * h.nx + x1] * tx * ty;
  }

  // _docSample samples a doc's named plane at an absolute valid time, blending the
  // two bracketing forecast steps; NaN when the doc lacks the field, the time is
  // outside its horizon, or the point is outside its coverage.
  _docSample(doc, name, validMs, pos) {
    if (!doc || !Number.isFinite(doc.refMs)) return NaN;
    const lead = (validMs - doc.refMs) / 3600000;
    const s = doc.steps;
    if (!s.length || lead < s[0].hour - 0.75 || lead > s[s.length - 1].hour + 0.75) return NaN;
    let i0 = 0;
    while (i0 < s.length - 2 && lead > s[i0 + 1].hour) i0++;
    const i1 = Math.min(i0 + 1, s.length - 1);
    const span = s[i1].hour - s[i0].hour;
    const t = span > 0 ? Math.max(0, Math.min(1, (lead - s[i0].hour) / span)) : 0;
    const a = this._docArr(doc, s[i0][name], pos.lng, pos.lat);
    const b = this._docArr(doc, s[i1][name], pos.lng, pos.lat);
    if (!Number.isFinite(a)) return b;
    if (!Number.isFinite(b)) return a;
    return a * (1 - t) + b * t;
  }

  // _sampleAt samples a named scalar plane at a fractional base-step position —
  // HRRR high-res first (by valid time), then the GFS base steps.
  _sampleAt(name, frac, pos) {
    const base = this._doc;
    const s = base && base.steps;
    if (!s || !s.length) return NaN;
    const i0 = Math.max(0, Math.min(s.length - 1, Math.floor(frac)));
    const i1 = Math.min(i0 + 1, s.length - 1);
    const t = frac - i0;
    if (Number.isFinite(base.refMs)) {
      const hour = s[i0].hour * (1 - t) + s[i1].hour * t;
      const validMs = base.refMs + hour * 3600000;
      for (const doc of [this._hi, this._mid]) {
        const hv = this._docSample(doc, name, validMs, pos);
        if (Number.isFinite(hv)) return hv;
      }
    }
    const a = this._docArr(base, s[i0][name], pos.lng, pos.lat);
    const b = this._docArr(base, s[i1][name], pos.lng, pos.lat);
    if (!Number.isFinite(a)) return b;
    if (!Number.isFinite(b)) return a;
    return a * (1 - t) + b * t;
  }

  _hasFix() {
    const v = this.ctx.vessel.get();
    const p = v && v.navigation && v.navigation.position;
    return !!(p && typeof p.lat === "number");
  }

  // _setProbe drops (or moves) the tap-probe marker and fills its readout.
  _setProbe(pos) {
    this._probePos = pos;
    if (!this._probeMarker) {
      this._probeMarker = this.ctx.markers.add("wind-probe", { rotationAlignment: "viewport", anchor: "bottom" });
      this._probeMarker.element.style.cursor = "pointer";
      this._probeMarker.onClick((e) => {
        e.stopPropagation(); // don't let the map click re-drop the probe
        this._clearProbe();
      });
    }
    this._probeMarker.setLngLat([pos.lng, pos.lat]);
    this._updateProbe();
  }

  _clearProbe() {
    this._probePos = null;
    if (this._probeMarker) this._probeMarker.hide();
  }

  // _updateProbe re-samples the probe point for the currently scrubbed time; called
  // on tap, step change, map rotation, and data reloads so the chip stays live.
  _updateProbe() {
    if (!this._probePos || !this._probeMarker || !this._on) return;
    const pos = this._probePos;
    const w = this._sample(pos.lng, pos.lat);
    const chip = (main, sub) => `
      <div style="transform:translateY(-6px);display:flex;flex-direction:column;align-items:center;">
        <div style="display:flex;align-items:center;gap:6px;padding:5px 9px;border-radius:10px;
          background:var(--ui-surface,#161b22);border:1px solid var(--ui-border,#30363d);
          color:var(--ui-text,#e6edf3);font:600 11px/1.3 system-ui,sans-serif;
          box-shadow:0 3px 12px rgba(0,0,0,.35);white-space:nowrap;">${main}</div>
        ${sub}
        <div style="width:0;height:0;border:5px solid transparent;border-top-color:var(--ui-border,#30363d);"></div>
      </div>`;
    if (!w) {
      this._probeMarker.setHTML(chip(`<span style="opacity:.7;">no wind data here</span>`, ""));
      this._probeMarker.show();
      return;
    }
    const spdKn = Math.hypot(w[0], w[1]) * 1.94384;
    const from = (Math.atan2(-w[0], -w[1]) * 180 / Math.PI + 360) % 360;
    const gust = this._sampleAt("gust", this._frac || 0, pos);
    const src = this._hiGrid && this._sampleGrid(this._hiGrid, pos.lng, pos.lat) ? "HRRR 3 km"
      : this._midGrid && this._sampleGrid(this._midGrid, pos.lng, pos.lat) ? "HRRR 15 km"
        : "GFS 0.25°";
    const sub = [Number.isFinite(gust) ? `G ${this.ctx.units.format("wind", gust * 1.94384)}` : null, src]
      .filter(Boolean).join(" · ");
    this._probeMarker.setHTML(chip(
      `<span style="display:inline-block;font-size:13px;transform:rotate(${from - this.ctx.map.getBearing()}deg);">↓</span>
       <span>${this.ctx.units.format("wind", spdKn)} · ${Math.round(from)}° ${compass(from)}<br>
         <span style="font-weight:500;opacity:.65;font-size:9.5px;">${sub}</span></span>`,
      ""));
    this._probeMarker.show();
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
    if (this._probeMarker) {
      if (on && this._probePos) this._updateProbe();
      else this._probeMarker.hide();
    }
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
    if (!c || !this._grid || this._zooming) return;
    // Full clear every frame — the streamlines are redrawn from each particle's
    // GEOGRAPHIC path, so they stay locked to the map (no smear/artifacts when the
    // follow camera pans) and nothing stale is ever left behind.
    c.clearRect(0, 0, this._cw, this._ch);
    c.lineCap = "round";
    c.lineJoin = "round";
    c.lineWidth = LINE_WIDTH;
    c.shadowColor = "rgba(0,0,0,0.5)"; // soft halo for contrast on light charts
    c.shadowBlur = 1.5;

    const map = this.ctx.map;
    // Screen axes only align with east/north when the chart is north-up. In
    // course-/head-up follow (or free rotation) the map bearing rotates the world
    // under the canvas, so rotate the wind vector into the screen frame — without
    // this every streamline is skewed by the map bearing (the classic "direction
    // doesn't match the forecast" bug on a rotated chart).
    const brg = (map.getBearing() * Math.PI) / 180;
    const cb = Math.cos(brg), sb = Math.sin(brg);
    for (const p of this._particles) {
      const wind = this._sample(p.lng, p.lat);
      if (!wind || p.age > MAX_AGE) {
        Object.assign(p, this._spawn());
        continue;
      }
      // Advance the head in screen space (zoom-independent speed; v northward → up
      // at bearing 0, rotated with the chart otherwise). The advance is floored at
      // MIN_ADV px so light air still draws a visible streak, with the speed term on
      // top; true calm (<1 kt) barely creeps.
      const spd = Math.hypot(wind[0], wind[1]);
      const adv = spd * STEP + (spd > 0.5 ? MIN_ADV : 0);
      const k = adv / (spd || 1); // scales the wind vector to `adv` px
      const head = map.project([p.lng, p.lat]);
      const b = map.unproject([
        head.x + (wind[0] * cb - wind[1] * sb) * k,
        head.y - (wind[1] * cb + wind[0] * sb) * k,
      ]);
      // Trail length follows the local wind speed on a sqrt curve (a while, not an
      // if: a particle drifting into calmer air sheds its excess tail over frames).
      const maxTrail = Math.round(TRAIL_MIN + (TRAIL_MAX - TRAIL_MIN) * Math.sqrt(Math.min(spd / TRAIL_REF, 1)));
      p.trail.push([p.lng, p.lat]);
      while (p.trail.length > maxTrail) p.trail.shift();
      p.lng = b.lng;
      p.lat = b.lat;
      p.age++;

      // Draw the trail as a fading streak: faint at BOTH ends (a soft comet), scaled
      // by a birth/death life fade so streamlines appear and vanish gently.
      const n = p.trail.length;
      if (n < 2) continue;
      const life = Math.sin((Math.PI * p.age) / MAX_AGE);
      c.strokeStyle = rampColor(spd);
      let prev = map.project(p.trail[0]);
      for (let i = 1; i < n; i++) {
        const pt = map.project(p.trail[i]);
        c.globalAlpha = Math.sin((Math.PI * i) / (n - 1)) * life; // 0 at both ends of the streak
        c.beginPath();
        c.moveTo(prev.x, prev.y);
        c.lineTo(pt.x, pt.y);
        c.stroke();
        prev = pt;
      }
    }
    c.globalAlpha = 1;
    c.shadowBlur = 0;
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
    clearInterval(this._autoTimer);
    clearTimeout(this._hiTimer);
    clearTimeout(this._rfTimer);
    const map = this.ctx.map;
    if (this._onResize) map.off("resize", this._onResize);
    if (this._onZoom) map.off("zoomstart", this._onZoom);
    if (this._onZoomEnd) map.off("zoomend", this._onZoomEnd);
    if (this._onRotate) map.off("rotate", this._onRotate);
    if (this._onClick) map.off("click", this._onClick); // legacy-shell fallback only
    if (this._probeMarker) this._probeMarker.remove();
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
// a small aligned header then, per forecast step, an hour and zero-copy Float32
// planes. v2 carries u/v; v3 always ships five planes (all-NaN = absent); v4 adds a
// per-step mask (bit 0 u, 1 v, 2 gust, 3 temp, 4 cloud) and ships only present planes.
function parseWindBin(buf) {
  const dv = new DataView(buf);
  if (dv.getUint8(0) !== 0x57 || dv.getUint8(1) !== 0x47 || dv.getUint8(2) !== 0x52 || dv.getUint8(3) !== 0x44) {
    return null; // not "WGRD"
  }
  const ver = dv.getUint32(4, true);
  if (ver < 2 || ver > 4) return null;
  const nx = dv.getUint32(8, true), ny = dv.getUint32(12, true), nSteps = dv.getUint32(16, true);
  const lo1 = dv.getFloat32(20, true), la1 = dv.getFloat32(24, true);
  const dx = dv.getFloat32(28, true), dy = dv.getFloat32(32, true);
  const refUnix = dv.getFloat64(36, true); // cycle reference time (s since epoch)
  const refMs = refUnix > 0 ? refUnix * 1000 : NaN;
  const np = nx * ny;
  let o = 44;
  const plane = () => {
    const a = new Float32Array(buf, o, np);
    o += np * 4;
    return Number.isNaN(a[0]) && Number.isNaN(a[np - 1]) ? null : a; // all-NaN = absent
  };
  const steps = [];
  for (let s = 0; s < nSteps; s++) {
    const hour = dv.getInt32(o, true);
    o += 4;
    let mask = ver >= 4 ? dv.getUint32(o, true) : ver >= 3 ? 0b11111 : 0b11;
    if (ver >= 4) o += 4;
    const rd = (bit) => (mask & bit ? plane() : null);
    const u = rd(1), v = rd(2), gust = rd(4), temp = rd(8), cloud = rd(16);
    steps.push({ hour, u, v, gust, temp, cloud });
  }
  const lo2 = lo1 + (nx - 1) * dx, la2 = la1 - (ny - 1) * dy;
  return { header: { nx, ny, lo1, la1, lo2, la2, dx, dy }, refMs, steps };
}

// cloudIcon / cloudWord bucket a total-cloud-cover % into the marine-familiar
// METAR-ish scale (clear / few / scattered / broken / overcast).
function cloudIcon(pct) {
  return pct < 10 ? "☀️" : pct < 25 ? "🌤" : pct < 50 ? "⛅" : pct < 85 ? "🌥" : "☁️";
}
function cloudWord(pct) {
  return pct < 10 ? "clear" : pct < 25 ? "few" : pct < 50 ? "scattered" : pct < 85 ? "broken" : "overcast";
}

// compass maps a bearing (deg) to an 8-point label.
function compass(deg) {
  return ["N", "NE", "E", "SE", "S", "SW", "W", "NW"][Math.round(deg / 45) % 8];
}

const WD = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];
const MO = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];

// Times read in the ship's LOCAL clock first (what the crew plans by); the UTC
// valid time stays available as the compact secondary reference in the fold-out.

// fmtShortLocal → "Thu 14:00" (fits the slim scrubber row).
function fmtShortLocal(d) {
  const hh = String(d.getHours()).padStart(2, "0");
  const mm = String(d.getMinutes()).padStart(2, "0");
  return `${WD[d.getDay()]} ${hh}:${mm}`;
}

// fmtLocalFull → "Thu 16 Jul · 14:00" (the fold-out's primary valid time).
function fmtLocalFull(d) {
  const hh = String(d.getHours()).padStart(2, "0");
  const mm = String(d.getMinutes()).padStart(2, "0");
  return `${WD[d.getDay()]} ${d.getDate()} ${MO[d.getMonth()]} · ${hh}:${mm}`;
}

// fmtZ → "18:00Z" (the marine-standard UTC reference).
function fmtZ(d) {
  const hh = String(d.getUTCHours()).padStart(2, "0");
  const mm = String(d.getUTCMinutes()).padStart(2, "0");
  return `${hh}:${mm}Z`;
}

// lerpHex blends two #rrggbb colours.
function lerpHex(a, b, t) {
  const pa = parseInt(a.slice(1), 16), pb = parseInt(b.slice(1), 16);
  const r = Math.round(((pa >> 16) & 255) * (1 - t) + ((pb >> 16) & 255) * t);
  const g = Math.round(((pa >> 8) & 255) * (1 - t) + ((pb >> 8) & 255) * t);
  const bl = Math.round((pa & 255) * (1 - t) + (pb & 255) * t);
  return `rgb(${r},${g},${bl})`;
}
