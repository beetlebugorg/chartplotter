// Web Worker that hosts the wasm ENC baker off the main thread. Cell parsing
// (cpBakeLoad, ~hundreds of ms) and per-tile baking (cpBakeTile) run here so they
// never block the UI / map render loop. Classic worker (importScripts) because the
// Go/tinygo wasm runtime (wasm_exec.js) is a classic script that sets globals.
//
// Message protocol (each carries an `id` the main thread correlates):
//   { id, op:"init", assets }      → loads the wasm runtime + module
//   { id, op:"load", cells }       → cpBakeLoad(cells) ; cells = { name: Uint8Array }
//   { id, op:"tile", z, x, y }     → cpBakeTile(z,x,y) ; reply transfers the tile buffer
// Reply: { id, ok, result?/tile? } or { id, error }.

let booted = false;

self.onmessage = async (e) => {
  const m = e.data;
  try {
    switch (m.op) {
      case "init": {
        if (!booted) {
          importScripts(m.assets + "vendor/wasm_exec.js"); // sets self.Go
          const go = new self.Go();
          const buf = await (await fetch(m.assets + "chartplotter.wasm")).arrayBuffer();
          const { instance } = await WebAssembly.instantiate(buf, go.importObject);
          go.run(instance); // runs main(): sets cpBakeLoad/cpBakeTile, blocks on select{}
          for (let i = 0; i < 1000 && !self.cpBakeReady; i++) await new Promise((r) => setTimeout(r, 5));
          booted = !!self.cpBakeReady;
        }
        self.postMessage({ id: m.id, ok: booted });
        break;
      }
      case "load": {
        const result = self.cpBakeLoad(m.cells);
        self.postMessage({ id: m.id, ok: true, result });
        break;
      }
      case "tile": {
        const t = self.cpBakeTile(m.z, m.x, m.y); // Uint8Array or null
        if (t && t.length) {
          const buf = t.buffer.slice(t.byteOffset, t.byteOffset + t.byteLength);
          self.postMessage({ id: m.id, ok: true, tile: buf }, [buf]); // transfer
        } else {
          self.postMessage({ id: m.id, ok: true, tile: null });
        }
        break;
      }
      default:
        self.postMessage({ id: m.id, error: "unknown op " + m.op });
    }
  } catch (err) {
    self.postMessage({ id: m.id, error: String(err && err.message || err) });
  }
};
