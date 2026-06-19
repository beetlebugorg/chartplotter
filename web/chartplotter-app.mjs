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

const SCHEMES = ["day", "dusk", "night"];
const SCHEME_LABEL = { day: "Day", dusk: "Dusk", night: "Night" };
const M_TO_FT = 3.280839895; // depth-setting display conversion (values stored in metres)
const LS_SCHEME = "chartplotter:scheme";
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
  // Individually-selectable "Other" items (S-52/IMO), all default on.
  showSoundings: true,
  showLightDescriptions: true,
  showNames: true,
  // Off by default.
  showFullSectorLines: false,        // 25mm legs (engine ShowFullLengthSectorLines=false, avoids clutter)
  showIsolatedDangersShallow: false, // ISODGR01 at DisplayBase (engine default); on → Standard category
  shallowPattern: false,
  showContourLabels: false,
  dataQuality: false,
  showMetaBounds: false,
};
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
// Native min Web-Mercator zoom per band (matches CHART_BANDS in chartplotter.mjs).
// Below it a cell's chart detail isn't baked, so we draw its coverage outline.
// General is overzoomed out to z0 (it renders where no overview covers — see
// generalOverzoomMin in the baker), so it loads from z0 rather than its native z7.
const BAND_MINZOOM = { overview: 0, general: 0, coastal: 9, approach: 11, harbor: 13, berthing: 16 };
// Usage bands in coarse→fine order, for the dev band-filter rows.
const DEV_BANDS = ["overview", "general", "coastal", "approach", "harbor", "berthing"];
// Chart packs = U.S. Coast Guard districts. NOAA publishes one ENC bundle per
// district (NNCGD_ENCs.zip on charts.noaa.gov/ENCs/ENCs.shtml) and tags every
// catalog cell with its district (the `cg` field), so a pack is exactly the set
// of cells with a given `cg` — and downloading one is a single zip fetch. The
// nine districts below are the ones NOAA actually ships (2/3/4/6/10/12/15/16
// were disestablished long ago); `region`/`blurb` are friendly labels for the
// card UI. Order is roughly east→Gulf→Lakes→west→Pacific→Alaska.
const DISTRICTS = [
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

// Escape text for safe innerHTML insertion (inspector panel renders feature
// properties straight from the tiles).
function esc(s) {
  return String(s).replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]));
}

// Does a GeoJSON geometry intersect the lon/lat box [W,S,E,N]? Points test exactly;
// lines/polygons use a bbox-overlap approximation (fine for the area inspector).
// A chart vector source: the realtime path has one "chart" source; the legacy
// pmtiles path had a "chart-<band>" source per band. (Used by the inspector.)
function isChartSource(s) {
  return typeof s === "string" && (s === "chart" || s.startsWith("chart-"));
}

// A representative [lng,lat] for any GeoJSON geometry (first vertex) — used to fly
// to a search hit.
function firstCoord(g) {
  if (!g) return null;
  const c = g.coordinates;
  switch (g.type) {
    case "Point": return c;
    case "MultiPoint": case "LineString": return c[0];
    case "MultiLineString": case "Polygon": return c[0] && c[0][0];
    case "MultiPolygon": return c[0] && c[0][0] && c[0][0][0];
    default: return null;
  }
}

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

// Lon/lat bbox area of a polygon ring — used to pick the smallest (most detailed)
// cell box when several overlap under the debug-overlay hover.
function bboxArea(g) {
  if (!g || g.type !== "Polygon" || !g.coordinates || !g.coordinates[0]) return Infinity;
  let minX = Infinity, minY = Infinity, maxX = -Infinity, maxY = -Infinity;
  for (const [x, y] of g.coordinates[0]) {
    if (x < minX) minX = x; if (x > maxX) maxX = x;
    if (y < minY) minY = y; if (y > maxY) maxY = y;
  }
  return (maxX - minX) * (maxY - minY);
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
    this._cellStatus = new Map();       // name -> "queued"|"loading"|"ready"|"failed" (lazy wasm baker load)
    this._cellError = new Map();        // name -> error message, for cells that failed to parse
    this._cellUsage = { bytes: 0, count: 0 }; // raw cell store disk usage (refreshed on popup open / load)
    this._archive = new Map();          // name -> {blob, entry, meta} from opened zips
    this._selected = new Set();         // names ticked for import / NOAA download
    this._dlRegions = new Set();        // installed NOAA region numbers (from GET /api/charts)
    this._regionArchives = [];          // [{num,file,bounds}] — one pmtiles per installed region
    this._districts = [];               // hosted per-district archives (charts-index.json)
    this._importedArchives = [];        // in-memory imported/uploaded archives (Blob/File), re-added on every coverage rebuild so a too-large-to-persist import isn't lost when a later provision resets the bands
    this._userBake = null;              // {cells:[…], bounds:[w,s,e,n]} of the map-selected charts-user.pmtiles, or null
    this._showCellBounds = localStorage.getItem("cp-cell-bounds") !== "0"; // coverage outlines on/off (default on)
    this._debugCells = localStorage.getItem("cp-debug-cells") === "1"; // debug: all cell footprints coloured by load state
    this._bandsOff = new Set();          // dev: usage bands excluded from the realtime baker (in-memory; default all on)
    this._renderSel = new Set();         // dev (debug mode): hand-picked cells — when non-empty, ONLY these render, at any zoom
    this._tileDbgOn = false;             // dev: tile-debugger plugin overlay (lifecycle + delivery integrity + bake logging)
    this._tileDbg = null;                // dev: the TileDebugger IControl instance (lazily imported)
    this._inspectMode = false;          // feature-inspect mode (toggled from the statusbar)
    this._inspectLocked = false;        // a feature is pinned (click-to-lock) — hover stops updating
    this._inspectLastKey = "";          // last rendered hover/lock key, to skip redundant re-renders
    this._inspectFeats = [];            // the stack of features under the cursor (cycler steps through them)
    this._inspectIdx = 0;               // which one of the stack is shown
    this._inspectMulti = false;         // true after a SHIFT+drag area capture (list all, no cycler)
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
    // S-52 §10.2: Display Base is the minimum safe-navigation set and can never
    // be deselected. Force it on regardless of any (stale) persisted value.
    this._mariner.displayBase = true;
    this._scheme = localStorage.getItem(LS_SCHEME) || "day";
    if (!SCHEMES.includes(this._scheme)) this._scheme = "day"; // drop a retired scheme (e.g. bright)
    this._agreed = localStorage.getItem(LS_AGREE) === "1"; // NOAA ENC agreement accepted
    this._activeDistrict = null;        // Coast Guard district (cg) currently previewed on the map, or null
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

    // Share-restore: opening <origin>/#share reconstructs someone else's exact
    // view — pull the snapshot, install its cells (downloaded via the server if
    // not already stored), and adopt its camera in place of the local last view.
    let shareView = null;
    if (isShareUrl()) {
      shareView = await this._loadSharedView().catch((e) => { console.warn("[share] restore failed:", e); return null; });
    }

    const plotter = document.createElement("chart-plotter");
    const view = shareView || loadJSON(LS_VIEW, null); // resume the last view → load in-region
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
    plotter.addEventListener("cell-status", (e) => this._onCellStatus(e.detail));
  }

  // Per-cell load status, streamed from the wasm baker as cells are parsed one at
  // a time. name → "queued" | "loading" | "ready" | "failed".
  _onCellStatus({ name, status, info }) {
    if (!this._cellStatus) this._cellStatus = new Map();
    this._cellStatus.set(name, status);
    if (status === "loading") console.log(`[charts] loading ${name}…`);
    else if (status === "ready") { this._cellError.delete(name); console.log(`[charts] ${name} ready${info && info.ms != null ? ` (${info.ms}ms)` : ""}`); }
    else if (status === "failed") { this._cellError.set(name, (info && info.error) || "parse failed"); console.warn(`[charts] ${name} failed:`, info && info.error); }
    this._updateBakeStatus();
    this._renderCellStatusPopup();
    if (this._debugCells) {
      this._refreshInstalledBounds(); // recolour the debug footprints by new state
      if (status === "ready") { this._pulseCell(name); this._refreshCoverage(); } // ping + real coverage
    }
  }

  // Pull the loaded cells' real M_COVR coverage from the baker and draw it on the
  // debug overlay (debounced — many cells can go ready in a burst).
  _refreshCoverage() {
    clearTimeout(this._covTimer);
    this._covTimer = setTimeout(() => {
      if (!this._plotter || !this._plotter.realtimeCoverage) return;
      this._plotter.realtimeCoverage().then((fc) => {
        const s = this._map && this._map.getSource("inst-cov");
        if (s) s.setData(fc || { type: "FeatureCollection", features: [] });
      }).catch(() => {});
    }, 250);
  }

  // Pulse a cell's border once (an expanding, fading ring) when it becomes ready.
  _pulseCell(name) {
    const c = this._byName.get(name);
    if (!c || !Array.isArray(c.bb) || c.bb.length !== 4) return;
    if (!this._pulses) this._pulses = new Map();
    this._pulses.set(name, performance.now());
    if (!this._pulseRAF) this._pulseTick();
  }
  _pulseTick() {
    const src = this._map && this._map.getSource("inst-pulse");
    if (!src || !this._pulses) { this._pulseRAF = 0; return; }
    const now = performance.now(), DUR = 650, feats = [];
    for (const [name, start] of this._pulses) {
      const t = (now - start) / DUR;
      if (t >= 1) { this._pulses.delete(name); continue; }
      const c = this._byName.get(name);
      if (!c || !Array.isArray(c.bb) || c.bb.length !== 4) { this._pulses.delete(name); continue; }
      const [w, s, e, n] = c.bb;
      feats.push({ type: "Feature", properties: { prog: t }, geometry: { type: "Polygon", coordinates: [[[w, s], [e, s], [e, n], [w, n], [w, s]]] } });
    }
    src.setData({ type: "FeatureCollection", features: feats });
    this._pulseRAF = this._pulses.size ? requestAnimationFrame(() => this._pulseTick()) : 0;
  }

  // Show/hide the debug cell-loading overlay (all installed footprints at every
  // zoom, coloured by lazy-load state).
  _setDebugCells(on) {
    this._debugCells = on;
    localStorage.setItem("cp-debug-cells", on ? "1" : "0");
    const map = this._map; if (!map) return;
    const vis = on ? "visible" : "none";
    for (const id of ["inst-dbg-fill", "inst-dbg-line", "inst-dbg-hover-fill", "inst-dbg-hover-label", "inst-cov-fill", "inst-cov-line"]) {
      if (map.getLayer(id)) map.setLayoutProperty(id, "visibility", vis);
    }
    if (on) { this._refreshInstalledBounds(); this._refreshCoverage(); } // status + real coverage
    else { this._clearDebugHover(); this._hideContextMenu(); }
    // Solo render applies only in debug mode, so toggling it with a render set
    // active flips between "only selected" and normal — re-bake to reflect that.
    if (this._renderSel.size) this._refreshRealtime(false);
  }

  // Pick the cell under `point` for the debug overlay. Prefers REAL data coverage
  // (M_COVR, the inst-cov layer) over the raw catalog bbox, so the result matches
  // where the cell actually has data — and only considers cells VISIBLE at the
  // current zoom (band not gated out), so hover/select track what's rendering. The
  // bbox layer is a fallback for cells not yet loaded (no M_COVR drawn). Smallest
  // area wins on overlap (the most detailed cell under the cursor). Returns
  // { name, geometry, status } or null.
  _debugCellsAt(point) {
    const map = this._map;
    if (!map) return [];
    const z = map.getZoom();
    const visible = (name) => {
      const c = this._byName.get(name);
      const band = c && typeof c.s === "number" ? bandForScale(c.s) : "harbor";
      return z >= (BAND_MINZOOM[band] || 0);
    };
    const byName = new Map(); // name -> { name, geometry, area, cov }
    const scan = (layer, key, isCov) => {
      if (!map.getLayer(layer)) return;
      for (const f of map.queryRenderedFeatures(point, { layers: [layer] })) {
        const name = f.properties[key];
        if (!name || !visible(name)) continue;
        const a = bboxArea(f.geometry);
        const prev = byName.get(name);
        // Prefer the real M_COVR geometry; among the same kind keep the smallest.
        if (!prev || (isCov && !prev.cov) || (isCov === prev.cov && a < prev.area)) {
          byName.set(name, { name, geometry: f.geometry, area: a, cov: isCov });
        }
      }
    };
    scan("inst-cov-fill", "cell", true);  // real M_COVR coverage (loaded cells)
    scan("inst-dbg-fill", "name", false); // bbox footprints (incl. not-yet-loaded)
    // Coverage-backed cells first, then by ascending area (most detailed first).
    return [...byName.values()].sort((a, b) => (b.cov ? 1 : 0) - (a.cov ? 1 : 0) || a.area - b.area);
  }
  _debugCellAt(point) { return this._debugCellsAt(point)[0] || null; }

  // Debug overlay hover: report EVERY cell under the cursor and, per cell, exactly
  // what it's drawing here — grouped from the actually-rendered chart features
  // (queryRenderedFeatures, which returns only what's painted) by their `cell`
  // attribute. Cells whose M_COVR covers the point but draw nothing (gated /
  // suppressed) are listed as "(not drawing)" — the key signal when debugging a
  // cut-off or empty patch. Highlights all candidate footprints + a HUD readout.
  _onDebugHover(e) {
    if (!this._debugCells || !this._map) return;
    const map = this._map;

    // What's actually painted here, grouped by source cell → { layer: {cls:count} }.
    const chartLayers = map.getStyle().layers.filter((l) => l.source && isChartSource(l.source)).map((l) => l.id);
    const drawn = new Map(); // cell -> Map(sourceLayer -> Map(class -> count))
    if (chartLayers.length) {
      for (const f of map.queryRenderedFeatures(e.point, { layers: chartLayers })) {
        const cell = (f.properties && f.properties.cell) || "?";
        const lyr = f.sourceLayer || "?";
        const cls = (f.properties && f.properties.class) || "";
        const byLyr = drawn.get(cell) || new Map();
        const byCls = byLyr.get(lyr) || new Map();
        byCls.set(cls, (byCls.get(cls) || 0) + 1);
        byLyr.set(lyr, byCls); drawn.set(cell, byLyr);
      }
    }

    // Cells whose footprint/coverage is under the cursor (visible at this zoom),
    // smallest-first — so cells that cover here but draw nothing still get listed.
    const covering = this._debugCellsAt(e.point); // [{name, geometry}], smallest area first
    const order = covering.map((c) => c.name);
    for (const cell of drawn.keys()) if (!order.includes(cell)) order.push(cell);

    if (!order.length) { this._clearDebugHover(); this._hideDebugHud(); return; }

    // Highlight all candidate footprints on the map.
    const src = map.getSource("inst-dbg-hover");
    if (src) src.setData({ type: "FeatureCollection", features: covering.map((c) => ({ type: "Feature", properties: { name: c.name, status: this._cellStatus.get(c.name) || "" }, geometry: c.geometry })) });

    // HUD readout: per cell, the layers/classes it's drawing (or "not drawing").
    // Cap the list (most-detailed cells sort first) so it can't grow unwieldy.
    const CAP = 10;
    const shown = order.slice(0, CAP);
    const more = order.length - shown.length;
    const rows = shown.map((cell) => {
      const c = this._byName.get(cell);
      const band = c && typeof c.s === "number" ? bandForScale(c.s) : "harbor";
      const dot = `<span class="dh-dot" style="background:${BAND_COLOR[band] || "#888"}"></span>`;
      const byLyr = drawn.get(cell);
      let body;
      if (!byLyr) {
        body = `<div class="dh-draw dh-none">not drawing here</div>`;
      } else {
        body = [...byLyr.entries()].map(([lyr, byCls]) => {
          const parts = [...byCls.entries()].map(([cls, n]) => (cls ? `${cls}×${n}` : `×${n}`)).join(", ");
          return `<div class="dh-draw"><span class="dh-lyr">${esc(lyr)}</span>: ${esc(parts)}</div>`;
        }).join("");
      }
      return `<div class="dh-cell"><div class="dh-name">${dot}${esc(cell)}</div>${body}</div>`;
    }).join("") + (more > 0 ? `<div class="dh-cell dh-none">+${more} more cell${more > 1 ? "s" : ""}</div>` : "");
    this._showDebugHud(e, rows);
  }

  _showDebugHud(e, html) {
    const hud = this.shadowRoot.getElementById("debug-hud");
    if (!hud) return;
    hud.innerHTML = html;
    hud.hidden = false;
    const rect = this.getBoundingClientRect();
    const oe = e.originalEvent || e;
    let x = (oe.clientX - rect.left) + 14, y = (oe.clientY - rect.top) + 14;
    // Keep it on-screen.
    const w = hud.offsetWidth, h = hud.offsetHeight;
    if (x + w > rect.width) x = (oe.clientX - rect.left) - w - 14;
    if (y + h > rect.height) y = Math.max(4, rect.height - h - 4);
    hud.style.left = x + "px";
    hud.style.top = y + "px";
  }
  _hideDebugHud() {
    const hud = this.shadowRoot.getElementById("debug-hud");
    if (hud && !hud.hidden) hud.hidden = true;
  }

  _clearDebugHover() {
    const src = this._map && this._map.getSource("inst-dbg-hover");
    if (src) src.setData({ type: "FeatureCollection", features: [] });
    this._hideDebugHud();
  }

  // Right-click a cell box in the debug overlay → context menu to pick which cells
  // render. Once any cell is picked, ONLY the picked set renders (at any zoom), so
  // you can isolate exactly the cells you care about. Picks the smallest box on
  // overlap (the most detailed cell under the cursor).
  _onDebugContextMenu(e) {
    if (!this._debugCells || !this._map) return;
    const hit = this._debugCellAt(e.point); // M_COVR-aware, visible-at-this-zoom only
    if (!hit) return;
    if (e.preventDefault) e.preventDefault();
    if (e.originalEvent && e.originalEvent.preventDefault) e.originalEvent.preventDefault();
    const name = hit.name;
    const picked = this._renderSel.has(name);
    const items = [
      { label: `Render only ${name}`, onClick: () => this._setRenderSel([name]) },
      picked
        ? { label: `Remove ${name} from render set`, onClick: () => this._toggleRenderCell(name) }
        : { label: `Add ${name} to render set`, onClick: () => this._toggleRenderCell(name) },
    ];
    if (this._renderSel.size) items.push({ label: `Clear render set (show all)`, onClick: () => this._setRenderSel([]) });
    const rect = this.getBoundingClientRect();
    const oe = e.originalEvent;
    this._showContextMenu(oe.clientX - rect.left, oe.clientY - rect.top, items);
  }

  // Add/remove one cell from the debug render selection, then re-bake in place.
  async _toggleRenderCell(name) {
    if (this._renderSel.has(name)) this._renderSel.delete(name); else this._renderSel.add(name);
    await this._applyRenderSel();
  }

  // Replace the whole render selection (e.g. "render only this", or clear with []).
  async _setRenderSel(names) {
    this._renderSel = new Set(names);
    await this._applyRenderSel();
  }

  async _applyRenderSel() {
    console.log(`[debug] render set: ${this._renderSel.size ? [...this._renderSel].join(", ") : "(all)"}`);
    if (this._section === "inspect" && this._drawerOpen()) this._renderDevPanel();
    await this._refreshRealtime(false); // keep the camera put
  }

  // A tiny shadow-DOM context menu. items: [{ label, onClick }]. Positioned at
  // host-relative (x,y); dismissed on any click or map move.
  _showContextMenu(x, y, items) {
    const m = this.shadowRoot.getElementById("ctx-menu");
    if (!m) return;
    m.innerHTML = "";
    for (const it of items) {
      const btn = document.createElement("button");
      btn.className = "ctx-item";
      btn.textContent = it.label;
      btn.onclick = (ev) => { ev.stopPropagation(); this._hideContextMenu(); it.onClick(); };
      m.appendChild(btn);
    }
    m.style.left = x + "px";
    m.style.top = y + "px";
    m.hidden = false;
  }

  _hideContextMenu() {
    const m = this.shadowRoot.getElementById("ctx-menu");
    if (m && !m.hidden) m.hidden = true;
  }

  // Remove every cell that failed to parse from the browser store (and reset the
  // baker registry without them) — the "clear them out" affordance.
  async _removeFailedCells() {
    const failed = [...this._cellStatus.entries()].filter(([, v]) => v === "failed").map(([k]) => k);
    if (!failed.length) return;
    for (const name of failed) {
      try { await this._store.remove(name); } catch (e) { console.warn("[store] remove", name, e); }
      this._installed.delete(name);
      this._cellStatus.delete(name);
      this._cellError.delete(name);
    }
    console.log("[charts] removed failed cells:", failed);
    await this._refreshRealtime();
    this.renderCharts();
    this._renderCellStatusPopup();
  }

  // Live wasm tile-baking count (tiles currently baking in the worker, 0 = idle).
  _onBakeActivity(inflight) {
    this._bakeInflight = inflight;
    this._updateBakeStatus();
  }

  // The installed set / registry changed: mark every installed cell "queued"
  // (not yet parsed — cells lazy-load on demand) and prune orphans.
  _resetCellStatus() {
    const next = new Map();
    for (const n of this._installed) next.set(n, "queued");
    this._cellStatus = next;
    this._updateBakeStatus();
    this._renderCellStatusPopup();
  }

  _countStatus(s) { let n = 0; for (const v of this._cellStatus.values()) if (v === s) n++; return n; }

  // Centered statusbar indicator. Priority: cells currently parsing (lazy) → live
  // tile baking → a resting "N charts" affordance (kept visible so it stays
  // clickable for the per-cell popup). Hidden only when nothing is installed.
  _updateBakeStatus() {
    const root = this.shadowRoot; if (!root) return;
    this._updateLoadBar(); // subtle top-bar cue (independent of the pill below)
    const el = root.getElementById("bake-status"); if (!el) return;
    const txt = el.querySelector(".sb-bake-txt");
    const loading = this._countStatus("loading"), failed = this._countStatus("failed");
    let busy = false, label = "";
    if (loading > 0) {
      busy = true; label = loading > 1 ? `Loading ${loading} charts…` : "Loading chart…";
    } else if (this._bakeInflight > 0) {
      busy = true; label = this._bakeInflight > 1 ? `Generating ${this._bakeInflight} tiles…` : "Generating tile…";
    } else if (this._installed.size > 0) {
      // Cells parse lazily (only when an area is baked), so a "loaded" count would
      // read 0/N after a cached refresh even with charts on screen. Show the chart
      // count instead — the per-cell popup still has the detailed parse status.
      const n = this._installed.size;
      label = `${n} chart${n !== 1 ? "s" : ""}` + (failed ? ` · ${failed} failed` : "");
    } else {
      el.hidden = true; el.classList.remove("busy"); return;
    }
    txt.textContent = label;
    el.classList.toggle("busy", busy);
    el.classList.toggle("has-fail", failed > 0);
    el.hidden = false;
  }

  // Subtle "loading more while data is shown" cue: a thin indeterminate bar at the
  // top of the map. Shown only when there's bake/parse activity AND data is already
  // on screen (cold start keeps the louder pill/welcome). Debounced — delay-in
  // ~150ms so instant bakes don't flash it; linger ~300ms after idle.
  _updateLoadBar() {
    const want = (this._bakeInflight > 0 || this._countStatus("loading") > 0)
      && (this._hasArchive || this._countStatus("ready") > 0);
    if (want === this._loadBarWant) return; // only act on a change of desired state
    this._loadBarWant = want;
    clearTimeout(this._loadBarTimer);
    this._loadBarTimer = setTimeout(() => {
      const bar = this.shadowRoot && this.shadowRoot.getElementById("load-bar");
      if (bar) bar.classList.toggle("on", want);
    }, want ? 100 : 300);
  }

  _toggleCellStatusPopup() {
    this._cellPopOpen = !this._cellPopOpen;
    // Keep cache stats (tiles/memory/disk) live while the popup is open.
    clearInterval(this._cellPopTimer);
    if (this._cellPopOpen) {
      this._cellPopTimer = setInterval(() => this._renderCellStatusPopup(), 600);
      this._refreshCellUsage(); // raw cell disk usage (changes only on download/remove)
    }
    this._renderCellStatusPopup();
  }

  // Refresh the cached raw-cell store disk usage, then re-render the popup.
  _refreshCellUsage() {
    this._store.usage().then((u) => { this._cellUsage = u; if (this._cellPopOpen) this._renderCellStatusPopup(); }).catch(() => {});
  }

  _fmtBytes(n) {
    if (!n) return "0 B";
    const u = ["B", "KB", "MB", "GB"]; let i = 0;
    while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
    return `${n.toFixed(n < 10 && i > 0 ? 1 : 0)} ${u[i]}`;
  }

  // Popup listing every installed cell with its band colour + load status. Opened
  // by clicking the centered statusbar indicator.
  _renderCellStatusPopup() {
    const root = this.shadowRoot; if (!root) return;
    const pop = root.getElementById("cell-status-pop"); if (!pop) return;
    if (!this._cellPopOpen) { pop.hidden = true; return; }
    const names = [...new Set([...this._installed, ...this._cellStatus.keys()])].sort();
    const loaded = this._countStatus("ready"), loading = this._countStatus("loading"), failed = this._countStatus("failed");
    const parts = [`${this._installed.size} installed`, `${loaded} loaded`];
    if (loading) parts.push(`${loading} loading`);
    if (failed) parts.push(`${failed} failed`);
    const head = parts.join(" · ");
    const STAT = { loading: ["loading…", "csp-loading"], ready: ["loaded", "csp-ready"], failed: ["failed", "csp-failed"] };
    // Only list cells that are actually loaded (or loading / failed) — with lazy
    // loading most installed cells are idle, which is just noise. Each row shows
    // the chart name first, then the cell code.
    const rows = names.map((n) => {
      const st = this._cellStatus.get(n) || (this._installed.has(n) ? "queued" : "ready");
      if (!STAT[st]) return ""; // skip idle/queued cells
      const c = this._byName.get(n);
      const band = c && typeof c.s === "number" ? bandForScale(c.s) : "harbor";
      const [lbl, cls] = STAT[st];
      const err = st === "failed" ? this._cellError.get(n) : "";
      const title = (c && c.l) || n;
      return `<li class="csp-row${err ? " is-fail" : ""}"><span class="csp-dot" style="background:${BAND_COLOR[band]}"></span>`
        + `<span class="csp-name">`
        + `<span class="csp-title" title="${esc(title)}">${esc(title)}</span>`
        + `<span class="csp-code">${esc(n)}</span>`
        + (err ? `<span class="csp-err">${esc(err)}</span>` : "")
        + `</span><span class="csp-stat ${cls}">${lbl}</span></li>`;
    }).join("");
    const emptyMsg = this._installed.size
      ? "No charts loaded yet — pan or zoom to chart coverage"
      : "No charts installed";
    const clearBtn = failed
      ? `<button id="csp-clear-failed" class="csp-clear" type="button">Remove ${failed} failed</button>` : "";
    const u = this._plotter && this._plotter.realtimeStats && this._plotter.realtimeStats();
    const cu = this._cellUsage || { bytes: 0, count: 0 };
    const statsHtml = u ? `<div class="csp-stats">`
      + `<div><span>Cells</span><b>${loaded}/${this._installed.size} loaded</b></div>`
      + `<div><span>Cell data</span><b>${this._fmtBytes(cu.bytes)} on disk</b></div>`
      + `<div><span>Tiles</span><b>${u.memTiles} mem · ${u.diskTiles} disk</b></div>`
      + `<div><span>Tile memory</span><b>${this._fmtBytes(u.memBytes)} / ${this._fmtBytes(u.memCap)}</b></div>`
      + `<div><span>Tile disk</span><b>${this._fmtBytes(u.diskBytes)} / ${this._fmtBytes(u.diskCap)}</b></div>`
      + `<div><span>Cache</span><b>${u.l1Hit + u.l2Hit} hit · ${u.miss} baked</b></div>`
      + `</div>` : "";
    // The popup re-renders on a timer (live stats); preserve the cell list's
    // scroll position so the user can actually scroll it without it snapping back.
    const prevScroll = pop.querySelector(".csp-list")?.scrollTop || 0;
    pop.innerHTML = `<div class="csp-head"><span>${esc(head)}</span>${clearBtn}</div>`
      + statsHtml
      + `<ul class="csp-list">${rows || `<li class="csp-empty">${esc(emptyMsg)}</li>`}</ul>`;
    const list = pop.querySelector(".csp-list"); if (list && prevScroll) list.scrollTop = prevScroll;
    pop.querySelector("#csp-clear-failed")?.addEventListener("click", (e) => { e.stopPropagation(); this._removeFailedCells(); });
    pop.hidden = false;
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
    await this.restoreArchive();
    // 100%-wasm path: register stored cells for lazy, on-demand parsing (nothing
    // parses until a viewed tile needs it). Plain reload → keep persisted tiles.
    this._resetCellStatus();
    try {
      const rt = await this._plotter.loadStoreCells(this._realtimeCellMeta(), false);
      this._refreshInstalledBounds();
      if (rt && rt.ok && rt.names && rt.names.length) { this._hasArchive = true; this.updateEmptyState(); }
    } catch (e) { console.warn("[realtime] loadStoreCells", e); }
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
      // Dev tile inspector: refresh in-view band counts; a prior coverage measure
      // is now stale (different viewport), so drop its hole overlay.
      if (this._section === "inspect" && this._drawerOpen()) { this._clearDevHoles(); this._renderDevPanel(); }
    });

    // Live zoom/scale/band readout (left of the statusbar).
    this._updateHud();
    map.on("move", () => this._updateHud());

    // Debug overlay: tint + name the cell footprint under the cursor (only while
    // the overlay is on); clear when the pointer leaves the map. Right-click a cell
    // to force-bake it at the current zoom regardless of its band min-zoom.
    map.on("mousemove", (e) => this._onDebugHover(e));
    map.on("mouseout", () => this._clearDebugHover());
    map.on("contextmenu", (e) => this._onDebugContextMenu(e));
    map.on("movestart", () => this._hideContextMenu());

    // Close any pinned band-pill popup when clicking elsewhere (pill/cell clicks
    // stopPropagation, so this only fires for clicks outside them). Also tuck the
    // on-map search back into its tab when clicking away while it's empty (map
    // clicks bubble out of the renderer's shadow root to here).
    this.shadowRoot.addEventListener("click", (e) => {
      this._hideContextMenu(); // any click dismisses the debug context menu (item handlers run first)
      this.shadowRoot.querySelectorAll(".sb-band-wrap.open").forEach((w) => w.classList.remove("open"));
      // Close the per-cell status popup when clicking outside it (the indicator
      // button's own handler stopPropagation's, so this only fires for outside clicks).
      if (this._cellPopOpen) {
        const pop = this.shadowRoot.getElementById("cell-status-pop");
        if (pop && !e.composedPath().some((n) => n === pop)) { this._cellPopOpen = false; clearInterval(this._cellPopTimer); this._renderCellStatusPopup(); }
      }
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
      `<span class="hud-band">${BAND_LABEL[band]}</span><span class="hud-sep">·</span>` +
      `<span class="hud-scale">1:${fmtScale(scaleDenom(z, c.lat))}</span><span class="hud-sep">·</span>` +
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
      this.shadowRoot.querySelectorAll(".pk-btn").forEach((b) => { b.disabled = busy; });
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
    this.shadowRoot.getElementById("dtitle").textContent = "Chart library";
    el.innerHTML = `
      ${this._renderPackSearch()}
      ${this._renderPacks()}
      <details class="import-more">
        <summary>Import from a file</summary>
        <div id="drop" class="drop">Drop a <code>.zip</code>, <code>.000</code> or <code>.pmtiles</code> here, or<br><button id="pick" class="btn" style="margin-top:6px">Choose files…</button></div>
        <input id="file" type="file" accept=".zip,.000,.pmtiles" multiple hidden>
        <div id="import-log" class="muted"></div>
        <div id="archive-list"></div>
      </details>
      ${this._renderDataFreshness()}`;
    this._wirePackSearch();
    this._wirePacks();
    this._wireImport();
  }

  // -- chart packs (Coast Guard districts) ---------------------------------
  // The cells in a district pack (every catalog cell tagged with that `cg`).
  _districtCellNames(cg) {
    const out = [];
    for (const c of this._catalog) if (c.cg === cg) out.push(c.n);
    return out;
  }
  // Counts + download size for a pack's card: how many of its cells exist in the
  // catalog, how many are already on this device, and the total download bytes.
  _districtStat(cg) {
    let total = 0, have = 0, bytes = 0;
    for (const c of this._catalog) {
      if (c.cg !== cg) continue;
      total++;
      if (typeof c.zs === "number") bytes += c.zs;
      if (this._installed.has(c.n)) have++;
    }
    return { total, have, bytes };
  }
  // NOAA's per-district bundle URL (NNCGD_ENCs.zip), derived from any catalog
  // cell's per-cell zip URL so it tracks the catalog's host. cg is zero-padded.
  _districtZipUrl(cg) {
    const any = this._catalog.find((c) => c.z);
    const dir = any ? any.z.replace(/[^/]+$/, "") : "https://www.charts.noaa.gov/ENCs/";
    return dir + String(cg).padStart(2, "0") + "CGD_ENCs.zip";
  }

  // The pack grid: one card per Coast Guard district. Tap a card to preview its
  // coverage on the map; the button downloads the whole pack (or uninstalls it).
  _renderPacks() {
    const busy = this._taskRunning();
    const cards = DISTRICTS.map((d) => {
      const { total, have, bytes } = this._districtStat(d.cg);
      if (!total) return "";
      const mb = `${Math.round(bytes / 1e6)} MB`;
      const full = have >= total;
      const partial = have > 0 && !full;
      const active = this._activeDistrict === d.cg ? " active" : "";
      let badge = "", actions;
      if (full) {
        badge = `<span class="pk-badge ok">✓ Installed</span>`;
        actions = `<button class="pk-btn ghost" data-uninstall="${d.cg}"${busy ? " disabled" : ""}>Uninstall</button>`;
      } else if (partial) {
        badge = `<span class="pk-badge part">${have} of ${total}</span>`;
        actions = `<button class="pk-btn" data-download="${d.cg}"${busy ? " disabled" : ""}>Get the rest</button>` +
          `<button class="pk-btn ghost" data-uninstall="${d.cg}"${busy ? " disabled" : ""}>Remove</button>`;
      } else {
        actions = `<button class="pk-btn" data-download="${d.cg}"${busy ? " disabled" : ""}>⬇ Download · ~${mb}</button>`;
      }
      return `<div class="pack-card${active}${full ? " installed" : ""}" data-pack="${d.cg}" role="button" tabindex="0" title="Show the ${esc(d.region)} pack on the map">
        <div class="pk-top"><span class="pk-region">${esc(d.region)}</span>${badge}</div>
        <div class="pk-name">${esc(d.name)}</div>
        <div class="pk-blurb">${esc(d.blurb)}</div>
        <div class="pk-meta">${total.toLocaleString()} charts · ~${mb}</div>
        <div class="pk-actions">${actions}</div>
      </div>`;
    }).join("");
    return `<div class="pack-intro">Charts come in packs grouped by U.S. Coast Guard district. Tap a pack to preview it on the map, then download. Looking for one harbor? Search above to find its pack.</div>
      <div class="pack-grid">${cards}</div>`;
  }
  _wirePacks() {
    const r = this.shadowRoot;
    r.querySelectorAll(".pack-card[data-pack]").forEach((card) => {
      const cg = +card.dataset.pack;
      card.addEventListener("click", () => this._showDistrictOnMap(cg));
      card.addEventListener("keydown", (e) => {
        if (e.key === "Enter" || e.key === " ") { e.preventDefault(); this._showDistrictOnMap(cg); }
      });
    });
    r.querySelectorAll(".pk-btn[data-download]").forEach((b) =>
      b.addEventListener("click", (e) => { e.stopPropagation(); this._downloadPack(+b.dataset.download); }));
    r.querySelectorAll(".pk-btn[data-uninstall]").forEach((b) =>
      b.addEventListener("click", (e) => { e.stopPropagation(); this._uninstallPack(+b.dataset.uninstall); }));
  }

  // Find-a-chart search: type a port or cell name, get the matching charts and
  // which pack each lives in, with a one-tap button to grab that whole pack
  // (individual cells aren't downloadable on their own — packs are the unit).
  _renderPackSearch() {
    const q = this._cellQuery || "";
    return `<input id="pack-search" class="pack-search" type="search" placeholder="Find a chart or port…" autocomplete="off" spellcheck="false" value="${esc(q)}">
      <div id="pack-results" class="pack-results"></div>`;
  }
  _wirePackSearch() {
    const i = this.shadowRoot.getElementById("pack-search");
    if (!i) return;
    i.oninput = () => { this._cellQuery = i.value; this._renderPackResultsInto(); };
    this._renderPackResultsInto();
  }
  _renderPackResultsInto() {
    const box = this.shadowRoot.getElementById("pack-results");
    if (!box) return;
    const q = (this._cellQuery || "").trim().toLowerCase();
    if (q.length < 2) { box.innerHTML = ""; return; }
    const hits = [];
    for (const c of this._catalog) {
      if (c.n.toLowerCase().includes(q) || (c.l && c.l.toLowerCase().includes(q))) hits.push(c);
      if (hits.length >= 40) break;
    }
    if (!hits.length) { box.innerHTML = `<div class="pkr-empty">No charts match “${esc(this._cellQuery)}”.</div>`; return; }
    box.innerHTML = hits.map((c) => {
      const d = DISTRICTS.find((x) => x.cg === c.cg);
      const have = this._installed.has(c.n);
      const where = d ? `${d.region} · ${d.name}` : (c.cg ? `District ${c.cg}` : "no district");
      const action = !d ? "" : have
        ? `<span class="pkr-have">✓ installed</span>`
        : `<button class="pkr-dl pk-btn" data-download="${c.cg}">Download ${esc(d.region)} pack</button>`;
      return `<div class="pkr-row" data-pack="${d ? c.cg : ""}" title="${d ? `Show the ${esc(d.region)} pack on the map` : ""}">
        <div class="pkr-info">
          <span class="pkr-title">${esc(c.l || c.n)}</span>
          <span class="pkr-sub">${esc(c.n)} · in the <b>${esc(where)}</b> pack</span>
        </div>${action}</div>`;
    }).join("");
    box.querySelectorAll(".pkr-row[data-pack]").forEach((row) => {
      const cg = +row.dataset.pack;
      if (cg) row.addEventListener("click", () => this._showDistrictOnMap(cg));
    });
    box.querySelectorAll(".pkr-dl[data-download]").forEach((b) =>
      b.addEventListener("click", (e) => { e.stopPropagation(); this._downloadPack(+b.dataset.download); }));
  }

  // Preview a pack on the map: highlight that district's cells and frame to them.
  // Re-renders the grid so the tapped card reads as active.
  _showDistrictOnMap(cg) {
    this._activeDistrict = cg;
    if (!this._chartsMode) this._enterChartsMode();
    this._setCellOverlay(true);
    this._refreshCellSel();
    this._frameCells(this._districtCellNames(cg));
    if (this._section === "charts" && this._drawerOpen()) this.renderCharts();
  }

  // NOAA data freshness footer (req: show when the catalog data is from).
  _renderDataFreshness() {
    if (!this._catalogDate) return "";
    const total = this._catalog.length.toLocaleString();
    return `<div class="data-fresh">NOAA chart data current as of <b>${fmtIssue(this._catalogDate)}</b> · ${total} charts available</div>`;
  }

  // Tear down an armed/active box-drag (no-op now the drag-a-box selector is
  // gone, but still called on Charts-mode exit / section switch).
  _cancelAreaSelect() { if (this._areaCleanup) this._areaCleanup(); }

  // Download a whole pack: fetch NOAA's per-district bundle once (one zip holding
  // exactly this district's cells), extract the cells we don't already have into
  // the browser store, then re-bake in-browser. If the district bundle can't be
  // opened it falls back to All_ENCs.zip, then to per-cell fetches.
  async _downloadPack(cg) {
    if (this._taskRunning()) return;
    const d = DISTRICTS.find((x) => x.cg === cg);
    const label = d ? `${d.region} pack` : `District ${cg}`;
    const want = this._districtCellNames(cg).filter((n) => !this._installed.has(n));
    if (!want.length) return;
    if (!await this._ensureAgreed()) return; // NOAA ENC User Agreement gate

    this._activeDistrict = cg;
    this._task = { kind: "download", status: "running" };
    if (this._section === "charts" && this._drawerOpen()) this.renderCharts();

    let failed = want.length;
    try {
      failed = await this._bulkExtractZip(this._districtZipUrl(cg), want, label);
    } catch (e) {
      console.warn(`[pack] ${label} bundle failed — trying All_ENCs.zip:`, e.message);
      try { failed = await this._bulkExtract(want); }
      catch (e2) { console.warn("[pack] All_ENCs.zip failed — per-cell:", e2.message); failed = await this._downloadPerCell(want); }
    }

    this._setProgress({ label: "Baking tiles…", sub: failed ? `${failed} chart${failed !== 1 ? "s" : ""} failed` : "", frac: 1 });
    await this._refreshRealtime();
    this._task = null;
    this._setProgress(null);
    this.updateEmptyState();
    this._refreshCellSel();
    if (this._section === "charts" && this._drawerOpen()) this.renderCharts();
    if (failed) console.warn(`[pack] ${label}: ${failed} of ${want.length} chart(s) failed`);
  }

  // Uninstall a pack: drop its cells from the browser store and re-bake.
  async _uninstallPack(cg) {
    if (this._taskRunning()) return;
    const names = this._districtCellNames(cg).filter((n) => this._installed.has(n));
    if (!names.length) return;
    this._activeDistrict = cg;
    this._task = { kind: "download", status: "running" };
    this._setProgress({ label: "Removing charts…", sub: `${names.length} chart${names.length !== 1 ? "s" : ""}`, frac: null });
    if (this._section === "charts" && this._drawerOpen()) this.renderCharts();
    for (const n of names) {
      try { await this._store.remove(n); this._installed.delete(n); } catch (e) { console.warn("[store] remove", n, e); }
    }
    await this._refreshRealtime();
    this._task = null;
    this._setProgress(null);
    this.updateEmptyState();
    this._refreshCellSel();
    if (this._section === "charts" && this._drawerOpen()) this.renderCharts();
  }

  // Per-cell fallback: fetch each cell's own zip through the byte proxy. Returns
  // the count that failed.
  async _downloadPerCell(names) {
    let done = 0, failed = 0;
    for (const name of names) {
      this._setProgress({ label: `Downloading ${names.length} chart${names.length !== 1 ? "s" : ""}`, sub: `${name} · ${done + 1} of ${names.length}`, frac: names.length ? done / names.length : null });
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
    return failed;
  }

  // The URL of NOAA's All_ENCs.zip, derived from any catalog cell's per-cell zip
  // URL (same directory) so it tracks the catalog's host.
  _allEncsUrl() {
    const any = this._catalog.find((c) => c.z);
    return any ? any.z.replace(/[^/]+$/, "All_ENCs.zip") : "https://www.charts.noaa.gov/ENCs/All_ENCs.zip";
  }

  // Bulk path: download a NOAA zip bundle ONCE (streamed through the dumb byte
  // proxy into a disk-backed Blob), then slice + inflate the wanted base cells
  // out of it locally — no network per cell. `label` names the bundle for the
  // progress UI. Returns the count that failed; throws if the archive can't be
  // opened (caller falls back).
  async _bulkExtractZip(url, names, label) {
    const proxy = "api/proxy?url=" + encodeURIComponent(url);
    const resp = await fetch(proxy);
    if (!resp.ok) throw new Error("proxy HTTP " + resp.status);

    // Stream the download into a Blob, reporting progress. Piping through a
    // counting TransformStream into Response().blob() lets the browser back the
    // Blob on disk (no multi-hundred-MB JS heap spike) while we track bytes.
    const total = +resp.headers.get("Content-Length") || 0;
    let blob;
    if (resp.body) {
      let recv = 0;
      const tap = new TransformStream({
        transform: (chunk, ctrl) => {
          recv += chunk.length;
          this._setProgress({ label: `Downloading ${label}…`, sub: total ? `${this._fmtBytes(recv)} / ${this._fmtBytes(total)} · ${names.length} charts` : this._fmtBytes(recv), frac: total ? recv / total : null });
          ctrl.enqueue(chunk);
        },
      });
      blob = await new Response(resp.body.pipeThrough(tap)).blob();
    } else {
      blob = await resp.blob();
    }

    this._setProgress({ label: `Reading ${label}…`, sub: `${names.length} charts — extracting`, frac: null });
    const byName = new Map(cellEntries(await readCentralDirectory(blob)).map((c) => [c.name, c]));

    let done = 0, failed = 0;
    for (const name of names) {
      this._setProgress({ label: `Extracting ${names.length} charts`, sub: `${name} · ${done + 1} of ${names.length}`, frac: names.length ? done / names.length : null });
      const rec = byName.get(name);
      if (!rec || !rec.base) { console.warn("[bulk]", name, "not in", label); failed++; done++; continue; }
      try {
        await this._store.put(name, await extractEntry(blob, rec.base));
        this._installed.add(name);
      } catch (e) { console.warn("[bulk]", name, e.message); failed++; }
      done++;
    }
    console.log(`[bulk] ${label}: extracted ${names.length - failed}/${names.length}`);
    return failed;
  }

  // All_ENCs.zip fallback for when a per-district bundle isn't available.
  async _bulkExtract(names) { return this._bulkExtractZip(this._allEncsUrl(), names, "All_ENCs.zip"); }

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
    // Dev tile inspector: sampled coverage-hole points (Inspect → Coverage → Measure).
    map.addSource("tile-holes", { type: "geojson", data: empty });
    map.addLayer({ id: "tile-holes", type: "circle", source: "tile-holes", paint: { "circle-radius": 4, "circle-color": "#ff1744", "circle-opacity": 0.75, "circle-stroke-color": "#fff", "circle-stroke-width": 1 } });
    // Installed-cell coverage: at zooms BELOW a cell's native band (where its
    // chart detail isn't baked yet) draw its footprint + name, so when zoomed out
    // you can tell WHAT coverage you have, not just that you have some. One set of
    // layers per band, auto-hidden at the band's native min zoom (maxzoom) — where
    // the real chart takes over.
    map.addSource("inst-bounds", { type: "geojson", data: empty });
    const boundsVis = this._showCellBounds ? "visible" : "none";
    for (const band of ["general", "coastal", "approach", "harbor", "berthing"]) {
      const mz = BAND_MINZOOM[band];
      const f = ["==", ["get", "band"], band];
      map.addLayer({ id: `inst-fill-${band}`, type: "fill", source: "inst-bounds", maxzoom: mz, filter: f, layout: { visibility: boundsVis }, paint: { "fill-color": BAND_COLOR[band], "fill-opacity": 0.06 } });
      map.addLayer({ id: `inst-line-${band}`, type: "line", source: "inst-bounds", maxzoom: mz, filter: f, layout: { visibility: boundsVis }, paint: { "line-color": BAND_COLOR[band], "line-width": 1.1, "line-opacity": 0.85 } });
      // (cell-name labels removed — the per-box text was too noisy; the outline alone marks coverage)
    }
    // Debug overlay (Settings → "Debug cell loading"): every installed cell's
    // footprint at ALL zooms, coloured by lazy-load state — green=loaded,
    // amber=loading, red=failed, grey=not loaded. Lets you see which cells are
    // loaded vs missing and whether their (catalog-bbox) load region is where you
    // expect, so "some cells of the same band don't render" is diagnosable.
    const dbgColor = ["match", ["get", "status"], "ready", "#2e9b57", "loading", "#d9892b", "failed", "#cf3b3b", "#9aa7b4"];
    const dbgVis = this._debugCells ? "visible" : "none";
    // Default debug overlay is just coloured outlines (green=loaded, amber=loading,
    // red=failed, grey=not loaded). inst-dbg-fill is an invisible hit-target so a
    // hover anywhere inside a box is detectable; the tint + name show only for the
    // hovered cell, via the one-feature inst-dbg-hover source (see _onDebugHover) —
    // so we never lay out a label per cell across the whole library.
    map.addLayer({ id: "inst-dbg-fill", type: "fill", source: "inst-bounds", layout: { visibility: dbgVis }, paint: { "fill-color": dbgColor, "fill-opacity": 0 } });
    map.addLayer({ id: "inst-dbg-line", type: "line", source: "inst-bounds", layout: { visibility: dbgVis }, paint: { "line-color": dbgColor, "line-width": 1.6 } });
    map.addSource("inst-dbg-hover", { type: "geojson", data: empty });
    map.addLayer({ id: "inst-dbg-hover-fill", type: "fill", source: "inst-dbg-hover", layout: { visibility: dbgVis }, paint: { "fill-color": dbgColor, "fill-opacity": 0.25 } });
    map.addLayer({ id: "inst-dbg-hover-label", type: "symbol", source: "inst-dbg-hover", layout: { visibility: dbgVis, "text-field": ["get", "name"], "text-font": ["Noto Sans Regular"], "text-size": 12, "text-allow-overlap": true }, paint: { "text-color": dbgColor, "text-halo-color": "rgba(255,255,255,0.95)", "text-halo-width": 1.4 } });
    // One-shot "ready" pulse: the whole cell flashes green (fill) with a bright
    // border, ramping up fast then fading — obvious across the entire footprint.
    map.addSource("inst-pulse", { type: "geojson", data: empty });
    map.addLayer({ id: "inst-pulse-fill", type: "fill", source: "inst-pulse", paint: {
      "fill-color": "#2e9b57",
      "fill-opacity": ["interpolate", ["linear"], ["get", "prog"], 0, 0.0, 0.15, 0.45, 1, 0.0],
    } });
    map.addLayer({ id: "inst-pulse-line", type: "line", source: "inst-pulse", paint: {
      "line-color": "#2e9b57",
      "line-width": ["interpolate", ["linear"], ["get", "prog"], 0, 1, 0.15, 5, 1, 1],
      "line-opacity": ["interpolate", ["linear"], ["get", "prog"], 0, 0.2, 0.15, 1, 1, 0],
    } });
    // Real M_COVR data-coverage of LOADED cells (vs the bbox rectangles above):
    // chart data should fill these exactly. Nodata INSIDE a coverage polygon =
    // a bug; nodata OUTSIDE every polygon (but inside a bbox) = an unloaded cell;
    // nodata outside all = a genuine gap. Drawn as a green hatched fill + outline.
    map.addSource("inst-cov", { type: "geojson", data: empty });
    map.addLayer({ id: "inst-cov-fill", type: "fill", source: "inst-cov", layout: { visibility: dbgVis }, paint: { "fill-color": "#1f9d55", "fill-opacity": 0.16 } });
    map.addLayer({ id: "inst-cov-line", type: "line", source: "inst-cov", layout: { visibility: dbgVis }, paint: { "line-color": "#136b3a", "line-width": 1.4, "line-dasharray": [3, 2] } });
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
      // Charts selection map: a tap previews the pack (Coast Guard district) of
      // the cell under it (drags pan, and MapLibre only emits "click" for
      // non-pan gestures).
      if (this._chartsMode) { this._pickDistrictAt(e.point.x, e.point.y); return; }
      if (!this._inspectMode || this._areaCleanup || e.originalEvent.shiftKey) return; // shift = box
      if (this._inspectLocked) { this._inspectLocked = false; this._inspectAt(e.point, false); return; }
      this._inspectAt(e.point, true); // lock onto whatever's here
    });
  }

  // Arm/disarm feature-inspect interaction (crosshair, hover/click capture,
  // SHIFT+drag area select). The Inspect drawer panel's visibility is owned by
  // the drawer (toggleSection/setDrawerOpen) — this only manages the map-side
  // interaction + the panel's content. Mutually exclusive with the box selector.
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
    if (on) this._inspectHint("Hover to inspect · click to lock · SHIFT+drag to capture an area.");
    else this._closeInspect();
    // The rail button reflects dev-panel-open (setDrawerOpen); inspect on/off is
    // shown by the in-panel "Inspect features" button — refresh it.
    if (this._section === "inspect" && this._drawerOpen()) this._renderDevPanel();
  }

  // Inspect the chart features at a canvas point. `lock` freezes the panel on a
  // hit (the click-to-lock action); a no-hit lock is a no-op (so clicking empty
  // chart doesn't clear a useful hover), a no-hit hover shows the hint.
  _inspectAt(point, lock) {
    const map = this._map;
    const feats = map.queryRenderedFeatures(point).filter((f) => isChartSource(f.source));
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
    // The realtime path has one "chart" source; the legacy pmtiles path had a
    // "chart-<band>" source per band. Use whichever the live style has.
    const styleSrc = map.getStyle().sources || {};
    const sources = Object.keys(styleSrc).filter(isChartSource);
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
    if (feats.length > 1) {
      this.shadowRoot.getElementById("ins-prev").onclick = () => this._inspectStep(-1);
      this.shadowRoot.getElementById("ins-next").onclick = () => this._inspectStep(1);
    }
  }

  // --- Share my view -------------------------------------------------------
  // Publish the current scene (camera + installed cells) to the server so anyone
  // — including a headless browser used for debugging — can open <origin>/#share
  // and see exactly what's on screen here. Catalog (NOAA) cells travel as a name
  // + download url the reconstructing browser pulls through the server; cells the
  // server can't fetch itself (hand-imported, non-NOAA) are uploaded byte-for-byte
  // into its cache first. The share URL is copied to the clipboard.
  async _shareView(btn) {
    const m = this._map;
    if (!m) return;
    if (btn) flashBtn(btn, "…");
    try {
      const c = m.getCenter();
      const view = {
        center: [+c.lng.toFixed(6), +c.lat.toFixed(6)],
        zoom: +m.getZoom().toFixed(3),
        bearing: +m.getBearing().toFixed(1),
        pitch: +m.getPitch().toFixed(1),
      };
      const cells = [];
      for (const n of this._installed) {
        const cat = this._byName.get(n);
        const z = cat && cat.z ? cat.z : "";
        cells.push(z ? { n, z } : { n });
        if (z) continue; // server can fetch catalog cells itself via api/cell?url=
        // No NOAA url → the server can't get this cell; push its bytes into the cache.
        try {
          const bytes = await this._store.getBytes(n);
          const r = await fetch("api/cell/" + encodeURIComponent(n), { method: "PUT", headers: { "content-type": "application/octet-stream" }, body: bytes });
          if (!r.ok) throw new Error("HTTP " + r.status);
        } catch (e) { console.warn("[share] upload", n, e); }
      }
      const snap = { v: 1, when: new Date().toISOString(), view, cells };
      const resp = await fetch("api/share", { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify(snap) });
      if (!resp.ok) throw new Error("HTTP " + resp.status);
      const url = location.origin + location.pathname + "#share";
      const ok = await copyText(url);
      console.log("[share] view published:", url, `(${cells.length} cell${cells.length !== 1 ? "s" : ""})`);
      if (btn) flashBtn(btn, ok ? "✓ copied" : "✓");
    } catch (e) {
      console.warn("[share] publish failed:", e);
      if (btn) flashBtn(btn, "✗");
    }
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

  // --- Dev panel (tile/band inspector) -------------------------------------
  // Render the Developer block at the foot of the Inspect panel: share/debug
  // actions, per-band baker toggles, and a tile-coverage inspector. Re-rendered
  // on band changes, coverage measurements, and (for the in-view counts) map move.
  _renderDevPanel() {
    const el = this.shadowRoot.getElementById("dev-tools");
    if (!el) return;
    const z = this._map ? this._map.getZoom() : null;
    const stats = this._devInViewBands();
    const bandRows = DEV_BANDS.map((b) => {
      const st = stats[b] || { overlap: 0, loaded: 0 };
      const off = this._bandsOff.has(b);
      const mz = BAND_MINZOOM[b] || 0;
      const gated = z != null && z < mz && st.overlap > 0;
      const tag = st.overlap
        ? `${st.loaded}/${st.overlap} loaded${gated ? ` · gated &lt;z${mz}` : ""}`
        : "none in view";
      return `<label class="dev-band${off ? " off" : ""}${gated ? " gated" : ""}">
        <input type="checkbox" data-band="${b}"${off ? "" : " checked"}>
        <span class="bn">${b}</span><span class="bs">${tag}</span></label>`;
    }).join("");
    const cov = this._devCoverage;
    let covLine = "not measured for this view";
    if (cov) {
      if (cov.holePct === 0) covLine = "✓ full coverage — no holes";
      else if (cov.gated.length) covLine = `${cov.holePct}% holes · filled by ${cov.gated.slice(0, 6).join(", ")}${cov.gated.length > 6 ? `, +${cov.gated.length - 6}` : ""} (zoom in to load)`;
      else covLine = `${cov.holePct}% holes · no installed cell covers them`;
    }
    const inspecting = this._inspectMode;
    const dbgOn = this._debugCells;
    el.innerHTML = `
      <section class="dev-sec">
        <div class="dev-h">Share view</div>
        <button id="dev-share" class="btn wide">Copy share link</button>
        <p class="dev-note">Publishes the current camera + installed charts and copies a link that reproduces exactly this.</p>
      </section>

      <section class="dev-sec">
        <div class="dev-h">Feature inspector</div>
        <button id="dev-inspect" class="btn wide${inspecting ? " on" : ""}">${inspecting ? "● Inspecting — click to stop" : "Inspect features"}</button>
        <button id="dev-feat" class="btn wide"${inspecting ? "" : " disabled"} title="Copy the selected feature's source/geometry/attributes to clipboard + server">Copy feature debug</button>
        <p class="dev-note">Hover a feature to highlight it · click to lock · SHIFT+drag to capture an area.</p>
      </section>

      <section class="dev-sec">
        <div class="dev-h">Cell overlay</div>
        <label class="dev-row"><span>Show debug cell overlay</span><input id="dev-debug-cells" type="checkbox"${dbgOn ? " checked" : ""}></label>
        <p class="dev-note">Coloured cell outlines (green=loaded, amber=loading, red=failed, grey=idle). Hover a box to name it; right-click to pick which cells render.</p>
        ${this._renderSel.size ? `<div class="dev-row"><span class="dev-cov">Render set (${this._renderSel.size}): ${[...this._renderSel].slice(0, 4).join(", ")}${this._renderSel.size > 4 ? `, +${this._renderSel.size - 4}` : ""}</span><button id="dev-unforce" class="btn sm">Clear</button></div>` : ""}
      </section>

      <section class="dev-sec">
        <div class="dev-h">Tiles</div>
        <label class="dev-row"><span>Tile debugger</span><input id="dev-tiledbg" type="checkbox"${this._tileDbgOn ? " checked" : ""}></label>
        <p class="dev-note">Per-tile overlay + bake logging. Each chart tile's box shows its lifecycle (with z/x/y): green=rendering, <b style="color:#e53935">red=delivered-but-empty</b>, amber=loading; click a box for detail, and the panel lists any delivered-but-parsed-to-zero tiles. Also logs each bake to the console (<code>eligible=… empty=…</code>).</p>
        <div class="dev-row"><span>Flush baked tile cache</span><button id="dev-flush" class="btn sm">Flush + re-bake</button></div>
        <p class="dev-note">Clear the in-browser baked-tile cache (memory + IndexedDB) and drop MapLibre's loaded tiles, forcing every visible tile to re-bake.</p>
      </section>

      <section class="dev-sec">
        <div class="dev-h">Bands <span class="bz">${z != null ? `z ${z.toFixed(2)} · tiles z${Math.floor(z)}` : ""}</span></div>
        <p class="dev-note">Uncheck a band to drop its cells from the baker and re-bake — isolate which band paints what. Counts are cells overlapping this view.</p>
        <div class="dev-bands">${bandRows}</div>
      </section>

      <section class="dev-sec">
        <div class="dev-h">Coverage</div>
        <div class="dev-row"><span class="dev-cov">${covLine}</span><button id="dev-measure" class="btn sm">Measure</button></div>
        <p class="dev-note">Grid-samples the view for holes (no chart data) and paints them red.</p>
      </section>`;
    const q = (id) => el.querySelector("#" + id);
    q("dev-share").onclick = (e) => this._shareView(e.currentTarget);
    q("dev-inspect").onclick = () => this._setInspectMode(!this._inspectMode);
    const feat = q("dev-feat"); if (!feat.disabled) feat.onclick = (e) => this._copyInspectDebug(e.currentTarget);
    const dbg = q("dev-debug-cells"); dbg.onchange = () => this._setDebugCells(dbg.checked);
    q("dev-measure").onclick = (e) => this._measureCoverage(e.currentTarget);
    const unforce = q("dev-unforce");
    if (unforce) unforce.onclick = () => this._setRenderSel([]);
    const tiledbg = q("dev-tiledbg");
    if (tiledbg) tiledbg.onchange = () => this._setTileDebugger(tiledbg.checked);
    const flush = q("dev-flush");
    if (flush) flush.onclick = (e) => this._flushTiles(e.currentTarget);
    el.querySelectorAll("[data-band]").forEach((cb) => (cb.onchange = () => this._setBandOff(cb.dataset.band, !cb.checked)));
  }

  // Toggle the tile-debugger plugin (per-tile lifecycle + delivery-integrity
  // overlay). Lazily imported on first use — it's a dev-only diagnostic. Reuses
  // one IControl instance; add/remove fully sets up / tears it down each time.
  // Also turns on MapLibre's built-in tile-boundary grid (so the chart tiles are
  // outlined in the map itself, not just the plugin's own boxes) and the wasm
  // baker's per-tile bake logging (`eligible=… empty=…` → console).
  async _setTileDebugger(on) {
    this._tileDbgOn = !!on;
    const map = this._map; if (!map) return;
    map.showTileBoundaries = this._tileDbgOn;
    if (this._plotter && this._plotter.setTileDiag) this._plotter.setTileDiag(this._tileDbgOn);
    if (on) {
      if (!this._tileDbg) {
        const mod = await import("./tile-debugger.mjs");
        this._tileDbg = new mod.TileDebugger({ source: "chart", inspectURL: (z, x, y) => this._tileInspectURL(z, x, y) });
      }
      if (this._tileDbgOn) map.addControl(this._tileDbg, "top-right"); // re-check: user may have toggled off during the import
    } else if (this._tileDbg) {
      try { map.removeControl(this._tileDbg); } catch (e) { /* not added */ }
    }
  }

  // Build the hittable URL for the tile-debugger's "inspect this tile" button:
  // GET /api/tile/{z}/{x}/{y}?cells=… — the server re-bakes that z/x/y from the
  // cached cells (same baker) and returns the raw MVT, so it can be pulled with
  // curl / fed to an MVT inspector. We scope ?cells to the installed cells whose
  // footprint overlaps the tile (cells with no known footprint are always passed),
  // so the server bakes the same set the app loaded. Absolute URL (clipboard-ready).
  _tileInspectURL(z, x, y) {
    const [W, S, E, N] = this._tileBBox(z, x, y);
    const names = [];
    for (const name of this._installed) {
      const c = this._byName.get(name);
      if (!c || !Array.isArray(c.bb) || c.bb.length !== 4) { names.push(name); continue; } // unknown footprint → always include
      const [w, s, e, n] = c.bb;
      if (e < W || w > E || n < S || s > N) continue; // no overlap
      names.push(name);
    }
    const q = names.length ? `?cells=${encodeURIComponent(names.join(","))}` : "";
    return new URL(`api/tile/${z}/${x}/${y}${q}`, location.href).href;
  }

  // Web-Mercator tile z/x/y → lon/lat bbox [W,S,E,N].
  _tileBBox(z, x, y) {
    const n = 2 ** z;
    const lon = (xx) => (xx / n) * 360 - 180;
    const lat = (yy) => { const r = Math.PI - (2 * Math.PI * yy) / n; return (180 / Math.PI) * Math.atan(0.5 * (Math.exp(r) - Math.exp(-r))); };
    return [lon(x), lat(y + 1), lon(x + 1), lat(y)];
  }

  // Flush the baked-tile caches and force every visible tile to re-bake: clears
  // the in-browser cp TileCache (memory + IndexedDB), drops MapLibre's already-
  // loaded tiles for the chart source, and bumps the tile version so it re-fetches.
  // A debugging probe — if a stale/empty tile fills in after this, it was cached;
  // if it stays blank, the emptiness is being regenerated (not a cache artifact).
  async _flushTiles(btn) {
    const pl = this._plotter, map = this._map;
    if (!pl || !map) return;
    if (btn) flashBtn(btn, "…");
    try {
      if (pl._rtCache && pl._rtCache.clear) await pl._rtCache.clear(); // cp two-layer cache
      // Drop MapLibre's loaded tiles for the chart source. MapLibre 4 exposed
      // `map.style.sourceCaches[id]`; v5 renamed/minified that and splits a source
      // into paint + symbol caches — so duck-type every cache-shaped dict on style
      // keyed by "chart" (clearTiles/update survive minification).
      for (const sc of this._chartSourceCaches()) {
        if (sc.clearTiles) { sc.clearTiles(); if (sc.update) sc.update(map.transform); }
      }
      if (pl.refresh) pl.refresh(); // bump version → re-request + re-bake
      console.log("[flush] tile caches cleared, re-baking visible tiles");
      if (btn) flashBtn(btn, "✓");
    } catch (e) { console.warn("[flush]", e); if (btn) flashBtn(btn, "✗"); }
  }

  // Every MapLibre SourceCache backing the "chart" source. v4 had one at
  // map.style.sourceCaches["chart"]; v5 renamed that property and can hold a
  // separate paint + symbol cache, so we duck-type rather than hardcode the name.
  _chartSourceCaches() {
    const style = this._map && this._map.style;
    if (!style) return [];
    const out = [];
    const consider = (c) => { if (c && (c._tiles || typeof c.clearTiles === "function") && !out.includes(c)) out.push(c); };
    const fromDict = (d) => {
      if (!d || typeof d !== "object") return;
      if (d instanceof Map) { consider(d.get("chart")); return; }
      if (Object.prototype.hasOwnProperty.call(d, "chart")) consider(d["chart"]);
    };
    fromDict(style.sourceCaches);
    for (const k of Object.keys(style)) { const v = style[k]; if (v && typeof v === "object") fromDict(v); }
    return out;
  }

  // Cells overlapping the current viewport, tallied per usage band:
  // { band: { overlap, loaded } }. Drives the dev band rows.
  _devInViewBands() {
    const out = {};
    if (!this._map) return out;
    const b = this._map.getBounds();
    const W = b.getWest(), S = b.getSouth(), E = b.getEast(), N = b.getNorth();
    for (const name of this._installed) {
      const c = this._byName.get(name);
      if (!c || !Array.isArray(c.bb) || c.bb.length !== 4) continue;
      const [w, s, e, n] = c.bb;
      if (e < W || w > E || n < S || s > N) continue; // no overlap
      const band = typeof c.s === "number" ? bandForScale(c.s) : "overview";
      const o = (out[band] ||= { overlap: 0, loaded: 0 });
      o.overlap++;
      if (this._cellStatus.get(name) === "ready") o.loaded++;
    }
    return out;
  }

  // Exclude/include a usage band from the realtime baker, then re-bake the view
  // (loadStoreCells with clearCache re-registers + drops stale tiles).
  async _setBandOff(band, off) {
    if (off) this._bandsOff.add(band); else this._bandsOff.delete(band);
    this._clearDevHoles();
    this._renderDevPanel();
    await this._refreshRealtime(false); // re-bake in place — don't move the camera
  }

  // Sample a grid over the viewport: a point where no chart feature renders is a
  // coverage hole. Paints the holes on the map and reports the hole %, plus which
  // installed cells cover the holes but are gated out at the current zoom (so you
  // can see "zoom in past z9 to load these coastal cells"). One-shot (button).
  async _measureCoverage(btn) {
    const map = this._map;
    if (!map) return;
    if (btn) flashBtn(btn, "…");
    const layers = map.getStyle().layers.filter((l) => l.source && isChartSource(l.source)).map((l) => l.id);
    const W = map.getCanvas().clientWidth, H = map.getCanvas().clientHeight;
    const cols = 32, rows = 20;
    const holes = [];
    let covered = 0, total = 0;
    for (let j = 0; j <= rows; j++) for (let i = 0; i <= cols; i++) {
      const x = (i / cols) * W, y = (j / rows) * H;
      total++;
      if (map.queryRenderedFeatures([x, y], { layers }).length) covered++;
      else holes.push(map.unproject([x, y]));
    }
    const holePct = total ? +(100 * (total - covered) / total).toFixed(1) : 0;
    const z = map.getZoom();
    const gated = new Set();
    for (const ll of holes) {
      for (const name of this._installed) {
        const c = this._byName.get(name);
        if (!c || !Array.isArray(c.bb)) continue;
        const [w, s, e, n] = c.bb;
        if (ll.lng < w || ll.lng > e || ll.lat < s || ll.lat > n) continue;
        const band = typeof c.s === "number" ? bandForScale(c.s) : "overview";
        if (!this._bandsOff.has(band) && z < (BAND_MINZOOM[band] || 0)) gated.add(name);
      }
    }
    this._devCoverage = { holePct, gated: [...gated].sort() };
    const src = map.getSource("tile-holes");
    if (src) src.setData({ type: "FeatureCollection", features: holes.map((ll) => ({ type: "Feature", properties: {}, geometry: { type: "Point", coordinates: [ll.lng, ll.lat] } })) });
    this._renderDevPanel();
    if (btn) flashBtn(btn, holePct ? `${holePct}%` : "✓");
  }

  _clearDevHoles() {
    this._devCoverage = null;
    const src = this._map && this._map.getSource("tile-holes");
    if (src) src.setData({ type: "FeatureCollection", features: [] });
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
    // Render diagnostics: complex linestyles are tessellated in the baker — the
    // dash "on" segments land in the complex-lines layer and the embedded marks
    // in point_symbols. Report how many of each are in view. (Pins "lines blank /
    // symbols missing" without a screenshot.)
    let render = null;
    if (m && m.getStyle) {
      const cnt = (ids) => { try { return m.queryRenderedFeatures({ layers: ids }).length; } catch { return -1; } };
      render = {
        complexLineSegmentsInView: cnt(["complex-lines"]),
        pointSymbolsInView: cnt(["point_symbols"]),
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
    const body = this.shadowRoot.getElementById("inspect-body");
    if (body) body.innerHTML = ""; // drop any feature cards so the dev tools sit at the top
    if (this._map) {
      this._map.getCanvas().style.cursor = "";
      const src = this._map.getSource("inspect");
      if (src) src.setData({ type: "FeatureCollection", features: [] });
      this._clearInspectFocus();
    }
  }

  // Build a GeoJSON FeatureCollection of cell footprints. `cells` is an iterable
  // of catalog entries; when `mark` is set, cells of the previewed pack (the
  // active Coast Guard district) are tagged sel=1 so they highlight on the map.
  _cellsFC(cells, mark) {
    const f = [];
    const active = this._activeDistrict;
    for (const c of cells) {
      const b = c.bb;
      if (!Array.isArray(b) || b.length !== 4) continue;
      f.push({
        type: "Feature",
        properties: { sel: mark && active && c.cg === active ? 1 : 0 },
        geometry: { type: "Polygon", coordinates: [[[b[0], b[1]], [b[2], b[1]], [b[2], b[3]], [b[0], b[3]], [b[0], b[1]]]] },
      });
    }
    return { type: "FeatureCollection", features: f };
  }

  // Show/hide the all-cells selection overlay (and refresh the previewed-pack fill).
  _setCellOverlay(on) {
    const map = this._map;
    if (!map) return;
    const vis = on ? "visible" : "none";
    for (const id of ["selcells-line", "selcells-fill", "selcells-sel-line"]) {
      if (map.getLayer(id)) map.setLayoutProperty(id, "visibility", vis);
    }
    const s = map.getSource("selcells");
    if (s) s.setData(on ? this._cellsFC(this._catalog, true) : { type: "FeatureCollection", features: [] });
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
    const isChartLayer = (l) => isChartSource(l.source) || l.id === "nodata";
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
    this._activeDistrict = null; // clear the previewed-pack highlight so it doesn't persist across open/close
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

  // Click a cell on the selection map to preview its pack. Picks the finest
  // (largest-scale) cell whose footprint contains the point and shows that
  // cell's Coast Guard district. A plain MapLibre "click" only fires when the
  // gesture wasn't a pan, so dragging still pans the map.
  _pickDistrictAt(px, py) {
    if (!this._map) return;
    const ll = this._map.unproject([px, py]);
    let best = null;
    for (const c of this._catalog) {
      const b = c.bb;
      if (!Array.isArray(b) || b.length !== 4) continue;
      if (ll.lng >= b[0] && ll.lng <= b[2] && ll.lat >= b[1] && ll.lat <= b[3]) {
        if (!best || (c.s || 0) < (best.s || 0)) best = c; // finest = smallest scale denom
      }
    }
    if (best && best.cg) this._showDistrictOnMap(best.cg);
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

  // A not-downloaded in-view cell was tapped in the coverage HUD: open the Charts
  // selector and preview the pack (Coast Guard district) that contains the cell,
  // ready to download.
  _downloadCellRegion(name) {
    const c = this._byName.get(name);
    this.openCharts();
    if (c && c.cg) this._showDistrictOnMap(c.cg);
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
  // frame=false re-bakes in place without moving the camera — used by the dev
  // band toggles, which only change what renders in the current view.
  async _refreshRealtime(frame = true) {
    if (!this._plotter) return;
    this._resetCellStatus();
    try {
      // The installed set changed → drop persisted tiles so removed cells vanish.
      const rt = await this._plotter.loadStoreCells(this._realtimeCellMeta(), true);
      this._refreshInstalledBounds();
      if (rt && rt.ok && rt.names && rt.names.length) {
        this._hasArchive = true;
        this.updateEmptyState();
        if (frame) this._frameCells(rt.names);
      }
    } catch (e) { console.warn("[realtime] refresh", e); }
    this._refreshCellUsage();
  }

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

  _realtimeCellMeta() {
    // Debug solo: when cells are hand-picked (debug mode), render ONLY those — at
    // any zoom — and exclude everything else. The render selection overrides the
    // band gate and the per-band toggles entirely.
    const solo = this._debugCells && this._renderSel.size > 0;
    const meta = new Map();
    for (const name of this._installed) {
      const c = this._byName.get(name);
      let bb = c && Array.isArray(c.bb) && c.bb.length === 4 ? c.bb : null;
      // No catalog footprint → bound to the union of the cell's region bboxes so
      // it only loads near its actual area (not globally). Null only as a last
      // resort (no regions either), which setCellRegistry treats as world-wide.
      if (!bb && c && Array.isArray(c.rg) && c.rg.length) {
        const rm = this._regionBBoxes();
        let u = null;
        for (const r of c.rg) {
          const rb = rm.get(r); if (!rb) continue;
          if (!u) u = rb.slice();
          else { u[0] = Math.min(u[0], rb[0]); u[1] = Math.min(u[1], rb[1]); u[2] = Math.max(u[2], rb[2]); u[3] = Math.max(u[3], rb[3]); }
        }
        if (u) bb = u;
      }
      const band = c && typeof c.s === "number" ? bandForScale(c.s) : "overview";
      // 999 is a sentinel min-zoom the baker's `z < minzoom` gate never satisfies,
      // so the cell never bakes; 0 means "bake at any zoom".
      let minzoom;
      if (solo) minzoom = this._renderSel.has(name) ? 0 : 999;
      else minzoom = this._bandsOff.has(band) ? 999 : (BAND_MINZOOM[band] || 0);
      meta.set(name, { bb, minzoom });
    }
    return meta;
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
        properties: { name, band: bandForScale(c.s), status: this._cellStatus.get(name) || "queued" },
        geometry: { type: "Polygon", coordinates: [[[w, s], [e, s], [e, n], [w, n], [w, s]]] },
      });
    }
    src.setData({ type: "FeatureCollection", features: feats });
  }

  // Show/hide the installed-cell coverage outlines (the inst-* overlay layers).
  _setCellBoundsVisible(on) {
    this._showCellBounds = on;
    localStorage.setItem("cp-cell-bounds", on ? "1" : "0");
    const map = this._map; if (!map) return;
    const vis = on ? "visible" : "none";
    for (const band of ["general", "coastal", "approach", "harbor", "berthing"]) {
      for (const pre of ["inst-fill-", "inst-line-", "inst-label-"]) {
        if (map.getLayer(pre + band)) map.setLayoutProperty(pre + band, "visibility", vis);
      }
    }
  }

  toggleSelect(name) {
    if (this._selected.has(name)) this._selected.delete(name);
    else this._selected.add(name);
    this.refreshBoxes();
    this.renderArchiveList();
  }

  // -- search: catalog (places/charts) + loaded chart feature data ---------
  doSearch(q) {
    const el = this.shadowRoot.getElementById("search-results");
    if (!el) return;
    const needle = q.trim().toLowerCase();
    if (needle.length < 2) { el.hidden = true; el.innerHTML = ""; this._searchHits = []; return; }
    // 1) Catalog cells (chart titles / numbers). Coarser charts first — "Chesapeake"
    // should land on the overview, not an arbitrary harbour inset.
    const cells = [];
    for (const c of this._catalog) {
      if (!Array.isArray(c.bb) || c.bb.length !== 4) continue;
      if ((c.l || "").toLowerCase().includes(needle) || c.n.toLowerCase().includes(needle)) cells.push(c);
    }
    cells.sort((a, b) => (b.s || 0) - (a.s || 0));
    // 2) Every loaded chart feature, matched across ALL of its attribute data.
    const feats = this._searchFeatures(needle);
    const hits = [...cells.slice(0, 5).map((c) => ({ type: "cell", c })), ...feats.slice(0, 8)];
    this._searchHits = hits;
    el.innerHTML = hits.length
      ? hits.map((h, i) => {
          const sel = i === 0 ? " sel" : "";
          if (h.type === "cell") return `<div class="sr-item${sel}" data-i="${i}"><div class="t">${esc(h.c.l || h.c.n)}</div><div class="s">Chart · ${esc(h.c.n)} · 1:${(h.c.s || 0).toLocaleString()}</div></div>`;
          return `<div class="sr-item${sel}" data-i="${i}"><div class="t">${esc(h.label)}</div><div class="s">${esc(h.sub)}</div></div>`;
        }).join("")
      : `<div class="sr-item"><span class="muted">No matches in view</span></div>`;
    el.hidden = false;
    el.querySelectorAll(".sr-item[data-i]").forEach((d) => (d.onmousedown = (e) => { e.preventDefault(); this.gotoSearchHit(+d.dataset.i); }));
  }

  // Search the loaded chart vector tiles across EVERY attribute value (name, class,
  // readable type, and any other string field). Limited to currently-loaded tiles
  // (roughly the area you've viewed), since that's all the data the client holds.
  _searchFeatures(needle) {
    const map = this._map; if (!map) return [];
    let sources;
    try { sources = Object.keys(map.getStyle().sources || {}).filter(isChartSource); } catch { return []; }
    const layers = ["point_symbols", "soundings", "areas", "area_patterns", "lines", "complex_lines", "text"];
    const seen = new Set(), out = [];
    for (const src of sources) {
      for (const layer of layers) {
        let feats; try { feats = map.querySourceFeatures(src, { sourceLayer: layer }); } catch { continue; }
        for (const f of feats) {
          const p = f.properties || {};
          const objnam = p.objnam || "", cls = p.class || "";
          const typeName = S57_CLASS[cls] || INSPECT_LAYER_LABEL[layer] || cls || layer;
          let match = objnam.toLowerCase().includes(needle) || cls.toLowerCase().includes(needle) || typeName.toLowerCase().includes(needle);
          if (!match) for (const k in p) { const v = p[k]; if (typeof v === "string" && v.toLowerCase().includes(needle)) { match = true; break; } }
          if (!match) continue;
          const co = firstCoord(f.geometry); if (!co) continue;
          const key = cls + "|" + objnam + "|" + co[0].toFixed(3) + "," + co[1].toFixed(3);
          if (seen.has(key)) continue; seen.add(key);
          out.push({ type: "feat", label: objnam || typeName, sub: objnam ? typeName : (p.cell ? `▦ ${p.cell}` : typeName), lng: co[0], lat: co[1] });
          if (out.length >= 60) { out.sort((a, b) => a.label.localeCompare(b.label)); return out; }
        }
      }
    }
    out.sort((a, b) => a.label.localeCompare(b.label)); // named features read better alphabetised
    return out;
  }

  gotoSearchHit(i) {
    const h = (this._searchHits || [])[i];
    if (!h || !this._map) return;
    if (h.type === "feat") this._map.flyTo({ center: [h.lng, h.lat], zoom: Math.max(this._map.getZoom(), 14), duration: 800 });
    else { const c = h.c; this._map.fitBounds([[c.bb[0], c.bb[1]], [c.bb[2], c.bb[3]]], { padding: 80, maxZoom: 13, duration: 800 }); }
    const r = this.shadowRoot;
    const el = r.getElementById("search-results"); if (el) el.hidden = true;
    // Keep the query (and selected highlight) so reopening search returns you to
    // the same results — the input is persisted, not cleared.
    const si = r.getElementById("search-input"); if (si) si.blur();
    r.getElementById("search").hidden = true;
    r.getElementById("search-tab").classList.remove("on");
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
    this._syncSchemeUI();
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
    r.querySelectorAll("#scheme-seg button").forEach((b) => b.classList.toggle("sel", b.dataset.scheme === this._scheme));
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
          /* One layout for every width: a full-bleed map over a slim bottom tab
             bar; panels rise as a sheet from the bar (full-screen on a phone, a
             contained per-section dialog on desktop). */
          --botbar-h:calc(54px + env(safe-area-inset-bottom,0px));
          --ui-bg:#fafafa; --ui-surface:#fff; --ui-surface-2:#eef1f4; --ui-text:#2a2f35; --ui-text-dim:#7a828b; --ui-text-faint:#9aa0a8; --ui-border:#e2e2e2; --ui-border-2:#ededed; --ui-border-strong:#cfcfcf; --ui-hover:#f0f3f6; --ui-accent:#1565c0; --ui-accent-hover:#1257a8; --ui-accent-text:#fff; --ui-shadow:rgba(0,0,0,.2); }
        :host([data-scheme="dusk"]) {
          --ui-bg:#20262b; --ui-surface:#2a3137; --ui-surface-2:#333b42; --ui-text:#cdd6dc; --ui-text-dim:#9aa6ae; --ui-text-faint:#7d8990; --ui-border:#3a434a; --ui-border-2:#333b42; --ui-border-strong:#4a555d; --ui-hover:#353f47; --ui-accent:#4f9be6; --ui-accent-hover:#69abe9; --ui-accent-text:#0c1318; --ui-shadow:rgba(0,0,0,.5); }
        :host([data-scheme="night"]) {
          --ui-bg:#14181b; --ui-surface:#1b2024; --ui-surface-2:#232a2f; --ui-text:#aeb8be; --ui-text-dim:#7e898f; --ui-text-faint:#626c72; --ui-border:#2a3137; --ui-border-2:#232a2f; --ui-border-strong:#38424a; --ui-hover:#232a30; --ui-accent:#3f7fb5; --ui-accent-hover:#4d8cc2; --ui-accent-text:#0a0e11; --ui-shadow:rgba(0,0,0,.6); }
        /* Full-bleed map filling everything above the bottom tab bar; panels rise
           over it as a sheet rather than displacing it. */
        #map { position:absolute; inset:0 0 var(--botbar-h) 0; }
        #map chart-plotter { width:100%; height:100%; }
        .btn { cursor:pointer; border:1px solid var(--ui-border-strong); background:var(--ui-surface); border-radius:6px; padding:6px 10px; font:inherit; color:var(--ui-text); }
        .btn:hover { background:var(--ui-hover); }
        /* Bottom bar — the navigation tabs, centred. The drawer flies up over the
           map above it; the bar itself stays put. (Live status lives in the thin
           top strip, see #statusbar.) */
        /* 3-column grid keeps the nav tabs centred in the viewport while the
           scheme toggle pins to the right edge. */
        #rail { position:absolute; left:0; right:0; bottom:0; height:var(--botbar-h); z-index:7;
          background:var(--ui-surface); border-top:1px solid var(--ui-border); box-shadow:0 -1px 5px rgba(0,0,0,.07);
          display:grid; grid-template-columns:1fr auto 1fr; align-items:center;
          padding:0 6px env(safe-area-inset-bottom,0px); box-sizing:border-box; }
        #rail .rail-tabs { grid-column:2; display:flex; flex-direction:row; align-items:center; justify-content:center; gap:8px; }
        #rail .rail-end { grid-column:3; justify-self:end; display:flex; flex-direction:row; align-items:center; gap:2px; }
        #rail .scheme-toggle, #rail .search-toggle { width:44px; }
        #rail .ri { flex:none; width:64px; height:44px; border:none; background:none; border-radius:12px; cursor:pointer; color:var(--ui-text-dim);
          display:flex; flex-direction:column; align-items:center; justify-content:center; gap:4px;
          transition:background .12s, color .12s; }
        #rail .ri:hover { background:var(--ui-surface-2); color:var(--ui-accent); }
        #rail .ri.on { background:var(--ui-accent); color:var(--ui-accent-text); }
        #rail .ri svg { width:20px; height:20px; display:block; }
        #rail .ri .cap { font-size:9.5px; font-weight:500; letter-spacing:.02em; }
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
        .add-tools { display:flex; gap:8px; margin-bottom:4px; }
        .add-tools .tool { flex:1; border:1px solid var(--ui-border-strong); background:var(--ui-surface); border-radius:8px; padding:9px; font:inherit; font-size:13px; cursor:pointer; color:var(--ui-text); }
        .add-tools .tool:hover { background:var(--ui-hover); }
        .add-tools .tool.on { background:var(--ui-accent); color:var(--ui-accent-text); border-color:var(--ui-accent); }
        .add-sel { border-top:1px solid var(--ui-border-2); margin-top:14px; padding-top:14px; }
        .add-sel .empty { color:var(--ui-text-faint); font-size:13px; text-align:center; padding:6px 0; }
        .add-sel .sel-line { display:flex; align-items:center; justify-content:space-between; margin-bottom:10px; font-weight:600; }
        .add-clear { background:none; border:none; color:var(--ui-accent); cursor:pointer; font:inherit; }
        .linkbtn:disabled { color:var(--ui-text-faint); cursor:default; text-decoration:none; }
        .pack-search { width:100%; box-sizing:border-box; border:1px solid var(--ui-border-strong); border-radius:8px; padding:9px 12px; font:inherit; margin-bottom:10px; background:var(--ui-surface); color:var(--ui-text); }
        .pack-search:focus { outline:none; border-color:var(--ui-accent); }
        /* region browser */
        .region-list { display:flex; flex-direction:column; }
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
        .region-title { margin:4px 0 2px; font-size:16px; }
        .region-status { background:#e4f5ea; color:#1f7a36; font-weight:600; font-size:12.5px; padding:6px 10px; border-radius:8px; margin:2px 0 4px; }
        .linkbtn { background:none; border:none; color:var(--ui-accent); cursor:pointer; font:inherit; padding:8px 0; display:block; }
        .linkbtn.danger { color:#c0392b; }
        /* persistent in-flight download/import pill (bottom-centre) */
        #dlpill { position:absolute; bottom:calc(var(--botbar-h) + 12px); left:50%; transform:translateX(-50%); z-index:7; display:inline-flex; align-items:center;
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
        /* chart packs (Coast Guard districts) */
        .pack-intro { color:var(--ui-text-dim); font-size:12px; line-height:1.5; margin:2px 0 12px; }
        .pack-grid { display:grid; grid-template-columns:1fr 1fr; gap:10px; }
        .pack-card { border:1px solid var(--ui-border-strong); border-radius:10px; padding:11px 12px 12px; background:var(--ui-surface); cursor:pointer; display:flex; flex-direction:column; gap:3px; transition:border-color .12s, box-shadow .12s; }
        .pack-card:hover { border-color:var(--ui-accent); }
        .pack-card:focus-visible { outline:none; border-color:var(--ui-accent); box-shadow:0 0 0 2px var(--ui-accent); }
        .pack-card.active { border-color:var(--ui-accent); box-shadow:inset 3px 0 0 var(--ui-accent); }
        .pack-card.installed { background:var(--ui-surface-2); }
        .pk-top { display:flex; align-items:center; justify-content:space-between; gap:6px; min-height:16px; }
        .pk-region { font-size:11px; text-transform:uppercase; letter-spacing:.05em; color:var(--ui-text-faint); font-weight:700; }
        .pk-badge { flex:none; font-size:10.5px; font-weight:700; padding:1px 8px; border-radius:10px; }
        .pk-badge.ok { background:#e4f5ea; color:#1f7a36; }
        .pk-badge.part { background:#fbf0d8; color:#8a6000; }
        .pk-name { font-weight:600; font-size:14px; }
        .pk-blurb { color:var(--ui-text-dim); font-size:12px; line-height:1.4; }
        .pk-meta { color:var(--ui-text-faint); font-size:11.5px; font-variant-numeric:tabular-nums; margin-top:2px; }
        .pk-actions { display:flex; gap:6px; margin-top:auto; padding-top:10px; } /* pinned to card bottom so buttons align across a row */
        .pk-btn { flex:1; border:none; background:var(--ui-accent); color:var(--ui-accent-text); border-radius:7px; padding:8px 9px; font:inherit; font-size:12.5px; font-weight:600; cursor:pointer; white-space:nowrap; }
        .pk-btn:hover { background:var(--ui-accent-hover); }
        .pk-btn:disabled { background:#9fb6cf; cursor:default; }
        .pk-btn.ghost { flex:none; background:var(--ui-surface); color:var(--ui-text-dim); border:1px solid var(--ui-border-strong); }
        .pk-btn.ghost:hover { background:#fdeceb; color:#c0392b; border-color:#e2b6b1; }
        /* find-a-chart search results */
        .pkr-row { display:flex; align-items:center; gap:10px; padding:9px 4px; border-bottom:1px solid var(--ui-border-2); cursor:pointer; }
        .pkr-row:last-child { border-bottom:none; }
        .pkr-row:hover { background:var(--ui-hover); }
        .pkr-info { flex:1; min-width:0; display:flex; flex-direction:column; }
        .pkr-title { font-weight:600; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
        .pkr-sub { color:var(--ui-text-faint); font-size:12px; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
        .pkr-have { flex:none; color:#1f7a36; font-size:12px; font-weight:600; }
        .pkr-dl { flex:none; padding:7px 11px; }
        .pkr-empty { color:var(--ui-text-faint); font-size:13px; text-align:center; padding:14px 0; }
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
        /* Live status as a small chip embedded in the bottom-right corner (the
           scale bar owns the bottom-left), over a full-bleed map; the subtle
           attribution line sits just above it. Anchors the per-cell popup above it. */
        #statusbar { position:absolute; right:calc(10px + env(safe-area-inset-right,0px)); bottom:calc(var(--botbar-h) + 8px); z-index:6;
          display:inline-flex; align-items:center; gap:10px; padding:5px 11px; box-sizing:border-box;
          max-width:calc(100vw - 20px); /* no overflow:hidden — it would clip the cell-status popup */
          background:color-mix(in srgb, var(--ui-surface) 82%, transparent); border:1px solid rgba(0,0,0,.06);
          border-radius:11px; backdrop-filter:blur(6px);
          box-shadow:inset 0 1px 2px rgba(0,0,0,.18), inset 0 -1px 0 rgba(255,255,255,.4), 0 1px 0 rgba(255,255,255,.4);
          font:11px system-ui,sans-serif; color:var(--ui-text); }
        /* NOAA attribution + "not for navigation" — subtle one-line text tucked
           under the status chip (no box), kept legible over the chart with a soft
           halo in the current surface colour. */
        #noaa-attr { position:absolute; right:calc(12px + env(safe-area-inset-right,0px)); bottom:calc(var(--botbar-h) + 40px); z-index:5; pointer-events:auto;
          font:500 10px/1.35 system-ui,sans-serif; letter-spacing:.01em; white-space:nowrap; text-align:right;
          color:var(--ui-text-dim);
          text-shadow:0 0 3px var(--ui-surface), 0 0 3px var(--ui-surface), 0 1px 1px var(--ui-surface); }
        #noaa-attr a, #noaa-attr .attr-link { color:inherit; text-shadow:inherit; cursor:pointer;
          text-decoration:underline; text-decoration-color:var(--ui-text-faint); text-underline-offset:2px; }
        #noaa-attr a:hover, #noaa-attr .attr-link:hover { color:var(--ui-accent); }
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
        .ins-lock { background:var(--ui-surface-2); color:var(--ui-text-dim); border-radius:6px; padding:6px 9px; margin-bottom:10px; font-size:12px; }
        .ins-cycler { display:flex; align-items:center; justify-content:center; gap:10px; margin-bottom:10px; font-size:12px; color:var(--ui-text-dim); }
        .ins-cycler .btn { padding:2px 9px; line-height:1.3; }
        /* Tile/cell generation indicator — inline in the status overlay, clickable
           to open the per-cell status popup. Spinner shows only while busy
           (loading cells or baking tiles); otherwise it's a resting "N charts". */
        .sb-bake { flex:0 1 auto; min-width:0; display:inline-flex; align-items:center; gap:6px; color:var(--ui-accent); cursor:pointer;
          border:1px solid transparent; background:transparent; border-radius:9px; padding:2px 6px; margin:-2px -2px -2px -4px;
          font:600 11px/1 system-ui,sans-serif; white-space:nowrap; font-variant-numeric:tabular-nums; }
        .sb-bake .sb-bake-txt { overflow:hidden; text-overflow:ellipsis; white-space:nowrap; min-width:0; }
        .sb-bake:hover { border-color:var(--ui-border-strong); background:var(--ui-surface-2); }
        .sb-bake[hidden] { display:none; }
        .sb-bake.has-fail { color:#cf3b3b; }
        /* When nothing is installed, hide the whole overlay's leading gap cleanly. */
        .sb-bake-spin { display:none; width:12px; height:12px; flex:none; border-radius:50%;
          border:2px solid color-mix(in srgb, var(--ui-accent) 30%, transparent); border-top-color:var(--ui-accent);
          animation:sb-bake-spin .7s linear infinite; }
        .sb-bake.busy .sb-bake-spin { display:inline-block; }
        @keyframes sb-bake-spin { to { transform:rotate(360deg); } }
        @media (prefers-reduced-motion: reduce) { .sb-bake-spin { animation-duration:2s; } }
        /* Per-cell status popup, opening upward from the chip's right edge. */
        #cell-status-pop { position:absolute; bottom:calc(100% + 8px); right:0; z-index:9;
          width:min(400px,calc(100vw - 24px)); max-height:min(70vh,560px); overflow:hidden;
          display:flex; flex-direction:column;
          background:var(--ui-surface); border:1px solid var(--ui-border-strong); border-radius:12px;
          box-shadow:0 10px 32px rgba(0,0,0,.24); padding:14px 16px; }
        #cell-status-pop[hidden] { display:none; }
        .csp-head { display:flex; align-items:center; justify-content:space-between; gap:8px;
          font:600 11px/1.2 system-ui,sans-serif; color:var(--ui-text-dim); text-transform:uppercase;
          letter-spacing:.04em; padding:0 0 12px; }
        .csp-clear { flex:none; border:1px solid #cf3b3b; color:#cf3b3b; background:transparent; cursor:pointer;
          border-radius:9px; padding:4px 10px; font:600 10px/1 system-ui,sans-serif; text-transform:none; letter-spacing:0; }
        .csp-clear:hover { background:#cf3b3b; color:#fff; }
        .csp-stats { display:grid; grid-template-columns:1fr 1fr; gap:9px 16px; padding:0 0 14px; margin-bottom:12px; border-bottom:1px solid var(--ui-border); }
        .csp-stats > div { display:flex; align-items:baseline; justify-content:space-between; gap:10px; font:500 12px/1.5 system-ui,sans-serif; white-space:nowrap; }
        .csp-stats span { color:var(--ui-text-dim); text-transform:uppercase; letter-spacing:.03em; font-size:9.5px; white-space:nowrap; flex:none; }
        .csp-stats b { color:var(--ui-text); font-weight:600; font-variant-numeric:tabular-nums; white-space:nowrap; flex:none; }
        /* The cell list scrolls within the popup; header + stats stay fixed. */
        .csp-list { list-style:none; margin:0 -4px; padding:0 4px; flex:1 1 auto; min-height:60px; overflow-y:auto; }
        .csp-row { display:flex; align-items:center; gap:10px; padding:8px 0; font:500 12.5px/1.3 system-ui,sans-serif; }
        .csp-row + .csp-row { border-top:1px solid var(--ui-border); }
        .csp-row.is-fail { align-items:flex-start; }
        .csp-dot { width:9px; height:9px; border-radius:50%; flex:none; margin-top:3px; box-shadow:0 0 0 1.5px rgba(255,255,255,.6); }
        .csp-name { flex:1; min-width:0; display:flex; flex-direction:column; gap:2px; color:var(--ui-text); }
        .csp-title { overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
        .csp-code { font-size:11px; color:var(--ui-text-dim); font-variant-numeric:tabular-nums; letter-spacing:.02em; }
        .csp-err { font:500 10.5px/1.35 system-ui,sans-serif; color:#cf3b3b; white-space:normal; word-break:break-word; }
        .csp-stat { flex:none; font-weight:600; font-size:11px; }
        .csp-queued { color:#9aa7b4; } .csp-loading { color:#d9892b; } .csp-ready { color:#2e9b57; } .csp-failed { color:#cf3b3b; }
        .csp-empty { color:var(--ui-text-dim); font:500 12px/1.2 system-ui,sans-serif; padding:8px 0; }
        /* The chip hugs its content; left edge is anchored, so the leading dot+band
           stay put as the scale digits change (tabular figures). */
        .sb-readout { flex:none; }
        .sb-readout .hud-main { display:flex; align-items:center; gap:6px;
          font-weight:600; font-size:12px; white-space:nowrap; font-variant-numeric:tabular-nums; }
        .sb-readout .hud-dot { width:8px; height:8px; border-radius:50%; flex:none; box-shadow:0 0 0 2px rgba(255,255,255,.6); margin-right:1px; }
        .sb-readout .hud-scale { color:var(--ui-accent); }
        .sb-readout .hud-z { color:var(--ui-text-dim); }
        .sb-readout .hud-sep { color:var(--ui-text-faint); }
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
        /* Panels are dialog popovers that pop FROM their tab, with a little caret
           arrow pointing back to it. MOBILE (base): above the bottom bar, caret
           down. DESKTOP: right of the dock, caret left (see min-width query). The
           caret position (--caret-left / --caret-top) is set in JS to the active
           tab's centre. Pops in with a fade+scale from the caret edge; fully
           hidden (visibility) when closed. */
        #drawer, #search { --caret:9px; }
        /* NB: no overflow:hidden on the popover itself — it would clip the caret.
           Inner scroll areas (.body / #search-results) round their own corners. */
        #drawer { position:absolute; left:8px; right:8px; bottom:calc(var(--botbar-h) + 14px); width:auto; max-height:76vh; z-index:6;
          background:var(--ui-bg); color:var(--ui-text); border:1px solid var(--ui-border); border-radius:14px;
          box-shadow:0 12px 38px rgba(0,0,0,.30); display:flex; flex-direction:column;
          transform-origin:bottom center; transform:translateY(6px) scale(.97); opacity:0; visibility:hidden;
          transition:opacity .15s ease, transform .15s ease, visibility 0s linear .15s; }
        #drawer.open { opacity:1; transform:none; visibility:visible; transition:opacity .15s ease, transform .15s ease; }
        #drawer .body { border-radius:0 0 13px 13px; }
        /* caret (base = pointing down at the tab below) */
        #drawer::after, #search::after { content:""; position:absolute; bottom:calc(-1 * var(--caret)); left:var(--caret-left,50%); transform:translateX(-50%);
          width:0; height:0; border-left:var(--caret) solid transparent; border-right:var(--caret) solid transparent;
          border-top:var(--caret) solid var(--ui-bg); filter:drop-shadow(0 2px 1px rgba(0,0,0,.12)); }
        #search::after { border-top-color:var(--ui-surface); }
        /* Settings lays its sections in responsive columns to use the panel width. */
        #settings-body { display:grid; grid-template-columns:repeat(auto-fit, minmax(240px, 1fr)); gap:0 28px; align-items:start; }
        /* Feature inspector — slides in from the RIGHT (overlays the map). */
        .ins-body { overflow:auto; padding:12px 0; }
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
        /* Dev panel: clearly separated sections, most-used at top, roomy spacing. */
        .dev-tools { display:flex; flex-direction:column; }
        .dev-sec { display:flex; flex-direction:column; gap:8px; padding:16px 0; border-top:1px solid var(--ui-border); }
        .dev-sec:first-child { padding-top:4px; border-top:none; }
        .dev-h { font-weight:600; font-size:11px; text-transform:uppercase; letter-spacing:.06em; color:var(--ui-text-faint); }
        .dev-h .bz { float:right; text-transform:none; letter-spacing:0; font-weight:500; color:var(--ui-text-dim); }
        .dev-note { margin:0; color:var(--ui-text-dim); font-size:12px; line-height:1.45; }
        .dev-row { display:flex; align-items:center; justify-content:space-between; gap:10px; min-height:24px; }
        .btn.wide { width:100%; text-align:center; }
        .btn.on { background:var(--ui-accent); color:#fff; border-color:var(--ui-accent); }
        .btn[disabled] { opacity:.45; cursor:default; }
        .btn.sm { padding:3px 10px; font-size:12px; white-space:nowrap; }
        .dev-cov { font-size:12px; color:var(--ui-text); }
        .dev-bands { display:flex; flex-direction:column; gap:3px; }
        .dev-band { display:flex; align-items:center; gap:8px; padding:4px 6px; border-radius:6px; cursor:pointer; }
        .dev-band:hover { background:var(--ui-hover); }
        .dev-band .bn { flex:1; text-transform:capitalize; font-weight:500; }
        .dev-band .bs { color:var(--ui-text-dim); font-size:12px; }
        .dev-band.off { opacity:.5; } .dev-band.off .bn { text-decoration:line-through; }
        .dev-band.gated .bs { color:#d9892b; }
        /* Right-click context menu (debug cell picker). */
        .ctx-menu { position:absolute; z-index:30; background:var(--ui-bg); color:var(--ui-text); border:1px solid var(--ui-border);
          border-radius:8px; box-shadow:0 6px 22px rgba(0,0,0,.28); padding:4px; min-width:180px; font:13px/1.4 system-ui,sans-serif; }
        .ctx-menu[hidden] { display:none; }
        .ctx-item { display:block; width:100%; text-align:left; padding:7px 10px; border:0; background:none; color:inherit; border-radius:6px; cursor:pointer; white-space:nowrap; }
        .ctx-item:hover { background:var(--ui-hover); }
        /* Debug overlay hover HUD: which cells are under the cursor and what each draws. */
        .debug-hud { position:absolute; z-index:29; pointer-events:none; background:rgba(17,22,28,.94); color:#e6edf3;
          border:1px solid #2b3742; border-radius:6px; padding:6px 8px; max-width:300px; font:11px/1.45 ui-monospace,Menlo,Consolas,monospace;
          box-shadow:0 4px 16px rgba(0,0,0,.4); }
        .debug-hud[hidden] { display:none; }
        .debug-hud .dh-cell { margin:0 0 4px; }
        .debug-hud .dh-cell:last-child { margin-bottom:0; }
        .debug-hud .dh-name { font-weight:700; }
        .debug-hud .dh-dot { display:inline-block; width:8px; height:8px; border-radius:50%; margin-right:5px; vertical-align:baseline; }
        .debug-hud .dh-none { opacity:.5; font-style:italic; }
        .debug-hud .dh-draw { opacity:.85; padding-left:13px; }
        .debug-hud .dh-lyr { color:#90caf9; }
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
        #empty { position:absolute; inset:0 0 var(--botbar-h) 0; display:flex; align-items:center; justify-content:center; z-index:4; pointer-events:none; }
        #empty[hidden] { display:none; }
        #empty .card { pointer-events:auto; background:var(--ui-surface); color:var(--ui-text); border-radius:16px; padding:30px 30px 24px; max-width:360px;
          text-align:center; box-shadow:0 8px 34px rgba(0,0,0,.22); }
        #empty .welcome-mark { width:44px; height:44px; margin-bottom:10px; }
        #empty h2 { margin:0 0 8px; font-size:21px; }
        #empty p { color:var(--ui-text-dim); margin:0 0 18px; line-height:1.5; }
        #empty .welcome-cta { display:inline-flex; align-items:center; gap:8px; width:auto; padding:11px 22px; font-size:15px; }
        #empty .welcome-sub { margin-top:12px; font-size:13px; color:var(--ui-text-faint); }
        #empty .linkbtn { background:none; border:none; color:var(--ui-accent); cursor:pointer; font:inherit; padding:0; text-decoration:underline; }
        /* Search sits beside the day/night toggle (the .rail-end group); tapping it
           opens the tiny flyout. */
        #rail .search-toggle.on { background:var(--ui-accent); color:var(--ui-accent-text); }
        #search[hidden] { display:block; } /* defeat UA hidden so the popover can fade out (base styles keep it invisible/non-interactive) */
        /* "All charts" — jump back to the zoomed-out world view (Charts mode only).
           Sits at the top-left of the map area, which starts past the open drawer. */
        #world-btn { position:absolute; top:12px; left:12px; z-index:5; display:inline-flex; align-items:center; gap:6px;
          border:1px solid var(--ui-border-strong); background:var(--ui-surface); color:var(--ui-text-dim); border-radius:18px; padding:7px 13px 7px 10px;
          font:600 12px system-ui,sans-serif; cursor:pointer; box-shadow:0 1px 4px rgba(0,0,0,.25); transition:left .2s; }
        #world-btn:hover { color:var(--ui-accent); border-color:var(--ui-accent); }
        #world-btn svg { width:16px; height:16px; }
        #world-btn[hidden] { display:none; }
        /* Search: same caret-popover as the panels — a dialog card with the input
           on top and results filling in underneath, popping from the search tab. */
        #search { position:absolute; right:8px; left:auto; bottom:calc(var(--botbar-h) + 14px); z-index:8; width:min(340px, calc(100vw - 16px));
          background:var(--ui-surface); border:1px solid var(--ui-border); border-radius:14px;
          box-shadow:0 12px 38px rgba(0,0,0,.30);
          transform-origin:bottom center; transform:translateY(6px) scale(.97); opacity:0; visibility:hidden;
          transition:opacity .15s ease, transform .15s ease, visibility 0s linear .15s; }
        #search:not([hidden]) { opacity:1; transform:none; visibility:visible; transition:opacity .15s ease, transform .15s ease; }
        #search input { width:100%; box-sizing:border-box; border:none; border-radius:14px; padding:11px 16px;
          font:inherit; background:transparent; color:var(--ui-text); outline:none; }
        #search-results { border-top:1px solid var(--ui-border-2); max-height:min(50vh, 360px); overflow-y:auto; border-radius:0 0 13px 13px; }
        #search-results[hidden] { display:none; }
        .sr-item { padding:8px 16px; cursor:pointer; border-bottom:1px solid var(--ui-border-2); }
        .sr-item:last-child { border-bottom:none; }
        .sr-item:hover, .sr-item.sel { background:var(--ui-hover); }
        .sr-item .t { font-weight:600; } .sr-item .s { color:var(--ui-text-faint); font-size:12px; }
        /* Subtle "loading more while data is shown" cue: a hairline indeterminate
           bar riding the top edge of the bottom tab bar. Opacity-controlled (always
           in DOM) so it fades in/out; the slide animation runs continuously. */
        .load-bar { position:absolute; bottom:var(--botbar-h); left:0; right:0; height:3px; z-index:25; pointer-events:none; overflow:hidden;
          opacity:0; transition:opacity .2s ease; background:rgba(13,71,161,.3); }
        .load-bar.on { opacity:1; }
        .load-bar::before { content:""; position:absolute; top:0; height:100%; width:40%;
          background:linear-gradient(90deg, transparent, #0d47a1 45%, #0d47a1 55%, transparent);
          box-shadow:0 0 8px rgba(13,71,161,.7); animation:load-slide 1.1s ease-in-out infinite; }
        :host([data-scheme="night"]) .load-bar, :host([data-scheme="dusk"]) .load-bar { background:rgba(90,155,216,.22); }
        :host([data-scheme="night"]) .load-bar::before, :host([data-scheme="dusk"]) .load-bar::before {
          background:linear-gradient(90deg, transparent, #6aaef0 45%, #6aaef0 55%, transparent); box-shadow:0 0 8px rgba(106,174,240,.6); }
        @keyframes load-slide { 0% { left:-40%; } 100% { left:100%; } }
        /* ---- Phone (base): popover content reflow --------------------------
           Chart packs go one-per-row and settings rows wrap their control under
           the label so they fit the narrower popover. */
        @media (max-width: 640px) {
          #empty .card { max-width:min(360px, calc(100vw - 48px)); }
          .pack-grid { grid-template-columns:1fr; }
          .set-row { flex-wrap:wrap; gap:8px 14px; }
          .set-row .lbl { flex:1 1 60%; }
          .seg-multi { flex-wrap:wrap; gap:8px 14px; }
        }
        /* On a narrow phone, drop the zoom from the status chip so the readout +
           chart-count never run past the screen edge (scale is what matters). */
        @media (max-width: 430px) {
          .sb-readout .hud-z, .sb-readout .hud-scale + .hud-sep { display:none; }
        }
        /* ---- Desktop: floating left dock + 40% left overlay panel -----------
           The nav becomes a floating vertical dock; opening a section overlays a
           panel over the LEFT of the map without moving it, so the right of the
           chart (and live S-52 changes) stays visible. */
        @media (min-width: 641px) {
          :host { --botbar-h:0px; }
          #map { inset:0; }
          #rail { left:14px; right:auto; top:50%; bottom:auto; transform:translateY(-50%);
            width:auto; height:auto; grid-template-columns:none;
            display:flex; flex-direction:column; align-items:center; gap:6px; padding:8px;
            border:1px solid var(--ui-border); border-radius:20px; box-shadow:0 6px 26px rgba(0,0,0,.20); }
          #rail .rail-tabs { flex-direction:column; gap:4px; }
          /* search + day/night group sits at the bottom of the dock, divided off. */
          #rail .rail-end { flex-direction:column; gap:4px; margin-top:6px; padding-top:10px;
            border-top:1px solid var(--ui-border-2); }
          #rail .scheme-toggle, #rail .search-toggle { width:64px; }
          .load-bar { top:0; bottom:auto; }
          /* Popovers sit right of the dock and pop from the left (caret points at
             the dock tab). The panel is the tall 40% left overlay; search is a
             compact card near the bottom (by the search tab). */
          #drawer, #search { right:auto; transform-origin:left center; transform:translateX(-6px) scale(.985); }
          #drawer.open, #search:not([hidden]) { transform:none; }
          #drawer { left:90px; top:14px; bottom:14px; width:min(40vw, 520px); max-height:none; }
          #search { left:90px; top:auto; bottom:14px; width:340px; max-height:calc(100vh - 28px); }
          /* caret points LEFT toward the dock tab */
          #drawer::after, #search::after { left:calc(-1 * var(--caret)); right:auto; bottom:auto; top:var(--caret-top,50%);
            transform:translateY(-50%); border-left:none;
            border-top:var(--caret) solid transparent; border-bottom:var(--caret) solid transparent;
            border-right:var(--caret) solid var(--ui-bg); }
          #search::after { border-right-color:var(--ui-surface); }
        }
      </style>
      <div id="map"></div>
      <div id="statusbar">
        <button id="bake-status" class="sb-bake" type="button" hidden title="Chart tile generation — click for per-cell status">
          <span class="sb-bake-spin"></span><span class="sb-bake-txt"></span>
        </button>
        <div id="cov-readout" class="sb-readout"></div>
        <div id="cell-status-pop" hidden></div>
      </div>
      <div id="load-bar" class="load-bar" aria-hidden="true"></div>
      <div id="rail">
        <div class="rail-tabs">
        <button class="ri" id="rail-home" title="Chart view">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="M9 3 3 5.5v15L9 18l6 3 6-2.5v-15L15 6 9 3Z"/><path d="M9 3v15M15 6v15"/></svg>
          <span class="cap">Charts</span>
        </button>
        <button class="ri" id="rail-menu" title="Get &amp; manage charts">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="M12 3 3 7.5l9 4.5 9-4.5L12 3Z"/><path d="M3 12l9 4.5L21 12"/><path d="M3 16.5 12 21l9-4.5"/></svg>
          <span class="cap">Library</span>
        </button>
        <button class="ri" id="rail-settings" title="Settings">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1Z"/></svg>
          <span class="cap">Settings</span>
        </button>
        <button class="ri" id="dev-toggle" title="Developer tools — share, tile/band inspector, feature inspect">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="m8 9-4 3 4 3"/><path d="m16 9 4 3-4 3"/><path d="m13 6-2 12"/></svg>
          <span class="cap">Dev</span>
        </button>
        </div>
        <div class="rail-end">
          <button class="ri search-toggle" id="search-tab" type="button" title="Search charts & features">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round"><circle cx="11" cy="11" r="7"/><path d="m20 20-3.5-3.5"/></svg>
          </button>
          <button class="ri scheme-toggle" id="scheme-toggle" type="button" title="Colour scheme — tap to cycle Day · Dusk · Night">
            <svg id="scheme-svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"></svg>
          </button>
        </div>
      </div>
      <div id="search" hidden><input id="search-input" type="search" placeholder="Search charts & features…" autocomplete="off" spellcheck="false"><div id="search-results" hidden></div></div>
      <button id="world-btn" hidden title="Zoom out to all charts">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="9"/><path d="M3 12h18M12 3c2.5 2.5 3.8 5.7 3.8 9S14.5 18.5 12 21c-2.5-2.5-3.8-5.7-3.8-9S9.5 5.5 12 3Z"/></svg>
        <span>All charts</span>
      </button>
      <div id="noaa-attr"><a href="${NOAA_ENC_URL}" target="_blank" rel="noopener">NOAA ENC®</a> · <button id="attr-terms" class="attr-link" type="button">Terms</button> · not for navigation</div>
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
      <div id="ctx-menu" class="ctx-menu" hidden></div>
      <div id="debug-hud" class="debug-hud" hidden></div>
      <div id="empty" hidden><div class="card">
        <svg class="welcome-mark" viewBox="0 0 24 24" fill="none" stroke="#1565c0" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="5" r="2"/><path d="M12 7v14M5 12a7 7 0 0 0 14 0M3 12h2m14 0h2M12 21a7 7 0 0 1-5-2m10 0a7 7 0 0 1-5 2"/></svg>
        <h2>Welcome aboard</h2>
        <p>No charts yet. Pick a cruising region and download a pack — official NOAA charts are fetched and baked right here on your machine, ready to use offline.</p>
        <button id="empty-add" class="cta welcome-cta">⚓ Browse chart regions</button>
        <div class="welcome-sub">or <button id="empty-import" class="linkbtn">import from a file</button></div>
      </div></div>
      <div id="drawer">
        <div class="dhead"><strong id="dtitle">Chart library</strong><button id="close" class="btn">✕</button></div>
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
          <div class="panel" data-panel="inspect">
            <div id="inspect-body" class="ins-body"></div>
            <div id="dev-tools" class="dev-tools"></div>
          </div>
        </div>
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
    $("dev-toggle").onclick = () => this.toggleSection("inspect");
    $("scheme-toggle").onclick = () => this._cycleScheme();
    this._syncSchemeUI(); // paint the toggle's initial icon
    this._renderDevPanel(); // fills #dev-tools (share, band toggles, tile inspector)
    $("bake-status").onclick = (e) => { e.stopPropagation(); this._toggleCellStatusPopup(); };
    // Esc dismisses the debug context menu, else exits the feature inspector.
    window.addEventListener("keydown", (e) => {
      if (e.key !== "Escape") return;
      const menu = this.shadowRoot.getElementById("ctx-menu");
      if (menu && !menu.hidden) { this._hideContextMenu(); return; }
      if (this._inspectMode) this.closeDrawer();
    });
    $("empty-add").onclick = () => this.openCharts();
    $("empty-import").onclick = () => { this.openCharts(); const det = r.querySelector(".import-more"); if (det) det.open = true; };
    $("rail-home").classList.add("on"); // boot shows the bare chart viewer
    // NOAA ENC user-agreement gate + attribution "Terms" link.
    $("attr-terms").onclick = () => this._showAgreement();
    $("agree-accept").onclick = () => this._resolveAgreement(true);
    $("agree-decline").onclick = () => this._resolveAgreement(false);

    // Search (offline, over catalog titles + loaded chart feature data). The nav
    // button toggles a tiny flyout with the input + results.
    const si = $("search-input");
    const closeSearch = () => { $("search").hidden = true; $("search-tab").classList.remove("on"); };
    const openSearch = () => { $("search").hidden = false; $("search-tab").classList.add("on"); this._positionCaret($("search"), $("search-tab")); si.focus(); };
    $("search-tab").onclick = () => ($("search").hidden ? openSearch() : closeSearch());
    // "All charts" — re-frame the selection map to the zoomed-out world view.
    $("world-btn").onclick = () => this._frameChartsWorld();
    si.oninput = () => this.doSearch(si.value);
    si.onkeydown = (e) => {
      if (e.key === "Enter") this.gotoSearchHit(0);
      else if (e.key === "Escape") { si.value = ""; closeSearch(); }
    };
    si.onfocus = () => { if (si.value.trim().length >= 2) this.doSearch(si.value); };

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
    r.getElementById("dtitle").textContent = name === "settings" ? "Settings" : name === "inspect" ? "Dev" : "Chart library";
    // Charts is the "get & see charts" mode: overlay every catalog cell on the
    // map (selected ones highlighted) so you can see and box-select coverage.
    // Any other section is just chrome over the live viewer — no overlay.
    if (name === "charts") { this.renderCharts(); this._enterChartsMode(); }
    else this._exitChartsMode();
    // Feature-inspect is NOT auto-armed; it's a button inside the Dev panel. Any
    // section switch disarms it (and clears dev hole markers / refreshes the panel).
    this._setInspectMode(false);
    if (name === "inspect") { this._clearDevHoles(); this._renderDevPanel(); }
    this.setDrawerOpen(true);
  }

  // Home: the full-screen chart viewer — drop any selection overlay/section and
  // show just the map.
  goHome() {
    this._cancelAreaSelect();
    this.closeDrawer();
  }

  closeDrawer() { this.setDrawerOpen(false); }

  // Slide the panel sheet up/down from the tab bar. data-sec drives its per-section
  // size (Charts wide+short, Settings/Dev tall); set before opening so it animates
  // in at the right size.
  // Point a popover's caret at the tab it opened from. Sets --caret-left (mobile,
  // caret on the bottom edge) or --caret-top (desktop, caret on the left edge) to
  // the tab's centre, clamped to the popover's edges. Measures the popover with its
  // pop-in transform removed so the rect is the final resting position.
  _positionCaret(pop, tab) {
    if (!pop || !tab) return;
    const desktop = window.matchMedia("(min-width:641px)").matches;
    const tr = tab.getBoundingClientRect();
    const prev = pop.style.transform; pop.style.transform = "none";
    const pr = pop.getBoundingClientRect();
    pop.style.transform = prev;
    if (desktop) pop.style.setProperty("--caret-top", `${Math.max(16, Math.min(pr.height - 16, tr.top + tr.height / 2 - pr.top))}px`);
    else pop.style.setProperty("--caret-left", `${Math.max(18, Math.min(pr.width - 18, tr.left + tr.width / 2 - pr.left))}px`);
  }

  setDrawerOpen(open) {
    const r = this.shadowRoot;
    const drawer = r.getElementById("drawer");
    if (open && this._section) drawer.dataset.sec = this._section;
    drawer.classList.toggle("open", open);
    r.getElementById("rail-menu").classList.toggle("on", open && this._section === "charts");
    r.getElementById("rail-settings").classList.toggle("on", open && this._section === "settings");
    r.getElementById("dev-toggle").classList.toggle("on", open && this._section === "inspect");
    if (open) {
      const tabId = this._section === "settings" ? "rail-settings" : this._section === "inspect" ? "dev-toggle" : "rail-menu";
      this._positionCaret(drawer, r.getElementById(tabId));
    }
    // Home is "active" whenever the drawer is shut — i.e. the bare chart viewer.
    r.getElementById("rail-home").classList.toggle("on", !open);
    // Closing the drawer leaves Charts mode: restore the ENC render + prior view,
    // clear the region highlight, and cancel any in-progress box drag. Also disarm
    // feature-inspect (crosshair/box-zoom) so it doesn't linger over the bare map.
    if (!open) { this._exitChartsMode(); this._setInspectMode(false); }
    this.updateEmptyState(); // the welcome card hides while the drawer is open
    setTimeout(() => {
      if (!this._map) return;
      this._map.resize();
      // Frame the world view when entering Charts mode (once the sheet has settled).
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
    const depthRow = (key, label) => {
      const defM = DEFAULT_MARINER[key];
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
        ${depthRow("shallowContour", "Shallow contour")}
        ${depthRow("safetyContour", "Safety contour")}
        ${depthRow("deepContour", "Deep contour")}
        ${depthRow("safetyDepth", "Safety depth")}
      </div>
      <div class="set-section">
        <h3>Display</h3>
        <div class="set-row"><div class="lbl"><span class="t">Detail level</span><span class="d">Which feature categories are drawn — Base is always on (S-52)</span></div>
          <div class="ctl"><div class="seg-multi">
            <label class="chk" title="Display Base is the minimum safe-navigation set and cannot be turned off (S-52 §10.2)"><input type="checkbox" checked disabled>Base</label>
            <label class="chk"><input type="checkbox" data-key="displayStandard" ${m.displayStandard === false ? "" : "checked"}>Standard</label>
            <label class="chk"><input type="checkbox" data-key="displayOther" ${m.displayOther ? "checked" : ""}>Other</label></div></div></div>
        <div class="set-row"><div class="lbl"><span class="t">Area boundaries</span></div>
          <div class="ctl"><select data-key="boundaryStyle">${["plain", "symbolized"].map((v) =>
            `<option ${(m.boundaryStyle || "symbolized") === v ? "selected" : ""}>${v}</option>`).join("")}</select></div></div>
        ${toggle("simplifiedPoints", "Simplified symbols", "Simplified point symbols instead of paper-chart shapes (buoys, beacons)", !!m.simplifiedPoints)}
        ${toggle("fourShadeWater", "Four-shade water", "Four depth shades instead of two", m.fourShadeWater !== false)}
        ${toggle("showNoData", "No-data hatch", "Mark areas with no chart data (off shows the plain basemap)", m.showNoData !== false)}
        <div class="set-row"><div class="lbl"><span class="t">Cell boundaries</span><span class="d">Outline + name of installed cells when zoomed out past their detail</span></div>
          <label class="switch"><input type="checkbox" data-app-key="showCellBounds" ${this._showCellBounds ? "checked" : ""}><span class="sl"></span></label></div>
        ${toggle("shallowPattern", "Shallow pattern", "Diagonal fill in shallow water", !!m.shallowPattern)}
        ${toggle("showSoundings", "Spot soundings", "Individual depth soundings", m.showSoundings !== false)}
        ${toggle("showLightDescriptions", "Light descriptions", "Light characteristics text (e.g. Fl(2)R 10s)", m.showLightDescriptions !== false)}
        ${toggle("showFullSectorLines", "Full sector lines", "Extend light sector legs to their nominal range instead of short 25mm stubs", !!m.showFullSectorLines)}
        ${toggle("showIsolatedDangersShallow", "Isolated dangers (shallow)", "Show isolated-danger symbols only at Standard detail instead of always (S-52 UDWHAZ05)", !!m.showIsolatedDangersShallow)}
        ${toggle("showNames", "Place names", "Geographic names and object labels", m.showNames !== false)}
        ${toggle("showContourLabels", "Contour labels", "Show depth values on contours", !!m.showContourLabels)}
        ${toggle("dataQuality", "Data quality", "CATZOC zones-of-confidence overlay (M_QUAL)", !!m.dataQuality)}
        ${toggle("showMetaBounds", "Metadata boundaries", "Coverage/region indicator lines (nautical-publication, nav-system, coverage)", !!m.showMetaBounds)}
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
    // App-level toggles (not S-52 mariner settings): cell-boundary overlay.
    el.querySelectorAll("[data-app-key]").forEach((inp) => {
      inp.onchange = () => {
        if (inp.dataset.appKey === "showCellBounds") this._setCellBoundsVisible(inp.checked);
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

// True when the page was opened as a shared-view link (<origin>/#share or
// ?share) — boot() then reconstructs the publisher's scene from /api/share.
function isShareUrl() {
  const h = (location.hash || "").replace(/^#/, "");
  return h === "share" || new URLSearchParams(location.search).has("share");
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
