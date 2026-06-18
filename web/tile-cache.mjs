// Two-layer cache for real-time-baked MVT tiles (the wasm baker generates tiles
// on demand; this keeps them so a tile is baked at most once until evicted).
//
//   L1 — memory:     Map keyed by "z/x/y", LRU, capped at 512 MB.
//   L2 — persistent: IndexedDB, LRU, capped at 1 GB.
//
// NOTE on storage: the spec asked for "localStorage", but localStorage is a
// synchronous string store limited to ~5-10 MB per origin — it cannot hold 1 GB
// of binary tiles. IndexedDB is the correct persistent layer for this (async,
// binary, multi-GB), and it's what the rest of the app already uses.
//
// Lookup order: L1 → L2 (promote to L1) → bake() (fill both). Each layer evicts
// least-recently-used entries when it exceeds its cap.

const MEM_CAP_DEFAULT = 512 * 1024 * 1024; // 512 MB
const DISK_CAP_DEFAULT = 1024 * 1024 * 1024; // 1 GB
const DB_NAME = "cp-tilecache";
const STORE = "tiles";
const DB_VERSION = 1;

export class TileCache {
  // namespace scopes keys to a particular loaded chart set, so changing the
  // installed charts doesn't serve stale tiles. memCap/diskCap are byte caps.
  constructor({ namespace = "", memCap = MEM_CAP_DEFAULT, diskCap = DISK_CAP_DEFAULT } = {}) {
    this.ns = namespace;
    this.memCap = memCap;
    this.diskCap = diskCap;

    // L1: insertion-ordered Map IS the LRU order — on a hit we delete+re-set to
    // move the key to the most-recent (end) position; eviction pops from the front.
    this._mem = new Map(); // key -> Uint8Array
    this._memBytes = 0;

    // L2 bookkeeping. `_atime` is a monotonic counter (ties-free recency stamp);
    // `_diskBytes` is the running total, summed once on open.
    this._atime = 1;
    this._diskBytes = 0;
    this._diskCount = 0;
    this._db = null;
    this._ready = this._open();

    this.stats = { l1Hit: 0, l2Hit: 0, miss: 0, evictedMem: 0, evictedDisk: 0 };
  }

  _key(z, x, y) { return `${this.ns}|${z}/${x}/${y}`; }

  // -- IndexedDB plumbing -------------------------------------------------
  _open() {
    return new Promise((resolve) => {
      let req;
      try { req = indexedDB.open(DB_NAME, DB_VERSION); }
      catch { resolve(null); return; } // no IndexedDB → memory-only
      req.onupgradeneeded = () => {
        const db = req.result;
        if (!db.objectStoreNames.contains(STORE)) {
          const os = db.createObjectStore(STORE, { keyPath: "k" });
          os.createIndex("atime", "atime"); // LRU eviction order
        }
      };
      req.onsuccess = async () => {
        this._db = req.result;
        try { await this._sumDisk(); } catch {}
        resolve(this._db);
      };
      req.onerror = () => resolve(null);
    });
  }

  _tx(mode) { return this._db.transaction(STORE, mode).objectStore(STORE); }

  // Sum the persisted size + seed the atime counter above the max on disk.
  _sumDisk() {
    return new Promise((resolve, reject) => {
      let total = 0, maxA = 0, count = 0;
      const cur = this._tx("readonly").openCursor();
      cur.onsuccess = () => {
        const c = cur.result;
        if (!c) { this._diskBytes = total; this._diskCount = count; this._atime = maxA + 1; resolve(); return; }
        total += c.value.s || 0; count++;
        if (c.value.atime > maxA) maxA = c.value.atime;
        c.continue();
      };
      cur.onerror = () => reject(cur.error);
    });
  }

  // -- public API ---------------------------------------------------------
  // Return the tile bytes (Uint8Array) for z/x/y, baking via bake(z,x,y) on a
  // full miss. bake may return null/undefined for an empty tile — that's cached
  // too (as a zero-length marker) so we don't re-bake known-empty tiles.
  async get(z, x, y, bake) {
    const key = this._key(z, x, y);

    // L1
    const m = this._mem.get(key);
    if (m !== undefined) {
      this._mem.delete(key); this._mem.set(key, m); // bump LRU
      this.stats.l1Hit++;
      return m.length ? m : null;
    }

    // L2
    await this._ready;
    if (this._db) {
      const rec = await this._dbGet(key);
      if (rec) {
        this.stats.l2Hit++;
        this._dbTouch(key); // refresh recency (fire-and-forget)
        this._memPut(key, rec.b);
        return rec.b.length ? rec.b : null;
      }
    }

    // miss → bake
    this.stats.miss++;
    let bytes = await bake(z, x, y);
    if (bytes == null) bytes = new Uint8Array(0);
    else if (!(bytes instanceof Uint8Array)) bytes = new Uint8Array(bytes);
    this._memPut(key, bytes);
    if (this._db) this._dbPut(key, bytes);
    return bytes.length ? bytes : null;
  }

  // -- L1 (memory) --------------------------------------------------------
  _memPut(key, bytes) {
    const prev = this._mem.get(key);
    if (prev) this._memBytes -= prev.length;
    this._mem.set(key, bytes);
    this._memBytes += bytes.length;
    this._memEvict();
  }
  _memEvict() {
    while (this._memBytes > this.memCap && this._mem.size > 1) {
      const oldest = this._mem.keys().next().value; // front = LRU
      const v = this._mem.get(oldest);
      this._mem.delete(oldest);
      this._memBytes -= v.length;
      this.stats.evictedMem++;
    }
  }

  // -- L2 (IndexedDB) -----------------------------------------------------
  _dbGet(key) {
    return new Promise((resolve) => {
      const r = this._tx("readonly").get(key);
      r.onsuccess = () => resolve(r.result || null);
      r.onerror = () => resolve(null);
    });
  }
  _dbTouch(key) {
    try {
      const os = this._tx("readwrite");
      const g = os.get(key);
      g.onsuccess = () => { const v = g.result; if (v) { v.atime = this._atime++; os.put(v); } };
    } catch {}
  }
  _dbPut(key, bytes) {
    try {
      const size = bytes.length;
      // store a copy of the buffer (the wasm scratch buffer is reused)
      const buf = bytes.slice();
      const rec = { k: key, b: buf, s: size, atime: this._atime++ };
      const os = this._tx("readwrite");
      const g = os.get(key);
      g.onsuccess = () => {
        if (g.result) this._diskBytes -= g.result.s || 0;
        else this._diskCount++;
        os.put(rec);
        this._diskBytes += size;
        if (this._diskBytes > this.diskCap) this._dbEvict();
      };
    } catch {}
  }
  // Evict least-recently-used persisted tiles until back under the disk cap.
  _dbEvict() {
    try {
      const os = this._tx("readwrite");
      const cur = os.index("atime").openCursor(); // ascending atime = LRU first
      cur.onsuccess = () => {
        const c = cur.result;
        if (!c || this._diskBytes <= this.diskCap) return;
        this._diskBytes -= c.value.s || 0;
        this._diskCount = Math.max(0, this._diskCount - 1);
        this.stats.evictedDisk++;
        c.delete();
        c.continue();
      };
    } catch {}
  }

  // Drop everything (e.g. when the installed chart set changes).
  async clear() {
    this._mem.clear(); this._memBytes = 0;
    await this._ready;
    if (this._db) {
      await new Promise((res) => { const r = this._tx("readwrite").clear(); r.onsuccess = r.onerror = () => res(); });
      this._diskBytes = 0; this._diskCount = 0;
    }
  }

  usage() {
    return {
      memBytes: this._memBytes, memCap: this.memCap, memTiles: this._mem.size,
      diskBytes: this._diskBytes, diskCap: this.diskCap, diskTiles: this._diskCount,
      ...this.stats,
    };
  }
}
