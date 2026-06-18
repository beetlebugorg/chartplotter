// Streaming, dependency-free ZIP reader for importing ENC cells in the browser.
//
// NOAA distributes ENC cells as .zip (single-cell zips, or the giant
// ALL_ENCs.zip), and the catalog only has zip URLs — but charts.noaa.gov sends
// no CORS headers, so we can't fetch them cross-origin. Instead the user picks a
// .zip from their disk and we read it locally: no server, no backend.
//
// The container can be >1GB (ALL_ENCs.zip), so we never load it whole. We read
// the central directory with range reads (Blob.slice), then slice + inflate one
// entry at a time. Inflation uses the platform DecompressionStream — no library.
//
// ZIP layout we rely on (PKWARE APPNOTE):
//   [local header + data]* [central directory]* [end of central directory]
// Entry sizes/method/CRC come from the CENTRAL DIRECTORY, never the local
// header: NOAA sets the data-descriptor flag (general-purpose bit 3), which
// zeroes those fields in the local header and writes them after the data.

const SIG_EOCD = 0x06054b50;   // end of central directory
const SIG_EOCD64 = 0x06064b50; // zip64 end of central directory record
const SIG_LOC64 = 0x07064b50;  // zip64 EOCD locator
const SIG_CEN = 0x02014b50;    // central directory file header
const Z64 = 0xffffffff;        // sentinel: real value is in the zip64 extra field
const U16 = 0xffff;

async function view(blob, start, end) {
  return new DataView(await blob.slice(start, end).arrayBuffer());
}

// Locate and parse the End Of Central Directory, returning the central
// directory's { offset, size, count }. Handles ZIP64 (mandatory for >4GB or
// >65535 entries, both of which ALL_ENCs.zip hits).
async function findCentralDir(blob) {
  const size = blob.size;
  const tailLen = Math.min(size, 22 + 65535); // EOCD is 22 bytes + ≤64KB comment
  const tail = await view(blob, size - tailLen, size);

  // Scan backwards for the EOCD signature.
  let p = -1;
  for (let i = tail.byteLength - 22; i >= 0; i--) {
    if (tail.getUint32(i, true) === SIG_EOCD) { p = i; break; }
  }
  if (p < 0) throw new Error("not a zip (no end-of-central-directory record)");

  let count = tail.getUint16(p + 10, true);
  let cdSize = tail.getUint32(p + 12, true);
  let cdOffset = tail.getUint32(p + 16, true);

  // ZIP64: any sentinel means the real values live in the zip64 records. The
  // zip64 EOCD locator sits 20 bytes before the EOCD.
  if (cdOffset === Z64 || cdSize === Z64 || count === U16) {
    const locAbs = size - tailLen + p - 20;
    if (locAbs >= 0) {
      const loc = await view(blob, locAbs, locAbs + 20);
      if (loc.getUint32(0, true) === SIG_LOC64) {
        const z64Abs = Number(loc.getBigUint64(8, true));
        const z = await view(blob, z64Abs, z64Abs + 56);
        if (z.getUint32(0, true) === SIG_EOCD64) {
          count = Number(z.getBigUint64(32, true));
          cdSize = Number(z.getBigUint64(40, true));
          cdOffset = Number(z.getBigUint64(48, true));
        }
      }
    }
  }
  return { offset: cdOffset, size: cdSize, count };
}

// Read a zip64 extra field, replacing any sentinel sizes/offset in order:
// uncompressed, compressed, local-header offset (only the ones that were 0xFFFF…).
function readZip64Extra(dv, start, len, need) {
  let i = start;
  const end = start + len;
  while (i + 4 <= end) {
    const id = dv.getUint16(i, true);
    const sz = dv.getUint16(i + 2, true);
    i += 4;
    if (id === 0x0001) {
      let o = i;
      const out = {};
      if (need.uncomp) { out.uncompSize = Number(dv.getBigUint64(o, true)); o += 8; }
      if (need.comp) { out.compSize = Number(dv.getBigUint64(o, true)); o += 8; }
      if (need.offset) { out.localOffset = Number(dv.getBigUint64(o, true)); o += 8; }
      return out;
    }
    i += sz;
  }
  return {};
}

// Read the central directory and return all entries:
//   { name, method, compSize, uncompSize, crc, localOffset }
export async function readCentralDirectory(blob) {
  const { offset, size, count } = await findCentralDir(blob);
  const cd = new DataView(await blob.slice(offset, offset + size).arrayBuffer());
  const dec = new TextDecoder("utf-8"); // NOAA cell names are ASCII
  const entries = [];
  let p = 0;
  for (let i = 0; i < count && p + 46 <= cd.byteLength; i++) {
    if (cd.getUint32(p, true) !== SIG_CEN) break;
    const method = cd.getUint16(p + 10, true);
    const crc = cd.getUint32(p + 16, true);
    let compSize = cd.getUint32(p + 20, true);
    let uncompSize = cd.getUint32(p + 24, true);
    const nameLen = cd.getUint16(p + 28, true);
    const extraLen = cd.getUint16(p + 30, true);
    const commentLen = cd.getUint16(p + 32, true);
    let localOffset = cd.getUint32(p + 42, true);
    const name = dec.decode(new Uint8Array(cd.buffer, p + 46, nameLen));

    if (compSize === Z64 || uncompSize === Z64 || localOffset === Z64) {
      const z = readZip64Extra(cd, p + 46 + nameLen, extraLen, {
        uncomp: uncompSize === Z64, comp: compSize === Z64, offset: localOffset === Z64,
      });
      if (z.uncompSize !== undefined) uncompSize = z.uncompSize;
      if (z.compSize !== undefined) compSize = z.compSize;
      if (z.localOffset !== undefined) localOffset = z.localOffset;
    }

    entries.push({ name, method, compSize, uncompSize, crc, localOffset });
    p += 46 + nameLen + extraLen + commentLen;
  }
  return entries;
}

// Inflate one entry to a Uint8Array. The local header's own filename/extra
// lengths give where the data starts; sizes come from the central-dir `entry`.
export async function extractEntry(blob, entry) {
  const lh = await view(blob, entry.localOffset, entry.localOffset + 30);
  const nameLen = lh.getUint16(26, true);
  const extraLen = lh.getUint16(28, true);
  const dataStart = entry.localOffset + 30 + nameLen + extraLen;
  const comp = blob.slice(dataStart, dataStart + entry.compSize);

  if (entry.method === 0) return new Uint8Array(await comp.arrayBuffer()); // STORED
  if (entry.method !== 8) throw new Error("unsupported zip method " + entry.method);

  // DEFLATE — ZIP stores *raw* deflate (no zlib/gzip wrapper).
  const stream = comp.stream().pipeThrough(new DecompressionStream("deflate-raw"));
  return new Uint8Array(await new Response(stream).arrayBuffer());
}

// Group entries into cells by the NOAA layout `…/<CELL>/<CELL>.NNN`:
//   { name, base: entry(.000), updates: [entry(.001)…], updateCount }
// Cells without a base .000 are skipped (the engine ingests a base only; .NNN
// update application is a follow-up).
export function cellEntries(entries) {
  const re = /(?:^|\/)([A-Z]{2}\d[A-Z0-9]{4,})\.(\d{3})$/;
  const byName = new Map();
  for (const e of entries) {
    const m = e.name.match(re);
    if (!m) continue;
    const [, cell, ext] = m;
    let rec = byName.get(cell);
    if (!rec) { rec = { name: cell, base: null, updates: [] }; byName.set(cell, rec); }
    if (ext === "000") rec.base = e;
    else rec.updates.push(e);
  }
  const out = [];
  for (const rec of byName.values()) {
    if (!rec.base) continue;
    rec.updates.sort((a, b) => a.name.localeCompare(b.name));
    rec.updateCount = rec.updates.length;
    out.push(rec);
  }
  out.sort((a, b) => a.name.localeCompare(b.name));
  return out;
}

// Adapt a remote, Range-readable URL to the minimal Blob-like interface the
// reader above uses — `size` and `slice(start,end)` returning an object with
// `arrayBuffer()`/`stream()`. This lets readCentralDirectory/extractEntry run
// over a remote ALL_ENCs.zip (fetched in pieces via HTTP Range) without ever
// downloading the whole multi-GB archive. `rangeFetch(start,end)` resolves to a
// Response for bytes [start, end). `size` is the total byte length.
export function remoteZipBlob(size, rangeFetch) {
  const slice = (start, end) => ({
    async arrayBuffer() { return (await rangeFetch(start, end)).arrayBuffer(); },
    stream() {
      return new ReadableStream({
        async pull(ctrl) {
          ctrl.enqueue(new Uint8Array(await (await rangeFetch(start, end)).arrayBuffer()));
          ctrl.close();
        },
      });
    },
  });
  return { size, slice };
}
