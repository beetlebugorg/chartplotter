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
    this.auxUrl = "";        // companion aux zip (TXTDSC/PICREP files), if the manifest names one
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
      .then((j) => {
        this.districts = (j && j.districts) || [];
        // The aux zip sits beside the manifest; resolve its basename against manUrl.
        this.auxUrl = j && j.aux ? manUrl.replace(/[^/]*$/, j.aux) : "";
      })
      .catch(() => { this.districts = []; this.auxUrl = ""; });
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

  // Acquisition is SERVER-SIDE now: the shell POSTs a fetch spec to /api/import
  // (zipUrl/names or per-cell {name,url}) and the server downloads the cells from
  // NOAA into its XDG cache, then bakes. (The old in-browser bulk/per-cell
  // download-into-OPFS primitives were retired with the wasm baker.) Per-cell
  // NOAA URLs come from each catalogue entry's `z` field; the district bundle URL
  // is districtZipUrl(cg) above.
}
