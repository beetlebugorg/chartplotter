// Persistent ENC cell store, with three backends picked automatically:
//
//   1. OPFS         — origin-private filesystem; only in a SECURE CONTEXT
//                     (HTTPS or localhost). The most efficient for large blobs.
//   2. IndexedDB    — works over plain http:// too (e.g. a LAN IP like a boat's
//                     192.168.x.x device), where OPFS is unavailable. This is
//                     what keeps charts after a refresh on an insecure origin.
//   3. in-memory    — last resort if neither exists; session-only (lost on
//                     reload). The plotter still renders.
//
// Cells are stored once and read back, so after the first import the plotter is
// self-contained and works fully offline with no server. The engine ingests the
// Uint8Array bytes this store hands it.
//
// `download` fetches same-origin today (a static host serving raw .000 files);
// the in-browser .zip import (see zip-import.mjs) is the no-server path. Pulling
// NOAA .zip cells directly is a follow-up: charts.noaa.gov has no CORS headers.

const DIR = "cells";
const DB_NAME = "chartplotter";
const DB_STORE = "cells";

const HAS_OPFS =
  typeof navigator !== "undefined" &&
  navigator.storage &&
  typeof navigator.storage.getDirectory === "function";
const HAS_IDB = typeof indexedDB !== "undefined";

// -- OPFS backend (secure context) ------------------------------------------
async function cellsDir() {
  const root = await navigator.storage.getDirectory();
  return root.getDirectoryHandle(DIR, { create: true });
}
const opfsBackend = {
  async has(name) {
    try { const d = await cellsDir(); await d.getFileHandle(name + ".000"); return true; }
    catch { return false; }
  },
  async get(name) {
    const d = await cellsDir();
    const fh = await d.getFileHandle(name + ".000");
    return new Uint8Array(await (await fh.getFile()).arrayBuffer());
  },
  async put(name, bytes) {
    const d = await cellsDir();
    const fh = await d.getFileHandle(name + ".000", { create: true });
    const w = await fh.createWritable();
    await w.write(bytes);
    await w.close();
  },
  async list() {
    const d = await cellsDir();
    const out = [];
    for await (const [n] of d.entries()) if (n.endsWith(".000")) out.push(n.slice(0, -4));
    return out.sort();
  },
  async remove(name) {
    const d = await cellsDir();
    await d.removeEntry(name + ".000").catch(() => {});
  },
};

// -- IndexedDB backend (works on plain http) --------------------------------
let _dbPromise = null;
function openDB() {
  if (_dbPromise) return _dbPromise;
  _dbPromise = new Promise((resolve, reject) => {
    const req = indexedDB.open(DB_NAME, 1);
    req.onupgradeneeded = () => req.result.createObjectStore(DB_STORE);
    req.onsuccess = () => resolve(req.result);
    req.onerror = () => reject(req.error);
  });
  return _dbPromise;
}
function reqDone(req) {
  return new Promise((res, rej) => { req.onsuccess = () => res(req.result); req.onerror = () => rej(req.error); });
}
function txDone(tx) {
  return new Promise((res, rej) => { tx.oncomplete = () => res(); tx.onerror = () => rej(tx.error); tx.onabort = () => rej(tx.error); });
}
const idbBackend = {
  async has(name) {
    const db = await openDB();
    const k = await reqDone(db.transaction(DB_STORE).objectStore(DB_STORE).getKey(name));
    return k !== undefined;
  },
  async get(name) {
    const db = await openDB();
    const v = await reqDone(db.transaction(DB_STORE).objectStore(DB_STORE).get(name));
    if (!v) throw new Error("cell not stored: " + name);
    return v instanceof Uint8Array ? v : new Uint8Array(v);
  },
  async put(name, bytes) {
    const db = await openDB();
    const tx = db.transaction(DB_STORE, "readwrite");
    tx.objectStore(DB_STORE).put(bytes, name);
    await txDone(tx);
  },
  async list() {
    const db = await openDB();
    const keys = await reqDone(db.transaction(DB_STORE).objectStore(DB_STORE).getAllKeys());
    return keys.map(String).sort();
  },
  async remove(name) {
    const db = await openDB();
    const tx = db.transaction(DB_STORE, "readwrite");
    tx.objectStore(DB_STORE).delete(name);
    await txDone(tx);
  },
};

// -- in-memory backend (last resort, session-only) --------------------------
function memBackend() {
  const m = new Map();
  return {
    async has(name) { return m.has(name); },
    async get(name) { const b = m.get(name); if (!b) throw new Error("cell not stored: " + name); return b; },
    async put(name, bytes) { m.set(name, bytes); },
    async list() { return [...m.keys()].sort(); },
    async remove(name) { m.delete(name); },
  };
}

export class ChartStore {
  constructor() {
    if (HAS_OPFS) { this.backend = opfsBackend; this.kind = "opfs"; }
    else if (HAS_IDB) { this.backend = idbBackend; this.kind = "indexeddb"; }
    else { this.backend = memBackend(); this.kind = "memory"; }
    // True when reloads keep the data (OPFS or IndexedDB); false for in-memory.
    this.persistent = this.kind !== "memory";
  }

  has(name) { return this.backend.has(name); }
  getBytes(name) { return this.backend.get(name); }
  put(name, bytes) { return this.backend.put(name, bytes); }
  list() { return this.backend.list(); }
  remove(name) { return this.backend.remove(name); }

  // Download a cell's bytes from `url` into local storage and return them.
  // Accepts a raw .000 today; NOAA ZIP unwrap is handled by the in-browser
  // importer (zip-import.mjs), not here.
  async download(name, url) {
    const res = await fetch(url);
    if (!res.ok) throw new Error(`download ${name}: HTTP ${res.status}`);
    const bytes = new Uint8Array(await res.arrayBuffer());
    await this.put(name, bytes);
    return bytes;
  }

  // Return a cell's bytes, downloading it via `urlFor(name)` if not yet stored.
  async ensure(name, urlFor) {
    if (await this.has(name)) return this.getBytes(name);
    return this.download(name, urlFor(name));
  }
}
