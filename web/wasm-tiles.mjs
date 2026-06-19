// Real-time chart tiles, baked OFF the main thread. The wasm ENC baker runs in a
// Web Worker (wasm-tiles-worker.js); this module is a thin RPC proxy + the
// MapLibre "cp://" protocol, backed by the two-layer TileCache. Cell parsing and
// per-tile baking happen in the worker, so the UI / map render loop never blocks.
import { TileCache } from "./tile-cache.mjs";

let _worker = null;
let _ready = null;
let _seq = 0;
const _pending = new Map();

function ensureWorker(assets) {
  if (_worker) return;
  _worker = new Worker(assets + "wasm-tiles-worker.js"); // classic worker
  _worker.onmessage = (e) => {
    if (e.data && e.data.diag !== undefined) { console.log("%c[baketile]", "color:#0a8", e.data.diag); return; }
    const { id, error, ...rest } = e.data;
    const p = _pending.get(id);
    if (!p) return;
    _pending.delete(id);
    if (error) p.reject(new Error(error));
    else p.resolve(rest);
  };
  _worker.onerror = (e) => {
    // fail every in-flight call so callers don't hang
    for (const [, p] of _pending) p.reject(new Error("worker: " + (e.message || "error")));
    _pending.clear();
  };
}

function call(op, payload, transfer) {
  const id = ++_seq;
  return new Promise((resolve, reject) => {
    _pending.set(id, { resolve, reject });
    _worker.postMessage({ id, op, ...payload }, transfer || []);
  });
}

// Boot the worker + wasm baker (idempotent). `assets` is the base path the
// worker, .wasm and wasm_exec.js are served from.
export function initBaker(assets = "./") {
  if (_ready) return _ready;
  ensureWorker(assets);
  _ready = call("init", { assets }).then((r) => {
    if (!r.ok) throw new Error("wasm baker did not start");
    console.log("[realtime] wasm baker worker ready");
  }).catch((e) => { console.error("[realtime] baker worker init failed:", e.message); throw e; });
  return _ready;
}

// -- lazy, demand-driven cell loading -----------------------------------------
// Cells are NOT parsed up front. Instead each installed cell is registered with
// its footprint (bbox) + the zoom it starts rendering at (its band min-zoom).
// A cell is parsed into the baker only when a requested tile actually needs it
// (the tile's zoom has reached the cell's band and the tile overlaps the cell).
// So opening to a single harbour parses one cell, not the whole library.
let _registry = [];          // [{name, w, s, e, n, minzoom, getBytes}]
const _loaded = new Set();   // cells already parsed into the baker
const _loading = new Map();  // name -> in-flight load promise (dedupe concurrent tiles)
let _onCell = null;          // (name, status, info) status callback

// Register the installed cells for lazy loading. `list` items are
// { name, bb:[w,s,e,n]|null, minzoom, getBytes:()=>Promise<Uint8Array> }; a null
// bb means "always relevant" (e.g. an imported cell with no catalog footprint).
export function setCellRegistry(list, onCell) {
  _registry = (list || []).map((c) => ({
    name: c.name,
    w: c.bb ? c.bb[0] : -180, s: c.bb ? c.bb[1] : -90, e: c.bb ? c.bb[2] : 180, n: c.bb ? c.bb[3] : 90,
    minzoom: c.minzoom || 0,
    getBytes: c.getBytes,
  }));
  _onCell = onCell || null;
}

// Reset the baker to empty and forget what's been loaded — call when the
// installed set changes. Cells then re-parse lazily as the map needs them.
export async function resetCells(assets = "./") {
  await initBaker(assets);
  await call("reset", {});
  _loaded.clear();
  _loading.clear();
}

function tile2lat(y, n) { const r = Math.PI * (1 - (2 * y) / n); return (180 / Math.PI) * Math.atan(Math.sinh(r)); }
function tileBBox(z, x, y) {
  const n = 2 ** z;
  const a = tile2lat(y, n), b = tile2lat(y + 1, n);
  return [(x / n) * 360 - 180, Math.min(a, b), ((x + 1) / n) * 360 - 180, Math.max(a, b)];
}

// Ensure every registered cell this tile needs (band min-zoom reached + bbox
// overlaps) is parsed into the baker. Each cell loads at most once; concurrent
// tiles needing the same cell share one load.
async function ensureCellsForTile(z, x, y) {
  if (!_registry.length) return;
  const [tw, ts, te, tn] = tileBBox(z, x, y);
  const jobs = [];
  for (const c of _registry) {
    if (z < c.minzoom) continue;
    if (c.e < tw || c.w > te || c.n < ts || c.s > tn) continue; // no overlap
    if (_loaded.has(c.name)) continue;
    let p = _loading.get(c.name);
    if (!p) {
      p = (async () => {
        if (_onCell) _onCell(c.name, "loading");
        try {
          const bytes = await c.getBytes();
          const r = await call("addcell", { name: c.name, cell: bytes });
          if (r.result && r.result.ok) { _loaded.add(c.name); if (_onCell) _onCell(c.name, "ready", r.result); }
          else { console.warn("[realtime] cell", c.name, "failed:", r.result && r.result.error); if (_onCell) _onCell(c.name, "failed", r.result); }
        } catch (e) {
          console.warn("[realtime] cell", c.name, "failed:", e.message); if (_onCell) _onCell(c.name, "failed", { error: e.message });
        } finally { _loading.delete(c.name); }
      })();
      _loading.set(c.name, p);
    }
    jobs.push(p);
  }
  if (jobs.length) await Promise.all(jobs);
}

// Toggle per-tile bake diagnostics in the worker. When on, every cpBakeTile logs
// `tile z/x/y: eligible=.. suppDown=.. suppUp=.. emptyGeom=.. empty=..` (and a
// note for tiles requested before the baker session exists) to the worker console
// — so an empty/blank tile reveals where it lost its primitives.
let _tileDiagOn = false;
export function setTileDiag(on) {
  _tileDiagOn = !!on; // also gates the JS-side delivery log in the cp protocol
  if (!_worker) return Promise.resolve();
  return call("tilediag", { on: !!on });
}

// Delivery observers. Diagnostics surfaces (the tile-debugger plugin) subscribe
// to learn exactly what the cp:// protocol handed MapLibre per tile — the key
// correlation for the "delivered N bytes but the tile kept 0 buckets" bug. Each
// listener gets { z, x, y, bytes, ver, error, t }. Read-only; returns an
// unsubscribe fn. This can subsume the [cp deliver] console log.
const _deliveryListeners = new Set();
export function onTileDelivery(fn) {
  _deliveryListeners.add(fn);
  return () => _deliveryListeners.delete(fn);
}
function _notifyDelivery(z, x, y, bytes, ver, error) {
  if (!_deliveryListeners.size) return;
  const ev = { z, x, y, bytes, ver, error: error || null, t: performance.now() };
  for (const fn of _deliveryListeners) { try { fn(ev); } catch (e) { console.warn("[cp delivery hook]", e); } }
}

// GeoJSON (parsed) of every loaded cell's M_COVR data-coverage polygon, for the
// debug overlay (where cells actually have data vs their bbox). {} when no baker.
export async function coverage() {
  if (!_worker) return { type: "FeatureCollection", features: [] };
  const r = await call("coverage", {});
  try { return JSON.parse(r.geojson || "") || { type: "FeatureCollection", features: [] }; }
  catch { return { type: "FeatureCollection", features: [] }; }
}

// Register the "cp://{z}/{x}/{y}" vector-tile protocol backed by the cache + the
// worker baker. Returns the TileCache (for usage()/clear()). Use it as a source:
//   { type: "vector", tiles: ["cp://{z}/{x}/{y}"], minzoom, maxzoom }
// opts.onActivity(inflight) fires whenever the number of tiles currently baking
// in the worker changes (0 = idle) — used to drive a "generating tiles" status.
export function registerTileProtocol(maplibregl, opts = {}) {
  const cache = new TileCache(opts);
  const onActivity = opts.onActivity;
  let inflight = 0;
  const tick = (d) => { inflight += d; if (onActivity) onActivity(inflight); };
  maplibregl.addProtocol("cp", async (params) => {
    const m = params.url.match(/(\d+)\/(\d+)\/(\d+)$/);
    if (!m) return { data: new ArrayBuffer(0) };
    const [, z, x, y] = m;
    // The version token (`cp://{ver}/{z}/{x}/{y}`) lets a debug surface see which
    // tile version a stuck tile is pinned to across a refresh()/bump.
    const vm = params.url.match(/cp:\/\/(\d+)\//);
    const ver = vm ? +vm[1] : 0;
    try {
      // Parse this tile's cells ONLY on a cache miss — i.e. only when we actually
      // have to bake. If the tile is already cached (memory or IndexedDB) it's
      // served as-is with no chart loading at all, so a refresh over cached tiles
      // does zero parsing. Cells load lazily, on demand, the first time an area
      // is baked.
      const bytes = await cache.get(+z, +x, +y, async (z, x, y) => {
        await ensureCellsForTile(z, x, y); // miss: cells must be parsed before baking
        tick(1); // a real bake in the worker (cache miss)
        try { const r = await call("tile", { z, x, y }); return r.tile ? new Uint8Array(r.tile) : null; }
        finally { tick(-1); }
      });
      const n = bytes ? bytes.length : 0;
      if (_tileDiagOn) console.log("%c[cp deliver]", "color:#08a", `${z}/${x}/${y} → ${n} bytes`);
      _notifyDelivery(+z, +x, +y, n, ver);
      if (!bytes || !bytes.length) return { data: new ArrayBuffer(0) };
      return { data: bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength) };
    } catch (e) {
      console.warn("[cp] tile", z, x, y, "failed:", e.message);
      _notifyDelivery(+z, +x, +y, 0, ver, e.message || "error");
      return { data: new ArrayBuffer(0) };
    }
  });
  return cache;
}
