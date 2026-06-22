// settings-store.mjs — the one persistence layer for all settings.
//
// Holds a single settings blob, mirrored to localStorage (instant, per-screen,
// offline) and persisted to the server (GET/POST /api/settings — shared across
// screens, debounced). Everything reads + writes through a NAMESPACE so the
// app's own display settings and each plugin's settings live side by side in one
// store without colliding:
//
//   const settings = new SettingsStore({ assets, prod });
//   settings.load();                          // overlay the server copy at boot
//   const core = settings.ns("core");         // the app's display settings
//   const ais  = settings.ns("ais");          // a plugin's settings
//   core.get("scheme", "day");  core.set("scheme", "night");   // persists
//   ais.get("cpaWarn", 0.5);    ais.set("cpaWarn", 1.0);
//
// Blob shape (backward-compatible with the old flat /api/settings format):
//   { scheme, basemap, mariner:{…}, … ,        ← "core" → top-level keys
//     ais:{…}, "own-ship":{…} }                 ← plugins → nested under their id
// Keeping core at the top level means existing client-settings.json files load
// unchanged; only plugin namespaces add new nested keys.

const LS_KEY = "chartplotter:settings";

export class SettingsStore {
  constructor({ assets = "", prod = false } = {}) {
    this._assets = assets;
    this._prod = prod;
    this._saveT = 0;
    // Seed instantly from the localStorage mirror so boot has values before the
    // server responds (and so prod, which has no server, works at all).
    this._blob = readLocal();
  }

  // Overlay the server-persisted copy (server mode). Best-effort: offline / older
  // server / prod just keeps the local values. Returns the merged blob.
  async load() {
    if (this._prod) return this._blob;
    try {
      const r = await fetch(`${this._assets}api/settings`, { cache: "no-store" });
      if (r.ok) {
        const s = await r.json();
        if (s && typeof s === "object") this._blob = { ...this._blob, ...s };
      }
    } catch (e) { /* offline / older server → keep local */ }
    writeLocal(this._blob);
    return this._blob;
  }

  // A namespaced accessor. name "core" addresses the top-level blob (backward
  // compat); any other name addresses a nested object blob[name].
  ns(name) {
    const top = name === "core";
    const bag = () => (top ? this._blob : (this._blob[name] = this._blob[name] || {}));
    return {
      get: (key, def) => { const v = bag()[key]; return v === undefined ? def : v; },
      set: (key, value) => { bag()[key] = value; this._dirty(); },
      has: (key) => bag()[key] !== undefined,
      all: () => ({ ...bag() }),
      // Shallow-merge several keys at once (one persist).
      merge: (obj) => { Object.assign(bag(), obj || {}); this._dirty(); },
    };
  }

  // The whole blob (read-only snapshot) — for migration / inspection.
  snapshot() { return JSON.parse(JSON.stringify(this._blob)); }

  // Mirror locally now; debounce the server POST so a flurry of toggles coalesces.
  _dirty() {
    writeLocal(this._blob);
    if (this._prod) return; // no server in prod; localStorage is the store
    clearTimeout(this._saveT);
    this._saveT = setTimeout(() => {
      fetch(`${this._assets}api/settings`, {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify(this._blob),
      }).catch(() => { /* best-effort; the localStorage mirror is the fallback */ });
    }, 400);
  }
}

function readLocal() {
  try { return JSON.parse(localStorage.getItem(LS_KEY)) || {}; } catch { return {}; }
}
function writeLocal(blob) {
  try { localStorage.setItem(LS_KEY, JSON.stringify(blob)); } catch (e) { /* quota / private mode */ }
}
