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

import { esc, fmtIssue, fmtScale } from "../lib/util.mjs";
import { seaColor, landColor, coastColor } from "../chart-canvas/s52-style.mjs"; // our own basemap palette (consistent with the chart)
import {
  STYLE, widgetBody, libraryBody, packSearch, providersCol, packsHeader,
  packBadge, userPackRow, packRow, packsCol, emptyRow, downloadBtn,
  detailEmpty, detailUnknownSet, detailPack, installedActions, previewMapHost,
  importDetail, dataFreshness, agreementModal, millerBack, packCellList,
} from "./chart-library.view.mjs";

// NOAA ENC User Agreement acceptance (localStorage). Exported so the shell can
// share the same key if it ever needs to read it.
export const LS_AGREE = "chartplotter:enc-agreement";

// The GSHHG coastline basemap (the same offline land/lakes the main map uses),
// fetched ONCE and shared across every coverage-preview snapshot so they all
// look consistent. Resolves null when absent (best-effort — preview then shows
// just the coverage box on the sea background).
let _coastlinePromise = null;
function loadCoastline(assets) {
  if (!_coastlinePromise) {
    _coastlinePromise = fetch(assets + "basemap/coastline.geojson")
      .then((r) => (r.ok ? r.json() : null))
      .catch(() => null);
  }
  return _coastlinePromise;
}
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

export class ChartLibrary extends HTMLElement {
  constructor() {
    super();
    if (!this.shadowRoot) this.attachShadow({ mode: "open" });
    // Injected via configure(); guarded so a stray render before configure no-ops.
    this._dl = null;       // ChartDownloader (NOAA catalogue/discovery)
    this._api = null;      // ChartService (server import/bake + pack registry)
    this._notify = null;   // NotificationCenter (task progress + banners)
    this._assets = "./";

    // Selection state for the 3-pane drill-down.
    this._selProvider = null; // "noaa" | "ienc" | "user"
    this._selPack = null;     // set key of the selected pack
    // Phone drill-down level: which single column shows on a narrow screen
    // ("provider"|"pack"|"detail"). Ignored by desktop/tablet CSS (it shows all
    // three). Selecting a row advances it; the phone back bar retreats it.
    this._phoneLevel = "provider";
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
    this._hiddenCells = new Set(); // cell names hidden from the map (per-cell toggle); owned by the shell, mirrored here for render
    this._packMeta = new Map();   // pack name → /api/packs entry (title/agency/scale/cellCount/imported/bounds)
    this._packDetail = new Map(); // pack name → /api/pack/<name> detail (per-cell list), fetched lazily on select

    // NOAA ENC agreement acceptance (persisted).
    this._agreed = localStorage.getItem(LS_AGREE) === "1";
    this._agreeResolve = null;

    this._previewMap = null;    // live preview map (unused; kept for safe teardown)
    this._previewCache = new Map(); // pack key → coverage snapshot dataURL (rendered once)
    this._previewKey = null;    // pack key the detail preview currently targets
    this._snapEl = null; this._snapMap = null; // in-flight off-screen snapshot map
    this._lightbox = null; this._lightboxKey = null; // tap-to-enlarge overlay
    this._widget = false;       // widget (prebaked) build: import-only Library
    this._active = false;       // is the charts UI currently shown?
  }

  connectedCallback() {
    this.shadowRoot.innerHTML = `<style>${STYLE}</style><div id="body"></div><div id="agree-host"></div>`;
  }

  disconnectedCallback() { this.teardownPreview(); }

  // Tear down the detail-pane preview (called when the drawer closes): drop any
  // in-flight off-screen snapshot map + the enlarge overlay. (Cached snapshots are
  // kept so reopening is instant.)
  teardownPreview() {
    if (this._snapMap) { try { this._snapMap.remove(); } catch (e) { /* ignore */ } this._snapMap = null; }
    if (this._snapEl && this._snapEl.parentNode) this._snapEl.parentNode.removeChild(this._snapEl);
    this._snapEl = null;
    this._closeLightbox();
    if (this._previewMap) { try { this._previewMap.remove(); } catch (e) { /* ignore */ } this._previewMap = null; }
  }

  // Inject dependencies (call once after creation). `widget` flips the Library to
  // import-only (no NOAA download/region picker), matching the shell's widget mode.
  configure({ dl, api, notify, assets, widget } = {}) {
    this._dl = dl || null;
    this._api = api || null;
    this._notify = notify || null;
    if (assets) this._assets = assets;
    this._widget = !!widget;
    return this;
  }

  // Make the charts UI active for a provider id ("noaa"|"ienc"|"user") and render.
  show(provider) {
    this._active = true;
    if (provider) { this._selProvider = provider; this._selPack = null; this._phoneLevel = "provider"; }
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

  // An onStatus handler for a job that drives a NotificationCenter task: the
  // phase-aware label ("Generating NOAA · Northeast tiles…") AND the numeric
  // sub-line both update as the server moves through download → bake → finish.
  _jobStatus(t) { return (p) => { if (!t) return; if (p.label) t.label(p.label); t.progress(p.frac, p.sub); }; }

  // Refresh the installed/disabled/installed-cell snapshot from the server, so the
  // panel's pack badges + counts are current. Called before a (re-)render that
  // depends on it. Best-effort: a transient failure keeps the last snapshot.
  async _syncRegistry() {
    if (!this._api) return;
    try {
      const packs = await this._api.packs();
      this._installedSets = new Set(packs.map((p) => p.name));
      this._disabled = new Set(packs.filter((p) => !p.enabled).map((p) => p.name));
      // Keep each pack's extracted metadata (title/agency/scale/cellCount/imported/
      // bounds) for the User-Charts rows + detail — the per-upload info display.
      this._packMeta = new Map(packs.map((p) => [p.name, p]));
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
    // user: uploaded packs (anything not NOAA/IENC), with extracted metadata.
    return [...sets].filter((n) => !/^(noaa-d\d+|ienc-)/.test(n)).sort().map((n) => {
      const m = this._packMeta.get(n) || {};
      return {
        key: n, kind: "user", installed: true,
        title: m.title || this._setLabel(n),
        sub: this._userPackSub(m),
        bbox: Array.isArray(m.bounds) ? m.bounds : null,
        meta: m,
      };
    });
  }

  // One-line summary for a user pack row: chart count, scale (range), agency.
  _userPackSub(m) {
    const parts = [];
    if (m.cellCount) parts.push(`${m.cellCount} chart${m.cellCount > 1 ? "s" : ""}`);
    if (m.scaleMin) parts.push(m.scaleMax && m.scaleMax !== m.scaleMin ? `1:${fmtScale(m.scaleMin)}–1:${fmtScale(m.scaleMax)}` : `1:${fmtScale(m.scaleMin)}`);
    if (m.agency) parts.push(m.agency);
    return parts.join(" · ") || "installed";
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
  // The whole charts panel. Widget (prebaked) builds get the import-only Library;
  // otherwise the 3-pane drill-down + search + preview.
  render() {
    const el = this.shadowRoot.getElementById("body");
    if (!el) return;
    if (this._widget) {
      el.innerHTML = widgetBody();
      this._wireImport();
      return;
    }
    el.innerHTML = libraryBody({
      searchHtml: this._renderPackSearch(),
      providersCol: this._renderProvidersCol(),
      packsCol: this._renderPacksCol(),
      detailCol: this._renderDetailCol(),
      freshnessHtml: this._renderDataFreshness(),
      level: this._phoneLevel,
      backLabel: this._backLabel(),
    });
    this._wirePackSearch();
    this._wirePacks();
    this._wireMillerBack();
    this._wireImport();
    this._renderPreview();
  }

  // The phone back bar's crumb: the title of the level we'd return TO. From the
  // detail level it's the provider's pack list; from pack it's "Source".
  _backLabel() {
    if (this._phoneLevel === "detail") return this._providerName(this._selProvider || "noaa");
    return "Source";
  }

  // Set the phone drill-down level + sync the .miller data-attr and back crumb in
  // place (so column hot-swaps don't need a full re-render to stay correct).
  _setPhoneLevel(level) {
    this._phoneLevel = level;
    const m = this.shadowRoot.querySelector(".miller");
    if (!m) return;
    m.dataset.level = level;
    const crumb = m.querySelector(".miller-back .mb-crumb");
    if (crumb) crumb.textContent = this._backLabel();
  }

  // Phone back bar: step UP a level (detail→pack, pack→provider). Wired each
  // render (the bar is a stable child of .miller, untouched by column swaps).
  _wireMillerBack() {
    const bar = this.shadowRoot.getElementById("miller-back");
    if (!bar) return;
    const back = () => {
      if (this._phoneLevel === "detail") this._setPhoneLevel("pack");
      else if (this._phoneLevel === "pack") this._setPhoneLevel("provider");
    };
    bar.addEventListener("click", back);
    bar.addEventListener("keydown", (e) => { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); back(); } });
  }

  // Show the detail-pane coverage preview for the selected pack: a STATIC image
  // snapshotted once (no live embedded map). Cached per pack so revisits are
  // instant; user packs have no coverage map.
  _renderPreview() {
    const pk = this._selectedPack();
    if (pk && pk.kind !== "user") { this._renderPreviewImage(pk); return; }
    this._previewKey = null;
    const h = this.shadowRoot.getElementById("preview-map");
    if (h) h.innerHTML = "";
  }

  // Paint the cached snapshot if we have one; otherwise show a placeholder and
  // render one off-screen, then paint it (only if the pack is still selected).
  _renderPreviewImage(pk) {
    const host = this.shadowRoot.getElementById("preview-map");
    if (!host) return;
    const key = pk.key;
    this._previewKey = key;
    const cached = this._previewCache.get(key);
    if (cached) { this._setPreviewImg(host, cached); return; }
    host.innerHTML = `<div class="prev-ph">Rendering coverage…</div>`;
    this._snapshotPreview(this._packCoverage(pk)).then((url) => {
      if (!url) return;
      this._previewCache.set(key, url);
      if (this._previewKey === key) {
        const h = this.shadowRoot.getElementById("preview-map");
        if (h) this._setPreviewImg(h, url);
      }
    }).catch(() => {});
  }

  _setPreviewImg(host, url) {
    host.innerHTML = "";
    const img = document.createElement("img");
    img.className = "prev-img"; img.src = url; img.alt = "Chart coverage"; img.title = "Tap to enlarge";
    img.addEventListener("click", () => this._openPreviewLightbox(url));
    host.appendChild(img);
  }

  // Render the coverage ONCE over our own GSHHG coastline basemap (same land/sea
  // colours as the chart) into an off-screen MapLibre map, snapshot the canvas to
  // a PNG data URL, then tear the map down. Resolves null on failure.
  async _snapshotPreview(cov) {
    if (!window.maplibregl) return null;
    // MapLibre's CSS must live in this shadow root for the canvas to size.
    if (!this.shadowRoot.querySelector("link[data-mlcss]")) {
      const l = document.createElement("link");
      l.rel = "stylesheet"; l.href = this._assets + "vendor/maplibre-gl.css"; l.setAttribute("data-mlcss", "");
      this.shadowRoot.appendChild(l);
    }
    const coast = await loadCoastline(this._assets); // module-cached; null if absent
    const accent = getComputedStyle(this).getPropertyValue("--ui-accent").trim() || "#1565c0";
    // Off-screen but LAID OUT (display:none won't render WebGL).
    const el = document.createElement("div");
    el.style.cssText = "position:absolute;left:-10000px;top:0;width:380px;height:200px;pointer-events:none";
    this.shadowRoot.appendChild(el);
    const sources = { cov: { type: "geojson", data: cov.fc } };
    const layers = [{ id: "bg", type: "background", paint: { "background-color": seaColor({}) } }];
    if (coast) {
      sources.coast = { type: "geojson", data: coast };
      layers.push(
        { id: "coast-land", type: "fill", source: "coast", filter: ["==", ["get", "level"], 1], paint: { "fill-color": landColor({}) } },
        { id: "coast-lake", type: "fill", source: "coast", filter: ["==", ["get", "level"], 2], paint: { "fill-color": seaColor({}) } },
        { id: "coast-line", type: "line", source: "coast", filter: ["<=", ["get", "level"], 2], paint: { "line-color": coastColor({}), "line-width": 1.1 } },
      );
    }
    layers.push(
      { id: "cov-fill", type: "fill", source: "cov", paint: { "fill-color": accent, "fill-opacity": 0.2 } },
      { id: "cov-line", type: "line", source: "cov", paint: { "line-color": accent, "line-width": 1.2, "line-opacity": 0.95 } },
    );
    const map = new window.maplibregl.Map({
      container: el, interactive: false, attributionControl: false, fadeDuration: 0, preserveDrawingBuffer: true,
      style: { version: 8, sources, layers },
      center: cov.bounds ? [(cov.bounds[0] + cov.bounds[2]) / 2, (cov.bounds[1] + cov.bounds[3]) / 2] : [-98, 39],
      zoom: 3,
    });
    this._snapEl = el; this._snapMap = map;
    return new Promise((resolve) => {
      let done = false;
      const finish = () => {
        if (done) return; done = true;
        let url = null;
        try { url = map.getCanvas().toDataURL("image/png"); } catch (e) { /* ignore */ }
        try { map.remove(); } catch (e) { /* ignore */ }
        if (el.parentNode) el.parentNode.removeChild(el);
        if (this._snapEl === el) { this._snapEl = null; this._snapMap = null; }
        resolve(url);
      };
      map.on("load", () => {
        if (cov.bounds) map.fitBounds([[cov.bounds[0], cov.bounds[1]], [cov.bounds[2], cov.bounds[3]]], { padding: 24, duration: 0 });
        map.once("idle", finish);
      });
      setTimeout(finish, 2500); // safety: never hang if idle doesn't fire
    });
  }

  // Tap-to-enlarge: a full-viewport overlay (appended to the body so it escapes
  // the drawer's stacking context). Click anywhere or Esc to dismiss.
  _openPreviewLightbox(url) {
    this._closeLightbox();
    const ov = document.createElement("div");
    ov.style.cssText = "position:fixed;inset:0;z-index:10000;display:flex;align-items:center;justify-content:center;" +
      "background:rgba(0,0,0,.72);padding:24px;box-sizing:border-box;cursor:zoom-out";
    const img = document.createElement("img");
    img.src = url; img.alt = "Chart coverage";
    img.style.cssText = "max-width:100%;max-height:100%;border-radius:8px;box-shadow:0 8px 40px rgba(0,0,0,.5)";
    ov.appendChild(img);
    ov.addEventListener("click", () => this._closeLightbox());
    this._lightboxKey = (e) => { if (e.key === "Escape") this._closeLightbox(); };
    document.addEventListener("keydown", this._lightboxKey);
    document.body.appendChild(ov);
    this._lightbox = ov;
  }

  _closeLightbox() {
    if (this._lightbox) { this._lightbox.remove(); this._lightbox = null; }
    if (this._lightboxKey) { document.removeEventListener("keydown", this._lightboxKey); this._lightboxKey = null; }
  }

  // Pane 1: providers. With an active search, providers that contain a match are
  // highlighted and the rest dimmed.
  _renderProvidersCol() {
    const sel = this._selProvider; // null until the user picks → nothing highlighted by default
    const hits = this._searchHits();
    const providers = this._providers().map((p) => {
      let cls = sel === p.id ? " sel" : "";
      if (hits) cls += this._providerHasMatch(p.id, hits) ? " match" : " dim";
      return { ...p, cls };
    });
    return providersCol(providers);
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
    const prov = this._selProvider;
    if (!prov) return packsCol({ header: "", rows: emptyRow("Select a source to see its charts.") });
    const packs = this._providerPacks(prov);
    const hits = this._searchHits();
    const q = (this._cellQuery || "").trim().toLowerCase();
    let rows;
    if (prov === "ienc" && this._ienc === undefined) {
      // Catalogue not loaded yet — fetch it, then refresh just this column.
      if (!this._iencPromise) this._iencCatalog().then(() => { if (this._active && this._selProvider === "ienc") this._refreshPacksCol(); });
      rows = emptyRow("Loading inland ENC catalogue…");
    } else if (prov === "user") rows = packs.length ? packs.map((pk) => this._userPackRow(pk)).join("") : emptyRow("No imported charts yet — open this to add some.");
    else if (!packs.length) rows = emptyRow(prov === "ienc" ? "No inland ENC packs available." : "Nothing installed.");
    else rows = packs.map((pk) => {
      let cls = (this._selPack === pk.key ? " sel" : "") + (pk.installed ? " on" : "");
      let sub = pk.sub;
      if (hits) {
        const hit = pk.cg != null ? (hits.has(pk.cg) ? hits.get(pk.cg) : undefined) : (pk.title.toLowerCase().includes(q) ? null : undefined);
        if (hit === undefined) cls += " dim";
        else { cls += " match"; if (hit) sub = `matches “${esc(hit.l || hit.n)}”`; }
      }
      return packRow({ key: pk.key, title: pk.title, cls, sub, cg: pk.cg, badge: this._packBadge(pk.key, pk.installed) });
    }).join("");
    return packsCol({ header: this._packsHeader(prov), rows });
  }

  // A user-imported pack row.
  _userPackRow(pk) {
    return userPackRow(pk, { selPack: this._selPack, badge: this._packBadge(pk.key, true) });
  }

  // Status pill for a pack row. A pending/active download takes priority (so you
  // can see at a glance which packs are downloading/queued); otherwise an installed
  // pack shows "Active"/"Disabled", and a plain not-installed pack shows nothing.
  _packBadge(key, installed) {
    return packBadge({ installed, disabled: this._disabled.has(key), downloadState: this._packDownloadState(key) });
  }

  // Pane-2 header: the provider's name + a one-line description and when its source
  // catalogue was last refreshed.
  _packsHeader(prov) {
    let line = "";
    if (prov === "noaa") line = `U.S. Coast Guard districts${this._catalogDate ? ` · catalogue ${fmtIssue(this._catalogDate)}` : ""} · ${this._catalog.length.toLocaleString()} charts`;
    else if (prov === "ienc") line = "USACE inland waterway ENC";
    else line = "Charts you've imported from a file";
    return packsHeader({ providerName: this._providerName(prov), line });
  }

  // Pane 3: the selected pack's detail — coverage map + download/remove.
  _renderDetailCol() {
    const key = this._selPack;
    if (!key) {
      if (this._selProvider === "user") return this._renderImportDetail();
      return detailEmpty();
    }
    const busy = this.busy;
    const installed = this._installedSets && this._installedSets.has(key);
    const pk = this._selectedPack();
    // An installed pack not in the current catalogue (e.g. an old set) → remove only.
    if (!pk) {
      return detailUnknownSet({ label: this._setLabel(key), key, installed, busy });
    }
    const disabled = this._disabled.has(key);
    const tick = installed ? (disabled ? ' <span class="m-badge off">Disabled</span>' : ' <span class="m-badge on">Active</span>') : "";
    const act = installed
      ? installedActions({ key, disabled, busy })
      : this._downloadBtnHtml(key);
    let title, sub, meta;
    if (pk.kind === "noaa") {
      const d = DISTRICTS.find((x) => x.cg === pk.cg);
      title = d ? d.region : pk.title;
      sub = d ? `${esc(d.name)} · ${esc(d.blurb)}` : "";
      meta = `${pk.sub} · outlined area below is the coverage`;
    } else if (pk.kind === "user") {
      const m = pk.meta || this._packMeta.get(key) || {};
      title = pk.title || this._setLabel(key);
      sub = this._userPackSub(m);
      const det = this._packDetail.get(key);
      const ed = det && det.cells && det.cells[0]; // single-cell editions/dates, when applicable
      const ymd = (s) => (/^\d{8}$/.test(s) ? `${s.slice(0, 4)}-${s.slice(4, 6)}-${s.slice(6, 8)}` : s); // S-57 YYYYMMDD → ISO
      const bits = [];
      if (m.imported) bits.push(`imported ${fmtIssue(m.imported.slice(0, 10))}`);
      if (ed && ed.edition) bits.push(`ed. ${ed.edition}${ed.update && ed.update !== "0" ? `/${ed.update}` : ""}`);
      if (ed && ed.issueDate) bits.push(`issued ${fmtIssue(ymd(ed.issueDate))}`);
      meta = [bits.join(" · "), "outlined area below is the coverage"].filter(Boolean).join(" — ");
    } else { // ienc
      title = `${pk.title} River`;
      sub = `USACE Inland ENC · ${pk.cells.length} chart${pk.cells.length > 1 ? "s" : ""}`;
      meta = "outlined area below is the coverage";
    }
    // Every pack now shows the coverage preview (user packs get per-cell bboxes
    // from the server metadata).
    const previewMap = previewMapHost();
    // Per-cell show/hide list, only for an installed & active pack (a fully
    // disabled pack is already hidden, so per-cell control is moot there).
    let extra = "";
    if (installed && !disabled) {
      const items = this._packCellItems(pk);
      extra = packCellList({ items, nShown: items.filter((it) => it.shown).length });
    }
    return detailPack({ title, tick, sub, meta, act, previewMap, extra });
  }

  // The installed cells belonging to a pack, as render items for packCellList:
  // { name, title, shown }. Cells come from the pack's catalogue membership
  // intersected with what's actually installed; titles from the NOAA catalogue.
  _packCellItems(pk) {
    // User packs: the per-cell list comes from the server detail (its own titles +
    // compilation scale), already exactly the baked set — no catalogue intersect.
    if (pk.kind === "user") {
      const cells = (this._packDetail.get(pk.key) || {}).cells || [];
      return cells.map((c) => ({ name: c.name, title: c.title || "", scale: c.scale || 0, shown: !this._hiddenCells.has(c.name) }))
        .sort((a, b) => a.name.localeCompare(b.name));
    }
    let names = [];
    if (pk.kind === "noaa") names = this._districtCellNames(pk.cg) || [];
    else if (pk.cells) names = pk.cells.map((c) => c.name);
    const seen = new Set();
    const items = [];
    for (const name of names) {
      if (seen.has(name) || !this._installed.has(name)) continue; // installed cells only
      seen.add(name);
      items.push({ name, title: (this._byName.get(name) || {}).l || "", shown: !this._hiddenCells.has(name) });
    }
    items.sort((a, b) => a.name.localeCompare(b.name));
    return items;
  }

  // Replace the shell-owned hidden-cell set (called on boot from persisted
  // settings and whenever it changes elsewhere). Re-renders the detail so the
  // checkboxes reflect the new state. Does NOT emit (avoids a feedback loop).
  setHiddenCells(names) {
    this._hiddenCells = new Set(names || []);
    if (this._active) this._updateDetail();
  }

  // The User-Charts detail: the import drop zone (baked server-side into the
  // "import" pack). Shown when the User Charts provider is open with no pack picked.
  _renderImportDetail() {
    return importDetail();
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

  // Coverage {fc, bounds} for any pack: NOAA cells (catalog bb), IENC cells, or a
  // user pack (per-cell bboxes from the server detail, else the pack's union bbox).
  _packCoverage(pk) {
    if (!pk) return { fc: { type: "FeatureCollection", features: [] }, bounds: null };
    if (pk.kind === "noaa") return this._districtCoverage(pk.cg);
    const box = (w, s, e, n) => ({ type: "Feature", properties: {}, geometry: { type: "Polygon", coordinates: [[[w, s], [e, s], [e, n], [w, n], [w, s]]] } });
    const feats = [];
    if (pk.kind === "user") {
      const cells = (this._packDetail.get(pk.key) || {}).cells || [];
      for (const c of cells) {
        const [w, s, e, n] = c.bbox || [];
        if ([w, s, e, n].every(Number.isFinite)) feats.push(box(w, s, e, n));
      }
      if (!feats.length && Array.isArray(pk.bbox) && pk.bbox.every(Number.isFinite)) feats.push(box(...pk.bbox));
      return { fc: { type: "FeatureCollection", features: feats }, bounds: pk.bbox || null };
    }
    for (const c of pk.cells || []) {
      const [w, s, e, n] = c.bbox || [];
      if ([w, s, e, n].every(Number.isFinite)) feats.push(box(w, s, e, n));
    }
    return { fc: { type: "FeatureCollection", features: feats }, bounds: pk.bbox || null };
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
    this._setPhoneLevel("pack"); // phone: advance provider → packs
  }

  // Pane 2 selection: choose a pack. Partial update.
  _selectPack(key, cg) {
    this._selPack = key;
    this._activeDistrict = cg || null;
    this.shadowRoot.querySelectorAll(".m-row[data-pack]").forEach((el) => el.classList.toggle("sel", el.dataset.pack === key));
    this._updateDetail();
    this._setPhoneLevel("detail"); // phone: advance packs → detail
    this._ensurePackDetail(key); // user packs: lazy-load per-cell detail, then re-render
  }

  // Fetch a user pack's per-cell detail (GET /api/pack/<name>) once and cache it,
  // re-rendering the detail pane when it arrives. NOAA/IENC packs derive their cell
  // list from their catalogue, so they're skipped.
  async _ensurePackDetail(key) {
    if (!key || !this._api || this._packDetail.has(key)) return;
    if (/^(noaa-d\d+|ienc-)/.test(key)) return;
    const detail = await this._api.packDetail(key);
    if (!detail) return;
    this._packDetail.set(key, detail);
    if (this._active && this._selPack === key) this._updateDetail();
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
    // Per-cell show/hide checkboxes + Select all / Clear all.
    r.querySelectorAll(".cell-row input[data-cell]").forEach((cb) =>
      cb.addEventListener("change", () => this._setCellShown(cb.dataset.cell, cb.checked)));
    const allBtn = r.querySelector("[data-cells-all]");
    if (allBtn) allBtn.addEventListener("click", (e) => { e.stopPropagation(); this._setAllCellsShown(true); });
    const noneBtn = r.querySelector("[data-cells-none]");
    if (noneBtn) noneBtn.addEventListener("click", (e) => { e.stopPropagation(); this._setAllCellsShown(false); });
  }

  // Show/hide one cell. The checkbox the user clicked is already in the right
  // state, so we update the mirror + count in place (NOT a full detail re-render,
  // which would reset the list's scroll position) and emit so the shell applies
  // the map filter and persists. shown=true → not hidden.
  _setCellShown(name, shown) {
    if (shown) this._hiddenCells.delete(name);
    else this._hiddenCells.add(name);
    this._emitHiddenCells();
    this._refreshCellCount();
  }

  // Show/hide every cell in the selected pack at once. Updates the mirror, every
  // checkbox, and the count in place (preserving scroll).
  _setAllCellsShown(shown) {
    const pk = this._selectedPack();
    if (!pk) return;
    for (const it of this._packCellItems(pk)) {
      if (shown) this._hiddenCells.delete(it.name);
      else this._hiddenCells.add(it.name);
    }
    this.shadowRoot.querySelectorAll(".cell-row input[data-cell]").forEach((cb) => { cb.checked = shown; });
    this._emitHiddenCells();
    this._refreshCellCount();
  }

  // Update only the "(shown/total)" count label from the current checkbox states.
  _refreshCellCount() {
    const r = this.shadowRoot;
    const boxes = r.querySelectorAll(".cell-row input[data-cell]");
    const title = r.querySelector(".cell-list-title");
    if (!boxes.length || !title) return;
    const nShown = [...boxes].filter((cb) => cb.checked).length;
    title.textContent = `Charts in this pack (${nShown}/${boxes.length})`;
  }

  _emitHiddenCells() { this._emit("cells-hidden-changed", { hidden: [...this._hiddenCells] }); }

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
    return downloadBtn(key, {
      downloading: this._activeDownloadKey === key,
      queued: this._dlQueue.some((j) => j.key === key),
    });
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
    const name = `Inland ENC · ${pk.title || pk.name || "river"}`;
    const t = this._notify ? this._notify.task("download:" + pk.key, { label: `Preparing ${name}…` }) : null;
    if (this._active) this.render();
    try {
      const cells = pk.cells.map((c) => ({ name: c.name, url: c.url }));
      await this._api.importAndWait({ set: pk.key, cells }, { name, onStatus: this._jobStatus(t) });
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
    const name = d ? `NOAA · ${d.region}` : "NOAA";
    const t = this._notify ? this._notify.task("download:" + set, { label: `Preparing ${name}…` }) : null;
    const onStatus = this._jobStatus(t);
    if (this._active) this.render();
    let ok = false;
    try {
      // The district bundle holds the whole district; bake the FULL district into the
      // pack so it's a complete set (names=all, not just the not-yet-installed ones).
      await this._api.importAndWait({ set, zipUrl: this._districtZipUrl(cg), names: all }, { name, onStatus });
      ok = true;
    } catch (e) {
      console.warn(`[pack] ${label} server bundle failed — per-cell:`, e.message);
      const cells = all.map((n) => ({ name: n, url: (this._byName.get(n) || {}).z || "" }));
      try { await this._api.importAndWait({ set, cells }, { name, onStatus }); ok = true; }
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
    return packSearch(this._cellQuery || "");
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
    return dataFreshness({ catalogDate: this._catalogDate, total: this._catalog.length.toLocaleString() });
  }

  // Uninstall any pack by set name: DELETE /api/set removes the baked pmtiles/aux
  // from the cache (source cells in the data store are kept), then re-render.
  async _uninstallSet(set) {
    if (this._uninstalling) return;
    if (!(this._installedSets && this._installedSets.has(set))) return;
    this._uninstalling = true;
    const t = this._notify ? this._notify.task("uninstall:" + set, { label: `Removing ${this._setLabel(set)}…` }) : null;
    if (t) t.progress(null);
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
      host.innerHTML = agreementModal({ encUrl: NOAA_ENC_URL, agreementUrl: NOAA_AGREEMENT_URL });
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

  // -- User-Charts import (drop a .zip / .000 / .pmtiles) --------------------
  // .zip → upload the whole exchange set; the server parses CATALOG.031, names the
  // pack from its identity, bakes it, and writes the metadata sidecar (one pack per
  // upload). .000 → upload the lone cell + auto-bake. .pmtiles → handed to the shell
  // (its client-side plotter archive path). After each, charts-changed lets the
  // shell reconcile the map.
  async openFiles(fileList) {
    const log = this.shadowRoot.getElementById("import-log");
    for (const file of fileList) {
      const lower = file.name.toLowerCase();
      try {
        if (lower.endsWith(".zip")) {
          const t = this._notify ? this._notify.task("import:zip", { label: `Importing ${file.name}…` }) : null;
          try {
            const { set } = await this._api.importZipAndWait(file, { name: file.name.replace(/\.zip$/i, ""), onStatus: this._jobStatus(t) });
            if (t) t.done();
            if (log) log.textContent = `imported ${file.name}`;
            this._selProvider = "user"; this._selPack = set; // reveal the new pack
          } catch (e) { if (t) t.fail(e); throw e; }
        } else if (lower.endsWith(".000")) {
          // Lone base cell: upload to the server cache, then bake an auto-named pack.
          const name = file.name.replace(/\.000$/i, "");
          const t = this._notify ? this._notify.task("import:cell", { label: `Importing ${name}…` }) : null;
          try {
            await this._api.uploadCell(name, new Uint8Array(await file.arrayBuffer()));
            const { job, set } = await this._api.importCells("auto", [name]);
            await this._api.pollJob(job, { name, onStatus: this._jobStatus(t) });
            if (t) t.done();
            if (log) log.textContent = `imported ${name}`;
            this._selProvider = "user"; this._selPack = set;
          } catch (e) { if (t) t.fail(e); throw e; }
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
    await this._syncRegistry();
    this._changed();
    if (this._active) this.render();
  }

  // Wire the file-import controls (the drop zone is re-rendered, so bound each render).
  _wireImport() {
    const r = this.shadowRoot;
    const file = r.getElementById("file"), drop = r.getElementById("drop"), pick = r.getElementById("pick");
    if (!file || !drop || !pick) return;
    pick.onclick = (e) => { e.stopPropagation(); file.click(); };
    // iOS/touch has no file drag-and-drop: make the whole drop zone a tap target
    // that opens the picker (the inner button still works on desktop).
    drop.onclick = () => file.click();
    file.onchange = () => { if (file.files.length) this.openFiles(file.files); file.value = ""; };
    drop.ondragover = (e) => { e.preventDefault(); drop.classList.add("over"); };
    drop.ondragleave = () => drop.classList.remove("over");
    drop.ondrop = (e) => { e.preventDefault(); drop.classList.remove("over"); if (e.dataTransfer.files.length) this.openFiles(e.dataTransfer.files); };
  }
}

customElements.define("chart-library", ChartLibrary);
