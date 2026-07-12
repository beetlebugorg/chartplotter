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

  // POST /api/import/packs — download SEVERAL districts into their provider ENC_ROOTs
  // and re-bake each touched provider (one archive per provider) in ONE job. packs is
  // an array of { set:"<provider>-<district>", zipUrl|cells, names? }. Returns { job }.
  async importPacks(packs) {
    const res = await fetch(this._url("api/import/packs"), {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ packs }),
    });
    const j = await res.json().catch(() => ({}));
    if (!res.ok || !j.job) throw new Error(j.error || `import HTTP ${res.status}`);
    return j;
  }

  async importPacksAndWait(packs, opts) {
    const { job } = await this.importPacks(packs);
    return this.pollJob(job, opts);
  }

  // POST /api/import?set=auto (multipart) — upload a whole ENC exchange-set zip;
  // the server parses its CATALOG.031, names the pack from its identity, bakes it,
  // and writes the metadata sidecar. Returns {job, set} (the server-derived name).
  // `set` defaults to "auto"; pass a name to override.
  async importZip(file, { set = "auto" } = {}) {
    const form = new FormData();
    form.append("file", file, file.name || "upload.zip");
    const res = await fetch(this._url(`api/import?set=${encodeURIComponent(set)}`), { method: "POST", body: form });
    const j = await res.json().catch(() => ({}));
    if (!res.ok || !j.job) throw new Error(j.error || `import HTTP ${res.status}`);
    return j; // {ok, job, set}
  }

  // upload a zip and wait for the bake. Resolves with { status, set } so the
  // caller knows the server-derived pack name.
  async importZipAndWait(file, opts) {
    const { job, set } = await this.importZip(file, opts);
    const status = await this.pollJob(job, opts);
    return { status, set };
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
    // The SERVER owns the wording: `action` is the live step and `detail` the count/ETA beside
    // it, both display-ready — the client renders them verbatim rather than re-deriving the
    // status from the raw phase/unit/note fields. `frac` is the bar fill (0..1, or null → the
    // shell sweeps an indeterminate bar). The client only supplies the region title (label) and
    // the pack context (pack/packNum/packTotal, resolved to a friendly name by the caller), so
    // progress reads "Mid-Atlantic (2 of 4) · Composing tiles…".
    const frac = (s.frac === null || s.frac === undefined) ? null : s.frac;
    return {
      label: name || "",
      sub: s.action ? `${s.action}…` : "",
      detail: s.detail || "",
      eta: s.eta || "", // kept separate from detail so each pins in its own fixed slot
      frac,
      pack: s.pack || "", packNum: s.packNum || 0, packTotal: s.packTotal || 0,
    };
  }

  // GET /api/packs — the installed-pack registry (single source of truth, incl.
  // server-side enabled state). Returns [{name, enabled, bands, bounds}]; [] offline.
  async packs() {
    try { const j = await fetch(this._url("api/packs")).then((r) => (r.ok ? r.json() : null)); return (j && j.packs) || []; }
    catch (e) { return []; }
  }

  // GET /api/pack/<name> — one pack's full extracted metadata, incl. the per-cell
  // list (title/scale/edition/date/agency/bbox). null when the pack has no sidecar
  // (built-in packs, or one baked before metadata extraction). Used for the
  // per-upload detail view.
  async packDetail(name) {
    try { return await fetch(this._url(`api/pack/${encodeURIComponent(name)}`)).then((r) => (r.ok ? r.json() : null)); }
    catch (e) { return null; }
  }

  // GET /api/cells — the set of installed cell names. Returns a Set; null on failure
  // (so callers can keep their current view rather than blanking it).
  async cells() {
    try { const j = await fetch(this._url("api/cells")).then((r) => (r.ok ? r.json() : null)); return new Set((j && j.cells) || []); }
    catch (e) { return null; }
  }

  // GET /api/cells?active=1 — the ACTIVE (enabled-pack) cells that are indexed,
  // as search-catalog entries {n,l,bb}: so a cell can be found by name and flown
  // to its footprint. Only cells with known bounds (indexed) are returned.
  async activeCells() {
    try {
      const j = await fetch(this._url("api/cells?active=1")).then((r) => (r.ok ? r.json() : null));
      const bb = (j && j.bbox) || {};
      return Object.keys(bb).map((n) => ({ n, l: n, bb: bb[n] }));
    } catch (e) { return []; }
  }

  // POST /api/set/{enable,disable} — toggle a pack's rendering (data is kept).
  async setEnabled(set, on) {
    await fetch(this._url(`api/set/${on ? "enable" : "disable"}?set=${encodeURIComponent(set)}`), { method: "POST" });
  }

  // DELETE /api/set — uninstall a whole PROVIDER (baked bundle + its ENC_ROOT source).
  async deleteSet(set) {
    await fetch(this._url(`api/set?set=${encodeURIComponent(set)}`), { method: "DELETE" });
  }

  // DELETE /api/district — remove ONE district from a provider; the server re-bakes the
  // provider (or drops it if that was the last district) as a background job. Returns
  // {job} to poll (like a download), or {} when no re-bake job was started.
  async deleteDistrict(provider, district) {
    const res = await fetch(
      this._url(`api/district?provider=${encodeURIComponent(provider)}&district=${encodeURIComponent(district)}`),
      { method: "DELETE" },
    );
    return await res.json().catch(() => ({}));
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
