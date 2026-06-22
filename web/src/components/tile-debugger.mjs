// tile-debugger.mjs — a debug-only MapLibre IControl for diagnosing vector tiles
// that are DELIVERED but DON'T RENDER. It cross-references, per visible tile of a
// chosen source, the full lifecycle in one glance:
//
//   requested → delivered bytes → parsed buckets/features → tile state → rendered
//
// and flags the mismatches that are otherwise invisible: a tile MapLibre marks
// `loaded` but parsed into zero buckets while the cp:// protocol logged real bytes
// delivered (the stuck-empty-tile bug that cost us a long debugging session).
//
//   import { TileDebugger } from "./tile-debugger.mjs";
//   map.addControl(new TileDebugger({ source: "chart" }), "top-right");
//
// Options:
//   source  vector source id to inspect (default "chart")
//   layers  optional style-layer id subset for the rendered-check; defaults to
//           every style layer whose source is `source`.
//
// It reads PRIVATE MapLibre source-cache internals (`getVisibleCoordinates()` +
// `getTile()` → `tile.buckets` / `tile.latestRawTileData` / `tile.state`). The
// dict that holds those caches moved between majors (`style.sourceCaches` in v4 →
// `style.tileManagers` in v5), so we DUCK-TYPE it (see `_sourceCaches`) rather
// than hardcode the property — robust across v4/v5, still tied to the vendored
// web/vendor/maplibre-gl.js. (The per-tile delivered-bytes correlation was a
// hook on the retired in-browser cp:// baker; server tiles are inspected via the
// "inspect this tile" button → /api/tile instead.)

// Tile box colour + badge by classification. The red EMPTY box is the bug
// signature: state=loaded but no buckets / no raw bytes.
const CLS = {
  ok:      { stroke: "#2e7d32", fill: "rgba(46,125,50,0.10)", badge: "OK" },     // loaded, ≥1 bucket → rendering
  empty:   { stroke: "#e53935", fill: "rgba(229,57,53,0.22)", badge: "EMPTY" },  // loaded but buckets:[] / no raw bytes
  loading: { stroke: "#f9a825", fill: "rgba(249,168,37,0.10)", badge: "LOAD" },  // in flight
  errored: { stroke: "#9e9e9e", fill: "rgba(158,158,158,0.14)", badge: "ERR" },  // errored / aborted
  other:   { stroke: "#90a4ae", fill: "rgba(144,164,174,0.05)", badge: "?" },
};

const tile2lon = (x, z) => (x / Math.pow(2, z)) * 360 - 180;
const tile2lat = (y, z) => {
  const n = Math.PI - (2 * Math.PI * y) / Math.pow(2, z);
  return (180 / Math.PI) * Math.atan(0.5 * (Math.exp(n) - Math.exp(-n)));
};

function esc(s) {
  return String(s).replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]));
}
function fmtBytes(n) {
  if (n == null) return "—";
  if (n === 0) return "0";
  const u = ["B", "KB", "MB"]; let i = 0; let v = n;
  while (v >= 1024 && i < u.length - 1) { v /= 1024; i++; }
  return `${v.toFixed(v < 10 && i ? 1 : 0)} ${u[i]}`;
}
function pointInPoly(pt, poly) {
  let inside = false;
  for (let i = 0, j = poly.length - 1; i < poly.length; j = i++) {
    const [xi, yi] = poly[i], [xj, yj] = poly[j];
    if ((yi > pt[1]) !== (yj > pt[1]) && pt[0] < ((xj - xi) * (pt[1] - yi)) / (yj - yi) + xi) inside = !inside;
  }
  return inside;
}
function polyArea(poly) {
  let a = 0;
  for (let i = 0, j = poly.length - 1; i < poly.length; j = i++) a += poly[j][0] * poly[i][1] - poly[i][0] * poly[j][1];
  return Math.abs(a / 2);
}

export class TileDebugger {
  constructor(opts = {}) {
    this.source = opts.source || "chart";
    this._layersOpt = opts.layers || null;
    // (z,x,y) → a hittable URL that returns this tile's raw MVT bytes (for
    // inspecting in an external tool). Supplied by the host; null hides the button.
    this._inspectURL = typeof opts.inspectURL === "function" ? opts.inspectURL : null;
    this._delivered = new Map();   // "z/x/y" -> { bytes, ver, error, t }
    this._log = new Map();         // "z/x/y" -> [{ t, type, state, err }]
    this._infos = new Map();       // "z/x/y" -> latest tile info (rebuilt each draw)
    this._boxes = [];              // [{ key, poly, info }] last drawn, for click hit-testing
    this._selected = null;         // "z/x/y" pinned for the detail panel
    this._overlayOn = true;
    this._raf = 0;
  }

  getDefaultPosition() { return "top-right"; }

  onAdd(map) {
    this._map = map;

    // Lifecycle log: dataloading / data / error, filtered to our source.
    this._onData = (e) => this._record(e, false);
    this._onError = (e) => this._record(e, true);
    map.on("dataloading", this._onData);
    map.on("data", this._onData);
    map.on("error", this._onError);

    // Overlay canvas, over the GL canvas (pans/zooms with the map; redrawn each frame).
    const host = map.getCanvasContainer();
    this._canvas = document.createElement("canvas");
    Object.assign(this._canvas.style, { position: "absolute", top: "0", left: "0", pointerEvents: "none", zIndex: "2" });
    host.appendChild(this._canvas);
    // The map (and this control) live in <chart-plotter>'s shadow root, so the
    // panel's stylesheet must go in THAT root, not document.head.
    this._root = host.getRootNode();

    // Click a tile box → pin its detail (read-only; coexists with the app's tools).
    this._onClick = (e) => this._pick(e);
    map.on("click", this._onClick);

    this._onRender = () => this._scheduleDraw();
    map.on("render", this._onRender);

    // Panel (the IControl element MapLibre mounts).
    this._panel = document.createElement("div");
    this._panel.className = "maplibregl-ctrl tile-debugger";
    this._panel.style.cssText = "background:#11161c;color:#e6edf3;font:11px/1.4 ui-monospace,Menlo,Consolas,monospace;"
      + "max-width:300px;max-height:70vh;overflow:auto;border-radius:6px;box-shadow:0 2px 12px rgba(0,0,0,.4);";
    this._injectCSS();
    this._refreshPanel();

    // Throttle the (HTML-rebuilding) panel; the canvas redraws on every frame.
    this._panelTimer = setInterval(() => this._refreshPanel(), 400);
    this._scheduleDraw();
    return this._panel;
  }

  onRemove() {
    const map = this._map;
    clearInterval(this._panelTimer);
    if (this._raf) cancelAnimationFrame(this._raf);
    if (this._unsub) this._unsub();
    if (map) {
      map.off("dataloading", this._onData);
      map.off("data", this._onData);
      map.off("error", this._onError);
      map.off("click", this._onClick);
      map.off("render", this._onRender);
    }
    if (this._canvas && this._canvas.parentNode) this._canvas.parentNode.removeChild(this._canvas);
    if (this._styleEl && this._styleEl.parentNode) this._styleEl.parentNode.removeChild(this._styleEl);
    // MapLibre's removeControl calls onRemove but leaves the element we returned
    // from onAdd in the DOM — it's the control's job to detach it.
    if (this._panel && this._panel.parentNode) this._panel.parentNode.removeChild(this._panel);
    this._canvas = null;
    this._panel = null;
    this._map = null;
  }

  // -- data model ----------------------------------------------------------

  _record(e, isError) {
    if (!e) return;
    if (e.sourceId && e.sourceId !== this.source) return;
    const tile = e.tile;
    if (!tile || !tile.tileID || !tile.tileID.canonical) return;
    const c = tile.tileID.canonical;
    const id = `${c.z}/${c.x}/${c.y}`;
    const arr = this._log.get(id) || [];
    arr.push({ t: e.timeStamp || performance.now(), type: e.type || (isError ? "error" : "data"), state: tile.state, err: isError && e.error ? e.error.message : null });
    if (arr.length > 40) arr.shift();
    this._log.set(id, arr);
  }

  // Find every SourceCache backing our source. MapLibre 4 exposed them as
  // `map.style.sourceCaches[id]`; v5 renamed/minified that property AND splits a
  // source into separate paint + symbol caches. So instead of hardcoding a
  // property name, duck-type: scan style's own props for a dict (or Map) keyed by
  // our source id whose value is cache-shaped (`getVisibleCoordinates`/`_tiles` —
  // those identifiers survive minification). Cached and re-validated against the
  // live source so a style reload can't leave us pointing at a stale cache.
  _sourceCaches() {
    const map = this._map, style = map && map.style;
    if (!style) return [];
    const out = [];
    const consider = (c) => { if (c && (c._tiles || typeof c.getVisibleCoordinates === "function") && !out.includes(c)) out.push(c); };
    const fromDict = (d) => {
      if (!d || typeof d !== "object") return;
      if (d instanceof Map) { consider(d.get(this.source)); return; }
      if (Object.prototype.hasOwnProperty.call(d, this.source)) consider(d[this.source]);
    };
    fromDict(style.sourceCaches); // v4 fast path
    for (const k of Object.keys(style)) { const v = style[k]; if (v && typeof v === "object") fromDict(v); }
    return out;
  }

  // Visible tiles for the source, merged across its paint/symbol caches and keyed
  // by canonical z/x/y so each tile box is drawn once. When the same tile lives in
  // two caches we keep the richer one (the paint cache has the buckets we report).
  _visibleTiles() {
    const byId = new Map();
    for (const sc of this._sourceCaches()) {
      let coords = [];
      try { coords = sc.getVisibleCoordinates() || []; } catch (e) { /* fall through */ }
      let tiles = [];
      if (coords.length) {
        for (const coord of coords) {
          const t = (sc.getTile && sc.getTile(coord)) || (sc._tiles && sc._tiles[coord.key]);
          if (t && t.tileID) tiles.push(t);
        }
      } else if (sc._tiles) {
        for (const k of Object.keys(sc._tiles)) tiles.push(sc._tiles[k]);
      }
      for (const t of tiles) {
        const c = t.tileID.canonical;
        const id = `${c.z}/${c.x}/${c.y}`;
        const prev = byId.get(id);
        const nb = t.buckets ? Object.keys(t.buckets).length : 0;
        if (!prev || nb > (prev.buckets ? Object.keys(prev.buckets).length : 0)) byId.set(id, t);
      }
    }
    return [...byId.values()];
  }

  _tileInfo(tile) {
    const id0 = tile.tileID;
    const c = id0.canonical;
    const id = `${c.z}/${c.x}/${c.y}`;
    const buckets = tile.buckets || {};
    const bucketIds = Object.keys(buckets);
    const raw = tile.latestRawTileData || (tile.latestFeatureIndex && tile.latestFeatureIndex.rawTileData) || null;
    const rawBytes = raw ? (raw.byteLength != null ? raw.byteLength : raw.length) : null;
    const state = tile.state;
    const hasData = typeof tile.hasData === "function" ? tile.hasData() : undefined;
    let cls = "other";
    if (state === "loading") cls = "loading";
    else if (state === "errored" || state === "expired") cls = "errored";
    else if (state === "loaded" || state === "reloading") cls = (bucketIds.length >= 1 && rawBytes) ? "ok" : "empty";
    return {
      id, canonical: c, overscaledZ: id0.overscaledZ, wrap: id0.wrap, key: id0.key,
      state, hasData, bucketIds, bucketCount: bucketIds.length, rawBytes, cls,
    };
  }

  // Best-effort per-source-layer feature counts from the tile's feature index.
  _sourceLayerCounts(tile) {
    const fi = tile.latestFeatureIndex;
    if (!fi || !fi.loadVTLayers) return null;
    try {
      const layers = fi.loadVTLayers();
      if (!layers) return null;
      const out = {};
      for (const name of Object.keys(layers)) out[name] = layers[name] && layers[name].length;
      return out;
    } catch (e) { return null; }
  }

  _chartLayerIds() {
    if (this._layersOpt) return this._layersOpt;
    const map = this._map;
    try { return (map.getStyle().layers || []).filter((l) => l.source === this.source).map((l) => l.id); }
    catch (e) { return []; }
  }

  // -- drawing -------------------------------------------------------------

  _scheduleDraw() {
    if (this._raf) return;
    this._raf = requestAnimationFrame(() => { this._raf = 0; this._draw(); });
  }

  _project(c, wrap) {
    const map = this._map;
    const off = (wrap || 0) * 360;
    const corners = [[c.x, c.y], [c.x + 1, c.y], [c.x + 1, c.y + 1], [c.x, c.y + 1]];
    return corners.map(([tx, ty]) => {
      const p = map.project([tile2lon(tx, c.z) + off, tile2lat(ty, c.z)]);
      return [p.x, p.y];
    });
  }

  _draw() {
    const map = this._map, cv = this._canvas;
    if (!map || !cv) return;
    const host = map.getCanvasContainer();
    const w = host.clientWidth, h = host.clientHeight, dpr = window.devicePixelRatio || 1;
    if (cv.width !== Math.round(w * dpr) || cv.height !== Math.round(h * dpr)) {
      cv.width = Math.round(w * dpr); cv.height = Math.round(h * dpr);
      cv.style.width = w + "px"; cv.style.height = h + "px";
    }
    const ctx = cv.getContext("2d");
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    ctx.clearRect(0, 0, w, h);
    this._boxes = [];
    this._infos = new Map();
    if (!this._overlayOn) return;

    ctx.font = "600 11px ui-monospace,Menlo,Consolas,monospace";
    ctx.textBaseline = "top";
    for (const tile of this._visibleTiles()) {
      const info = this._tileInfo(tile);
      this._infos.set(info.id, info);
      const poly = this._project(info.canonical, info.wrap);
      // Skip boxes fully off-screen (cheap viewport cull).
      const xs = poly.map((p) => p[0]), ys = poly.map((p) => p[1]);
      if (Math.max(...xs) < 0 || Math.min(...xs) > w || Math.max(...ys) < 0 || Math.min(...ys) > h) continue;
      this._boxes.push({ key: info.id, poly, info });

      const sk = CLS[info.cls] || CLS.other;
      const d = this._delivered.get(info.id);
      const mismatch = d && d.bytes > 0 && info.bucketCount === 0 && (info.state === "loaded" || info.state === "reloading");
      const selected = this._selected === info.id;

      ctx.beginPath();
      ctx.moveTo(poly[0][0], poly[0][1]);
      for (let i = 1; i < poly.length; i++) ctx.lineTo(poly[i][0], poly[i][1]);
      ctx.closePath();
      ctx.fillStyle = sk.fill;
      ctx.fill();
      ctx.lineWidth = selected ? 3 : mismatch ? 2.5 : 1.5;
      ctx.setLineDash(mismatch ? [6, 4] : []);
      ctx.strokeStyle = selected ? "#42a5f5" : sk.stroke;
      ctx.stroke();
      ctx.setLineDash([]);

      // Label near the top-left corner: z/x/y · badge · delivered bytes.
      const tx = Math.min(...xs) + 4, ty = Math.min(...ys) + 4;
      const label = `${info.id}`;
      const sub = `${sk.badge} b:${info.bucketCount}${d ? " d:" + fmtBytes(d.bytes) : ""}`;
      ctx.fillStyle = "rgba(0,0,0,0.55)";
      const wlab = Math.max(ctx.measureText(label).width, ctx.measureText(sub).width) + 8;
      ctx.fillRect(tx - 2, ty - 2, wlab, 28);
      ctx.fillStyle = "#fff";
      ctx.fillText(label, tx, ty);
      ctx.fillStyle = sk.stroke === "#9e9e9e" ? "#ddd" : sk.stroke;
      ctx.fillText(sub, tx, ty + 13);
    }
  }

  // -- interaction + panel -------------------------------------------------

  _pick(e) {
    if (!this._overlayOn || !this._boxes.length) return;
    const pt = [e.point.x, e.point.y];
    let best = null, bestArea = Infinity;
    for (const b of this._boxes) {
      if (pointInPoly(pt, b.poly)) { const a = polyArea(b.poly); if (a < bestArea) { bestArea = a; best = b; } }
    }
    if (best) { this._selected = best.key; this._refreshPanel(); }
  }

  _mismatches() {
    const out = [];
    for (const [id, info] of this._infos) {
      if (info.cls !== "empty") continue; // loaded but buckets:[] / no raw bytes
      const d = this._delivered.get(id);
      out.push({ id, delivered: d ? d.bytes : null, ver: d ? d.ver : null, buckets: info.bucketCount, state: info.state, rawBytes: info.rawBytes });
    }
    // Stable order, the delivered-but-empty bug first (it has real bytes).
    out.sort((a, b) => (b.delivered || 0) - (a.delivered || 0) || a.id.localeCompare(b.id));
    return out;
  }

  _refreshPanel() {
    if (!this._panel) return;
    const mm = this._mismatches();
    const total = this._infos.size;
    const counts = { ok: 0, empty: 0, loading: 0, errored: 0, other: 0 };
    for (const info of this._infos.values()) counts[info.cls] = (counts[info.cls] || 0) + 1;

    let html = `<div class="td-head">`
      + `<label class="td-toggle"><input type="checkbox"${this._overlayOn ? " checked" : ""}> overlay</label>`
      + `<span class="td-src">${esc(this.source)}</span></div>`
      + `<div class="td-counts">`
      + `<span class="td-c ok">${counts.ok} ok</span>`
      + `<span class="td-c empty">${counts.empty} empty</span>`
      + `<span class="td-c load">${counts.loading} load</span>`
      + `<span class="td-c err">${counts.errored} err</span>`
      + `<span class="td-c tot">${total} tiles</span></div>`;

    // The single most valuable readout: delivered-but-not-parsed.
    html += `<div class="td-sec"><div class="td-h">Mismatches (loaded · buckets:0)</div>`;
    if (!mm.length) html += `<div class="td-empty">none — all visible tiles parsed ✓</div>`;
    else html += `<ul class="td-list">` + mm.map((m) =>
      `<li class="td-mm${this._selected === m.id ? " sel" : ""}" data-id="${m.id}">`
      + `<b>${m.id}</b> delivered=${m.delivered != null ? fmtBytes(m.delivered) : "?"}, buckets=${m.buckets}`
      + `, rawBytes=${m.rawBytes == null ? "null" : m.rawBytes}</li>`).join("") + `</ul>`;
    html += `</div>`;

    // Detail of the clicked tile.
    html += this._detailHTML();

    this._panel.innerHTML = html;
    const cb = this._panel.querySelector(".td-toggle input");
    if (cb) cb.onchange = () => { this._overlayOn = cb.checked; this._scheduleDraw(); this._refreshPanel(); };
    this._panel.querySelectorAll(".td-mm").forEach((li) => (li.onclick = () => { this._selected = li.dataset.id; this._refreshPanel(); this._flyTo(li.dataset.id); }));
    this._panel.querySelectorAll(".td-btn[data-url]").forEach((ib) => (ib.onclick = () => this._copyURL(ib)));
  }

  // Copy the tile-inspect URL to the clipboard (also logged, so it's reachable on
  // an insecure LAN origin where the clipboard API is blocked).
  _copyURL(btn) {
    const url = btn.getAttribute("data-url");
    console.log("[tile-debugger] tile URL:", url);
    const done = () => { const t = btn.textContent; btn.textContent = "Copied ✓"; setTimeout(() => { btn.textContent = t; }, 1200); };
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(url).then(done, () => { this._fallbackCopy(url); done(); });
    } else { this._fallbackCopy(url); done(); }
  }
  _fallbackCopy(text) {
    try {
      const ta = document.createElement("textarea");
      ta.value = text; ta.style.position = "fixed"; ta.style.opacity = "0";
      document.body.appendChild(ta); ta.select(); document.execCommand("copy"); ta.remove();
    } catch (e) { /* console log above is the fallback-of-last-resort */ }
  }

  _detailHTML() {
    const id = this._selected;
    if (!id) return `<div class="td-sec"><div class="td-h">Tile detail</div><div class="td-empty">click a tile box</div></div>`;
    const info = this._infos.get(id);
    const d = this._delivered.get(id);
    const log = this._log.get(id) || [];

    let rows = `<div class="td-h">Tile ${esc(id)}</div>`;
    if (!info) {
      rows += `<div class="td-empty">not currently in the source cache</div>`;
    } else {
      const tile = this._visibleTiles().find((t) => `${t.tileID.canonical.z}/${t.tileID.canonical.x}/${t.tileID.canonical.y}` === id);
      const slc = tile ? this._sourceLayerCounts(tile) : null;
      const rendered = this._renderedCount(info.canonical);
      const kv = (k, v) => `<div class="td-kv"><span>${k}</span><b>${v}</b></div>`;
      rows += kv("state", esc(info.state) + (info.cls === "empty" ? " ⚠" : ""));
      rows += kv("overscaledZ / wrap", `${info.overscaledZ} / ${info.wrap}`);
      rows += kv("key", esc(String(info.key)));
      rows += kv("hasData()", String(info.hasData));
      rows += kv("rawBytes", info.rawBytes == null ? "null" : `${info.rawBytes}`);
      rows += kv("delivered", d ? `${fmtBytes(d.bytes)} (ver ${d.ver})${d.error ? " · " + esc(d.error) : ""}` : "—");
      rows += kv("buckets", info.bucketCount ? esc(info.bucketIds.join(", ")) : "0");
      if (rendered != null) rows += kv("rendered@center", String(rendered));
      if (slc) {
        const parts = Object.keys(slc).map((n) => `${esc(n)}:${slc[n]}`).join(", ");
        rows += kv("source-layers", parts || "(none)");
      }
    }
    // "Inspect this tile": hittable URLs that re-bake this z/x/y server-side — as a
    // single-tile .pmtiles archive (loads in pmtiles.io / any PMTiles viewer) or as
    // the raw .mvt (for vt2geojson etc.). Open downloads the .pmtiles.
    if (this._inspectURL) {
      const [tz, tx, ty] = id.split("/").map(Number);
      const base = this._inspectURL(tz, tx, ty);
      if (base) {
        const sep = base.includes("?") ? "&" : "?";
        const pm = `${base}${sep}format=pmtiles`;
        rows += `<div class="td-actions">`
          + `<button class="td-btn" type="button" data-url="${esc(pm)}">Copy .pmtiles URL</button>`
          + `<button class="td-btn" type="button" data-url="${esc(base)}">Copy .mvt URL</button>`
          + `<a class="td-btn td-link" href="${esc(pm)}" download="${tz}-${tx}-${ty}.pmtiles">Download</a></div>`;
      }
    }
    if (log.length) {
      const t0 = log[0].t;
      rows += `<div class="td-h2">Lifecycle</div><ul class="td-log">`
        + log.map((e) => `<li><span>+${((e.t - t0) / 1000).toFixed(2)}s</span> ${esc(e.type)} <i>${esc(e.state || "")}</i>${e.err ? ` <em>${esc(e.err)}</em>` : ""}</li>`).join("")
        + `</ul>`;
    }
    return `<div class="td-sec">${rows}</div>`;
  }

  // Optional rendered check: features painted at the tile centre for our layers.
  // Source-nonempty but rendered-empty points at a style/paint issue, not delivery.
  _renderedCount(c) {
    const map = this._map;
    const layers = this._chartLayerIds();
    if (!layers.length) return null;
    const off = 0;
    const cx = c.x + 0.5, cy = c.y + 0.5;
    try {
      const p = map.project([tile2lon(cx, c.z) + off, tile2lat(cy, c.z)]);
      return map.queryRenderedFeatures([p.x, p.y], { layers }).length;
    } catch (e) { return null; }
  }

  _flyTo(id) {
    const info = this._infos.get(id);
    if (!info || !this._map) return;
    const c = info.canonical;
    // Centre on the tile without changing zoom — just to bring it into view.
    this._map.panTo([tile2lon(c.x + 0.5, c.z), tile2lat(c.y + 0.5, c.z)]);
  }

  _injectCSS() {
    const root = this._root || document;
    const container = root.getElementById ? root : document; // ShadowRoot or Document both have getElementById
    if (container.getElementById("td-style")) return;
    const mount = root.head || root; // document → <head>; ShadowRoot → itself
    const css = `
      .tile-debugger { padding:0; }
      .tile-debugger .td-head { display:flex; align-items:center; justify-content:space-between; padding:6px 8px; background:#0b1014; border-radius:6px 6px 0 0; position:sticky; top:0; }
      .tile-debugger .td-toggle { display:flex; align-items:center; gap:4px; cursor:pointer; }
      .tile-debugger .td-src { opacity:.6; }
      .tile-debugger .td-counts { display:flex; flex-wrap:wrap; gap:6px; padding:5px 8px; border-bottom:1px solid #222b33; }
      .tile-debugger .td-c.ok { color:#66bb6a; } .tile-debugger .td-c.empty { color:#ef5350; }
      .tile-debugger .td-c.load { color:#ffca28; } .tile-debugger .td-c.err { color:#bbb; } .tile-debugger .td-c.tot { opacity:.6; }
      .tile-debugger .td-sec { padding:6px 8px; border-bottom:1px solid #1d262d; }
      .tile-debugger .td-h, .tile-debugger .td-h2 { font-weight:700; opacity:.75; margin:0 0 4px; text-transform:uppercase; font-size:10px; letter-spacing:.04em; }
      .tile-debugger .td-h2 { margin-top:8px; }
      .tile-debugger .td-empty { opacity:.45; font-style:italic; }
      .tile-debugger .td-list { list-style:none; margin:0; padding:0; }
      .tile-debugger .td-mm { padding:3px 4px; border-radius:3px; cursor:pointer; color:#ffcdd2; }
      .tile-debugger .td-mm:hover, .tile-debugger .td-mm.sel { background:#3a1f22; }
      .tile-debugger .td-mm b { color:#fff; }
      .tile-debugger .td-kv { display:flex; justify-content:space-between; gap:8px; padding:1px 0; }
      .tile-debugger .td-kv span { opacity:.6; } .tile-debugger .td-kv b { font-weight:600; text-align:right; word-break:break-all; }
      .tile-debugger .td-log { list-style:none; margin:0; padding:0; }
      .tile-debugger .td-log li { padding:1px 0; white-space:nowrap; overflow:hidden; text-overflow:ellipsis; }
      .tile-debugger .td-log span { opacity:.5; } .tile-debugger .td-log i { color:#90caf9; font-style:normal; } .tile-debugger .td-log em { color:#ef9a9a; font-style:normal; }
      .tile-debugger .td-actions { display:flex; gap:6px; margin-top:6px; }
      .tile-debugger .td-btn { flex:1; text-align:center; padding:4px 6px; border:1px solid #2b3742; border-radius:4px; background:#1b2530; color:#cfe3f5; cursor:pointer; font:inherit; text-decoration:none; }
      .tile-debugger .td-btn:hover { background:#243140; }
      .tile-debugger .td-link { flex:0 0 auto; }`;
    const el = document.createElement("style");
    el.id = "td-style";
    el.textContent = css;
    mount.appendChild(el);
    this._styleEl = el;
  }
}
