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
    this._index = null;      // referencedName(UPPER) → { stored, type, from } | { type }
    this._entries = null;    // stored entry name → central-directory entry (zip mode)
    this._blob = null;       // the fetched aux zip (zip mode)
    this._apiBase = null;    // assets/API base when resolving per-file via the server
    this._cache = new Map(); // key → resolved { type, text|url }
  }

  // Server mode: index the aux files the server exposes (GET <base>api/aux returns
  // { files: { NAME: mimeType } }) so has()/resolve() work WITHOUT downloading the
  // whole zip — each TXTDSC/PICREP file is fetched on demand from GET api/aux/<name>.
  // The raw aux.zip is never exposed. Best-effort: failure leaves the store empty.
  async loadApi(base) {
    try {
      const r = await fetch(`${base}api/aux`);
      if (!r.ok) throw new Error("HTTP " + r.status);
      const j = await r.json();
      const files = (j && j.files) || {};
      const idx = {};
      for (const [name, type] of Object.entries(files)) idx[String(name).toUpperCase()] = { type };
      this._index = idx;
      this._apiBase = base;
      return true;
    } catch (e) {
      console.warn("[aux] api index load failed:", e.message);
      return false;
    }
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
    const key = String(ref).toUpperCase();
    const meta = this._index[key];
    if (!meta) return null;
    // Server mode: fetch the single file on demand; the response Content-Type tells
    // image vs text (falling back to the indexed type). Object URLs/text are cached.
    if (this._apiBase) {
      if (this._cache.has(key)) return this._cache.get(key);
      try {
        const r = await fetch(`${this._apiBase}api/aux/${encodeURIComponent(ref)}`);
        if (!r.ok) throw new Error("HTTP " + r.status);
        const type = (r.headers.get("content-type") || meta.type || "").split(";")[0].trim();
        let out;
        if (type.startsWith("image/")) {
          out = { type: "image", url: URL.createObjectURL(await r.blob()) };
        } else {
          out = { type: "text", text: new TextDecoder("iso-8859-1").decode(await r.arrayBuffer()) };
        }
        this._cache.set(key, out);
        return out;
      } catch (e) {
        console.warn("[aux] resolve failed:", ref, e.message);
        return null;
      }
    }
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
