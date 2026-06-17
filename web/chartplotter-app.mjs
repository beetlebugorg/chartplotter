// <chart-plotter-app> — the chart-management shell around <chart-plotter>.
//
// <chart-plotter> is a pure renderer; this wraps it with the UI a user needs to
// actually run a plotter: discover charts on a map (NOAA catalog cell boxes),
// import them with no backend (drop a .zip / .000, unzipped in-browser to OPFS),
// manage what's installed, and set S-52 mariner options.
//
//   <script type="module" src="chartplotter-app.mjs"></script>
//   <chart-plotter-app></chart-plotter-app>
//
// Attributes (all optional, forwarded to the inner <chart-plotter>):
//   center, zoom, assets, cell-url   — see chartplotter.mjs
//   basemap   "coastline" | "osm" | "none"   default "coastline" (offline GSHHG)
//
// Everything is driven through the renderer's public API (bakePmtiles/setArchive/
// listCharts/setScheme/setMariner and its `map` handle) plus the shared ChartStore.

import "./chartplotter.mjs"; // defines <chart-plotter> (the renderer we wrap)
import { ChartStore } from "./chart-store.mjs";
import { readCentralDirectory, cellEntries, extractEntry } from "./zip-import.mjs";

const SCHEMES = ["day", "dusk", "night", "day_bright"];
const SCHEME_LABEL = { day: "Day", dusk: "Dusk", night: "Night", day_bright: "Bright" };
const M_TO_FT = 3.280839895; // depth-setting display conversion (values stored in metres)
const LS_SCHEME = "chartplotter:scheme";
const LS_MARINER = "chartplotter:mariner";
const LS_VIEW = "chartplotter:view";
const LS_SOURCE = "chartplotter:source"; // {type:"blob"} or {type:"url",file}
const LS_AGREE = "chartplotter:enc-agreement"; // NOAA ENC User Agreement acceptance
// NOAA's ENC distribution pages + the User Agreement that must be displayed and
// accepted before downloading ENCs (charts.noaa.gov/ENCs/ENCs.shtml §3).
const NOAA_ENC_URL = "https://www.charts.noaa.gov/ENCs/ENCs.shtml";
const NOAA_AGREEMENT_URL = "https://www.charts.noaa.gov/ENCs/ENC_Agreement.shtml";
// NOTE: the installed-region list is NOT cached in localStorage — the server's
// GET /api/charts manifest (one entry per baked region archive in its XDG cache)
// is the single source of truth, so the UI can never show charts that aren't
// actually on disk.

// Box colours by state (kept readable in both day and night chrome).
const STATE_FILL = { installed: "#2e7d32", archive: "#1565c0", catalog: "#000000" };

// NOAA navigational-purpose bands, coarse→fine, with a colour for the picker so
// overlapping cells are distinguishable and each band's toggle is identifiable.
const BANDS = ["overview", "general", "coastal", "approach", "harbor", "berthing"];
const BAND_LABEL = { overview: "Overview", general: "General", coastal: "Coastal", approach: "Approach", harbor: "Harbor", berthing: "Berthing" };
const BAND_COLOR = { overview: "#7e57c2", general: "#5c6bc0", coastal: "#26a69a", approach: "#9ccc65", harbor: "#ffa726", berthing: "#ef5350" };

export class ChartPlotterApp extends HTMLElement {
  constructor() {
    super();
    this.attachShadow({ mode: "open" });
    this._catalog = [];                 // [{n,l,s,e,u,d,z,zs,bb}]
    this._byName = new Map();           // name -> catalog entry
    this._installed = new Set();        // all stored cell names
    this._archive = new Map();          // name -> {blob, entry, meta} from opened zips
    this._selected = new Set();         // names ticked for import / NOAA download
    this._dlRegions = new Set();        // installed NOAA region numbers (from GET /api/charts)
    this._regionArchives = [];          // [{num,file,bounds}] — one pmtiles per installed region
    this._districts = [];               // hosted per-district archives (charts-index.json)
    this._hasArchive = false;           // is a chart archive currently loaded?
    this._mariner = loadJSON(LS_MARINER, {});
    // Migrate the old single-value display category (base|standard|other) to
    // the multi-select Base/Standard/Other booleans (now client-side filters).
    if (this._mariner.displayCategory) {
      const c = this._mariner.displayCategory;
      this._mariner.displayBase = true;
      this._mariner.displayStandard = c === "standard" || c === "other";
      this._mariner.displayOther = c === "other";
      delete this._mariner.displayCategory;
    }
    this._scheme = localStorage.getItem(LS_SCHEME) || "day";
    this._agreed = localStorage.getItem(LS_AGREE) === "1"; // NOAA ENC agreement accepted
    this._region = null;                // the region currently open in the drawer (null = list)
    // The provision job is a SERVER task: `_task` mirrors GET /api/tasks (polled,
    // never invented), `_taskMeta` holds the client-only label hints (which region,
    // which verb) the server doesn't know. `_poll` is the polling interval handle.
    this._task = null;                  // mirror of the server's current task (or null)
    this._taskMeta = null;              // { name, verb, bytes, errMsg } — pill labelling
    this._poll = null;                  // setInterval handle while a task is observed
    // Resolves once the inner renderer's `ready` fires.
    this._readyPromise = new Promise((res) => (this._resolveReady = res));
  }

  get _assets() {
    let a = this.getAttribute("assets") || "./";
    if (!a.endsWith("/")) a += "/";
    return a;
  }

  connectedCallback() {
    this.boot().catch((e) => {
      console.error("[chartplotter-app]", e);
      this.shadowRoot.innerHTML = `<div style="font:13px system-ui;padding:12px;color:#900">chart manager failed: ${e.message}</div>`;
    });
  }

  async boot() {
    this.renderChrome();

    // What's already stored? We do NOT eagerly load it (that's the slow part on
    // a device with many charts) — the renderer boots with an empty `charts`
    // attribute so the map/basemap paints immediately, then we ingest cells
    // lazily by viewport (see ingestViewport).
    this._store = new ChartStore();
    this._installed = new Set(await this._store.list());

    // Catalog drives the picker AND the lazy-load gating (cell bboxes). Kick it
    // off in parallel; ingestViewport awaits it.
    this._catalogReady = this.loadCatalog();

    const plotter = document.createElement("chart-plotter");
    const view = loadJSON(LS_VIEW, null); // resume the last view → load in-region
    plotter.setAttribute("center", view ? view.center.join(",") : (this.getAttribute("center") || "-76.4875,38.975"));
    plotter.setAttribute("zoom", String(view ? view.zoom : (this.getAttribute("zoom") || 11)));
    if (this.hasAttribute("cell-url")) plotter.setAttribute("cell-url", this.getAttribute("cell-url"));
    plotter.setAttribute("assets", this._assets);
    plotter.setAttribute("basemap", this.getAttribute("basemap") || "coastline");
    this._plotter = plotter;
    this.shadowRoot.getElementById("map").appendChild(plotter);

    plotter.addEventListener("ready", (e) => this.onReady(e.detail.map), { once: true });
  }

  loadCatalog() {
    // Optional — the picker just shows nothing if absent. Also load the hosted
    // per-district archive manifest (charts-index.json, written by --bake-districts).
    const cat = fetch(this._assets + "catalog.json")
      .then((r) => (r.ok ? r.json() : null)).then((j) => (j && j.cells) || []).catch(() => [])
      .then((cells) => { this._catalog = cells; for (const c of cells) this._byName.set(c.n, c); });
    const man = fetch(this._assets + "charts-index.json")
      .then((r) => (r.ok ? r.json() : null)).then((j) => { this._districts = (j && j.districts) || []; }).catch(() => { this._districts = []; });
    return Promise.all([cat, man]);
  }

  async onReady(map) {
    this._map = map;
    this._resolveReady();
    // Apply persisted display prefs.
    if (this._scheme !== "day") this._plotter.setScheme(this._scheme);
    if (Object.keys(this._mariner).length) {
      try { this._plotter.setMariner(this._mariner); } catch (e) { console.warn(e); }
    }
    await this._catalogReady;
    this._buildRegions();
    this.addCatalogOverlay(map);
    await this.restoreArchive();
    this.updateEmptyState();
    this.renderCharts();
    this._assessCoverage();
    // Refresh-resume: if a provision job is still running on the server, re-attach
    // (show the pill + start polling). A finished/idle task is ignored.
    this._reattachTask();

    // Persist the view so a refresh resumes where you were; refresh the coverage
    // panel's in-view cell list for the new viewport.
    map.on("moveend", () => { this.saveView(); this._assessCoverage(); });

    // Live zoom/scale/band readout (left of the statusbar).
    this._updateHud();
    map.on("move", () => this._updateHud());

    // Close any pinned band-pill popup when clicking elsewhere (pill/cell clicks
    // stopPropagation, so this only fires for clicks outside them).
    this.shadowRoot.addEventListener("click", () => {
      this.shadowRoot.querySelectorAll(".sb-band-wrap.open").forEach((w) => w.classList.remove("open"));
    });
  }

  // -- zoom/scale HUD + inspect tool --------------------------------------
  // Live readout of the current view: zoom, map scale denominator (web-Mercator
  // at the centre latitude), and the active NOAA band for this zoom (the source
  // that paints here — see CHART_BANDS in chartplotter.mjs).
  _updateHud() {
    const el = this.shadowRoot.getElementById("cov-readout");
    if (!el || !this._map) return;
    const z = this._map.getZoom(), c = this._map.getCenter();
    const band = bandForZoom(z);
    // Fixed-width fields (+ tabular-nums in CSS) so scale/zoom don't reflow the
    // bar as their digit counts change.
    el.innerHTML =
      `<span class="hud-main"><span class="hud-dot" style="background:${BAND_COLOR[band]}"></span>` +
      `<span class="hud-band">${BAND_LABEL[band]}</span>` +
      `<span class="hud-scale">1:${fmtScale(scaleDenom(z, c.lat))}</span>` +
      `<span class="hud-z">z${z.toFixed(1)}</span></span>`;
  }


  // Load the chart archive(s) on boot. An explicit `?pmtiles=` query pins ONE
  // archive; otherwise we open EVERY hosted district at once (each opens by
  // reading just its header + directory — tiles stream by viewport) so charts
  // appear wherever you look, at every baked zoom, with no manual picking. A
  // persisted uploaded archive is added alongside. Sets `_hasArchive`.
  async restoreArchive() {
    const urlParam = new URLSearchParams(location.search).get("pmtiles");
    if (urlParam && await this.loadHostedArchive(urlParam, this._districtFor(urlParam))) return;

    let loaded = false;
    if (this._districts.length) {
      // Each manifest entry is one NOAA band of a region (charts-r<N>-<band>.pmtiles);
      // route it to its band source. Older bandless manifests fall back to `all`.
      const arcs = await this._plotter.addArchives(this._districts.map((d) => ({ src: d.file, band: d.band || "all" })));
      if (arcs.length) { this._hasArchive = true; loaded = true; this._frameInitial(); }
    }
    if (this._districts.length) {
      // Hosted-district deployment: a persisted uploaded archive coexists.
      const src = loadJSON(LS_SOURCE, null);
      if (src && src.type === "blob") {
        try { const b = await archiveGet(); if (b) { await this._plotter.addArchive(b); this._markArchive({ type: "blob" }); loaded = true; } } catch (e) { console.warn(e); }
      }
    } else {
      // The provisioned deployment: ONE pmtiles per NOAA region in the server's
      // XDG cache, listed by GET /api/charts. Render exactly that set (+ blob).
      const regions = await this._loadManifest();
      await this._applyArchives();
      if (regions && regions.length) {
        loaded = true;
        if (!loadJSON(LS_VIEW, null)) this._frameRegionArchives(regions);
      }
    }
    if (loaded) { this.updateEmptyState(); return; }
    if (this.getAttribute("pmtiles")) await this.loadHostedArchive(this.getAttribute("pmtiles"));
  }

  // With no saved view, make sure charts are on screen at boot. If the default
  // centre is already within some district's coverage, leave it (the union of all
  // districts would be the whole country — useless to frame). Only when the centre
  // is over no district at all do we jump to the first one. (District bboxes
  // overlap, so "contains the centre" can't pick THE district — but it reliably
  // answers "is the centre covered at all", which is all we need here.)
  _frameInitial() {
    if (loadJSON(LS_VIEW, null) || !this._districts.length) return;
    const c = this._map.getCenter();
    const covered = (d) => d.bounds && c.lng >= d.bounds[0] && c.lng <= d.bounds[2] && c.lat >= d.bounds[1] && c.lat <= d.bounds[3];
    if (this._districts.some(covered)) return;
    const d = this._districts[0];
    if (d.bounds) this._map.fitBounds([[d.bounds[0], d.bounds[1]], [d.bounds[2], d.bounds[3]]], { padding: 40, duration: 0 });
  }

  _districtFor(file) { return this._districts.find((d) => d.file === file) || null; }

  // Read the per-region manifest (GET /api/charts) the server builds from the
  // cache: one entry per baked region archive ({num,file,bounds}). Sets
  // _dlRegions (installed region numbers — a cell is "installed" when its region
  // is) and _regionArchives (the archives to render). Returns the regions array,
  // or null on a transient failure (keep what we have).
  async _loadManifest() {
    try {
      const r = await fetch(`api/charts?t=${Date.now()}`);
      if (!r.ok) return null;
      const j = await r.json();
      const regions = Array.isArray(j.regions) ? j.regions : [];
      this._regionArchives = regions;
      this._dlRegions = new Set(regions.map((x) => x.num));
      return regions;
    } catch { return null; } // network error → keep what we have
  }

  // Render exactly the installed region archives (each fanned across the per-band
  // sources), plus a persisted uploaded blob if any. Add/remove a region just
  // re-applies the manifest's set — header reads only, no re-bake.
  async _applyArchives() {
    const urls = (this._regionArchives || []).map((x) => `charts/${x.file}?t=${Date.now()}`);
    await this._plotter.loadRegions(urls);
    if (urls.length) this._hasArchive = true;
    const src = loadJSON(LS_SOURCE, null);
    if (src && src.type === "blob") {
      try { const b = await archiveGet(); if (b) await this._plotter.addArchive(b); } catch (e) { console.warn(e); }
    }
  }

  // Fetch + render a hosted archive (incremental, via HTTP Range), framing to
  // its bounds when there's no saved view. Returns true on success. `add` ADDS
  // it to the loaded coverage (vs. REPLACING) — used when restoring the
  // provisioned archive so a transient load failure can't wipe already-loaded
  // districts (setArchive clears every band BEFORE the new one loads).
  async loadHostedArchive(url, entry, add = false) {
    try {
      const arc = add ? await this._plotter.addArchive(url, "all") : await this._plotter.loadArchiveUrl(url);
      const b = (entry && entry.bounds) || (arc && arc.bounds);
      if (b && !loadJSON(LS_VIEW, null)) this._map.fitBounds([[b[0], b[1]], [b[2], b[3]]], { padding: 40, duration: 0 });
      this._markArchive(entry ? { type: "url", file: entry.file } : null);
      return true;
    } catch (e) { console.warn("[archive] load", url, e); return false; }
  }

  // Record the loaded archive: flip `_hasArchive`, persist the source so the next
  // visit restores it, and refresh the empty-state.
  _markArchive(source) {
    this._hasArchive = true;
    this._curSource = source;
    if (source) localStorage.setItem(LS_SOURCE, JSON.stringify(source));
    this.updateEmptyState();
  }

  // -- Charts: one region-centric surface (browse + download + view existing) --
  // Open the Charts drawer to the region browser (the one chart surface).
  openCharts() {
    this._region = null;
    this._section = "charts";
    this.shadowRoot.querySelectorAll(".panel").forEach((p) => p.classList.toggle("sel", p.dataset.panel === "charts"));
    this.shadowRoot.getElementById("empty").hidden = true;
    this.setDrawerOpen(true);
    this.renderCharts();
  }

  // -- background provision task (server-owned, client-observed) -----------
  // Provisioning runs on the SERVER as a background job; the client starts it
  // (POST /api/provision), then POLLS GET /api/tasks. `_task` is a pure mirror of
  // that poll — closing/refreshing the page never cancels the job, and on boot we
  // re-attach to whatever's running (see `_reattachTask`). The persistent pill +
  // drawer progress card both render from `_task`.

  // Start (or no-op to) the background provision of `cells` (the full set to
  // bake). `meta` = { name (region label), verb, bytes } for the pill. Once the
  // POST is acked, progress comes from polling.
  // NOAA ENC User Agreement gate: must be displayed + accepted before any chart
  // download (charts.noaa.gov/ENCs §3). Resolves true once accepted (persisted).
  _ensureAgreed() {
    if (this._agreed) return Promise.resolve(true);
    return this._showAgreement();
  }
  _showAgreement() {
    return new Promise((resolve) => {
      const m = this.shadowRoot.getElementById("agree");
      if (!m) return resolve(this._agreed);
      m.hidden = false;
      this._agreeResolve = resolve;
    });
  }
  _resolveAgreement(accepted) {
    const m = this.shadowRoot.getElementById("agree");
    if (m) m.hidden = true;
    if (accepted) { this._agreed = true; try { localStorage.setItem(LS_AGREE, "1"); } catch {} }
    const r = this._agreeResolve; this._agreeResolve = null;
    if (r) r(accepted);
  }

  // On boot, re-attach to a job that's still running on the server (refresh-
  // resume — no client-side job persistence). A finished/idle task is ignored so
  // a stale "done" never shows a phantom pill.
  async _reattachTask() {
    try {
      const r = await fetch(`api/tasks?t=${Date.now()}`);
      const j = await r.json();
      if (j && j.task != null && j.status === "running") {
        this._task = j;
        this._taskMeta = { name: null, verb: "Downloading" }; // we didn't start it → generic label
        this._renderTaskUI();
        this._startPolling();
      }
    } catch { /* no server / transient — nothing to re-attach */ }
  }

  _startPolling() {
    if (this._poll) return;
    this._pollTask();
    this._poll = setInterval(() => this._pollTask(), 750);
  }
  _stopPolling() { if (this._poll) { clearInterval(this._poll); this._poll = null; } }

  // One poll tick: mirror GET /api/tasks into `_task`, re-render, and on a
  // terminal status stop polling (re-reading the on-disk manifest on success).
  async _pollTask() {
    let j;
    try { j = await (await fetch(`api/tasks?t=${Date.now()}`)).json(); }
    catch { return; } // transient — try again next tick, keep the last render
    if (!j || j.task == null) { this._stopPolling(); this._clearTask(); return; }
    this._task = j;
    this._renderTaskUI();
    if (j.status === "running") return;
    this._stopPolling();
    if (j.status === "done") await this._onTaskDone();
    else this._clearTaskSoon(3500); // error: leave the red pill up briefly
  }

  // A provision finished: the server's per-region manifest is now the truth —
  // reload it, render the (new) region archive set, re-project, flash "done".
  async _onTaskDone() {
    await this._loadManifest();
    await this._applyArchives();
    this.updateEmptyState();
    this._assessCoverage();
    if (this._section === "charts" && this._drawerOpen()) this.renderCharts();
    // success flourish, then clear the pill.
    this._task = { status: "done", _flourish: true };
    this._renderTaskUI();
    this._clearTaskSoon(1500);
  }

  _drawerOpen() { return this.shadowRoot.getElementById("drawer").classList.contains("open"); }

  // True while a provision (download/remove) job is in flight. The server runs
  // ONE job at a time, so this gates starting another — you can't download again
  // mid-download.
  _taskRunning() { return !!this._task && this._task.status === "running"; }

  // Clear the pill after `ms`, but only if the task is still terminal (a new job
  // started in the meantime must not be wiped).
  _clearTaskSoon(ms) {
    setTimeout(() => { if (!this._task || this._task.status !== "running") this._clearTask(); }, ms);
  }
  _clearTask() { this._task = null; this._taskMeta = null; this._stopPolling(); this._renderTaskUI(); }

  // Map `_task` (+ `_taskMeta`) → the pill text and the drawer card fields.
  _taskDisplay() {
    const t = this._task;
    if (!t) return null;
    const meta = this._taskMeta || {};
    const name = meta.name || null;
    if (t.status === "error")
      return { pill: `${meta.verb || "Download"} failed`, label: `${meta.verb || "Download"} failed`, sub: meta.errMsg || t.error || "", frac: null, error: true };
    if (t._flourish || t.status === "done")
      return { pill: meta.verb === "Removing" ? "✓ Removed" : "✓ Added", label: meta.verb === "Removing" ? "Removed" : "Added", sub: name || "", frac: 1 };
    // running. A removal re-bakes the REMAINING regions from cache (one combined
    // archive — no re-download), so don't present it as a download/import: label
    // it "Removing…". Charts being added go through the genuine download + bake.
    const removing = meta.verb === "Removing";
    if (t.phase === "download") {
      const total = t.total || t.cells || 0;
      const frac = total ? t.done / total : null;
      if (removing)
        return { pill: `Removing ${name || ""}`.trim(), label: "Removing region…", sub: "rebuilding charts", frac };
      const mb = meta.bytes ? ` · ${fmtMB(meta.bytes)}` : "";
      return { pill: `⬇ ${name || "Charts"}`, label: "Downloading from NOAA", sub: `${t.cell || ""}${total ? ` · ${Math.min(t.done + 1, total)} of ${total}` : ""}${mb}`, frac };
    }
    const frac = t.total ? t.done / t.total : null;
    const tiles = t.total ? `${(t.done || 0).toLocaleString()} / ${t.total.toLocaleString()} tiles` : "preparing";
    if (removing)
      return { pill: `Removing ${name || ""}`.trim(), label: "Removing region…", sub: `rebuilding · ${tiles}`, frac };
    return { pill: `Importing ${name || "charts"}`, label: "Importing charts…", sub: tiles, frac };
  }

  // Project `_task` to the persistent pill + (when the drawer's open) the
  // progress card. The pill is visible whatever the drawer is doing; tapping it
  // opens the region (or the Charts drawer).
  _renderTaskUI() {
    const d = this._taskDisplay();
    const el = this.shadowRoot.getElementById("dlpill");
    if (el) {
      if (!d) { el.hidden = true; el.innerHTML = ""; }
      else {
        const pct = d.frac != null ? ` · ${Math.round(d.frac * 100)}%` : "";
        el.hidden = false;
        el.classList.toggle("error", !!d.error);
        el.innerHTML = `<span class="dlp-spin"></span><span class="dlp-txt">${d.pill}${pct}</span>`;
        el.onclick = () => { const n = (this._taskMeta || {}).name; if (n) this.openRegion(n); else this.openCharts(); };
      }
    }
    this._setProgress(d ? { label: d.label, sub: d.sub, frac: d.frac } : null);
    // Keep the open region's action buttons in step with the job state (a
    // running job disables Download/Remove); the detail has no text input to
    // disturb, so a re-render is safe.
    if (this._region && this._section === "charts" && this._drawerOpen()) {
      const cb = this.shadowRoot.getElementById("charts-body");
      if (cb) this._renderRegionDetail(cb);
    }
    // A running download means charts are inbound — don't show the empty-state
    // welcome over the map (and restore it if the task failed with no coverage).
    this.updateEmptyState();
  }

  // Group catalog cells into NOAA's official ENC regions (the catalog `rg`
  // numbers). A cell joins every region in its `rg` list, so a region holds the
  // complete set NOAA ships for that area (no centroid partition that splits a
  // bay). Each region's MAP EXTENT comes from its LOCAL cells (coastal and
  // finer); the wide-area overview/general cells span whole oceans and would
  // balloon the box.
  _buildRegions() {
    const byNum = new Map(); // rg number -> region
    for (const c of this._catalog) {
      if (!Array.isArray(c.bb) || c.bb.length !== 4) continue;
      const local = bandForScale(c.s) !== "overview" && bandForScale(c.s) !== "general";
      for (const num of (c.rg || [])) {
        let r = byNum.get(num);
        if (!r) {
          const [name, coast] = NOAA_REGIONS[num] || ["Region " + num, "Other"];
          r = { num, name, coast, cells: [], bb: null };
          byNum.set(num, r);
        }
        r.cells.push(c.n);
        if (local) {
          const [w, s, e, n] = c.bb;
          if (!r.bb) r.bb = [w, s, e, n];
          else r.bb = [Math.min(r.bb[0], w), Math.min(r.bb[1], s), Math.max(r.bb[2], e), Math.max(r.bb[3], n)];
        }
      }
    }
    const regions = [...byNum.values()];
    for (const r of regions) if (!r.bb && r.cells.length) {
      const c = this._byName.get(r.cells[0]); if (c && c.bb) r.bb = [...c.bb];
    }
    this._regions = regions.filter((r) => r.cells.length).sort((a, b) => a.num - b.num);
  }

  // A region's map extent: the union bbox of its LOCAL (coastal-and-finer) cells
  // — the area drawn/framed on the map. (The region's overview/general cells
  // span whole oceans, so they're excluded from the box.) Falls back to a small
  // box if a region somehow has only coarse cells.
  _regionFootprint(region) {
    return region.bb || null;
  }

  // The Charts drawer body: the region list, or (once one is picked) that
  // region's detail. This is the ONE chart surface — browse, download, and view
  // existing charts all live here, organised by region.
  renderCharts() {
    const el = this.shadowRoot.getElementById("charts-body");
    if (!el) return;
    this.shadowRoot.getElementById("dtitle").textContent = this._region ? this._region.name : "Charts";
    if (this._region) return this._renderRegionDetail(el);
    el.innerHTML = `
      <input id="region-search" class="region-search" type="search" placeholder="Search a region…" autocomplete="off" spellcheck="false">
      <div id="region-list" class="region-list"></div>
      <details class="import-more">
        <summary>Import from a file</summary>
        <div id="drop" class="drop">Drop a <code>.zip</code>, <code>.000</code> or <code>.pmtiles</code> here, or<br><button id="pick" class="btn" style="margin-top:6px">Choose files…</button></div>
        <input id="file" type="file" accept=".zip,.000,.pmtiles" multiple hidden>
        <div id="import-log" class="muted"></div>
        <div id="archive-list"></div>
      </details>`;
    const si = el.querySelector("#region-search");
    si.oninput = () => this._renderRegionList(si.value);
    this._wireImport();
    this._renderRegionList("");
  }

  _renderRegionList(q) {
    const el = this.shadowRoot.getElementById("region-list");
    if (!el) return;
    const needle = (q || "").trim().toLowerCase();
    const all = (this._regions || []).filter((r) => !needle || r.name.toLowerCase().includes(needle) || r.coast.toLowerCase().includes(needle));
    const row = (r) => {
      const inst = this._dlRegions.has(r.num);
      const dot = `<span class="rdot ${inst ? "full" : "none"}"></span>`;
      const meta = inst ? "downloaded" : "available";
      return `<button class="region-row" data-region="${r.name}">${dot}<span class="region-name">${r.name}</span><span class="region-meta">${meta}</span></button>`;
    };
    let html = "";
    const downloaded = all.filter((r) => this._dlRegions.has(r.num)).sort((a, b) => a.name.localeCompare(b.name));
    if (downloaded.length) html += `<div class="region-group">Downloaded</div>` + downloaded.map(row).join("");
    for (const coast of COAST_ORDER) {
      const rs = all.filter((r) => r.coast === coast && !this._dlRegions.has(r.num)).sort((a, b) => a.name.localeCompare(b.name));
      if (rs.length) html += `<div class="region-group">${coast}</div>` + rs.map(row).join("");
    }
    el.innerHTML = html || `<div class="region-empty">No regions match “${q}”.</div>`;
    el.querySelectorAll(".region-row").forEach((b) => (b.onclick = () => this.openRegion(b.dataset.region)));
  }

  // Open a region: outline its extent on the map, frame it, show its detail.
  openRegion(name) {
    const r = (this._regions || []).find((x) => x.name === name);
    if (!r) return;
    this._region = r;
    // make sure we're on the Charts surface (e.g. when opened from the pill)
    this._section = "charts";
    this.shadowRoot.querySelectorAll(".panel").forEach((p) => p.classList.toggle("sel", p.dataset.panel === "charts"));
    this.shadowRoot.getElementById("empty").hidden = true;
    this.setDrawerOpen(true);
    this._frameRegion(r);
    this.renderCharts();
  }

  _frameRegion(r) {
    if (!this._map) return;
    // Highlight (and frame to) the region's coarse-cell footprint — the same
    // area whose cells the download grabs, so "what's highlighted" == "what's
    // downloaded".
    const bb = this._regionFootprint(r);
    if (!bb) return;
    const [w, s, e, n] = bb;
    const src = this._map.getSource("focus");
    if (src) src.setData({ type: "FeatureCollection", features: [{ type: "Feature", properties: {}, geometry: { type: "Polygon", coordinates: [[[w, s], [e, s], [e, n], [w, n], [w, s]]] } }] });
    this._map.fitBounds([[w, s], [e, n]], { padding: 50, duration: 600 });
  }

  _renderRegionDetail(el) {
    const r = this._region;
    // A region downloads as ONE NOAA bundle zip (every scale together), so this
    // is whole-region, not a per-band pick. Show the chart count + per-band
    // breakdown for context.
    const per = {};
    let count = 0;
    for (const n of r.cells) {
      const c = this._byName.get(n); if (!c) continue;
      const b = bandForScale(c.s); (per[b] ??= { count: 0 }).count++; count++;
    }
    const breakdown = BANDS.filter((b) => per[b]).map((b) =>
      `<span class="band-chip" data-band="${b}"><span class="sw" style="background:${BAND_COLOR[b]}"></span>${BAND_LABEL[b]} (${per[b].count})</span>`).join("");
    const installed = this._dlRegions.has(r.num);
    const busy = this._taskRunning(); // a download/remove is already in flight
    const status = installed ? `<div class="region-status">✓ ${r.name} is downloaded</div>` : "";
    const actions = installed
      ? `<button class="linkbtn danger" id="region-remove"${busy ? " disabled" : ""}>Remove this region from device</button>`
      : `<button class="add-dl" id="region-dl"${busy ? " disabled" : ""}>${busy ? "Downloading…" : `⬇ Download ${count} chart${count !== 1 ? "s" : ""}`}</button>`;
    el.innerHTML = `
      <div class="add-head"><button id="region-back" class="btn">← All regions</button></div>
      ${status}
      <p class="add-hint">The whole region downloads together (every chart scale), straight from NOAA.</p>
      <div class="band-row">${breakdown}</div>
      <div class="add-sel">${actions}</div>`;
    el.querySelector("#region-back").onclick = () => { this._region = null; this._clearFocus(); this.renderCharts(); };
    el.querySelector("#region-dl")?.addEventListener("click", () => this.downloadRegion());
    el.querySelector("#region-remove")?.addEventListener("click", () => this.removeRegion());
  }

  // Download the open region: add it to the installed-region set and provision
  // the union via NOAA's per-region bundle zips (one big, authoritative download
  // per region, server-side). The server re-bakes from cached zips, so adding a
  // region doesn't re-download the ones you already have.
  async downloadRegion() {
    const r = this._region;
    if (!r) return;
    if (!await this._ensureAgreed()) return; // NOAA ENC User Agreement gate
    // One pmtiles per region: provision ONLY the new region (the server bakes
    // just it; already-baked regions are a no-op).
    await this._startProvisionRegions([r.num], { name: r.name, verb: "Downloading" });
  }

  // Region numbers the user has downloaded — authoritative from the manifest.
  _installedRegions() { return new Set(this._dlRegions); }

  // Remove a region = delete its own archive (DELETE /api/charts/<NN>) and
  // re-apply the remaining set. No re-bake, no download — instant.
  async removeRegion() {
    const r = this._region;
    if (!r) return;
    if (!this._dlRegions.has(r.num) || this._taskRunning()) return;
    this._taskMeta = { name: r.name, verb: "Removing" };
    this._task = { kind: "remove", status: "running", phase: "import", done: 0, total: 0 };
    this._renderTaskUI();
    try {
      const res = await fetch(`api/charts/${r.num}`, { method: "DELETE" });
      const j = await res.json().catch(() => ({}));
      if (!res.ok || !j.ok) throw new Error(j.error || `HTTP ${res.status}`);
    } catch (e) {
      console.error("[remove]", e);
      this._task = { kind: "remove", status: "error", error: "delete" };
      this._taskMeta = { name: r.name, verb: "Removing", errMsg: "Couldn’t remove region" };
      this._renderTaskUI();
      this._clearTaskSoon(3000);
      return;
    }
    await this._loadManifest();
    await this._applyArchives();
    this.updateEmptyState();
    this._assessCoverage();
    if (this._section === "charts" && this._drawerOpen()) this.renderCharts();
    this._task = { status: "done", _flourish: true };
    this._renderTaskUI();
    this._clearTaskSoon(1200);
  }

  // Start a background provision of the given NOAA region numbers (POST
  // {regions:[…]}); progress comes from polling GET /api/tasks.
  async _startProvisionRegions(regions, meta) {
    if (this._taskRunning()) return; // one provision job at a time (server is single-flight)
    this._taskMeta = meta || null;
    this._task = { kind: "provision", status: "running", phase: "download", done: 0, total: regions.length, cells: regions.length, cell: "" };
    this._renderTaskUI();
    try {
      const res = await fetch("api/provision", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ regions }),
      });
      const j = await res.json().catch(() => ({}));
      if (!res.ok || !j.ok) throw new Error(j.error || `HTTP ${res.status}`);
    } catch (e) {
      console.error("[provision]", e);
      this._task = { kind: "provision", status: "error", error: "start" };
      this._taskMeta = { ...(meta || {}), errMsg: "Is the chartplotter server running?" };
      this._renderTaskUI();
      this._clearTaskSoon(3500);
      return;
    }
    this._startPolling();
  }

  // Remove ALL regions at once (DELETE /api/charts), then reflect empty by
  // re-applying the now-empty manifest — no reload needed.
  async _deleteAllCharts(name) {
    if (this._taskRunning()) return;
    this._taskMeta = { name, verb: "Removing" };
    this._task = { kind: "remove", status: "running", phase: "import", done: 0, total: 0 };
    this._renderTaskUI();
    try {
      const res = await fetch("api/charts", { method: "DELETE" });
      const j = await res.json().catch(() => ({}));
      if (!res.ok || !j.ok) throw new Error(j.error || `HTTP ${res.status}`);
    } catch (e) {
      console.error("[remove]", e);
      this._task = { kind: "remove", status: "error", error: "delete" };
      this._taskMeta = { name, verb: "Removing", errMsg: "Couldn’t remove charts" };
      this._renderTaskUI();
      this._clearTaskSoon(3000);
      return;
    }
    await this._loadManifest();
    await this._applyArchives();
    this.updateEmptyState();
    this._assessCoverage();
    if (this._section === "charts" && this._drawerOpen()) this.renderCharts();
    this._task = { status: "done", _flourish: true };
    this._renderTaskUI();
    this._clearTaskSoon(1200);
  }

  saveView() {
    const c = this._map.getCenter();
    try { localStorage.setItem(LS_VIEW, JSON.stringify({ center: [c.lng, c.lat], zoom: this._map.getZoom() })); } catch {}
  }

  // The map's region-highlight layer (outline of the region open in the drawer).
  addCatalogOverlay(map) {
    map.addSource("focus", { type: "geojson", data: { type: "FeatureCollection", features: [] } });
    map.addLayer({ id: "focus-fill", type: "fill", source: "focus", paint: { "fill-color": "#1565c0", "fill-opacity": 0.12 } });
    map.addLayer({ id: "focus-line", type: "line", source: "focus", paint: { "line-color": "#1565c0", "line-width": 2.5 } });
  }

  // A cell is "installed" when locally imported (OPFS) OR its NOAA region is
  // downloaded (one pmtiles per region — so region membership IS installed-ness).
  stateOf(name) {
    if (this._installed.has(name)) return "installed";
    const c = this._byName.get(name);
    if (c && Array.isArray(c.rg) && c.rg.some((n) => this._dlRegions.has(n))) return "installed";
    if (this._archive.has(name)) return "archive";
    return "catalog";
  }

  refreshBoxes() { /* region-centric UI: no per-cell overlay to refresh */ }

  // (legacy) focus a single chart cell — kept for reference; superseded by
  // openRegion's region highlight.
  focusChart(name) {
    const c = this._byName.get(name);
    if (!c || !this._map || !Array.isArray(c.bb) || c.bb.length !== 4) return;
    this._focused = name;
    const [w, s, e, n] = c.bb;
    const src = this._map.getSource("focus");
    if (src) src.setData({ type: "FeatureCollection", features: [{ type: "Feature", properties: {}, geometry: { type: "Polygon", coordinates: [[[w, s], [e, s], [e, n], [w, n], [w, s]]] } }] });
    this._map.fitBounds([[w, s], [e, n]], { padding: 70, maxZoom: 13, duration: 700 });
    this._showChartPill(c, [(w + e) / 2, n]);
    this.shadowRoot.querySelectorAll(".chart-card").forEach((el) => el.classList.toggle("focus", el.dataset.name === name));
  }

  // Info pill (map popup) for a focused chart. Inline-styled so it renders
  // correctly inside the renderer's shadow DOM (where the popup is attached).
  _showChartPill(c, lngLat) {
    const maplibregl = window.maplibregl;
    if (!maplibregl || !this._map) return;
    if (this._chartPopup) this._chartPopup.remove();
    const band = bandForScale(c.s), f = freshness(c.d);
    const fc = { current: ["#e4f5ea", "#1f7a36"], aging: ["#fbf0d8", "#8a6000"], stale: ["#fbe3e1", "#c0392b"] }[f.cls] || ["#eef1f4", "#7a828b"];
    const dot = `display:inline-block;width:9px;height:9px;border-radius:50%;background:${BAND_COLOR[band]};margin-right:5px;vertical-align:-1px`;
    const ed = c.e ? `Ed ${c.e}/${c.u ?? 0} · ` : "";
    const html = `<div style="font:13px/1.4 system-ui,sans-serif;min-width:160px">
      <div style="font-weight:600;margin-bottom:2px">${c.l || c.n}</div>
      <div style="color:#6b7280;font-size:12px">${c.n} · 1:${(c.s || 0).toLocaleString()} · ${BAND_LABEL[band]}</div>
      <div style="margin-top:5px;font-size:12px;color:#6b7280"><span style="${dot}"></span>${ed}issued ${fmtIssue(c.d)} <span style="background:${fc[0]};color:${fc[1]};font-size:10.5px;font-weight:600;padding:1px 7px;border-radius:10px">${f.label}</span></div>
    </div>`;
    this._chartPopup = new maplibregl.Popup({ closeButton: true, closeOnClick: false, maxWidth: "300px", offset: 12 })
      .setLngLat(lngLat).setHTML(html).addTo(this._map);
    this._chartPopup.on("close", () => this._clearFocus());
  }

  // Clear the focus highlight + pill (and the list emphasis).
  _clearFocus() {
    this._focused = null;
    const src = this._map && this._map.getSource("focus");
    if (src) src.setData({ type: "FeatureCollection", features: [] });
    if (this._chartPopup) { this._chartPopup.remove(); this._chartPopup = null; }
    this.shadowRoot.querySelectorAll(".chart-card.focus").forEach((el) => el.classList.remove("focus"));
  }

  // On view change: hold the map at the 1:MIN_DETAIL_SCALE scale floor (so charts
  // never magnify into blocky overzoom, berthing included) and refresh the
  // lower-right coverage panel. The cap is the SAME scale everywhere — uniform
  // zoom — and just resolves to a different zoom level per latitude.
  _assessCoverage() {
    if (this._addMode) return; // picking owns the map
    this._applyScaleFloor();
    this._renderCoverageCells();
  }

  // Cap the map's max zoom at the 1:MIN_DETAIL_SCALE scale for the current centre
  // latitude — a consistent scale floor rather than a per-location data cap.
  _applyScaleFloor() {
    if (!this._map) return;
    const mz = maxZoomForScaleFloor(this._map.getCenter().lat);
    if (Math.abs(this._map.getMaxZoom() - mz) > 1e-3) this._map.setMaxZoom(mz);
  }

  // List the catalog cells intersecting the current viewport in the coverage
  // panel, grouped by band (finest first). Downloaded cells show plain; cells
  // that aren't downloaded are flagged and tap-to-download (jump to their NOAA
  // region). This is the same surface that makes overscale obvious: zoom past
  // your downloaded detail and the finer in-view cells here light up as missing.
  _renderCoverageCells() {
    const el = this.shadowRoot.getElementById("cov-cells");
    if (!el || !this._map) return;
    if (!this._catalog.length) { el.innerHTML = ""; return; }
    const b = this._map.getBounds();
    const vw = b.getWest(), ve = b.getEast(), vs = b.getSouth(), vn = b.getNorth();
    const inView = (bb) => Array.isArray(bb) && bb.length === 4 && !(bb[2] < vw || bb[0] > ve || bb[3] < vs || bb[1] > vn);
    const byBand = {};
    for (const c of this._catalog) {
      if (!inView(c.bb)) continue;
      (byBand[bandForScale(c.s)] ??= []).push(c);
    }
    let html = "", total = 0;
    for (const band of [...BANDS].reverse()) { // berthing -> overview (finest first)
      const cells = byBand[band];
      if (!cells || !cells.length) continue;
      cells.sort((a, z) => a.n.localeCompare(z.n));
      total += cells.length;
      const missing = cells.filter((c) => this.stateOf(c.n) !== "installed").length;
      const chips = cells.map((c) => {
        const have = this.stateOf(c.n) === "installed";
        const hint = `${c.l || c.n} · 1:${(c.s || 0).toLocaleString()} · ${have ? "downloaded" : "tap to download"}`;
        return `<button class="cov-cell${have ? "" : " missing"}" data-name="${c.n}" title="${hint}">${c.n}</button>`;
      }).join("");
      const head = `${BAND_LABEL[band]} · ${cells.length} chart${cells.length !== 1 ? "s" : ""}${missing ? ` · ${missing} to download` : ""}`;
      // One pill per in-view band (label + count, ↓N if some aren't downloaded);
      // hover/tap opens the cell list with its download actions.
      html += `<div class="sb-band-wrap">` +
        `<button class="sb-band${missing ? " has-missing" : ""}" style="--bc:${BAND_COLOR[band]}">` +
        `<span class="sb-dot"></span>${BAND_LABEL[band]}<span class="sb-ct">${cells.length}</span>` +
        `${missing ? `<span class="sb-miss">↓${missing}</span>` : ""}</button>` +
        `<div class="band-pop"><div class="band-pop-h">${head}</div><div class="band-pop-cells">${chips}</div></div>` +
        `</div>`;
    }
    el.innerHTML = total ? html : `<span class="cov-empty">No charts cover this view.</span>`;
    el.querySelectorAll(".cov-cell").forEach((btn) => {
      const name = btn.dataset.name;
      btn.onclick = (e) => {
        e.stopPropagation();
        if (btn.classList.contains("missing")) this._downloadCellRegion(name); else this.focusChart(name);
      };
    });
    // Tap a band pill to pin its popup open (hover handles desktop); tapping
    // another pill or elsewhere (see onReady) closes it.
    el.querySelectorAll(".sb-band").forEach((btn) => {
      btn.onclick = (e) => {
        e.stopPropagation();
        const wrap = btn.parentElement, wasOpen = wrap.classList.contains("open");
        el.querySelectorAll(".sb-band-wrap.open").forEach((w) => w.classList.remove("open"));
        if (!wasOpen) wrap.classList.add("open");
      };
    });
  }

  // A not-downloaded in-view cell was tapped: open its NOAA region in the Charts
  // drawer (the region bundle that covers it) so it can be downloaded. Falls back
  // to the Charts list if the cell's region can't be resolved.
  _downloadCellRegion(name) {
    const c = this._byName.get(name);
    for (const num of (c && c.rg) || []) {
      const reg = (this._regions || []).find((r) => r.num === num);
      if (reg) return this.openRegion(reg.name);
    }
    this.openCharts();
  }

  // -- import (drop a .zip / .000, unzip in-browser → OPFS) ----------------
  async openFiles(fileList) {
    const log = this.shadowRoot.getElementById("import-log");
    const rawInstalled = [];
    for (const file of fileList) {
      const lower = file.name.toLowerCase();
      try {
        if (lower.endsWith(".zip")) {
          const cells = cellEntries(await readCentralDirectory(file));
          let added = 0;
          for (const rec of cells) {
            this._archive.set(rec.name, { blob: file, entry: rec.base, updates: rec.updateCount });
            this._selected.add(rec.name);
            added++;
          }
          log.textContent = `${file.name}: ${added} cell(s) found`;
        } else if (lower.endsWith(".000")) {
          // Raw cell: persist it; it gets baked into the archive below.
          const name = file.name.replace(/\.000$/i, "");
          await this.installCell(name, new Uint8Array(await file.arrayBuffer()));
          rawInstalled.push(name);
          log.textContent = `imported ${name}`;
        } else if (lower.endsWith(".pmtiles")) {
          // A prebaked archive — add it to the loaded coverage (reads only header
          // + directory; tiles stream on demand from the File). Persist in the
          // BACKGROUND so a multi-GB file doesn't block the map on the IndexedDB copy.
          await this._plotter.addArchive(file);
          this._markArchive({ type: "blob" });
          log.textContent = `loaded ${file.name}`;
          this.closeDrawer();
          archivePut(file).catch((e) => console.warn("[archive] persist failed (too large for IndexedDB?)", e));
        } else {
          log.textContent = `skipped ${file.name} (need .zip, .000 or .pmtiles)`;
        }
      } catch (err) {
        console.error(err);
        log.textContent = `${file.name}: ${err.message}`;
      }
    }
    this.updateEmptyState();
    this.renderArchiveList();
    // Dropping raw cells is a complete action — bake them in, then show them.
    if (rawInstalled.length) { await this.rebakeArchive(); this.launchInto(rawInstalled); }
  }

  toggleSelect(name) {
    if (this._selected.has(name)) this._selected.delete(name);
    else this._selected.add(name);
    this.refreshBoxes();
    this.renderArchiveList();
  }

  // -- geo search (offline, over catalog titles) ---------------------------
  doSearch(q) {
    const el = this.shadowRoot.getElementById("search-results");
    if (!el) return;
    const needle = q.trim().toLowerCase();
    if (needle.length < 2) { el.hidden = true; el.innerHTML = ""; this._searchHits = []; return; }
    const hits = [];
    for (const c of this._catalog) {
      if (!Array.isArray(c.bb) || c.bb.length !== 4) continue;
      if ((c.l || "").toLowerCase().includes(needle) || c.n.toLowerCase().includes(needle)) {
        hits.push(c);
        if (hits.length >= 60) break;
      }
    }
    // Coarser charts first — a name like "Chesapeake" should land you on the
    // overview, not an arbitrary harbour inset.
    hits.sort((a, b) => (b.s || 0) - (a.s || 0));
    this._searchHits = hits.slice(0, 8);
    el.innerHTML = this._searchHits.length
      ? this._searchHits.map((c, i) => `<div class="sr-item${i === 0 ? " sel" : ""}" data-i="${i}">
          <div class="t">${c.l || c.n}</div><div class="s">${c.n} · 1:${(c.s || 0).toLocaleString()}</div></div>`).join("")
      : `<div class="sr-item"><span class="muted">No matches</span></div>`;
    el.hidden = false;
    el.querySelectorAll(".sr-item[data-i]").forEach((d) => (d.onmousedown = (e) => { e.preventDefault(); this.gotoSearchHit(+d.dataset.i); }));
  }

  gotoSearchHit(i) {
    const c = (this._searchHits || [])[i];
    if (!c || !this._map) return;
    this._map.fitBounds([[c.bb[0], c.bb[1]], [c.bb[2], c.bb[3]]], { padding: 80, maxZoom: 13, duration: 800 });
    const el = this.shadowRoot.getElementById("search-results");
    if (el) el.hidden = true;
    const si = this.shadowRoot.getElementById("search-input");
    if (si) { si.value = ""; si.blur(); }
  }

  // Drive the drawer's progress card. Pass null to hide it.
  _setProgress(p) {
    const r = this.shadowRoot;
    const wrap = r.querySelector(".progwrap");
    if (!wrap) return;
    if (!p) { wrap.hidden = true; return; }
    wrap.hidden = false;
    r.getElementById("prog-label").textContent = p.label || "";
    const bar = r.getElementById("prog");
    if (p.frac == null) bar.removeAttribute("value"); else bar.value = p.frac;
    r.getElementById("prog-pct").textContent = p.frac == null ? "" : `${Math.round(p.frac * 100)}%`;
    r.getElementById("prog-sub").textContent = p.sub || "";
  }

  // Frame the map to the combined extent of the given catalog cells.
  _frameCells(names) {
    let W = Infinity, S = Infinity, E = -Infinity, N = -Infinity, any = false;
    for (const n of names) {
      const c = this._byName.get(n);
      if (c && Array.isArray(c.bb) && c.bb.length === 4) {
        W = Math.min(W, c.bb[0]); S = Math.min(S, c.bb[1]); E = Math.max(E, c.bb[2]); N = Math.max(N, c.bb[3]); any = true;
      }
    }
    if (any && this._map) this._map.fitBounds([[W, S], [E, N]], { padding: 60, maxZoom: 14, duration: 800 });
  }

  // Frame to the union bounds of the installed region archives (from the manifest).
  _frameRegionArchives(regions) {
    let W = Infinity, S = Infinity, E = -Infinity, N = -Infinity, any = false;
    for (const x of regions || []) {
      const b = x.bounds;
      if (Array.isArray(b) && b.length === 4) {
        W = Math.min(W, b[0]); S = Math.min(S, b[1]); E = Math.max(E, b[2]); N = Math.max(N, b[3]); any = true;
      }
    }
    if (any && this._map) this._map.fitBounds([[W, S], [E, N]], { padding: 60, maxZoom: 14, duration: 800 });
  }

  async importSelected() {
    const names = [...this._selected].filter((n) => this._archive.has(n));
    if (!names.length) return;
    const imported = [];
    let done = 0;
    for (const name of names) {
      this._setProgress({ label: "Importing charts", sub: `${name} · ${done + 1} of ${names.length}`, frac: done / names.length });
      try {
        const { blob, entry } = this._archive.get(name);
        const bytes = await extractEntry(blob, entry);
        await this.installCell(name, bytes); // persist only
        this._archive.delete(name);
        this._selected.delete(name);
        imported.push(name);
      } catch (err) {
        console.error("[import]", name, err);
        this._setProgress({ label: "Importing charts", sub: `${name}: ${err.message}`, frac: done / names.length });
      }
      done++;
    }
    this._setProgress({ label: `Imported ${imported.length} chart${imported.length > 1 ? "s" : ""}`, sub: "Baking tiles next…", frac: 1 });
    this.updateEmptyState();
    this.renderCharts();
    this.refreshBoxes();
    this.renderArchiveList();
    // New cells stored → bake everything into one .pmtiles, then frame them.
    if (imported.length) { await this.rebakeArchive(); this.launchInto(imported); }
    else setTimeout(() => this._setProgress(null), 1200);
  }

  // Bake every installed cell into ONE static .pmtiles (the bake-once path),
  // persist it, and point the renderer at it. Shows tile-bake progress.
  async rebakeArchive() {
    const names = [...this._installed];
    if (!names.length) return;
    this._setProgress({ label: "Importing charts…", sub: `${names.length} chart${names.length > 1 ? "s" : ""}`, frac: 0 });
    try {
      const bytes = await this._plotter.bakePmtiles(names, (p) => {
        this._setProgress({ label: "Importing charts…", sub: `${p.done.toLocaleString()} / ${p.total.toLocaleString()} tiles`, frac: p.total ? p.done / p.total : null });
      });
      const blob = new Blob([bytes], { type: "application/octet-stream" });
      await this._plotter.addArchive(blob); // render first (header + dir only)
      this._markArchive({ type: "blob" });
      archivePut(blob).catch((e) => console.warn("[archive] persist failed", e)); // background
      this._setProgress({ label: `Imported ${names.length} chart${names.length > 1 ? "s" : ""}`, sub: "Ready", frac: 1 });
    } catch (e) {
      console.error("[bake]", e);
      this._setProgress({ label: "Import failed", sub: e.message, frac: null });
    }
    setTimeout(() => this._setProgress(null), 1500);
  }

  // Import = PERSIST ONLY. We just store the cell bytes; the worker bakes tiles
  // from them on demand (and caches the tiles to disk). Nothing is held in RAM
  // per installed cell, so installing the whole catalog is fine.
  async installCell(name, bytes) {
    await this._store.put(name, bytes);
    this._installed.add(name);
  }

  // Close the drawer and frame the map to the combined extent of `names` (their
  // catalog cell boxes). Tiles for the new view bake on demand.
  launchInto(names) {
    if (!this._map) return;
    this.closeDrawer();
    let W = Infinity, S = Infinity, E = -Infinity, N = -Infinity, any = false;
    for (const n of names) {
      const c = this._byName.get(n);
      if (c && Array.isArray(c.bb) && c.bb.length === 4) {
        W = Math.min(W, c.bb[0]); S = Math.min(S, c.bb[1]);
        E = Math.max(E, c.bb[2]); N = Math.max(N, c.bb[3]); any = true;
      }
    }
    if (any) this._map.fitBounds([[W, S], [E, N]], { padding: 60, maxZoom: 14, duration: 800 });
  }

  async removeChart(name) {
    // NOAA-provisioned charts are removed a whole region at a time (one pmtiles
    // per region — see removeRegion), so this only handles locally-imported
    // (OPFS) cells: drop it and re-bake the in-browser archive.
    await this._store.remove(name);
    this._installed.delete(name);
    await this.rebakeArchive();
    this.updateEmptyState();
    this.renderCharts();
    this.refreshBoxes();
    this._assessCoverage();
  }

  // -- settings ------------------------------------------------------------
  applyScheme(name) {
    this._scheme = name;
    this._plotter.setScheme(name);
    localStorage.setItem(LS_SCHEME, name);
  }

  applyMariner(patch) {
    this._mariner = { ...this._mariner, ...patch };
    // Every mariner setting is an instant client-side restyle/filter (no
    // re-bake), so just apply the changed key(s) and persist.
    try { this._plotter.setMariner(patch); }
    catch (e) { console.warn(e); }
    localStorage.setItem(LS_MARINER, JSON.stringify(this._mariner));
    // Switching units relabels + reconverts the depth fields (still in metres
    // under the hood), so redraw the form.
    if ("depthUnit" in patch) this.renderSettings();
  }

  // -- chrome / panels -----------------------------------------------------
  renderChrome() {
    const r = this.shadowRoot;
    r.innerHTML = `
      <style>
        :host { display:block; position:relative; width:100%; height:100%; font:13px/1.4 system-ui,sans-serif;
          --drawer-w:clamp(340px, 33%, 560px); }
        /* The map sits right of the 56px rail; when the drawer flies out it shrinks
           to clear the drawer rather than being overlaid. */
        #map { position:absolute; inset:0 0 0 56px; transition:left .2s; }
        #map.with-drawer { left:calc(56px + var(--drawer-w)); }
        #map chart-plotter { width:100%; height:100%; }
        .btn { cursor:pointer; border:1px solid #aaa; background:#fff; border-radius:6px; padding:6px 10px; font:inherit; }
        .btn:hover { background:#f0f0f0; }
        /* persistent left rail — the sidebar's docked spine (drawer flies out from it) */
        #rail { position:absolute; left:0; top:0; bottom:0; width:56px; z-index:7; background:#fff; border-right:1px solid #e2e2e2;
          box-shadow:1px 0 5px rgba(0,0,0,.07); display:flex; flex-direction:column; align-items:center; gap:4px; padding-top:10px; }
        #rail .ri { width:48px; min-height:48px; border:none; background:none; border-radius:11px; cursor:pointer; color:#5a626b;
          display:flex; flex-direction:column; align-items:center; justify-content:center; gap:3px; padding:7px 0;
          transition:background .12s, color .12s; }
        #rail .ri:hover { background:#eef1f4; color:#1565c0; }
        #rail .ri.on { background:#1565c0; color:#fff; }
        #rail .ri svg { width:21px; height:21px; display:block; }
        #rail .ri .cap { font-size:9.5px; font-weight:500; letter-spacing:.02em; }
        #rail .spacer { flex:1; }
        /* band filter chips (Add charts view) + on-map box-select rectangle */
        .band-chip { display:inline-flex; align-items:center; gap:6px; cursor:pointer; border:1px solid #ddd; border-radius:16px;
          padding:4px 10px; font-size:12px; background:#fff; user-select:none; }
        .band-chip .sw { width:11px; height:11px; border-radius:3px; }
        .band-chip.off { opacity:.38; }
        .box-sel { position:absolute; z-index:5; border:2px solid #1565c0; background:rgba(21,101,192,.12); pointer-events:none; }
        /* charts panel: action header + "your charts" cards */
        .charts-actions { display:flex; gap:8px; margin-bottom:10px; }
        .cta { flex:1; background:#1565c0; color:#fff; border:none; border-radius:8px; padding:11px 12px; font:inherit;
          font-weight:600; cursor:pointer; display:inline-flex; align-items:center; justify-content:center; gap:7px; }
        .cta:hover { background:#1257a8; }
        .cta svg { width:17px; height:17px; }
        .upd { display:inline-flex; align-items:center; gap:6px; white-space:nowrap; }
        .charts-summary { color:#7a828b; font-size:12px; margin:0 0 12px; }
        .charts-empty { text-align:center; color:#8a8a8a; padding:26px 10px; }
        .chart-card { display:flex; align-items:flex-start; gap:10px; padding:11px 0; border-bottom:1px solid #ededed; }
        .chart-card .cc-dot { width:10px; height:10px; border-radius:3px; flex:none; margin-top:3px; }
        .chart-card .cc-main { flex:1; min-width:0; }
        .chart-card .cc-title { font-weight:600; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
        .chart-card .cc-meta { color:#868d95; font-size:12px; margin-top:1px; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
        .chart-card .cc-edition { font-size:12px; color:#9aa0a8; margin-top:4px; display:flex; align-items:center; gap:7px; flex-wrap:wrap; }
        .chart-card .cc-actions { flex:none; display:flex; align-items:center; gap:4px; }
        .cc-btn { border:1px solid #d4d8dd; background:#fff; color:#5a626b; border-radius:7px; width:30px; height:30px; cursor:pointer;
          font-size:14px; display:inline-flex; align-items:center; justify-content:center; }
        .cc-btn:hover { background:#f0f3f6; color:#1565c0; border-color:#b9c0c8; }
        .cc-btn.cc-rm:hover { color:#c0392b; border-color:#e2b6b1; background:#fdeceb; }
        /* freshness pill */
        .fresh { font-size:10.5px; font-weight:600; padding:1px 8px; border-radius:10px; }
        .fresh.current { background:#e4f5ea; color:#1f7a36; }
        .fresh.aging { background:#fbf0d8; color:#8a6000; }
        .fresh.stale { background:#fbe3e1; color:#c0392b; }
        .chart-card.focus { background:#eef4fb; box-shadow:inset 3px 0 0 #1565c0; }
        .chart-card.clickable { cursor:pointer; }
        .chart-card.clickable:hover { background:#f4f7fa; }
        /* in-drawer "Add charts" view */
        .add-head { display:flex; align-items:center; justify-content:space-between; margin-bottom:6px; }
        .add-head strong { font-size:15px; }
        .add-hint { color:#7a828b; font-size:12px; line-height:1.5; margin:0 0 12px; }
        #charts-add .band-row { display:flex; flex-wrap:wrap; gap:6px; margin-bottom:12px; }
        .add-tools { display:flex; gap:8px; margin-bottom:4px; }
        .add-tools .tool { flex:1; border:1px solid #cfcfcf; background:#fff; border-radius:8px; padding:9px; font:inherit; font-size:13px; cursor:pointer; }
        .add-tools .tool:hover { background:#f4f7fa; }
        .add-tools .tool.on { background:#1565c0; color:#fff; border-color:#1565c0; }
        .add-sel { border-top:1px solid #ededed; margin-top:14px; padding-top:14px; }
        .add-sel .empty { color:#9aa0a8; font-size:13px; text-align:center; padding:6px 0; }
        .add-sel .sel-line { display:flex; align-items:center; justify-content:space-between; margin-bottom:10px; font-weight:600; }
        .add-clear { background:none; border:none; color:#1565c0; cursor:pointer; font:inherit; }
        .add-dl { display:block; width:100%; box-sizing:border-box; background:#1565c0; color:#fff; border:none;
          border-radius:8px; padding:11px; font:inherit; font-weight:600; cursor:pointer; }
        .add-dl:hover { background:#1257a8; }
        .add-dl:disabled { background:#9fb6cf; cursor:default; }
        .add-dl:disabled:hover { background:#9fb6cf; }
        .linkbtn:disabled { color:#9aa0a6; cursor:default; text-decoration:none; }
        /* region browser */
        .region-search { width:100%; box-sizing:border-box; border:1px solid #cfcfcf; border-radius:8px; padding:9px 12px; font:inherit; margin-bottom:10px; }
        .region-search:focus { outline:none; border-color:#1565c0; }
        .region-list { display:flex; flex-direction:column; }
        .region-group { font-size:11px; text-transform:uppercase; letter-spacing:.05em; color:#9098a0; font-weight:700; margin:12px 0 4px; }
        .region-row { display:flex; align-items:center; gap:9px; width:100%; text-align:left;
          border:none; background:none; border-bottom:1px solid #ededed; padding:10px 4px; font:inherit; cursor:pointer; }
        .region-row:hover { background:#f4f7fa; }
        .region-row .region-name { font-weight:600; color:#2a2f35; flex:1; min-width:0; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
        .region-row .region-meta { flex:none; color:#9aa0a8; font-size:12px; }
        .rdot { flex:none; width:9px; height:9px; border-radius:50%; box-shadow:inset 0 0 0 1.5px #c2c8cf; }
        .rdot.full { background:#1f9d4d; box-shadow:none; }
        .rdot.partial { background:#f0a500; box-shadow:none; }
        .rdot.none { background:transparent; }
        .region-empty { color:#9aa0a8; text-align:center; padding:20px; }
        .region-title { margin:4px 0 2px; font-size:16px; }
        .region-status { background:#e4f5ea; color:#1f7a36; font-weight:600; font-size:12.5px; padding:6px 10px; border-radius:8px; margin:2px 0 4px; }
        .linkbtn { background:none; border:none; color:#1565c0; cursor:pointer; font:inherit; padding:8px 0; display:block; }
        .linkbtn.danger { color:#c0392b; }
        /* persistent in-flight download/import pill (bottom-centre) */
        #dlpill { position:absolute; bottom:42px; left:50%; transform:translateX(-50%); z-index:7; display:inline-flex; align-items:center;
          gap:9px; background:#1565c0; color:#fff; border:none; border-radius:22px; padding:8px 16px; font:inherit; font-size:13px; font-weight:600;
          cursor:pointer; box-shadow:0 4px 16px rgba(0,0,0,.28); }
        #dlpill[hidden] { display:none; }
        #dlpill.error { background:#c0392b; }
        #dlpill .dlp-spin { width:13px; height:13px; border:2px solid rgba(255,255,255,.4); border-top-color:#fff; border-radius:50%; animation:dlspin .8s linear infinite; }
        #dlpill.error .dlp-spin { display:none; }
        @keyframes dlspin { to { transform:rotate(360deg); } }
        /* chart info pill (map popup when focusing a chart from the list) */
        .chart-pill { font:13px/1.4 system-ui,sans-serif; min-width:170px; }
        .chart-pill .cp-title { font-weight:600; margin-bottom:2px; }
        .chart-pill .cp-meta { color:#6b7280; font-size:12px; }
        .chart-pill .cp-ed { margin-top:5px; display:flex; align-items:center; gap:6px; flex-wrap:wrap; font-size:12px; color:#6b7280; }
        /* settings */
        .set-section { margin:0 0 22px; }
        .set-section > h3 { font-size:11px; text-transform:uppercase; letter-spacing:.05em; color:#9098a0; margin:0 0 4px; font-weight:700; }
        .set-row { display:flex; align-items:center; justify-content:space-between; gap:18px; padding:10px 0; border-bottom:1px solid #ededed; }
        .set-row:last-child { border-bottom:none; }
        .set-row .lbl { display:flex; flex-direction:column; min-width:0; }
        .set-row .lbl .t { font-weight:500; }
        .set-row .lbl .d { font-size:12px; color:#9a9a9a; margin-top:1px; }
        .set-row .ctl { flex:none; display:flex; align-items:center; gap:6px; }
        .set-row .ctl input[type=number] { width:58px; text-align:right; border:1px solid #cfcfcf; border-radius:6px; padding:5px 7px; font:inherit; }
        .set-row .ctl .unit { color:#9a9a9a; font-size:12px; width:14px; }
        .set-row .ctl select { border:1px solid #cfcfcf; border-radius:6px; padding:5px 8px; font:inherit; background:#fff; }
        /* toggle switch */
        .switch { position:relative; width:38px; height:22px; display:inline-block; flex:none; }
        .switch input { opacity:0; width:0; height:0; }
        .switch .sl { position:absolute; inset:0; background:#cdd2d8; border-radius:22px; cursor:pointer; transition:.15s; }
        .switch .sl:before { content:""; position:absolute; width:16px; height:16px; left:3px; top:3px; background:#fff; border-radius:50%; transition:.15s; box-shadow:0 1px 2px rgba(0,0,0,.3); }
        .switch input:checked + .sl { background:#1565c0; }
        .switch input:checked + .sl:before { transform:translateX(16px); }
        /* segmented control */
        .seg { display:inline-flex; border:1px solid #cfcfcf; border-radius:7px; overflow:hidden; }
        .seg button { border:none; background:#fff; padding:6px 11px; font:inherit; font-size:13px; cursor:pointer; border-left:1px solid #ededed; color:#333; }
        .seg button:first-child { border-left:none; }
        .seg button.sel { background:#1565c0; color:#fff; }
        .seg-multi { display:inline-flex; gap:12px; }
        .seg-multi .chk { display:inline-flex; align-items:center; gap:5px; cursor:pointer; }
        /* Bottom statusbar: live readout (left) · in-view band pills (right). */
        #statusbar { position:absolute; left:56px; right:0; bottom:0; z-index:6; height:30px;
          display:flex; align-items:center; gap:14px; padding:0 12px; box-sizing:border-box;
          background:rgba(255,255,255,.95); border-top:1px solid rgba(0,0,0,.08);
          box-shadow:0 -1px 6px rgba(0,0,0,.07); backdrop-filter:blur(5px);
          font:12px system-ui,sans-serif; color:#2a2f35; transition:left .2s; }
        #statusbar.with-drawer { left:calc(56px + var(--drawer-w)); }
        /* NOAA attribution — a pill DEBOSSED into the chart: faint inset fill +
           inset shadow (pressed-in) with a light bottom bevel, under an engraved
           letterpress text effect, so the whole pill reads as embossed in the map. */
        #noaa-attr { position:absolute; right:12px; bottom:38px; z-index:5; pointer-events:auto;
          font:600 11px/1.4 system-ui,sans-serif; letter-spacing:.01em;
          color:rgba(33,40,48,.6); text-shadow:0 1px 0 rgba(255,255,255,.7);
          background:rgba(255,255,255,.72); border-radius:10px; padding:3px 10px; border:1px solid rgba(0,0,0,.06);
          box-shadow:inset 0 1px 2px rgba(0,0,0,.22), inset 0 -1px 0 rgba(255,255,255,.5), 0 1px 0 rgba(255,255,255,.45); }
        #noaa-attr a, #noaa-attr .attr-link { color:inherit; text-shadow:inherit; cursor:pointer;
          text-decoration:underline; text-decoration-color:rgba(33,40,48,.32); text-underline-offset:2px; }
        #noaa-attr a:hover, #noaa-attr .attr-link:hover { color:rgba(18,24,31,.82); }
        #noaa-attr .attr-link { background:none; border:none; padding:0; font:inherit; }
        /* NOAA ENC user-agreement gate (shown before the first download). */
        .modal { position:absolute; inset:0; z-index:30; display:flex; align-items:center; justify-content:center;
          background:rgba(15,20,26,.55); backdrop-filter:blur(2px); }
        .modal[hidden] { display:none; }
        .modal-card { background:#fff; max-width:520px; width:calc(100% - 40px); max-height:86%; overflow:auto;
          border-radius:12px; padding:20px 22px; box-shadow:0 12px 40px rgba(0,0,0,.3); font:14px/1.5 system-ui,sans-serif; color:#2a2f35; }
        .modal-card h2 { margin:0 0 10px; font-size:18px; }
        .modal-card .agree-body ul { margin:8px 0; padding-left:20px; }
        .modal-card .agree-body li { margin:5px 0; }
        .modal-card a { color:#1565c0; }
        .agree-actions { display:flex; gap:10px; justify-content:flex-end; margin-top:16px; }
        /* Live band·scale·zoom readout (left of the statusbar), one line. Each
           field has a fixed width + tabular figures so the bar never reflows. */
        .sb-readout { flex:none; }
        .sb-readout .hud-main { display:inline-flex; align-items:center; gap:10px; font-weight:600; font-size:12px; white-space:nowrap; font-variant-numeric:tabular-nums; }
        .sb-readout .hud-dot { width:8px; height:8px; border-radius:50%; flex:none; box-shadow:0 0 0 2px rgba(255,255,255,.6); margin-right:-4px; }
        .sb-readout .hud-band { display:inline-block; min-width:62px; }
        .sb-readout .hud-scale { color:#1565c0; display:inline-block; min-width:92px; }
        .sb-readout .hud-z { display:inline-block; min-width:42px; color:#6b7280; }
        /* In-view band pills, right-aligned; each opens a cell-list popup. */
        .sb-bands { display:flex; align-items:center; gap:8px; min-width:0; margin-left:auto; }
        .sb-band-wrap { position:relative; flex:none; }
        .sb-band { display:inline-flex; align-items:center; gap:5px; font:600 11px/1 system-ui,sans-serif; color:#384049;
          background:#fff; border:1px solid rgba(0,0,0,.14); border-radius:13px; padding:4px 9px; cursor:pointer; white-space:nowrap; }
        .sb-band:hover { border-color:#1565c0; }
        .sb-band .sb-dot { width:8px; height:8px; border-radius:50%; background:var(--bc); flex:none; }
        .sb-band .sb-ct { color:#8a9098; font-weight:500; }
        .sb-band .sb-miss { color:#1565c0; font-weight:700; }
        .sb-band.has-missing { border-color:rgba(21,101,192,.5); }
        /* Cell-list popup above a band pill (hover on desktop; tap to pin on touch). */
        .band-pop { display:none; position:absolute; bottom:calc(100% + 6px); right:0; z-index:10;
          background:#fff; border:1px solid rgba(0,0,0,.1); border-radius:9px; padding:8px 9px;
          box-shadow:0 6px 22px rgba(0,0,0,.22); width:max-content; max-width:280px; }
        .band-pop::before { content:""; position:absolute; left:0; right:0; bottom:-6px; height:6px; } /* hover bridge over the gap */
        .sb-band-wrap:hover .band-pop, .band-pop:hover, .sb-band-wrap.open .band-pop { display:block; }
        .band-pop-h { font:600 11px/1.3 system-ui,sans-serif; color:#5a6068; margin-bottom:6px; }
        .band-pop-cells { display:flex; flex-wrap:wrap; gap:4px; max-height:210px; overflow-y:auto; }
        .cov-cell { font:11px/1.6 ui-monospace,SFMono-Regular,Menlo,monospace; padding:0 6px; border-radius:5px;
          border:1px solid rgba(0,0,0,.12); background:#eef1f4; color:#384049; cursor:pointer; }
        .cov-cell:hover { border-color:#1565c0; color:#1565c0; }
        .cov-cell.missing { background:repeating-linear-gradient(45deg,#fff,#fff 4px,#f3f4f6 4px,#f3f4f6 8px);
          color:#8a9098; border-style:dashed; }
        .cov-cell.missing::after { content:" ↓"; color:#1565c0; }
        .cov-cell.missing:hover { color:#1565c0; border-color:#1565c0; }
        .cov-empty { font:12px system-ui,sans-serif; color:#8a9098; }
        #loading { position:absolute; top:12px; left:50%; transform:translateX(-50%); z-index:5; background:rgba(0,0,0,.72);
          color:#fff; border-radius:14px; padding:5px 12px; font-size:12px; box-shadow:0 1px 4px rgba(0,0,0,.3); }
        #drawer { position:absolute; top:0; left:56px; width:var(--drawer-w); height:100%; background:#fafafa;
          box-shadow:2px 0 8px rgba(0,0,0,.25); z-index:6; transform:translateX(calc(-100% - 56px)); transition:transform .2s; display:flex; flex-direction:column; }
        #drawer.open { transform:none; }
        .dhead { display:flex; align-items:center; gap:8px; padding:10px 12px; border-bottom:1px solid #ddd; }
        .dhead strong { flex:1; }
        .body { overflow:auto; padding:12px; flex:1; }
        .panel { display:none; } .panel.sel { display:block; }
        .drop { border:2px dashed #bbb; border-radius:8px; padding:18px; text-align:center; color:#666; margin-bottom:10px; }
        .drop.over { border-color:#1565c0; background:#eef4fb; color:#1565c0; }
        .row { display:flex; align-items:center; gap:8px; padding:4px 0; border-bottom:1px solid #eee; }
        .row .name { font-weight:600; } .row .meta { color:#777; font-size:12px; }
        .grow { flex:1; }
        .muted { color:#888; }
        label.fld { display:block; margin:8px 0; }
        label.fld span { display:inline-block; min-width:135px; }
        input[type=number] { width:64px; }
        /* progress surface (drawer): phase label + percent, bar, detail sub-line */
        .progwrap { margin:4px 0 16px; background:#f1f4f7; border:1px solid #e4e8ec; border-radius:10px; padding:11px 13px; }
        .progwrap .prog-top { display:flex; align-items:baseline; justify-content:space-between; gap:10px; margin-bottom:7px; }
        .progwrap .prog-label { font-weight:600; }
        .progwrap .prog-pct { color:#1565c0; font-weight:600; font-variant-numeric:tabular-nums; }
        .progwrap .prog-sub { margin-top:6px; font-size:12px; }
        progress { width:100%; height:8px; -webkit-appearance:none; appearance:none; border:none; border-radius:5px; overflow:hidden; background:#dde3e9; }
        progress::-webkit-progress-bar { background:#dde3e9; border-radius:5px; }
        progress::-webkit-progress-value { background:#1565c0; border-radius:5px; }
        progress::-moz-progress-bar { background:#1565c0; border-radius:5px; }
        /* collapsible "import from a file" */
        .import-more { margin-top:18px; border-top:1px solid #ededed; padding-top:6px; }
        .import-more > summary { cursor:pointer; color:#5a626b; font-weight:500; padding:6px 0; list-style:none; }
        .import-more > summary::-webkit-details-marker { display:none; }
        .import-more > summary:before { content:"▸ "; color:#9aa0a8; }
        .import-more[open] > summary:before { content:"▾ "; }
        .legend { display:flex; gap:12px; font-size:12px; margin-bottom:10px; flex-wrap:wrap; }
        .legend i { display:inline-block; width:11px; height:11px; border-radius:2px; margin-right:4px; vertical-align:-1px; }
        #empty { position:absolute; inset:0; display:flex; align-items:center; justify-content:center; z-index:4; pointer-events:none; }
        #empty[hidden] { display:none; }
        #empty .card { pointer-events:auto; background:rgba(255,255,255,.97); border-radius:16px; padding:30px 30px 24px; max-width:360px;
          text-align:center; box-shadow:0 8px 34px rgba(0,0,0,.22); }
        #empty .welcome-mark { width:44px; height:44px; margin-bottom:10px; }
        #empty h2 { margin:0 0 8px; font-size:21px; }
        #empty p { color:#5a626b; margin:0 0 18px; line-height:1.5; }
        #empty .welcome-cta { display:inline-flex; align-items:center; gap:8px; width:auto; padding:11px 22px; font-size:15px; }
        #empty .welcome-sub { margin-top:12px; font-size:13px; color:#9098a0; }
        #empty .linkbtn { background:none; border:none; color:#1565c0; cursor:pointer; font:inherit; padding:0; text-decoration:underline; }
        /* geo search */
        #search { position:absolute; top:12px; left:50%; transform:translateX(-50%); z-index:5; width:340px; max-width:44%; }
        #search input { width:100%; box-sizing:border-box; border:1px solid #b3b3b3; border-radius:20px; padding:8px 16px;
          font:inherit; background:#fff; box-shadow:0 1px 4px rgba(0,0,0,.3); outline:none; }
        #search input:focus { border-color:#1565c0; box-shadow:0 1px 6px rgba(21,101,192,.4); }
        #search-results { margin-top:5px; background:#fff; border-radius:12px; box-shadow:0 6px 22px rgba(0,0,0,.25); overflow:hidden; }
        #search-results[hidden] { display:none; }
        .sr-item { padding:8px 16px; cursor:pointer; border-bottom:1px solid #f1f1f1; }
        .sr-item:last-child { border-bottom:none; }
        .sr-item:hover, .sr-item.sel { background:#eef4fb; }
        .sr-item .t { font-weight:600; } .sr-item .s { color:#8a8a8a; font-size:12px; }
      </style>
      <div id="map"></div>
      <div id="rail">
        <button class="ri" id="rail-menu" title="Your charts">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="M12 3 2.5 8 12 13l9.5-5L12 3Z"/><path d="m2.5 12 9.5 5 9.5-5"/><path d="m2.5 16 9.5 5 9.5-5"/></svg>
          <span class="cap">Charts</span>
        </button>
        <button class="ri" id="rail-settings" title="Settings">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1Z"/></svg>
          <span class="cap">Settings</span>
        </button>
      </div>
      <div id="search"><input id="search-input" type="search" placeholder="Search a port or area…" autocomplete="off" spellcheck="false"><div id="search-results" hidden></div></div>
      <div id="statusbar">
        <div id="cov-readout" class="sb-readout"></div>
        <div id="cov-cells" class="sb-bands"></div>
      </div>
      <div id="noaa-attr">
        Data from <a href="${NOAA_ENC_URL}" target="_blank" rel="noopener">NOAA ENC®</a>
        · <button id="attr-terms" class="attr-link" type="button">Terms</button>
        · not for navigation
      </div>
      <div id="agree" class="modal" hidden>
        <div class="modal-card">
          <h2>NOAA ENC® — User Agreement</h2>
          <div class="agree-body">
            <p>NOAA Electronic Navigational Charts (NOAA ENC®) are downloaded directly from NOAA. By continuing you acknowledge that you have read, understood, and accepted NOAA's User Agreement.</p>
            <ul>
              <li><b>Not for navigation.</b> Charts downloaded and baked here are processed for display and are <b>not</b> the official NOAA ENC; they do not meet chart-carriage regulations. Use official, up-to-date charts for navigation.</li>
              <li><b>Updates.</b> NOAA updates ENCs weekly on a best-efforts basis. You are responsible for ensuring you have the current edition and latest updates.</li>
              <li><b>Origin.</b> Charts are sourced from <a href="${NOAA_ENC_URL}" target="_blank" rel="noopener">NOAA Office of Coast Survey</a>. NOAA makes no warranty and assumes no liability for their use.</li>
            </ul>
            <p>Read the full terms: <a href="${NOAA_AGREEMENT_URL}" target="_blank" rel="noopener">NOAA ENC User Agreement</a>.</p>
          </div>
          <div class="agree-actions">
            <button id="agree-decline" class="btn" type="button">Decline</button>
            <button id="agree-accept" class="cta" type="button">Accept &amp; continue</button>
          </div>
        </div>
      </div>
      <button id="dlpill" hidden></button>
      <div id="empty" hidden><div class="card">
        <svg class="welcome-mark" viewBox="0 0 24 24" fill="none" stroke="#1565c0" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="5" r="2"/><path d="M12 7v14M5 12a7 7 0 0 0 14 0M3 12h2m14 0h2M12 21a7 7 0 0 1-5-2m10 0a7 7 0 0 1-5 2"/></svg>
        <h2>Welcome aboard</h2>
        <p>No charts yet. Pick a cruising region and download a pack — official NOAA charts are fetched and baked right here on your machine, ready to use offline.</p>
        <button id="empty-add" class="cta welcome-cta">⚓ Browse chart regions</button>
        <div class="welcome-sub">or <button id="empty-import" class="linkbtn">import from a file</button></div>
      </div></div>
      <div id="drawer">
        <div class="dhead"><strong id="dtitle">Charts</strong><button id="close" class="btn">✕</button></div>
        <div class="body">
          <div class="panel sel" data-panel="charts">
            <!-- shared progress card (download / import), visible in any view -->
            <div class="progwrap" hidden>
              <div class="prog-top"><span id="prog-label" class="prog-label"></span><span id="prog-pct" class="prog-pct"></span></div>
              <progress id="prog" max="1" value="0"></progress>
              <div id="prog-sub" class="prog-sub muted"></div>
            </div>
            <!-- the whole chart UI is one region browser (renderCharts fills this) -->
            <div id="charts-body"></div>
          </div>
          <div class="panel" data-panel="settings">
            <div id="settings-body"></div>
          </div>
        </div>
      </div>`;

    // wiring
    const $ = (id) => r.getElementById(id);
    // The rail is the sidebar's docked spine: its icons pick which drawer section
    // flies out (clicking the active one closes it). "Add charts" / "Update all"
    // live inside the Charts section itself.
    $("rail-menu").onclick = () => this.toggleSection("charts");
    $("rail-settings").onclick = () => this.toggleSection("settings");
    $("close").onclick = () => this.closeDrawer();
    $("empty-add").onclick = () => this.openCharts();
    $("empty-import").onclick = () => { this.openCharts(); const det = r.querySelector(".import-more"); if (det) det.open = true; };
    // NOAA ENC user-agreement gate + attribution "Terms" link.
    $("attr-terms").onclick = () => this._showAgreement();
    $("agree-accept").onclick = () => this._resolveAgreement(true);
    $("agree-decline").onclick = () => this._resolveAgreement(false);

    // Geo search (offline, over the catalog titles).
    const si = $("search-input");
    si.oninput = () => this.doSearch(si.value);
    si.onkeydown = (e) => {
      if (e.key === "Enter") this.gotoSearchHit(0);
      else if (e.key === "Escape") { $("search-results").hidden = true; si.blur(); }
    };
    si.onfocus = () => { if (si.value.trim().length >= 2) this.doSearch(si.value); };
    si.onblur = () => setTimeout(() => { const sr = $("search-results"); if (sr) sr.hidden = true; }, 150);

    this.renderSettings();
  }

  // Rail-icon → drawer section. Clicking the icon of the already-open section
  // closes the drawer; otherwise it opens (or switches) to that section.
  toggleSection(name) {
    const open = this.shadowRoot.getElementById("drawer").classList.contains("open");
    if (open && this._section === name) { this.closeDrawer(); return; }
    this._section = name;
    const r = this.shadowRoot;
    r.querySelectorAll(".panel").forEach((p) => p.classList.toggle("sel", p.dataset.panel === name));
    r.getElementById("dtitle").textContent = name === "settings" ? "Settings" : (this._region ? this._region.name : "Charts");
    if (name === "charts") this.renderCharts();
    this.setDrawerOpen(true);
  }

  closeDrawer() { this.setDrawerOpen(false); }

  // Fly the drawer in/out. The map shrinks to clear it (CSS transition), so once
  // that settles, tell MapLibre to resize its canvas to the new container width.
  setDrawerOpen(open) {
    const r = this.shadowRoot;
    r.getElementById("drawer").classList.toggle("open", open);
    r.getElementById("map").classList.toggle("with-drawer", open);
    r.getElementById("statusbar").classList.toggle("with-drawer", open);
    r.getElementById("rail-menu").classList.toggle("on", open && this._section === "charts");
    r.getElementById("rail-settings").classList.toggle("on", open && this._section === "settings");
    // Closing the drawer drops the open region, so clear its map highlight box.
    if (!open) { this._region = null; this._clearFocus(); }
    setTimeout(() => { if (this._map) this._map.resize(); }, 230);
  }

  // Empty only when there's nothing to show AND nothing to pick: no archive
  // loaded and no hosted districts available.
  updateEmptyState() {
    // Show the welcome whenever nothing actually loaded — keyed off _hasArchive
    // (real coverage), not the district manifest (which may list packs that
    // 404, e.g. a stale charts-index.json). But NOT while a provision is in
    // flight: a running download means charts ARE on the way (the pill shows
    // progress), so the "no charts yet" card would be wrong and overlay the map.
    const el = this.shadowRoot.getElementById("empty");
    if (el) el.hidden = this._hasArchive || !!(this._task && this._task.status === "running");
  }

  renderArchiveList() {
    const el = this.shadowRoot.getElementById("archive-list");
    if (!el) return;
    const names = [...this._archive.keys()].sort();
    if (!names.length) { el.innerHTML = ""; return; }
    const nSel = [...this._selected].filter((n) => this._archive.has(n)).length;
    el.innerHTML = `<h4>From archive (${names.length})</h4>` + names.map((name) => {
      const c = this._byName.get(name);
      const checked = this._selected.has(name) ? "checked" : "";
      return `<label class="row"><input type="checkbox" data-name="${name}" ${checked}>
        <span class="grow"><span class="name">${name}</span> <span class="meta">${c?.l || ""}</span></span></label>`;
    }).join("") +
      `<div style="margin-top:8px"><button id="import-btn" class="btn">Import ${nSel} chart(s)</button></div>`;
    el.querySelectorAll("input[type=checkbox]").forEach((cb) => (cb.onchange = () => this.toggleSelect(cb.dataset.name)));
    const ib = this.shadowRoot.getElementById("import-btn");
    if (ib) ib.onclick = () => this.importSelected();
  }

  // Wire the file-import controls (the import-more <details> is re-rendered with
  // the region list, so its drop/pick handlers are bound here each render).
  _wireImport() {
    const r = this.shadowRoot;
    const file = r.getElementById("file"), drop = r.getElementById("drop"), pick = r.getElementById("pick");
    if (!file || !drop || !pick) return;
    pick.onclick = () => file.click();
    file.onchange = () => { if (file.files.length) this.openFiles(file.files); file.value = ""; };
    drop.ondragover = (e) => { e.preventDefault(); drop.classList.add("over"); };
    drop.ondragleave = () => drop.classList.remove("over");
    drop.ondrop = (e) => { e.preventDefault(); drop.classList.remove("over"); if (e.dataTransfer.files.length) this.openFiles(e.dataTransfer.files); };
  }

  // The settings panel: appearance (colour scheme) + the mariner display
  // settings, laid out as labelled rows / toggles / segmented controls.
  renderSettings() {
    const el = this.shadowRoot.getElementById("settings-body");
    if (!el) return;
    const m = this._mariner;
    const ft = m.depthUnit === "ft";
    // Depth settings are STORED in metres (the renderer's expressions are all
    // metric); the row just displays/accepts feet when imperial, converting on
    // edit (see the input handler below). `defM` is the metric default.
    const depthRow = (key, label, defM) => {
      const v = ft ? Math.round((m[key] ?? defM) * M_TO_FT) : (m[key] ?? defM);
      return `<div class="set-row"><div class="lbl"><span class="t">${label}</span></div>
        <div class="ctl"><input type="number" step="${ft ? "1" : "0.1"}" data-key="${key}" data-depth="1" value="${v}"><span class="unit">${ft ? "ft" : "m"}</span></div></div>`;
    };
    const toggle = (key, label, desc, on) =>
      `<div class="set-row"><div class="lbl"><span class="t">${label}</span>${desc ? `<span class="d">${desc}</span>` : ""}</div>
        <label class="switch"><input type="checkbox" data-key="${key}" ${on ? "checked" : ""}><span class="sl"></span></label></div>`;

    el.innerHTML = `
      <div class="set-section">
        <h3>Appearance</h3>
        <div class="set-row"><div class="lbl"><span class="t">Colour scheme</span></div>
          <div class="ctl"><div class="seg" id="scheme-seg">${SCHEMES.map((s) =>
            `<button data-scheme="${s}" class="${this._scheme === s ? "sel" : ""}">${SCHEME_LABEL[s]}</button>`).join("")}</div></div></div>
      </div>
      <div class="set-section">
        <h3>Depths</h3>
        <div class="set-row"><div class="lbl"><span class="t">Units</span></div>
          <div class="ctl"><div class="seg" id="unit-seg">
            <button data-unit="m" class="${ft ? "" : "sel"}">Metric</button>
            <button data-unit="ft" class="${ft ? "sel" : ""}">Imperial</button></div></div></div>
        ${depthRow("shallowContour", "Shallow contour", 2)}
        ${depthRow("safetyContour", "Safety contour", 10)}
        ${depthRow("deepContour", "Deep contour", 20)}
        ${depthRow("safetyDepth", "Safety depth", 30)}
      </div>
      <div class="set-section">
        <h3>Display</h3>
        <div class="set-row"><div class="lbl"><span class="t">Detail level</span><span class="d">Which feature categories are drawn</span></div>
          <div class="ctl"><div class="seg-multi">
            <label class="chk"><input type="checkbox" data-key="displayBase" ${m.displayBase === false ? "" : "checked"}>Base</label>
            <label class="chk"><input type="checkbox" data-key="displayStandard" ${m.displayStandard === false ? "" : "checked"}>Standard</label>
            <label class="chk"><input type="checkbox" data-key="displayOther" ${m.displayOther ? "checked" : ""}>Other</label></div></div></div>
        <div class="set-row"><div class="lbl"><span class="t">Area boundaries</span></div>
          <div class="ctl"><select data-key="boundaryStyle">${["plain", "symbolized"].map((v) =>
            `<option ${(m.boundaryStyle || "plain") === v ? "selected" : ""}>${v}</option>`).join("")}</select></div></div>
        ${toggle("fourShadeWater", "Four-shade water", "Four depth shades instead of two", m.fourShadeWater !== false)}
        ${toggle("shallowPattern", "Shallow pattern", "Diagonal fill in shallow water", !!m.shallowPattern)}
        ${toggle("showContourLabels", "Contour labels", "Show depth values on contours", !!m.showContourLabels)}
      </div>`;

    el.querySelectorAll("#scheme-seg button").forEach((b) =>
      (b.onclick = () => { this.applyScheme(b.dataset.scheme); this.renderSettings(); }));
    el.querySelectorAll("#unit-seg button").forEach((b) =>
      (b.onclick = () => { if (b.dataset.unit !== (ft ? "ft" : "m")) this.applyMariner({ depthUnit: b.dataset.unit }); }));
    el.querySelectorAll("[data-key]").forEach((inp) => {
      inp.onchange = () => {
        const key = inp.dataset.key;
        let val;
        if (inp.type === "checkbox") val = inp.checked;
        else if (inp.type === "number") {
          val = parseFloat(inp.value);
          // Depth fields are shown in feet when imperial; store back in metres.
          if (inp.dataset.depth && this._mariner.depthUnit === "ft") val = val / M_TO_FT;
        } else val = inp.value;
        this.applyMariner({ [key]: val });
      };
    });
  }
}

function loadJSON(key, fallback) {
  try { return JSON.parse(localStorage.getItem(key)) || fallback; } catch { return fallback; }
}

// Mirror of bake.zig `bandForScale`: a cell's compilation scale → its NOAA band.
function bandForScale(s) {
  const n = s || 0;
  if (n <= 8000) return "berthing";
  if (n <= 32000) return "harbor";
  if (n <= 130000) return "approach";
  if (n <= 500000) return "coastal";
  if (n <= 2300000) return "general";
  return "overview";
}

const COAST_ORDER = ["Northeast", "Mid-Atlantic", "Southeast", "Gulf of Mexico", "Great Lakes", "California", "Pacific Northwest", "Alaska", "Pacific Islands", "Caribbean"];

// NOAA's official ENC regions (catalog `rg` numbers) — name from
// charts.noaa.gov/MCD/images/list.txt, plus a coast bucket for grouping the
// browser list. A region's cells are exactly the catalog cells whose `rg`
// includes its number, so a body of water (e.g. the Chesapeake) is never split.
const NOAA_REGIONS = {
  2: ["Block Island, RI to the Canadian Border", "Northeast"],
  3: ["New York to Nantucket and Cape May, New Jersey", "Northeast"],
  4: ["Chesapeake and Delaware Bays", "Mid-Atlantic"],
  6: ["Norfolk, VA to Florida — The Intracoastal Waterway", "Mid-Atlantic"],
  7: ["Florida East Coast and the Keys", "Southeast"],
  8: ["Florida West Coast and the Keys", "Gulf of Mexico"],
  10: ["Puerto Rico and US Virgin Islands", "Caribbean"],
  12: ["Southern California — Point Arena to Mexican Border", "California"],
  13: ["Lake Michigan", "Great Lakes"],
  14: ["San Francisco to Cape Flattery", "California"],
  15: ["Pacific Northwest — Puget Sound to Canadian Border", "Pacific Northwest"],
  17: ["Mobile, AL to Mexican Border", "Gulf of Mexico"],
  22: ["Lake Superior and Lake Huron", "Great Lakes"],
  24: ["Lake Erie (US Waters)", "Great Lakes"],
  26: ["Lake Ontario (US Waters)", "Great Lakes"],
  30: ["Southeast Alaska", "Alaska"],
  32: ["South Central Alaska", "Alaska"],
  34: ["Alaska — The Aleutians and Bristol Bay", "Alaska"],
  36: ["Alaska — Norton Sound to Beaufort Sea", "Alaska"],
  40: ["Hawaiian Islands", "Pacific Islands"],
};

// The finest band whose source paints at zoom z (Band.zoomRange mins in bake.zig:
// overview 0, general 7, coastal 9, approach 11, harbor 13, berthing 16).
function bandForZoom(z) {
  if (z >= 16) return "berthing";
  if (z >= 13) return "harbor";
  if (z >= 11) return "approach";
  if (z >= 9) return "coastal";
  if (z >= 7) return "general";
  return "overview";
}

// Web-Mercator map scale denominator at zoom z / latitude lat (OGC 0.28mm pixel).
function scaleDenom(z, lat) {
  const mpp = 156543.03392804097 * Math.cos((lat * Math.PI) / 180) / Math.pow(2, z);
  return mpp / 0.00028;
}

// Finest map scale we allow: don't let charts magnify past 1:MIN_DETAIL_SCALE,
// even where berthing data exists (past this it's just blocky overzoom). Inverse
// of scaleDenom — the (fractional) zoom whose scale at `lat` equals the floor.
// Latitude-dependent because 1:4000 is a different zoom at each latitude.
const MIN_DETAIL_SCALE = 4000;
function maxZoomForScaleFloor(lat) {
  const z = Math.log2(156543.03392804097 * Math.cos((lat * Math.PI) / 180) / (0.00028 * MIN_DETAIL_SCALE));
  return Math.max(1, Math.min(18, z));
}

// NOAA ENC freshness from a cell's issue date `d` ("YYYY-MM-DD"). ENCs have no
// hard expiry — this is an age signal (kept current via Notices to Mariners), so
// we grade by how long ago the edition was issued rather than a fixed expiry.
function freshness(d) {
  const t = d ? Date.parse(d + "T00:00:00Z") : NaN;
  if (!isFinite(t)) return { cls: "aging", label: "Age unknown" };
  const months = (Date.now() - t) / (1000 * 60 * 60 * 24 * 30.44);
  if (months < 6) return { cls: "current", label: "Current" };
  if (months < 12) return { cls: "aging", label: "Aging" };
  return { cls: "stale", label: "Out of date" };
}

// "2025-12-09" → "Dec 2025" (UTC, locale month). Falls back to the raw string.
function fmtIssue(d) {
  const t = d ? Date.parse(d + "T00:00:00Z") : NaN;
  if (!isFinite(t)) return d || "unknown date";
  return new Date(t).toLocaleDateString(undefined, { year: "numeric", month: "short", timeZone: "UTC" });
}

// Bytes → a compact "12 MB" / "1.4 MB" string (thin-space before the unit).
function fmtMB(bytes) {
  const mb = (bytes || 0) / (1024 * 1024);
  return (mb < 10 ? mb.toFixed(1) : Math.round(mb)) + " MB";
}

// A scale denominator rounded to 3 significant figures and thousands-grouped.
function fmtScale(d) {
  if (!isFinite(d) || d <= 0) return "—";
  const mag = Math.pow(10, Math.max(0, Math.floor(Math.log10(d)) - 2));
  return (Math.round(d / mag) * mag).toLocaleString();
}

// Copy `text` to the clipboard, returning whether it worked. Prefers the async
// Clipboard API but that needs a secure context (https/localhost) — on a plain
// http LAN origin it's absent, so fall back to a hidden-textarea execCommand,
// which still works there. Returns false only if both paths fail.
async function copyText(text) {
  try {
    if (navigator.clipboard && window.isSecureContext) { await navigator.clipboard.writeText(text); return true; }
  } catch (e) { /* fall through to the legacy path */ }
  try {
    const ta = document.createElement("textarea");
    ta.value = text;
    ta.style.cssText = "position:fixed;top:0;left:0;opacity:0;pointer-events:none";
    document.body.appendChild(ta);
    ta.focus(); ta.select();
    const ok = document.execCommand("copy");
    ta.remove();
    return ok;
  } catch (e) { return false; }
}

// Briefly show `msg` on a button, then restore its original label.
function flashBtn(btn, msg) {
  if (!btn) return;
  const prev = btn.textContent;
  btn.textContent = msg;
  setTimeout(() => { btn.textContent = prev; }, 1400);
}

// Persist the single current .pmtiles archive as a Blob in IndexedDB (works on
// a plain-http LAN device — unlike OPFS, no secure context needed). One slot:
// the most recently baked/uploaded archive, reloaded on boot.
const ARCHIVE_DB = "chartplotter-archive";
function archiveDB() {
  return new Promise((res, rej) => {
    const r = indexedDB.open(ARCHIVE_DB, 1);
    r.onupgradeneeded = () => r.result.createObjectStore("archive");
    r.onsuccess = () => res(r.result);
    r.onerror = () => rej(r.error);
  });
}
async function archivePut(blob) {
  const db = await archiveDB();
  try {
    await new Promise((res, rej) => {
      const tx = db.transaction("archive", "readwrite");
      tx.objectStore("archive").put(blob, "current");
      tx.oncomplete = res;
      tx.onerror = () => rej(tx.error);
    });
  } finally { db.close(); }
}
async function archiveGet() {
  const db = await archiveDB();
  try {
    return await new Promise((res, rej) => {
      const tx = db.transaction("archive", "readonly");
      const rq = tx.objectStore("archive").get("current");
      rq.onsuccess = () => res(rq.result || null);
      rq.onerror = () => rej(rq.error);
    });
  } finally { db.close(); }
}

customElements.define("chart-plotter-app", ChartPlotterApp);
