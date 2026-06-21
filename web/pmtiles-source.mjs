// Minimal PMTiles v3 reader (dependency-free).
//
// Reads the archives this project bakes (see src/pmtiles.zig): a single root
// directory (no leaf dirs), no internal/tile compression, MVT tiles, clustered
// in tile-id order. One MERGED tile per (z,x,y) holding every layer. We keep the
// 127-byte header + the decoded root directory in memory and slice each tile's
// bytes out of the backing Blob on demand, so a large pack never has to be held
// whole in RAM.
//
// Spec: https://github.com/protomaps/PMTiles/blob/main/spec/v3/spec.md

// (z,x,y) → PMTiles tile id: all tiles in lower zooms, plus the Hilbert-curve
// index within the zoom. Mirrors zxyToTileId in src/pmtiles.zig. Plain Number
// math is exact for our zoom range (ids stay well under 2^53).
export function zxyToTileId(z, x, y) {
  let acc = 0, base = 1;
  for (let i = 0; i < z; i++) { acc += base; base *= 4; }
  const side = 2 ** z;
  let xx = x, yy = y, d = 0;
  for (let s = side >> 1; s > 0; s = Math.floor(s / 2)) {
    const rx = (xx & s) > 0 ? 1 : 0;
    const ry = (yy & s) > 0 ? 1 : 0;
    d += s * s * ((3 * rx) ^ ry);
    if (ry === 0) {
      if (rx === 1) { xx = side - 1 - xx; yy = side - 1 - yy; }
      const t = xx; xx = yy; yy = t;
    }
  }
  return acc + d;
}

// Inverse of zxyToTileId (PMTiles spec d2xy). Used mainly for tests/tools.
export function tileIdToZxy(id) {
  let acc = 0, z = 0;
  for (;;) {
    const numTiles = 4 ** z;
    if (acc + numTiles > id) break;
    acc += numTiles;
    z++;
  }
  let t = id - acc;
  const side = 2 ** z;
  let x = 0, y = 0;
  for (let s = 1; s < side; s *= 2) {
    const rx = 1 & Math.floor(t / 2);
    const ry = 1 & (t ^ rx);
    if (ry === 0) {
      if (rx === 1) { x = s - 1 - x; y = s - 1 - y; }
      const tmp = x; x = y; y = tmp;
    }
    x += s * rx;
    y += s * ry;
    t = Math.floor(t / 4);
  }
  return { z, x, y };
}

// LEB128 unsigned varint from a Uint8Array at byte offset `p`. Returns the value
// and the next offset. Values here (counts/offsets/lengths) stay under 2^53.
function readVarint(buf, p) {
  let value = 0, shift = 0, b;
  do {
    b = buf[p++];
    value += (b & 0x7f) * 2 ** shift;
    shift += 7;
  } while (b & 0x80);
  return [value, p];
}

// Decode a serialized directory (see serializeDir in src/pmtiles.zig) into an
// array of { tileId, runLength, offset, length }, sorted by tileId.
function decodeDirectory(buf) {
  let p = 0, n;
  [n, p] = readVarint(buf, 0);
  const e = Array.from({ length: n }, () => ({}));
  let prev = 0;
  for (let i = 0; i < n; i++) { let d; [d, p] = readVarint(buf, p); prev += d; e[i].tileId = prev; }
  for (let i = 0; i < n; i++) { [e[i].runLength, p] = readVarint(buf, p); }
  for (let i = 0; i < n; i++) { [e[i].length, p] = readVarint(buf, p); }
  for (let i = 0; i < n; i++) {
    let v; [v, p] = readVarint(buf, p);
    // 0 → contiguous with the previous tile's bytes; else stored as offset+1.
    e[i].offset = v === 0 ? e[i - 1].offset + e[i - 1].length : v - 1;
  }
  return e;
}

// Binary-search a directory for the entry with the largest tileId <= `tileId`
// (the "floor"). For a tile run, the caller checks it's within the run; for a
// leaf pointer (runLength 0) it's the leaf whose range covers `tileId`.
function floorEntry(entries, tileId) {
  let lo = 0, hi = entries.length - 1, best = null;
  while (lo <= hi) {
    const mid = (lo + hi) >> 1;
    if (entries[mid].tileId <= tileId) { best = entries[mid]; lo = mid + 1; }
    else hi = mid - 1;
  }
  return best;
}

const HEADER_BYTES = 127;

export class PMTilesArchive {
  // `src` is either a Blob/File (local — `.slice()` reads ranges from disk) or a
  // URL string (hosted — read via HTTP Range). Either way we only ever fetch the
  // header, the directory, and tiles on demand — NEVER the whole archive. This
  // matters for multi-GB packs.
  constructor(src) {
    this._url = typeof src === "string" ? src : null;
    this._blob = this._url ? null : src;
    this._leafCache = new Map(); // leaf offset → decoded entries (FIFO-capped)
    this._misses = new Set();    // "z/x/y" the archive has no tile for (cheap to keep)
  }

  // Read `len` bytes at `start`. Blob → slice; URL → a single HTTP Range request.
  async _read(start, len) {
    if (this._blob) return new Uint8Array(await this._blob.slice(start, start + len).arrayBuffer());
    const r = await fetch(this._url, { headers: { Range: `bytes=${start}-${start + len - 1}` } });
    // 206 = partial (range honoured). A 200 means the server ignored Range and
    // sent the WHOLE file — refuse it, that's the thing we're avoiding.
    if (r.status === 200) throw new Error("server ignored Range (sent whole file); host must support byte ranges");
    if (r.status !== 206) throw new Error("HTTP " + r.status);
    return new Uint8Array(await r.arrayBuffer());
  }

  // Read + validate the header and decode the root directory. Throws on a
  // malformed archive or an unsupported (compressed / leaf-dir) layout.
  async init() {
    const headBytes = await this._read(0, HEADER_BYTES);
    const head = new DataView(headBytes.buffer, headBytes.byteOffset, headBytes.byteLength);
    for (let i = 0; i < 7; i++) {
      if (head.getUint8(i) !== "PMTiles".charCodeAt(i)) throw new Error("not a PMTiles archive");
    }
    if (head.getUint8(7) !== 3) throw new Error("unsupported PMTiles version " + head.getUint8(7));
    const u64 = (off) => Number(head.getBigUint64(off, true));
    const rootOff = u64(8), rootLen = u64(16);
    this._leafOff = u64(40);
    this._dataOff = u64(56);
    // Compression: 1=none, 2=gzip (3=brotli/4=zstd unsupported). Our own baked
    // chart archives are uncompressed; hosted OSM-vector (Protomaps) archives are
    // gzip — decompress directories + tiles on read.
    const ic = head.getUint8(97), tc = head.getUint8(98);
    const okComp = (v) => v === 1 || v === 2;
    if (!okComp(ic) || !okComp(tc)) throw new Error("unsupported PMTiles compression (only none/gzip)");
    this._internalGz = ic === 2;
    this._tileGz = tc === 2;
    this.minZoom = head.getUint8(100);
    this.maxZoom = head.getUint8(101);
    // Data extent straight from the header (the writer stores it), so we never
    // have to scan the directory — important once it's leaf-sharded.
    const e7 = (off) => head.getInt32(off, true) / 1e7;
    this.bounds = [e7(102), e7(106), e7(110), e7(114)]; // [W,S,E,N]
    const dir = await this._read(rootOff, rootLen);
    this._root = decodeDirectory(this._internalGz ? await gunzip(dir) : dir);
    return this;
  }

  get tileCount() { return this._root ? this._root.length : 0; }

  // Decode (and FIFO-cache) the leaf directory at `offset` (relative to the leaf
  // section), `length` bytes. Keeps repeated lookups in the same leaf cheap.
  async _leaf(offset, length) {
    const hit = this._leafCache.get(offset);
    if (hit) return hit;
    const raw = await this._read(this._leafOff + offset, length);
    const entries = decodeDirectory(this._internalGz ? await gunzip(raw) : raw);
    if (this._leafCache.size >= 64) this._leafCache.delete(this._leafCache.keys().next().value);
    this._leafCache.set(offset, entries);
    return entries;
  }

  // Tile bytes (Uint8Array) for (z,x,y), or null when the archive has no such
  // tile (reads as blank — MapLibre overzooms from a present parent). Resolves
  // root → leaf (fetched on demand) → tile, so the whole directory is never held.
  async getTile(z, x, y) {
    const key = z + "/" + x + "/" + y;
    if (this._misses.has(key)) return null; // known-absent — don't re-probe (re-fetch a leaf)
    const id = zxyToTileId(z, x, y);
    let e = floorEntry(this._root, id);
    if (e && e.runLength === 0) e = floorEntry(await this._leaf(e.offset, e.length), id); // descend into leaf
    if (!e || id >= e.tileId + e.runLength || e.length === 0) {
      if (this._misses.size >= 8192) this._misses.clear();
      this._misses.add(key);
      return null;
    }
    const raw = await this._read(this._dataOff + e.offset, e.length);
    return this._tileGz ? gunzip(raw) : raw;
  }
}

// Gunzip a byte range using the platform DecompressionStream (no deps). Used for
// gzip-compressed PMTiles directories + tiles (e.g. Protomaps OSM basemaps).
async function gunzip(bytes) {
  const stream = new Response(new Blob([bytes])).body.pipeThrough(new DecompressionStream("gzip"));
  return new Uint8Array(await new Response(stream).arrayBuffer());
}

// Web-Mercator tile (z,x,y) → its [W,S,E,N] lon/lat bounds. Used to route a tile
// request to the archive(s) whose data covers it.
function tile2lat(y, z) {
  const n = Math.PI - (2 * Math.PI * y) / 2 ** z;
  return (180 / Math.PI) * Math.atan(0.5 * (Math.exp(n) - Math.exp(-n)));
}
function tileLonLatBounds(z, x, y) {
  const n = 2 ** z;
  return [(x / n) * 360 - 180, tile2lat(y + 1, z), ((x + 1) / n) * 360 - 180, tile2lat(y, z)];
}

// Does archive bounds `b` ([W,S,E,N]) cover any of tile (z,x,y)? Latitude is
// always a reliable discriminator; longitude is skipped for archives whose bbox
// spans (≈) the whole globe — the antimeridian-crossing Pacific districts
// (Alaska/Hawaii) bake a meaningless full-width lon bbox.
function boundsCoverTile(b, z, x, y) {
  if (!b) return true;
  const tb = tileLonLatBounds(z, x, y);
  if (tb[3] < b[1] || tb[1] > b[3]) return false; // no latitude overlap
  if (b[0] <= -179.5 && b[2] >= 179.5) return true; // global-longitude bbox → trust latitude
  return !(tb[2] < b[0] || tb[0] > b[2]);
}

// A set of PMTiles archives presented as one tile source. Districts bake to
// separate, geographically DISJOINT archives, so any (z,x,y) has data in at most
// one — `getTile` routes by bounds (no merging) and returns the first hit. Zoom
// range + bounds are the union, so MapLibre requests every baked zoom across the
// whole loaded coverage. Opening an archive reads only its header + directory,
// so all districts can be open at once with tiles streaming by viewport.
export class MultiArchive {
  constructor() {
    this.archives = [];
    this.minZoom = 0;
    this.maxZoom = 16;
    this.bounds = null;
  }

  // Add (open) an archive from a Blob/File or a URL string. Returns the opened
  // PMTilesArchive (read its `.bounds` to frame the view).
  async add(src) {
    return this.addOpened(await new PMTilesArchive(src).init());
  }

  // Register an ALREADY-opened archive. Lets a bandless pack that fans across
  // every band source share one opened handle instead of re-fetching its header
  // + directory once per band (see chartplotter _openPrebaked).
  addOpened(a) {
    this.archives.push(a);
    this._recompute();
    return a;
  }

  _recompute() {
    let mn = 24, mx = 0, b = null;
    for (const a of this.archives) {
      mn = Math.min(mn, a.minZoom);
      mx = Math.max(mx, a.maxZoom);
      if (a.bounds) {
        b = b
          ? [Math.min(b[0], a.bounds[0]), Math.min(b[1], a.bounds[1]), Math.max(b[2], a.bounds[2]), Math.max(b[3], a.bounds[3])]
          : a.bounds.slice();
      }
    }
    this.minZoom = this.archives.length ? mn : 0;
    this.maxZoom = this.archives.length ? mx : 16;
    this.bounds = b;
  }

  get tileCount() { return this.archives.reduce((s, a) => s + a.tileCount, 0); }

  async getTile(z, x, y) {
    for (const a of this.archives) {
      if (!boundsCoverTile(a.bounds, z, x, y)) continue;
      const t = await a.getTile(z, x, y);
      if (t) return t;
    }
    return null;
  }
}

// Register a MapLibre protocol that serves merged tiles from the CURRENT archive
// returned by getArchive() — a PMTilesArchive or a MultiArchive, same getTile()
// interface (so the loaded pack(s) can change without re-registering).
// Style sources use `${scheme}://{z}/{x}/{y}`; a missing tile/archive → blank.
export function registerPmtilesProtocol(maplibregl, scheme, getArchive) {
  maplibregl.addProtocol(scheme, async (params) => {
    const archive = getArchive();
    if (!archive) return { data: new ArrayBuffer(0) };
    // Capture the trailing z/x/y; any leading segment (e.g. a cache-bust {v})
    // is ignored, so the source can bump its tile URL to force a re-request.
    const m = params.url.match(/(\d+)\/(\d+)\/(\d+)$/);
    if (!m) return { data: new ArrayBuffer(0) };
    const [, z, x, y] = m;
    try {
      const bytes = await archive.getTile(+z, +x, +y);
      return { data: bytes ? bytes.buffer : new ArrayBuffer(0) };
    } catch (e) {
      console.warn("[pmtiles] tile", z, x, y, "failed:", e.message);
      return { data: new ArrayBuffer(0) };
    }
  });
}
