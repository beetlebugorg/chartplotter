// Real-time chart tiles: drive the wasm ENC baker (cmd/chartplotter-wasm) behind
// a MapLibre custom protocol, backed by the two-layer TileCache. Cells are loaded
// once (raw .000 bytes); tiles are then baked on demand as the map requests them
// and cached so each z/x/y bakes at most once.
import { TileCache } from "./tile-cache.mjs";

let _ready = null;

function loadScript(src) {
  return new Promise((resolve, reject) => {
    const s = document.createElement("script");
    s.src = src; s.onload = resolve; s.onerror = () => reject(new Error("load " + src));
    document.head.appendChild(s);
  });
}

// Load the Go wasm runtime + the baker module (idempotent). `assets` is the base
// path the .wasm + wasm_exec.js are served from.
export function initBaker(assets = "./") {
  if (_ready) return _ready;
  _ready = (async () => {
    if (!globalThis.Go) await loadScript(assets + "vendor/wasm_exec.js");
    const go = new globalThis.Go();
    // Plain fetch+instantiate (not instantiateStreaming) so it works even if the
    // server doesn't send Content-Type: application/wasm.
    const buf = await (await fetch(assets + "chartplotter.wasm")).arrayBuffer();
    const { instance } = await WebAssembly.instantiate(buf, go.importObject);
    go.run(instance); // runs main(): sets cpBakeLoad/cpBakeTile, then blocks on select{}
    for (let i = 0; i < 500 && !globalThis.cpBakeReady; i++) await new Promise((r) => setTimeout(r, 10));
    if (!globalThis.cpBakeReady) throw new Error("wasm baker did not start");
  })();
  return _ready;
}

// Parse + index the given cells into the baker. `cellMap` is { name: Uint8Array }.
// Returns { ok, names, ms }.
export async function loadCells(cellMap, assets = "./") {
  await initBaker(assets);
  return globalThis.cpBakeLoad(cellMap);
}

// Register a "cp://{z}/{x}/{y}" vector-tile protocol backed by the cache + baker.
// Returns the TileCache (for usage()/clear()). Use it as a source:
//   { type: "vector", tiles: ["cp://{z}/{x}/{y}"], minzoom, maxzoom }
export function registerTileProtocol(maplibregl, opts = {}) {
  const cache = new TileCache(opts);
  maplibregl.addProtocol("cp", async (params) => {
    const m = params.url.match(/(\d+)\/(\d+)\/(\d+)$/);
    if (!m) return { data: new ArrayBuffer(0) };
    const [, z, x, y] = m;
    try {
      const bytes = await cache.get(+z, +x, +y, (z, x, y) => globalThis.cpBakeTile(z, x, y));
      if (!bytes || !bytes.length) return { data: new ArrayBuffer(0) };
      return { data: bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength) };
    } catch (e) {
      console.warn("[cp] tile", z, x, y, "failed:", e.message);
      return { data: new ArrayBuffer(0) };
    }
  });
  return cache;
}
