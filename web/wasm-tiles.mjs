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
  });
  return _ready;
}

// Parse + index the given cells into the baker (in the worker). `cellMap` is
// { name: Uint8Array }. Returns { ok, names, ms }.
export async function loadCells(cellMap, assets = "./") {
  await initBaker(assets);
  const r = await call("load", { cells: cellMap });
  return r.result;
}

// Register the "cp://{z}/{x}/{y}" vector-tile protocol backed by the cache + the
// worker baker. Returns the TileCache (for usage()/clear()). Use it as a source:
//   { type: "vector", tiles: ["cp://{z}/{x}/{y}"], minzoom, maxzoom }
export function registerTileProtocol(maplibregl, opts = {}) {
  const cache = new TileCache(opts);
  maplibregl.addProtocol("cp", async (params) => {
    const m = params.url.match(/(\d+)\/(\d+)\/(\d+)$/);
    if (!m) return { data: new ArrayBuffer(0) };
    const [, z, x, y] = m;
    try {
      const bytes = await cache.get(+z, +x, +y, async (z, x, y) => {
        const r = await call("tile", { z, x, y });
        return r.tile ? new Uint8Array(r.tile) : null;
      });
      if (!bytes || !bytes.length) return { data: new ArrayBuffer(0) };
      return { data: bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength) };
    } catch (e) {
      console.warn("[cp] tile", z, x, y, "failed:", e.message);
      return { data: new ArrayBuffer(0) };
    }
  });
  return cache;
}
