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
const LS_AREACELLS = "chartplotter:areacells"; // names of cells picked by drag-a-box area selection
const LS_SELBANDS = "chartplotter:selbands"; // navigational-purpose bands enabled in the map selector
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
// Native min Web-Mercator zoom per band (matches CHART_BANDS in chartplotter.mjs).
// Below it a cell's chart detail isn't baked, so we draw its coverage outline.
const BAND_MINZOOM = { overview: 0, general: 7, coastal: 9, approach: 11, harbor: 13, berthing: 16 };

// Curated quick-pick regions for the Charts selector. NOAA's catalog has no
// human-readable region names (only the 20 numeric `rg` ENC regions), so these
// are defined by bounding box [w,s,e,n] over the catalog; clicking one selects
// every cell it covers (band-filtered) and frames the map there. `all:true`
// (Entire US) selects the whole catalog. Boxes intentionally overlap at edges
// (e.g. Florida) — a cell can belong to two regions. The Pacific box reaches
// past the antimeridian (negative wrapped lng, as the catalog stores it) to
// catch Guam/Mariana/Wake alongside Hawaii.
const REGIONS = [
  { id: "ec", name: "US East Coast", bb: [-82, 23.5, -65, 45.5] },
  { id: "gulf", name: "Gulf Coast", bb: [-98, 24, -80.5, 31] },
  { id: "wc", name: "US West Coast", bb: [-128, 31, -116.5, 49.5] },
  { id: "gl", name: "Great Lakes", bb: [-93, 40.5, -74, 49.5] },
  { id: "ak", name: "Alaska", bb: [-190, 50, -129, 73] },
  { id: "pac", name: "Hawaii & Pacific", bb: [-230, 0, -150, 30] },
  { id: "car", name: "Caribbean", bb: [-68.5, 16.5, -64, 19.2] },
  { id: "all", name: "Entire US", all: true, bb: [-180, 13, -60, 74] },
];

// Escape text for safe innerHTML insertion (inspector panel renders feature
// properties straight from the tiles).
function esc(s) {
  return String(s).replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]));
}

// Does a GeoJSON geometry intersect the lon/lat box [W,S,E,N]? Points test exactly;
// lines/polygons use a bbox-overlap approximation (fine for the area inspector).
function geomIntersectsBox(g, W, S, E, N) {
  if (!g) return false;
  if (g.type === "Point") {
    const c = g.coordinates;
    return c[0] >= W && c[0] <= E && c[1] >= S && c[1] <= N;
  }
  const bb = [Infinity, Infinity, -Infinity, -Infinity];
  (function walk(c) {
    if (typeof c[0] === "number") {
      if (c[0] < bb[0]) bb[0] = c[0];
      if (c[1] < bb[1]) bb[1] = c[1];
      if (c[0] > bb[2]) bb[2] = c[0];
      if (c[1] > bb[3]) bb[3] = c[1];
    } else for (const x of c) walk(x);
  })(g.coordinates || []);
  return !(bb[2] < W || bb[0] > E || bb[3] < S || bb[1] > N);
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
    this._importedArchives = [];        // in-memory imported/uploaded archives (Blob/File), re-added on every coverage rebuild so a too-large-to-persist import isn't lost when a later provision resets the bands
    this._userBake = null;              // {cells:[…], bounds:[w,s,e,n]} of the map-selected charts-user.pmtiles, or null
    this._selBands = this._loadSelBands(); // navigational-purpose bands enabled in the selector (Set of band slugs)
    this._inspectMode = false;          // feature-inspect mode (toggled from the statusbar)
    this._inspectLocked = false;        // a feature is pinned (click-to-lock) — hover stops updating
    this._inspectLastKey = "";          // last rendered hover/lock key, to skip redundant re-renders
    this._inspectFeats = [];            // the stack of features under the cursor (cycler steps through them)
    this._inspectIdx = 0;               // which one of the stack is shown
    this._inspectMulti = false;         // true after a SHIFT+drag area capture (list all, no cycler)
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
    this._areaCells = new Set();        // selected cells — seeded from what's downloaded on load (see _seedAreaCells)
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
    plotter.setAttribute("tiles", "realtime"); // 100%-wasm: bake tiles in-browser from stored cells
    this._plotter = plotter;
    this.shadowRoot.getElementById("map").appendChild(plotter);

    plotter.addEventListener("ready", (e) => this.onReady(e.detail.map), { once: true });
    plotter.addEventListener("bake-activity", (e) => this._onBakeActivity(e.detail.inflight));
  }

  // Reflect live wasm tile-baking in the statusbar. inflight = tiles currently
  // baking in the worker (0 = idle). Hide promptly when idle, but debounce the
  // hide a touch so the indicator doesn't strobe between back-to-back tiles.
  _onBakeActivity(inflight) {
    const el = this.shadowRoot && this.shadowRoot.getElementById("bake-status");
    if (!el) return;
    this._bakeInflight = inflight;
    if (inflight > 0) {
      clearTimeout(this._bakeHideT); this._bakeHideT = 0;
      el.querySelector(".sb-bake-txt").textContent = inflight > 1 ? `Generating ${inflight} tiles…` : "Generating tile…";
      el.hidden = false;
    } else if (!this._bakeHideT) {
      this._bakeHideT = setTimeout(() => { this._bakeHideT = 0; if (!this._bakeInflight) el.hidden = true; }, 250);
    }
  }

  loadCatalog() {
    // Optional — the picker just shows nothing if absent. Also load the hosted
    // per-district archive manifest (charts-index.json, written by --bake-districts).
    const cat = fetch(this._assets + "catalog.json")
      .then((r) => (r.ok ? r.json() : null)).then((j) => { this._catalogDate = (j && j.date) || ""; return (j && j.cells) || []; }).catch(() => [])
      .then((cells) => {
        // NOAA catalog titles are HTML-encoded (e.g. "Hawai&#39;i"). Decode once
        // to plain text so esc() can re-encode them safely for display instead of
        // double-encoding the entity into a literal "&#39;".
        const ta = document.createElement("textarea");
        const decode = (s) => { if (!s || s.indexOf("&") < 0) return s; ta.innerHTML = s; return ta.value; };
        for (const c of cells) { if (c.l) c.l = decode(c.l); }
        this._catalog = cells; for (const c of cells) this._byName.set(c.n, c);
      });
    const man = fetch(this._assets + "charts-index.json")
      .then((r) => (r.ok ? r.json() : null)).then((j) => { this._districts = (j && j.districts) || []; }).catch(() => { this._districts = []; });
    return Promise.all([cat, man]);
  }

  async onReady(map) {
    this._map = map;
    this._resolveReady();
    // Apply persisted display prefs.
    if (this._scheme !== "day") this._plotter.setScheme(this._scheme);
    this.setAttribute("data-scheme", this._scheme);
    if (Object.keys(this._mariner).length) {
      try { this._plotter.setMariner(this._mariner); } catch (e) { console.warn(e); }
    }
    await this._catalogReady;
    this.addCatalogOverlay(map);
    await this.restoreArchive();
    // 100%-wasm path: bake whatever cells are already stored (imported offline).
    try {
      const rt = await this._plotter.loadStoreCells();
      this._refreshInstalledBounds();
      if (rt && rt.ok && rt.names && rt.names.length) { this._hasArchive = true; this.updateEmptyState(); }
    } catch (e) { console.warn("[realtime] loadStoreCells", e); }
    await this._seedAreaCells();
    this.updateEmptyState();
    this.renderCharts();
    this._assessCoverage();
    // If the user opened Charts before the renderer was ready (the drawer's
    // already on the charts panel), engage selection mode now that the map exists
    // — otherwise they'd be left in a half state (panel open, map not framed). The
    // drawer's resize already fired (map was null then), so size + frame directly.
    if (this._section === "charts" && this._drawerOpen()) {
      this._enterChartsMode();
      this._pendingChartsFrame = false;
      map.resize();
      this._frameChartsWorld();
    }
    // Refresh-resume: if a provision job is still running on the server, re-attach
    // (show the pill + start polling). A finished/idle task is ignored.
    this._reattachTask();

    // Persist the view so a refresh resumes where you were; refresh the coverage
    // panel's in-view cell list for the new viewport.
    map.on("moveend", () => {
      this.saveView();
      this._assessCoverage();
      // Keep the Charts panel's "in view" cell list in step with the map when no
      // search query is pinning it to catalog-wide matches.
      if (this._chartsMode && this._drawerOpen() && !(this._cellQuery || "").trim()) this._renderCellListInto();
    });

    // Live zoom/scale/band readout (left of the statusbar).
    this._updateHud();
    map.on("move", () => this._updateHud());

    // Close any pinned band-pill popup when clicking elsewhere (pill/cell clicks
    // stopPropagation, so this only fires for clicks outside them). Also tuck the
    // on-map search back into its tab when clicking away while it's empty (map
    // clicks bubble out of the renderer's shadow root to here).
    this.shadowRoot.addEventListener("click", (e) => {
      this.shadowRoot.querySelectorAll(".sb-band-wrap.open").forEach((w) => w.classList.remove("open"));
      const search = this.shadowRoot.getElementById("search");
      if (search && !search.hidden) {
        const onSearch = e.composedPath().some((n) => n === search || (n.id === "search-tab"));
        const si = this.shadowRoot.getElementById("search-input");
        if (!onSearch && !(si && si.value.trim())) {
          search.hidden = true;
          this.shadowRoot.getElementById("search-tab").hidden = false;
        }
      }
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
    if (this._chartsMode || loadJSON(LS_VIEW, null) || !this._districts.length) return;
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

  // -- Charts: the map selector + "on this device" coverage manager ---------
  // Open the Charts drawer (and the all-cells map overlay).
  openCharts() {
    this._section = "charts";
    this.shadowRoot.querySelectorAll(".panel").forEach((p) => p.classList.toggle("sel", p.dataset.panel === "charts"));
    this.shadowRoot.getElementById("empty").hidden = true;
    this.renderCharts();
    this._enterChartsMode();
    this.setDrawerOpen(true);
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
        el.onclick = () => this.openCharts();
      }
    }
    this._setProgress(d ? { label: d.label, sub: d.sub, frac: d.frac } : null);
    // Keep the Charts panel's action buttons in step with the job state without a
    // full re-render each poll tick (which would flicker / collapse the import
    // panel) — just disable Download/Remove while a job runs. The completed-state
    // re-render happens in _onTaskDone.
    if (this._section === "charts" && this._drawerOpen()) {
      const busy = this._taskRunning();
      const dl = this.shadowRoot.getElementById("area-apply");
      if (dl) { dl.disabled = busy; if (busy) dl.textContent = "Working…"; }
      const rm = this.shadowRoot.getElementById("owned-remove");
      if (rm) rm.disabled = busy;
      const rb = this.shadowRoot.getElementById("owned-rebake");
      if (rb) rb.disabled = busy;
    }
    // A running download means charts are inbound — don't show the empty-state
    // welcome over the map (and restore it if the task failed with no coverage).
    this.updateEmptyState();
  }

  // The Charts drawer body: the region list, or (once one is picked) that
  // region's detail. This is the ONE chart surface — browse, download, and view
  // existing charts all live here, organised by region.
  renderCharts() {
    const el = this.shadowRoot.getElementById("charts-body");
    if (!el) return;
    this.shadowRoot.getElementById("dtitle").textContent = "Charts";
    el.innerHTML = `
      ${this._renderRegions()}
      ${this._renderCellSearch()}
      ${this._renderSelectionBar()}
      ${this._renderBandToggles()}
      ${this._renderCellList()}
      ${this._renderOwned()}
      <details class="import-more">
        <summary>Import from a file</summary>
        <div id="drop" class="drop">Drop a <code>.zip</code>, <code>.000</code> or <code>.pmtiles</code> here, or<br><button id="pick" class="btn" style="margin-top:6px">Choose files…</button></div>
        <input id="file" type="file" accept=".zip,.000,.pmtiles" multiple hidden>
        <div id="import-log" class="muted"></div>
        <div id="archive-list"></div>
      </details>
      ${this._renderDataFreshness()}`;
    this._wireRegions();
    this._wireCellSearch();
    this._wireSelectionBar();
    this._wireBandToggles();
    this._wireCellList();
    this.shadowRoot.getElementById("owned-remove")?.addEventListener("click", () => this._removeDownloaded());
    this.shadowRoot.getElementById("owned-rebake")?.addEventListener("click", () => this._rebakeDownloaded());
    this._wireImport();
  }

  // Quick-pick geographic regions: one click selects every catalog cell the
  // region covers (band-filtered) and frames the map there. A fast path to a
  // whole coast; box-select + search refine from there.
  _renderRegions() {
    const chips = REGIONS.map((rgn) => {
      const cells = this._regionCells(rgn);
      const on = cells.length > 0 && cells.every((n) => this._areaCells.has(n)); // fully selected → active
      return `<button class="region-btn${on ? " on" : ""}" data-region="${rgn.id}" title="${on ? "Click to deselect" : "Select"} · ${cells.length} chart${cells.length !== 1 ? "s" : ""}">${rgn.name}<span class="rb-n">${cells.length}</span></button>`;
    }).join("");
    return `<div class="set-section" id="region-sec">
      <div class="rg-head"><h3>Jump to a region</h3></div>
      <div class="region-grid">${chips}</div>
    </div>`;
  }
  // Re-render just the region section in place (button active states + Reset link
  // depend on the current selection, which cell toggles change).
  _refreshRegions() {
    const sec = this.shadowRoot.getElementById("region-sec");
    if (sec) { sec.outerHTML = this._renderRegions(); this._wireRegions(); }
  }
  _regionCells(rgn) {
    const out = [];
    for (const c of this._catalog) {
      const b = c.bb;
      if (!Array.isArray(b) || b.length !== 4 || !this._bandOn(c)) continue;
      if (rgn.all || !(b[2] < rgn.bb[0] || b[0] > rgn.bb[2] || b[3] < rgn.bb[1] || b[1] > rgn.bb[3])) out.push(c.n);
    }
    return out;
  }
  _wireRegions() {
    this.shadowRoot.querySelectorAll(".region-btn[data-region]").forEach((b) =>
      (b.onclick = () => this._selectRegion(b.dataset.region)));
  }
  // Region buttons are toggles: if the region is fully selected, clicking clears
  // its cells; otherwise it adds them. No map movement — the selection just lights
  // up in place on the zoomed-out map (cells shared with another region follow
  // this region's footprint).
  _selectRegion(id) {
    const rgn = REGIONS.find((r) => r.id === id);
    if (!rgn) return;
    const cells = this._regionCells(rgn);
    const on = cells.length > 0 && cells.every((n) => this._areaCells.has(n));
    if (on) for (const n of cells) this._areaCells.delete(n);
    else for (const n of cells) this._areaCells.add(n);
    this._saveAreaCells();
    this._refreshCellSel();
    this.renderCharts();
  }

  // Find-a-chart search box (filters the cell list below over the whole catalog).
  _renderCellSearch() {
    const q = this._cellQuery || "";
    return `<input id="cell-search" class="region-search" type="search" placeholder="Search a port or chart name…" autocomplete="off" spellcheck="false" value="${esc(q)}">`;
  }
  _wireCellSearch() {
    const i = this.shadowRoot.getElementById("cell-search");
    if (!i) return;
    i.oninput = () => { this._cellQuery = i.value; this._renderCellListInto(); };
  }

  // The selection bar: the selection vs what's installed. It only acts on the
  // DIFFERENCE — added cells download, deselected cells uninstall — so re-opening
  // the picker with everything already downloaded offers nothing to do. The map
  // pans/zooms freely; tap a cell to add/remove it.
  _renderSelectionBar() {
    const { added, removed, eff } = this._pendingChanges();
    const have = this._downloadedCells();
    const busy = this._taskRunning();
    if (!eff.length && !have.size) {
      return `<div class="sel-bar empty"><span class="muted">Pick charts: tap a region, tap cells on the map, or search below.</span></div>`;
    }
    let bytes = 0; for (const n of eff) { const c = this._byName.get(n); if (c && typeof c.zs === "number") bytes += c.zs; }
    const addBytes = added.reduce((s, n) => { const c = this._byName.get(n); return s + (c && typeof c.zs === "number" ? c.zs : 0); }, 0);
    const changed = added.length || removed.length;

    let action;
    if (!changed) {
      action = `<button class="add-dl" disabled>✓ Up to date</button>`;
    } else if (!eff.length) {
      action = `<button class="add-dl" id="area-apply"${busy ? " disabled" : ""}>${busy ? "Working…" : `Remove all ${removed.length} chart${removed.length !== 1 ? "s" : ""}`}</button>`;
    } else {
      let label;
      if (added.length && removed.length) label = `Apply changes (+${added.length} · −${removed.length})`;
      else if (added.length) label = `⬇ Download ${added.length} chart${added.length !== 1 ? "s" : ""}`;
      else label = `Remove ${removed.length} chart${removed.length !== 1 ? "s" : ""}`;
      action = `<button class="add-dl" id="area-apply"${busy ? " disabled" : ""}>${busy ? "Working…" : label}</button>`;
    }
    const bits = [];
    if (added.length) bits.push(`+${added.length} to add${addBytes ? ` · ~${(addBytes / 1e6).toFixed(1)} MB` : ""}`);
    if (removed.length) bits.push(`−${removed.length} to remove`);
    const changeLine = bits.length ? `<div class="sel-change">${bits.join(" · ")}</div>` : "";
    const revert = changed ? `<button class="linkbtn" id="area-revert"${busy ? " disabled" : ""}>Revert to installed</button>` : "";

    return `<div class="sel-bar">
      <div class="sel-count"><b>${eff.length}</b> chart${eff.length !== 1 ? "s" : ""} selected <span class="muted">· ~${(bytes / 1e6).toFixed(1)} MB total</span></div>
      ${changeLine}
      ${action}
      ${revert}
    </div>`;
  }
  _wireSelectionBar() {
    const r = this.shadowRoot;
    r.getElementById("area-apply")?.addEventListener("click", () => this._applyChanges());
    r.getElementById("area-revert")?.addEventListener("click", () => this._resetToDownloaded());
  }
  // Apply the pending selection: nothing left selected (all deselected) → uninstall
  // everything; otherwise re-provision the selection, which fetches the added cells
  // and drops the removed ones from the re-baked archive.
  _applyChanges() {
    const { eff, removed } = this._pendingChanges();
    if (!eff.length && removed.length) { this._removeDownloaded(); return; }
    this._downloadArea();
  }
  // Discard pending edits — snap the selection back to what's installed.
  _resetToDownloaded() {
    this._areaCells = this._downloadedCells();
    this._saveAreaCells();
    this._refreshCellSel();
    if (this._section === "charts" && this._drawerOpen()) this.renderCharts();
  }

  // NOAA data freshness footer (req: show when the catalog data is from).
  _renderDataFreshness() {
    if (!this._catalogDate) return "";
    const total = this._catalog.length.toLocaleString();
    return `<div class="data-fresh">NOAA chart data current as of <b>${fmtIssue(this._catalogDate)}</b> · ${total} charts available</div>`;
  }

  // Navigational-purpose band on/off chips — control which cells the selector
  // shows on the map and grabs into a box (turn off Overview/General for a small,
  // fast package of just the detailed charts).
  _renderBandToggles() {
    const chips = BANDS.map((b) =>
      `<button class="band-chip${this._selBands.has(b) ? "" : " off"}" data-band="${b}"><span class="sw" style="background:${BAND_COLOR[b]}"></span>${BAND_LABEL[b]}</button>`).join("");
    return `<div class="region-group">Bands to include</div><div class="band-row" style="margin-bottom:14px">${chips}</div>`;
  }
  _wireBandToggles() {
    this.shadowRoot.querySelectorAll(".band-chip[data-band]").forEach((b) => (b.onclick = () => this._toggleBand(b.dataset.band)));
  }

  // The selected cells that will actually be baked: every selected catalog cell.
  // Bands are a VIEW/add filter only (they limit what the regions/map add and
  // what's drawn) — they must NOT shrink the install set here, or downloaded
  // cells of a hidden band would read as "to remove" forever.
  _effectiveAreaCells() {
    const out = [];
    for (const n of this._areaCells) if (this._byName.has(n)) out.push(n);
    return out;
  }

  // The cells installed on this device — in the 100%-wasm model that's exactly the
  // browser cell store (`_installed`), the set the baker renders from. (Server-side
  // bake/region state no longer applies.) Catalog-filtered so the diff compares
  // like with like.
  _downloadedCells() {
    const have = new Set();
    for (const n of this._installed) if (this._byName.has(n)) have.add(n);
    return have;
  }

  // Pending change between the selection and what's installed: cells to add (in
  // the selection, not yet downloaded) and to remove (downloaded, deselected).
  _pendingChanges() {
    const effArr = this._effectiveAreaCells();
    const eff = new Set(effArr);
    const have = this._downloadedCells();
    const added = effArr.filter((n) => !have.has(n));
    const removed = [...have].filter((n) => !eff.has(n));
    return { added, removed, eff: effArr };
  }

  // "On this device": the map-selected bake (charts-user) + any imported files,
  // so it's always clear what coverage you actually have.
  _renderOwned() {
    const n = this._installed.size;
    if (!n) return "";
    let bytes = 0;
    for (const name of this._installed) { const c = this._byName.get(name); if (c && typeof c.zs === "number") bytes += c.zs; }
    const mb = bytes ? ` · ~${(bytes / 1e6).toFixed(1)} MB` : "";
    const busy = this._taskRunning();
    return `<div class="region-group">On this device</div>` +
      `<div class="owned-row"><div><b>${n} chart${n !== 1 ? "s" : ""}</b><div class="muted">stored in browser${mb}</div></div>` +
      `<div class="owned-actions"><button class="linkbtn danger" id="owned-remove"${busy ? " disabled" : ""}>Remove all</button></div></div>`;
  }

  // 100%-wasm: remove every cell from the browser store, reset the selection, and
  // reload the (now empty) baker so the map clears.
  async _removeDownloaded() {
    if (this._taskRunning()) return;
    const names = await this._store.list();
    for (const name of names) { try { await this._store.remove(name); } catch (e) { console.warn("[store] remove", name, e); } }
    this._installed = new Set();
    this._areaCells = new Set();
    this._saveAreaCells();
    this._userBake = null;
    this._hasArchive = false;
    await this._refreshRealtime(); // reloads the baker (empty) → tiles clear
    this._refreshCellSel();
    this.updateEmptyState();
    if (this._section === "charts" && this._drawerOpen()) this.renderCharts();
  }

  // Re-bake the already-downloaded map selection: re-POST the baked cell list to
  // /api/provision. The server reads each cell from the local cell cache (no
  // re-download) and regenerates charts-user.pmtiles, so bake-side code changes
  // (symbology, soundings, light text…) take effect without fetching anything.
  async _rebakeDownloaded() {
    if (this._taskRunning()) return;
    const cells = (this._userBake && Array.isArray(this._userBake.cells)) ? this._userBake.cells.slice() : [];
    if (!cells.length) return;
    this._taskMeta = { name: "Map selection", verb: "Re-baking" };
    this._task = { kind: "provision", status: "running", phase: "import", done: 0, total: cells.length, cells: cells.length, cell: "" };
    this._renderTaskUI();
    try {
      const res = await fetch("api/provision", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ cells }),
      });
      const j = await res.json().catch(() => ({}));
      if (!res.ok || !j.ok) throw new Error(j.error || `HTTP ${res.status}`);
    } catch (e) {
      console.error("[rebake]", e);
      this._task = { kind: "provision", status: "error", error: "start" };
      this._taskMeta = { name: "Map selection", verb: "Re-baking", errMsg: "Is the chartplotter server running?" };
      this._renderTaskUI();
      this._clearTaskSoon(3500);
      return;
    }
    this._startPolling();
  }

  // -- Drag-a-box custom area selection ------------------------------------
  // Pick your OWN region by dragging a rectangle on the map: every NOAA cell
  // whose bbox intersects the box joins `_areaCells` (deduped — drag more boxes
  // and they accumulate without re-downloading overlaps). Downloading bakes the
  // whole union into ONE charts-user.pmtiles, so you get a small package fast.

  _saveAreaCells() {
    try { localStorage.setItem(LS_AREACELLS, JSON.stringify(Array.from(this._areaCells))); } catch {}
  }
  // Default the selection to exactly what's already downloaded on this device, so
  // the selector opens reflecting the charts you have (edit from there). This is
  // authoritative on load — it replaces any stale persisted selection that may
  // have drifted from what was actually downloaded.
  async _seedAreaCells() {
    // 100%-wasm: the selection defaults to exactly the cells in the browser store
    // (what the baker renders). Edit from there.
    this._areaCells = this._downloadedCells();
    this._saveAreaCells();
  }

  // Selector bands (navigational purpose) the user has enabled. Default: all.
  _loadSelBands() {
    const arr = loadJSON(LS_SELBANDS, null);
    return new Set(Array.isArray(arr) && arr.length ? arr.filter((b) => BANDS.includes(b)) : BANDS);
  }
  _saveSelBands() {
    try { localStorage.setItem(LS_SELBANDS, JSON.stringify(Array.from(this._selBands))); } catch {}
  }
  _bandOn(c) { return this._selBands.has(bandForScale(c.s)); }
  // Toggle a band on/off → repaint the cell overlay (it only shows enabled bands)
  // and re-render the panel (the selection summary counts enabled cells only).
  _toggleBand(b) {
    if (this._selBands.has(b)) this._selBands.delete(b); else this._selBands.add(b);
    this._saveSelBands();
    this._setCellOverlay(true);
    if (this._section === "charts" && this._drawerOpen()) this.renderCharts();
  }

  // Arm the on-map drag-a-box selector (Charts mode already shows every cell).
  // Disables pan for one drag, draws a live rectangle + amber preview of the
  // cells it covers, and on release adds them to the selection. The drawer stays
  // open (the map is visible beside it). Re-entrant: cancels a prior arm first.
  _enterAreaSelect() {
    if (!this._map) return;
    this._setInspectMode(false); // inspect + box-select are mutually exclusive
    this._cancelAreaSelect();
    this._setCellOverlay(true); // ensure the all-cells overlay is up
    const map = this._map.getContainer();
    const prevCursor = map.style.cursor;
    map.style.cursor = "crosshair";
    this._map.dragPan.disable();

    let box = null, start = null;
    const ptOf = (ev) => {
      const t = ev.touches ? ev.touches[0] : ev;
      const r = map.getBoundingClientRect();
      return [t.clientX - r.left, t.clientY - r.top];
    };
    const geoBox = (p) => {
      const a = this._map.unproject([Math.min(start[0], p[0]), Math.min(start[1], p[1])]);
      const b = this._map.unproject([Math.max(start[0], p[0]), Math.max(start[1], p[1])]);
      return [Math.min(a.lng, b.lng), Math.min(a.lat, b.lat), Math.max(a.lng, b.lng), Math.max(a.lat, b.lat)];
    };
    // resetDrag clears the in-progress rectangle but leaves the tool ARMED — the
    // selector stays live across drags (box-select is the default Charts gesture),
    // so you can grab area after area without re-arming. Full teardown is cleanup.
    const resetDrag = () => {
      if (box && box.parentNode) box.parentNode.removeChild(box);
      box = null; start = null;
      this._setPreviewBox(null);
    };
    const cleanup = () => {
      map.removeEventListener("mousedown", onDown);
      map.removeEventListener("mousemove", onMove);
      window.removeEventListener("mouseup", onUp);
      map.removeEventListener("touchstart", onDown);
      map.removeEventListener("touchmove", onMove);
      window.removeEventListener("touchend", onUp);
      resetDrag();
      if (this._map) { this._map.dragPan.enable(); }
      map.style.cursor = prevCursor;
      this._areaCleanup = null;
    };
    this._areaCleanup = cleanup;
    const onDown = (ev) => {
      ev.preventDefault();
      start = ptOf(ev);
      box = document.createElement("div");
      // Inline styles, not the app's `.box-sel` class: the map container lives in
      // the chart-plotter's OWN shadow root, where the app shadow root's CSS can't
      // reach — a class-styled box would be invisible.
      box.style.cssText = "position:absolute;z-index:1000;border:2px solid #1565c0;background:rgba(21,101,192,.12);pointer-events:none;box-sizing:border-box;border-radius:2px;";
      map.appendChild(box);
      onMove(ev);
    };
    const onMove = (ev) => {
      if (!start || !box) return;
      const p = ptOf(ev);
      box.style.left = Math.min(start[0], p[0]) + "px";
      box.style.top = Math.min(start[1], p[1]) + "px";
      box.style.width = Math.abs(p[0] - start[0]) + "px";
      box.style.height = Math.abs(p[1] - start[1]) + "px";
      this._setPreviewBox(geoBox(p)); // live amber preview of cells under the box
    };
    const onUp = (ev) => {
      if (!start) return;
      const p = ev.changedTouches ? ptOf({ touches: [ev.changedTouches[0]] }) : ptOf(ev);
      const dx = Math.abs(p[0] - start[0]), dy = Math.abs(p[1] - start[1]);
      const drag = dx >= 5 && dy >= 5;
      const bbox = drag ? geoBox(p) : null;
      resetDrag(); // stay armed for the next box
      if (bbox) this._addAreaBox(bbox); // commit; re-renders the panel + selected fill
      else this._pickCellAt(p); // a plain click inspects the single cell under it
    };
    map.addEventListener("mousedown", onDown);
    map.addEventListener("mousemove", onMove);
    window.addEventListener("mouseup", onUp);
    map.addEventListener("touchstart", onDown, { passive: false });
    map.addEventListener("touchmove", onMove, { passive: false });
    window.addEventListener("touchend", onUp);
  }

  // Tear down an armed/active box-drag (leaving Charts mode, Home, etc.).
  _cancelAreaSelect() { if (this._areaCleanup) this._areaCleanup(); }

  // A plain click in select mode: focus the finest (largest-scale) enabled cell
  // whose footprint contains the clicked point — highlights it on the map and
  // opens its detail in the panel, so you can inspect/add one cell at a time.
  _pickCellAt([px, py]) {
    if (!this._map) return;
    const ll = this._map.unproject([px, py]);
    let best = null;
    for (const c of this._catalog) {
      const b = c.bb;
      if (!Array.isArray(b) || b.length !== 4 || !this._bandOn(c)) continue;
      if (ll.lng >= b[0] && ll.lng <= b[2] && ll.lat >= b[1] && ll.lat <= b[3]) {
        if (!best || (c.s || 0) < (best.s || 0)) best = c; // finest = smallest scale denom
      }
    }
    if (best) this._focusListCell(best.n, false);
  }

  // Add every catalog cell whose bbox intersects the drawn box to the union.
  _addAreaBox([w, s, e, n]) {
    for (const c of this._catalog) {
      if (!Array.isArray(c.bb) || c.bb.length !== 4 || !this._bandOn(c)) continue;
      if (!(c.bb[2] < w || c.bb[0] > e || c.bb[3] < s || c.bb[1] > n)) this._areaCells.add(c.n);
    }
    this._saveAreaCells();
    this._refreshCellSel(); // repaint the selected-cell highlight on the map
    if (this._section === "charts" && this._drawerOpen()) this.renderCharts();
  }

  // Apply the pending change in the 100%-wasm model: fetch added cells' raw bytes
  // (through the shim's NOAA proxy, /api/cell?url=…) into the browser store, drop
  // removed ones, then re-bake everything in-browser. No server provision/pmtiles.
  async _downloadArea() {
    if (this._taskRunning()) return;
    const { added, removed } = this._pendingChanges();
    if (!added.length && !removed.length) return;
    if (added.length && !await this._ensureAgreed()) return; // NOAA ENC User Agreement gate

    this._task = { kind: "download", status: "running" };
    if (this._section === "charts" && this._drawerOpen()) this.renderCharts();

    // 1) remove deselected cells from the browser store.
    for (const name of removed) {
      try { await this._store.remove(name); this._installed.delete(name); } catch (e) { console.warn("[store] remove", name, e); }
    }
    // 2) fetch added cells into the store (the shim downloads from NOAA + caches).
    let done = 0, failed = 0;
    for (const name of added) {
      this._setProgress({ label: `Downloading ${added.length} chart${added.length !== 1 ? "s" : ""}`, sub: `${name} · ${done + 1} of ${added.length}`, frac: added.length ? done / added.length : null });
      try {
        const c = this._byName.get(name);
        const url = "api/cell/" + encodeURIComponent(name) + (c && c.z ? "?url=" + encodeURIComponent(c.z) : "");
        const resp = await fetch(url);
        if (!resp.ok) throw new Error("HTTP " + resp.status);
        await this._store.put(name, new Uint8Array(await resp.arrayBuffer()));
        this._installed.add(name);
      } catch (e) { console.warn("[download]", name, e.message); failed++; }
      done++;
    }
    // 3) re-bake the whole store in-browser.
    this._setProgress({ label: "Baking tiles…", sub: failed ? `${failed} cell${failed !== 1 ? "s" : ""} failed` : "", frac: 1 });
    await this._refreshRealtime();

    this._task = null;
    this._setProgress(null);
    this._saveAreaCells();
    this.updateEmptyState();
    if (this._section === "charts" && this._drawerOpen()) this.renderCharts();
    if (failed) console.warn(`[download] ${failed} of ${added.length} cell(s) failed`);
  }

  // -- cell list (browse + inspect, req: detailed list & cell info) --------
  // The browsable list under the search box. With a query, lists catalog matches;
  // otherwise lists the cells in the current viewport (band-filtered, finest
  // first), capped so a world-zoom view stays manageable. Selected cells are
  // ticked; clicking a row highlights the cell on the map and expands its detail.
  _cellListCells() {
    const q = (this._cellQuery || "").trim().toLowerCase();
    let cells;
    if (q.length >= 2) {
      cells = this._catalog.filter((c) => Array.isArray(c.bb) && c.bb.length === 4 && this._bandOn(c) &&
        ((c.l || "").toLowerCase().includes(q) || c.n.toLowerCase().includes(q)));
      cells.sort((a, b) => (b.s || 0) - (a.s || 0)); // coarsest first — overview lands first
    } else if (this._map) {
      const b = this._map.getBounds();
      const vw = b.getWest(), ve = b.getEast(), vs = b.getSouth(), vn = b.getNorth();
      cells = this._catalog.filter((c) => Array.isArray(c.bb) && c.bb.length === 4 && this._bandOn(c) &&
        !(c.bb[2] < vw || c.bb[0] > ve || c.bb[3] < vs || c.bb[1] > vn));
      cells.sort((a, z) => (a.s || 0) - (z.s || 0) || a.n.localeCompare(z.n)); // finest first
    } else cells = [];
    return cells;
  }
  _renderCellList() {
    return `<div class="set-section cell-list-sec">
      <h3 id="cell-list-head"></h3>
      <div id="cell-list"></div>
    </div>`;
  }
  _renderCellListInto() {
    const list = this.shadowRoot.getElementById("cell-list");
    const head = this.shadowRoot.getElementById("cell-list-head");
    if (!list || !head) return;
    const q = (this._cellQuery || "").trim();
    const all = this._cellListCells();
    const CAP = 250;
    const cells = all.slice(0, CAP);
    head.textContent = q.length >= 2
      ? `${all.length} match${all.length !== 1 ? "es" : ""}`
      : `${all.length} chart${all.length !== 1 ? "s" : ""} in view`;
    if (!cells.length) {
      list.innerHTML = `<div class="cl-empty">${q.length >= 2 ? "No charts match." : "Zoom to some coverage, or pick a region above."}</div>`;
      return;
    }
    list.innerHTML = cells.map((c) => this._renderCellRow(c)).join("") +
      (all.length > CAP ? `<div class="cl-more">+${(all.length - CAP).toLocaleString()} more — zoom in or search to narrow.</div>` : "");
    list.querySelectorAll(".cl-row[data-name]").forEach((row) => {
      const name = row.dataset.name;
      row.querySelector(".cl-main").onclick = () => this._focusListCell(name, true);
      const tg = row.querySelector(".cl-toggle");
      if (tg) tg.onclick = (e) => { e.stopPropagation(); this._toggleAreaCell(name); };
    });
  }
  _renderCellRow(c) {
    const band = bandForScale(c.s);
    const sel = this._areaCells.has(c.n);
    const open = this._focusedCell === c.n;
    return `<div class="cl-row${open ? " open" : ""}${sel ? " sel" : ""}" data-name="${c.n}">
      <div class="cl-main">
        <span class="cl-dot" style="background:${BAND_COLOR[band]}"></span>
        <span class="cl-text"><span class="cl-title">${esc(c.l || c.n)}</span><span class="cl-sub">${c.n} · 1:${(c.s || 0).toLocaleString()}</span></span>
        <button class="cl-toggle${sel ? " on" : ""}" title="${sel ? "Remove from selection" : "Add to selection"}">${sel ? "✓" : "+"}</button>
      </div>
      ${open ? this._renderCellDetail(c) : ""}
    </div>`;
  }
  _renderCellDetail(c) {
    const band = bandForScale(c.s), f = freshness(c.d);
    const mb = typeof c.zs === "number" ? `~${(c.zs / 1e6).toFixed(1)} MB` : "—";
    const ed = c.e ? `Ed ${c.e}/${c.u ?? 0}` : "—";
    const rg = Array.isArray(c.rg) && c.rg.length ? c.rg.join(", ") : "—";
    return `<div class="cl-detail">
      <span class="k">Band</span><span class="v"><span class="cl-dot sm" style="background:${BAND_COLOR[band]}"></span>${BAND_LABEL[band]}</span>
      <span class="k">Scale</span><span class="v">1:${(c.s || 0).toLocaleString()}</span>
      <span class="k">Edition</span><span class="v">${ed}</span>
      <span class="k">Issued</span><span class="v">${fmtIssue(c.d)} <span class="fresh ${f.cls}">${f.label}</span></span>
      <span class="k">Size</span><span class="v">${mb}</span>
      <span class="k">NOAA region</span><span class="v">${rg}</span>
    </div>`;
  }
  _wireCellList() { this._renderCellListInto(); }
  _toggleAreaCell(name) {
    if (this._areaCells.has(name)) this._areaCells.delete(name); else this._areaCells.add(name);
    this._saveAreaCells();
    this._refreshCellSel();
    // Re-render the affected pieces in place (keep the rest of the panel + scroll).
    const bar = this.shadowRoot.querySelector(".sel-bar");
    if (bar) bar.outerHTML = this._renderSelectionBar(), this._wireSelectionBar();
    this._refreshRegions();
    this._renderCellListInto();
  }
  // Focus one cell: outline + frame it on the map, open its detail row.
  _focusListCell(name, frame) {
    const c = this._byName.get(name);
    if (!c || !Array.isArray(c.bb) || c.bb.length !== 4) return;
    this._focusedCell = this._focusedCell === name ? null : name; // click again to collapse
    const [w, s, e, n] = c.bb;
    const src = this._map && this._map.getSource("focus");
    if (src) src.setData(this._focusedCell
      ? { type: "FeatureCollection", features: [{ type: "Feature", properties: {}, geometry: { type: "Polygon", coordinates: [[[w, s], [e, s], [e, n], [w, n], [w, s]]] } }] }
      : { type: "FeatureCollection", features: [] });
    if (this._focusedCell && frame && this._map) this._map.fitBounds([[w, s], [e, n]], { padding: 60, maxZoom: 11, duration: 600 });
    this._renderCellListInto();
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
    // Don't persist the Charts-mode selection framing (the zoomed-out world view):
    // a refresh should resume where the user was actually looking at charts, not
    // on the picker. The pre-Charts position stays the last saved view.
    if (this._chartsMode) return;
    const c = this._map.getCenter();
    try { localStorage.setItem(LS_VIEW, JSON.stringify({ center: [c.lng, c.lat], zoom: this._map.getZoom() })); } catch {}
  }

  // The map's region-highlight layer (outline of the region open in the drawer)
  // plus the area-select overlay: every catalog cell footprint (shown only in
  // selection mode), the already-selected cells (blue fill), and a live amber
  // preview of the cells the in-progress drag box will grab.
  addCatalogOverlay(map) {
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
    map.addSource("selpreview", { type: "geojson", data: empty });
    map.addLayer({ id: "selpreview-fill", type: "fill", source: "selpreview", layout: { visibility: "none" }, paint: { "fill-color": "#f0a500", "fill-opacity": 0.28 } });
    map.addLayer({ id: "selpreview-line", type: "line", source: "selpreview", layout: { visibility: "none" }, paint: { "line-color": "#e08a00", "line-width": 1.2 } });
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
    // Installed-cell coverage: at zooms BELOW a cell's native band (where its
    // chart detail isn't baked yet) draw its footprint + name, so when zoomed out
    // you can tell WHAT coverage you have, not just that you have some. One set of
    // layers per band, auto-hidden at the band's native min zoom (maxzoom) — where
    // the real chart takes over.
    map.addSource("inst-bounds", { type: "geojson", data: empty });
    for (const band of ["general", "coastal", "approach", "harbor", "berthing"]) {
      const mz = BAND_MINZOOM[band];
      const f = ["==", ["get", "band"], band];
      map.addLayer({ id: `inst-fill-${band}`, type: "fill", source: "inst-bounds", maxzoom: mz, filter: f, paint: { "fill-color": BAND_COLOR[band], "fill-opacity": 0.06 } });
      map.addLayer({ id: `inst-line-${band}`, type: "line", source: "inst-bounds", maxzoom: mz, filter: f, paint: { "line-color": BAND_COLOR[band], "line-width": 1.1, "line-opacity": 0.85 } });
      map.addLayer({ id: `inst-label-${band}`, type: "symbol", source: "inst-bounds", maxzoom: mz, filter: f, layout: { "text-field": ["get", "name"], "text-font": ["Noto Sans Regular"], "text-size": 11 }, paint: { "text-color": "#1b2733", "text-halo-color": "rgba(255,255,255,0.9)", "text-halo-width": 1.2 } });
    }
    // Inspect mode (toggled from the statusbar), CSS-devtools style: while ON,
    // hovering highlights + previews the feature under the cursor; a click LOCKS
    // it (freezes the panel) until you click again to release. SHIFT+drag boxes a
    // region and captures every chart feature inside it. Skipped while the area
    // (box-download) selector is armed (it owns pointer events).
    let boxStart = null, boxEl = null;
    map.on("mousedown", (e) => {
      if (!this._inspectMode || this._areaCleanup || !e.originalEvent.shiftKey) return;
      e.preventDefault();
      this._map.dragPan.disable();
      boxStart = e.point;
      boxEl = document.createElement("div");
      boxEl.style.cssText = "position:absolute;z-index:1000;border:2px solid #ff5252;background:rgba(255,82,82,.12);pointer-events:none;box-sizing:border-box;border-radius:2px;";
      map.getContainer().appendChild(boxEl);
    });
    map.on("mousemove", (e) => {
      if (boxStart && boxEl) {
        const p = e.point;
        boxEl.style.left = Math.min(boxStart.x, p.x) + "px";
        boxEl.style.top = Math.min(boxStart.y, p.y) + "px";
        boxEl.style.width = Math.abs(p.x - boxStart.x) + "px";
        boxEl.style.height = Math.abs(p.y - boxStart.y) + "px";
        return;
      }
      if (!this._inspectMode || this._inspectLocked || this._areaCleanup) return;
      this._inspectAt(e.point, false);
    });
    map.on("mouseup", (e) => {
      if (!boxStart) return;
      const a = boxStart, b = e.point;
      if (boxEl && boxEl.parentNode) boxEl.parentNode.removeChild(boxEl);
      boxEl = null; boxStart = null;
      this._map.dragPan.enable();
      if (Math.abs(b.x - a.x) < 3 || Math.abs(b.y - a.y) < 3) return; // too small → ignore
      this._showInspectArea(this._captureArea(a, b));
    });
    map.on("click", (e) => {
      // Charts selection map: a tap toggles the cell under it (drags pan, and
      // MapLibre only emits "click" for non-pan gestures).
      if (this._chartsMode) { this._toggleCellAt(e.point.x, e.point.y); return; }
      if (!this._inspectMode || this._areaCleanup || e.originalEvent.shiftKey) return; // shift = box
      if (this._inspectLocked) { this._inspectLocked = false; this._inspectAt(e.point, false); return; }
      this._inspectAt(e.point, true); // lock onto whatever's here
    });
  }

  // Toggle inspect mode on/off. ON opens the panel (with a hover hint), arms the
  // hover/click handlers, sets a crosshair cursor, and cancels the box selector.
  // OFF clears everything. Mutually exclusive with the area selector.
  _setInspectMode(on) {
    on = !!on;
    if (on === this._inspectMode) return;
    this._inspectMode = on;
    this._inspectLocked = false;
    this._inspectLastKey = "";
    if (on) this._cancelAreaSelect();
    const map = this._map;
    if (map) {
      map.getCanvas().style.cursor = on ? "crosshair" : "";
      // Free SHIFT+drag for area capture (MapLibre uses it for box-zoom by default).
      if (on) map.boxZoom.disable(); else map.boxZoom.enable();
    }
    this.shadowRoot.getElementById("inspect-toggle")?.classList.toggle("on", on);
    if (on) {
      this._inspectHint("Hover to inspect · click to lock · SHIFT+drag to capture an area.");
      this.shadowRoot.getElementById("inspect").classList.add("open");
    } else {
      this._closeInspect();
    }
  }

  // Inspect the chart features at a canvas point. `lock` freezes the panel on a
  // hit (the click-to-lock action); a no-hit lock is a no-op (so clicking empty
  // chart doesn't clear a useful hover), a no-hit hover shows the hint.
  _inspectAt(point, lock) {
    const map = this._map;
    const feats = map.queryRenderedFeatures(point).filter((f) => typeof f.source === "string" && f.source.startsWith("chart-"));
    if (!feats.length) {
      if (lock) return;
      this._inspectLastKey = "";
      this._inspectFeats = [];
      const src = map.getSource("inspect");
      if (src) src.setData({ type: "FeatureCollection", features: [] });
      this._inspectHint("Hover to inspect · click to lock · SHIFT+drag to capture an area.");
      return;
    }
    const seen = new Set(), uniq = [];
    for (const f of feats) {
      const key = (f.sourceLayer || "") + "|" + JSON.stringify(f.properties || {});
      if (seen.has(key)) continue;
      seen.add(key);
      uniq.push(f);
    }
    const rank = { point_symbols: 0, soundings: 1, lines: 2, complex_lines: 3, areas: 4, area_patterns: 5, text: 6 };
    uniq.sort((a, b) => (rank[a.sourceLayer] ?? 9) - (rank[b.sourceLayer] ?? 9));
    if (lock) this._inspectLocked = true;
    // Skip re-render when hovering the same feature set (cheap mousemove path).
    const setKey = (lock ? "L:" : "") + uniq.map((f) => f.sourceLayer + "|" + JSON.stringify(f.properties)).join("~");
    if (setKey === this._inspectLastKey) return;
    this._inspectLastKey = setKey;
    this._inspectFeats = uniq; // the stack under the cursor
    this._inspectIdx = 0; // show the topmost; the cycler steps through the rest
    this._inspectMulti = false; // single-point pick → cycler
    this._renderInspect();
  }

  // Capture EVERY chart feature in the dragged pixel box (corners a,b). Reads the
  // loaded vector tiles directly via querySourceFeatures — unlike
  // queryRenderedFeatures it also returns collision-hidden symbols and features
  // not currently painted — across every loaded chart band, deduped, geo-filtered
  // to the box. So "capture everything" really means everything there.
  _captureArea(a, b) {
    const map = this._map;
    const tl = map.unproject([Math.min(a.x, b.x), Math.min(a.y, b.y)]);
    const br = map.unproject([Math.max(a.x, b.x), Math.max(a.y, b.y)]);
    const W = Math.min(tl.lng, br.lng), E = Math.max(tl.lng, br.lng);
    const S = Math.min(tl.lat, br.lat), N = Math.max(tl.lat, br.lat);
    const inBox = (g) => geomIntersectsBox(g, W, S, E, N);
    const sources = [...BANDS, "all"].map((s) => "chart-" + s);
    const layers = ["point_symbols", "soundings", "areas", "area_patterns", "lines", "complex_lines", "text"];
    const seen = new Set(), out = [];
    for (const src of sources) {
      for (const layer of layers) {
        let feats;
        try { feats = map.querySourceFeatures(src, { sourceLayer: layer }); } catch { continue; }
        for (const f of feats) {
          if (!inBox(f.geometry)) continue;
          const key = layer + "|" + JSON.stringify(f.properties || {});
          if (seen.has(key)) continue;
          seen.add(key);
          out.push({ source: src, sourceLayer: layer, properties: f.properties, geometry: f.geometry });
        }
      }
    }
    return out;
  }

  // SHIFT+drag box capture: show EVERY chart feature inside the dragged region as
  // a list (locked), highlighting them all. Answers "what's all here / what's
  // overlapping?" in one shot.
  _showInspectArea(feats) {
    const seen = new Set(), uniq = [];
    for (const f of feats) {
      const key = (f.sourceLayer || "") + "|" + JSON.stringify(f.properties || {});
      if (seen.has(key)) continue;
      seen.add(key);
      uniq.push(f);
    }
    const rank = { point_symbols: 0, soundings: 1, lines: 2, complex_lines: 3, areas: 4, area_patterns: 5, text: 6 };
    uniq.sort((a, b) => (rank[a.sourceLayer] ?? 9) - (rank[b.sourceLayer] ?? 9));
    this._inspectFeats = uniq;
    this._inspectIdx = 0;
    this._inspectMulti = true;
    this._inspectLocked = true;
    this._inspectLastKey = "AREA"; // distinct from any hover key
    this._renderInspect();
  }

  // Render the inspected feature(s): a single card + cycler for a point pick, or
  // the full list for a SHIFT+drag area capture. Highlights the shown feature(s).
  _renderInspect() {
    const feats = this._inspectFeats || [];
    if (!feats.length) return;
    const src = this._map.getSource("inspect");
    const body = this.shadowRoot.getElementById("inspect-body");
    const lockNote = this._inspectLocked ? `<div class="ins-lock">🔒 Locked — click the map to release</div>` : "";
    if (this._inspectMulti) {
      const cap = 80;
      const shown = feats.slice(0, cap);
      if (src) src.setData({ type: "FeatureCollection", features: feats.map((f) => ({ type: "Feature", properties: {}, geometry: f.geometry })) });
      this._clearInspectFocus();
      const more = feats.length > cap ? `<div class="ins-empty">…and ${feats.length - cap} more</div>` : "";
      const hint = `<div class="ins-cycler"><span>${feats.length} features in area · click one to isolate it</span></div>`;
      body.innerHTML = lockNote + hint + shown.map((f, i) => this._renderFeatureCard(f, i)).join("") + more;
      this.shadowRoot.getElementById("inspect").classList.add("open");
      body.querySelectorAll(".ins-feat[data-fi]").forEach((el) => (el.onclick = () => this._focusInspectFeature(+el.dataset.fi)));
      return;
    }
    const i = Math.min(this._inspectIdx, feats.length - 1);
    const f = feats[i];
    if (src) src.setData({ type: "FeatureCollection", features: [{ type: "Feature", properties: {}, geometry: f.geometry }] });
    const cycler = feats.length > 1
      ? `<div class="ins-cycler"><button id="ins-prev" class="btn" title="Previous">◀</button><span>${i + 1} / ${feats.length} here</span><button id="ins-next" class="btn" title="Next">▶</button></div>`
      : "";
    body.innerHTML = lockNote + cycler + this._renderFeatureCard(f);
    this.shadowRoot.getElementById("inspect").classList.add("open");
    if (feats.length > 1) {
      this.shadowRoot.getElementById("ins-prev").onclick = () => this._inspectStep(-1);
      this.shadowRoot.getElementById("ins-next").onclick = () => this._inspectStep(1);
    }
  }

  // Copy a debug snapshot of the current inspector selection — the picked
  // feature's source/layer, baked properties, and GeoJSON geometry (the exact
  // lon/lat MapLibre read from the tile, for diagnosing placement) plus the map
  // view — to the clipboard AND POST it to /api/debug so it can be pulled
  // server-side. Works on a plain-http LAN origin via copyText's fallback.
  async _copyInspectDebug(btn) {
    const m = this._map;
    let view = null;
    if (m) {
      const c = m.getCenter();
      view = { center: [+c.lng.toFixed(6), +c.lat.toFixed(6)], zoom: +m.getZoom().toFixed(3), bearing: +m.getBearing().toFixed(1) };
    }
    const feats = this._inspectFeats || [];
    const pick = this._inspectMulti ? feats.slice(0, 80) : (feats.length ? [feats[Math.min(this._inspectIdx, feats.length - 1)]] : []);
    // Render diagnostics: do the complex-linestyle SYMBOL layers (lc-marks) exist,
    // are their images registered, and do they actually place anything in view?
    // (Pins "lines show but symbols don't" without a screenshot.)
    let render = null;
    if (m && m.getStyle) {
      const layers = (m.getStyle().layers || []).map((l) => l.id);
      const markIds = layers.filter((id) => id.startsWith("lc-marks"));
      const lineIds = layers.filter((id) => /^lc-line/.test(id));
      const cnt = (ids) => { if (!ids.length) return 0; try { return m.queryRenderedFeatures({ layers: ids }).length; } catch { return -1; } };
      const images = {};
      for (const n of ["EMAREMG1", "EMAREGR1", "EMACHRE2", "EMCBLSU1", "EMRESAR1", "EMRECTR1"]) {
        images[n] = !!(m.hasImage && m.hasImage(n));
      }
      render = {
        markLayerCount: markIds.length,
        markFeaturesInView: cnt(markIds),
        complexLineFeaturesInView: cnt(lineIds),
        markImagesRegistered: images,
      };
    }
    const snap = {
      when: new Date().toISOString(),
      view,
      count: feats.length,
      features: pick.map((f) => ({ source: f.source, sourceLayer: f.sourceLayer, geometry: f.geometry, properties: f.properties })),
      render,
    };
    const text = JSON.stringify(snap, null, 2);
    const ok = await copyText(text);
    fetch("api/debug", { method: "POST", headers: { "content-type": "application/json" }, body: text }).catch(() => {});
    flashBtn(btn, ok ? "✓" : "✗");
  }

  // Isolate one feature from the area list: paint it cyan over the dim red set
  // and mark its card, so you can see its exact footprint and judge overlap.
  _focusInspectFeature(i) {
    const f = (this._inspectFeats || [])[i];
    if (!f) return;
    const src = this._map.getSource("inspect-focus");
    if (src) src.setData({ type: "FeatureCollection", features: [{ type: "Feature", properties: {}, geometry: f.geometry }] });
    this.shadowRoot.querySelectorAll(".ins-feat[data-fi]").forEach((el) => el.classList.toggle("active", +el.dataset.fi === i));
  }

  _clearInspectFocus() {
    const src = this._map && this._map.getSource("inspect-focus");
    if (src) src.setData({ type: "FeatureCollection", features: [] });
  }

  // Step the cycler through overlapping features (wraps).
  _inspectStep(d) {
    const n = (this._inspectFeats || []).length;
    if (!n) return;
    this._inspectIdx = (this._inspectIdx + d + n) % n;
    this._renderInspect();
  }

  _inspectHint(msg) {
    const body = this.shadowRoot.getElementById("inspect-body");
    if (body) body.innerHTML = `<div class="ins-empty">${esc(msg)}</div>`;
  }


  // `idx` (when given) makes the card a clickable list item for the area view —
  // clicking it isolates that feature's geometry on the map (see _focusInspectFeature).
  _renderFeatureCard(f, idx) {
    const p = f.properties || {};
    const acr = p.class || "";
    const named = S57_CLASS[acr];
    const label = named || INSPECT_LAYER_LABEL[f.sourceLayer] || acr || f.sourceLayer || "Feature";
    const name = p.objnam ? `<div class="ins-name">${esc(p.objnam)}</div>` : "";
    const cellPill = p.cell ? `<span class="ins-cell" title="Source ENC cell">▦ ${esc(p.cell)}</span>` : "";
    const lightPill = p.light ? `<span class="ins-light" title="Light characteristic">✦ ${esc(p.light)}</span>` : "";
    const pills = cellPill || lightPill ? `<div class="ins-pills">${cellPill}${lightPill}</div>` : "";
    // name/light/cell get their own prominent rows; class is in the title.
    const keys = Object.keys(p).filter((k) => !["cell", "class", "objnam", "light"].includes(k)).sort();
    const rows = keys.map((k) => `<div class="k">${esc(k)}</div><div class="v">${esc(this._fmtInspectVal(k, p[k]))}</div>`).join("")
      || `<div class="k" style="grid-column:1/-1;color:var(--ui-text-faint)">no attributes</div>`;
    const clickable = idx != null ? ` data-fi="${idx}" class="ins-feat ins-clickable"` : ` class="ins-feat"`;
    return `<div${clickable}>
      <div class="ins-title">${esc(label)}${named && acr ? `<span class="ins-acr">${esc(acr)}</span>` : ""}<span class="ins-layer">${esc(f.sourceLayer || "")}</span></div>
      ${name}
      ${pills}
      <div class="ins-kv">${rows}</div>
    </div>`;
  }

  // Friendlier rendering for a few baked enum/typed attributes.
  _fmtInspectVal(k, v) {
    if (k === "cat") return ["base", "standard", "other"][v] ?? String(v);
    if (k === "bnd") return ["plain", "symbolized", "common"][v] ?? String(v);
    if ((k === "depth" || k === "danger_depth" || k === "drval1" || k === "drval2") && v !== "" && v != null && !isNaN(v)) return `${v} m`;
    return String(v);
  }

  _closeInspect() {
    this._inspectLocked = false;
    this._inspectLastKey = "";
    this._inspectFeats = [];
    this._inspectIdx = 0;
    this._inspectMulti = false;
    this.shadowRoot.getElementById("inspect")?.classList.remove("open");
    if (this._map) {
      this._map.getCanvas().style.cursor = "";
      const src = this._map.getSource("inspect");
      if (src) src.setData({ type: "FeatureCollection", features: [] });
      this._clearInspectFocus();
    }
  }

  // Build a GeoJSON FeatureCollection of cell footprints. `cells` is an iterable
  // of catalog entries; `mark` tags each with sel=1 when already selected. Cells
  // of a band the user has toggled off are omitted (the selector only shows/grabs
  // enabled bands).
  _cellsFC(cells, mark) {
    const f = [];
    for (const c of cells) {
      const b = c.bb;
      if (!Array.isArray(b) || b.length !== 4 || !this._bandOn(c)) continue;
      f.push({
        type: "Feature",
        properties: { sel: mark && this._areaCells.has(c.n) ? 1 : 0 },
        geometry: { type: "Polygon", coordinates: [[[b[0], b[1]], [b[2], b[1]], [b[2], b[3]], [b[0], b[3]], [b[0], b[1]]]] },
      });
    }
    return { type: "FeatureCollection", features: f };
  }

  // Show/hide the all-cells selection overlay (and refresh the selected fill).
  _setCellOverlay(on) {
    const map = this._map;
    if (!map) return;
    const vis = on ? "visible" : "none";
    for (const id of ["selcells-line", "selcells-fill", "selcells-sel-line", "selpreview-fill", "selpreview-line"]) {
      if (map.getLayer(id)) map.setLayoutProperty(id, "visibility", vis);
    }
    const s = map.getSource("selcells");
    if (s) s.setData(on ? this._cellsFC(this._catalog, true) : { type: "FeatureCollection", features: [] });
    if (!on) { const p = map.getSource("selpreview"); if (p) p.setData({ type: "FeatureCollection", features: [] }); }
  }

  // Re-emit the all-cells layer so newly-selected cells pick up sel=1.
  _refreshCellSel() {
    const s = this._map && this._map.getSource("selcells");
    if (s) s.setData(this._cellsFC(this._catalog, true));
  }

  // -- chart-select map mode ----------------------------------------------
  // Charts view turns the map into a dedicated selection surface: the rendered
  // ENC symbology is hidden so only the offline basemap (land/sea) + the catalog
  // cell boxes show, framed out to a wide overview, with box-select armed. Home
  // restores the chart render and the view you came from.

  // Toggle visibility of the ENC render: every per-band chart layer
  // (source:"chart-…") plus the world "no chart data" hatch. The offline basemap
  // (source:"coastline"), the sea background, and the selection overlays stay put,
  // so Charts mode reads as a clean land/sea map with just the cell boxes on top.
  _setChartLayersVisible(on) {
    const map = this._map;
    if (!map || !map.getStyle) return;
    const isChartLayer = (l) => (l.source && l.source.startsWith("chart-")) || l.id === "nodata";
    for (const l of map.getStyle().layers || []) {
      if (!isChartLayer(l)) continue;
      // Restoring is NOT a blanket "visible": a couple of chart layers are kept
      // hidden by mariner toggles (shallow-pattern, contour-labels), so re-derive
      // those from the current settings rather than force-showing them — otherwise
      // leaving Charts mode would switch them on while the settings still read off.
      let vis = on ? "visible" : "none";
      if (on && l.id.startsWith("shallow-pattern")) vis = this._mariner.shallowPattern ? "visible" : "none";
      else if (on && l.id.startsWith("contour-labels")) vis = this._mariner.showContourLabels ? "visible" : "none";
      else if (on && l.id === "nodata") vis = this._mariner.showNoData === false ? "none" : "visible";
      map.setLayoutProperty(l.id, "visibility", vis);
    }
  }

  // Enter selection mode: hide the ENC render, show the cell overlay, frame to a
  // wide view (remembering where we were so Home can fly back). The map pans/zooms
  // freely; cells are picked by tapping them (see the map click handler) or via
  // the region buttons / search / cell list.
  _enterChartsMode() {
    if (!this._map) return;
    const first = !this._chartsMode;
    if (first) this._preChartsView = { center: this._map.getCenter(), zoom: this._map.getZoom() };
    this._chartsMode = true;
    this._setChartLayersVisible(false);
    this._setCellOverlay(true);
    const wb = this.shadowRoot.getElementById("world-btn");
    if (wb) wb.hidden = false; // "All charts" jump-to-world control
    // Zoom all the way out so every catalog cell is in frame at once (small) — the
    // "show the world" selection map. Framed after the drawer finishes opening (it
    // resizes the map at ~230ms) so the coverage centres in the narrower map area
    // rather than the full pre-open width. See setDrawerOpen's resize callback.
    if (first) this._pendingChartsFrame = true;
  }
  // Extent that frames essentially all NOAA coverage, centred on the bulk (cached).
  // Uses the 2nd–98th percentile of cell centres rather than the raw union: a
  // handful of far-flung EEZ/territory cells (Guam, American Samoa, the Arctic,
  // mid-Atlantic) would otherwise stretch the box across an empty ocean and push
  // the mainland into a corner. Those outliers are a region-button tap away.
  _catalogBounds() {
    if (this._catBounds) return this._catBounds;
    const xs = [], ys = [];
    for (const c of this._catalog) {
      const b = c.bb;
      if (!Array.isArray(b) || b.length !== 4) continue;
      xs.push((b[0] + b[2]) / 2); ys.push((b[1] + b[3]) / 2);
    }
    if (!xs.length) return null;
    xs.sort((a, b) => a - b); ys.sort((a, b) => a - b);
    const q = (arr, p) => arr[Math.floor((arr.length - 1) * p)];
    this._catBounds = [[q(xs, 0.02) - 2, Math.max(q(ys, 0.02) - 2, -84)], [q(xs, 0.98) + 2, Math.min(q(ys, 0.98) + 2, 84)]];
    return this._catBounds;
  }
  // Frame the selection map to the full coverage extent, centred in the visible
  // (drawer-open) map area.
  _frameChartsWorld() {
    const b = this._catalogBounds();
    if (this._map && b) this._map.fitBounds(b, { padding: 26, duration: 400 });
  }

  // Leave selection mode: disarm box-select, restore the ENC render + the
  // pre-Charts view, drop the cell overlay.
  _exitChartsMode() {
    this._cancelAreaSelect();
    this._setCellOverlay(false);
    this._clearFocus();
    const wb = this.shadowRoot.getElementById("world-btn");
    if (wb) wb.hidden = true;
    if (!this._chartsMode) return;
    this._chartsMode = false;
    this._setChartLayersVisible(true);
    if (this._preChartsView && this._map) {
      const v = this._preChartsView;
      this._preChartsView = null;
      // Defer past the drawer-close resize (230ms) so the restore isn't truncated.
      setTimeout(() => { if (!this._chartsMode && this._map) this._map.easeTo({ center: v.center, zoom: v.zoom, duration: 400 }); }, 280);
    }
  }

  // Click a cell on the selection map to add/remove it. Picks the finest (largest-
  // scale) enabled cell whose footprint contains the point, then toggles it. A
  // plain MapLibre "click" only fires when the gesture wasn't a pan, so dragging
  // still pans the map.
  _toggleCellAt(px, py) {
    if (!this._map) return;
    const ll = this._map.unproject([px, py]);
    let best = null;
    for (const c of this._catalog) {
      const b = c.bb;
      if (!Array.isArray(b) || b.length !== 4 || !this._bandOn(c)) continue;
      if (ll.lng >= b[0] && ll.lng <= b[2] && ll.lat >= b[1] && ll.lat <= b[3]) {
        if (!best || (c.s || 0) < (best.s || 0)) best = c; // finest = smallest scale denom
      }
    }
    if (best) this._toggleAreaCell(best.n);
  }

  // Live amber preview of the cells the current drag box ([w,s,e,n]) will grab.
  _setPreviewBox(box) {
    const map = this._map;
    if (!map) return;
    let fc = { type: "FeatureCollection", features: [] };
    if (box) {
      const [w, s, e, n] = box;
      const hit = [];
      for (const c of this._catalog) {
        const b = c.bb;
        if (Array.isArray(b) && b.length === 4 && !(b[2] < w || b[0] > e || b[3] < s || b[1] > n)) hit.push(c);
      }
      fc = this._cellsFC(hit, false);
    }
    const src = map.getSource("selpreview");
    if (src) src.setData(fc);
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

  // A not-downloaded in-view cell was tapped in the coverage HUD: add it to the
  // map selection (enabling its band so it counts) and open the Charts selector
  // so it can be downloaded.
  _downloadCellRegion(name) {
    const c = this._byName.get(name);
    if (c) {
      this._selBands.add(bandForScale(c.s));
      this._saveSelBands();
      this._areaCells.add(name);
      this._saveAreaCells();
      this._refreshCellSel();
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
          this._importedArchives.push(file); // keep in memory so a coverage rebuild can re-add it
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
    // Re-bake the in-browser wasm tiles from the now-larger stored cell set.
    await this._refreshRealtime();
  }

  // Reload every stored cell into the wasm baker (the 100%-wasm render path) and
  // reflect coverage in the empty state. Called after any import.
  async _refreshRealtime() {
    if (!this._plotter) return;
    try {
      const rt = await this._plotter.loadStoreCells();
      this._refreshInstalledBounds();
      if (rt && rt.ok && rt.names && rt.names.length) {
        this._hasArchive = true;
        this.updateEmptyState();
        this._frameCells(rt.names);
      }
    } catch (e) { console.warn("[realtime] refresh", e); }
  }

  // Rebuild the installed-cell coverage outlines (shown when zoomed out past a
  // cell's detail zoom) from the browser store + catalog footprints.
  _refreshInstalledBounds() {
    const src = this._map && this._map.getSource("inst-bounds");
    if (!src) return;
    const feats = [];
    for (const name of this._installed) {
      const c = this._byName.get(name);
      if (!c || !Array.isArray(c.bb) || c.bb.length !== 4) continue;
      const [w, s, e, n] = c.bb;
      feats.push({
        type: "Feature",
        properties: { name, band: bandForScale(c.s) },
        geometry: { type: "Polygon", coordinates: [[[w, s], [e, s], [e, n], [w, n], [w, s]]] },
      });
    }
    src.setData({ type: "FeatureCollection", features: feats });
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
    if (this._chartsMode) return; // user already opened the selection map — don't yank it
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
    this._setProgress({ label: `Imported ${imported.length} chart${imported.length > 1 ? "s" : ""}`, sub: "Ready", frac: 1 });
    this.updateEmptyState();
    this.renderCharts();
    this.refreshBoxes();
    this.renderArchiveList();
    // New cells stored → the wasm baker renders them on demand (no pre-bake).
    if (imported.length) await this._refreshRealtime();
    setTimeout(() => this._setProgress(null), 1200);
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
      this._importedArchives.push(blob); // keep in memory so a coverage rebuild can re-add it
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
    // per region), so this only handles locally-imported
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
    this.setAttribute("data-scheme", name);
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
          --drawer-w:clamp(340px, 33%, 560px);
          --ui-bg:#fafafa; --ui-surface:#fff; --ui-surface-2:#eef1f4; --ui-text:#2a2f35; --ui-text-dim:#7a828b; --ui-text-faint:#9aa0a8; --ui-border:#e2e2e2; --ui-border-2:#ededed; --ui-border-strong:#cfcfcf; --ui-hover:#f0f3f6; --ui-accent:#1565c0; --ui-accent-hover:#1257a8; --ui-accent-text:#fff; --ui-shadow:rgba(0,0,0,.2); }
        :host([data-scheme="dusk"]) {
          --ui-bg:#20262b; --ui-surface:#2a3137; --ui-surface-2:#333b42; --ui-text:#cdd6dc; --ui-text-dim:#9aa6ae; --ui-text-faint:#7d8990; --ui-border:#3a434a; --ui-border-2:#333b42; --ui-border-strong:#4a555d; --ui-hover:#353f47; --ui-accent:#4f9be6; --ui-accent-hover:#69abe9; --ui-accent-text:#0c1318; --ui-shadow:rgba(0,0,0,.5); }
        :host([data-scheme="night"]) {
          --ui-bg:#14181b; --ui-surface:#1b2024; --ui-surface-2:#232a2f; --ui-text:#aeb8be; --ui-text-dim:#7e898f; --ui-text-faint:#626c72; --ui-border:#2a3137; --ui-border-2:#232a2f; --ui-border-strong:#38424a; --ui-hover:#232a30; --ui-accent:#3f7fb5; --ui-accent-hover:#4d8cc2; --ui-accent-text:#0a0e11; --ui-shadow:rgba(0,0,0,.6); }
        /* The map sits right of the 56px rail; when the drawer flies out it shrinks
           to clear the drawer rather than being overlaid. */
        #map { position:absolute; inset:0 0 0 56px; transition:left .2s; }
        #map.with-drawer { left:calc(56px + var(--drawer-w)); }
        #map chart-plotter { width:100%; height:100%; }
        .btn { cursor:pointer; border:1px solid var(--ui-border-strong); background:var(--ui-surface); border-radius:6px; padding:6px 10px; font:inherit; color:var(--ui-text); }
        .btn:hover { background:var(--ui-hover); }
        /* persistent left rail — the sidebar's docked spine (drawer flies out from it) */
        #rail { position:absolute; left:0; top:0; bottom:0; width:56px; z-index:7; background:var(--ui-surface); border-right:1px solid var(--ui-border);
          box-shadow:1px 0 5px rgba(0,0,0,.07); display:flex; flex-direction:column; align-items:center; gap:4px; padding-top:10px; }
        #rail .ri { width:48px; min-height:48px; border:none; background:none; border-radius:11px; cursor:pointer; color:var(--ui-text-dim);
          display:flex; flex-direction:column; align-items:center; justify-content:center; gap:3px; padding:7px 0;
          transition:background .12s, color .12s; }
        #rail .ri:hover { background:var(--ui-surface-2); color:var(--ui-accent); }
        #rail .ri.on { background:var(--ui-accent); color:var(--ui-accent-text); }
        #rail .ri svg { width:21px; height:21px; display:block; }
        #rail .ri .cap { font-size:9.5px; font-weight:500; letter-spacing:.02em; }
        #rail .spacer { flex:1; }
        /* band filter chips (Add charts view) + on-map box-select rectangle */
        .band-chip { display:inline-flex; align-items:center; gap:6px; cursor:pointer; border:1px solid var(--ui-border); border-radius:16px;
          padding:4px 10px; font-size:12px; background:var(--ui-surface); user-select:none; color:var(--ui-text); }
        .band-chip .sw { width:11px; height:11px; border-radius:3px; }
        .band-chip.off { opacity:.38; }
        .box-sel { position:absolute; z-index:5; border:2px solid var(--ui-accent); background:rgba(21,101,192,.12); pointer-events:none; }
        /* charts panel: action header + "your charts" cards */
        .charts-actions { display:flex; gap:8px; margin-bottom:10px; }
        .cta { flex:1; background:var(--ui-accent); color:var(--ui-accent-text); border:none; border-radius:8px; padding:11px 12px; font:inherit;
          font-weight:600; cursor:pointer; display:inline-flex; align-items:center; justify-content:center; gap:7px; }
        .cta:hover { background:var(--ui-accent-hover); }
        .cta svg { width:17px; height:17px; }
        .upd { display:inline-flex; align-items:center; gap:6px; white-space:nowrap; }
        .charts-summary { color:var(--ui-text-dim); font-size:12px; margin:0 0 12px; }
        .charts-empty { text-align:center; color:var(--ui-text-faint); padding:26px 10px; }
        .chart-card { display:flex; align-items:flex-start; gap:10px; padding:11px 0; border-bottom:1px solid var(--ui-border-2); }
        .chart-card .cc-dot { width:10px; height:10px; border-radius:3px; flex:none; margin-top:3px; }
        .chart-card .cc-main { flex:1; min-width:0; }
        .chart-card .cc-title { font-weight:600; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
        .chart-card .cc-meta { color:var(--ui-text-dim); font-size:12px; margin-top:1px; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
        .chart-card .cc-edition { font-size:12px; color:var(--ui-text-faint); margin-top:4px; display:flex; align-items:center; gap:7px; flex-wrap:wrap; }
        .chart-card .cc-actions { flex:none; display:flex; align-items:center; gap:4px; }
        .cc-btn { border:1px solid var(--ui-border-strong); background:var(--ui-surface); color:var(--ui-text-dim); border-radius:7px; width:30px; height:30px; cursor:pointer;
          font-size:14px; display:inline-flex; align-items:center; justify-content:center; }
        .cc-btn:hover { background:var(--ui-hover); color:var(--ui-accent); border-color:#b9c0c8; }
        .cc-btn.cc-rm:hover { color:#c0392b; border-color:#e2b6b1; background:#fdeceb; }
        /* freshness pill */
        .fresh { font-size:10.5px; font-weight:600; padding:1px 8px; border-radius:10px; }
        .fresh.current { background:#e4f5ea; color:#1f7a36; }
        .fresh.aging { background:#fbf0d8; color:#8a6000; }
        .fresh.stale { background:#fbe3e1; color:#c0392b; }
        .chart-card.focus { background:var(--ui-hover); box-shadow:inset 3px 0 0 var(--ui-accent); }
        .chart-card.clickable { cursor:pointer; }
        .chart-card.clickable:hover { background:var(--ui-hover); }
        /* in-drawer "Add charts" view */
        .add-head { display:flex; align-items:center; justify-content:space-between; margin-bottom:6px; }
        .add-head strong { font-size:15px; }
        .add-hint { color:var(--ui-text-dim); font-size:12px; line-height:1.5; margin:0 0 12px; }
        #charts-add .band-row { display:flex; flex-wrap:wrap; gap:6px; margin-bottom:12px; }
        .add-tools { display:flex; gap:8px; margin-bottom:4px; }
        .add-tools .tool { flex:1; border:1px solid var(--ui-border-strong); background:var(--ui-surface); border-radius:8px; padding:9px; font:inherit; font-size:13px; cursor:pointer; color:var(--ui-text); }
        .add-tools .tool:hover { background:var(--ui-hover); }
        .add-tools .tool.on { background:var(--ui-accent); color:var(--ui-accent-text); border-color:var(--ui-accent); }
        .add-sel { border-top:1px solid var(--ui-border-2); margin-top:14px; padding-top:14px; }
        .add-sel .empty { color:var(--ui-text-faint); font-size:13px; text-align:center; padding:6px 0; }
        .add-sel .sel-line { display:flex; align-items:center; justify-content:space-between; margin-bottom:10px; font-weight:600; }
        .add-clear { background:none; border:none; color:var(--ui-accent); cursor:pointer; font:inherit; }
        .add-dl { display:block; width:100%; box-sizing:border-box; background:var(--ui-accent); color:var(--ui-accent-text); border:none;
          border-radius:8px; padding:11px; font:inherit; font-weight:600; cursor:pointer; }
        .add-dl:hover { background:var(--ui-accent-hover); }
        .add-dl:disabled { background:#9fb6cf; cursor:default; }
        .add-dl:disabled:hover { background:#9fb6cf; }
        .linkbtn:disabled { color:var(--ui-text-faint); cursor:default; text-decoration:none; }
        /* region browser */
        .region-search { width:100%; box-sizing:border-box; border:1px solid var(--ui-border-strong); border-radius:8px; padding:9px 12px; font:inherit; margin-bottom:10px; background:var(--ui-surface); color:var(--ui-text); }
        .region-search:focus { outline:none; border-color:var(--ui-accent); }
        .region-list { display:flex; flex-direction:column; }
        .region-group { font-size:11px; text-transform:uppercase; letter-spacing:.05em; color:var(--ui-text-faint); font-weight:700; margin:12px 0 4px; }
        .region-row { display:flex; align-items:center; gap:9px; width:100%; text-align:left;
          border:none; background:none; border-bottom:1px solid var(--ui-border-2); padding:10px 4px; font:inherit; cursor:pointer; }
        .region-row:hover { background:var(--ui-hover); }
        .region-row .region-name { font-weight:600; color:var(--ui-text); flex:1; min-width:0; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
        .region-row .region-meta { flex:none; color:var(--ui-text-faint); font-size:12px; }
        .rdot { flex:none; width:9px; height:9px; border-radius:50%; box-shadow:inset 0 0 0 1.5px #c2c8cf; }
        .rdot.full { background:#1f9d4d; box-shadow:none; }
        .rdot.partial { background:#f0a500; box-shadow:none; }
        .rdot.none { background:transparent; }
        .region-empty { color:var(--ui-text-faint); text-align:center; padding:20px; }
        /* "On this device" coverage rows */
        .owned-row { display:flex; align-items:center; justify-content:space-between; gap:10px; padding:9px 2px; border-bottom:1px solid var(--ui-border-2); }
        .owned-row:last-of-type { border-bottom:none; }
        .owned-row b { font-weight:600; }
        .owned-actions { display:flex; align-items:center; gap:12px; flex:none; }
        .region-title { margin:4px 0 2px; font-size:16px; }
        .region-status { background:#e4f5ea; color:#1f7a36; font-weight:600; font-size:12.5px; padding:6px 10px; border-radius:8px; margin:2px 0 4px; }
        .linkbtn { background:none; border:none; color:var(--ui-accent); cursor:pointer; font:inherit; padding:8px 0; display:block; }
        .linkbtn.danger { color:#c0392b; }
        /* persistent in-flight download/import pill (bottom-centre) */
        #dlpill { position:absolute; bottom:42px; left:50%; transform:translateX(-50%); z-index:7; display:inline-flex; align-items:center;
          gap:9px; background:var(--ui-accent); color:var(--ui-accent-text); border:none; border-radius:22px; padding:8px 16px; font:inherit; font-size:13px; font-weight:600;
          cursor:pointer; box-shadow:0 4px 16px rgba(0,0,0,.28); }
        #dlpill[hidden] { display:none; }
        #dlpill.error { background:#c0392b; }
        #dlpill .dlp-spin { width:13px; height:13px; border:2px solid rgba(255,255,255,.4); border-top-color:#fff; border-radius:50%; animation:dlspin .8s linear infinite; }
        #dlpill.error .dlp-spin { display:none; }
        @keyframes dlspin { to { transform:rotate(360deg); } }
        /* chart info pill (map popup when focusing a chart from the list) */
        .chart-pill { font:13px/1.4 system-ui,sans-serif; min-width:170px; }
        .chart-pill .cp-title { font-weight:600; margin-bottom:2px; }
        .chart-pill .cp-meta { color:var(--ui-text-dim); font-size:12px; }
        .chart-pill .cp-ed { margin-top:5px; display:flex; align-items:center; gap:6px; flex-wrap:wrap; font-size:12px; color:var(--ui-text-dim); }
        /* settings */
        .set-section { margin:0 0 22px; }
        .set-section > h3 { font-size:11px; text-transform:uppercase; letter-spacing:.05em; color:var(--ui-text-faint); margin:0 0 4px; font-weight:700; }
        /* charts selector: region quick-picks */
        .rg-head { display:flex; align-items:center; justify-content:space-between; gap:8px; }
        .rg-head > h3 { margin-bottom:8px; }
        .region-grid { display:grid; grid-template-columns:1fr 1fr; gap:7px; }
        .region-btn { display:flex; align-items:center; justify-content:space-between; gap:8px; border:1px solid var(--ui-border-strong); background:var(--ui-surface); color:var(--ui-text); border-radius:8px; padding:9px 11px; font:inherit; font-size:13px; font-weight:500; cursor:pointer; text-align:left; }
        .region-btn:hover { background:var(--ui-hover); border-color:var(--ui-accent); }
        .region-btn .rb-n { color:var(--ui-text-faint); font-size:11px; font-weight:600; font-variant-numeric:tabular-nums; }
        .region-btn.on { background:var(--ui-accent); color:var(--ui-accent-text); border-color:var(--ui-accent); }
        .region-btn.on .rb-n { color:var(--ui-accent-text); opacity:.8; }
        /* selection bar */
        .sel-bar { margin:0 0 16px; }
        .sel-bar.empty { background:var(--ui-surface-2); border-radius:8px; padding:10px 12px; }
        .sel-bar.empty .muted { font-size:12px; line-height:1.45; }
        .sel-bar .sel-count { margin-bottom:4px; font-size:13px; }
        .sel-bar .sel-change { margin-bottom:8px; font-size:12px; font-weight:600; color:var(--ui-accent); }
        .sel-bar #area-revert { padding-top:6px; }
        /* cell list (browse + inspect) */
        .cell-list-sec { margin-bottom:18px; }
        .cl-row { border-bottom:1px solid var(--ui-border-2); }
        .cl-row:last-child { border-bottom:none; }
        .cl-main { display:flex; align-items:center; gap:9px; padding:8px 2px; cursor:pointer; }
        .cl-main:hover, .cl-row.open > .cl-main { background:var(--ui-hover); }
        .cl-dot { width:10px; height:10px; border-radius:3px; flex:none; }
        .cl-dot.sm { width:9px; height:9px; display:inline-block; vertical-align:-1px; margin-right:5px; border-radius:2px; }
        .cl-text { flex:1; min-width:0; display:flex; flex-direction:column; }
        .cl-title { font-weight:500; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
        .cl-sub { color:var(--ui-text-faint); font-size:12px; font-variant-numeric:tabular-nums; }
        .cl-toggle { flex:none; width:26px; height:26px; border:1px solid var(--ui-border-strong); background:var(--ui-surface); color:var(--ui-text-dim); border-radius:7px; cursor:pointer; font-size:14px; line-height:1; display:inline-flex; align-items:center; justify-content:center; }
        .cl-toggle:hover { border-color:var(--ui-accent); color:var(--ui-accent); }
        .cl-toggle.on { background:var(--ui-accent); color:var(--ui-accent-text); border-color:var(--ui-accent); }
        .cl-detail { padding:4px 2px 12px 31px; display:grid; grid-template-columns:auto 1fr; gap:4px 12px; font-size:12px; }
        .cl-detail .k { color:var(--ui-text-faint); }
        .cl-detail .v { color:var(--ui-text); }
        .cl-empty { color:var(--ui-text-faint); font-size:13px; text-align:center; padding:14px 0; }
        .cl-more { color:var(--ui-text-faint); font-size:12px; text-align:center; padding:10px 0 2px; }
        /* NOAA data freshness footer */
        .data-fresh { color:var(--ui-text-faint); font-size:11.5px; text-align:center; line-height:1.5; padding:14px 0 4px; border-top:1px solid var(--ui-border-2); margin-top:4px; }
        .set-row { display:flex; align-items:center; justify-content:space-between; gap:18px; padding:10px 0; border-bottom:1px solid var(--ui-border-2); }
        .set-row:last-child { border-bottom:none; }
        .set-row .lbl { display:flex; flex-direction:column; min-width:0; }
        .set-row .lbl .t { font-weight:500; }
        .set-row .lbl .d { font-size:12px; color:var(--ui-text-faint); margin-top:1px; }
        .set-row .ctl { flex:none; display:flex; align-items:center; gap:6px; }
        .set-row .ctl input[type=number] { width:58px; text-align:right; border:1px solid var(--ui-border-strong); border-radius:6px; padding:5px 7px; font:inherit; background:var(--ui-surface); color:var(--ui-text); }
        .set-row .ctl .unit { color:var(--ui-text-faint); font-size:12px; width:14px; }
        .set-row .ctl select { border:1px solid var(--ui-border-strong); border-radius:6px; padding:5px 8px; font:inherit; background:var(--ui-surface); color:var(--ui-text); }
        /* toggle switch */
        .switch { position:relative; width:38px; height:22px; display:inline-block; flex:none; }
        .switch input { opacity:0; width:0; height:0; }
        .switch .sl { position:absolute; inset:0; background:var(--ui-border-strong); border-radius:22px; cursor:pointer; transition:.15s; }
        .switch .sl:before { content:""; position:absolute; width:16px; height:16px; left:3px; top:3px; background:#fff; border-radius:50%; transition:.15s; box-shadow:0 1px 2px rgba(0,0,0,.3); }
        .switch input:checked + .sl { background:var(--ui-accent); }
        .switch input:checked + .sl:before { transform:translateX(16px); }
        /* segmented control */
        .seg { display:inline-flex; border:1px solid var(--ui-border-strong); border-radius:7px; overflow:hidden; }
        .seg button { border:none; background:var(--ui-surface); padding:6px 11px; font:inherit; font-size:13px; cursor:pointer; border-left:1px solid var(--ui-border-2); color:var(--ui-text); }
        .seg button:first-child { border-left:none; }
        .seg button.sel { background:var(--ui-accent); color:var(--ui-accent-text); }
        .seg-multi { display:inline-flex; gap:12px; }
        .seg-multi .chk { display:inline-flex; align-items:center; gap:5px; cursor:pointer; }
        /* Bottom statusbar: live readout (left) · in-view band pills (right). */
        #statusbar { position:absolute; left:56px; right:0; bottom:0; z-index:6; height:30px;
          display:flex; align-items:center; gap:14px; padding:0 12px; box-sizing:border-box;
          background:var(--ui-surface); border-top:1px solid rgba(0,0,0,.08);
          box-shadow:0 -1px 6px rgba(0,0,0,.07); backdrop-filter:blur(5px);
          font:12px system-ui,sans-serif; color:var(--ui-text); transition:left .2s; }
        #statusbar.with-drawer { left:calc(56px + var(--drawer-w)); }
        /* NOAA attribution — a pill DEBOSSED into the chart: faint inset fill +
           inset shadow (pressed-in) with a light bottom bevel, under an engraved
           letterpress text effect, so the whole pill reads as embossed in the map. */
        #noaa-attr { position:absolute; right:12px; bottom:38px; z-index:5; pointer-events:auto;
          font:600 11px/1.4 system-ui,sans-serif; letter-spacing:.01em;
          color:var(--ui-text-dim); text-shadow:0 1px 0 rgba(255,255,255,.7);
          background:var(--ui-surface); border-radius:10px; padding:3px 10px; border:1px solid rgba(0,0,0,.06);
          box-shadow:inset 0 1px 2px rgba(0,0,0,.22), inset 0 -1px 0 rgba(255,255,255,.5), 0 1px 0 rgba(255,255,255,.45); }
        #noaa-attr a, #noaa-attr .attr-link { color:inherit; text-shadow:inherit; cursor:pointer;
          text-decoration:underline; text-decoration-color:rgba(33,40,48,.32); text-underline-offset:2px; }
        #noaa-attr a:hover, #noaa-attr .attr-link:hover { color:rgba(18,24,31,.82); }
        #noaa-attr .attr-link { background:none; border:none; padding:0; font:inherit; }
        /* NOAA ENC user-agreement gate (shown before the first download). */
        .modal { position:absolute; inset:0; z-index:30; display:flex; align-items:center; justify-content:center;
          background:rgba(15,20,26,.55); backdrop-filter:blur(2px); }
        .modal[hidden] { display:none; }
        .modal-card { background:var(--ui-surface); max-width:520px; width:calc(100% - 40px); max-height:86%; overflow:auto;
          border-radius:12px; padding:20px 22px; box-shadow:0 12px 40px rgba(0,0,0,.3); font:14px/1.5 system-ui,sans-serif; color:var(--ui-text); }
        .modal-card h2 { margin:0 0 10px; font-size:18px; }
        .modal-card .agree-body ul { margin:8px 0; padding-left:20px; }
        .modal-card .agree-body li { margin:5px 0; }
        .modal-card a { color:var(--ui-accent); }
        .agree-actions { display:flex; gap:10px; justify-content:flex-end; margin-top:16px; }
        /* Live band·scale·zoom readout (left of the statusbar), one line. Each
           field has a fixed width + tabular figures so the bar never reflows. */
        /* Statusbar inspect toggle (bottom-left). */
        .sb-btn { display:inline-flex; align-items:center; gap:5px; flex:none; border:1px solid var(--ui-border-strong); background:var(--ui-surface);
          color:var(--ui-text-dim); border-radius:13px; padding:3px 10px 3px 8px; font:600 11px/1 system-ui,sans-serif; cursor:pointer; white-space:nowrap; }
        .sb-btn svg { width:14px; height:14px; }
        .sb-btn:hover { border-color:var(--ui-accent); color:var(--ui-accent); }
        .sb-btn.on { background:var(--ui-accent); color:var(--ui-accent-text); border-color:var(--ui-accent); }
        .ins-lock { background:var(--ui-surface-2); color:var(--ui-text-dim); border-radius:6px; padding:6px 9px; margin-bottom:10px; font-size:12px; }
        .ins-cycler { display:flex; align-items:center; justify-content:center; gap:10px; margin-bottom:10px; font-size:12px; color:var(--ui-text-dim); }
        .ins-cycler .btn { padding:2px 9px; line-height:1.3; }
        /* Tile-bake activity indicator (spinner + count), shown only while the
           wasm worker is baking tiles on demand. */
        .sb-bake { display:inline-flex; align-items:center; gap:6px; flex:none; color:var(--ui-accent);
          font:600 11px/1 system-ui,sans-serif; white-space:nowrap; font-variant-numeric:tabular-nums; }
        .sb-bake[hidden] { display:none; }
        .sb-bake-spin { width:12px; height:12px; flex:none; border-radius:50%;
          border:2px solid color-mix(in srgb, var(--ui-accent) 30%, transparent); border-top-color:var(--ui-accent);
          animation:sb-bake-spin .7s linear infinite; }
        @keyframes sb-bake-spin { to { transform:rotate(360deg); } }
        @media (prefers-reduced-motion: reduce) { .sb-bake-spin { animation-duration:2s; } }
        .sb-readout { flex:none; }
        .sb-readout .hud-main { display:inline-flex; align-items:center; gap:10px; font-weight:600; font-size:12px; white-space:nowrap; font-variant-numeric:tabular-nums; }
        .sb-readout .hud-dot { width:8px; height:8px; border-radius:50%; flex:none; box-shadow:0 0 0 2px rgba(255,255,255,.6); margin-right:-4px; }
        .sb-readout .hud-band { display:inline-block; min-width:62px; }
        .sb-readout .hud-scale { color:var(--ui-accent); display:inline-block; min-width:92px; }
        .sb-readout .hud-z { display:inline-block; min-width:42px; color:var(--ui-text-dim); }
        /* In-view band pills, right-aligned; each opens a cell-list popup. */
        .sb-bands { display:flex; align-items:center; gap:8px; min-width:0; margin-left:auto; }
        .sb-band-wrap { position:relative; flex:none; }
        .sb-band { display:inline-flex; align-items:center; gap:5px; font:600 11px/1 system-ui,sans-serif; color:var(--ui-text);
          background:var(--ui-surface); border:1px solid rgba(0,0,0,.14); border-radius:13px; padding:4px 9px; cursor:pointer; white-space:nowrap; }
        .sb-band:hover { border-color:var(--ui-accent); }
        .sb-band .sb-dot { width:8px; height:8px; border-radius:50%; background:var(--bc); flex:none; }
        .sb-band .sb-ct { color:var(--ui-text-faint); font-weight:500; }
        .sb-band .sb-miss { color:var(--ui-accent); font-weight:700; }
        .sb-band.has-missing { border-color:rgba(21,101,192,.5); }
        /* Cell-list popup above a band pill (hover on desktop; tap to pin on touch). */
        .band-pop { display:none; position:absolute; bottom:calc(100% + 6px); right:0; z-index:10;
          background:var(--ui-surface); border:1px solid rgba(0,0,0,.1); border-radius:9px; padding:8px 9px;
          box-shadow:0 6px 22px rgba(0,0,0,.22); width:max-content; max-width:280px; }
        .band-pop::before { content:""; position:absolute; left:0; right:0; bottom:-6px; height:6px; } /* hover bridge over the gap */
        .sb-band-wrap:hover .band-pop, .band-pop:hover, .sb-band-wrap.open .band-pop { display:block; }
        .band-pop-h { font:600 11px/1.3 system-ui,sans-serif; color:var(--ui-text-dim); margin-bottom:6px; }
        .band-pop-cells { display:flex; flex-wrap:wrap; gap:4px; max-height:210px; overflow-y:auto; }
        .cov-cell { font:11px/1.6 ui-monospace,SFMono-Regular,Menlo,monospace; padding:0 6px; border-radius:5px;
          border:1px solid rgba(0,0,0,.12); background:var(--ui-surface-2); color:var(--ui-text); cursor:pointer; }
        .cov-cell:hover { border-color:var(--ui-accent); color:var(--ui-accent); }
        .cov-cell.missing { background:repeating-linear-gradient(45deg,var(--ui-surface),var(--ui-surface) 4px,var(--ui-surface-2) 4px,var(--ui-surface-2) 8px);
          color:var(--ui-text-faint); border-style:dashed; }
        .cov-cell.missing::after { content:" ↓"; color:var(--ui-accent); }
        .cov-cell.missing:hover { color:var(--ui-accent); border-color:var(--ui-accent); }
        .cov-empty { font:12px system-ui,sans-serif; color:var(--ui-text-faint); }
        #loading { position:absolute; top:12px; left:50%; transform:translateX(-50%); z-index:5; background:rgba(0,0,0,.72);
          color:#fff; border-radius:14px; padding:5px 12px; font-size:12px; box-shadow:0 1px 4px rgba(0,0,0,.3); }
        #drawer { position:absolute; top:0; left:56px; width:var(--drawer-w); height:100%; background:var(--ui-bg); color:var(--ui-text);
          box-shadow:2px 0 8px rgba(0,0,0,.25); z-index:6; transform:translateX(calc(-100% - 56px)); transition:transform .2s; display:flex; flex-direction:column; }
        #drawer.open { transform:none; }
        /* Feature inspector — slides in from the RIGHT (overlays the map). */
        .inspect { position:absolute; top:0; right:0; width:clamp(300px, 30%, 460px); height:100%; z-index:8;
          background:var(--ui-bg); color:var(--ui-text); box-shadow:-2px 0 10px rgba(0,0,0,.3);
          transform:translateX(100%); transition:transform .2s; display:flex; flex-direction:column; font:13px/1.45 system-ui,sans-serif; }
        .inspect.open { transform:none; }
        .ins-head { display:flex; align-items:center; gap:8px; padding:10px 12px; border-bottom:1px solid var(--ui-border); }
        .ins-head strong { flex:1; font-size:14px; }
        .ins-body { overflow:auto; padding:12px; flex:1; }
        .ins-empty { color:var(--ui-text-faint); text-align:center; padding:24px 10px; }
        .ins-feat { margin:0 0 14px; border:1px solid var(--ui-border-2); border-radius:8px; overflow:hidden; }
        .ins-feat .ins-title { padding:8px 10px; background:var(--ui-surface-2); font-weight:600; display:flex; align-items:baseline; gap:8px; flex-wrap:wrap; }
        .ins-feat .ins-acr { color:var(--ui-text-dim); font:11px/1 ui-monospace,SFMono-Regular,Menlo,monospace; font-weight:500; }
        .ins-feat .ins-layer { margin-left:auto; color:var(--ui-text-faint); font-size:11px; }
        .ins-feat.ins-clickable { cursor:pointer; }
        .ins-feat.ins-clickable:hover { border-color:#00b8d4; }
        .ins-feat.active { border-color:#00b8d4; box-shadow:0 0 0 1px #00b8d4 inset; }
        .ins-feat.active .ins-title { background:rgba(0,184,212,.14); }
        .ins-pills { padding:6px 10px 0; }
        .ins-cell { display:inline-flex; align-items:center; gap:4px; background:var(--ui-accent); color:var(--ui-accent-text);
          border-radius:11px; padding:2px 9px; font:600 11px/1.4 ui-monospace,SFMono-Regular,Menlo,monospace; letter-spacing:.02em; }
        .ins-name { padding:2px 10px 0; font-weight:600; }
        .ins-light { display:inline-flex; align-items:center; gap:4px; background:#7e3ff2; color:#fff;
          border-radius:11px; padding:2px 9px; font:600 11px/1.4 ui-monospace,SFMono-Regular,Menlo,monospace; }
        .ins-kv { display:grid; grid-template-columns:minmax(80px,auto) 1fr; gap:3px 12px; padding:8px 10px; font:12px/1.5 ui-monospace,SFMono-Regular,Menlo,monospace; }
        .ins-kv .k { color:var(--ui-text-dim); }
        .ins-kv .v { color:var(--ui-text); word-break:break-word; }
        .dhead { display:flex; align-items:center; gap:8px; padding:10px 12px; border-bottom:1px solid var(--ui-border); }
        .dhead strong { flex:1; font-size:14px; }
        .body { overflow:auto; padding:12px; flex:1; }
        .panel { display:none; } .panel.sel { display:block; }
        .drop { border:2px dashed var(--ui-border-strong); border-radius:8px; padding:18px; text-align:center; color:var(--ui-text-dim); margin-bottom:10px; }
        .drop.over { border-color:var(--ui-accent); background:var(--ui-hover); color:var(--ui-accent); }
        .row { display:flex; align-items:center; gap:8px; padding:4px 0; border-bottom:1px solid var(--ui-border-2); }
        .row .name { font-weight:600; } .row .meta { color:var(--ui-text-dim); font-size:12px; }
        .grow { flex:1; }
        .muted { color:var(--ui-text-dim); }
        label.fld { display:block; margin:8px 0; }
        label.fld span { display:inline-block; min-width:135px; }
        input[type=number] { width:64px; }
        /* progress surface (drawer): phase label + percent, bar, detail sub-line */
        .progwrap { margin:4px 0 16px; background:var(--ui-surface-2); border:1px solid var(--ui-border); border-radius:10px; padding:11px 13px; }
        .progwrap .prog-top { display:flex; align-items:baseline; justify-content:space-between; gap:10px; margin-bottom:7px; }
        .progwrap .prog-label { font-weight:600; }
        .progwrap .prog-pct { color:var(--ui-accent); font-weight:600; font-variant-numeric:tabular-nums; }
        .progwrap .prog-sub { margin-top:6px; font-size:12px; }
        progress { width:100%; height:8px; -webkit-appearance:none; appearance:none; border:none; border-radius:5px; overflow:hidden; background:var(--ui-surface-2); }
        progress::-webkit-progress-bar { background:var(--ui-surface-2); border-radius:5px; }
        progress::-webkit-progress-value { background:var(--ui-accent); border-radius:5px; }
        progress::-moz-progress-bar { background:var(--ui-accent); border-radius:5px; }
        /* collapsible "import from a file" */
        .import-more { margin-top:18px; border-top:1px solid var(--ui-border-2); padding-top:6px; }
        .import-more > summary { cursor:pointer; color:var(--ui-text-dim); font-weight:500; padding:6px 0; list-style:none; }
        .import-more > summary::-webkit-details-marker { display:none; }
        .import-more > summary:before { content:"▸ "; color:var(--ui-text-faint); }
        .import-more[open] > summary:before { content:"▾ "; }
        .legend { display:flex; gap:12px; font-size:12px; margin-bottom:10px; flex-wrap:wrap; }
        .legend i { display:inline-block; width:11px; height:11px; border-radius:2px; margin-right:4px; vertical-align:-1px; }
        #empty { position:absolute; inset:0; display:flex; align-items:center; justify-content:center; z-index:4; pointer-events:none; }
        #empty[hidden] { display:none; }
        #empty .card { pointer-events:auto; background:var(--ui-surface); color:var(--ui-text); border-radius:16px; padding:30px 30px 24px; max-width:360px;
          text-align:center; box-shadow:0 8px 34px rgba(0,0,0,.22); }
        #empty .welcome-mark { width:44px; height:44px; margin-bottom:10px; }
        #empty h2 { margin:0 0 8px; font-size:21px; }
        #empty p { color:var(--ui-text-dim); margin:0 0 18px; line-height:1.5; }
        #empty .welcome-cta { display:inline-flex; align-items:center; gap:8px; width:auto; padding:11px 22px; font-size:15px; }
        #empty .welcome-sub { margin-top:12px; font-size:13px; color:var(--ui-text-faint); }
        #empty .linkbtn { background:none; border:none; color:var(--ui-accent); cursor:pointer; font:inherit; padding:0; text-decoration:underline; }
        /* geo search */
        /* On-map search is collapsed behind a small tab button by default;
           clicking it reveals the input, which collapses again when left empty. */
        #search-tab { position:absolute; top:12px; left:50%; transform:translateX(-50%); z-index:5; width:38px; height:38px; border-radius:50%;
          border:1px solid var(--ui-border-strong); background:var(--ui-surface); color:var(--ui-text-dim); cursor:pointer;
          display:flex; align-items:center; justify-content:center; box-shadow:0 1px 4px rgba(0,0,0,.25); }
        #search-tab:hover { color:var(--ui-accent); border-color:var(--ui-accent); }
        #search-tab svg { width:18px; height:18px; }
        #search-tab[hidden], #search[hidden] { display:none; }
        /* "All charts" — jump back to the zoomed-out world view (Charts mode only).
           Sits at the top-left of the map area, which starts past the open drawer. */
        #world-btn { position:absolute; top:12px; left:calc(56px + var(--drawer-w) + 12px); z-index:5; display:inline-flex; align-items:center; gap:6px;
          border:1px solid var(--ui-border-strong); background:var(--ui-surface); color:var(--ui-text-dim); border-radius:18px; padding:7px 13px 7px 10px;
          font:600 12px system-ui,sans-serif; cursor:pointer; box-shadow:0 1px 4px rgba(0,0,0,.25); transition:left .2s; }
        #world-btn:hover { color:var(--ui-accent); border-color:var(--ui-accent); }
        #world-btn svg { width:16px; height:16px; }
        #world-btn[hidden] { display:none; }
        #search { position:absolute; top:12px; left:50%; transform:translateX(-50%); z-index:5; width:340px; max-width:44%; }
        #search input { width:100%; box-sizing:border-box; border:1px solid var(--ui-border-strong); border-radius:20px; padding:8px 16px;
          font:inherit; background:var(--ui-surface); color:var(--ui-text); box-shadow:0 1px 4px rgba(0,0,0,.3); outline:none; }
        #search input:focus { border-color:var(--ui-accent); box-shadow:0 1px 6px rgba(21,101,192,.4); }
        #search-results { margin-top:5px; background:var(--ui-surface); border-radius:12px; box-shadow:0 6px 22px rgba(0,0,0,.25); overflow:hidden; }
        #search-results[hidden] { display:none; }
        .sr-item { padding:8px 16px; cursor:pointer; border-bottom:1px solid var(--ui-border-2); }
        .sr-item:last-child { border-bottom:none; }
        .sr-item:hover, .sr-item.sel { background:var(--ui-hover); }
        .sr-item .t { font-weight:600; } .sr-item .s { color:var(--ui-text-faint); font-size:12px; }
      </style>
      <div id="map"></div>
      <div id="rail">
        <button class="ri" id="rail-home" title="Chart viewer">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="M3 10.5 12 3l9 7.5"/><path d="M5 9.5V21h14V9.5"/><path d="M9.5 21v-6h5v6"/></svg>
          <span class="cap">Home</span>
        </button>
        <button class="ri" id="rail-menu" title="Get &amp; manage charts">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="M9 3 3 5.5v15L9 18l6 3 6-2.5v-15L15 6 9 3Z"/><path d="M9 3v15M15 6v15"/></svg>
          <span class="cap">Charts</span>
        </button>
        <button class="ri" id="rail-settings" title="Settings">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1Z"/></svg>
          <span class="cap">Settings</span>
        </button>
      </div>
      <button id="search-tab" title="Search a port or area">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round"><circle cx="11" cy="11" r="7"/><path d="m20 20-3.5-3.5"/></svg>
      </button>
      <div id="search" hidden><input id="search-input" type="search" placeholder="Search a port or area…" autocomplete="off" spellcheck="false"><div id="search-results" hidden></div></div>
      <button id="world-btn" hidden title="Zoom out to all charts">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="9"/><path d="M3 12h18M12 3c2.5 2.5 3.8 5.7 3.8 9S14.5 18.5 12 21c-2.5-2.5-3.8-5.7-3.8-9S9.5 5.5 12 3Z"/></svg>
        <span>All charts</span>
      </button>
      <div id="statusbar">
        <button id="inspect-toggle" class="sb-btn" title="Inspect features — hover to highlight, click to lock, SHIFT+drag to capture an area">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="M12 12 4 9l16-5-5 16-3-8Z"/><path d="m12 12 7 7"/></svg>
          <span>Inspect</span>
        </button>
        <div id="bake-status" class="sb-bake" hidden title="Generating chart tiles">
          <span class="sb-bake-spin"></span><span class="sb-bake-txt"></span>
        </div>
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
      </div>
      <div id="inspect" class="inspect">
        <div class="ins-head"><strong>Feature inspector</strong><button id="ins-copy" class="btn" title="Copy debug data (clipboard + server)">⧉</button><button id="ins-close" class="btn" title="Close">✕</button></div>
        <div id="inspect-body" class="ins-body"></div>
      </div>`;

    // wiring
    const $ = (id) => r.getElementById(id);
    // The rail is the sidebar's docked spine: its icons pick which drawer section
    // flies out (clicking the active one closes it). "Add charts" / "Update all"
    // live inside the Charts section itself.
    $("rail-home").onclick = () => this.goHome();
    $("rail-menu").onclick = () => this.toggleSection("charts");
    $("rail-settings").onclick = () => this.toggleSection("settings");
    $("close").onclick = () => this.closeDrawer();
    $("ins-close").onclick = () => this._setInspectMode(false);
    $("ins-copy").onclick = (e) => this._copyInspectDebug(e.currentTarget);
    $("inspect-toggle").onclick = () => this._setInspectMode(!this._inspectMode);
    // Esc exits the feature inspector.
    window.addEventListener("keydown", (e) => { if (e.key === "Escape" && this._inspectMode) this._setInspectMode(false); });
    $("empty-add").onclick = () => this.openCharts();
    $("empty-import").onclick = () => { this.openCharts(); const det = r.querySelector(".import-more"); if (det) det.open = true; };
    $("rail-home").classList.add("on"); // boot shows the bare chart viewer
    // NOAA ENC user-agreement gate + attribution "Terms" link.
    $("attr-terms").onclick = () => this._showAgreement();
    $("agree-accept").onclick = () => this._resolveAgreement(true);
    $("agree-decline").onclick = () => this._resolveAgreement(false);

    // Geo search (offline, over the catalog titles). Collapsed behind a tab: the
    // button reveals the input; it tucks away again once blurred while empty.
    const si = $("search-input");
    const collapseSearch = () => { $("search").hidden = true; $("search-tab").hidden = false; };
    $("search-tab").onclick = () => { $("search").hidden = false; $("search-tab").hidden = true; si.focus(); };
    // "All charts" — re-frame the selection map to the zoomed-out world view.
    $("world-btn").onclick = () => this._frameChartsWorld();
    si.oninput = () => this.doSearch(si.value);
    si.onkeydown = (e) => {
      if (e.key === "Enter") this.gotoSearchHit(0);
      else if (e.key === "Escape") { $("search-results").hidden = true; si.value = ""; si.blur(); }
    };
    si.onfocus = () => { if (si.value.trim().length >= 2) this.doSearch(si.value); };
    si.onblur = () => setTimeout(() => {
      const sr = $("search-results"); if (sr) sr.hidden = true;
      if (!si.value.trim()) collapseSearch(); // empty → tuck back into the tab
    }, 150);

    this.renderSettings();
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
    r.getElementById("dtitle").textContent = name === "settings" ? "Settings" : "Charts";
    // Charts is the "get & see charts" mode: overlay every catalog cell on the
    // map (selected ones highlighted) so you can see and box-select coverage.
    // Any other section is just chrome over the live viewer — no overlay.
    if (name === "charts") { this.renderCharts(); this._enterChartsMode(); }
    else this._exitChartsMode();
    this.setDrawerOpen(true);
  }

  // Home: the full-screen chart viewer — drop any selection overlay/section and
  // show just the map.
  goHome() {
    this._cancelAreaSelect();
    this.closeDrawer();
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
    // Home is "active" whenever the drawer is shut — i.e. the bare chart viewer.
    r.getElementById("rail-home").classList.toggle("on", !open);
    // Closing the drawer leaves Charts mode: restore the ENC render + prior view,
    // clear the region highlight, and cancel any in-progress box drag.
    if (!open) this._exitChartsMode();
    this.updateEmptyState(); // the welcome card hides while the drawer is open
    setTimeout(() => {
      if (!this._map) return;
      this._map.resize();
      // Frame the selection map only AFTER the resize — so the fit centres the
      // coverage in the now-narrower (drawer-open) map area, not the full width.
      if (this._pendingChartsFrame) { this._pendingChartsFrame = false; this._frameChartsWorld(); }
    }, 230);
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
    if (el) el.hidden = this._hasArchive || this._drawerOpen() || !!(this._task && this._task.status === "running");
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
            `<option ${(m.boundaryStyle || "symbolized") === v ? "selected" : ""}>${v}</option>`).join("")}</select></div></div>
        ${toggle("fourShadeWater", "Four-shade water", "Four depth shades instead of two", m.fourShadeWater !== false)}
        ${toggle("showNoData", "No-data hatch", "Mark areas with no chart data (off shows the plain basemap)", m.showNoData !== false)}
        ${toggle("shallowPattern", "Shallow pattern", "Diagonal fill in shallow water", !!m.shallowPattern)}
        ${toggle("showContourLabels", "Contour labels", "Show depth values on contours", !!m.showContourLabels)}
        ${toggle("dataQuality", "Data quality", "CATZOC zones-of-confidence overlay (M_QUAL)", !!m.dataQuality)}
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
