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
import { DISTRICTS, NOAA_ENC_URL } from "./components/chart-library.mjs"; // NOAA CG-district packs + ENC page (shared)
import { ChartDownloader } from "./data/chart-downloader.mjs"; // chart discovery + acquisition
import { NotificationCenter } from "./app/notification-center.mjs"; // app-level task-progress + banner bus
import { ChartService } from "./data/chart-service.mjs"; // server import/bake jobs + pack registry
import { AuxStore } from "./data/aux-store.mjs"; // TXTDSC/PICREP external files (companion aux zip)
import { ChartStore } from "./data/chart-store.mjs";
import { UNIT_DEFAULTS } from "./lib/units.mjs"; // configurable display units (categories now in core-settings.mjs)
import { ChartRadar } from "./map/radar.mjs"; // off-screen installed-chart edge pointers
import { HudController } from "./map/hud.mjs"; // status readout + overscale zoom cap
import { CoverageBoxes } from "./map/coverage-boxes.mjs"; // installed-chart coverage overlay
import { BANDS, BAND_LABEL, BAND_COLOR, BAND_MINZOOM, DEV_BANDS, bandForScale } from "./lib/bands.mjs";
import { esc, loadJSON, maxZoomForScaleFloor, freshness, fmtIssue, fmtMB, fmtBytes, isShareUrl, parseViewHash, copyText, flashBtn } from "./lib/util.mjs";
import { archivePut, archiveGet } from "./data/archive-store.mjs";

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
// still needs the region labels (_reattachName, _rebuildAllPerBand).

// Does a GeoJSON geometry intersect the lon/lat box [W,S,E,N]? Points test exactly;
// lines/polygons use a bbox-overlap approximation (fine for the area inspector).
// A chart vector source: the realtime path has one "chart" source; the legacy
// pmtiles path had a "chart-<band>" source per band. (Used by the inspector.)
function isChartSource(s) {
  return typeof s === "string" && (s === "chart" || s.startsWith("chart-"));
}

// Fuzzy match score: does `q` appear as a (possibly non-contiguous) subsequence
// of `text`? Both must already be lowercase. Returns a score (higher = better) or
// -1 for no match. Rewards contiguous runs, matches at word starts, and an early
// first match — so "chesbay" finds "Chesapeake Bay" and a clean substring beats a
// scattered one. A leading exact-substring hit gets a big bonus so it ranks first.
function fuzzyScore(q, text) {
  if (!q) return 0;
  if (!text) return -1;
  let qi = 0, score = 0, run = 0, prev = -2;
  for (let i = 0; i < text.length && qi < q.length; i++) {
    if (text[i] !== q[qi]) continue;
    let s = 1;
    if (prev === i - 1) { run++; s += run * 5; } else run = 0; // contiguous run bonus
    const before = i === 0 ? " " : text[i - 1];
    if (i === 0 || before === " " || before === "-" || before === "/" || before === "," || before === ".") s += 10; // word-start bonus
    if (qi === 0) s += Math.max(0, 8 - i); // earlier first match is better
    score += s;
    prev = i; qi++;
  }
  if (qi < q.length) return -1; // not all query chars matched, in order
  if (text.includes(q)) score += 25 + (text.startsWith(q) ? 15 : 0); // contiguous / prefix boost
  return score;
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
    this._cellStatus = new Map();       // name -> "queued"|"loading"|"ready"|"failed" (lazy wasm baker load)
    this._cellError = new Map();        // name -> error message, for cells that failed to parse
    this._cellBounds = new Map();       // name -> [w,s,e,n] footprint (from the baker), to locate uploaded cells
    this._cellScale = new Map();        // name -> compilation scale (CSCL) of uploaded cells, for picking a detail zoom
    this._cellUsage = { bytes: 0, count: 0 }; // raw cell store disk usage (refreshed on popup open / load)
    this._archive = new Map();          // name -> {blob, entry, meta} from opened zips
    this._selected = new Set();         // names ticked for import / NOAA download
    this._dlRegions = new Set();        // installed NOAA region numbers (from GET /api/charts)
    this._regionArchives = [];          // [{num,file,bounds}] — one pmtiles per installed region
    this._importedArchives = [];        // in-memory imported/uploaded archives (Blob/File), re-added on every coverage rebuild so a too-large-to-persist import isn't lost when a later provision resets the bands
    this._userBake = null;              // {cells:[…], bounds:[w,s,e,n]} of the map-selected charts-user.pmtiles, or null
    this._showCellBounds = localStorage.getItem("cp-cell-bounds") !== "0"; // coverage boxes when zoomed out past chart data (default ON; opt-out)
    this._showChartRadar = localStorage.getItem("cp-chart-radar") !== "0"; // edge pointers to off-screen installed charts (default ON)
    this._debugCells = localStorage.getItem("cp-debug-cells") === "1"; // debug: all cell footprints coloured by load state
    this._bandsOff = new Set(loadJSON(LS_BANDS_OFF, [])); // usage bands turned off (hide layers + gate the realtime baker)
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
    // persist methods, so persistence is unchanged. Plugins will register here
    // too. The dev tools stay in the shell shadow (#dev-region) and are revealed
    // when the dialog's active tab is Advanced (see _syncDevRegion).
    this._settingsRegistry = new SettingsRegistry();
    for (const c of coreSettingsContributions(this)) this._settingsRegistry.register(c);
    this._settingsDlg = this.shadowRoot.getElementById("settings-dlg");
    if (this._settingsDlg) {
      this._settingsDlg.configure({ registry: this._settingsRegistry });
      // The dev tools live in the shell shadow (#dev-region); reveal them when the
      // dialog switches to the Advanced tab (option B).
      this._settingsDlg.addEventListener("tab-change", () => this._syncDevRegion());
    }

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

  // Per-cell load status, streamed from the wasm baker as cells are parsed one at
  // a time. name → "queued" | "loading" | "ready" | "failed".
  _onCellStatus({ name, status, info }) {
    if (!this._cellStatus) this._cellStatus = new Map();
    this._cellStatus.set(name, status);
    if (status === "loading") console.log(`[charts] loading ${name}…`);
    else if (status === "ready") {
      this._cellError.delete(name);
      if (info && Array.isArray(info.bounds) && info.bounds.length === 4) this._cellBounds.set(name, info.bounds);
      console.log(`[charts] ${name} ready${info && info.ms != null ? ` (${info.ms}ms)` : ""}`);
    }
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
    if (this._renderSel.size) this._refreshCharts(false);
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
    const chartLayers = (map.getStyle()?.layers || []).filter((l) => l.source && isChartSource(l.source) && !l.id.startsWith("scaminprobe")).map((l) => l.id);
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
    if (this._devVisible()) this._renderDevPanel();
    await this._refreshCharts(false); // keep the camera put
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
    await this._refreshCharts();
    if (this._chartLib) this._chartLib.refresh();
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
      const loc = this._cellLocation(n); // clickable → fly to the cell when we know where it is
      return `<li class="csp-row${err ? " is-fail" : ""}${loc ? " csp-loc" : ""}"${loc ? ` data-cell="${esc(n)}" title="Go to ${esc(title)}"` : ""}><span class="csp-dot" style="background:${BAND_COLOR[band]}"></span>`
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
      + `<div><span>Cell data</span><b>${fmtBytes(cu.bytes)} on disk</b></div>`
      + `<div><span>Tiles</span><b>${u.memTiles} mem · ${u.diskTiles} disk</b></div>`
      + `<div><span>Tile memory</span><b>${fmtBytes(u.memBytes)} / ${fmtBytes(u.memCap)}</b></div>`
      + `<div><span>Tile disk</span><b>${fmtBytes(u.diskBytes)} / ${fmtBytes(u.diskCap)}</b></div>`
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
    pop.querySelectorAll(".csp-row[data-cell]").forEach((li) => li.addEventListener("click", (e) => { e.stopPropagation(); this._flyToCell(li.dataset.cell); }));
    pop.hidden = false;
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

  // Fly the map to a cell's footprint — lets you find an uploaded chart you can't
  // otherwise locate. Closes the popup + drawer so the chart is visible.
  _flyToCell(name) {
    const bb = this._cellLocation(name);
    if (!bb || !this._map) return;
    this._cellPopOpen = false; clearInterval(this._cellPopTimer); this._renderCellStatusPopup();
    this.closeDrawer();
    // Zoom to the cell's detail level — a large-scale cell only renders at high
    // zoom, so fitting its small footprint at ~z14 would still show only basemap.
    const need = this._cellTargetZoom(name);
    const cam = this._map.cameraForBounds([[bb[0], bb[1]], [bb[2], bb[3]]], { padding: 60 });
    const zoom = Math.min(18, Math.max(cam ? cam.zoom : 13, need ? need + 0.5 : 0));
    this._map.easeTo({ center: cam ? cam.center : [(bb[0] + bb[2]) / 2, (bb[1] + bb[3]) / 2], zoom, duration: 800 });
  }


  // Fly to a set of packs from a tapped chart-radar chip. A single pack lands at
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
    if (this._radar) this._radar.setVisible(on);
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

    // Persist the view so a refresh resumes where you were; refresh the coverage
    // panel's in-view cell list for the new viewport.
    map.on("moveend", () => {
      this.saveView();
      this._assessCoverage();
      this._hud.updateZoomCap(); // clamp zoom-in to the finest band covering the new view
      // Dev tile inspector: refresh in-view band counts; a prior coverage measure
      // is now stale (different viewport), so drop its hole overlay.
      if (this._devVisible()) { this._clearDevHoles(); this._renderDevPanel(); }
    });

    // Chart radar: edge pointers to off-screen installed charts (its own module;
    // owns its overlay + map listener). Fed from the installed-pack metadata.
    this._radar = new ChartRadar({
      host: this.shadowRoot.getElementById("chart-radar"),
      map,
      getPacks: () => this._packsMeta || [],
      getUnits: () => this._mariner,
      labelFor: (name) => this._setLabel(name),
      onPick: (packs) => this._flyToPacks(packs),
      visible: this._showChartRadar,
    });

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
    if (this._chartLib) this._chartLib.refresh();
    this._task = { status: "done", _flourish: true };
    this._renderTaskUI();
    this._clearTaskSoon(1200);
  }

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
    // Dev tile inspector: sampled coverage-hole points (Inspect → Coverage → Measure).
    map.addSource("tile-holes", { type: "geojson", data: empty });
    map.addLayer({ id: "tile-holes", type: "circle", source: "tile-holes", paint: { "circle-radius": 4, "circle-color": "#ff1744", "circle-opacity": 0.75, "circle-stroke-color": "#fff", "circle-stroke-width": 1 } });
    // Installed-cell coverage: at zooms BELOW a cell's native band (where its
    // chart detail isn't baked yet) draw its footprint + name, so when zoomed out
    // you can tell WHAT coverage you have, not just that you have some. One set of
    // layers per band, auto-hidden at the band's native min zoom (maxzoom) — where
    // the real chart takes over.
    // Installed-chart coverage overlay (own controller — owns the inst-bounds source
    // + its box/outline layers + the per-zoom min-size growth + click-to-fly). The
    // debug overlay below reuses this source, so addLayers() must run first.
    this._coverage = this._coverage || new CoverageBoxes({ map, visible: this._showCellBounds });
    this._coverage.addLayers();
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
    // ECDIS-style crosshair cursor over the chart so it's clear the pointer is a
    // pick point (a click runs the cursor pick / district preview). Inspect mode and
    // the box selectors set their own cursor and restore this on exit.
    map.getCanvas().style.cursor = "crosshair";
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
      // (The Charts cell-picker tap-to-preview-a-district branch was removed with
      // the main-map cell picker; the <chart-library> panel is the chart surface.)
      if (this._inspectMode) { // dev feature inspector
        if (this._areaCleanup || e.originalEvent.shiftKey) return; // shift = box
        if (this._inspectLocked) { this._inspectLocked = false; this._inspectAt(e.point, false); return; }
        this._inspectAt(e.point, true); // lock onto whatever's here
        return;
      }
      // Zoomed out over an installed-chart coverage marker → fly to that chart at
      // its detail zoom (so you can find + open installed charts without knowing
      // where/at what zoom they live). Otherwise the default ECDIS cursor pick.
      if (this._coverage && this._coverage.tapFlyTo(e.point)) return;
      // Default chart-view interaction: ECDIS cursor pick (S-52 PresLib §10.8).
      this._pickReportAt(e.point, e.originalEvent);
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
    if (on) this._closePick(); // dev inspector and the user pick report are mutually exclusive
    this._inspectLocked = false;
    this._inspectLastKey = "";
    if (on) this._cancelAreaSelect();
    const map = this._map;
    if (map) {
      map.getCanvas().style.cursor = "crosshair"; // chart default is also crosshair
      // Free SHIFT+drag for area capture (MapLibre uses it for box-zoom by default).
      if (on) map.boxZoom.disable(); else map.boxZoom.enable();
    }
    if (on) this._inspectHint("Hover to inspect · click to lock · SHIFT+drag to capture an area.");
    else this._closeInspect();
    // The rail button reflects dev-panel-open (setDrawerOpen); inspect on/off is
    // shown by the in-panel "Inspect features" button — refresh it.
    if (this._devVisible()) this._renderDevPanel();
  }

  // Inspect the chart features at a canvas point. `lock` freezes the panel on a
  // hit (the click-to-lock action); a no-hit lock is a no-op (so clicking empty
  // chart doesn't clear a useful hover), a no-hit hover shows the hint.
  _inspectAt(point, lock) {
    const map = this._map;
    const feats = map.queryRenderedFeatures(point).filter((f) => isChartSource(f.source) && !f.layer.id.startsWith("scaminprobe"));
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
    if (!body) return; // the inspector panel only exists while Settings → Advanced is open
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
  // — including a headless browser used for debugging — reopens the same camera.
  // The link carries ONLY the view (#v=lon,lat,zoom[,bearing,pitch]); the cells
  // and tiles already live on the server (the hub), so there is nothing to
  // upload or re-download — the opener just renders the same spot from what is
  // already loaded. The link is copied to the clipboard. (Legacy #share snapshot
  // links still restore via _loadSharedView for backward compatibility.)
  _shareView(btn) {
    const m = this._map;
    if (!m) return;
    try {
      const c = m.getCenter();
      const parts = [+c.lng.toFixed(6), +c.lat.toFixed(6), +m.getZoom().toFixed(3)];
      const b = +m.getBearing().toFixed(1), p = +m.getPitch().toFixed(1);
      if (b || p) { parts.push(b); if (p) parts.push(p); } // omit trailing zeros
      const url = location.origin + location.pathname + "#v=" + parts.join(",");
      console.log("[share] view link:", url);
      copyText(url).then((ok) => { if (btn) flashBtn(btn, ok ? "✓ copied" : "✓"); });
    } catch (e) {
      console.warn("[share] link failed:", e);
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
    const cov = this._devCoverage;
    let covLine = "not measured for this view";
    if (cov) {
      if (cov.holePct === 0) covLine = "✓ full coverage — no holes";
      else if (cov.gated.length) covLine = `${cov.holePct}% holes · filled by ${cov.gated.slice(0, 6).join(", ")}${cov.gated.length > 6 ? `, +${cov.gated.length - 6}` : ""} (zoom in)`;
      else covLine = `${cov.holePct}% holes · no installed cell covers them`;
    }
    const inspecting = this._inspectMode;
    const dbgOn = this._debugCells;
    const busy = this._taskRunning() || (this._chartLib && this._chartLib.busy);
    el.innerHTML = `
      <section class="dev-sec">
        <div class="dev-h">Charts</div>
        <button id="dev-rebuild" class="btn wide"${busy ? " disabled" : ""}>↻ Rebuild all charts</button>
        <p class="dev-note">Re-bake every installed NOAA / IENC district into per-band tile sets from the cells already on the server — <b>no re-download</b>. Use after a baking change. Progress shows in the notification pill.</p>
      </section>

      <section class="dev-sec">
        <div class="dev-h">Share view</div>
        <button id="dev-share" class="btn wide">Copy share link</button>
        <p class="dev-note">Copies a link to <b>this exact camera</b> (center / zoom / bearing). Opens the same spot using the charts already on the server — <b>no upload, no re-download</b>.</p>
      </section>

      <section class="dev-sec">
        <div class="dev-h">Feature inspector</div>
        <button id="dev-inspect" class="btn wide${inspecting ? " on" : ""}">${inspecting ? "● Inspecting — click to stop" : "Inspect features"}</button>
        <button id="dev-feat" class="btn wide"${inspecting ? "" : " disabled"} title="Copy the selected feature's source/geometry/attributes to clipboard + server">Copy feature debug</button>
        <p class="dev-note">Hover a feature to highlight it · click to lock · SHIFT+drag to capture an area.</p>
      </section>

      <section class="dev-sec">
        <div class="dev-h">Coverage</div>
        <div class="dev-row"><span class="dev-cov">${covLine}</span><button id="dev-measure" class="btn sm">Measure</button></div>
        <p class="dev-note">Grid-samples the view for holes (no chart data) and paints them red.</p>
      </section>

      <section class="dev-sec">
        <div class="dev-h">Chart bands</div>
        ${DEV_BANDS.map((b) => `<label class="dev-row"><span><span style="display:inline-block;width:9px;height:9px;border-radius:50%;background:${BAND_COLOR[b]};margin-right:7px;vertical-align:-1px"></span>${BAND_LABEL[b]}</span><input class="dev-band" type="checkbox" data-band="${b}"${this._bandsOff.has(b) ? "" : " checked"}></label>`).join("")}
        <p class="dev-note">Turn a usage band's charts off to declutter or compare what each band contributes. Hides that band's layers everywhere; persists across reloads.</p>
      </section>

      <section class="dev-sec">
        <div class="dev-h">Diagnostics</div>
        <label class="dev-row"><span>Cell footprints</span><input id="dev-debug-cells" type="checkbox"${dbgOn ? " checked" : ""}></label>
        <p class="dev-note">Outline every installed cell (hover a box to name it).</p>
        <label class="dev-row"><span>Tile debugger</span><input id="dev-tiledbg" type="checkbox"${this._tileDbgOn ? " checked" : ""}></label>
        <p class="dev-note">Per-tile overlay: green=rendering, <b style="color:#e53935">red=delivered-but-empty</b>, amber=loading. Click a box for detail.</p>
        <div class="dev-row"><span>Refresh tiles</span><button id="dev-flush" class="btn sm">Re-fetch</button></div>
        <p class="dev-note">Drop MapLibre's loaded tiles and re-request them from the server (e.g. after a rebuild).</p>
      </section>`;
    const q = (id) => el.querySelector("#" + id);
    const rebuild = q("dev-rebuild"); if (rebuild && !rebuild.disabled) rebuild.onclick = (e) => this._rebuildAllPerBand(e.currentTarget);
    q("dev-share").onclick = (e) => this._shareView(e.currentTarget);
    q("dev-inspect").onclick = () => this._setInspectMode(!this._inspectMode);
    const feat = q("dev-feat"); if (!feat.disabled) feat.onclick = (e) => this._copyInspectDebug(e.currentTarget);
    el.querySelectorAll(".dev-band").forEach((cb) => { cb.onchange = () => this._setBandOff(cb.dataset.band, !cb.checked); });
    const dbg = q("dev-debug-cells"); dbg.onchange = () => this._setDebugCells(dbg.checked);
    q("dev-measure").onclick = (e) => this._measureCoverage(e.currentTarget);
    const tiledbg = q("dev-tiledbg"); if (tiledbg) tiledbg.onchange = () => this._setTileDebugger(tiledbg.checked);
    const flush = q("dev-flush"); if (flush) flush.onclick = (e) => this._flushTiles(e.currentTarget);
  }

  // Re-bake every installed NOAA/IENC district into per-band tile sets from the cells
  // ALREADY on the server (no NOAA re-download). The CLIENT supplies each district's
  // cell list (from its catalogue) since the server doesn't track membership. Runs
  // the districts one at a time, surfacing progress through the notification pill.
  // user/import/legacy packs are skipped (no client-known cell list).
  async _rebuildAllPerBand(btn) {
    if (this._taskRunning() || (this._chartLib && this._chartLib.busy)) { if (btn) flashBtn(btn, "busy"); return; }
    let packs = [];
    try { packs = ((await fetch(`${this._assets}api/packs`).then((r) => (r.ok ? r.json() : null))) || {}).packs || []; } catch (e) { /* offline */ }
    // Load the IENC catalogue so we know each installed river pack's cells (the
    // catalogue + ienc-pack grouping live in <chart-library> now).
    if (packs.some((p) => p.name.startsWith("ienc-"))) { try { await (this._chartLib ? this._chartLib._iencCatalog() : Promise.resolve()); } catch (e) { /* skip ienc */ } }
    const iencPacks = (this._chartLib ? this._chartLib._providerPacks("ienc") : null) || [];
    const todo = [];
    for (const p of packs) {
      const m = /^noaa-d(\d+)$/.exec(p.name);
      if (m) { const names = this._districtCellNames(+m[1]); if (names.length) todo.push({ set: p.name, label: this._setLabel(p.name), names }); continue; }
      if (p.name.startsWith("ienc-")) {
        const pk = iencPacks.find((x) => x.key === p.name);
        const names = pk && pk.cells ? pk.cells.map((c) => c.name) : [];
        if (names.length) todo.push({ set: p.name, label: this._setLabel(p.name), names });
      }
    }
    if (!todo.length) { if (btn) flashBtn(btn, "nothing to rebuild"); return; }
    this._task = { kind: "download", status: "running" };
    this._renderDevPanel(); // disable the button while running
    let done = 0;
    for (const j of todo) {
      this._setProgress({ label: "Rebuilding charts", pill: `Rebuilding ${j.label}`, sub: `${done + 1} of ${todo.length} · ${j.names.length} charts`, frac: done / todo.length });
      try {
        const res = await fetch(`${this._assets}api/import?set=${encodeURIComponent(j.set)}&cells=${encodeURIComponent(j.names.join(","))}`, { method: "POST" });
        const job = await res.json().catch(() => ({}));
        if (job.job) await this._pollImport(job.job, (p) => this._setProgress(p), j.label);
      } catch (e) { console.warn("[rebuild]", j.set, e); }
      done++;
    }
    this._task = null;
    this._setProgress(null);
    await this._renderInstalledSets();
    if (this._plotter && this._plotter.flushTiles) { try { await this._plotter.flushTiles(); } catch (e) { /* ignore */ } } // re-fetch the freshly-baked tiles
    if (this._chartLib) this._chartLib.refresh();
    this._renderDevPanel();
    if (btn) flashBtn(btn, `✓ rebuilt ${todo.length}`);
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
        const mod = await import("./components/tile-debugger.mjs");
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
    const pl = this._plotter;
    if (!pl || !pl.flushTiles) return;
    if (btn) flashBtn(btn, "…");
    try {
      await pl.flushTiles();
      console.log("[flush] tile caches cleared, re-baking visible tiles");
      if (btn) flashBtn(btn, "✓");
    } catch (e) { console.warn("[flush]", e); if (btn) flashBtn(btn, "✗"); }
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

  // Turn a usage band off/on. Hides that band's layers instantly (server +
  // per-band pmtiles render one source per band) and persists the choice; the set
  // also gates the realtime baker (minzoom 999) so a later in-browser re-bake skips
  // those cells too. No re-bake needed just to hide.
  _setBandOff(band, off) {
    if (off) this._bandsOff.add(band); else this._bandsOff.delete(band);
    try { localStorage.setItem(LS_BANDS_OFF, JSON.stringify([...this._bandsOff])); } catch {}
    this._persistSettings();
    if (this._plotter) this._plotter.setBandVisible(band, !off);
    this._renderDevPanel();
  }

  // Re-apply persisted band on/off to the plotter once its layers exist (on boot
  // and after any archive (re)load). Idempotent; also seeds the plotter's hidden
  // set so a later style rebuild keeps the bands off.
  _applyBandsOff() {
    if (!this._plotter) return;
    for (const band of DEV_BANDS) this._plotter.setBandVisible(band, !this._bandsOff.has(band));
  }

  // Sample a grid over the viewport: a point where no chart feature renders is a
  // coverage hole. Paints the holes on the map and reports the hole %, plus which
  // installed cells cover the holes but are gated out at the current zoom (so you
  // can see "zoom in past z9 to load these coastal cells"). One-shot (button).
  async _measureCoverage(btn) {
    const map = this._map;
    if (!map) return;
    if (btn) flashBtn(btn, "…");
    const layers = (map.getStyle()?.layers || []).filter((l) => l.source && isChartSource(l.source)).map((l) => l.id);
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
      this._map.getCanvas().style.cursor = "crosshair"; // back to the chart pick cursor
      const src = this._map.getSource("inspect");
      if (src) src.setData({ type: "FeatureCollection", features: [] });
      this._clearInspectFocus();
    }
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
    if (this._radar) this._radar.update(); // packs changed → recompute off-screen pointers
    this._refreshCellUsage();
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
      // Overzoom-down loading is scoped to FOREIGN uploads (cells with no catalog
      // entry, hence no overview/general coverage of their own): they load at ANY
      // zoom so the baker can draw their zoomed-out skeleton (Baker.OverzoomAllBands).
      // Catalog (NOAA) cells keep band-gated loading — with a large installed set
      // (e.g. 1700+ cells) a minzoom of 0 would make every overlapping cell try to
      // load per tile, overwhelming the worker and starving the prebaked fallback
      // (empty tiles → no-data hatch). The overview/general bands already supply
      // the zoomed-out skeleton for catalog charts.
      const isForeign = !this._byName.has(name);
      let minzoom;
      if (solo) minzoom = this._renderSel.has(name) ? 0 : 999;
      else if (this._bandsOff.has(band)) minzoom = 999;
      else minzoom = isForeign ? 0 : (BAND_MINZOOM[band] || 0);
      meta.set(name, { bb, minzoom });
    }
    return meta;
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
          properties: { name, band, status: this._cellStatus.get(name) || "queued" },
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

  // -- search: catalog (places/charts) + loaded chart feature data ---------
  doSearch(q) {
    const el = this.shadowRoot.getElementById("search-results");
    if (!el) return;
    const needle = q.trim().toLowerCase();
    if (needle.length < 2) { el.hidden = true; el.innerHTML = ""; this._searchHits = []; this._positionSearch(); return; }
    // 1) Catalog cells (chart titles / numbers), fuzzy-matched. Best score wins;
    // ties break to the coarser chart (overview before an arbitrary harbour inset).
    const cells = [];
    for (const c of this._catalog) {
      if (!Array.isArray(c.bb) || c.bb.length !== 4) continue;
      const score = Math.max(fuzzyScore(needle, (c.l || "").toLowerCase()), fuzzyScore(needle, c.n.toLowerCase()));
      if (score >= 0) cells.push({ c, score });
    }
    cells.sort((a, b) => (b.score - a.score) || ((b.c.s || 0) - (a.c.s || 0)));
    // 2) Every loaded chart feature, fuzzy-matched across its attribute data.
    const feats = this._searchFeatures(needle);
    const hits = [...cells.slice(0, 5).map(({ c }) => ({ type: "cell", c })), ...feats.slice(0, 8)];
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
    this._positionSearch(); // re-align to the search tab as the result count changes the height
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
          // Score the name/type strongly; also fuzzy-match the rest of the attribute
          // data (lower weight) so "search all feature data" still works.
          let score = Math.max(fuzzyScore(needle, objnam.toLowerCase()), fuzzyScore(needle, typeName.toLowerCase()), fuzzyScore(needle, cls.toLowerCase()));
          if (score < 0) for (const k in p) { const v = p[k]; if (typeof v === "string") { const s = fuzzyScore(needle, v.toLowerCase()); if (s >= 0) { score = Math.max(score, s - 6); break; } } }
          if (score < 0) continue;
          const co = firstCoord(f.geometry); if (!co) continue;
          const key = cls + "|" + objnam + "|" + co[0].toFixed(3) + "," + co[1].toFixed(3);
          if (seen.has(key)) continue; seen.add(key);
          out.push({ type: "feat", score, label: objnam || typeName, sub: objnam ? typeName : (p.cell ? `▦ ${p.cell}` : typeName), lng: co[0], lat: co[1] });
        }
      }
    }
    out.sort((a, b) => (b.score - a.score) || a.label.localeCompare(b.label)); // best matches first
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

  // Frame the map to the combined extent of the given catalog cells.
  // The minimum zoom at which a cell actually renders — its band's start zoom
  // (large-scale harbour/berthing cells only bake at z13/16+, so fitting their
  // small footprint at ~z14 would show nothing).
  _cellTargetZoom(name) {
    const c = this._byName.get(name);
    const scale = (c && typeof c.s === "number" && c.s) || this._cellScale.get(name) || 0;
    return scale ? (BAND_MINZOOM[bandForScale(scale)] || 0) : 0;
  }

  _frameCells(names) {
    let W = Infinity, S = Infinity, E = -Infinity, N = -Infinity, any = false, need = 0;
    for (const n of names) {
      const bb = this._cellLocation(n); // catalog bbox OR baker/parsed bounds (foreign cells)
      if (bb) {
        W = Math.min(W, bb[0]); S = Math.min(S, bb[1]); E = Math.max(E, bb[2]); N = Math.max(N, bb[3]); any = true;
        need = Math.max(need, this._cellTargetZoom(n)); // zoom in enough to actually render
      }
    }
    if (!any || !this._map) return;
    const cam = this._map.cameraForBounds([[W, S], [E, N]], { padding: 60 });
    const zoom = Math.min(18, Math.max(cam ? cam.zoom : 12, need ? need + 0.5 : 0));
    this._map.easeTo({ center: cam ? cam.center : [(W + E) / 2, (S + N) / 2], zoom, duration: 800 });
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
    r.innerHTML = `
      <style>
        :host { display:block; position:relative; width:100%; height:100%; font:13px/1.4 system-ui,sans-serif;
          /* The map is the UI: it fills the whole element. All chrome floats over
             it — four round buttons in the corners and one data card at the bottom
             centre. Panels drop down from their corner button as caret popovers. */
          --botbar-h:env(safe-area-inset-bottom,0px);
          --ui-bg:#fafafa; --ui-surface:#fff; --ui-surface-2:#eef1f4; --ui-text:#2a2f35; --ui-text-dim:#7a828b; --ui-text-faint:#9aa0a8; --ui-border:#e2e2e2; --ui-border-2:#ededed; --ui-border-strong:#cfcfcf; --ui-hover:#f0f3f6; --ui-accent:#1565c0; --ui-accent-hover:#1257a8; --ui-accent-text:#fff; --ui-shadow:rgba(0,0,0,.2); }
        :host([data-scheme="dusk"]) {
          --ui-bg:#20262b; --ui-surface:#2a3137; --ui-surface-2:#333b42; --ui-text:#cdd6dc; --ui-text-dim:#9aa6ae; --ui-text-faint:#7d8990; --ui-border:#3a434a; --ui-border-2:#333b42; --ui-border-strong:#4a555d; --ui-hover:#353f47; --ui-accent:#4f9be6; --ui-accent-hover:#69abe9; --ui-accent-text:#0c1318; --ui-shadow:rgba(0,0,0,.5); }
        :host([data-scheme="night"]) {
          --ui-bg:#14181b; --ui-surface:#1b2024; --ui-surface-2:#232a2f; --ui-text:#aeb8be; --ui-text-dim:#7e898f; --ui-text-faint:#626c72; --ui-border:#2a3137; --ui-border-2:#232a2f; --ui-border-strong:#38424a; --ui-hover:#232a30; --ui-accent:#3f7fb5; --ui-accent-hover:#4d8cc2; --ui-accent-text:#0a0e11; --ui-shadow:rgba(0,0,0,.6); }
        /* Full-bleed map; everything else floats over it. */
        #map { position:absolute; inset:0; }
        #map chart-canvas { width:100%; height:100%; }
        /* Chart radar: edge chips pointing at off-screen installed charts. The
           overlay is click-through; chips opt back into pointer events. */
        #chart-radar { position:absolute; inset:0; z-index:5; pointer-events:none; overflow:hidden; }
        .radar-chip { position:absolute; transform:translate(-50%,-50%); display:flex; align-items:center; gap:6px;
          padding:5px 9px 5px 7px; border-radius:999px; background:var(--ui-surface); color:var(--ui-text);
          border:1px solid var(--ui-border-strong); box-shadow:0 2px 8px var(--ui-shadow); cursor:pointer;
          font:600 12px/1 system-ui,sans-serif; white-space:nowrap; pointer-events:auto; user-select:none;
          max-width:42vw; transition:background .1s; }
        .radar-chip:hover { background:var(--ui-hover); }
        .radar-chip .rc-arrow { flex:none; width:14px; height:14px; color:var(--ui-accent); }
        .radar-chip .rc-band { flex:none; width:8px; height:8px; border-radius:50%; box-shadow:0 0 0 1.5px var(--ui-surface); }
        .radar-chip .rc-name { overflow:hidden; text-overflow:ellipsis; }
        .radar-chip .rc-dist { flex:none; color:var(--ui-text-dim); font-weight:500; }
        .btn { cursor:pointer; border:1px solid var(--ui-border-strong); background:var(--ui-surface); border-radius:6px; padding:6px 10px; font:inherit; color:var(--ui-text); }
        .btn:hover { background:var(--ui-hover); }
        /* Floating corner controls — a top-left group (search) and a top-right
           group (charts · scheme · settings). Each is a round button; the active
           section's button lights up while its panel is open. */
        #tl-controls, #tr-controls { position:absolute; top:calc(12px + env(safe-area-inset-top,0px)); z-index:7;
          display:flex; align-items:center; gap:8px; }
        #tl-controls { left:calc(12px + env(safe-area-inset-left,0px)); }
        #tr-controls { right:calc(12px + env(safe-area-inset-right,0px)); }
        .rbtn { flex:none; width:44px; height:44px; border-radius:50%; cursor:pointer; padding:0;
          display:flex; align-items:center; justify-content:center; color:var(--ui-text);
          background:color-mix(in srgb, var(--ui-surface) 90%, transparent); border:1px solid var(--ui-border);
          box-shadow:0 2px 10px rgba(0,0,0,.18); backdrop-filter:blur(6px);
          transition:background .12s, color .12s, box-shadow .12s, transform .08s; }
        .rbtn:hover { color:var(--ui-accent); border-color:var(--ui-accent); box-shadow:0 3px 14px rgba(0,0,0,.24); }
        .rbtn:active { transform:scale(.94); }
        .rbtn.on { background:var(--ui-accent); color:var(--ui-accent-text); border-color:var(--ui-accent); }
        .rbtn svg { width:21px; height:21px; display:block; }
        /* Prod / prebaked deployment: charts load from a configured hosted archive
           (pmtiles="…" / catalog="…"); there's no NOAA download and no Dev tools.
           The Charts button stays but its panel becomes import-only (drop your own
           ENC, baked server-side) — see renderCharts. */
        :host([prod]) #empty-add, :host([prod]) #empty .welcome-sub { display:none; }
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
        /* Job activity is signalled by the top notification pill; the dlspin
           keyframes drive its spinner. */
        @keyframes dlspin { to { transform:rotate(360deg); } }
        /* The ECDIS cursor-pick report (S-52 PresLib §10.8) is its own element,
           <pick-report> — see pick-report.mjs (styled via the inherited --ui-* tokens). */
        /* chart info pill (map popup when focusing a chart from the list) */
        .chart-pill { font:13px/1.4 system-ui,sans-serif; min-width:170px; }
        .chart-pill .cp-title { font-weight:600; margin-bottom:2px; }
        .chart-pill .cp-meta { color:var(--ui-text-dim); font-size:12px; }
        .chart-pill .cp-ed { margin-top:5px; display:flex; align-items:center; gap:6px; flex-wrap:wrap; font-size:12px; color:var(--ui-text-dim); }
        /* settings */
        .set-section { margin:0 0 28px; }
        .set-section > h3 { font-size:11px; text-transform:uppercase; letter-spacing:.05em; color:var(--ui-text-faint); margin:0 0 6px; padding-bottom:6px; border-bottom:1px solid var(--ui-border-2); font-weight:700; }
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
        /* The settings-row + control look (.set-row/.switch/.seg/.unit) moved into
           <settings-dialog>'s shadow (settings-dialog.view.mjs STYLE). */
        /* NOAA attribution + "not for navigation" — subtle one-line text tucked
           into the bottom-right corner (no box), kept legible over the chart with a
           soft halo in the current surface colour. */
        #noaa-attr { position:absolute; right:calc(12px + env(safe-area-inset-right,0px)); bottom:calc(var(--botbar-h) + 10px); z-index:5; pointer-events:auto;
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
        .csp-row.csp-loc { cursor:pointer; margin:0 -6px; padding-left:6px; padding-right:6px; border-radius:7px; }
        .csp-row.csp-loc:hover { background:var(--ui-hover); }
        .csp-row.is-fail { align-items:flex-start; }
        .csp-dot { width:9px; height:9px; border-radius:50%; flex:none; margin-top:3px; box-shadow:0 0 0 1.5px rgba(255,255,255,.6); }
        .csp-name { flex:1; min-width:0; display:flex; flex-direction:column; gap:2px; color:var(--ui-text); }
        .csp-title { overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
        .csp-code { font-size:11px; color:var(--ui-text-dim); font-variant-numeric:tabular-nums; letter-spacing:.02em; }
        .csp-err { font:500 10.5px/1.35 system-ui,sans-serif; color:#cf3b3b; white-space:normal; word-break:break-word; }
        .csp-stat { flex:none; font-weight:600; font-size:11px; }
        .csp-queued { color:#9aa7b4; } .csp-loading { color:#d9892b; } .csp-ready { color:#2e9b57; } .csp-failed { color:#cf3b3b; }
        .csp-empty { color:var(--ui-text-dim); font:500 12px/1.2 system-ui,sans-serif; padding:8px 0; }
        /* Bottom-centre DATA CARD — adopts the surface look of the old sidebar: a
           solid rounded card pinned to the bottom middle. Holds ONLY the live nav
           readout (band · scale · zoom · position). Purely presentational — no
           buttons, no transient status (activity lives in the notification pill). */
        #databox { position:absolute; left:50%; bottom:calc(var(--botbar-h) + 14px);
          transform:translateX(-50%); z-index:6; box-sizing:border-box;
          display:flex; flex-direction:column; align-items:center; gap:6px; padding:8px 14px;
          width:min(94vw, 420px);
          background:color-mix(in srgb, var(--ui-surface) 92%, transparent); border:1px solid var(--ui-border);
          border-radius:13px; backdrop-filter:blur(7px); overflow:hidden;
          box-shadow:0 4px 18px rgba(0,0,0,.18);
          font:11px system-ui,sans-serif; color:var(--ui-text); }
        #databox[hidden] { display:none; }
        /* Live band·scale·zoom·position readout — fixed-width fields + tabular
           figures so panning/zooming never reflows the card. The card width is
           FIXED (above) so it never grows/shrinks as the message changes; the
           overscale chip wraps to its own centred line rather than widening it. */
        .db-readout { display:flex; align-items:center; width:100%; justify-content:center; }
        .db-readout .hud-main { display:flex; align-items:center; justify-content:center; flex-wrap:wrap; gap:6px; row-gap:5px;
          font-weight:600; font-size:12px; white-space:nowrap; font-variant-numeric:tabular-nums; }
        .db-readout .hud-dot { width:8px; height:8px; border-radius:50%; flex:none; box-shadow:0 0 0 2px rgba(255,255,255,.6); margin-right:1px; }
        .db-readout .hud-band { display:inline-block; width:56px; }
        .db-readout .hud-scale { display:inline-block; width:74px; color:var(--ui-accent); }
        .db-readout .hud-z { display:inline-block; width:40px; color:var(--ui-text-dim); }
        .db-readout .hud-coord { display:inline-block; width:150px; color:var(--ui-text-dim); }
        .db-readout .hud-sep { color:var(--ui-text-faint); }
        /* Overscale indication (S-52 §10.1.10.1) — a full-width amber band filling
           the bottom of the card (its own warning notification area). Negative
           margins cancel the card's padding so it reaches the rounded edges; the
           card's overflow:hidden clips it to the bottom corners. */
        .db-warn { box-sizing:border-box; width:calc(100% + 28px); margin:1px -14px -8px; padding:5px 12px;
          display:flex; align-items:center; justify-content:center; gap:5px;
          background:#e8820c; color:#fff; font:700 11.5px/1.25 system-ui,sans-serif; text-align:center; }
        .db-warn[hidden] { display:none; }
        .db-warn-ico { width:14px; height:14px; flex:none; }
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
        /* Panels are dialog popovers that DROP DOWN from their corner button, with a
           caret on the top edge pointing back up at it. The Charts/Settings panel
           anchors top-right; search anchors top-left. The caret's horizontal offset
           (--caret-left) is set in JS to the originating button's centre. Pops in
           with a fade+scale from the caret edge; fully hidden when closed. */
        /* --panel-bottom: viewport-bottom space reserved for the data card so a
           panel never covers it (the card is bottom-centre, panels drop from the
           top — capping their height keeps the two from overlapping). */
        #drawer, #search { --caret:9px; --ctrl-top:calc(64px + env(safe-area-inset-top,0px));
          --panel-bottom:calc(var(--botbar-h) + 92px); }
        /* NB: no overflow:hidden on the popover itself — it would clip the caret.
           Inner scroll areas (.body / #search-results) round their own corners. */
        #drawer { position:absolute; right:calc(12px + env(safe-area-inset-right,0px)); top:var(--ctrl-top);
          width:min(440px, calc(100vw - 24px)); max-height:calc(100vh - var(--ctrl-top) - var(--panel-bottom)); z-index:6;
          background:var(--ui-bg); color:var(--ui-text); border:1px solid var(--ui-border); border-radius:14px;
          box-shadow:0 12px 38px rgba(0,0,0,.30); display:flex; flex-direction:column;
          transform-origin:top right; transform:translateY(-6px) scale(.97); opacity:0; visibility:hidden;
          transition:opacity .15s ease, transform .15s ease, visibility 0s linear .15s; }
        #drawer.open { opacity:1; transform:none; visibility:visible; transition:opacity .15s ease, transform .15s ease; }
        #drawer.wide { width:min(86vw, 940px); } /* charts: two-pane list + map */
        #drawer.set-wide { width:min(520px, calc(100vw - 24px)); } /* settings: rail + content */
        #drawer.wide .miller { height:calc(100vh - var(--ctrl-top) - var(--panel-bottom) - 118px); max-height:none; }
        #drawer .body { border-radius:0 0 13px 13px; }
        /* caret on the TOP edge, pointing up at the button above */
        #drawer::after, #search::after { content:""; position:absolute; top:calc(-1 * var(--caret)); left:var(--caret-left,50%); transform:translateX(-50%);
          width:0; height:0; border-left:var(--caret) solid transparent; border-right:var(--caret) solid transparent;
          border-bottom:var(--caret) solid var(--ui-bg); filter:drop-shadow(0 -2px 1px rgba(0,0,0,.08)); }
        #search::after { border-bottom-color:var(--ui-surface); }
        /* The settings panel + its control look (toggle/segmented/number/select)
           now live in <settings-dialog> (settings-dialog.view.mjs STYLE). The shell
           only keeps the developer-tools chrome below (dev panel + inspector),
           which option B renders in the shell shadow's #dev-region. */
        /* Dev tools live behind a button at the very top of Settings (spans all
           columns). The Dev panel itself opens as the inspect section with a back
           link to Settings. */
        .set-section.dev-entry { grid-column:1/-1; margin:0 0 14px; }
        .dev-open { display:flex; align-items:center; gap:9px; font-weight:600; padding:10px 12px; }
        .dev-open svg { width:17px; height:17px; flex:none; }
        .dev-open .dev-open-chev { margin-left:auto; color:var(--ui-text-faint); }
        .dev-back { display:inline-flex; align-items:center; gap:4px; margin:0 0 8px -4px; padding:4px 8px 4px 4px;
          border:none; background:none; color:var(--ui-accent); cursor:pointer; font:600 13px system-ui,sans-serif; border-radius:7px; }
        .dev-back:hover { background:var(--ui-hover); }
        .dev-back svg { width:16px; height:16px; }
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
        .body { overflow:auto; padding:14px 16px; flex:1; }
        .panel { display:none; } .panel.sel { display:block; }
        .drop { border:2px dashed var(--ui-border-strong); border-radius:8px; padding:18px; text-align:center; color:var(--ui-text-dim); margin-bottom:10px; }
        .drop.over { border-color:var(--ui-accent); background:var(--ui-hover); color:var(--ui-accent); }
        .row { display:flex; align-items:center; gap:8px; padding:4px 0; border-bottom:1px solid var(--ui-border-2); }
        .row .name { font-weight:600; } .row .meta { color:var(--ui-text-dim); font-size:12px; }
        .grow { flex:1; }
        .muted { color:var(--ui-text-dim); }
        /* Dev panel: clearly separated sections, most-used at top, roomy spacing. */
        .dev-tools { display:flex; flex-direction:column; }
        #dev-region .dev-tools { border-top:1px solid var(--ui-border-2); margin-top:8px; }
        .dev-sec { display:flex; flex-direction:column; gap:8px; padding:16px 0; border-top:1px solid var(--ui-border); }
        .dev-sec:first-child { padding-top:14px; border-top:none; }
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
        /* Top-centre notification pill — non-blocking "work happening" indicator.
           Hidden unless a job is active; "lights up" (pulsing ring + spinner) while
           busy; click drops a detail panel. */
        /* Job-progress row inside the bottom status card — sits ABOVE the nav
           readout, separated by a hairline divider. One width with the card so the
           label can run full-width; the percentage is pinned right. */
        .db-prog { width:100%; box-sizing:border-box; display:flex; flex-direction:column; gap:6px;
          padding-bottom:7px; margin-bottom:1px; border-bottom:1px solid var(--ui-border); }
        .db-prog[hidden] { display:none; }
        .db-prog-head { display:flex; align-items:center; gap:8px; font:600 12px/1.2 system-ui,sans-serif; color:var(--ui-text); }
        .db-prog-label { flex:1; min-width:0; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
        .db-prog-pct { flex:none; font-weight:700; color:var(--ui-accent); font-variant-numeric:tabular-nums; }
        .db-prog.error .db-prog-pct { color:#c0392b; }
        .db-prog.error .db-prog-label { color:#c0392b; }
        /* Spinner shows only while actively working (not on the done/error frame). */
        .db-prog-spin { width:13px; height:13px; flex:none; border-radius:50%; display:none;
          border:2px solid color-mix(in srgb, var(--ui-accent) 30%, transparent); border-top-color:var(--ui-accent);
          animation:dlspin .8s linear infinite; }
        .db-prog.busy .db-prog-spin { display:inline-block; }
        .db-prog-track { position:relative; width:100%; height:5px; border-radius:3px; overflow:hidden; background:var(--ui-surface-2); }
        .db-prog-fill { position:absolute; left:0; top:0; bottom:0; width:0; border-radius:3px; background:var(--ui-accent); transition:width .25s ease; }
        .db-prog.error .db-prog-fill { background:#c0392b; }
        /* Indeterminate (no known fraction): a sweeping segment instead of a fill. */
        .db-prog-fill.indet { width:35% !important; animation:db-sweep 1.1s ease-in-out infinite; }
        @keyframes db-sweep { 0% { left:-35%; } 100% { left:100%; } }
        @media (prefers-reduced-motion: reduce) { .db-prog-spin { animation-duration:2s; } .db-prog-fill.indet { animation:none; left:0; width:100% !important; } }
        /* NotificationCenter banners: a bottom-stacked toast list (non-task messages). */
        #toasts { position:absolute; left:50%; bottom:calc(var(--botbar-h) + 14px); transform:translateX(-50%); z-index:9; display:flex; flex-direction:column; gap:8px; align-items:center; pointer-events:none; }
        .toast { pointer-events:auto; max-width:80vw; padding:9px 14px; border-radius:8px; font:600 12.5px/1.3 system-ui,sans-serif; color:var(--ui-text); background:var(--ui-surface); border:1px solid var(--ui-border-2); box-shadow:0 4px 16px rgba(0,0,0,.28); opacity:1; transition:opacity .3s ease, transform .3s ease; }
        .toast.warn { border-color:#c0922f; } .toast.error { border-color:#c0392b; color:#e06b5c; }
        .toast.out { opacity:0; transform:translateY(6px); }
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
        #search[hidden] { display:block; } /* defeat UA hidden so the popover can fade out (base styles keep it invisible/non-interactive) */
        /* Search: same caret-popover as the panels — a dialog card with the input
           on top and results filling in underneath, dropping from the search button
           at the top-left. */
        #search { position:absolute; left:calc(12px + env(safe-area-inset-left,0px)); right:auto; top:var(--ctrl-top); z-index:8; width:min(360px, calc(100vw - 24px));
          background:var(--ui-surface); border:1px solid var(--ui-border); border-radius:14px;
          box-shadow:0 12px 38px rgba(0,0,0,.30);
          transform-origin:top left; transform:translateY(-6px) scale(.97); opacity:0; visibility:hidden;
          transition:opacity .15s ease, transform .15s ease, visibility 0s linear .15s; }
        #search:not([hidden]) { opacity:1; transform:none; visibility:visible; transition:opacity .15s ease, transform .15s ease; }
        #search input { width:100%; box-sizing:border-box; border:none; border-radius:14px; padding:11px 16px;
          font:inherit; background:transparent; color:var(--ui-text); outline:none; }
        #search-results { border-top:1px solid var(--ui-border-2); max-height:min(360px, calc(100vh - var(--ctrl-top) - var(--panel-bottom) - 52px)); overflow-y:auto; border-radius:0 0 13px 13px; }
        #search-results[hidden] { display:none; }
        .sr-item { padding:8px 16px; cursor:pointer; border-bottom:1px solid var(--ui-border-2); }
        .sr-item:last-child { border-bottom:none; }
        .sr-item:hover, .sr-item.sel { background:var(--ui-hover); }
        .sr-item .t { font-weight:600; } .sr-item .s { color:var(--ui-text-faint); font-size:12px; }
        /* Subtle "loading more while data is shown" cue: a hairline indeterminate
           bar riding the top edge of the viewport. Opacity-controlled (always in
           DOM) so it fades in/out; the slide animation runs continuously. */
        .load-bar { position:absolute; top:0; left:0; right:0; height:3px; z-index:25; pointer-events:none; overflow:hidden;
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
           Chart packs go one-per-row. (Settings rows wrap their control inside
           <settings-dialog>'s own responsive STYLE now.) */
        @media (max-width: 640px) {
          #empty .card { max-width:min(360px, calc(100vw - 48px)); }
          .pack-grid { grid-template-columns:1fr; }
        }
        /* On a narrow phone, drop the zoom from the readout so the band·scale·
           position line never runs past the card edge (scale is what matters). */
        @media (max-width: 430px) {
          .db-readout .hud-z, .db-readout .hud-scale + .hud-sep { display:none; }
        }
      </style>
      <div id="map"></div>
      <!-- Off-screen installed-chart pointers ("chart radar"): edge chips pointing
           toward enabled chart packs that aren't currently in view. Overlay is
           click-through; only the chips themselves take pointer events. -->
      <div id="chart-radar" aria-hidden="true"></div>
      <div id="load-bar" class="load-bar" aria-hidden="true"></div>
      <!-- The map is the UI. Chrome is reduced to four round buttons floating in
           the corners — search alone top-left; charts · scheme · settings top-right
           — plus a read-only data card pinned to the bottom centre. -->
      <div id="tl-controls" class="ctrl-group">
        <button class="rbtn" id="search-tab" type="button" title="Search charts &amp; features" aria-label="Search">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round"><circle cx="11" cy="11" r="7"/><path d="m20 20-3.5-3.5"/></svg>
        </button>
      </div>
      <div id="tr-controls" class="ctrl-group">
        <button class="rbtn" id="charts-btn" type="button" title="Get &amp; manage charts" aria-label="Charts">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="M12 3 3 7.5l9 4.5 9-4.5L12 3Z"/><path d="M3 12l9 4.5L21 12"/><path d="M3 16.5 12 21l9-4.5"/></svg>
        </button>
        <button class="rbtn" id="scheme-toggle" type="button" title="Colour scheme — tap to cycle Day · Dusk · Night" aria-label="Colour scheme">
          <svg id="scheme-svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"></svg>
        </button>
        <button class="rbtn" id="settings-btn" type="button" title="Settings" aria-label="Settings">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1Z"/></svg>
        </button>
      </div>
      <!-- NotificationCenter banner stack (non-task messages: failures, alerts). -->
      <div id="toasts"></div>
      <!-- Bottom-centre status card — the SINGLE surface for both the live nav
           readout (band · scale · zoom · position · overscale, always shown) and
           job progress (download / import / bake), which grows a row above the
           readout while a job runs. Driven by _updateHud + _setNotification. -->
      <div id="databox" hidden>
        <div id="db-prog" class="db-prog" hidden>
          <div class="db-prog-head">
            <span class="db-prog-spin"></span>
            <span id="db-prog-label" class="db-prog-label"></span>
            <span id="db-prog-pct" class="db-prog-pct"></span>
          </div>
          <div class="db-prog-track"><span id="db-prog-fill" class="db-prog-fill"></span></div>
        </div>
        <span id="cov-readout" class="db-readout"></span>
        <div id="db-warn" class="db-warn" hidden></div>
      </div>
      <div id="search" hidden><input id="search-input" type="search" placeholder="Search charts & features…" autocomplete="off" spellcheck="false"><div id="search-results" hidden></div></div>
      <div id="noaa-attr"><a href="${NOAA_ENC_URL}" target="_blank" rel="noopener">NOAA ENC®</a> · <button id="attr-terms" class="attr-link" type="button">Terms</button> · not for navigation</div>
      <!-- The NOAA ENC User Agreement modal moved into <chart-library> (it owns the
           download flow); the "Terms" link reaches into it. -->
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
            <!-- Job progress lives in the bottom data card + top notification pill,
                 not in the drawer. The whole charts UI is <chart-library>. -->
            <chart-library id="chart-lib"></chart-library>
          </div>
          <div class="panel" data-panel="settings">
            <settings-dialog id="settings-dlg"></settings-dialog>
            <!-- Developer tools (option B): rendered in the SHELL shadow because
                 _renderDevPanel / _renderInspect reach into it by id. Shown only
                 when the dialog's active tab is Advanced (and not in prod). -->
            <div id="dev-region" hidden></div>
          </div>
        </div>
      </div>`;

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
    // Esc dismisses the debug context menu, else exits the feature inspector.
    // Escape closes the topmost open dialog/overlay (one per press). The
    // cursor-pick report closes itself (its own captured handler runs first).
    window.addEventListener("keydown", (e) => {
      if (e.key !== "Escape") return;
      const root = this.shadowRoot;
      const ctx = root.getElementById("ctx-menu");
      if (ctx && !ctx.hidden) { this._hideContextMenu(); return; }
      // The NOAA agreement modal lives in <chart-library>; cancel it if open.
      if (this._chartLib && this._chartLib.agreementOpen) { this._chartLib._resolveAgreement(false); return; }
      if (this._cellPopOpen) { this._toggleCellStatusPopup(); return; }
      const search = root.getElementById("search");
      if (search && !search.hidden) { search.hidden = true; root.getElementById("search-tab").classList.remove("on"); return; }
      if (this._drawerOpen()) { this.closeDrawer(); return; }
    });
    $("empty-add").onclick = () => this.openCharts();
    $("empty-import").onclick = () => this.openCharts("user");
    // Attribution "Terms" link → the agreement modal owned by <chart-library>.
    $("attr-terms").onclick = () => { if (this._chartLib) this._chartLib._showAgreement(); };

    // Search (offline, over catalog titles + loaded chart feature data). The nav
    // button toggles a tiny flyout with the input + results.
    const si = $("search-input");
    const closeSearch = () => { $("search").hidden = true; $("search-tab").classList.remove("on"); };
    const openSearch = () => { $("search").hidden = false; $("search-tab").classList.add("on"); this._positionSearch(); si.focus(); };
    $("search-tab").onclick = () => ($("search").hidden ? openSearch() : closeSearch());
    si.oninput = () => this.doSearch(si.value);
    si.onkeydown = (e) => {
      if (e.key === "Enter") this.gotoSearchHit(0);
      else if (e.key === "Escape") { si.value = ""; closeSearch(); }
    };
    si.onfocus = () => { if (si.value.trim().length >= 2) this.doSearch(si.value); };
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
    // tab. Any section switch disarms it (and clears dev hole markers).
    this._setInspectMode(false);
    if (name === "settings") {
      this._clearDevHoles();
      if (this._settingsDlg) this._settingsDlg.show(this._settingsDlg.activeTab || "general");
      this._syncDevRegion(); // reveal dev tools if it reopened on the Advanced tab
    }
    this.setDrawerOpen(true);
  }

  // The developer tools live inline in Settings → Advanced. They're "visible"
  // (and worth re-rendering on band/coverage/inspect changes) when that drawer is
  // open on the Advanced tab. _renderDevPanel/_renderInspect also no-op safely when
  // their containers are absent, so this is just an optimisation + correctness gate.
  _devVisible() {
    return this._drawerOpen() && this._section === "settings"
      && !!(this._settingsDlg && this._settingsDlg.activeTab === "advanced");
  }

  // Reveal / tear down the developer-tools region (option B). The dev tools render
  // in the SHELL shadow (#dev-region) — not the dialog's — because _renderDevPanel /
  // _renderInspect reach into the shell shadow by id. We mount the #inspect-body +
  // #dev-tools containers here when the dialog's active tab is Advanced (and not
  // prod), then let the existing renderers fill them; otherwise the region is empty
  // + hidden so its content doesn't linger under other tabs.
  _syncDevRegion() {
    const region = this.shadowRoot.getElementById("dev-region");
    if (!region) return;
    const show = !this._prod && this._devVisible();
    region.hidden = !show;
    if (!show) { region.innerHTML = ""; return; }
    // Build the containers once; keep them across re-syncs so renderers can target
    // them and so re-render hooks don't thrash the DOM.
    if (!region.querySelector("#dev-tools")) {
      region.innerHTML = `<div id="inspect-body" class="ins-body"></div><div id="dev-tools" class="dev-tools"></div>`;
    }
    this._renderInspect();
    this._renderDevPanel();
  }

  // Home: the full-screen chart viewer — drop any selection overlay/section and
  // show just the map.
  goHome() {
    this._cancelAreaSelect();
    this.closeDrawer();
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

  // The search flyout grows with results; since it's anchored from the top its
  // height can change freely without re-aligning. Just re-point the caret at the
  // search button (called on open and after each query).
  _positionSearch() {
    const r = this.shadowRoot;
    const pop = r.getElementById("search"), tab = r.getElementById("search-tab");
    if (!pop || pop.hidden || !tab) return;
    this._positionCaret(pop, tab);
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
    if (!open) { this._cancelAreaSelect(); this._clearFocus(); this._setInspectMode(false); }
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
