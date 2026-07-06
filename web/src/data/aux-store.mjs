// AuxStore — resolves the external resources an ENC feature points at by *filename*
// rather than carrying inline: TXTDSC/NTXTDS textual descriptions and PICREP
// pictures. The baked tiles carry only the filename (in the s57 blob); the pick
// report resolves it here, by the (upper-cased) referenced name.
//
// Two source layouts, ONE load path, resolving one file at a time (never a whole-set
// download just to render one pick):
//   • load(manifestUrl) — a static index.json manifest whose files sit beside it as
//     loose static files, each a plain static GET. IDENTICAL online (the server's
//     /aux/index.json) or off (a bundle's aux/index.json on any static host / file://).
//   • load(zipUrl) — LEGACY: a single companion .aux.zip fetched whole and unzipped
//     client-side (pre-loose bundles); auto-detected by extension so old archives resolve.

import { readCentralDirectory, extractEntry } from "./zip-import.mjs";

export class AuxStore {
  constructor() {
    this._index = null;      // referencedName(UPPER) → { stored, type, from } | { type }
    this._entries = null;    // stored entry name → central-directory entry (legacy zip mode)
    this._blob = null;       // the fetched aux zip (legacy zip mode)
    this._staticBase = null; // dir holding the loose files when resolving static
    this._cache = new Map(); // key → resolved { type, text|url }
  }

  // Load the aux content: a static index.json manifest (loose files beside it) — the
  // one path used both online (server /aux/index.json) and offline (a bundle's
  // aux/index.json). A legacy ".aux.zip" is fetched whole and unzipped instead,
  // auto-detected by extension. Best-effort: failure leaves the store empty.
  async load(url) {
    if (/\.zip(\?|#|$)/i.test(url)) return this._loadZip(url);
    return this.loadStatic(url);
  }

  // Loose/offline layout: fetch the static index.json manifest and remember the dir it
  // sits in; resolve() then fetches each file as a plain static GET. No zip, no server.
  async loadStatic(manifestUrl) {
    try {
      const r = await fetch(manifestUrl);
      if (!r.ok) throw new Error("HTTP " + r.status);
      const j = await r.json();
      const files = (j && j.files) || {};
      const idx = {};
      for (const [name, e] of Object.entries(files)) idx[String(name).toUpperCase()] = e;
      this._index = idx;
      this._staticBase = manifestUrl.slice(0, manifestUrl.lastIndexOf("/") + 1);
      return true;
    } catch (e) {
      console.warn("[aux] static manifest load failed:", e.message);
      return false;
    }
  }

  // LEGACY: fetch + parse a whole companion aux.zip. Best-effort.
  async _loadZip(url) {
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
    // Static layout: fetch the single file on demand from beside the manifest; the
    // response Content-Type tells image vs text (falling back to the indexed type).
    if (this._staticBase) {
      if (this._cache.has(key)) return this._cache.get(key);
      const fileURL = `${this._staticBase}${encodeURIComponent(meta.stored || ref)}`;
      try {
        const r = await fetch(fileURL);
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
    // Legacy zip mode: extract the stored entry from the fetched archive.
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
