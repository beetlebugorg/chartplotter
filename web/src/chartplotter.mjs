// <chart-plotter> — the chart-management shell around <chart-canvas>.
//
// <chart-canvas> is a pure renderer; this wraps it with the UI a user needs to
// actually run a plotter: discover charts on a map (NOAA catalog cell boxes),
// import them with no backend (drop a .zip / .000, unzipped in-browser to OPFS),
// manage what's installed, and set S-52 mariner options.
//
//   <script type="module" src="chartplotter-app.mjs"></script>
//   <chart-plotter-app></chart-plotter-app>
//
// Attributes (all optional, forwarded to the inner <chart-plotter>):
//   center, zoom, assets, cell-url   — see chartplotter.mjs
//   basemap   "coastline" | "osm" | "osmvec" | "none"   default "coastline"
//             (offline GSHHG); "none" disables the underlay (test charts)
//
// Everything is driven through the renderer's public API (bakePmtiles/setArchive/
// listCharts/setScheme/setMariner and its `map` handle) plus the shared ChartStore.

import "./chart-canvas/chart-canvas.mjs"; // defines <chart-canvas> (the renderer we wrap)
import "./components/pick-report.mjs"; // defines <pick-report> (the ECDIS cursor-pick panel)
import "./components/chart-library.mjs"; // defines <chart-library> (the "Charts library" domain)
import "./components/settings-dialog.mjs"; // defines <settings-dialog> (the settings panel host)
import { SettingsRegistry } from "./app/settings-registry.mjs"; // contribution registry for the settings panel
import { coreSettingsContributions } from "./app/core-settings.mjs"; // the app's own display settings as contributions
import { DevTools } from "./components/dev-tools.mjs"; // the slim contributed Advanced-tab dev tools (rebake + feature inspector)
import { DISTRICTS, NOAA_ENC_URL } from "./components/chart-library.mjs"; // NOAA CG-district packs + ENC page (shared)
import { ChartDownloader } from "./data/chart-downloader.mjs"; // chart discovery + acquisition
import { NotificationCenter } from "./app/notification-center.mjs"; // app-level task-progress + banner bus
import { ChartService } from "./data/chart-service.mjs"; // server import/bake jobs + pack registry
import { AuxStore } from "./data/aux-store.mjs"; // TXTDSC/PICREP external files (companion aux zip)
import { ChartStore } from "./data/chart-store.mjs";
import { UNIT_DEFAULTS } from "./lib/units.mjs"; // configurable display units (categories now in core-settings.mjs)
import { ChartFinder } from "./map/chart-finder.mjs"; // off-screen installed-chart edge pointers
import { HudController } from "./map/hud.mjs"; // status readout + overscale zoom cap
import { CoverageBoxes } from "./map/coverage-boxes.mjs"; // installed-chart coverage overlay
import { SearchBox } from "./map/search-box.mjs"; // offline catalog + chart-feature search
import { BANDS, BAND_LABEL, BAND_COLOR, BAND_MINZOOM, DEV_BANDS, bandForScale } from "./lib/bands.mjs";
import { loadJSON, maxZoomForScaleFloor, freshness, fmtIssue, fmtMB, isShareUrl, parseViewHash, copyText, flashBtn } from "./lib/util.mjs";
import { archivePut, archiveGet } from "./data/archive-store.mjs";
import { STYLE, CHROME } from "./chartplotter.view.mjs"; // shell chrome (CSS + static markup)

const SCHEMES = ["day", "dusk", "night"];
const SCHEME_LABEL = { day: "Day", dusk: "Dusk", night: "Night" };
const LS_SCHEME = "chartplotter:scheme";
const LS_BASEMAP = "chartplotter:basemap"; // "coastline" (offline) | "osm" (online) | "osmvec" | "none" (disabled)
const LS_MARINER = "chartplotter:mariner";
// Canonical mariner defaults — the single source of truth on the client, kept in
// step with the engine's defaultMarinerSettings() (pkg/s52/mariner_settings.go).
// Depths are stored in METRES (the renderer's depth expressions are all metric);
// the settings form converts to feet for display only. Per S-52 these are the
// internally consistent set: shallow 2 < safety 10 = safetyDepth 10 < deep 30.
const DEFAULT_MARINER = {
  shallowContour: 2,
  safetyContour: 10,
  safetyDepth: 10,
  deepContour: 30,
  depthUnit: "ft", // US/NOAA preference (engine default DepthUnitFeet)
  // Display categories (S-52 §10.2). Base is the minimum safe-navigation set and
  // can NEVER be deselected by the mariner — it is forced on at boot. Default
  // display is Standard; Other is opt-in.
  displayBase: true,
  displayStandard: true,
  displayOther: false,
  boundaryStyle: "symbolized", // IMO/S-52 default (vs "plain")
  simplifiedPoints: false,     // paper-chart point symbols (engine SimplifiedPoints=false)
  fourShadeWater: true,        // four depth shades (engine TwoShades=false)
  showNoData: true,
  showScaleBoundaries: false, // DATCVR §10.1.9.1 chart scale boundaries — off by default (opt-in)
  // Individually-selectable "Other" items (S-52/IMO), all default on.
  showSoundings: true,
  // S-52 PresLib §14.5 text groupings — the mariner toggles text by group,
  // independent of display category (each TX/TE carries a group number, §14.4).
  showLightDescriptions: true, // group 23: light characteristics (e.g. Fl(2)R 10s)
  textImportant: true,         // group 11: bridge/cable/pipeline clearances, route/track bearings
  textNames: true,             // groups 21/26/29: buoy/beacon/geographic names, berth numbers
  textOther: true,             // groups 0-10/22/24/25/27/28/30/32-49: notes, seabed, mag variation, heights
  // Off by default.
  showFullSectorLines: false,        // 25mm legs (engine ShowFullLengthSectorLines=false, avoids clutter)
  showIsolatedDangersShallow: false, // ISODGR01 at DisplayBase (engine default); on → Standard category
  shallowPattern: false,
  showContourLabels: false,
  dataQuality: false,
  showMetaBounds: false,
  // Display units for non-depth quantities (distance/height/speed/wind/temp).
  // Depth has its own metric/imperial toggle (depthUnit, above). See units.mjs.
  ...UNIT_DEFAULTS,
};
const LS_VIEW = "chartplotter:view";
const LS_SOURCE = "chartplotter:source"; // {type:"blob"} or {type:"url",file}
const LS_BANDS_OFF = "chartplotter:bands-off"; // usage bands the user turned off (array of slugs)
// The NOAA ENC User Agreement gate (LS_AGREE) + agreement URLs now live in the
// <chart-library> component, which owns the download flow; NOAA_ENC_URL is
// imported above for the bottom-right attribution link the shell still renders.
// NOTE: the installed-region list is NOT cached in localStorage — the server's
// GET /api/charts manifest (one entry per baked region archive in its XDG cache)
// is the single source of truth, so the UI can never show charts that aren't
// actually on disk.

// Box colours by state (kept readable in both day and night chrome).
const STATE_FILL = { installed: "#2e7d32", archive: "#1565c0", catalog: "#000000" };

// Navigational-purpose bands (colours, zoom ranges, scale/zoom mappings) live in
// bands.mjs — imported at the top of this file.
// Chart packs = U.S. Coast Guard districts (the DISTRICTS table) now live in
// <chart-library>; the shell imports DISTRICTS from there for the few places it
// still needs the region labels (_reattachName, _setLabel — used by DevTools' rebake).

// A chart vector source: the realtime path has one "chart" source; the legacy
// pmtiles path had a "chart-<band>" source per band. (Used by the cursor pick.)
function isChartSource(s) {
  return typeof s === "string" && (s === "chart" || s.startsWith("chart-"));
}

// S-57 object-class acronym → human label (the baked `class` attribute is the
// acronym). Covers the common ENC classes; unknown acronyms fall back to raw.
const S57_CLASS = {
  ACHARE: "Anchorage area", ACHBRT: "Anchor berth", ADMARE: "Administration area", AIRARE: "Airport / airfield",
  BCNCAR: "Beacon, cardinal", BCNISD: "Beacon, isolated danger", BCNLAT: "Beacon, lateral", BCNSAW: "Beacon, safe water", BCNSPP: "Beacon, special purpose",
  BERTHS: "Berth", BOYCAR: "Buoy, cardinal", BOYINB: "Buoy, installation", BOYISD: "Buoy, isolated danger", BOYLAT: "Buoy, lateral", BOYSAW: "Buoy, safe water", BOYSPP: "Buoy, special purpose",
  BRIDGE: "Bridge", BUAARE: "Built-up area", BUISGL: "Building, single", CBLARE: "Cable area", CBLSUB: "Cable, submarine", CBLOHD: "Cable, overhead",
  CANALS: "Canal", CAUSWY: "Causeway", CGUSTA: "Coastguard station", COALNE: "Coastline", CONVYR: "Conveyor", CRANES: "Crane", CTNARE: "Caution area",
  CTRPNT: "Control point", CURENT: "Current, non-gravitational", DAMCON: "Dam", DAYMAR: "Daymark", DEPARE: "Depth area", DEPCNT: "Depth contour",
  DISMAR: "Distance mark", DMPGRD: "Dumping ground", DOCARE: "Dock area", DRGARE: "Dredged area", DRYDOC: "Dry dock", DWRTCL: "Deep water route centreline", DWRTPT: "Deep water route part",
  EXEZNE: "Exclusive economic zone", FAIRWY: "Fairway", FERYRT: "Ferry route", FLODOC: "Floating dock", FNCLNE: "Fence / wall", FOGSIG: "Fog signal",
  FORSTC: "Fortified structure", FSHFAC: "Fishing facility", FSHGRD: "Fishing ground", FSHZNE: "Fishery zone", GATCON: "Gate", GRIDRN: "Gridiron",
  HRBARE: "Harbour area", HRBFAC: "Harbour facility", HULKES: "Hulk", ICEARE: "Ice area", ISTZNE: "Inshore traffic zone", LAKARE: "Lake", LNDARE: "Land area",
  LNDELV: "Land elevation", LNDMRK: "Landmark", LNDRGN: "Land region", LIGHTS: "Light", LITFLT: "Light float", LITVES: "Light vessel", LOCMAG: "Local magnetic anomaly",
  MAGVAR: "Magnetic variation", MARCUL: "Marine farm / culture", MIPARE: "Military practice area", MORFAC: "Mooring / warping facility", MPAARE: "Marine protected area",
  NAVLNE: "Navigation line", OBSTRN: "Obstruction", OFSPLF: "Offshore platform", OSPARE: "Offshore production area", PILBOP: "Pilot boarding place", PILPNT: "Pile",
  PIPARE: "Pipeline area", PIPOHD: "Pipeline, overhead", PIPSOL: "Pipeline, submarine/on land", PONTON: "Pontoon", PRCARE: "Precautionary area", PRDARE: "Production / storage area",
  PYLONS: "Pylon / bridge support", RADLNE: "Radar line", RADRFL: "Radar reflector", RADRNG: "Radar range", RADSTA: "Radar station", RTPBCN: "Radar transponder beacon",
  RDOCAL: "Radio calling-in point", RDOSTA: "Radio station", RECTRC: "Recommended track", RCRTCL: "Recommended route centreline", RCTLPT: "Recommended traffic lane part",
  RESARE: "Restricted area", RIVERS: "River", ROADWY: "Road", RUNWAY: "Runway", SBDARE: "Seabed area", SLCONS: "Shoreline construction", SISTAT: "Signal station, traffic",
  SISTAW: "Signal station, warning", SILTNK: "Silo / tank", SLOTOP: "Slope topline", SLOGRD: "Sloping ground", SMCFAC: "Small craft facility", SNDWAV: "Sand waves",
  SOUNDG: "Sounding", SPRING: "Spring", STSLNE: "Straight territorial sea baseline", SUBTLN: "Submarine transit lane", SWPARE: "Swept area", TESARE: "Territorial sea area",
  TS_FEB: "Tidal stream", TSELNE: "Traffic separation line", TSEZNE: "Traffic separation zone", TSSBND: "Traffic separation boundary", TSSCRS: "Traffic separation crossing",
  TSSLPT: "Traffic separation lane part", TSSRON: "Traffic separation roundabout", TUNNEL: "Tunnel", TWRTPT: "Two-way route part", UWTROC: "Underwater / awash rock",
  VEGATN: "Vegetation", WATTUR: "Water turbulence", WATFAL: "Waterfall", WEDKLP: "Weed / kelp", WRECKS: "Wreck", WTWAXS: "Waterway axis", WTWPRF: "Waterway profile",
  "M_QUAL": "Quality of data", "M_COVR": "Coverage", "M_NPUB": "Nautical publication info", "M_NSYS": "Navigational system of marks", "M_SDAT": "Sounding datum", "M_VDAT": "Vertical datum",
};

// Fallback label for a chart MVT source-layer when a feature has no `class`.
const INSPECT_LAYER_LABEL = { point_symbols: "Symbol", soundings: "Sounding", lines: "Line", complex_lines: "Boundary", areas: "Area", area_patterns: "Area pattern", text: "Label" };

// Geometry-primitive rank for pick-report sorting (rule 9): points, then lines,
// then areas; labels last. (Decode/render lives in <pick-report>.)
function pickGeomRank(layer) {
  return { point_symbols: 0, soundings: 0, lines: 1, complex_lines: 1, areas: 2, area_patterns: 2, text: 3 }[layer] ?? 9;
}

// Order two picked features per S-52 PresLib §10.8.4: higher drawing priority
// first, then geometric primitive (point < line < area).
function pickCmp(a, b) {
  const pa = +(a.properties.draw_prio ?? 0), pb = +(b.properties.draw_prio ?? 0);
  if (pa !== pb) return pb - pa; // higher drawing priority (more significant) first
  return pickGeomRank(a.sourceLayer) - pickGeomRank(b.sourceLayer);
}

// Richness of a feature's geometry for *highlighting*: an area outlines better
// than a line, which outlines better than a centred symbol's anchor point. Used
// to pick which of an object's co-located representations the pick circle/outline
// should trace, so an area object highlights its extent rather than a dot.
function hiGeomRank(f) {
  const t = (f.geometry && f.geometry.type) || "";
  if (t.includes("Polygon")) return 3;
  if (t.includes("LineString")) return 2;
  if (t.includes("Point")) return 1;
  return 0;
}

export class ChartPlotter extends HTMLElement {
  constructor() {
    super();
    this.attachShadow({ mode: "open" });
    // NOAA catalogue/discovery lives in this._dl (ChartDownloader, created in
    // boot); _catalog/_byName/_districts/_catalogDate are proxy getters onto it.
    this._installed = new Set();        // all stored cell names
    this._cellError = new Map();        // name -> error message, for cells that failed to parse
    this._cellBounds = new Map();       // name -> [w,s,e,n] footprint (from the baker), to locate uploaded cells
    this._cellScale = new Map();        // name -> compilation scale (CSCL) of uploaded cells, for picking a detail zoom
    this._archive = new Map();          // name -> {blob, entry, meta} from opened zips
    this._selected = new Set();         // names ticked for import / NOAA download
    this._dlRegions = new Set();        // installed NOAA region numbers (from GET /api/charts)
    this._regionArchives = [];          // [{num,file,bounds}] — one pmtiles per installed region
    this._importedArchives = [];        // in-memory imported/uploaded archives (Blob/File), re-added on every coverage rebuild so a too-large-to-persist import isn't lost when a later provision resets the bands
    this._userBake = null;              // {cells:[…], bounds:[w,s,e,n]} of the map-selected charts-user.pmtiles, or null
    this._showCellBounds = localStorage.getItem("cp-cell-bounds") !== "0"; // coverage boxes when zoomed out past chart data (default ON; opt-out)
    this._showChartRadar = localStorage.getItem("cp-chart-radar") !== "0"; // edge pointers to off-screen installed charts (default ON)
    this._bandsOff = new Set(loadJSON(LS_BANDS_OFF, [])); // usage bands turned off (hide layers + gate the realtime baker)
    // Feature-inspect + tile-debugger state now live in DevTools (Advanced tab).
    this._hasArchive = false;           // is a chart archive currently loaded?
    this._mariner = { ...DEFAULT_MARINER, ...loadJSON(LS_MARINER, {}) };
    // Migrate the old single-value display category (base|standard|other) to
    // the multi-select Base/Standard/Other booleans (now client-side filters).
    if (this._mariner.displayCategory) {
      const c = this._mariner.displayCategory;
      this._mariner.displayBase = true;
      this._mariner.displayStandard = c === "standard" || c === "other";
      this._mariner.displayOther = c === "other";
      delete this._mariner.displayCategory;
    }
    // Migrate the old coarse "showNames" (which gated ALL non-light text) to the
    // S-52 §14.5 text-group toggles: off → hide every general text group.
    if (this._mariner.showNames === false) {
      this._mariner.textImportant = false;
      this._mariner.textNames = false;
      this._mariner.textOther = false;
    }
    delete this._mariner.showNames;
    // S-52 §10.2: Display Base is the minimum safe-navigation set and can never
    // be deselected. Force it on regardless of any (stale) persisted value.
    this._mariner.displayBase = true;
    this._scheme = localStorage.getItem(LS_SCHEME) || "day";
    if (!SCHEMES.includes(this._scheme)) this._scheme = "day"; // drop a retired scheme (e.g. bright)
    // The provision job is a SERVER task: `_task` mirrors GET /api/tasks (polled,
    // never invented), `_taskMeta` holds the client-only label hints (which region,
    // which verb) the server doesn't know. `_poll` is the polling interval handle.
    // The NOAA agreement gate + the per-pack download queue moved into the
    // <chart-library> component; the shell reads its `busy` for task gating.
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

  // NOAA catalogue/discovery state lives in the ChartDownloader (this._dl); these
  // proxies keep the shell's existing readers working. Guarded for the brief
  // window before _dl is created in boot.
  get _catalog() { return this._dl ? this._dl.catalog : []; }
  get _byName() { return this._dl ? this._dl.byName : new Map(); }
  get _districts() { return this._dl ? this._dl.districts : []; }
  get _catalogDate() { return this._dl ? this._dl.catalogDate : ""; }

  connectedCallback() {
    this.boot().catch((e) => {
      console.error("[chartplotter-app]", e);
      this.shadowRoot.innerHTML = `<div style="font:13px system-ui;padding:12px;color:#900">chart manager failed: ${e.message}</div>`;
    });
  }

  async boot() {
    // Prod (prebaked) mode: enabled by the `prod` attribute OR `?prod` (so a dev
    // build can be tested without rebuilding). Reflect it as the attribute so the
    // :host([prod]) styles apply either way.
    this._prod = this.hasAttribute("prod") || new URLSearchParams(location.search).has("prod");
    if (this._prod) this.setAttribute("prod", "");
    // Display settings (scheme · basemap · mariner toggles · cell-boundary toggle ·
    // bands-off) are persisted SERVER-side so every screen pointed at this boat's
    // server shares them and they survive a restart. Adopt them BEFORE the renderer
    // is configured below, overriding the localStorage values the constructor seeded
    // (localStorage stays as the offline cache). Prod (offline pmtiles) has no server.
    if (!this._prod) await this._loadServerSettings();
    this.renderChrome();

    // What's already stored? We do NOT eagerly load it (that's the slow part on
    // a device with many charts) — the renderer boots with an empty `charts`
    // attribute so the map/basemap paints immediately, then we ingest cells
    // lazily by viewport (see ingestViewport).
    this._store = new ChartStore();
    // Installed cells/sets are SERVER-side now (the XDG data/cache); onReady() calls
    // _renderInstalledSets() to load them (GET /tiles/ + /api/cells), so the map
    // survives a reload. Seed from the local OPFS store for the first paint.
    this._installed = new Set(await this._store.list().catch(() => []));
    this._installedSets = new Set();
    this._disabled = new Set(); // packs hidden from the map (server-side; loaded in _renderInstalledSets)
    // Chart discovery/acquisition domain (NOAA catalogue, packs, download, import).
    // Reads the installed set live via a getter (boot reassigns _installed above).
    this._dl = new ChartDownloader({
      assets: this._assets,
      cfg: (n) => this._cfg(n),
      store: this._store,
      getInstalled: () => this._installed,
    });

    // Server-side chart API (import/bake jobs + pack registry + set management):
    // one definition of every endpoint + the SSE job-progress protocol, shared
    // by the shell here and the <chart-library> component.
    this._api = new ChartService({ assets: this._assets });

    // App-level notification bus: feature components (chart-library now, plugins
    // later) post task progress + banners here instead of touching shell DOM.
    // Progress renders into the existing databox/db-prog row; messages toast.
    this._notify = new NotificationCenter({
      onProgress: (p) => this._setNotification(p),
      onMessage: (m) => this._toast(m),
    });

    // The "Charts library" domain lives in <chart-library> (mounted in the chrome
    // template inside #charts-body's slot). Inject its deps and listen for its
    // events: "charts-changed" → reconcile the main map; "chart-focus" → fly the
    // canvas to bounds; "chart-import-archive" → the shell-owned client-side
    // .pmtiles archive path (the plotter is shell-owned).
    this._chartLib = this.shadowRoot.getElementById("chart-lib");
    if (this._chartLib) {
      this._chartLib.configure({ dl: this._dl, api: this._api, notify: this._notify, store: this._store, assets: this._assets, prod: this._prod });
      this._chartLib.addEventListener("charts-changed", () => { this._renderInstalledSets().catch(() => {}); });
      this._chartLib.addEventListener("chart-focus", (e) => this._flyToBounds(e.detail && e.detail.bounds));
      this._chartLib.addEventListener("chart-import-archive", (e) => this._importArchiveFile(e.detail && e.detail.file));
    }

    // The settings panel is a HOST (<settings-dialog>) fed by a registry of
    // contributions. The app's own display settings are a set of "core"
    // contributions (see core-settings.mjs); they call the existing apply*/
    // persist methods, so persistence is unchanged. The dev tools register
    // themselves as the first NON-core contribution (a DevTools instance built in
    // onReady, once the map exists — see below); plugins will register here too.
    this._settingsRegistry = new SettingsRegistry();
    for (const c of coreSettingsContributions(this)) this._settingsRegistry.register(c);
    this._settingsDlg = this.shadowRoot.getElementById("settings-dlg");
    if (this._settingsDlg) this._settingsDlg.configure({ registry: this._settingsRegistry });

    // Catalog drives the picker AND the lazy-load gating (cell bboxes). Kick it
    // off in parallel; ingestViewport awaits it.
    this._catalogReady = this.loadCatalog();

    // Share-restore: a view-only link (#v=lon,lat,zoom[,bearing,pitch]) just
    // adopts the publisher's camera — no cells to install, nothing to download;
    // the spot renders from whatever the server/local store already holds. Strip
    // the hash afterward so a later reload resumes the user's own last view.
    // Legacy #share snapshot links still reconstruct cells via _loadSharedView.
    let shareView = parseViewHash();
    if (shareView) {
      this._sharePending = shareView; // onReady applies bearing/pitch
      try { history.replaceState(null, "", location.pathname + location.search); } catch (e) {}
    } else if (isShareUrl()) {
      shareView = await this._loadSharedView().catch((e) => { console.warn("[share] restore failed:", e); return null; });
    }

    const plotter = document.createElement("chart-canvas");
    const view = shareView || loadJSON(LS_VIEW, null); // resume the last view → load in-region
    plotter.setAttribute("center", view ? view.center.join(",") : (this.getAttribute("center") || "-76.4875,38.975"));
    plotter.setAttribute("zoom", String(view ? view.zoom : (this.getAttribute("zoom") || 11)));
    if (this.hasAttribute("cell-url")) plotter.setAttribute("cell-url", this.getAttribute("cell-url"));
    plotter.setAttribute("assets", this._assets);
    this._osmVecUrl = this._cfg("osm-pmtiles"); // hosted OSM vector basemap archive (enables the "Vector" option)
    if (this._osmVecUrl) plotter.setAttribute("osm-pmtiles", this._osmVecUrl);
    this._basemap = this._serverBasemap || localStorage.getItem(LS_BASEMAP) || this.getAttribute("basemap") || "coastline";
    if (!["coastline", "osm", "osmvec", "none"].includes(this._basemap)) this._basemap = "coastline";
    if (this._basemap === "osmvec" && !this._osmVecUrl) this._basemap = "coastline"; // vector not configured
    plotter.setAttribute("basemap", this._basemap);
    // Render source. Prod (hosted): prebaked per-region .pmtiles, loaded by
    // restoreArchive through the renderer's pmtiles path — no tile server needed.
    // Local serve: server-baked MVT from /tiles/{set} (server mode); imported /
    // downloaded cells are baked on the server (POST /api/import) and the renderer
    // is pointed at the resulting set (see _refreshCharts).
    if (!this._prod) plotter.setAttribute("tiles", "server");
    this._plotter = plotter;
    this.shadowRoot.getElementById("map").appendChild(plotter);

    plotter.addEventListener("ready", (e) => this.onReady(e.detail.map), { once: true });
  }

  // Background tile baking + lazy cell parsing (the constant on-pan work) is shown
  // only by the subtle hairline load bar at the top of the map — not the
  // notification pill, which is reserved for discrete jobs (download / import /
  // remove) so it doesn't pulse continuously while panning.
  _updateBakeStatus() {
    if (!this.shadowRoot) return;
    this._updateLoadBar();
  }

  // Subtle "loading more while data is shown" cue: a thin indeterminate bar at the
  // top of the map. Shown only when there's bake/parse activity AND data is already
  // on screen (cold start keeps the louder pill/welcome). Debounced — delay-in
  // ~150ms so instant bakes don't flash it; linger ~300ms after idle.
  _updateLoadBar() {
    const want = this._bakeInflight > 0 && this._hasArchive;
    if (want === this._loadBarWant) return; // only act on a change of desired state
    this._loadBarWant = want;
    clearTimeout(this._loadBarTimer);
    this._loadBarTimer = setTimeout(() => {
      const bar = this.shadowRoot && this.shadowRoot.getElementById("load-bar");
      if (bar) bar.classList.toggle("on", want);
    }, want ? 100 : 300);
  }

  // [w,s,e,n] footprint of a cell: the baker's reported bounds (uploaded cells) or
  // the catalog footprint (NOAA cells), else null.
  _cellLocation(name) {
    const b = this._cellBounds.get(name);
    if (Array.isArray(b) && b.length === 4) return b;
    const c = this._byName.get(name);
    if (c && Array.isArray(c.bb) && c.bb.length === 4) return c.bb;
    return null;
  }


  // Fly to a set of packs from a tapped chart-finder chip. A single pack lands at
  // its finest band's render zoom (so a berthing-only set actually draws); a
  // cluster just fits all of them so they come into view (and split into their own
  // chips / coverage boxes from there).
  _flyToPacks(packs) {
    const map = this._map;
    if (!map || !packs || !packs.length) return;
    let w = Infinity, s = Infinity, e = -Infinity, n = -Infinity, finest = -1;
    for (const p of packs) {
      const [pw, ps, pe, pn] = p.bounds;
      w = Math.min(w, pw); s = Math.min(s, ps); e = Math.max(e, pe); n = Math.max(n, pn);
      const fb = p.bands && p.bands[p.bands.length - 1];
      finest = Math.max(finest, BANDS.indexOf(fb));
    }
    const need = packs.length === 1 ? (BAND_MINZOOM[BANDS[finest]] || 12) + 0.3 : 0;
    const cam = map.cameraForBounds([[w, s], [e, n]], { padding: 80 });
    const zoom = Math.min(18, Math.max(cam ? cam.zoom : Math.max(need, 9), need));
    // Raise the dynamic zoom cap to the target FIRST — we're flying from open water
    // (low cap) into the pack's coverage, so without this the fly clamps short and a
    // berthing-only set wouldn't reach the zoom where it renders. _updateZoomCap
    // recomputes at the destination (which has the charts) and won't yank back.
    if (map.getMaxZoom() < zoom) map.setMaxZoom(zoom);
    map.flyTo({ center: cam ? cam.center : [(w + e) / 2, (s + n) / 2], zoom, duration: 1200 });
  }

  // Show/hide the chart radar (off-screen chart pointers). App-level setting,
  // persisted locally + server-side (shared across screens).
  _setChartRadarVisible(on) {
    this._showChartRadar = on;
    localStorage.setItem("cp-chart-radar", on ? "1" : "0");
    this._persistSettings();
    if (this._chartFinder) this._chartFinder.setVisible(on);
  }

  // A deploy-time config value from an attribute, overridable per-load by the
  // same-named query param (so a hosted build can be re-pointed without a rebuild,
  // e.g. ?pmtiles=… or ?catalog=…).
  _cfg(name) {
    const q = new URLSearchParams(location.search).get(name);
    return q != null ? q : (this.getAttribute(name) || "");
  }

  loadCatalog() {
    // NOAA chart catalogue + hosted-archive manifest — owned by the downloader.
    // Once the manifest settles it may name a companion aux zip (TXTDSC/PICREP
    // external files); load it so the pick report can show that content inline.
    this._aux = new AuxStore();
    const dl = this._dl.loadCatalog().then(async (r) => {
      // Prod/hosted: a companion aux.zip named in the manifest (fetched whole once).
      // Server: per-file on demand via GET api/aux — the raw zip is never exposed.
      if (this._dl.auxUrl) await this._aux.load(this._dl.auxUrl);
      else await this._aux.loadApi(this._assets);
      return r;
    });
    // S-57 object/attribute catalogue for the cursor-pick report (decodes class
    // and attribute names, enumerated values and units — S-52 PresLib §10.8).
    // Independent of the chart catalogue; the report degrades to raw acronyms if absent.
    this._s57cat = { classes: {}, attributes: {} };
    fetch(this._assets + "s57-catalogue.json")
      .then((r) => (r.ok ? r.json() : null)).then((j) => { if (j) this._s57cat = j; }).catch(() => {});
    return dl;
  }

  async onReady(map) {
    this._map = map;
    this._resolveReady();
    // Share-restore carries bearing/pitch too (center+zoom were applied as the
    // initial camera). The installed cells were already added to _installed in
    // boot(), so the loadStoreCells below bakes them at the restored viewport.
    if (this._sharePending) {
      const v = this._sharePending; this._sharePending = null;
      try { map.jumpTo({ bearing: v.bearing || 0, pitch: v.pitch || 0 }); } catch (e) { console.warn("[share] camera", e); }
    }
    // Apply persisted display prefs.
    if (this._scheme !== "day") this._plotter.setScheme(this._scheme);
    this.setAttribute("data-scheme", this._scheme);
    if (Object.keys(this._mariner).length) {
      try { this._plotter.setMariner(this._mariner); } catch (e) { console.warn(e); }
    }
    await this._catalogReady;
    this.addCatalogOverlay(map);
    // The plotter rebuilds the whole style (setStyle) when server sets load or the
    // SCAMIN buckets refresh, wiping every app-added overlay (coverage boxes, pick &
    // inspect highlights). Re-apply them after each rebuild, and repopulate the
    // coverage boxes — otherwise they vanish the moment a set renders.
    map.on("style.load", () => {
      this.addCatalogOverlay(map);
      this._refreshInstalledBounds();
    });
    await this.restoreArchive();
    // Local serve: render every baked pack the server holds (survives reload). Prod
    // already loaded its prebaked archives in restoreArchive() above.
    if (!this._prod) {
      try { await this._renderInstalledSets(); } catch (e) { console.warn("[charts] initial render", e); }
    }
    this._applyBandsOff(); // re-apply any persisted band on/off now that chart layers exist
    this.updateEmptyState();
    if (this._chartLib) this._chartLib.refresh();
    this._assessCoverage();
    // If the user opened Charts before the renderer was ready (the drawer's
    // already on the charts panel), make sure the panel paints + the map sizes.
    if (this._section === "charts" && this._drawerOpen()) {
      if (this._chartLib) this._chartLib.show(this._chartLib._selProvider || "noaa");
      map.resize();
    }
    // Refresh-resume: if a provision job is still running on the server, re-attach
    // (show the pill + start polling). A finished/idle task is ignored.
    this._reattachTask();

    // HUD / status readout (band·scale·zoom·position + warning band) and the
    // overscale zoom cap — own controller; owns the `move` listener for the readout
    // and does an initial draw + cap. updateZoomCap is driven from moveend below.
    this._hud = new HudController({
      map,
      root: this.shadowRoot,
      getInstalled: () => this._installed,
      cellMeta: (name) => this._byName.get(name),
      serverSetMetas: () => (this._plotter && this._plotter.serverSetMetas) ? this._plotter.serverSetMetas() : [],
      noChartsEnabled: () => this._noChartsEnabled(),
    });

    // Developer tools (Advanced tab) — the first NON-core contributor to the
    // settings registry. A plain class (like the map controllers), built now that
    // the map exists; it registers itself as the Advanced-tab contribution and owns
    // the rebake + feature-inspector tools (and their map listeners). Dev-only.
    if (!this._prod) {
      this._devTools = new DevTools({
        registry: this._settingsRegistry,
        map,
        plotter: this._plotter,
        api: this._api,
        notify: this._notify,
        assets: this._assets,
        isBusy: () => this._taskRunning() || (this._chartLib && this._chartLib.busy),
        setProgress: (p) => this._setProgress(p),
        setTask: (running) => { this._task = running ? { kind: "download", status: "running" } : null; },
        pollImport: (job, onProg, label) => this._pollImport(job, onProg, label),
        districtCellNames: (cg) => this._districtCellNames(cg),
        setLabel: (name) => this._setLabel(name),
        chartLib: () => this._chartLib,
        renderInstalledSets: () => this._renderInstalledSets(),
        s57Label: (acr) => S57_CLASS[acr],
        layerLabel: (srcLayer) => INSPECT_LAYER_LABEL[srcLayer],
        onInspectOn: () => { this._closePick(); this._cancelAreaSelect(); },
      });
    }

    // Persist the view so a refresh resumes where you were; refresh the coverage
    // panel's in-view cell list for the new viewport.
    map.on("moveend", () => {
      this.saveView();
      this._assessCoverage();
      this._hud.updateZoomCap(); // clamp zoom-in to the finest band covering the new view
    });

    // Off-screen chart pointers: edge pointers to installed charts not in view
    // (its own module; owns its overlay + map listener). Fed from pack metadata.
    this._chartFinder = new ChartFinder({
      host: this.shadowRoot.getElementById("chart-finder"),
      map,
      getPacks: () => this._packsMeta || [],
      getUnits: () => this._mariner,
      labelFor: (name) => this._setLabel(name),
      onPick: (packs) => this._flyToPacks(packs),
      visible: this._showChartRadar,
    });

    // Close any pinned band-pill popup when clicking elsewhere (pill/cell clicks
    // stopPropagation, so this only fires for clicks outside them). Also tuck the
    // on-map search back into its tab when clicking away while it's empty (map
    // clicks bubble out of the renderer's shadow root to here).
    this.shadowRoot.addEventListener("click", (e) => {
      this.shadowRoot.querySelectorAll(".sb-band-wrap.open").forEach((w) => w.classList.remove("open"));
      const search = this.shadowRoot.getElementById("search");
      if (search && !search.hidden) {
        const onSearch = e.composedPath().some((n) => n === search || (n.id === "search-tab"));
        if (!onSearch) {
          search.hidden = true;
          this.shadowRoot.getElementById("search-tab").classList.remove("on");
        }
      }
    });
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
      // _applyArchives sets _hasArchive for ANY coverage — region archives, the
      // map-selected bake, or a restored import — so key the loaded/empty state
      // off that, not just regions (otherwise box-selected charts read as "no
      // charts" and the welcome card stays up).
      if (this._hasArchive) {
        loaded = true;
        const frames = [...(regions || [])];
        if (this._userBake && this._userBake.bounds && !this._isWorldBounds(this._userBake.bounds)) frames.push({ bounds: this._userBake.bounds });
        if (frames.length && !loadJSON(LS_VIEW, null)) this._frameRegionArchives(frames);
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
    // (the cell-picker "charts mode" was removed — this is just the no-saved-view guard)
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
    // loadRegions() RESET every band, so re-add imported/uploaded archives too —
    // otherwise a box-select (or region add/remove) re-applying coverage would
    // drop them from the map. Prefer the in-memory copies (survive even when too
    // large to persist to IndexedDB); fall back to the persisted blob on a fresh
    // reload where the in-memory list is empty.
    if (this._importedArchives.length) {
      for (const a of this._importedArchives) {
        try { await this._plotter.addArchive(a); this._hasArchive = true; } catch (e) { console.warn(e); }
      }
    } else {
      const src = loadJSON(LS_SOURCE, null);
      if (src && src.type === "blob") {
        try { const b = await archiveGet(); if (b) { await this._plotter.addArchive(b); this._importedArchives.push(b); this._hasArchive = true; } } catch (e) { console.warn(e); }
      }
    }
    // loadRegions() RESETS every band source, so the drag-a-box bake
    // (charts-user.pmtiles, a separate single archive) must be APPENDED here to
    // survive alongside the region archives. Defensive: ignore an absent file.
    try {
      const r = await fetch("charts/charts-user.json?t=" + Date.now());
      if (r.ok) {
        const j = await r.json();
        if (j && Array.isArray(j.cells) && j.cells.length) {
          await this._plotter.addArchive("charts/charts-user.pmtiles?t=" + Date.now(), "all");
          this._userBake = { cells: j.cells, bounds: Array.isArray(j.bounds) ? j.bounds : null };
          this._hasArchive = true;
        } else {
          this._userBake = null;
        }
      } else {
        this._userBake = null;
      }
    } catch {}
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

  // -- Charts: open the drawer on the Charts library ------------------------
  // Open the Charts drawer on the given provider ("noaa"|"ienc"|"user"); the
  // <chart-library> component owns everything inside the panel.
  openCharts(provider) {
    this._section = "charts";
    this.shadowRoot.querySelectorAll(".panel").forEach((p) => p.classList.toggle("sel", p.dataset.panel === "charts"));
    this.shadowRoot.getElementById("empty").hidden = true;
    if (this._chartLib) this._chartLib.show(provider || "noaa");
    this.setDrawerOpen(true);
  }

  // Fly the main map to a [w,s,e,n] bounds (the <chart-library> chart-focus event).
  _flyToBounds(b) {
    if (!this._map || !Array.isArray(b) || b.length !== 4) return;
    this._map.fitBounds([[b[0], b[1]], [b[2], b[3]]], { padding: 60, maxZoom: 14, duration: 800 });
  }

  // Add a dropped .pmtiles to the loaded coverage (the client-side plotter path,
  // shell-owned because <chart-library> has no plotter handle). Reads only the
  // header + directory; tiles stream from the File. Persists in the background.
  async _importArchiveFile(file) {
    if (!file || !this._plotter) return;
    try {
      await this._plotter.addArchive(file);
      this._importedArchives.push(file); // keep in memory so a coverage rebuild can re-add it
      this._markArchive({ type: "blob" });
      this.closeDrawer();
      archivePut(file).catch((e) => console.warn("[archive] persist failed (too large for IndexedDB?)", e));
    } catch (e) { console.error("[archive] import", e); }
  }

  // -- background provision task (server-owned, client-observed) -----------
  // Provisioning runs on the SERVER as a background job; the client starts it
  // (POST /api/provision), then POLLS GET /api/tasks. `_task` is a pure mirror of
  // that poll — closing/refreshing the page never cancels the job, and on boot we
  // re-attach to whatever's running (see `_reattachTask`). The persistent pill +
  // drawer progress card both render from `_task`.

  // The NOAA ENC User Agreement gate (shown before any download) moved into the
  // <chart-library> component, which owns the download flow. The shell's bottom-
  // right "Terms" link + Escape handler reach into it (_chartLib._showAgreement /
  // ._resolveAgreement / .agreementOpen).

  // On boot, re-attach to a job that's still running on the server (refresh-
  // resume — no client-side job persistence). A finished/idle task is ignored so
  // a stale "done" never shows a phantom pill.
  // Refresh-resume: a bake / download / rebuild may still be running server-side
  // after a page refresh (we no longer hold the job id). Ask the server what's
  // running and, if anything is, RE-ATTACH — stream its progress back into the
  // notification pill and refresh the installed charts when it finishes.
  async _reattachTask() {
    let j = null;
    try { j = await fetch(`${this._assets}api/import/status?t=${Date.now()}`).then((r) => (r.ok ? r.json() : null)); }
    catch { return; } // no server / transient
    if (!j || j.state !== "running" || !j.id) return;
    this._task = { kind: "download", status: "running" };
    const name = this._reattachName(j.set);
    this._setProgress({ label: "Working", pill: `Working on ${name}`, sub: "", frac: j.percent ? j.percent / 100 : null });
    this._pollImport(j.id, (p) => this._setProgress(p), name).catch(() => {}).then(async () => {
      this._task = null;
      this._setProgress(null);
      try { await this._renderInstalledSets(); } catch (e) { /* ignore */ }
      if (this._plotter && this._plotter.flushTiles) { try { await this._plotter.flushTiles(); } catch (e) { /* ignore */ } }
      if (this._chartLib) this._chartLib.refresh();
    });
  }

  // Friendly region/river name from a (band-)set name, for the re-attached job label.
  _reattachName(set) {
    const m = /^noaa-d(\d+)/.exec(set || "");
    if (m) { const d = DISTRICTS.find((x) => x.cg === +m[1]); return d ? d.region : `District ${m[1]}`; }
    const ie = /^ienc-(.+)/.exec(set || "");
    if (ie) return ie[1].replace(/-(overview|general|coastal|approach|harbor|berthing)$/, "");
    return set || "charts";
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
    if (this._chartLib) this._chartLib.refresh();
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
    // No per-item detail to surface for the server-side bake — just the pill +
    // bar (so no notification drop-down; see _setNotification's `sub` gate).
    if (removing)
      return { pill: `Removing ${name || ""}`.trim(), label: "Removing region…", sub: "", frac };
    return { pill: `Importing ${name || "charts"}`, label: "Importing charts…", sub: "", frac };
  }

  // Project `_task` to the persistent pill + (when the drawer's open) the
  // progress card. The pill is visible whatever the drawer is doing; tapping it
  // opens the region (or the Charts drawer).
  _renderTaskUI() {
    const d = this._taskDisplay();
    // All job progress (download/import/bake/remove) flows through _setProgress,
    // which paints the bottom data card's slot (always visible) and the in-drawer
    // card. The compact card line uses the short pill text; the drawer keeps the
    // fuller label + sub.
    this._setProgress(d ? { label: d.label, pill: d.pill, sub: d.sub, frac: d.frac, error: d.error } : null);
    // Keep the Charts panel's MUTATING buttons (Remove/Disable/Enable) disabled
    // while a (shell-driven server) job runs, without a full re-render each poll
    // tick (which would flicker / collapse the import panel). Download buttons are
    // intentionally left alone — they're queue-managed (_downloadBtnHtml) so you
    // can queue more while one runs. Those buttons live in <chart-library>'s shadow
    // root now, so reach in. The completed-state re-render happens in _onTaskDone.
    if (this._section === "charts" && this._drawerOpen() && this._chartLib && this._chartLib.shadowRoot) {
      const busy = this._taskRunning();
      this._chartLib.shadowRoot.querySelectorAll(".pk-btn:not([data-getpack])").forEach((b) => { b.disabled = busy; });
    }
    // A running download means charts are inbound — don't show the empty-state
    // welcome over the map (and restore it if the task failed with no coverage).
    this.updateEmptyState();
  }

  // The whole "Charts library" panel body (provider→pack→detail drill-down,
  // search, preview, import, agreement) now lives in <chart-library>; the shell
  // mounts that element inside #charts-body. The shell keeps only the few
  // discovery delegators the dev tools still need (below).

  // -- chart packs (Coast Guard districts) ---------------------------------
  // The cells in a district pack (every catalog cell tagged with that `cg`). Kept
  // in the shell because the dev "rebuild all" tool (_rebuildAllPerBand) uses it.
  _districtCellNames(cg) { return this._dl.districtCellNames(cg); }

  // Human label for a set name (provider · pack).
  _setLabel(name) {
    const m = /^noaa-d(\d+)$/.exec(name);
    if (m) { const d = DISTRICTS.find((x) => x.cg === +m[1]); return d ? `NOAA · ${d.region}` : `NOAA · District ${m[1]}`; }
    if (name === "import") return "Imported charts";
    const ie = /^ienc-(.+)$/.exec(name);
    if (ie) return `IENC · ${ie[1]}`;
    return name;
  }

  // Tear down an armed/active box-drag (no-op now the drag-a-box selector is
  // gone, but still called on Charts-mode exit / section switch).
  _cancelAreaSelect() { if (this._areaCleanup) this._areaCleanup(); }

  // Public: open the viewport on a position at a target paper scale or zoom — a
  // shell-level entry to the <chart-plotter> primitive (see its setView for the
  // option shape). The map's move/moveend then update the readout and persist the
  // view as if the user had navigated there. Examples:
  //   app.setView({ lat: 37.81, lng: -122.45, scale: 20000 })   // 1:20,000
  //   app.setView({ lat: 37.81, lng: -122.45, zoom: 14, animate: true })
  setView(opts) {
    return this._plotter ? this._plotter.setView(opts) : null;
  }

  saveView() {
    // The cell-picker "charts mode" (whose zoomed-out framing we used to skip
    // persisting) was removed; the live view is always the one to save.
    const c = this._map.getCenter();
    try { localStorage.setItem(LS_VIEW, JSON.stringify({ center: [c.lng, c.lat], zoom: this._map.getZoom() })); } catch {}
  }

  // The map's region-highlight layer (outline of the region open in the drawer)
  // plus the area-select overlay: every catalog cell footprint (shown only in
  // selection mode), the already-selected cells (blue fill), and a live amber
  // preview of the cells the in-progress drag box will grab.
  addCatalogOverlay(map) {
    // Idempotent: the plotter REBUILDS the style (setStyle) on every server-set
    // change and SCAMIN-bucket refresh, which drops all these app-added sources/
    // layers. A style.load handler (see onReady) re-invokes this against the fresh
    // style; the guard makes a redundant call (when the overlay is still present) a
    // no-op so we never double-add.
    if (map.getSource("focus")) return;
    const empty = { type: "FeatureCollection", features: [] };
    map.addSource("focus", { type: "geojson", data: empty });
    map.addLayer({ id: "focus-fill", type: "fill", source: "focus", paint: { "fill-color": "#1565c0", "fill-opacity": 0.12 } });
    map.addLayer({ id: "focus-line", type: "line", source: "focus", paint: { "line-color": "#1565c0", "line-width": 2.5 } });
    // All catalog cells, shown only while selecting. `sel`=1 → already chosen.
    map.addSource("selcells", { type: "geojson", data: empty });
    map.addLayer({ id: "selcells-line", type: "line", source: "selcells", layout: { visibility: "none" }, paint: { "line-color": "#1565c0", "line-opacity": 0.42, "line-width": 0.6 } });
    map.addLayer({ id: "selcells-fill", type: "fill", source: "selcells", filter: ["==", ["get", "sel"], 1], layout: { visibility: "none" }, paint: { "fill-color": "#1565c0", "fill-opacity": 0.18 } });
    map.addLayer({ id: "selcells-sel-line", type: "line", source: "selcells", filter: ["==", ["get", "sel"], 1], layout: { visibility: "none" }, paint: { "line-color": "#1565c0", "line-width": 1.3 } });
    // Live preview of cells under the current drag box.
    // Feature inspector highlight (the picked feature's geometry).
    map.addSource("inspect", { type: "geojson", data: empty });
    map.addLayer({ id: "inspect-fill", type: "fill", source: "inspect", filter: ["==", ["geometry-type"], "Polygon"], paint: { "fill-color": "#ff5252", "fill-opacity": 0.12 } });
    map.addLayer({ id: "inspect-line", type: "line", source: "inspect", filter: ["!=", ["geometry-type"], "Point"], paint: { "line-color": "#ff5252", "line-width": 2.5 } });
    map.addLayer({ id: "inspect-pt", type: "circle", source: "inspect", filter: ["==", ["geometry-type"], "Point"], paint: { "circle-radius": 11, "circle-color": "rgba(255,82,82,0.15)", "circle-stroke-color": "#ff5252", "circle-stroke-width": 2.5 } });
    // Focused-feature highlight (one picked from the area list) — cyan, on top of
    // the dim red set, so you can see exactly which polygon is which.
    map.addSource("inspect-focus", { type: "geojson", data: empty });
    map.addLayer({ id: "inspect-focus-fill", type: "fill", source: "inspect-focus", filter: ["==", ["geometry-type"], "Polygon"], paint: { "fill-color": "#00e5ff", "fill-opacity": 0.25 } });
    map.addLayer({ id: "inspect-focus-line", type: "line", source: "inspect-focus", filter: ["!=", ["geometry-type"], "Point"], paint: { "line-color": "#00b8d4", "line-width": 3.5 } });
    map.addLayer({ id: "inspect-focus-pt", type: "circle", source: "inspect-focus", filter: ["==", ["geometry-type"], "Point"], paint: { "circle-radius": 13, "circle-color": "rgba(0,229,255,0.25)", "circle-stroke-color": "#00b8d4", "circle-stroke-width": 3 } });
    // ECDIS cursor-pick highlight (the picked feature's geometry) — accent-coloured,
    // distinct from the dev inspector's red. See _pickReportAt (S-52 PresLib §10.8).
    map.addSource("pick", { type: "geojson", data: empty });
    map.addLayer({ id: "pick-fill", type: "fill", source: "pick", filter: ["==", ["geometry-type"], "Polygon"], paint: { "fill-color": "#ffb300", "fill-opacity": 0.18 } });
    map.addLayer({ id: "pick-line", type: "line", source: "pick", filter: ["!=", ["geometry-type"], "Point"], paint: { "line-color": "#ff8f00", "line-width": 3 } });
    map.addLayer({ id: "pick-pt", type: "circle", source: "pick", filter: ["==", ["geometry-type"], "Point"], paint: { "circle-radius": 12, "circle-color": "rgba(255,179,0,0.18)", "circle-stroke-color": "#ff8f00", "circle-stroke-width": 3 } });
    // Installed-cell coverage: at zooms BELOW a cell's native band (where its
    // chart detail isn't baked yet) draw its footprint + name, so when zoomed out
    // you can tell WHAT coverage you have, not just that you have some. One set of
    // layers per band, auto-hidden at the band's native min zoom (maxzoom) — where
    // the real chart takes over.
    // Installed-chart coverage overlay (own controller — owns the inst-bounds source
    // + its box/outline layers + the per-zoom min-size growth + click-to-fly).
    this._coverage = this._coverage || new CoverageBoxes({ map, visible: this._showCellBounds });
    this._coverage.addLayers();
    // ECDIS-style crosshair cursor over the chart so it's clear the pointer is a
    // pick point (a click runs the cursor pick / district preview). The dev feature
    // inspector (now in DevTools) sets its own cursor + owns the hover/click/
    // SHIFT+drag listeners; its click handler runs only while inspecting.
    map.getCanvas().style.cursor = "crosshair";
    map.on("click", (e) => {
      // The dev feature inspector (DevTools) owns clicks while it's armed — defer to
      // it so a pick/coverage tap doesn't fire under an active inspect lock.
      if (this._devTools && this._devTools.inspecting) return;
      // (The Charts cell-picker tap-to-preview-a-district branch was removed with
      // the main-map cell picker; the <chart-library> panel is the chart surface.)
      // Zoomed out over an installed-chart coverage marker → fly to that chart at
      // its detail zoom (so you can find + open installed charts without knowing
      // where/at what zoom they live). Otherwise the default ECDIS cursor pick.
      if (this._coverage && this._coverage.tapFlyTo(e.point)) return;
      // Default chart-view interaction: ECDIS cursor pick (S-52 PresLib §10.8).
      this._pickReportAt(e.point, e.originalEvent);
    });
  }

  // Fetch the latest shared snapshot and install its cells locally, downloading
  // any not already stored through the server (which serves them from its cache —
  // including bytes the publisher uploaded — or fetches the NOAA url). Returns the
  // snapshot's camera ({center,zoom,...}) for boot() to use as the initial view;
  // bearing/pitch are stashed for onReady. Cells are added to _installed so the
  // normal loadStoreCells/lazy-bake path renders them.
  async _loadSharedView() {
    const resp = await fetch("api/share", { cache: "no-store" });
    if (!resp.ok) throw new Error("snapshot HTTP " + resp.status);
    const snap = await resp.json();
    const cells = Array.isArray(snap.cells) ? snap.cells : [];
    for (const cell of cells) {
      const n = typeof cell === "string" ? cell : (cell && cell.n);
      if (!n) continue;
      try {
        if (!(await this._store.has(n))) {
          const z = cell && cell.z ? cell.z : "";
          const url = "api/cell/" + encodeURIComponent(n) + (z ? "?url=" + encodeURIComponent(z) : "");
          const r = await fetch(url);
          if (!r.ok) throw new Error("HTTP " + r.status);
          await this._store.put(n, new Uint8Array(await r.arrayBuffer()));
        }
        this._installed.add(n);
      } catch (e) { console.warn("[share] install cell", n, e); }
    }
    const view = snap.view || null;
    this._sharePending = view; // onReady applies bearing/pitch
    console.log("[share] restored", cells.length, "cell(s)");
    return view;
  }

  // Re-apply persisted band on/off to the plotter once its layers exist (on boot
  // and after any archive (re)load). Idempotent; also seeds the plotter's hidden
  // set so a later style rebuild keeps the bands off.
  _applyBandsOff() {
    if (!this._plotter) return;
    for (const band of DEV_BANDS) this._plotter.setBandVisible(band, !this._bandsOff.has(band));
  }

  // --- ECDIS cursor pick (S-52 PresLib §10.8) -------------------------------
  // Report on the chart feature(s) under a tapped point. Queries the rendered
  // tiles (so it returns only visible objects — rule 7), dedupes, and sorts by
  // drawing priority then geometry primitive (rule 9), then hands the stack to
  // the <pick-report> panel, which decodes + renders it. `ev` is the originating
  // DOM event (its clientX/Y anchor the panel's out-of-the-way placement).
  _pickReportAt(point, ev) {
    const map = this._map;
    if (!map) return;
    const feats = map.queryRenderedFeatures(point).filter((f) => isChartSource(f.source) && !f.layer.id.startsWith("scaminprobe"));
    // Collapse the per-source-layer representations of one S-57 object — its area
    // fill, boundary line and centred symbol arrive as separate features that all
    // share class/cell/objnam/s57 — into a single pick entry, so stepping the
    // report walks real-world objects rather than draw primitives. Each object
    // keeps its highest-priority representation as the displayed row (§10.8.4
    // ordering is unchanged) plus the richest geometry under the cursor
    // (area > line > point) on `_hiGeom`, so the highlight traces an area's
    // extent instead of dropping a dot on a centred symbol's anchor.
    const groups = new Map();
    for (const f of feats) {
      const p = f.properties || {};
      const key = (p.class || "") + "|" + (p.cell || "") + "|" + (p.s57 || "") + "|" + (p.objnam || "");
      const g = groups.get(key);
      if (!g) { groups.set(key, { feat: f, hi: f }); continue; }
      if (pickCmp(f, g.feat) < 0) g.feat = f;        // higher drawing priority wins the report row
      if (hiGeomRank(f) > hiGeomRank(g.hi)) g.hi = f; // richer primitive wins the highlight
    }
    const uniq = [];
    for (const g of groups.values()) { g.feat._hiGeom = g.hi.geometry; uniq.push(g.feat); }
    if (!uniq.length) { this._closePick(); return; }
    uniq.sort(pickCmp);
    const el = this._ensurePickEl();
    if (!el) return; // <pick-report> module not loaded (degrade quietly)
    el.setCatalogue(this._s57cat);
    el.setAux(this._aux);
    el.setUnits(this._mariner); // heights/ranges/speeds in the mariner's chosen units
    el.show(uniq, ev ? { x: ev.clientX, y: ev.clientY } : null);
  }

  // Create the cursor-pick panel on first use and bridge it to the map highlight.
  // Returns null if <pick-report> hasn't been defined (module failed to load).
  _ensurePickEl() {
    if (this._pickEl) return this._pickEl;
    const el = document.createElement("pick-report");
    if (typeof el.show !== "function") { console.warn("[pick] <pick-report> not loaded"); return null; }
    this.shadowRoot.appendChild(el);
    el.addEventListener("pick-feature", (e) => {
      const f = e.detail && e.detail.feature;
      const geom = f ? (f._hiGeom || f.geometry) : null; // trace the object's extent, not the symbol anchor
      const src = this._map && this._map.getSource("pick");
      if (src) src.setData({ type: "FeatureCollection", features: geom ? [{ type: "Feature", properties: {}, geometry: geom }] : [] });
    });
    el.addEventListener("pick-close", () => this._clearPickHi());
    this._pickEl = el;
    return el;
  }

  _closePick() {
    if (this._pickEl) this._pickEl.hide();
    this._clearPickHi();
  }

  _clearPickHi() {
    const src = this._map && this._map.getSource("pick");
    if (src) src.setData({ type: "FeatureCollection", features: [] });
  }

  // The all-cells selection overlay (_cellsFC/_setCellOverlay/_refreshCellSel) and
  // the tap-to-preview cell picker were removed with the main-map cell picker. The
  // selcells source/layers are still added by addCatalogOverlay (left hidden), so
  // re-adding them is harmless; nothing drives them any more.

  // -- chart-select map mode ----------------------------------------------
  // Charts view turns the map into a dedicated selection surface: the rendered
  // ENC symbology is hidden so only the offline basemap (land/sea) + the catalog
  // cell boxes show, framed out to a wide overview, with box-select armed. Home
  // restores the chart render and the view you came from.

  // The main-map cell picker (cell-box overlay + tap-to-preview-a-district) was
  // removed: the <chart-library> panel is the one chart surface now. Closing the
  // drawer/leaving the charts section no longer toggles a map mode; _clearFocus()
  // still clears the focus highlight via _clearFocus below.

  // A cell is "installed" when locally imported (OPFS) OR its NOAA region is
  // downloaded (one pmtiles per region — so region membership IS installed-ness).
  stateOf(name) {
    if (this._installed.has(name)) return "installed";
    const c = this._byName.get(name);
    if (c && Array.isArray(c.rg) && c.rg.some((n) => this._dlRegions.has(n))) return "installed";
    if (this._archive.has(name)) return "archive";
    return "catalog";
  }

  // (legacy) focus a single chart cell — kept for reference; superseded by
  // the map drag-a-box selector's highlight.
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

  // A not-downloaded in-view cell was tapped in the coverage HUD: open the Charts
  // library (on NOAA) so the user can find + download the pack. The old main-map
  // district preview went away with the cell picker.
  _downloadCellRegion(name) {
    this.openCharts("noaa");
  }

  // The User-Charts local-file import (openFiles + the OPFS upload/bake path) now
  // lives in <chart-library>; a dropped .pmtiles is handed back to the shell via
  // the "chart-import-archive" event (see _importArchiveFile). The shell's debug
  // tools still re-bake the local store through the component's _refreshCharts.

  // Render every baked tile set the server has (GET /tiles/) — each a provider/pack
  // (noaa-d17, ienc-…, import). This is the single source of truth for what's
  // installed, so it makes the map survive a reload and reflects exactly what the
  // server holds. Also rebuilds the installed-cell set (GET /api/cells) for the
  // pack-card counts. Returns the set names.
  async _renderInstalledSets() {
    // /api/packs is the single source of truth: every baked pack + its enabled
    // state (server-side, in <data>/prefs.json). Render only the enabled ones;
    // disabled packs stay baked on disk but off the map.
    const packs = await this._api.packs();
    // Re-index aux content (server mode): a just-imported district's TXTDSC/PICREP
    // files become resolvable in the pick report without a page reload.
    if (this._aux && !this._dl.auxUrl) this._aux.loadApi(this._assets).catch(() => {});
    const cells = await this._api.cells();
    if (cells) this._installed = cells; // null → keep current view
    // Management keys on the DISTRICT name (noaa-d5); enable/disable/remove hit the
    // district and the server fans to its band-sets.
    this._installedSets = new Set(packs.map((p) => p.name));
    this._disabled = new Set(packs.filter((p) => !p.enabled).map((p) => p.name));
    this._packsMeta = packs; // {name,enabled,bands,bounds} — drives the coverage boxes (incl. disabled packs)
    // Rendering needs each enabled district's PER-BAND tile sets (noaa-d5-general …),
    // listed in `bands` ("all" for a bandless/merged pack → the bare set name).
    const active = packs.filter((p) => p.enabled).flatMap((p) =>
      (p.bands && p.bands.length ? p.bands : ["all"]).map((b) => (b === "all" ? p.name : `${p.name}-${b}`)));
    if (this._plotter) await this._plotter.setServerSets(active);
    this._hasArchive = active.length > 0;
    this.updateEmptyState();
    this._refreshInstalledBounds();
    if (this._chartFinder) this._chartFinder.update(); // packs changed → recompute off-screen pointers
    return active;
  }

  // Re-bake the local OPFS store on the server (the User-Charts import path). The
  // <chart-library> component owns this; the shell's debug tools delegate to it.
  // Returns a resolved promise when there's no component yet (boot/prod guards).
  _refreshCharts() {
    return this._chartLib ? this._chartLib._refreshCharts() : Promise.resolve();
  }

  // Wait for a server job (download/bake) to complete, surfacing progress through
  // prog({label,sub,frac}). Prefers a single Server-Sent-Events stream (one
  // connection, server pushes on change) and falls back to polling if EventSource
  // is unavailable or the stream drops. Resolves with the final status; throws on
  // error/timeout.
  // Wait for a server job, surfacing progress via prog({label,sub,frac}).
  // Delegates to ChartService (SSE stream + polling fallback + phase→verb mapping).
  _pollImport(job, prog = () => {}, name) { return this._api.pollJob(job, { name, onStatus: prog }); }

  // (The server download+bake helper (_serverFetch) moved into <chart-library>
  // with the download flow; the shell uses _pollImport for its own re-attach +
  // dev-rebuild paths.)

  // Footprint + render-start zoom for each installed cell, from the catalog —
  // drives lazy loading (which cells a tile needs) and the coverage outlines.
  // NOAA region (rg) → bounding box, unioned from every catalog cell that HAS a
  // footprint. Used to bound cells whose own bbox is missing (≈195 small-scale
  // overview charts in NOAA's catalog have no <vertex> coverage) to their region
  // instead of the whole world — otherwise a no-bb Alaska overview cell overlaps
  // every tile and lazy-loads everywhere. Cached after first build.
  _regionBBoxes() {
    if (this._rgBBox) return this._rgBBox;
    if (!this._catalog || !this._catalog.length) return new Map(); // catalog not ready — don't cache empty
    const m = new Map();
    for (const c of this._catalog) {
      if (!Array.isArray(c.bb) || c.bb.length !== 4 || !Array.isArray(c.rg)) continue;
      for (const r of c.rg) {
        const cur = m.get(r);
        if (!cur) m.set(r, c.bb.slice());
        else { cur[0] = Math.min(cur[0], c.bb[0]); cur[1] = Math.min(cur[1], c.bb[1]); cur[2] = Math.max(cur[2], c.bb[2]); cur[3] = Math.max(cur[3], c.bb[3]); }
      }
    }
    this._rgBBox = m;
    return m;
  }

  // Rebuild the installed-cell coverage outlines (shown when zoomed out past a
  // cell's detail zoom) from the browser store + catalog footprints.
  _refreshInstalledBounds() {
    if (!this._coverage) return; // coverage overlay not set up yet
    const feats = [];
    const missing = []; // foreign cells with no footprint yet → parse one once
    // Per-CELL footprints are the prod (pmtiles) path only. In SERVER mode we draw
    // one box per ENABLED pack (below) instead — a full NOAA install has thousands
    // of cells, and a box per cell (re-projected to its min on-screen size on every
    // zoom frame) would freeze the map. Per-cell boxes also ignore the enabled flag,
    // so they'd keep showing a disabled district's coverage. Per-pack boxes fix both.
    if (this._prod) {
      for (const name of this._installed) {
        const bb = this._cellLocation(name); // catalog bb OR a parsed foreign footprint
        if (!bb) {
          if (!this._byName.has(name) && !(this._foreignTried && this._foreignTried.has(name))) missing.push(name);
          continue;
        }
        const c = this._byName.get(name);
        const scale = (c && typeof c.s === "number" && c.s) || this._cellScale.get(name) || 0;
        const band = scale ? bandForScale(scale) : "harbor"; // unknown scale → assume large-scale
        const [w, s, e, n] = bb;
        feats.push({
          type: "Feature",
          properties: { name, band },
          geometry: { type: "Polygon", coordinates: [[[w, s], [e, s], [e, n], [w, n], [w, s]]] },
        });
      }
    }
    // Server mode: the per-cell footprints above come from the (retired) wasm baker,
    // so they're empty here. Add ONE coverage box per ENABLED pack from /api/packs
    // (which carries each pack's union bounds + bands). Tag with the pack's COARSEST
    // band (bands[0], coarse→fine from the server) for the click-to-fly zoom + the
    // band-capped fill. DISABLED packs render nothing on the map, so they get no
    // boundary either. An enabled full NOAA stack (overview/general band) hides its
    // fill at coarse zoom (no stray box); a standalone set keeps its box until you
    // zoom into its detail.
    for (const p of this._packsMeta || []) {
      if (!p.enabled) continue; // disabled packs aren't drawn → no coverage box
      const bb = p.bounds;
      if (!Array.isArray(bb) || bb.length !== 4) continue;
      const coarsest = (p.bands && p.bands[0]) || "harbor";
      const band = BANDS.includes(coarsest) ? coarsest : "harbor"; // "all"/unknown → large-scale
      const [w, s, e, n] = bb;
      feats.push({
        type: "Feature",
        properties: { name: p.name, band, status: "ready" },
        geometry: { type: "Polygon", coordinates: [[[w, s], [e, s], [e, n], [w, n], [w, s]]] },
      });
    }
    // Hand the TRUE footprints to the coverage controller, which pushes them with a
    // per-zoom minimum on-screen size so a tiny cell never shrinks to an invisible
    // speck when zoomed out.
    if (this._coverage) this._coverage.setFeatures(feats);
    // Foreign cells carry no catalog footprint, so they'd be invisible when zoomed
    // out. Parse each one's bounds ONCE (best-effort) then rebuild, so an uploaded
    // harbour shows a coverage outline at z0 you can see and zoom into.
    if (missing.length) {
      this._foreignTried = this._foreignTried || new Set();
      missing.forEach((n) => this._foreignTried.add(n));
      this._ensureForeignBounds(missing).then(() => this._refreshInstalledBounds()).catch(() => {});
    }
  }

  // Show/hide the installed-chart coverage overlay.
  _setCellBoundsVisible(on) {
    this._showCellBounds = on;
    localStorage.setItem("cp-cell-bounds", on ? "1" : "0");
    this._persistSettings();
    if (this._coverage) this._coverage.setVisible(on);
  }

  // Single progress surface for every job (download / import / bake / remove).
  // Activity is signalled ONLY by the top notification pill (a non-blocking "work
  // is happening" indicator that expands for details); the bottom data card stays
  // a static nav readout. Pass null to clear.
  // The single entry point for all job updates (download / import / bake / remove).
  // There is no progress surface in the bottom data card any more — every job is
  // signalled purely by the top notification pill (lit indicator + % in its detail
  // drop-down). The bottom card stays a static nav readout.
  _setProgress(p) { this._setNotification(p); }

  // Transient banner for NotificationCenter messages (download failures, "GPS
  // lost", etc.) that aren't task progress. Appends an auto-dismissing toast to
  // the #toasts stack; falls back to the console if the stack isn't mounted.
  _toast(m) {
    const msg = (m && m.msg) || "";
    const level = (m && m.level) || "info";
    const stack = this.shadowRoot && this.shadowRoot.getElementById("toasts");
    if (level === "error") console.error("[notify]", msg); else if (level === "warn") console.warn("[notify]", msg); else console.log("[notify]", msg);
    if (!stack || !msg) return;
    const el = document.createElement("div");
    el.className = `toast ${level}`;
    el.textContent = msg;
    stack.appendChild(el);
    setTimeout(() => { el.classList.add("out"); setTimeout(() => el.remove(), 300); }, level === "error" ? 6000 : 3500);
  }

  // Job progress (download / import / bake) lives in a row ABOVE the live nav
  // readout inside the bottom status card — one box, no separate pill or pop-out.
  // `p` carries { label, pill, sub, frac, error }; null clears the row. The label
  // and detail are packed onto one line so all the context (region · cell · count
  // · size) is visible at a glance; the bar shows the fraction (indeterminate when
  // unknown). Spacing is handled by the card's flex gap + the divider rule.
  _setNotification(p) {
    const r = this.shadowRoot;
    const box = r.getElementById("databox");
    const prog = r.getElementById("db-prog");
    if (!box || !prog) return;
    if (!p) {
      prog.hidden = true;
      prog.classList.remove("busy", "error");
      return;
    }
    box.hidden = false; // a job can finish before the map readout first paints
    const done = p.frac === 1 || !!p.error;
    prog.hidden = false;
    prog.classList.toggle("busy", !done); // spinner while working
    prog.classList.toggle("error", !!p.error);
    const detail = p.sub && p.sub.trim() ? p.sub.trim() : "";
    const label = p.label || p.pill || "";
    r.getElementById("db-prog-label").textContent = detail ? `${label} · ${detail}` : label;
    r.getElementById("db-prog-pct").textContent = p.frac != null ? `${Math.round(p.frac * 100)}%` : "";
    const fill = r.getElementById("db-prog-fill");
    fill.style.width = p.frac != null ? `${Math.round(p.frac * 100)}%` : "100%";
    fill.classList.toggle("indet", p.frac == null && !done); // sweeping bar when no fraction
  }

  // For installed cells with no catalog footprint (foreign uploads), parse their
  // bounds in the wasm (no bake) so we can frame/locate them. Capped so a huge
  // import doesn't stall; the rest still become locatable once they bake.
  async _ensureForeignBounds(names, cap = 60) {
    if (!this._plotter || !this._plotter.cellBounds || !this._store) return;
    let n = 0;
    for (const name of names) {
      if (n >= cap) break;
      if (this._cellLocation(name)) continue; // already have a footprint
      n++;
      try {
        const bytes = await this._store.getBytes(name);
        if (!bytes) continue;
        const res = await this._plotter.cellBounds(name, bytes instanceof Uint8Array ? bytes : new Uint8Array(bytes));
        const bb = res && res.bounds;
        if (Array.isArray(bb) && bb.length === 4 && bb[0] <= bb[2]) {
          this._cellBounds.set(name, bb);
          if (res.scale) this._cellScale.set(name, res.scale);
        }
      } catch (e) { /* best-effort */ }
    }
  }

  // Frame to the union bounds of the installed region archives (from the manifest).
  // A degenerate full-world bbox (some bakes write one) — useless to frame to.
  _isWorldBounds(b) {
    return Array.isArray(b) && b[0] <= -179.5 && b[1] <= -84 && b[2] >= 179.5 && b[3] >= 84;
  }
  _frameRegionArchives(regions) {
    let W = Infinity, S = Infinity, E = -Infinity, N = -Infinity, any = false;
    for (const x of regions || []) {
      const b = x.bounds;
      if (Array.isArray(b) && b.length === 4) {
        W = Math.min(W, b[0]); S = Math.min(S, b[1]); E = Math.max(E, b[2]); N = Math.max(N, b[3]); any = true;
      }
    }
    // (The cell-picker "charts mode" guard was removed with the picker.)
    if (any && this._map) this._map.fitBounds([[W, S], [E, N]], { padding: 60, maxZoom: 14, duration: 800 });
  }

  // The archive import path (importSelected / rebakeArchive / installCell) moved
  // into <chart-library>, which owns the OPFS store upload + server bake and
  // dispatches "charts-changed" when it finishes.

  // -- settings ------------------------------------------------------------

  // The display-settings blob shared with the server. Scheme/basemap/cell-bounds
  // toggle/bands-off plus the mariner (S-52) settings — everything that's a pure
  // client-side restyle. View (camera) is per-screen, so it's NOT included.
  _settingsBlob() {
    return {
      scheme: this._scheme,
      basemap: this._basemap,
      showCellBounds: this._showCellBounds,
      chartRadar: this._showChartRadar,
      bandsOff: [...this._bandsOff],
      mariner: this._mariner,
    };
  }

  // Fetch the server-persisted display settings at boot and adopt them over the
  // localStorage values the constructor seeded. Best-effort: an older server, an
  // offline/prod load, or a malformed blob just keeps the local values.
  async _loadServerSettings() {
    let s = null;
    try {
      const r = await fetch(`${this._assets}api/settings`, { cache: "no-store" });
      if (r.ok) s = await r.json();
    } catch (e) { /* offline / older server → keep local */ }
    if (!s || typeof s !== "object") return;
    if (typeof s.scheme === "string" && SCHEMES.includes(s.scheme)) this._scheme = s.scheme;
    if (typeof s.basemap === "string" && ["coastline", "osm", "osmvec", "none"].includes(s.basemap)) this._serverBasemap = s.basemap;
    if (typeof s.showCellBounds === "boolean") this._showCellBounds = s.showCellBounds;
    if (typeof s.chartRadar === "boolean") this._showChartRadar = s.chartRadar;
    if (Array.isArray(s.bandsOff)) this._bandsOff = new Set(s.bandsOff);
    // Merge mariner over the (migrated) defaults; Display Base is always forced on.
    if (s.mariner && typeof s.mariner === "object") this._mariner = { ...this._mariner, ...s.mariner, displayBase: true };
  }

  // Persist the display settings server-side (shared across screens). Debounced so
  // a flurry of toggles coalesces into one POST. Server mode only — prod has no
  // server, and localStorage already holds the per-screen copy.
  _persistSettings() {
    if (this._prod) return;
    clearTimeout(this._settingsSaveT);
    this._settingsSaveT = setTimeout(() => {
      fetch(`${this._assets}api/settings`, {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify(this._settingsBlob()),
      }).catch(() => { /* best-effort; localStorage is the offline fallback */ });
    }, 400);
  }

  applyScheme(name) {
    this._scheme = name;
    this._plotter.setScheme(name);
    this.setAttribute("data-scheme", name);
    localStorage.setItem(LS_SCHEME, name);
    this._persistSettings();
    this._syncSchemeUI();
  }

  // Basemap under the chart: "coastline" (offline GSHHG land/lakes) or "osm"
  // (online OpenStreetMap raster).
  applyBasemap(mode) {
    this._basemap = (mode === "osm" || mode === "osmvec" || mode === "none") ? mode : "coastline";
    if (this._plotter) this._plotter.setBasemap(this._basemap);
    localStorage.setItem(LS_BASEMAP, this._basemap);
    this._persistSettings();
  }

  // Cycle Day → Dusk → Night → Day from the tab-bar toggle.
  _cycleScheme() {
    const i = SCHEMES.indexOf(this._scheme);
    this.applyScheme(SCHEMES[(i + 1) % SCHEMES.length]);
  }

  // Inner SVG for the scheme toggle, reflecting the current scheme: sun (day),
  // sun-over-horizon (dusk), moon (night).
  _schemeSvg(s) {
    if (s === "night") return `<path d="M21 12.8A9 9 0 1 1 11.2 3a7 7 0 0 0 9.8 9.8Z"/>`;
    if (s === "dusk") return `<path d="M3 18h18M5.5 18a6.5 6.5 0 0 1 13 0"/><path d="M12 3v3M4 8l1.6 1.6M20 8l-1.6 1.6M2.5 13H4M20 13h1.5"/>`;
    return `<circle cx="12" cy="12" r="4.2"/><path d="M12 2v2.4M12 19.6V22M2 12h2.4M19.6 12H22M4.6 4.6l1.7 1.7M17.7 17.7l1.7 1.7M19.4 4.6l-1.7 1.7M6.3 17.7l-1.7 1.7"/>`;
  }

  // Keep the tab-bar toggle icon and the Settings segmented control in step with
  // the active scheme (either entry point can change it).
  _syncSchemeUI() {
    const r = this.shadowRoot; if (!r) return;
    const svg = r.getElementById("scheme-svg");
    if (svg) svg.innerHTML = this._schemeSvg(this._scheme);
    const tog = r.getElementById("scheme-toggle");
    if (tog) tog.title = `Colour scheme: ${SCHEME_LABEL[this._scheme]} — tap to cycle`;
    // The scheme picker moved into <settings-dialog>; reflect a tab-bar cycle there.
    this._settingsDlg && this._settingsDlg.refresh();
  }

  applyMariner(patch) {
    this._mariner = { ...this._mariner, ...patch };
    // Every mariner setting is an instant client-side restyle/filter (no
    // re-bake), so just apply the changed key(s) and persist.
    try { this._plotter.setMariner(patch); }
    catch (e) { console.warn(e); }
    localStorage.setItem(LS_MARINER, JSON.stringify(this._mariner));
    this._persistSettings();
    // Switching units relabels + reconverts the depth fields (still in metres
    // under the hood), so redraw the settings panel.
    if ("depthUnit" in patch) this._settingsDlg && this._settingsDlg.refresh();
  }

  // -- chrome / panels -----------------------------------------------------
  renderChrome() {
    const r = this.shadowRoot;
    r.innerHTML = `<style>${STYLE}</style>${CHROME}`;

    // wiring
    const $ = (id) => r.getElementById(id);
    // The corner round buttons are the whole nav: search top-left; charts · scheme
    // · settings top-right. Each section toggles its drawer (clicking the active
    // one closes it). Dev tools live behind a button at the top of Settings.
    $("charts-btn").onclick = () => this.toggleSection("charts");
    $("settings-btn").onclick = () => this.toggleSection("settings");
    $("close").onclick = () => this.closeDrawer();
    $("scheme-toggle").onclick = () => this._cycleScheme();
    this._syncSchemeUI(); // paint the toggle's initial icon
    // Escape closes the topmost open dialog/overlay (one per press). The
    // cursor-pick report closes itself (its own captured handler runs first).
    window.addEventListener("keydown", (e) => {
      if (e.key !== "Escape") return;
      const root = this.shadowRoot;
      // The NOAA agreement modal lives in <chart-library>; cancel it if open.
      if (this._chartLib && this._chartLib.agreementOpen) { this._chartLib._resolveAgreement(false); return; }
      const search = root.getElementById("search");
      if (search && !search.hidden) { search.hidden = true; root.getElementById("search-tab").classList.remove("on"); return; }
      if (this._drawerOpen()) { this.closeDrawer(); return; }
    });
    $("empty-add").onclick = () => this.openCharts();
    $("empty-import").onclick = () => this.openCharts("user");
    // Attribution "Terms" link → the agreement modal owned by <chart-library>.
    $("attr-terms").onclick = () => { if (this._chartLib) this._chartLib._showAgreement(); };

    // Search (offline, over catalog titles + loaded chart feature data) — the logic
    // + results rendering live in the SearchBox controller; the input + flyout are
    // shell chrome, so the handlers here just delegate. The map is late-bound (set
    // in onReady), so SearchBox reads it lazily via getMap.
    const si = $("search-input");
    this._search = new SearchBox({
      getMap: () => this._map,
      getResultsEl: () => $("search-results"),
      getInput: () => $("search-input"),
      getSearchPop: () => $("search"),
      getSearchTab: () => $("search-tab"),
      getCatalog: () => this._catalog,
      isChartSource,
      classLabel: (acr) => S57_CLASS[acr],
      layerLabel: (srcLayer) => INSPECT_LAYER_LABEL[srcLayer],
      positionCaret: (pop, tab) => this._positionCaret(pop, tab),
    });
    const closeSearch = () => { $("search").hidden = true; $("search-tab").classList.remove("on"); };
    const openSearch = () => { $("search").hidden = false; $("search-tab").classList.add("on"); this._search.position(); si.focus(); };
    $("search-tab").onclick = () => ($("search").hidden ? openSearch() : closeSearch());
    si.oninput = () => this._search.doSearch(si.value);
    si.onkeydown = (e) => {
      if (e.key === "Enter") this._search.gotoHit(0);
      else if (e.key === "Escape") { si.value = ""; closeSearch(); }
    };
    si.onfocus = () => { if (si.value.trim().length >= 2) this._search.doSearch(si.value); };
    // The settings panel (<settings-dialog>) renders lazily when opened via
    // toggleSection("settings") → dlg.show(); it's configured after the shadow DOM
    // is built (see boot/init), so there's nothing to pre-render here.
  }

  // Rail-icon → drawer section. Clicking the icon of the already-open section
  // closes the drawer; otherwise it opens (or switches) to that section.
  toggleSection(name) {
    const open = this.shadowRoot.getElementById("drawer").classList.contains("open");
    if (open && this._section === name) { this.closeDrawer(); return; }
    this._cancelAreaSelect(); // drop any armed box-drag when switching sections
    this._section = name;
    const r = this.shadowRoot;
    r.querySelectorAll(".panel").forEach((p) => p.classList.toggle("sel", p.dataset.panel === name));
    r.getElementById("drawer").classList.toggle("wide", name === "charts"); // two-pane list+map
    r.getElementById("drawer").classList.toggle("set-wide", name === "settings"); // rail + content
    r.getElementById("dtitle").textContent = name === "settings" ? "Settings" : (this._prod ? "Add charts" : "Chart library");
    // Charts = the <chart-library> panel; open it on its current provider.
    if (name === "charts" && this._chartLib) this._chartLib.show(this._chartLib._selProvider || "noaa");
    // Feature-inspect is NOT auto-armed; it's a button inside the Advanced (dev)
    // tab. Any section switch disarms it (DevTools owns the inspect state).
    if (this._devTools) this._devTools.setInspectMode(false);
    if (name === "settings" && this._settingsDlg) {
      this._settingsDlg.show(this._settingsDlg.activeTab || "general");
    }
    this.setDrawerOpen(true);
  }

  closeDrawer() {
    // Free the <chart-library> detail-pane OSM preview map (it owns it now).
    if (this._chartLib) this._chartLib.teardownPreview();
    this.setDrawerOpen(false);
  }

  // Slide the panel sheet up/down from the tab bar. data-sec drives its per-section
  // size (Charts wide+short, Settings/Dev tall); set before opening so it animates
  // in at the right size.
  // Point a popover's caret at the tab it opened from. Sets --caret-left (mobile,
  // caret on the bottom edge) or --caret-top (desktop, caret on the left edge) to
  // the tab's centre, clamped to the popover's edges. Measures the popover with its
  // pop-in transform removed so the rect is the final resting position.
  _positionCaret(pop, tab) {
    if (!pop || !tab) return;
    const tr = tab.getBoundingClientRect();
    const prev = pop.style.transform; pop.style.transform = "none";
    const pr = pop.getBoundingClientRect();
    pop.style.transform = prev;
    // Panels drop DOWN from their corner button, caret on the top edge: set its
    // horizontal offset to the button's centre, clamped to the panel's edges.
    pop.style.setProperty("--caret-left", `${Math.max(18, Math.min(pr.width - 18, tr.left + tr.width / 2 - pr.left))}px`);
  }


  setDrawerOpen(open) {
    const r = this.shadowRoot;
    const drawer = r.getElementById("drawer");
    if (open && this._section) drawer.dataset.sec = this._section;
    drawer.classList.toggle("open", open);
    // Charts opens from its own button; Settings + its Dev sub-view both anchor on
    // (and light up) the Settings button.
    r.getElementById("charts-btn").classList.toggle("on", open && this._section === "charts");
    r.getElementById("settings-btn").classList.toggle("on", open && this._section === "settings");
    if (open) {
      const tabId = this._section === "charts" ? "charts-btn" : "settings-btn";
      this._positionCaret(drawer, r.getElementById(tabId));
    }
    // Closing the drawer clears the region-focus highlight + any armed box drag,
    // and disarms feature-inspect (crosshair/box-zoom) so it doesn't linger over
    // the bare map. (The old cell-picker "charts mode" exit is gone with the picker.)
    if (!open) { this._cancelAreaSelect(); this._clearFocus(); if (this._devTools) this._devTools.setInspectMode(false); }
    this.updateEmptyState(); // the welcome card hides while the drawer is open
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
    // Also hide it whenever the drawer is open — the user is already in Charts/
    // Settings, so the centred "no charts yet" card would just float over them.
    // And DON'T show it when charts ARE installed but merely all disabled — that's
    // not an empty library, it's a deliberate state (surfaced as a bottom-bar
    // warning instead, see _updateHud / _noChartsEnabled).
    if (el) el.hidden = this._hasArchive || this._hasInstalledPacks() || this._drawerOpen() || !!(this._task && this._task.status === "running");
    if (this._hud) this._hud.updateHud(); // refresh the "no charts enabled" bottom-bar warning
  }

  // Are any chart packs installed on the server (enabled OR disabled)? Distinguishes
  // "empty library → Welcome aboard" from "library has charts, all turned off".
  _hasInstalledPacks() { return !!(this._installedSets && this._installedSets.size); }

  // Installed packs exist but none render (all disabled) → nothing on the map.
  _noChartsEnabled() { return !this._prod && this._hasInstalledPacks() && !this._hasArchive; }

  // The archive-list rendering + file-import wiring (renderArchiveList /
  // _wireImport) moved into <chart-library>, which owns the User-Charts import UI.

  // The settings panel is now the <settings-dialog> host fed by core-settings.mjs
  // contributions (see the boot wiring + applyScheme/applyBasemap/applyMariner).
  // The old inline renderSettings()/_applySettingSeg() builders + their local
  // depthRow/toggle/unitRow/segRow helpers are gone.
}

// Pure formatters/util now live in util.mjs; the archive blob store in
// archive-store.mjs; band maths in bands.mjs (all imported at the top).
customElements.define("chart-plotter", ChartPlotter);
