// chart-service.mjs — the server-side chart API client: import/bake jobs, the
// installed-pack registry, set enable/disable/remove, cell upload, and the
// inland-ENC catalogue. One definition of every endpoint + the job-progress
// protocol, shared by the shell (its own install/reconcile path) and the
// <chart-library> component (download queue + execution) so neither reaches for
// raw fetch() or re-implements SSE polling.
//
//   const api = new ChartService({ assets: "/" });
//   const { job } = await api.import({ set, zipUrl, names });   // or {set, cells:[{name,url}]}
//   await api.pollJob(job, { name: "Mid-Atlantic", onStatus: p => t.progress(p.frac, p.sub) });
//   const packs = await api.packs();                            // [{name,enabled,bands,bounds}]
//
// onStatus receives a UI-ready { label, sub, detail, frac } (frac 0..1 or null):
// label is the region title, sub the live action, detail the count-with-unit. The
// raw job phases are mapped to friendly verbs here so callers don't echo server
// filenames. Methods throw on hard failure; pollJob resolves with the final
// status object or rejects on error/timeout.

// Job phase → [verb, noun] for the progress label, e.g. bake → "Generating … tiles".
// The noun is what's being acted on at that phase (source charts while fetching /
// reading; tiles while baking; nothing while finishing).
const PHASE = {
  download: ["Downloading", "charts"], fetch: ["Downloading", "charts"], dl: ["Downloading", "charts"],
  upload: ["Uploading", "charts"],
  extract: ["Extracting", "charts"], unzip: ["Extracting", "charts"], expand: ["Extracting", "charts"],
  parse: ["Reading", "charts"], read: ["Reading", "charts"], import: ["Reading", "charts"],
  bake: ["Generating", "tiles"], tiles: ["Generating", "tiles"], render: ["Generating", "tiles"],
  register: ["Finishing", "up"], finalize: ["Finishing", "up"], index: ["Finishing", "up"],
};

// Bytes → compact "12 MB" / "1.4 KB" (local copy so this module is self-contained).
function fmtBytes(n) {
  if (!n) return "0 B";
  const u = ["B", "KB", "MB", "GB"]; let i = 0;
  while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
  return `${n.toFixed(n < 10 && i > 0 ? 1 : 0)} ${u[i]}`;
}

export class ChartService {
  constructor({ assets = "" } = {}) {
    this._assets = assets;
    this._ienc = undefined; // inland-ENC catalogue cache (array once loaded)
    this._iencPromise = null;
  }

  _url(path) { return `${this._assets}${path}`; }

  // POST /api/import — kick off a server-side fetch+bake. spec is {set, zipUrl,
  // names} (one district bundle) or {set, cells:[{name,url}]} (per-cell). Returns
  // the job descriptor {job}; throws on HTTP error / missing job id.
  async import(spec) {
    const res = await fetch(this._url("api/import"), {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(spec),
    });
    const j = await res.json().catch(() => ({}));
    if (!res.ok || !j.job) throw new Error(j.error || `import HTTP ${res.status}`);
    return j;
  }

  // POST /api/import?set=<set>&cells=<csv> — bake already-uploaded cells (the
  // local-import path). Returns {job}.
  async importCells(set, cellNames) {
    const res = await fetch(this._url(`api/import?set=${encodeURIComponent(set)}&cells=${encodeURIComponent(cellNames.join(","))}`), { method: "POST" });
    const j = await res.json().catch(() => ({}));
    if (!res.ok || !j.job) throw new Error(j.error || `import HTTP ${res.status}`);
    return j;
  }

  // import + wait, in one call (the common case). Resolves with the final status.
  async importAndWait(spec, opts) {
    const { job } = await this.import(spec);
    return this.pollJob(job, opts);
  }

  // Wait for a job, surfacing UI-ready progress via opts.onStatus({label,sub,frac}).
  // Prefers a single SSE stream; falls back to 500ms polling (~20min ceiling).
  async pollJob(job, { name, onStatus = () => {} } = {}) {
    const report = (s) => onStatus(this._formatStatus(s, name));
    if (typeof EventSource !== "undefined") {
      try { return await this._streamJob(job, report, name); }
      catch (e) { console.warn("[job] event stream failed — polling:", e.message); }
    }
    for (let i = 0; i < 2400; i++) {
      const r = await fetch(this._url(`api/import/status?job=${encodeURIComponent(job)}`));
      const s = await r.json().catch(() => ({}));
      if (s.state === "done") return s;
      if (s.state === "error") throw new Error(s.error || "job failed");
      report(s);
      await new Promise((res) => setTimeout(res, 500));
    }
    throw new Error("job timed out");
  }

  // SSE: GET /api/import/events — one long-lived connection, server pushes on change.
  _streamJob(job, report, name) {
    return new Promise((resolve, reject) => {
      const es = new EventSource(this._url(`api/import/events?job=${encodeURIComponent(job)}`));
      let settled = false;
      const done = (fn, arg) => { if (!settled) { settled = true; es.close(); fn(arg); } };
      es.onmessage = (ev) => {
        let s; try { s = JSON.parse(ev.data); } catch { return; }
        if (s.state === "done") return done(resolve, s);
        if (s.state === "error") return done(reject, new Error(s.error || "job failed"));
        report(s);
      };
      es.onerror = () => done(reject, new Error("event stream closed"));
    });
  }

  // Map a raw job status into UI-ready { label, sub, detail, frac }, laid out as
  // three stacked pieces: the region is the stable TITLE (label) on top; the live
  // action is the STATUS (sub) beneath it; the COUNT (detail) sits beside the
  // status, always with its unit spelled out ("7 / 12 charts", "1,234 / 4,567
  // tiles", "12 MB / 45 MB"). The action carries the band but not the noun, so the
  // unit isn't repeated — "Generating coastal…" + "1,234 / 4,567 tiles".
  _formatStatus(s, name) {
    const m = s.phase ? PHASE[s.phase] : null;
    let verb = m ? m[0] : (s.phase ? s.phase[0].toUpperCase() + s.phase.slice(1) : "Working");
    // The bake phase has two visible stages the server distinguishes by unit:
    // "cells" while a band's charts are parsed + portrayed (the long gap before
    // any tile emits), then "tiles" while that band's tiles are generated.
    if (m && m[1] === "tiles" && s.unit === "cells") verb = "Preparing";
    const sub = [verb, s.band].filter(Boolean).join(" ") + "…"; // "Preparing coastal…"
    // Count, with the unit named in every stage so a bare number is never ambiguous.
    let detail = "";
    if (s.unit === "bytes") detail = s.total ? `${fmtBytes(s.done)} / ${fmtBytes(s.total)}` : fmtBytes(s.done);
    else if (s.total) {
      const u = s.unit === "cells" ? "charts" : (s.unit || "");
      detail = `${s.done.toLocaleString()} / ${s.total.toLocaleString()} ${u}`.trim();
    }
    const frac = s.total ? s.done / s.total : (s.percent ? s.percent / 100 : null);
    return { label: name || "", sub, detail, frac };
  }

  // GET /api/packs — the installed-pack registry (single source of truth, incl.
  // server-side enabled state). Returns [{name, enabled, bands, bounds}]; [] offline.
  async packs() {
    try { const j = await fetch(this._url("api/packs")).then((r) => (r.ok ? r.json() : null)); return (j && j.packs) || []; }
    catch (e) { return []; }
  }

  // GET /api/cells — the set of installed cell names. Returns a Set; null on failure
  // (so callers can keep their current view rather than blanking it).
  async cells() {
    try { const j = await fetch(this._url("api/cells")).then((r) => (r.ok ? r.json() : null)); return new Set((j && j.cells) || []); }
    catch (e) { return null; }
  }

  // POST /api/set/{enable,disable} — toggle a pack's rendering (data is kept).
  async setEnabled(set, on) {
    await fetch(this._url(`api/set/${on ? "enable" : "disable"}?set=${encodeURIComponent(set)}`), { method: "POST" });
  }

  // DELETE /api/set — remove a pack's baked tiles/aux (source cells are kept).
  async deleteSet(set) {
    await fetch(this._url(`api/set?set=${encodeURIComponent(set)}`), { method: "DELETE" });
  }

  // PUT /api/cell/<name> — upload raw cell bytes to the server's data store
  // (the drag-a-file local-import path, before importCells bakes them).
  async uploadCell(name, bytes) {
    await fetch(this._url(`api/cell/${encodeURIComponent(name)}`), { method: "PUT", body: bytes });
  }

  // GET /api/ienc/catalog — USACE inland-ENC cells [{name,river,url,bbox,…}].
  // Memoised; the server fetches + parses the upstream catalogue. [] on failure.
  async iencCatalog() {
    if (this._ienc !== undefined) return this._ienc;
    if (!this._iencPromise) {
      this._iencPromise = (async () => {
        try { const j = await fetch(this._url("api/ienc/catalog")).then((r) => (r.ok ? r.json() : null)); return (j && Array.isArray(j.cells)) ? j.cells : []; }
        catch (e) { console.warn("[ienc] catalogue:", e); return []; }
      })();
    }
    this._ienc = await this._iencPromise;
    return this._ienc;
  }

  // Whether the inland-ENC catalogue has finished loading (undefined until then).
  get iencLoaded() { return this._ienc !== undefined; }
  get iencPending() { return !!this._iencPromise && this._ienc === undefined; }
}
