// <chart-library> — the "Charts library" domain, extracted from the
// <chart-plotter> shell. This element owns the WHOLE inside of the shell's
// charts panel: the Finder-style 3-pane provider→pack→detail drill-down, the
// find-a-chart search, the per-pack coverage preview (its own small MapLibre
// map), the download queue + execution, the User-Charts local-file import, and
// the NOAA ENC user-agreement gate. The shell keeps the drawer/tab chrome and
// mounts this inside #charts-body; this element inherits the shell's --ui-*
// theme tokens through the shadow boundary (same pattern as <pick-report>).
//
// It talks to the server ONLY through the injected ChartService (api) and posts
// ALL progress to the injected NotificationCenter (notify) — never to shell DOM.
// On any install/uninstall/enable/disable/import it dispatches "charts-changed";
// the shell listens and reconciles the main map (its _renderInstalledSets). It
// never imports the shell (no circular dependency).
//
//   const el = document.createElement("chart-library");
//   el.configure({ dl, api, notify, store, assets });
//   el.show("noaa");          // make the charts UI active + render
//   el.refresh();             // re-render (shell re-opened the section / state changed)
//   el.busy                   // true while a download/import/uninstall runs
//
// Events (CustomEvent, bubbles + composed):
//   "charts-changed"          — after any install/uninstall/enable/disable/import
//   "chart-focus" {bounds}    — ask the main map to fly to [w,s,e,n]
//   "chart-import-archive" {file} — hand a dropped .pmtiles to the shell's
//                               client-side archive path (plotter-coupled, so it
//                               stays in the shell; see chartplotter.mjs).

import { esc, fmtIssue } from "../lib/util.mjs";
import { readCentralDirectory, cellEntries, extractEntry } from "../data/zip-import.mjs";

// NOAA ENC User Agreement acceptance (localStorage). Exported so the shell can
// share the same key if it ever needs to read it.
export const LS_AGREE = "chartplotter:enc-agreement";
// NOAA's ENC distribution pages + the User Agreement that must be displayed and
// accepted before downloading ENCs (charts.noaa.gov/ENCs/ENCs.shtml §3).
export const NOAA_ENC_URL = "https://www.charts.noaa.gov/ENCs/ENCs.shtml";
export const NOAA_AGREEMENT_URL = "https://www.charts.noaa.gov/ENCs/ENC_Agreement.shtml";

// Chart packs = U.S. Coast Guard districts. NOAA publishes one ENC bundle per
// district (NNCGD_ENCs.zip on charts.noaa.gov/ENCs/ENCs.shtml) and tags every
// catalog cell with its district (the `cg` field), so a pack is exactly the set
// of cells with a given `cg` — and downloading one is a single zip fetch. The
// nine districts below are the ones NOAA actually ships (2/3/4/6/10/12/15/16
// were disestablished long ago); `region`/`blurb` are friendly labels for the
// card UI. Order is roughly east→Gulf→Lakes→west→Pacific→Alaska. Exported because
// the shell still uses it (_reattachName / _rebuildAllPerBand).
export const DISTRICTS = [
  { cg: 1, name: "1st District", region: "Northeast", blurb: "Maine south to northern New Jersey" },
  { cg: 5, name: "5th District", region: "Mid-Atlantic", blurb: "New Jersey to North Carolina · Chesapeake & Delaware bays" },
  { cg: 7, name: "7th District", region: "Southeast", blurb: "South Carolina, Georgia, eastern Florida · Puerto Rico & USVI" },
  { cg: 8, name: "8th District", region: "Gulf Coast", blurb: "Western Florida to Texas · the Western Rivers" },
  { cg: 9, name: "9th District", region: "Great Lakes", blurb: "All five Great Lakes & the St. Lawrence Seaway" },
  { cg: 11, name: "11th District", region: "California", blurb: "The California coast" },
  { cg: 13, name: "13th District", region: "Pacific Northwest", blurb: "Oregon & Washington" },
  { cg: 14, name: "14th District", region: "Pacific Islands", blurb: "Hawaii, Guam & American Samoa" },
  { cg: 17, name: "17th District", region: "Alaska", blurb: "All of Alaska" },
];

// Charts-panel styles, lifted verbatim from the shell <style> (the rules that
// only ever applied inside #charts-body). The element has its own shadow DOM, so
// a handful of generic helpers (.btn, .muted, .row, .grow) are duplicated here
// rather than relying on the shell's sheet — they no longer cross the boundary.
const STYLE = `
  :host { display:block; }
  .btn { cursor:pointer; border:1px solid var(--ui-border-strong); background:var(--ui-surface); border-radius:6px; padding:6px 10px; font:inherit; color:var(--ui-text); }
  .btn:hover { background:var(--ui-hover); }
  .add-hint { color:var(--ui-text-dim); font-size:12px; line-height:1.5; margin:0 0 12px; }
  .pack-search { width:100%; box-sizing:border-box; border:1px solid var(--ui-border-strong); border-radius:8px; padding:9px 12px; font:inherit; margin-bottom:10px; background:var(--ui-surface); color:var(--ui-text); }
  .pack-search:focus { outline:none; border-color:var(--ui-accent); }
  @keyframes dlspin { to { transform:rotate(360deg); } }
  /* chart download: Finder-style 3-pane drill-down */
  .miller { display:flex; align-items:stretch; border:1px solid var(--ui-border-2); border-radius:10px; overflow:hidden; min-height:300px; max-height:min(62vh,560px); margin:2px 0 12px; }
  .mcol { flex:0 0 26%; min-width:0; overflow-y:auto; border-right:1px solid var(--ui-border-2); padding:6px; }
  .mcol:nth-child(2) { flex:0 0 32%; }
  .mcol.mcol-detail { flex:1 1 0; border-right:none; padding:12px; }
  .mcol-h { font-size:11px; font-weight:700; color:var(--ui-text); padding:1px 6px 0; }
  .mcol-head { position:sticky; top:0; background:var(--ui-surface); padding:4px 0 7px; margin-bottom:2px; border-bottom:1px solid var(--ui-border-2); z-index:1; }
  .mcol-meta { font-size:10.5px; color:var(--ui-text-faint); padding:1px 6px 0; line-height:1.35; }
  .m-row { display:flex; align-items:center; gap:8px; padding:8px; border-radius:7px; cursor:pointer; transition:background .1s; }
  .m-row:hover { background:var(--ui-hover); }
  .m-row:focus-visible { outline:none; box-shadow:inset 0 0 0 2px var(--ui-accent); }
  .m-row.sel { background:var(--ui-accent); }
  .m-row.sel .m-name, .m-row.sel .m-sub, .m-row.sel .m-chev { color:var(--ui-accent-text); }
  .m-row.sel .m-badge.on { background:rgba(255,255,255,.25); color:var(--ui-accent-text); }
  .m-row.dim { opacity:.4; }
  .m-row.match { background:rgba(21,101,192,.10); }
  .m-row.match.sel { background:var(--ui-accent); }
  .m-info { flex:1; min-width:0; display:flex; flex-direction:column; gap:1px; }
  .m-name { font-weight:600; font-size:13px; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
  .m-sub { color:var(--ui-text-faint); font-size:11px; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
  .m-chev { flex:none; color:var(--ui-text-faint); font-size:16px; }
  .m-badge { flex:none; font-size:9.5px; font-weight:700; text-transform:uppercase; letter-spacing:.03em; padding:2px 7px; border-radius:10px; display:inline-flex; align-items:center; gap:5px; }
  .m-badge.on { background:#e4f5ea; color:#1f7a36; }
  .m-badge.off { background:var(--ui-surface-2); color:var(--ui-text-faint); }
  .m-badge.dl { background:color-mix(in srgb, var(--ui-accent) 16%, transparent); color:var(--ui-accent); }
  .m-badge.queued { background:var(--ui-surface-2); color:var(--ui-text-dim); }
  .m-row.dim .m-badge { opacity:.7; }
  .m-empty { color:var(--ui-text-faint); font-size:12px; padding:14px 8px; text-align:center; line-height:1.5; }
  /* detail pane — real OSM preview map with the pack's coverage outlined */
  .prev-map { width:100%; height:260px; border:1px solid var(--ui-border-2); border-radius:8px; background:var(--ui-surface-2); overflow:hidden; }
  .prev-map canvas { border-radius:8px; }
  .m-detail-body { padding:12px 2px 2px; }
  .m-detail-title { font-weight:700; font-size:15px; }
  .m-detail-sub { color:var(--ui-text-dim); font-size:12px; line-height:1.45; margin-top:3px; }
  .m-detail-meta { color:var(--ui-text-faint); font-size:11.5px; font-variant-numeric:tabular-nums; margin-top:5px; }
  .m-detail-act { margin-top:12px; display:flex; gap:8px; flex-wrap:wrap; }
  .pk-btn.danger { color:#c0392b; }
  .pk-btn.danger:hover { background:#fdeceb; border-color:#e2b6b1; }
  .pk-btn { display:inline-flex; align-items:center; justify-content:center; gap:7px; border:none; background:var(--ui-accent); color:var(--ui-accent-text); border-radius:7px; padding:8px 14px; font:inherit; font-size:13px; font-weight:600; cursor:pointer; white-space:nowrap; }
  .pk-btn:hover { background:var(--ui-accent-hover); }
  .pk-btn:disabled { background:#9fb6cf; cursor:default; }
  /* Downloading now: greyed, spinner, no hover lift. Queued: muted, waiting. */
  .pk-btn.downloading, .pk-btn.downloading:hover { background:#9fb6cf; cursor:default; }
  .pk-btn.queued, .pk-btn.queued:hover { background:var(--ui-surface-2); color:var(--ui-text-dim); border:1px solid var(--ui-border-strong); }
  .pk-btn.ghost { background:var(--ui-surface); color:var(--ui-text-dim); border:1px solid var(--ui-border-strong); }
  .pk-btn.ghost:hover { background:#fdeceb; color:#c0392b; border-color:#e2b6b1; }
  .pk-btn.mini { padding:5px 9px; font-size:11.5px; }
  /* Spinner used in the Downloading button + list badge. */
  .pk-spin { width:12px; height:12px; flex:none; border-radius:50%;
    border:2px solid rgba(255,255,255,.45); border-top-color:#fff; animation:dlspin .8s linear infinite; }
  .m-badge.dl .pk-spin { width:9px; height:9px; border-width:2px; border-color:color-mix(in srgb, var(--ui-accent) 35%, transparent); border-top-color:var(--ui-accent); }
  @media (prefers-reduced-motion: reduce) { .pk-spin { animation-duration:2s; } }
  /* NOAA data freshness footer */
  .data-fresh { color:var(--ui-text-faint); font-size:11.5px; text-align:center; line-height:1.5; padding:14px 0 4px; border-top:1px solid var(--ui-border-2); margin-top:4px; }
  /* import drop zone + archive list */
  .drop { border:2px dashed var(--ui-border-strong); border-radius:8px; padding:18px; text-align:center; color:var(--ui-text-dim); margin-bottom:10px; }
  .drop.over { border-color:var(--ui-accent); background:var(--ui-hover); color:var(--ui-accent); }
  .row { display:flex; align-items:center; gap:8px; padding:4px 0; border-bottom:1px solid var(--ui-border-2); }
  .row .name { font-weight:600; } .row .meta { color:var(--ui-text-dim); font-size:12px; }
  .grow { flex:1; }
  .muted { color:var(--ui-text-dim); }
  /* NOAA ENC user-agreement gate (shown before the first download). */
  .modal { position:fixed; inset:0; z-index:30; display:flex; align-items:center; justify-content:center;
    background:rgba(15,20,26,.55); backdrop-filter:blur(2px); }
  .modal[hidden] { display:none; }
  .modal-card { background:var(--ui-surface); max-width:520px; width:calc(100% - 40px); max-height:86%; overflow:auto;
    border-radius:12px; padding:20px 22px; box-shadow:0 12px 40px rgba(0,0,0,.3); font:14px/1.5 system-ui,sans-serif; color:var(--ui-text); }
  .modal-card h2 { margin:0 0 10px; font-size:18px; }
  .modal-card .agree-body ul { margin:8px 0; padding-left:20px; }
  .modal-card .agree-body li { margin:5px 0; }
  .modal-card a { color:var(--ui-accent); }
  .agree-actions { display:flex; gap:10px; justify-content:flex-end; margin-top:16px; }
  .cta { background:var(--ui-accent); color:var(--ui-accent-text); border:none; border-radius:8px; padding:11px 12px; font:inherit;
    font-weight:600; cursor:pointer; display:inline-flex; align-items:center; justify-content:center; gap:7px; }
  .cta:hover { background:var(--ui-accent-hover); }
`;

export class ChartLibrary extends HTMLElement {
  constructor() {
    super();
    if (!this.shadowRoot) this.attachShadow({ mode: "open" });
    // Injected via configure(); guarded so a stray render before configure no-ops.
    this._dl = null;       // ChartDownloader (NOAA catalogue/discovery)
    this._api = null;      // ChartService (server import/bake + pack registry)
    this._notify = null;   // NotificationCenter (task progress + banners)
    this._store = null;    // ChartStore (OPFS local cell store, for User imports)
    this._assets = "./";

    // Selection state for the 3-pane drill-down.
    this._selProvider = null; // "noaa" | "ienc" | "user"
    this._selPack = null;     // set key of the selected pack
    this._cellQuery = "";     // the find-a-chart search box
    this._activeDistrict = null; // CG district whose preview is highlighted

    // Inland-ENC catalogue cache (loaded lazily via api.iencCatalog()).
    this._ienc = undefined;
    this._iencPromise = null;

    // Download queue: one pack at a time; clicking Download on another while one
    // runs enqueues it. `_activeDownloadKey` is the set being baked now; `_dlQueue`
    // holds the waiting pack objects. Each detail button reflects its state.
    this._activeDownloadKey = null;
    this._dlQueue = [];
    this._uninstalling = false; // an uninstall job is in flight (busy gate)

    // The installed/disabled set state, kept in sync from the shell via show()/
    // refresh() reading the ChartService registry. Pure render input.
    this._installedSets = new Set();
    this._disabled = new Set();
    this._installed = new Set(); // installed cell names (for the NOAA pack counts)

    // NOAA ENC agreement acceptance (persisted).
    this._agreed = localStorage.getItem(LS_AGREE) === "1";
    this._agreeResolve = null;

    // Local-file import scratch (the User-Charts path).
    this._archive = new Map();  // cell name -> {blob, entry, updates} from opened zips
    this._selected = new Set(); // cell names ticked for import

    this._previewMap = null;    // the detail-pane mini coverage map (MapLibre)
    this._prod = false;         // prod (prebaked) build: import-only Library
    this._active = false;       // is the charts UI currently shown?
  }

  connectedCallback() {
    this.shadowRoot.innerHTML = `<style>${STYLE}</style><div id="body"></div><div id="agree-host"></div>`;
  }

  disconnectedCallback() { this.teardownPreview(); }

  // Tear down the detail-pane OSM preview map (called when the drawer closes).
  teardownPreview() {
    if (this._previewMap) { try { this._previewMap.remove(); } catch (e) { /* ignore */ } this._previewMap = null; }
  }

  // Inject dependencies (call once after creation). `prod` flips the Library to
  // import-only (no NOAA download/region picker), matching the shell's prod mode.
  configure({ dl, api, notify, store, assets, prod } = {}) {
    this._dl = dl || null;
    this._api = api || null;
    this._notify = notify || null;
    this._store = store || null;
    if (assets) this._assets = assets;
    this._prod = !!prod;
    return this;
  }

  // Make the charts UI active for a provider id ("noaa"|"ienc"|"user") and render.
  show(provider) {
    this._active = true;
    if (provider) { this._selProvider = provider; this._selPack = null; }
    this.refresh();
  }

  // Re-render the panel (shell re-opened the charts section, or state changed).
  // Pulls a fresh installed/disabled snapshot from the server first so the pack
  // badges + counts reflect what's actually baked — the shell's boot reconcile
  // updates the MAP, but this component keeps its own registry snapshot. Renders
  // the current snapshot immediately (instant structure) then again once synced.
  async refresh() {
    this.render();
    await this._syncRegistry();
    if (this._active) this.render();
  }

  // True while a download / import / uninstall job is running (the shell's dev
  // panel + task gating read this).
  get busy() { return !!this._activeDownloadKey || this._dlQueue.length > 0 || this._uninstalling; }

  // -- dependency proxies (mirror the shell's old getters) ------------------
  get _catalog() { return this._dl ? this._dl.catalog : []; }
  get _byName() { return this._dl ? this._dl.byName : new Map(); }
  get _catalogDate() { return this._dl ? this._dl.catalogDate : ""; }

  // Dispatch the public events (bubbles + composed so they cross the shadow edge).
  _emit(name, detail) {
    this.dispatchEvent(new CustomEvent(name, { detail, bubbles: true, composed: true }));
  }
  // Notify the shell that installed/enabled state changed (it reconciles the map).
  _changed() { this._emit("charts-changed"); }

  // Refresh the installed/disabled/installed-cell snapshot from the server, so the
  // panel's pack badges + counts are current. Called before a (re-)render that
  // depends on it. Best-effort: a transient failure keeps the last snapshot.
  async _syncRegistry() {
    if (!this._api) return;
    try {
      const packs = await this._api.packs();
      this._installedSets = new Set(packs.map((p) => p.name));
      this._disabled = new Set(packs.filter((p) => !p.enabled).map((p) => p.name));
    } catch (e) { /* keep last */ }
    try { const cells = await this._api.cells(); if (cells) this._installed = cells; } catch (e) { /* keep last */ }
  }

  // -- chart packs (Coast Guard districts) ----------------------------------
  // Discovery helpers delegate to the downloader (this._dl).
  _districtCellNames(cg) { return this._dl.districtCellNames(cg); }
  _districtStat(cg) { return this._dl.districtStat(cg); }
  _districtZipUrl(cg) { return this._dl.districtZipUrl(cg); }

  // The providers shown in pane 1.
  _providers() {
    return [
      { id: "noaa", name: "NOAA", sub: "Coast Guard districts" },
      { id: "ienc", name: "Inland ENC", sub: "USACE waterways" },
      { id: "user", name: "User Charts", sub: "Import your own" },
    ];
  }

  _providerName(id) { const p = this._providers().find((x) => x.id === id); return p ? p.name : id; }

  // The packs for a provider: {key (set name), kind, title, sub, installed, …}.
  _providerPacks(id) {
    const sets = this._installedSets || new Set();
    if (id === "noaa") {
      return DISTRICTS.map((d) => {
        const { total, bytes } = this._districtStat(d.cg);
        if (!total) return null;
        return { key: "noaa-d" + d.cg, kind: "noaa", cg: d.cg, title: d.region, sub: `${total.toLocaleString()} charts · ~${Math.round(bytes / 1e6)} MB`, installed: sets.has("noaa-d" + d.cg) };
      }).filter(Boolean);
    }
    if (id === "ienc") return this._iencPacks() || [];
    // user: locally-imported packs (anything not NOAA/IENC).
    return [...sets].filter((n) => !/^(noaa-d\d+|ienc-)/.test(n)).sort().map((n) => ({ key: n, kind: "user", title: this._setLabel(n), sub: "installed", installed: true }));
  }

  // USACE Inland ENC catalogue (server-fetched + parsed). Cached here once via
  // ChartService. Returns [] on failure.
  async _iencCatalog() {
    if (this._ienc !== undefined) return this._ienc;
    if (!this._iencPromise) this._iencPromise = this._api.iencCatalog();
    this._ienc = await this._iencPromise;
    return this._ienc;
  }

  // IENC packs = one per river (a group of cells), or null until the catalogue
  // loads. Each: {key:"ienc-<river>", kind, title, sub, installed, cells, bbox}.
  _iencPacks() {
    const cells = this._ienc;
    if (!cells) return null;
    const sets = this._installedSets || new Set();
    const byRiver = new Map();
    for (const c of cells) { if (!byRiver.has(c.river)) byRiver.set(c.river, []); byRiver.get(c.river).push(c); }
    return [...byRiver.entries()].sort((a, b) => a[0].localeCompare(b[0])).map(([river, cs]) => {
      const key = "ienc-" + river.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/(^-|-$)/g, "");
      let w = Infinity, s = Infinity, e = -Infinity, n = -Infinity;
      for (const c of cs) { const [cw, cse, ce, cn] = c.bbox; if ([cw, cse, ce, cn].every(Number.isFinite)) { w = Math.min(w, cw); s = Math.min(s, cse); e = Math.max(e, ce); n = Math.max(n, cn); } }
      return { key, kind: "ienc", title: river, sub: `${cs.length} chart${cs.length > 1 ? "s" : ""}`, installed: sets.has(key), cells: cs.map((c) => ({ name: c.name, url: c.url, bbox: c.bbox })), bbox: w <= e ? [w, s, e, n] : null };
    });
  }

  // -- rendering ------------------------------------------------------------
  // The whole charts panel. Prod (prebaked) builds get the import-only Library;
  // otherwise the 3-pane drill-down + search + preview.
  render() {
    const el = this.shadowRoot.getElementById("body");
    if (!el) return;
    if (this._prod) {
      el.innerHTML = `
        <p class="add-hint">Add your own charts — drop a NOAA <code>.zip</code> / <code>.000</code>, or a baked <code>.pmtiles</code>. They're baked right here in your browser and kept offline alongside the prebaked charts.</p>
        <div id="drop" class="drop">Drop a <code>.zip</code>, <code>.000</code> or <code>.pmtiles</code> here, or<br><button id="pick" class="btn" style="margin-top:6px">Choose files…</button></div>
        <input id="file" type="file" accept=".zip,.000,.pmtiles" multiple hidden>
        <div id="import-log" class="muted"></div>
        <div id="archive-list"></div>`;
      this._wireImport();
      return;
    }
    el.innerHTML = `
      ${this._renderPackSearch()}
      <div class="miller">
        ${this._renderProvidersCol()}
        ${this._renderPacksCol()}
        ${this._renderDetailCol()}
      </div>
      ${this._renderDataFreshness()}`;
    this._wirePackSearch();
    this._wirePacks();
    this._wireImport();
    this._renderPreview();
  }

  // Build (or tear down) the detail-pane preview map for the selected pack.
  _renderPreview() {
    const pk = this._selectedPack();
    if (pk && pk.kind !== "user") this._buildPreviewMap(this._packCoverage(pk));
    else if (this._previewMap) { try { this._previewMap.remove(); } catch (e) { /* ignore */ } this._previewMap = null; }
  }

  // Pane 1: providers. With an active search, providers that contain a match are
  // highlighted and the rest dimmed.
  _renderProvidersCol() {
    const sel = this._selProvider || "noaa";
    const hits = this._searchHits();
    const rows = this._providers().map((p) => {
      let cls = sel === p.id ? " sel" : "";
      if (hits) cls += this._providerHasMatch(p.id, hits) ? " match" : " dim";
      return `<div class="m-row${cls}" data-prov="${p.id}" role="button" tabindex="0">
        <span class="m-info"><span class="m-name">${esc(p.name)}</span><span class="m-sub">${esc(p.sub)}</span></span><span class="m-chev">›</span></div>`;
    }).join("");
    return `<div class="mcol"><div class="mcol-h">Source</div>${rows}</div>`;
  }

  // Does a provider contain a search match? NOAA → any matched district; others →
  // a pack whose label matches the raw query.
  _providerHasMatch(id, hits) {
    if (id === "noaa") return hits.size > 0;
    const q = (this._cellQuery || "").trim().toLowerCase();
    return this._providerPacks(id).some((pk) => pk.title.toLowerCase().includes(q) || pk.key.toLowerCase().includes(q));
  }

  // Pane 2: the selected provider's packs.
  _renderPacksCol() {
    const prov = this._selProvider || "noaa";
    const packs = this._providerPacks(prov);
    const hits = this._searchHits();
    const q = (this._cellQuery || "").trim().toLowerCase();
    let rows;
    if (prov === "ienc" && this._ienc === undefined) {
      // Catalogue not loaded yet — fetch it, then refresh just this column.
      if (!this._iencPromise) this._iencCatalog().then(() => { if (this._active && (this._selProvider || "noaa") === "ienc") this._refreshPacksCol(); });
      rows = `<div class="m-empty">Loading inland ENC catalogue…</div>`;
    } else if (prov === "user") rows = packs.length ? packs.map((pk) => this._userPackRow(pk)).join("") : `<div class="m-empty">No imported charts yet — open this to add some.</div>`;
    else if (!packs.length) rows = `<div class="m-empty">${prov === "ienc" ? "No inland ENC packs available." : "Nothing installed."}</div>`;
    else rows = packs.map((pk) => {
      let cls = (this._selPack === pk.key ? " sel" : "") + (pk.installed ? " on" : "");
      let sub = pk.sub;
      if (hits) {
        const hit = pk.cg != null ? (hits.has(pk.cg) ? hits.get(pk.cg) : undefined) : (pk.title.toLowerCase().includes(q) ? null : undefined);
        if (hit === undefined) cls += " dim";
        else { cls += " match"; if (hit) sub = `matches “${esc(hit.l || hit.n)}”`; }
      }
      return `<div class="m-row${cls}" data-pack="${esc(pk.key)}"${pk.cg ? ` data-cg="${pk.cg}"` : ""} role="button" tabindex="0">
        <span class="m-info"><span class="m-name">${esc(pk.title)}</span><span class="m-sub">${sub}</span></span>${this._packBadge(pk.key, pk.installed)}</div>`;
    }).join("");
    return `<div class="mcol">${this._packsHeader(prov)}${rows}</div>`;
  }

  // A user-imported pack row.
  _userPackRow(pk) {
    return `<div class="m-row on${this._selPack === pk.key ? " sel" : ""}" data-pack="${esc(pk.key)}" role="button" tabindex="0">
      <span class="m-info"><span class="m-name">${esc(pk.title)}</span><span class="m-sub">${esc(pk.sub)}</span></span>${this._packBadge(pk.key, true)}</div>`;
  }

  // Status pill for a pack row. A pending/active download takes priority (so you
  // can see at a glance which packs are downloading/queued); otherwise an installed
  // pack shows "Active"/"Disabled", and a plain not-installed pack shows nothing.
  _packBadge(key, installed) {
    if (!installed) {
      const dl = this._packDownloadState(key);
      if (dl === "downloading") return '<span class="m-badge dl"><span class="pk-spin"></span>Downloading</span>';
      if (dl === "queued") return '<span class="m-badge queued">Queued</span>';
      return "";
    }
    return this._disabled.has(key)
      ? '<span class="m-badge off">Disabled</span>'
      : '<span class="m-badge on">Active</span>';
  }

  // Pane-2 header: the provider's name + a one-line description and when its source
  // catalogue was last refreshed.
  _packsHeader(prov) {
    let line = "";
    if (prov === "noaa") line = `U.S. Coast Guard districts${this._catalogDate ? ` · catalogue ${fmtIssue(this._catalogDate)}` : ""} · ${this._catalog.length.toLocaleString()} charts`;
    else if (prov === "ienc") line = "USACE inland waterway ENC";
    else line = "Charts you've imported from a file";
    return `<div class="mcol-head"><div class="mcol-h">${esc(this._providerName(prov))}</div><div class="mcol-meta">${esc(line)}</div></div>`;
  }

  // Pane 3: the selected pack's detail — coverage map + download/remove.
  _renderDetailCol() {
    const key = this._selPack;
    if (!key) {
      if ((this._selProvider || "noaa") === "user") return this._renderImportDetail();
      return `<div class="mcol mcol-detail"><div class="m-empty">Select a chart pack.</div></div>`;
    }
    const busy = this.busy;
    const installed = this._installedSets && this._installedSets.has(key);
    const pk = this._selectedPack();
    // An installed pack not in the current catalogue (e.g. an old set) → remove only.
    if (!pk) {
      return `<div class="mcol mcol-detail"><div class="m-detail-body">
        <div class="m-detail-title">${esc(this._setLabel(key))}${installed ? ' <span class="pl-tick">✓</span>' : ""}</div>
        <div class="m-detail-sub">${esc(key)}</div>
        <div class="m-detail-act"><button class="pk-btn ghost" data-uninstall-set="${esc(key)}"${busy ? " disabled" : ""}>Remove</button></div>
      </div></div>`;
    }
    const disabled = this._disabled.has(key);
    const tick = installed ? (disabled ? ' <span class="m-badge off">Disabled</span>' : ' <span class="m-badge on">Active</span>') : "";
    const act = installed
      ? `<button class="pk-btn ghost" data-${disabled ? "enable" : "disable"}="${esc(key)}"${busy ? " disabled" : ""}>${disabled ? "Enable" : "Disable"}</button>
         <button class="pk-btn ghost danger" data-uninstall-set="${esc(key)}"${busy ? " disabled" : ""}>Remove</button>`
      : this._downloadBtnHtml(key);
    let title, sub, meta;
    if (pk.kind === "noaa") {
      const d = DISTRICTS.find((x) => x.cg === pk.cg);
      title = d ? d.region : pk.title;
      sub = d ? `${esc(d.name)} · ${esc(d.blurb)}` : "";
      meta = `${pk.sub} · outlined area below is the coverage`;
    } else if (pk.kind === "user") {
      title = pk.title || this._setLabel(key);
      sub = "Imported charts — baked on the server, kept under User Charts.";
      meta = "";
    } else { // ienc
      title = `${pk.title} River`;
      sub = `USACE Inland ENC · ${pk.cells.length} chart${pk.cells.length > 1 ? "s" : ""}`;
      meta = "outlined area below is the coverage";
    }
    // User packs have no coverage map; everything else shows the preview.
    const previewMap = pk.kind === "user" ? "" : `<div id="preview-map" class="prev-map"></div>`;
    return `<div class="mcol mcol-detail">
      ${previewMap}
      <div class="m-detail-body">
        <div class="m-detail-title">${esc(title)}${tick}</div>
        <div class="m-detail-sub">${sub}</div>
        ${meta ? `<div class="m-detail-meta">${esc(meta)}</div>` : ""}
        <div class="m-detail-act">${act}</div>
      </div></div>`;
  }

  // The User-Charts detail: the import drop zone (baked server-side into the
  // "import" pack). Shown when the User Charts provider is open with no pack picked.
  _renderImportDetail() {
    return `<div class="mcol mcol-detail"><div class="m-detail-body">
      <div class="m-detail-title">Import your charts</div>
      <div class="m-detail-sub">Add ENC you already have — a NOAA/IENC exchange-set <code>.zip</code>, individual <code>.000</code> cells, or a baked <code>.pmtiles</code>. They're baked on the server and kept under User Charts.</div>
      <div id="drop" class="drop">Drop a <code>.zip</code>, <code>.000</code> or <code>.pmtiles</code> here, or<br><button id="pick" class="btn" style="margin-top:8px">Choose files…</button></div>
      <input id="file" type="file" accept=".zip,.000,.pmtiles" multiple hidden>
      <div id="import-log" class="muted"></div>
      <div id="archive-list"></div>
    </div></div>`;
  }

  // The coverage GeoJSON for a district: one polygon per catalog cell (its bbox),
  // so the preview map shows the ACTUAL covered area. Returns {fc, bounds}.
  _districtCoverage(cg) {
    const feats = [];
    let w = Infinity, s = Infinity, e = -Infinity, n = -Infinity;
    for (const c of this._catalog) {
      if (c.cg !== cg || !Array.isArray(c.bb) || c.bb.length !== 4) continue;
      const [cw, cs, ce, cn] = c.bb;
      feats.push({ type: "Feature", properties: {}, geometry: { type: "Polygon", coordinates: [[[cw, cs], [ce, cs], [ce, cn], [cw, cn], [cw, cs]]] } });
      if (ce - cw < 90) { w = Math.min(w, cw); s = Math.min(s, cs); e = Math.max(e, ce); n = Math.max(n, cn); }
    }
    return { fc: { type: "FeatureCollection", features: feats }, bounds: feats.length && w <= e ? [w, s, e, n] : null };
  }

  // Coverage {fc, bounds} for any pack: NOAA cells (catalog bb) or IENC cells.
  _packCoverage(pk) {
    if (!pk) return { fc: { type: "FeatureCollection", features: [] }, bounds: null };
    if (pk.kind === "noaa") return this._districtCoverage(pk.cg);
    const feats = [];
    for (const c of pk.cells || []) {
      const [w, s, e, n] = c.bbox || [];
      if ([w, s, e, n].every(Number.isFinite)) feats.push({ type: "Feature", properties: {}, geometry: { type: "Polygon", coordinates: [[[w, s], [e, s], [e, n], [w, n], [w, s]]] } });
    }
    return { fc: { type: "FeatureCollection", features: feats }, bounds: pk.bbox || null };
  }

  // Build the detail-pane preview: a real OSM map framed to the pack with every
  // cell's coverage footprint outlined. Rebuilt per selection.
  _buildPreviewMap(cov) {
    const host = this.shadowRoot.getElementById("preview-map");
    if (!host || !window.maplibregl) return;
    if (this._previewMap) { try { this._previewMap.remove(); } catch (e) { /* ignore */ } this._previewMap = null; }
    // MapLibre's stylesheet must live in THIS shadow root for the canvas to size.
    if (!this.shadowRoot.querySelector("link[data-mlcss]")) {
      const l = document.createElement("link");
      l.rel = "stylesheet"; l.href = this._assets + "vendor/maplibre-gl.css"; l.setAttribute("data-mlcss", "");
      this.shadowRoot.appendChild(l);
    }
    const accent = getComputedStyle(this).getPropertyValue("--ui-accent").trim() || "#1565c0";
    const map = new window.maplibregl.Map({
      container: host, attributionControl: false, cooperativeGestures: false,
      style: {
        version: 8,
        sources: { osm: { type: "raster", tileSize: 256, maxzoom: 19, tiles: ["https://tile.openstreetmap.org/{z}/{x}/{y}.png"], attribution: "© OpenStreetMap" } },
        layers: [{ id: "osm", type: "raster", source: "osm" }],
      },
      center: cov.bounds ? [(cov.bounds[0] + cov.bounds[2]) / 2, (cov.bounds[1] + cov.bounds[3]) / 2] : [-98, 39],
      zoom: 3,
    });
    this._previewMap = map;
    map.on("load", () => {
      map.addSource("cov", { type: "geojson", data: cov.fc });
      map.addLayer({ id: "cov-fill", type: "fill", source: "cov", paint: { "fill-color": accent, "fill-opacity": 0.18 } });
      map.addLayer({ id: "cov-line", type: "line", source: "cov", paint: { "line-color": accent, "line-width": 1, "line-opacity": 0.9 } });
      if (cov.bounds) map.fitBounds([[cov.bounds[0], cov.bounds[1]], [cov.bounds[2], cov.bounds[3]]], { padding: 16, duration: 0 });
    });
  }

  // Pane 1 selection: choose a provider. Partial update — swap the packs + detail
  // columns only (the list keeps its scroll position; no full re-render).
  _selectProvider(id) {
    this._selProvider = id;
    this._selPack = null;
    this._activeDistrict = null;
    const r = this.shadowRoot;
    r.querySelectorAll(".m-row[data-prov]").forEach((el) => el.classList.toggle("sel", el.dataset.prov === id));
    const cols = r.querySelectorAll(".miller > .mcol");
    if (cols[1]) { cols[1].outerHTML = this._renderPacksCol(); this._wireMillerRows(); }
    this._updateDetail();
  }

  // Pane 2 selection: choose a pack. Partial update.
  _selectPack(key, cg) {
    this._selPack = key;
    this._activeDistrict = cg || null;
    this.shadowRoot.querySelectorAll(".m-row[data-pack]").forEach((el) => el.classList.toggle("sel", el.dataset.pack === key));
    this._updateDetail();
  }

  // Rebuild only the detail column (+ its buttons + preview map), leaving the list
  // columns and their scroll untouched.
  _updateDetail() {
    const col = this.shadowRoot.querySelector(".miller > .mcol-detail");
    if (!col) return;
    col.outerHTML = this._renderDetailCol();
    this._wireDetailButtons();
    this._wireImport(); // the User-Charts detail may render the drop zone
    this._renderPreview();
  }

  // Human label for a set name (provider · pack).
  _setLabel(name) {
    const m = /^noaa-d(\d+)$/.exec(name);
    if (m) { const d = DISTRICTS.find((x) => x.cg === +m[1]); return d ? `NOAA · ${d.region}` : `NOAA · District ${m[1]}`; }
    if (name === "import") return "Imported charts";
    const ie = /^ienc-(.+)$/.exec(name);
    if (ie) return `IENC · ${ie[1]}`;
    return name;
  }

  _wirePacks() { this._wireMillerRows(); this._wireDetailButtons(); }

  // Wire the provider/pack rows (re-run after a column is swapped).
  _wireMillerRows() {
    const r = this.shadowRoot;
    const onActivate = (el, fn) => {
      el.addEventListener("click", fn);
      el.addEventListener("keydown", (e) => { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); fn(); } });
    };
    r.querySelectorAll(".m-row[data-prov]").forEach((row) => onActivate(row, () => this._selectProvider(row.dataset.prov)));
    r.querySelectorAll(".m-row[data-pack]").forEach((row) => onActivate(row, () => this._selectPack(row.dataset.pack, row.dataset.cg ? +row.dataset.cg : null)));
  }

  // Wire the detail-pane action buttons (re-run after the detail column is swapped).
  _wireDetailButtons() {
    const r = this.shadowRoot;
    r.querySelectorAll(".pk-btn[data-getpack]").forEach((b) =>
      b.addEventListener("click", (e) => { e.stopPropagation(); this._downloadSelected(b.dataset.getpack); }));
    r.querySelectorAll(".pk-btn[data-uninstall-set]").forEach((b) =>
      b.addEventListener("click", (e) => { e.stopPropagation(); this._uninstallSet(b.dataset.uninstallSet); }));
    r.querySelectorAll(".pk-btn[data-disable]").forEach((b) =>
      b.addEventListener("click", (e) => { e.stopPropagation(); this._setPackDisabled(b.dataset.disable, true); }));
    r.querySelectorAll(".pk-btn[data-enable]").forEach((b) =>
      b.addEventListener("click", (e) => { e.stopPropagation(); this._setPackDisabled(b.dataset.enable, false); }));
  }

  // Click Download on a pack: enqueue it (or start immediately if idle).
  _downloadSelected(key) {
    const pk = this._providerPacks(this._selProvider || "noaa").find((p) => p.key === key);
    if (!pk) return;
    if (this._activeDownloadKey === key) return;          // downloading now
    if (this._dlQueue.some((j) => j.key === key)) return; // already queued
    if (this._installedSets && this._installedSets.has(key)) return; // already have it
    this._dlQueue.push(pk);
    this._reflectDownloadState();
    this._pumpDownloads();
  }

  // Run the next queued download, one at a time. Re-entrant-safe.
  async _pumpDownloads() {
    if (this._activeDownloadKey || !this._dlQueue.length) return;
    const pk = this._dlQueue.shift();
    this._activeDownloadKey = pk.key;
    this._reflectDownloadState();
    try {
      if (pk.kind === "ienc") await this._runDownloadIenc(pk);
      else if (pk.kind === "noaa") await this._runDownloadPack(pk.cg);
    } catch (e) { console.error("[download]", pk.key, e); }
    this._activeDownloadKey = null;
    this._reflectDownloadState();
    this._pumpDownloads(); // next in line
  }

  // Reflect queue state on the visible pack buttons (no full re-render → no map
  // flicker): update each Download button in place, and refresh the pack list.
  _reflectDownloadState() {
    if (!this._active) return;
    this._refreshDownloadButtons();
    this._refreshPacksCol();
  }

  _refreshDownloadButtons() {
    this.shadowRoot.querySelectorAll(".pk-btn[data-getpack]").forEach((b) => {
      b.outerHTML = this._downloadBtnHtml(b.dataset.getpack);
    });
    this._wireDetailButtons(); // re-bind the swapped button(s)
  }

  // The Download button's HTML for a pack key, by queue state.
  _downloadBtnHtml(key) {
    if (this._activeDownloadKey === key)
      return `<button class="pk-btn downloading" data-getpack="${esc(key)}" disabled><span class="pk-spin"></span>Downloading…</button>`;
    if (this._dlQueue.some((j) => j.key === key))
      return `<button class="pk-btn queued" data-getpack="${esc(key)}" disabled>Queued</button>`;
    return `<button class="pk-btn" data-getpack="${esc(key)}">⬇ Download</button>`;
  }

  // Whether a pack key is downloading now / waiting in the queue (for list badges).
  _packDownloadState(key) {
    if (this._activeDownloadKey === key) return "downloading";
    if (this._dlQueue.some((j) => j.key === key)) return "queued";
    return null;
  }

  // Download an IENC river pack: the server fetches each cell's s57 zip from
  // ienccloud.us and bakes them into the pack's set (ienc-<river>). Progress flows
  // through a NotificationCenter task; on success we dispatch charts-changed.
  async _runDownloadIenc(pk) {
    const name = pk.title || pk.name || "river";
    const t = this._notify ? this._notify.task("download:" + pk.key, { label: `Downloading ${name} charts…` }) : null;
    if (this._active) this.render();
    try {
      const cells = pk.cells.map((c) => ({ name: c.name, url: c.url }));
      await this._api.importAndWait({ set: pk.key, cells }, { name, onStatus: (p) => t && t.progress(p.frac, p.sub) });
      if (t) t.done();
    } catch (e) {
      console.error(`[ienc] ${pk.key} download:`, e.message);
      if (t) t.fail(e);
    }
    await this._syncRegistry();
    this._changed();
    if (this._active) this.render();
  }

  // Download a whole district pack: the SERVER fetches NOAA's per-district bundle
  // into its data store and bakes it into its OWN tile set (noaa-d<cg>). Falls back
  // to per-cell server fetches if the district bundle can't be opened.
  async _runDownloadPack(cg) {
    const d = DISTRICTS.find((x) => x.cg === cg);
    const label = d ? `${d.region} pack` : `District ${cg}`;
    const all = this._districtCellNames(cg);
    if (!all.length) return;
    if (all.every((n) => this._installed.has(n)) && this._installedSets.has("noaa-d" + cg)) return; // already installed
    if (!await this._ensureAgreed()) return; // NOAA ENC User Agreement gate

    this._activeDistrict = cg;
    const set = "noaa-d" + cg;
    const region = d ? d.region : "NOAA";
    const t = this._notify ? this._notify.task("download:" + set, { label: `Downloading ${region} charts…` }) : null;
    const onStatus = (p) => t && t.progress(p.frac, p.sub);
    if (this._active) this.render();
    let ok = false;
    try {
      // The district bundle holds the whole district; bake the FULL district into the
      // pack so it's a complete set (names=all, not just the not-yet-installed ones).
      await this._api.importAndWait({ set, zipUrl: this._districtZipUrl(cg), names: all }, { name: region, onStatus });
      ok = true;
    } catch (e) {
      console.warn(`[pack] ${label} server bundle failed — per-cell:`, e.message);
      const cells = all.map((n) => ({ name: n, url: (this._byName.get(n) || {}).z || "" }));
      try { await this._api.importAndWait({ set, cells }, { name: region, onStatus }); ok = true; }
      catch (e2) { console.error(`[pack] ${label} server download failed:`, e2.message); if (t) t.fail(e2); }
    }
    if (t && ok) t.done();
    await this._syncRegistry();
    this._changed();
    // Deliberately DON'T frame the map to the new pack — yanking the camera when a
    // background download finishes is distracting.
    if (this._active) this.render();
  }

  // Find-a-chart search box.
  _renderPackSearch() {
    const q = this._cellQuery || "";
    return `<input id="pack-search" class="pack-search" type="search" placeholder="Find a chart, port, or region…" autocomplete="off" spellcheck="false" value="${esc(q)}">`;
  }
  _wirePackSearch() {
    const i = this.shadowRoot.getElementById("pack-search");
    if (!i) return;
    i.oninput = () => { this._cellQuery = i.value; this._applySearch(); };
  }

  // Packs/regions matching the current query. Returns null when the query is too
  // short, else a Map cg → matching cell object (or null for a region-name match).
  _searchHits() {
    const q = (this._cellQuery || "").trim().toLowerCase();
    if (q.length < 2) return null;
    const hits = new Map();
    for (const c of this._catalog) {
      if (typeof c.cg !== "number" || hits.has(c.cg)) continue;
      if (c.n.toLowerCase().includes(q) || (c.l && c.l.toLowerCase().includes(q))) hits.set(c.cg, c);
    }
    for (const d of DISTRICTS) {
      if (hits.has(d.cg)) continue;
      if (d.region.toLowerCase().includes(q) || d.name.toLowerCase().includes(q) || (d.blurb && d.blurb.toLowerCase().includes(q))) hits.set(d.cg, null);
    }
    return hits;
  }

  // Re-render just the provider + pack columns (highlight + dim by the query).
  _applySearch() {
    const cols = this.shadowRoot.querySelectorAll(".miller > .mcol");
    if (cols.length < 2) return;
    cols[0].outerHTML = this._renderProvidersCol();
    cols[1].outerHTML = this._renderPacksCol();
    this._wireMillerRows();
  }

  // Re-render just the packs column (e.g. when the IENC catalogue finishes loading).
  _refreshPacksCol() {
    const cols = this.shadowRoot.querySelectorAll(".miller > .mcol");
    if (cols[1]) { cols[1].outerHTML = this._renderPacksCol(); this._wireMillerRows(); }
  }

  // The currently-selected pack object (for the current provider), or null.
  _selectedPack() {
    if (!this._selPack) return null;
    return this._providerPacks(this._selProvider || "noaa").find((p) => p.key === this._selPack) || null;
  }

  // NOAA data freshness footer.
  _renderDataFreshness() {
    if (!this._catalogDate) return "";
    const total = this._catalog.length.toLocaleString();
    return `<div class="data-fresh">NOAA chart data current as of <b>${fmtIssue(this._catalogDate)}</b> · ${total} charts available</div>`;
  }

  // Uninstall any pack by set name: DELETE /api/set removes the baked pmtiles/aux
  // from the cache (source cells in the data store are kept), then re-render.
  async _uninstallSet(set) {
    if (this._uninstalling) return;
    if (!(this._installedSets && this._installedSets.has(set))) return;
    this._uninstalling = true;
    const t = this._notify ? this._notify.task("uninstall:" + set, { label: "Removing charts…" }) : null;
    if (t) t.progress(null, this._setLabel(set));
    if (this._active) this.render();
    try { await this._api.deleteSet(set); if (t) t.done(); }
    catch (e) { console.warn("[pack] remove", set, e); if (t) t.fail(e); }
    await this._syncRegistry();
    this._uninstalling = false;
    this._changed();
    if (this._active) this.render();
  }

  // Show/hide an installed pack on the map. The state is SERVER-side (the data is
  // kept; this only toggles rendering); call the API, re-sync, dispatch changed.
  async _setPackDisabled(key, off) {
    try { await this._api.setEnabled(key, !off); }
    catch (e) { console.warn("[pack] toggle", key, e); }
    await this._syncRegistry();
    this._changed();
    if (this._active) { this._updateDetail(); this._refreshPacksCol(); }
  }

  // -- NOAA ENC user-agreement gate -----------------------------------------
  // Must be displayed + accepted before any chart download (charts.noaa.gov/ENCs
  // §3). Resolves true once accepted (persisted).
  _ensureAgreed() {
    if (this._agreed) return Promise.resolve(true);
    return this._showAgreement();
  }
  _showAgreement() {
    return new Promise((resolve) => {
      const host = this.shadowRoot.getElementById("agree-host");
      if (!host) return resolve(this._agreed);
      host.innerHTML = `
        <div id="agree" class="modal">
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
        </div>`;
      this._agreeResolve = resolve;
      const accept = host.querySelector("#agree-accept");
      const decline = host.querySelector("#agree-decline");
      if (accept) accept.onclick = () => this._resolveAgreement(true);
      if (decline) decline.onclick = () => this._resolveAgreement(false);
    });
  }
  _resolveAgreement(accepted) {
    const host = this.shadowRoot.getElementById("agree-host");
    if (host) host.innerHTML = "";
    if (accepted) { this._agreed = true; try { localStorage.setItem(LS_AGREE, "1"); } catch {} }
    const r = this._agreeResolve; this._agreeResolve = null;
    if (r) r(accepted);
  }
  // Whether the agreement modal is currently shown (the shell's Escape handler asks).
  get agreementOpen() {
    const host = this.shadowRoot.getElementById("agree-host");
    return !!(host && host.querySelector("#agree"));
  }

  // -- User-Charts local-file import (drop a .zip / .000 / .pmtiles) ---------
  // .zip → list its cells for selection; .000 → store + bake; .pmtiles → handed to
  // the shell (it owns the client-side plotter archive path). After a store/bake
  // we dispatch charts-changed so the shell reconciles the map.
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
          if (log) log.textContent = `${file.name}: ${added} cell(s) found`;
        } else if (lower.endsWith(".000")) {
          // Raw cell: persist it; it gets baked into the archive below.
          const name = file.name.replace(/\.000$/i, "");
          await this._store.put(name, new Uint8Array(await file.arrayBuffer()));
          this._installed.add(name);
          rawInstalled.push(name);
          if (log) log.textContent = `imported ${name}`;
        } else if (lower.endsWith(".pmtiles")) {
          // A prebaked archive — the plotter is shell-owned, so hand the file to the
          // shell's client-side archive path (addArchive + persist).
          this._emit("chart-import-archive", { file });
          if (log) log.textContent = `loaded ${file.name}`;
        } else {
          if (log) log.textContent = `skipped ${file.name} (need .zip, .000 or .pmtiles)`;
        }
      } catch (err) {
        console.error(err);
        if (log) log.textContent = `${file.name}: ${err.message}`;
      }
    }
    this.renderArchiveList();
    // Re-bake the now-larger stored cell set on the server.
    await this._refreshCharts();
  }

  // Bake the LOCALLY-imported cells (the OPFS store) into the "import" set: upload
  // each cell to the server, then kick the import job. On completion dispatch
  // charts-changed (the shell reconciles the map). Coalesces concurrent rebakes.
  async _refreshCharts() {
    if (!this._store || !this._api) return;
    if (this._charting) { this._chartingAgain = true; return; }
    this._charting = true;
    let t = null;
    try {
      const local = await this._store.list().catch(() => []);
      if (local.length) {
        t = this._notify ? this._notify.task("import:user", { label: "Baking your charts…" }) : null;
        for (const name of local) {
          try {
            const bytes = await this._store.getBytes(name);
            if (bytes && bytes.length) await this._api.uploadCell(name, bytes);
          } catch (e) { console.warn("[charts] upload", name, e); }
        }
        const { job } = await this._api.importCells("import", local);
        await this._api.pollJob(job, { name: "your", onStatus: (p) => t && t.progress(p.frac, p.sub) });
        if (t) t.done();
      }
      await this._syncRegistry();
      this._changed();
    } catch (e) {
      console.warn("[charts] import bake", e);
      if (t) t.fail(e);
    } finally {
      this._charting = false;
      if (this._chartingAgain) { this._chartingAgain = false; this._refreshCharts(); }
    }
  }

  // Bake the selected archive cells into the "import" set: extract each from its zip,
  // store it, then bake (via _refreshCharts). Mirrors the old shell importSelected.
  async importSelected() {
    const names = [...this._selected].filter((n) => this._archive.has(n));
    if (!names.length) return;
    const imported = [];
    let done = 0;
    const t = this._notify ? this._notify.task("import:archive", { label: "Importing charts" }) : null;
    for (const name of names) {
      if (t) t.progress(done / names.length, `${name} · ${done + 1} of ${names.length}`);
      try {
        const { blob, entry } = this._archive.get(name);
        const bytes = await extractEntry(blob, entry);
        await this._store.put(name, bytes); // persist only
        this._installed.add(name);
        this._archive.delete(name);
        this._selected.delete(name);
        imported.push(name);
      } catch (err) {
        console.error("[import]", name, err);
        if (t) t.progress(done / names.length, `${name}: ${err.message}`);
      }
      done++;
    }
    if (t) t.done();
    this.renderArchiveList();
    // New cells stored → bake them on the server.
    if (imported.length) await this._refreshCharts();
  }

  // Re-bake every installed cell into the server "user" set and render it (the
  // bake runs server-side; see _refreshCharts).
  async rebakeArchive() {
    const names = [...this._installed];
    if (!names.length) return;
    const t = this._notify ? this._notify.task("rebake:user", { label: "Baking charts…" }) : null;
    if (t) t.progress(null, `${names.length} chart${names.length > 1 ? "s" : ""}`);
    try { await this._refreshCharts(); if (t) t.done(); }
    catch (e) { console.error("[bake]", e); if (t) t.fail(e); }
  }

  // The "from archive" selectable cell list (after a .zip is opened).
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

  toggleSelect(name) {
    if (this._selected.has(name)) this._selected.delete(name);
    else this._selected.add(name);
    this.renderArchiveList();
  }

  // Wire the file-import controls (the drop zone is re-rendered, so bound each render).
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
}

customElements.define("chart-library", ChartLibrary);
