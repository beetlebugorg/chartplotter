// AuxStore — loads the companion "<stem>-aux.zip" the baker ships next to the
// hosted .pmtiles. It holds the external resources an ENC feature points at by
// *filename* rather than carrying inline: TXTDSC/NTXTDS textual descriptions and
// PICREP pictures. The baked tiles carry only the filename (in the s57 blob), so
// the pick report resolves it here, by the (upper-cased) referenced name.
//
// The zip is fetched whole once (it's small — the texts plus PNG-transcoded
// pictures) and parsed with the same dependency-free reader the .zip import uses.
// No server: it's a static file beside the charts, so it updates with them.

import { readCentralDirectory, extractEntry } from "./zip-import.mjs";

export class AuxStore {
  constructor() {
    this._index = null;      // referencedName(UPPER) → { stored, type, from }
    this._entries = null;    // stored entry name → central-directory entry
    this._blob = null;
    this._cache = new Map(); // stored → resolved { type, text|url }
  }

  // Fetch + parse the aux zip. Best-effort: any failure leaves the store empty
  // and the pick report degrades to showing the raw filename. Returns true on load.
  async load(url) {
    try {
      const r = await fetch(url);
      if (!r.ok) throw new Error("HTTP " + r.status);
      const blob = await r.blob();
      const byName = new Map((await readCentralDirectory(blob)).map((e) => [e.name, e]));
      const idx = byName.get("index.json");
      if (!idx) throw new Error("aux zip has no index.json");
      const meta = JSON.parse(new TextDecoder("utf-8").decode(await extractEntry(blob, idx)));
      this._index = meta.files || {};
      this._entries = byName;
      this._blob = blob;
      return true;
    } catch (e) {
      console.warn("[aux] load failed:", e.message);
      return false;
    }
  }

  // Is `ref` (a TXTDSC/PICREP filename) present in this set?
  has(ref) { return !!(this._index && this._index[String(ref).toUpperCase()]); }

  // Resolve a referenced filename to { type:"text", text } or { type:"image", url }.
  // Text is decoded ISO-8859-1 (the ENC text charset — degree signs etc.); images
  // become object URLs for an <img>. Cached, so repeat picks are instant.
  async resolve(ref) {
    if (!this._index) return null;
    const meta = this._index[String(ref).toUpperCase()];
    if (!meta) return null;
    if (this._cache.has(meta.stored)) return this._cache.get(meta.stored);
    const entry = this._entries.get(meta.stored);
    if (!entry) return null;
    const bytes = await extractEntry(this._blob, entry);
    let out;
    if ((meta.type || "").startsWith("image/")) {
      out = { type: "image", url: URL.createObjectURL(new Blob([bytes], { type: meta.type })) };
    } else {
      out = { type: "text", text: new TextDecoder("iso-8859-1").decode(bytes) };
    }
    this._cache.set(meta.stored, out);
    return out;
  }
}
