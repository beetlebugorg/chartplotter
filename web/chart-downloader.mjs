// ChartDownloader — the chart discovery + acquisition domain, extracted from the
// <chart-plotter-app> shell so "getting charts" (NOAA catalog, district packs,
// download, ZIP/.000 import) lives in one place rather than threaded through the
// shell. See specs/web-architecture.md.
//
// The shell still owns the OPFS store and the installed-cell Set (shared with the
// rendering/coverage code); this owns the NOAA catalogue and the acquisition
// logic built on top of it, reading the installed set through getInstalled().
//
// Increment 1: the catalogue/discovery core (catalog + manifest load, per-district
// helpers). Increment 2: the acquisition primitives (bulk-zip + per-cell download
// into the store). The shell keeps the orchestration (task state, agreement gate,
// re-render, renderer refresh) and calls these with an onProgress callback.

import { readCentralDirectory, cellEntries, extractEntry } from "./zip-import.mjs";

// Human-readable byte size (the shell has its own copy for its status popup).
function fmtBytes(n) {
  if (!n) return "0 B";
  const u = ["B", "KB", "MB", "GB"]; let i = 0;
  while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
  return `${n.toFixed(n < 10 && i > 0 ? 1 : 0)} ${u[i]}`;
}

export class ChartDownloader {
  // deps: { assets:string, cfg:(name)=>string, store:ChartStore, getInstalled:()=>Set<string> }
  constructor(deps = {}) {
    this._assets = deps.assets || "./";
    this._cfg = deps.cfg || (() => "");
    this.store = deps.store || null;
    this._getInstalled = deps.getInstalled || (() => new Set());

    this.catalog = [];       // [{n,l,s,e,u,d,z,zs,bb,cg,rg}] — NOAA cells + metadata
    this.byName = new Map(); // cell name -> catalog entry
    this.districts = [];     // hosted per-district archives (from charts-index.json)
    this.catalogDate = "";   // when the NOAA catalog snapshot was taken
  }

  // Fetch the NOAA catalogue (catalog.json) and the hosted per-district archive
  // manifest (charts-index.json; URL overridable via catalog="…" / ?catalog=…).
  // Best-effort: a missing file just leaves the corresponding list empty. Resolves
  // once both have settled.
  loadCatalog() {
    const manUrl = this._cfg("catalog") || (this._assets + "charts-index.json");
    const cat = fetch(this._assets + "catalog.json")
      .then((r) => (r.ok ? r.json() : null))
      .then((j) => { this.catalogDate = (j && j.date) || ""; return (j && j.cells) || []; })
      .catch(() => [])
      .then((cells) => {
        // NOAA titles are HTML-encoded ("Hawai&#39;i"); decode once so esc() can
        // re-encode them safely for display instead of double-encoding the entity.
        const ta = document.createElement("textarea");
        const decode = (s) => { if (!s || s.indexOf("&") < 0) return s; ta.innerHTML = s; return ta.value; };
        for (const c of cells) if (c.l) c.l = decode(c.l);
        this.catalog = cells;
        this.byName = new Map(cells.map((c) => [c.n, c]));
      });
    const man = fetch(manUrl)
      .then((r) => (r.ok ? r.json() : null))
      .then((j) => { this.districts = (j && j.districts) || []; })
      .catch(() => { this.districts = []; });
    return Promise.all([cat, man]);
  }

  // Cell names belonging to a Coast Guard district.
  districtCellNames(cg) {
    const out = [];
    for (const c of this.catalog) if (c.cg === cg) out.push(c.n);
    return out;
  }

  // Counts + download size for a pack card: cells in the catalogue, how many are
  // already stored on this device, and the total download bytes.
  districtStat(cg) {
    const installed = this._getInstalled();
    let total = 0, have = 0, bytes = 0;
    for (const c of this.catalog) {
      if (c.cg !== cg) continue;
      total++;
      if (typeof c.zs === "number") bytes += c.zs;
      if (installed.has(c.n)) have++;
    }
    return { total, have, bytes };
  }

  // NOAA's per-district bundle URL (NNCGD_ENCs.zip), derived from a catalogue
  // cell's per-cell zip URL so it tracks the catalogue host. cg is zero-padded.
  districtZipUrl(cg) {
    const any = this.catalog.find((c) => c.z);
    const dir = any ? any.z.replace(/[^/]+$/, "") : "https://www.charts.noaa.gov/ENCs/";
    return dir + String(cg).padStart(2, "0") + "CGD_ENCs.zip";
  }

  // NOAA's All_ENCs.zip URL (fallback when a per-district bundle isn't available).
  allEncsUrl() {
    const any = this.catalog.find((c) => c.z);
    return any ? any.z.replace(/[^/]+$/, "All_ENCs.zip") : "https://www.charts.noaa.gov/ENCs/All_ENCs.zip";
  }

  // -- acquisition primitives (fetch cell bytes → OPFS store) ---------------
  // Each downloads the wanted cells into the store, marks them installed (the
  // shell's shared set, via getInstalled), reports through onProgress({label,sub,
  // frac}), and returns the count that FAILED. The shell wraps these with the
  // task/agreement/UI/renderer orchestration.

  // Bulk path: download a NOAA zip bundle ONCE (streamed through the byte proxy
  // into a disk-backed Blob), then slice + inflate the wanted base cells out of
  // it locally — no per-cell network. `label` names the bundle for progress.
  // Throws if the archive can't be opened (so the caller can fall back).
  async bulkExtractZip(url, names, label, onProgress = () => {}) {
    const resp = await fetch("api/proxy?url=" + encodeURIComponent(url));
    if (!resp.ok) throw new Error("proxy HTTP " + resp.status);

    // Stream into a Blob, reporting bytes. Piping through a counting
    // TransformStream into Response().blob() lets the browser back the Blob on
    // disk (no huge JS-heap spike) while we track progress.
    const total = +resp.headers.get("Content-Length") || 0;
    let blob;
    if (resp.body) {
      let recv = 0;
      const tap = new TransformStream({
        transform: (chunk, ctrl) => {
          recv += chunk.length;
          onProgress({ label: `Downloading ${label}…`, sub: total ? `${fmtBytes(recv)} / ${fmtBytes(total)} · ${names.length} charts` : fmtBytes(recv), frac: total ? recv / total : null });
          ctrl.enqueue(chunk);
        },
      });
      blob = await new Response(resp.body.pipeThrough(tap)).blob();
    } else {
      blob = await resp.blob();
    }

    onProgress({ label: `Reading ${label}…`, sub: `${names.length} charts — extracting`, frac: null });
    const inZip = new Map(cellEntries(await readCentralDirectory(blob)).map((c) => [c.name, c]));
    const installed = this._getInstalled();

    let done = 0, failed = 0;
    for (const name of names) {
      onProgress({ label: `Extracting ${names.length} charts`, sub: `${name} · ${done + 1} of ${names.length}`, frac: names.length ? done / names.length : null });
      const rec = inZip.get(name);
      if (!rec || !rec.base) { console.warn("[bulk]", name, "not in", label); failed++; done++; continue; }
      try {
        await this.store.put(name, await extractEntry(blob, rec.base));
        installed.add(name);
      } catch (e) { console.warn("[bulk]", name, e.message); failed++; }
      done++;
    }
    console.log(`[bulk] ${label}: extracted ${names.length - failed}/${names.length}`);
    return failed;
  }

  // All_ENCs.zip fallback for when a per-district bundle isn't available.
  bulkExtract(names, onProgress) { return this.bulkExtractZip(this.allEncsUrl(), names, "All_ENCs.zip", onProgress); }

  // Per-cell fallback: fetch each cell's own zip through the byte proxy.
  async downloadPerCell(names, onProgress = () => {}) {
    const installed = this._getInstalled();
    let done = 0, failed = 0;
    for (const name of names) {
      onProgress({ label: `Downloading ${names.length} chart${names.length !== 1 ? "s" : ""}`, sub: `${name} · ${done + 1} of ${names.length}`, frac: names.length ? done / names.length : null });
      try {
        const c = this.byName.get(name);
        const url = "api/cell/" + encodeURIComponent(name) + (c && c.z ? "?url=" + encodeURIComponent(c.z) : "");
        const resp = await fetch(url);
        if (!resp.ok) throw new Error("HTTP " + resp.status);
        await this.store.put(name, new Uint8Array(await resp.arrayBuffer()));
        installed.add(name);
      } catch (e) { console.warn("[download]", name, e.message); failed++; }
      done++;
    }
    return failed;
  }
}
