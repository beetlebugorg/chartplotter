// <chart-canvas> — the public web component.
//
// A self-contained S-52 ENC chart plotter: a MapLibre map whose vector tiles are
// baked SERVER-SIDE and served from /tiles/{set} (or read from a hosted .pmtiles).
// Drop the tag on a page:
//
//   <script type="module" src="chart-canvas/chart-canvas.mjs"></script>
//   <chart-canvas center="-76.4875,38.975" zoom="13" tiles="server" set="charts"></chart-canvas>
//
// Attributes (all optional):
//   center   "lon,lat"            initial view centre (default Annapolis)
//   zoom     number               initial zoom (default 13)
//   tiles    "server"             pull MVT from the Go server's /tiles/{set}
//   set      name                 the server tile-set name (the {set} in the URL)
//   pmtiles  URL                  instead: render a hosted prebaked .pmtiles
//   assets   base URL             where the generated assets live (default "./":
//                                 colortables.json, sprite.*, linestyles.json,
//                                 patterns.*, glyphs/, and the /tiles base)
//   basemap  "coastline" | "osm" | "osmvec" | "none"   underlay (default none;
//                                 "none" renders nothing — useful for test charts)
//
// The full S-52 style (areas, patterns, lines, complex lines, point symbols,
// soundings, text) is assembled client-side from the baker's JSON assets, the
// same way the dev index.html does — colour is never baked, so Day/Dusk/Night
// is a restyle. This module supersedes index.html as the shipped surface.
//
// ── PUBLIC INTERFACE (the stable contract the shell + plugins build on) ──────
// This widget is the BASE layer of the app. Everything else — the chart
// downloader, the cursor-pick report, and overlay plugins (own-ship, AIS) — is a
// separate component that talks to it ONLY through the surface below. Nothing
// reaches into private (`_`) fields or MapLibre internals; see specs/web-architecture.md.
//
//   Charts:        setServerSets (server tiles) · setArchive · addArchive ·
//                  addArchives · loadRegions · replaceBand · loadArchiveUrl
//   Display:       setScheme · setMariner · setBasemap
//   Tiles:         refresh · flushTiles
//   Overlays:      get map · overlayBeforeId · addOverlayLayer · removeOverlay
//   Camera:        setCameraMode · updateFollow · clearFollow
//   Events:        ready{map}
//
// A plugin = a small element/module that, on the `ready` event, takes the `map`
// handle, adds its own namespaced source + layers (via addOverlayLayer), runs its
// own data loop, and (for tracking) drives the camera via updateFollow.

// Tiles are baked SERVER-SIDE. Two render sources are supported:
//   • server  (tiles="server" set="<name>") — MVT pulled live from the Go server
//     at /tiles/{set}/{z}/{x}/{y}.mvt (POST /api/import bakes + registers a set).
//   • prebaked (pmtiles="<url>" / setArchive / loadRegions) — a hosted .pmtiles
//     read by HTTP Range, the serverless static-CDN option. No tile server.
// Baking runs server-side; the client only renders tiles.
import { PMTilesArchive, registerPmtilesProtocol } from "./pmtiles-source.mjs";
import { convertDistance, unitSuffix } from "../lib/units.mjs";
import { zoomForScale, scaleDenomPhysical, DEFAULT_PX_PITCH_MM, clampPxPitch } from "../lib/util.mjs"; // shared scale↔zoom (512-tile MapLibre resolution)
import * as S52 from "./s52-style.mjs";
import { SpriteBuilder } from "./sprite-builder.mjs";
// Chart SOURCE / ARCHIVE management lives in its own stateful collaborator now (the
// server-tiles mode, per-band prebaked archives, cache-bust token + SCAMIN-bucket
// discovery, and the public chart-source API). The element delegates to it and
// re-imports the band/SCAMIN consts it still needs (expandChartLayers / buildStyle).
import {
  ChartSources, CHART_BANDS, BAND_SLUGS, bandOfSet,
} from "./chart-sources.mjs";
// The MapLibre chart layer/style BUILDING is a PURE function in its own module now
// (chart-style.mjs): buildChartLayers(state) returns the expanded layers plus the
// three bookkeeping maps (_layerBase/_variants/_layerVis) the live updaters below
// read. PAT_PREFIX (the fill-pattern image namespace) is homed there too — used by
// the layer builder AND this element's registerPattern — and imported back here.
import { buildChartLayers, PAT_PREFIX } from "./chart-style.mjs";

const FEATURE_SCALE = 0.01 / 0.26458;
// The baker emits feature pixel sizes (icon `scale`, `width_px`, `font_size_px`,
// pattern raster) at the 1/96-inch CSS reference pixel = 0.26458 mm — the SAME
// reference as portrayal.DefaultPxPerSymbolUnit (0.01/0.26458). To render at TRUE
// physical size we multiply every size by 0.26458/pxPitch (see _scaleSizes in
// chart-style.mjs / _featureSizeScale below): on a screen whose real CSS-pixel pitch
// is 0.26458 mm that is 1×, and the Calibration panel sets pxPitch from a ruler
// measurement of the 5 mm check box for any other screen. (Was 0.35278, the 1/72"
// typographic point — a reference the baker does NOT use, which rendered the whole
// chart 0.35278/0.26458 = 1.333× too big.)
const BAKED_FEATURE_PITCH_MM = 0.26458;
// Linear (constant-velocity) easing for the follow camera — see updateFollow. The
// default ease-in/out would stall at each fix boundary, reading as a step.
const LINEAR = (t) => t;
// Return a `to` bearing rewritten relative to `from` so the difference is in
// (-180,180], i.e. the camera rotates the SHORT way (no 359°→0° backspin).
function shortestBearing(from, to) {
  let d = (((to - from) % 360) + 540) % 360 - 180;
  return from + d;
}
// zoomForScale (scale→zoom) is imported from util.mjs so the 512-tile MapLibre
// resolution constant lives in exactly one place (see M_PER_PX_Z0).
// Fill-pattern (AP) images live under this id prefix so they never collide with
// point-symbol (SY) images of the SAME PresLib name. Several names are BOTH a
// point symbol and an area fill pattern (QUESMRK1, AIRARE02, FSHFAC03, MARCUL02):
// e.g. an unknown object is SY(QUESMRK1) — a 26×46 "?" mark — while an unknown
// AREA could be AP(QUESMRK1) — a 178×392 tiled "?" fill. MapLibre keys images by a
// single id, so without this prefix the pattern atlas cell hijacked the symbol
// (styleimagemissing fires before registerAllSymbols → pattern won, first-wins),
// rendering the point "?" as a stretched fragment. Symbols keep their bare names.
// PAT_PREFIX lives in chart-style.mjs now (imported above) — the layer builder and
// this element's registerPattern share it.

// NOAA ENC navigational-purpose bands (the rescheming standard) → one vector
// source each, baked over [min,max] and overzoomed above max (see bake.zig
// `Band`). Stacked coarse→fine: where a finer band has data its fill covers the
// coarser one; where it doesn't, the coarser shows through (overzoomed). `all`
// is the merged single archive (an upload / `--emit-pmtiles`) — one full-range
// source, drawn on top. Order here IS the draw order (bottom→top).
// `bake` is the top zoom the archive actually contains (the source maxzoom; the
// client overzooms above it). Coastal/approach bake +2 past native to sharpen the
// suppression cut vs the next finer band; harbor stops at its native max (z17/18
// would be pure buffer) and the client overzooms it to fill berth level. MUST
// match the baker's bandBakeCeil (internal/engine/bake/bake.go).
// CHART_BANDS, BAND_DISPLAY_MIN, SCAMIN_BUCKET_LAYERS, scaminDisplayZoom, BAND_SLUGS,
// BAND_RANK and bandOfSet now live in chart-sources.mjs (imported above) — they are
// the source/band vocabulary the chart-source manager owns; the element re-imports
// the ones expandChartLayers / buildStyle / band-on-off still reference.

// Ensure the vendored MapLibre UMD global is loaded (the component injects it if
// the host page hasn't), resolving to window.maplibregl.
function ensureMapLibre(assets) {
  if (window.maplibregl) return Promise.resolve(window.maplibregl);
  return new Promise((resolve, reject) => {
    const s = document.createElement("script");
    s.src = assets + "vendor/maplibre-gl.js";
    s.onload = () => resolve(window.maplibregl);
    s.onerror = () => reject(new Error("failed to load maplibre-gl.js"));
    document.head.appendChild(s);
  });
}

export class ChartCanvas extends HTMLElement {
  constructor() {
    super();
    this.attachShadow({ mode: "open" });
    this._colortables = {};
    this._linestyles = {};
    this._sprite = {};
    this._patterns = {};
    this._atlasPpu = 0.08;
    this._pxPitch = undefined; // calibrated CSS-pixel pitch (mm); undefined → CSS reference. Drives _featureSizeScale.
    this._active = "day";
    this._spriteImg = null;
    this._patternsImg = null;
    this._coastline = null; // offline GSHHG basemap GeoJSON fallback, if available
    this._coastlineArchive = null; // offline GSHHG coastline PMTiles (preferred vector basemap)
    this._mariner = {};      // current mariner settings (engine-side)
    // tile57 engine-style mode (set at boot by _initEngineStyle when the server serves
    // /api/style.json): render from the engine style + apply mariner toggles as engine-
    // computed diffs. Off → the JS style builder (Go backend). See buildStyle.
    this._engineMode = false;
    this._engineStyle = null;  // cached full engine style for the last-applied mariner
    this._engineSet = null;    // set the engine style targets (live "tile57" or a baked pack)
    this._lastMariner = null;  // mariner query the engine style currently reflects (diff `from`)
    // DEBUG (?ignoreScamin / ?noscamin): drop the per-SCAMIN display gate so every
    // feature shows in-band regardless of its 1:N min-display-scale. Deliberately a
    // per-page-load CONSTANT read from the URL — NOT a mariner toggle — so it's baked
    // into every buildStyle() and NEVER needs a mid-load setStyle (which would race
    // addCatalogOverlay's addSource → "Style is not done loading"). To flip it, change
    // the URL and reload.
    this._ignoreScamin = (() => {
      try { const q = new URLSearchParams(location.search); return q.has("ignoreScamin") || q.has("noscamin"); }
      catch (e) { return false; }
    })();
    // Engine mode uses the filter-gated SCAMIN style (one live-filtered layer per
    // render-type instead of per-value #sm bucket layers — scamin-layers.md). This is
    // what keeps the engine style at ~35 layers instead of ~1200, so a mariner-toggle
    // diff is ~34 ops, not ~1200 setFilter calls. ?noScaminGate opts out (A/B). The
    // client re-injects the current display-scale denominator (curDenom) into the gated
    // layers on SCAMIN-ladder boundary crossings — see _scaminUpdate.
    this._scaminGate = (() => {
      try { return !new URLSearchParams(location.search).has("noScaminGate"); }
      catch (e) { return true; }
    })();
    this._engineScaminValues = []; // SCAMIN ladder (from the set tilejson) — the crossing boundaries
    this._scaminBandLast = -1;     // last-applied band index (count of ladder values below curDenom)
    this._scaminLayersCache = null; // cached ids of the gated chart layers (filter carries the scamin clause)
    this._layerBase = {};    // chart layer id → intrinsic (pre-category) filter
    this._bandsHidden = new Set(); // usage bands turned off via setBandVisible (host-persisted)
    this._layerVis = {};     // chart layer id → intended (mariner) visibility, so band on/off restores it
    // Chart SOURCE / ARCHIVE state + API (server sets, per-band archives, cache-bust
    // token, SCAMIN-bucket discovery). Constructed in boot() once `assets` resolves.
    this._sources = null;
  }

  connectedCallback() {
    this.boot().catch((e) => {
      console.error("[chartplotter]", e);
      this.shadowRoot.innerHTML =
        `<div style="font:13px system-ui;padding:12px;color:#900">chartplotter failed to start: ${e.message}</div>`;
    });
  }

  get _assets() {
    let a = this.getAttribute("assets") || "./";
    if (!a.endsWith("/")) a += "/";
    return a;
  }

  async boot() {
    const assets = this._assets;
    const maplibregl = await ensureMapLibre(assets);

    // Chart SOURCE / ARCHIVE manager — constructed EARLY (before the first buildStyle
    // and the source-mode init below both touch its state). It owns `_server`,
    // `_serverSets`, `_bands`, `_ver`, and the SCAMIN-bucket discovery; the element's
    // chart-source methods delegate to it. `rebuild` re-applies the full style.
    this._sources = new ChartSources({
      assets,
      getMap: () => this._map,
      rebuild: () => this._map && this._map.setStyle(this.buildStyle(), { diff: false, validate: false }),
      getPxPitch: () => this._pxPitch, // SCAMIN gates on the calibrated physical scale (in-place re-gate)
    });

    // Shadow DOM: MapLibre CSS must live inside the shadow root, plus a sized
    // map container.
    const style = document.createElement("style");
    style.textContent =
      // Defensive: a mis-sized parent host shouldn't collapse the map to 0 height.
      // The #map inset:0 fill is correct; dvh full-screen sizing lives in global-shell.
      // #map is the MAP CANVAS, not chrome (Convention F): touch-action:none so iOS
      // doesn't pre-empt MapLibre's gestures with page double-tap-zoom — and suppress
      // the long-press selection/callout over the chart.
      ":host{display:block;position:relative;min-height:100%}" +
      "#map{position:absolute;inset:0;background:#93aebb;touch-action:none;-webkit-touch-callout:none;-webkit-user-select:none;user-select:none}" +
      // S-52 SCALEB-style scalebar (horizontal striped NM bar, bottom-left). Bottom/
      // left margins clear the iOS home indicator + rounded corners (Convention G).
      ".s52-scalebar{display:flex;flex-direction:column;align-items:flex-start;margin:0 0 max(8px,env(safe-area-inset-bottom)) max(10px,env(safe-area-inset-left));pointer-events:none;user-select:none}" +
      ".s52sb-label{font:700 11px/1.2 system-ui,sans-serif;color:#1a2026;background:rgba(255,255,255,.82);padding:1px 5px;border-radius:4px;margin-bottom:3px;box-shadow:0 1px 3px rgba(0,0,0,.2);font-variant-numeric:tabular-nums}" +
      ".s52sb-bar{display:flex;flex-direction:row;height:8px;min-width:8px;border:1px solid #1a2026;box-sizing:border-box;box-shadow:0 1px 3px rgba(0,0,0,.3)}" +
      ".s52sb-bar span{flex:1;display:block}" +
      // Bottom-left control container (attribution lives here): nudge in from the
      // home indicator / rounded corners so it isn't occluded (Convention G).
      ".maplibregl-ctrl-bottom-left{padding-bottom:env(safe-area-inset-bottom);padding-left:env(safe-area-inset-left)}";
    const css = document.createElement("link");
    css.rel = "stylesheet";
    css.href = assets + "vendor/maplibre-gl.css";
    const mapEl = document.createElement("div");
    mapEl.id = "map";
    this.shadowRoot.append(style, css, mapEl);

    // Tiles are baked server-side (POST /api/import) and served from /tiles/{set};
    // the browser only renders them. Rendering also supports a hosted prebaked
    // .pmtiles read by HTTP Range (the serverless static-CDN option).

    // -- assets (parallel) --------------------------------------------------
    // colortables/sprite are REQUIRED (colours + symbol atlas). The server emits
    // them from the S-101 catalogue; a 404 means the server didn't (stale binary,
    // or built without the catalogue) — fail with a clear message, not a cryptic
    // JSON.parse of the "404 page not found" body.
    const reqJSON = async (name) => {
      const r = await fetch(assets + name);
      if (!r.ok) throw new Error(`${name} not available (HTTP ${r.status}) — rebuild/restart the server (it generates the S-101 client assets)`);
      return r.json();
    };
    const [ct, sj, lsj, pj] = await Promise.all([
      reqJSON("colortables.json"),
      reqJSON("sprite.json"),
      fetch(assets + "linestyles.json").then((r) => (r.ok ? r.json() : {})).catch(() => ({})),
      fetch(assets + "patterns.json").then((r) => (r.ok ? r.json() : {})).catch(() => ({})),
    ]);
    this._colortables = ct;
    this._sprite = sj;
    this._linestyles = lsj;
    this._patterns = pj;
    this._atlasPpu = (sj._meta && sj._meta.px_per_unit) || this._atlasPpu;
    this._patternPixelRatio = 0.08 / FEATURE_SCALE;

    // crossOrigin MUST be set (before src) on the atlas images: SpriteBuilder
    // draws them to a <canvas> and calls getImageData() to cut out each symbol /
    // fill-pattern cell. Without it, if the assets are served cross-origin (a
    // different host/port than the page) the canvas is TAINTED and Chrome throws a
    // SecurityError on getImageData — so EVERY symbol build fails (icons vanish)
    // and EVERY pattern fails to register (MapLibre paints missing fill-patterns
    // as black boxes). The server sends Access-Control-Allow-Origin:* so anonymous
    // CORS succeeds for both same- and cross-origin.
    this._spriteImg = new Image();
    this._spriteImg.crossOrigin = "anonymous";
    this._spriteImg.src = assets + "sprite.png";
    this._patternsImg = new Image();
    this._patternsImg.crossOrigin = "anonymous";
    this._patternsImg.src = assets + "patterns.png";
    await Promise.all([
      this._spriteImg.decode().catch(() => {}),
      this._patternsImg.decode().catch(() => {}),
    ]);
    // Sprite/glyph image SYNTHESIS collaborator (centred symbols, composited
    // sounding glyphs, raw pattern cells). Constructed here — after the atlas
    // metadata + decoded images are all set — so it exists before any
    // registerImage/registerAll* fires (the map "load" handler, below).
    this._sprites = new SpriteBuilder({
      sprite: this._sprite, spriteImg: this._spriteImg,
      patterns: this._patterns, patternsImg: this._patternsImg, atlasPpu: this._atlasPpu,
    });

    // Offline basemap: load the GSHHG-derived coastline if this map uses it
    // (best-effort — absent → plain sea bg). Prefer the tiled vector basemap
    // (coastline.pmtiles: sharper, loads by viewport, overzooms crisply); fall
    // back to the flat coastline.geojson blob when the tileset isn't present.
    const basemap = this.getAttribute("basemap") || "none";
    if (basemap === "coastline" || basemap === "gshhg") await this._ensureCoastline();
    // Serve the coastline tileset over its own protocol (separate archive from
    // the chart tiles, so it never collides with the chart:// source).
    registerPmtilesProtocol(maplibregl, "coastline", () => this._coastlineArchive);

    // Optional OSM vector basemap: a hosted Protomaps .pmtiles, read by range over
    // its own protocol (works offline if hosted alongside the charts). Loaded
    // lazily when the basemap is switched to "osmvec".
    this._osmvecUrl = this.getAttribute("osm-pmtiles") || "";
    registerPmtilesProtocol(maplibregl, "osmvec", () => this._osmvecArchive);
    if (basemap === "osmvec" && this._osmvecUrl) {
      this._osmvecArchive = await new PMTilesArchive(this._osmvecUrl).init().catch(() => null);
    }

    // Render-source mode. server (tiles="server"): one vector source per server pack
    // (/tiles/{set}), each a baked provider/pack (noaa-d17, ienc-…). The packs are
    // chosen by the `sets`/`set` attribute or setServerSets(). Otherwise the prebaked
    // per-band pmtiles:// path (setArchive/loadRegions/pmtiles=), a static-CDN archive.
    // The manager learns each declared set's real zoom range before the first
    // buildStyle so the source maxzoom is truthful (overzoom, not empty-tile holes).
    {
      const names = (this.getAttribute("sets") || this.getAttribute("set") || "")
        .split(",").map((s) => s.trim()).filter(Boolean);
      await this._sources.initServerMode(this.getAttribute("tiles") === "server", names);
    }

    // Per-band prebaked sources (chart-<slug>), one PMTiles protocol each. Each
    // carries its own maxzoom so MapLibre client-overzooms a coarse band up into
    // finer display zooms (coastal z11 → z18 offshore). Used by the prebaked path;
    // harmless (blank) in server mode.
    for (const band of CHART_BANDS) {
      const slug = band.slug;
      registerPmtilesProtocol(maplibregl, "chart-" + slug, () => this._sources.bandArchive(slug));
    }

    // tile57 engine-style probe: if the server serves /api/style.json (a -tags tile57
    // backend), adopt the engine style as the render style (engine mode) BEFORE the first
    // buildStyle. A 501/error leaves engine mode off → the JS builder (Go backend).
    await this._initEngineStyle();

    // -- map ----------------------------------------------------------------
    const [lon, lat] = (this.getAttribute("center") || "-76.4875,38.975")
      .split(",").map(Number);
    const map = new maplibregl.Map({
      container: mapEl,
      style: this.buildStyle(),
      center: [lon, lat],
      zoom: Number(this.getAttribute("zoom") || 13),
      // Max zoom-OUT clamped to z3.5 (continental scale) — there's no finer-than-
      // useless world view to show. Max zoom-IN is 18, but the app lowers it
      // dynamically to the finest band that actually covers the view (overscale cap,
      // see _updateZoomCap) so you can't zoom into featureless open water.
      minZoom: 3.5,
      maxZoom: 18,
      // Skip MapLibre's per-layer style validation. A server-mode install builds
      // THOUSANDS of SCAMIN-bucket layers; validating (and serializing) each one at
      // style-build dominated startup (~7s in a profile). The style is generated by
      // buildStyle(), not user input, so validation is pure overhead.
      validateStyle: false,
      // Attribution bottom-left so the bottom-right corner is free for the app's
      // scale/zoom readout.
      attributionControl: { position: "bottom-left" },
    });
    this._map = map;
    this.map = map; // public handle

    // Console diagnostics (black-box triage), available as soon as the map exists.
    // __chartImages() lists every CURRENTLY-registered image + size (live, via the
    // public listImages/getImage API). __chartGL() prints the GPU limits.
    window.__chartImages = () => {
      const rows = (map.listImages() || []).map((id) => {
        const im = map.getImage(id);
        const d = (im && (im.data || im)) || {};
        return { id, size: (d.width || 0) + "x" + (d.height || 0) };
      }).sort((a, b) => a.id.localeCompare(b.id));
      console.table(rows);
      return `${rows.length} images registered`;
    };
    window.__chartGL = () => this._logGLDiag();

    // Touch gestures: keep pinch-zoom but DROP two-finger rotate (and drag-rotate)
    // so a pinch can't tilt/spin the chart out from under the north-up/course-up/
    // head-up follow modes (setCameraMode/updateFollow). MapLibre's default
    // touchZoomRotate couples zoom+rotate on the same two-finger gesture, which on
    // iOS/iPad constantly fought the orientation lock. If free rotation is wanted
    // later, gate this on the camera mode instead of disabling outright.
    if (map.touchZoomRotate && map.touchZoomRotate.disableRotation) map.touchZoomRotate.disableRotation();
    if (map.dragRotate && map.dragRotate.disable) map.dragRotate.disable();

    // Graphical bar scale, complementing the numeric 1:N readout in the app HUD.
    // Follows the mariner unit setting: metric (m/km) or imperial (ft/mi); MapLibre
    // auto-picks the small/large unit by distance. Kept on the instance so a later
    // unit change can switch it live (see setMariner).
    // S-52 PresLib SCALEB-style nautical scalebar (latitude / nautical miles),
    // replacing MapLibre's generic metric/imperial line. A vertical striped bar
    // (SCALEB10 = 1 NM, SCALEB11 = 10 NM are the spec references) whose length is a
    // round NM distance measured along latitude at the view centre — exact for NM
    // since 1 NM ≡ 1 arcminute of latitude. Re-rendered on every move.
    this._scaleEl = document.createElement("div");
    this._scaleEl.className = "s52-scalebar maplibregl-ctrl";
    // Spec mode (chrome-free capture, see chartplotter.mjs) hides the scalebar too —
    // it lives in this element's shadow root, out of reach of the app's :host([spec]) CSS.
    if (document.querySelector("chart-plotter-app[spec], chart-plotter[spec]")) this._scaleEl.style.display = "none";
    map.addControl({ onAdd: () => this._scaleEl, onRemove: () => { this._scaleEl = null; } }, "bottom-left");
    map.on("move", () => { this._renderScalebar(); this._scaminUpdate(); });

    // Surface MapLibre's own errors (style/source/tile/WebGL) to the console —
    // otherwise a failed texture upload is silent (renders black).
    map.on("error", (e) => console.warn("[maplibre error]", (e && e.error && e.error.message) || e));

    map.on("styleimagemissing", (e) => {
      if (window.__chartImgLog) console.log(`[missing] ${e.id}`); // did MapLibre request it?
      // Pattern images are requested under the `pat:` namespace (fill-pattern
      // exprs add the prefix); everything else is a point/sounding symbol.
      if (e.id.startsWith(PAT_PREFIX)) this.registerPattern(e.id.slice(PAT_PREFIX.length));
      else this.registerImage(e.id);
    });
    // Learn the distinct SCAMIN values from tiles as they load → per-SCAMIN bucket
    // layers. Only on idle (tiles settled), and it rebuilds the style ONLY when the
    // value set grows or the centre latitude shifts enough to move a bucket minzoom
    // — never per zoom. Once converged, MapLibre gates the buckets natively for free.
    map.on("idle", () => this._sources._refreshScaminBuckets());
    map.on("load", async () => {
      this._renderScalebar(); // initial draw (the move hook only fires on movement)
      this._scaminLayersCache = null; this._scaminUpdate(true); // engine gate: initial SCAMIN cutoff
      // Images are registered LAZILY via the styleimagemissing handler above —
      // only the symbols/patterns actually referenced by visible tiles enter
      // MapLibre's icon atlas. Eagerly registering all ~750 (724 symbols + 26
      // patterns, some 300×600) packed a huge single atlas texture that exceeded
      // MAX_TEXTURE_SIZE on low-end / software GPUs, so the WHOLE atlas failed to
      // upload and every symbol/pattern rendered as a black box. Lazy keeps the
      // atlas to the handful on screen (see _logGLDiag for the live count).
      this._logGLDiag(); // print GPU limits + image-atlas stats (black-box triage)
      // `pmtiles="<url>"`: render a hosted prebaked archive directly (no
      // baking) — the third ingest route alongside upload + bake-once. With no
      // explicit `center`, frame to the archive's data extent.
      const pmUrl = this.getAttribute("pmtiles");
      if (pmUrl) {
        try {
          const arc = await this.loadArchiveUrl(pmUrl);
          if (arc && arc.bounds && !this.hasAttribute("center")) {
            map.fitBounds([[arc.bounds[0], arc.bounds[1]], [arc.bounds[2], arc.bounds[3]]], { padding: 40, duration: 0 });
          }
        } catch (e) { console.warn("[chartplotter] pmtiles load", pmUrl, e); }
      }
      // composed so it crosses the shell's shadow boundary → a page-level splash
      // (index.html) can hear it and fade out once the map's first frame is up.
      this.dispatchEvent(new CustomEvent("ready", { detail: { map }, bubbles: true, composed: true }));
    });
  }

  // The active scheme's colour table (token → rgb); the palette every S52.*
  // colour helper takes. Empty object when no colortables are loaded yet.
  _palette() { return this._colortables[this._active] || {}; }

  // -- colour --------------------------------------------------------------
  // Resolve a single S-52 colour token for the active scheme (concrete value,
  // not an expression) — used for basemap layers whose colour is fixed.
  token(name, fallback) { return S52.token(name, fallback, this._palette()); }
  seaColor() { return S52.seaColor(this._palette()); }   // deep water / sea backdrop
  landColor() { return S52.landColor(this._palette()); }  // S-52 land area
  coastColor() { return S52.coastColor(this._palette()); } // coastline stroke

  colorExpr(prop, fallback) { return S52.colorExpr(prop, fallback, this._palette()); }

  // Resolve a colour-token-valued expression to an RGB for the active scheme.
  colorMatch(tokenExpr, fallback) { return S52.colorMatch(tokenExpr, fallback, this._palette()); }

  // Legible chart-text colour. S-52's dusk/night palettes dim the text inks
  // (CHBLK/CHGRD) to near-black, which is unreadable on the equally dark scheme
  // — a halo can't help because the glyph *body* itself vanishes. So at
  // dusk/night we render text in a bright neutral (legibility over strict
  // night-vision dimming, per user request) and pair it with a dark halo
  // (textHaloColor). Day keeps the per-feature S-52 ink (so coloured labels
  // stay semantic) over a light halo.
  textColor() { return S52.textColor(this._active, this._palette()); }
  // Backing that contrasts with textColor: light under day's dark inks, dark
  // under the bright dusk/night ink. Applied to ALL text — the old bake gated
  // the halo to ≥10 px glyphs, leaving small labels bare.
  textHaloColor() { return S52.textHaloColor(this._active); }
  // Contour (depth) labels: S-52 CHGRD by day, bright neutral at dusk/night so
  // they stay legible like the rest of the chart text.
  contourLabelColor() { return S52.contourLabelColor(this._active, this._palette()); }

  // SEABED01 (S-52 §13.2.15) as a data-driven expression: a depth area's
  // DRVAL1/DRVAL2 vs the mariner's shallow/safety/deep contours → a depth
  // colour token. Done client-side so dragging the contours is an instant
  // restyle, not a re-bake. Deepest band first (the spec cascade's last match
  // wins → first match in a `case`). `>= X && > X` on both bounds per the spec.
  seabedTokenExpr() { return S52.seabedTokenExpr(this._mariner); }

  // Fill colour for the `areas` layer: depth areas (carry drval1) shade live via
  // SEABED01; everything else uses its baked colour token.
  areasFillColor() { return S52.areasFillColor(this._palette(), this._mariner); }

  // SHALLOW_PATTERN filter: depth areas on the shallow side of the live safety
  // contour — SEABED01's SHALLOW flag, i.e. NOT (drval1 ≥ SFC && drval2 > SFC).
  shallowPatternFilter() { return S52.shallowPatternFilter(this._mariner); }

  // Safety-contour line (DEPARE03, client-side): the DEPSC-emphasised edge is
  // approximated by the outline of any depth area whose [DRVAL1, DRVAL2) range
  // straddles the live safety contour (drval1 < SFC ≤ drval2) — the same
  // area-level approximation the engine used to bake, now a filter so moving
  // the safety contour restyles instantly with no re-bake.
  safetyContourFilter() { return S52.safetyContourFilter(this._mariner); }

  // Bar-scale unit following the mariner depth-unit setting: imperial (ft/mi) when
  // depths are in feet, otherwise metric (m/km). MapLibre picks the small vs large
  // unit by the current distance.
  _scaleUnit() { return this._mariner.depthUnit === "ft" ? "imperial" : "metric"; }

  // Render the S-52 SCALEB-style scalebar: a vertical striped bar of a round
  // distance in the mariner's chosen distance unit (NM / km / mi). 1 NM ≡ 1
  // arcminute of latitude, so px-per-NM is measured along the meridian at the view
  // centre (exact, no Mercator distortion), then scaled into the chosen unit. The
  // distance is the largest "nice" step (… 0.5, 1, 2, 5 …) that stays under a target
  // length; SCALEB colours (SCLBR / CHGRD) are scheme-aware via token().
  _renderScalebar() {
    const m = this._map, el = this._scaleEl;
    if (!m || !el) return;
    const c = m.getCenter();
    const pxPerNM = Math.abs(m.project([c.lng, c.lat]).y - m.project([c.lng, c.lat + 1 / 60]).y);
    if (!pxPerNM || !isFinite(pxPerNM) || pxPerNM < 0.01) { el.innerHTML = ""; return; }
    const unit = this._mariner.distanceUnit || "NM";
    const pxPerU = pxPerNM / convertDistance(1, unit); // px per chosen unit (units-per-NM)
    const MAXPX = 150;
    const STEPS = [0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 25, 50, 100, 250, 500, 1000, 2500];
    let dist = STEPS[0];
    for (const v of STEPS) { if (v * pxPerU <= MAXPX) dist = v; else break; }
    const totalPx = Math.round(dist * pxPerU);
    const dark = this.token("SCLBR", "#e8820c"), light = this.token("CHGRD", "#dfe3e7");
    let bar = "";
    for (let i = 0; i < 4; i++) bar += `<span style="background:${i % 2 ? light : dark}"></span>`;
    el.innerHTML = `<div class="s52sb-label">${dist} ${unitSuffix(unit)}</div><div class="s52sb-bar" style="width:${totalPx}px">${bar}</div>`;
  }

  // SAFCON01 (S-52 §13.2.13): the depth-contour value label. Drawn client-side
  // along DEPCNT lines from the baked VALDCO (whole metres, or whole feet when
  // the mariner picks imperial units), shown only when "contour labels" is on.
  contourLabelField() { return S52.contourLabelField(this._mariner); }

  // Dredged-area depth label (S-52 row 47): DRVAL1 at the DRGARE centroid in the
  // chosen depth unit — see s52-style.drgareLabelField.
  drgareLabelField() { return S52.drgareLabelField(this._mariner); }

  // SNDFRM04 (S-52 §13.2.16): a sounding ≤ the live safety depth uses the bold
  // SOUNDS glyphs, else the faint SOUNDG glyphs — picked client-side from the
  // baked depth + both name variants. Falls back to the baked names if a tile
  // predates the variants. In imperial mode the metres glyphs can't be reused
  // (the number changes), so synthesize a `snd:` image name from the numeric
  // depth + palette; `registerImage` builds the converted glyph composite.
  soundingsIconImage() { return S52.soundingsIconImage(this._mariner); }

  // OBSTRN06/WRECKS05 (S-52 §13.2.6/§13.2.20): a danger symbol carries its
  // VALSOU + the deep-water variant. The baked `symbol_name` is the dangerous
  // (DANGER01) variant; when the depth is DEEPER than the live safety contour
  // swap to the less-prominent `sym_deep` (DANGER02). Picked client-side so
  // changing the safety contour needs no re-bake. Non-danger symbols use `symbol_name`.
  pointSymbolImage() { return S52.pointSymbolImage(this._mariner); }

  // The dotted CHBLK foul boundary (OBSTRN/WRECKS) is shown only where the
  // feature's VALSOU is at/above the live safety contour — a danger.
  dangerBoundaryFilter() { return S52.dangerBoundaryFilter(this._mariner); }

  // Display category (S-52 §10.3.4), client-side + MULTI-SELECT: every feature
  // is baked with its category rank `cat` (0=base,1=standard,2=other); the
  // mariner independently toggles each, so this is a membership test, not a
  // cumulative level. Missing `cat` (stale tile) defaults to standard.
  categoryFilter() { return S52.categoryFilter(this._mariner); }

  // Boundary symbolization (S-52 §8.6.1), client-side: each primitive is baked
  // with a `bnd` tag — 2 = style-independent (always shown), 0 = plain-boundary
  // only, 1 = symbolized-boundary only. Show common (2) + the active style.
  // Missing `bnd` (non-area / stale tile) defaults to common. Default to
  // SYMBOLIZED (rank 1) per the IMO/S-52 default (the engine also bakes
  // SymbolizedBoundaries=true by default); plain only when explicitly chosen.
  // Symbolized is the variant that carries the embedded LC line symbols (e.g.
  // RESARE's EMAREMG1), so a plain default hid every complex-line symbol.
  boundaryFilter() { return S52.boundaryFilter(this._mariner); }

  // Point-symbol style (S-52 §11.2.2), client-side: point features that resolve
  // differently under the simplified vs paper-chart LUP tables are baked twice,
  // tagged `pts` — 2 = style-independent (always shown), 0 = paper-chart, 1 =
  // simplified. Show common (2) + the active style. Missing `pts` (non-point /
  // identical-in-both / stale tile) defaults to common. Default PAPER (rank 0)
  // per the engine default (SimplifiedPoints=false).
  pointStyleFilter() { return S52.pointStyleFilter(this._mariner); }

  // Light sector leg length (S-52 LIGHTS06 note 1), client-side: each sector
  // light's legs are baked twice, tagged `sleg` — 0 = the 25 mm short leg
  // (default, avoids clutter), 1 = the full VALNMR nominal-range leg. Arcs/rings
  // are untagged (coalesce 2 → always shown). Show common (2) + the active
  // length. Default SHORT (rank 0) per the engine (ShowFullLengthSectorLines=false).
  sectorLegFilter() { return S52.sectorLegFilter(this._mariner); }

  // SCAMIN-bucket discovery (the prebaked path's idle loop) lives on the chart-source
  // manager now — the map's "idle" handler calls this._sources._refreshScaminBuckets().

  // Combine a layer's intrinsic (base) filter with the live category +
  // boundary-style filters (the two client-side portrayal axes baked as
  // per-feature `cat`/`bnd`), then drop any individually-hidden cells. Hiding is
  // a pure client filter on the baked per-feature `cell` id — instant, no re-bake,
  // and it works the same in prebaked (pmtiles) and server (dynamic) modes.
  combineFilters(base) {
    const f = S52.combineFilters(base, this._mariner);
    if (this._hiddenCells && this._hiddenCells.length) {
      // Features without a `cell` (none, in practice) get null → kept.
      return ["all", ["!", ["in", ["get", "cell"], ["literal", this._hiddenCells]]], f];
    }
    return f;
  }

  // Hide/show individual cells by id (the baked per-feature `cell`). `cells` is the
  // full set of hidden ids; an empty array shows everything. Re-applies the combined
  // filter across every chart layer — a snappy restyle, never a re-bake.
  setHiddenCells(cells) {
    this._hiddenCells = Array.isArray(cells) ? cells.slice() : [];
    this.applyFeatureFilters();
  }

  // Re-apply the combined feature filter to every chart layer (on a category
  // or boundary-style toggle), preserving each layer's recorded base filter.
  applyFeatureFilters() {
    const map = this._map;
    if (!map || !this._layerBase) return;
    // `{ validate: false }` skips MapLibre's per-call filter validation — the
    // dominant cost when a full install has THOUSANDS of (SCAMIN-bucket) chart
    // layers (a category/boundary toggle re-filters them all). The filters are
    // generated here, not user input, so validation is pure overhead. This turns a
    // multi-second main-thread freeze on every display toggle into a snappy update.
    for (const id in this._layerBase) {
      if (map.getLayer(id)) map.setFilter(id, this.combineFilters(this._layerBase[id]), { validate: false });
    }
    // The gated layers' base filter carries the SCAMIN clause at curDenom=0 (show-all);
    // re-inject the live cutoff so a mariner toggle doesn't reveal everything until the
    // next pan/zoom. Cheap (~1 setFilter per gated layer, ~16), only when the gate is on.
    if (this._scaminGate) { this._scaminLayersCache = null; this._scaminUpdate(true); }
  }

  // Update a chart layer's base filter and re-apply it combined with the live
  // category + boundary filters. Used when a base filter that depends on
  // another mariner setting (e.g. the safety contour) changes.
  setBaseFilter(id, base) {
    const map = this._map;
    for (const lid of this._variantIds(id)) {
      if (this._layerBase) this._layerBase[lid] = base;
      if (map && map.getLayer(lid)) map.setFilter(lid, this.combineFilters(base), { validate: false });
    }
  }

  // Switch Day/Dusk/Night with zero re-tiling (colour is never baked).
  setScheme(name) {
    if (!this._colortables[name]) return;
    this._active = name;
    // Engine mode: scheme is a mariner field → the diff emits the colour ops.
    if (this._engineMode) { this._engineRestyle(); return; }
    const map = this._map;
    // A chart base id targets every band variant; basemap ids fall back to self.
    const setIf = (id, prop, val) => { for (const lid of this._variantIds(id)) if (map.getLayer(lid)) map.setPaintProperty(lid, prop, val); };
    setIf("areas", "fill-color", this.areasFillColor());
    for (const id of ["lines-solid", "lines-dashed", "lines-dotted"]) setIf(id, "line-color", this.colorExpr("color_token"));
    setIf("safety-contour", "line-color", this.token("DEPSC", "#3a6a8a"));
    setIf("danger-boundary", "line-color", this.token("CHBLK", "#000000"));
    setIf("contour-labels", "text-color", this.contourLabelColor());
    setIf("contour-labels", "text-halo-color", this.textHaloColor());
    setIf("drgare-labels", "text-color", this.textColor());
    setIf("drgare-labels", "text-halo-color", this.textHaloColor());
    setIf("complex-lines", "line-color", this.colorExpr("color_token"));
    setIf("text", "text-color", this.textColor());
    setIf("text", "text-halo-color", this.textHaloColor());
    setIf("light-text", "text-color", this.textColor());
    setIf("light-text", "text-halo-color", this.textHaloColor());
    // Basemap (sea background + offline coastline) is scheme-aware too.
    setIf("bg", "background-color", this.seaColor());
    setIf("coast-land", "fill-color", this.landColor());
    setIf("coast-lake", "fill-color", this.seaColor());
    setIf("coast-line", "line-color", this.coastColor());
    // OSM raster underlay dims for dusk/night (and restores for day).
    const osmPaint = this._osmRasterPaint();
    for (const k in osmPaint) setIf("osm", k, osmPaint[k]);
  }

  // Feature-size multiplier that renders baked (point-pixel) sizes at true physical
  // size on this screen: 0.35278 mm/baked-px ÷ the (calibrated) CSS-pixel pitch. On
  // the default CSS pixel (0.2645 mm) ≈1.333×; calibration makes it exact.
  _featureSizeScale() {
    return BAKED_FEATURE_PITCH_MM / clampPxPitch(this._pxPitch || DEFAULT_PX_PITCH_MM);
  }

  // Set the calibrated CSS-pixel pitch (mm) and rebuild the style so every feature
  // size (icons/lines/text/halos/patterns) re-renders at true physical size. Driven
  // by the shell's screen-calibration setting, mirroring the scale-readout path.
  setPxPitch(mm) {
    const v = (typeof mm === "number" && mm > 0) ? mm : undefined;
    if (v === this._pxPitch) return;
    this._pxPitch = v;
    // Engine mode: sizeScale (from the pixel pitch) is a style input → engine diff.
    if (this._engineMode) { this._engineRestyle(); return; }
    if (this._map && this._sources) {
      // Patterns bake the physical-size correction into their pixelRatio at
      // registration; a calibration change alters that ratio but registerPattern
      // never updates an existing image (hasImage guard). Drop them so they
      // re-register at the new ratio (point symbols rescale via icon-size).
      this._dropPatternImages();
      this._map.setStyle(this.buildStyle(), { diff: false, validate: false });
    }
  }

  // Switch the basemap live: "coastline" (offline GSHHG land/lakes), "osm"
  // (online OpenStreetMap raster), or "osmvec" (hosted OSM vector .pmtiles).
  // Rebuilds the style from buildStyle() so the basemap sources/layers swap
  // cleanly; chart sources, loaded archives and the tile protocols persist.
  async setBasemap(mode) {
    const m = mode === "osm" || mode === "osmvec" || mode === "none" ? mode : "coastline";
    if ((this.getAttribute("basemap") || "coastline") === m) return;
    if (m === "osmvec" && !this._osmvecArchive && this._osmvecUrl) {
      this._osmvecArchive = await new PMTilesArchive(this._osmvecUrl).init().catch(() => null);
    }
    // The offline coastline archive is fetched lazily — if you started on OSM/none
    // and only now switch to "offline" it was never loaded at connect, so load it
    // here. Otherwise buildStyle adds no coastline layers and the map goes blank
    // (the "toggling offline after OSM doesn't reload" case).
    if (m === "coastline") await this._ensureCoastline();
    this.setAttribute("basemap", m);
    if (this._map) this._map.setStyle(this.buildStyle(), { diff: false, validate: false });
  }

  // Load the offline GSHHG coastline basemap once (best-effort): the tiled vector
  // archive (coastline.pmtiles) preferred, else the flat coastline.geojson blob.
  // Idempotent — returns immediately if either is already loaded.
  async _ensureCoastline() {
    if (this._coastlineArchive || this._coastline) return;
    this._coastlineArchive = await new PMTilesArchive(this._assets + "basemap/coastline.pmtiles").init().catch(() => null);
    if (!this._coastlineArchive) {
      this._coastline = await fetch(this._assets + "basemap/coastline.geojson")
        .then((r) => (r.ok ? r.json() : null))
        .catch(() => null);
      if (!this._coastline) console.warn("[chartplotter] no offline coastline basemap (basemap/coastline.pmtiles or .geojson)");
    }
  }

  // Is the active basemap any OSM variant (raster or vector)? Used to let the OSM
  // land show through (drop the chart's land fill + no-data hatch).
  _osmBasemap() {
    const b = this.getAttribute("basemap") || "none";
    return b === "osm" || b === "osmvec";
  }

  // Raster-paint adjustment for the OSM basemap per active colour scheme. The
  // public OSM tiles are a bright daytime street map; at dusk/night we dim and
  // desaturate them (marine night-vision) so the underlay doesn't blow out the
  // dark S-52 palette. Day = identity. All four keys are always returned so
  // setScheme can restore defaults when switching back to day.
  _osmRasterPaint() { return S52.osmRasterPaint(this._active); }

  // -- runtime chart & settings API (driven by the <chart-plotter-app> shell) --

  // Force the chart source to re-request its tiles (after the loaded archive
  // changes). Delegates to the chart-source manager (it owns the cache-bust token).
  refresh() { return this._sources.refresh(); }

  // Re-request tiles after the SERVER re-bakes a set. Delegates to the chart-source
  // manager (re-fetches each set's TileJSON for the fresh bake-generation token).
  // Public; the shell calls it when a re-bake completes.
  flushTiles() { return this._sources.flushTiles(); }

  // -- overlay & camera API (for plugins: own-ship, AIS, …) ----------------
  // Overlay plugins (own-ship marker, AIS targets, the pick highlight) live in
  // their own modules and render on top of the chart. They add their own GeoJSON
  // source via the public `map` handle, then place layers through addOverlayLayer
  // so z-order against the chart is consistent. See specs/web-architecture.md.

  // The chart layer overlays should insert *before* to sit beneath chart text /
  // symbol labels; undefined ⇒ append on top of everything. Plugins rarely need
  // it directly (use addOverlayLayer); exposed for fine z-control.
  get overlayBeforeId() {
    const map = this._map;
    if (!map) return undefined;
    for (const l of map.getStyle().layers || []) {
      if (l.type === "symbol" && typeof l.source === "string" && l.source.startsWith("chart")) return l.id;
    }
    return undefined;
  }

  // Add a plugin overlay layer (its source must already be added via `map`).
  // Default z-order is on top of the chart; pass {belowLabels:true} to slot it
  // beneath the chart's text/symbol labels. Idempotent. Returns the layer id.
  addOverlayLayer(layer, { belowLabels = false } = {}) {
    const map = this._map;
    if (!map || map.getLayer(layer.id)) return layer.id;
    map.addLayer(layer, belowLabels ? this.overlayBeforeId : undefined);
    return layer.id;
  }

  // Remove plugin overlay layers (and optionally their source) on teardown.
  removeOverlay(layerIds = [], sourceId) {
    const map = this._map;
    if (!map) return;
    for (const id of [].concat(layerIds)) if (map.getLayer(id)) map.removeLayer(id);
    if (sourceId && map.getSource(sourceId)) map.removeSource(sourceId);
  }

  // Camera orientation for own-ship / target-following overlays. A tracking
  // plugin sets the mode, then pushes each new fix via updateFollow():
  //   "free"      — user controls the camera (default)
  //   "north-up"  — recentre on the target, bearing held north
  //   "course-up" — recentre on the target, chart rotated to the target's course
  //   "head-up"   — recentre on the target, chart rotated to the target's heading
  setCameraMode(mode) {
    this._cameraMode = mode || "free";
    if (this._map && this._cameraMode === "north-up") this._map.easeTo({ bearing: 0, duration: 300 });
    if (this._followFix) this.updateFollow(this._followFix);
    return this._cameraMode;
  }

  // Push the latest target fix {lng, lat, courseDeg?, headingDeg?} from a tracking
  // plugin; the camera recentres (and, in course-/head-up, rotates) per the active
  // mode. A no-op in "free" mode, so a plugin can stream fixes regardless of mode.
  //
  // Smoothing: each fix eases (not jumps) to the new pose over a duration sized to
  // the gap since the previous fix — so the segment finishes about when the next
  // fix lands and the motion is continuous instead of stepping. The easing is
  // LINEAR (constant velocity), not the default ease-in/out, which would stutter at
  // every segment boundary. A new fix's easeTo cancels any in-flight one. Reduced-
  // motion (or the very first fix) collapses the duration to a near-instant snap.
  updateFollow(fix) {
    this._followFix = fix || null;
    const map = this._map;
    if (!map || !fix || (this._cameraMode || "free") === "free") return;

    // Duration ≈ the interval between fixes, clamped sane, so one ease runs into the
    // next. First fix (no prior timestamp) snaps; prefers-reduced-motion snaps too.
    const now = (typeof performance !== "undefined" ? performance.now() : Date.now());
    const gap = this._lastFollowTs ? now - this._lastFollowTs : 0;
    this._lastFollowTs = now;
    const reduce = this._reduceMotion || (this._reduceMotion = window.matchMedia
      && window.matchMedia("(prefers-reduced-motion: reduce)").matches);
    const duration = (!gap || reduce) ? 0 : Math.max(200, Math.min(1200, gap));

    const cam = { center: [fix.lng, fix.lat], duration, easing: LINEAR };

    // Look-ahead offset: in course-/head-up the chart rotates so the vessel's
    // direction points up, so we sit the vessel ⅓ up from the bottom — most of the
    // screen is water *ahead*. Screen y is down, so a positive y-offset drops the
    // centre (the vessel) below the container middle; ⅓-from-bottom is ⅙ of the
    // height below centre. North-up stays centred (offset 0).
    const h = (this._followLookAhead !== false && (this._cameraMode === "course-up" || this._cameraMode === "head-up"))
      ? (map.getContainer() && map.getContainer().clientHeight) || 0 : 0;
    if (h) cam.offset = [0, h / 6];

    // Hold the mode's bearing on every fix; otherwise this centre-only ease would
    // cancel the one-shot bearing reset from setCameraMode (north-up gets stuck at
    // the previous course-up heading). Feed an UNWRAPPED target relative to the
    // current bearing so MapLibre always rotates the short way (no 359°→0° spin).
    let target = null;
    if (this._cameraMode === "course-up" && typeof fix.courseDeg === "number") target = fix.courseDeg;
    else if (this._cameraMode === "head-up" && typeof fix.headingDeg === "number") target = fix.headingDeg;
    else if (this._cameraMode === "north-up") target = 0;
    if (target != null) cam.bearing = shortestBearing(map.getBearing(), target);
    map.easeTo(cam);
  }

  // Stop following and release the camera to the user.
  clearFollow() { this._followFix = null; this._cameraMode = "free"; }

  // Open the viewport on a geographic position at a target paper scale. Public API.
  //   setView({ lat, lng, scale })            — centre + 1:N scale (jump)
  //   setView({ lat, lng, zoom })             — centre + explicit web-Mercator zoom
  //   setView({ scale })                      — restage scale, keep the centre
  //   setView({ lat, lng, scale, animate:true, duration:800 }) — fly instead of jump
  // `scale` is the paper-chart denominator (1:N) and is converted to the zoom that
  // yields that scale at the target latitude (web-Mercator scale is latitude-
  // dependent), the inverse of the HUD's scale readout. `bearing`/`pitch` pass
  // through. Omitted fields hold their current value. Returns the resolved
  // { center:[lng,lat], zoom }. The map's own max-zoom (scale floor) still
  // clamps an over-fine request, exactly as user zoom does.
  setView({ lat, lng, scale, zoom, bearing, pitch, animate = false, duration = 800 } = {}) {
    const map = this._map;
    if (!map) return null;
    const c = map.getCenter();
    // Clamp to the web-Mercator latitude limit so an out-of-range lat can't drive
    // cos(φ) negative and yield a NaN zoom (and so the centre is itself valid).
    const la = Math.max(-85.051129, Math.min(85.051129, Number.isFinite(lat) ? lat : c.lat));
    const lo = Number.isFinite(lng) ? lng : c.lng;
    let z = Number.isFinite(zoom) ? zoom : (Number.isFinite(scale) ? zoomForScale(scale, la) : map.getZoom());
    const cam = { center: [lo, la], zoom: z };
    if (Number.isFinite(bearing)) cam.bearing = bearing;
    if (Number.isFinite(pitch)) cam.pitch = pitch;
    if (animate) map.easeTo({ ...cam, duration }); else map.jumpTo(cam);
    return { center: [lo, la], zoom: z };
  }

  // -- chart-source API (thin delegators to the ChartSources manager) ------
  // The chart SOURCE / ARCHIVE state + logic lives in chart-sources.mjs; the element
  // keeps the SAME public method names/signatures so external callers (the shell) are
  // unchanged, forwarding (and returning the awaited value) to `this._sources`.

  // Render exactly these server tile sets (provider/pack names, the {set} in
  // /tiles/{set}/…), baked + registered by the Go server. Switches into server mode,
  // (re)builds the style, and re-requests tiles. Pass [] to clear. Returns the
  // active set names.
  async setServerSets(names) {
    // Re-evaluate the engine style for the NEW active set(s) first (a tile57-baked pack
    // renders from the engine style, not the JS builder), so buildStyle picks the right
    // path when the source manager rebuilds. Reset engine state, re-probe for `names`.
    this._engineMode = false; this._engineStyle = null; this._engineSet = null; this._scaminLayersCache = null;
    await this._initEngineStyle(Array.isArray(names) ? names : (names ? [names] : []));
    return this._sources.setServerSets(names);
  }

  // Convenience: render a single server set (or none). See setServerSets.
  setServerSet(name) { return this._sources.setServerSet(name); }

  // The active server tile-set names ([] when not in server mode).
  serverSets() { return this._sources.serverSets(); }

  // The active server sets' metadata ({name,band,min,max,bounds}) — so the host's
  // zoom-cap can tell which finest band covers the view centre. [] when not in
  // server mode.
  serverSetMetas() { return this._sources.serverSetMetas(); }

  // REPLACE the loaded chart coverage with a single archive (a Blob/File or URL).
  // Returns the opened archive (read `.bounds` to frame). Re-requests tiles.
  setArchive(src) { return this._sources.setArchive(src); }

  // ADD an archive to the loaded coverage into its NOAA band (or fanned across every
  // band for a bandless merged archive). Returns the opened archive.
  addArchive(src, band) { return this._sources.addArchive(src, band); }

  // Replace ALL loaded chart coverage with exactly these region-archive URLs. An
  // empty list clears the map.
  loadRegions(urls) { return this._sources.loadRegions(urls); }

  // REPLACE every archive in ONE band with `src` (a URL or Blob/File). Returns the
  // opened archive.
  replaceBand(band, src) { return this._sources.replaceBand(band, src); }

  // ADD several archives at once, then re-request tiles ONCE. Returns the opened
  // archives.
  addArchives(entries) { return this._sources.addArchives(entries); }

  // Render a hosted `.pmtiles` by URL — read incrementally via HTTP Range. Resolves
  // to the opened archive (read `.bounds` to frame). REPLACES the current coverage.
  loadArchiveUrl(url) { return this._sources.loadArchiveUrl(url); }

  // Update S-52 mariner settings. EVERY setting is applied CLIENT-SIDE from
  // baked per-feature attributes — an INSTANT restyle/filter, never a re-bake:
  // depth shading (SEABED01, DRVAL1/DRVAL2), soundings (SNDFRM04), shallow
  // pattern, contour labels, the safety-contour line + danger symbols/boundary,
  // display category (cat), and boundary symbolization (bnd). Tiles are baked
  // once and immutable. Colour scheme is separate (setScheme).
  setMariner(settings) {
    this._mariner = { ...this._mariner, ...settings };
    // tile57 engine mode: the display state lives in the engine-generated style, so a
    // toggle is an engine-computed diff applied in place (no JS in-place updaters).
    if (this._engineMode) { this._engineRestyle(); return; }
    const keys = Object.keys(settings);
    const map = this._map;
    if (!map) return;
    // Only touch the layer a changed setting actually affects. In particular,
    // DON'T re-set the soundings `icon-image` (a LAYOUT property → full symbol
    // re-layout) unless the safety depth changed — otherwise a paint-only
    // contour/four-shade change would needlessly re-layout every sounding.
    const fillKeys = ["shallowContour", "deepContour", "safetyContour", "fourShadeWater"];
    if (keys.some((k) => fillKeys.includes(k))) {
        this._eachLayer("areas", (id) => map.setPaintProperty(id, "fill-color", this.areasFillColor())); // cheap repaint
      }
      if (keys.includes("safetyDepth")) {
        this._eachLayer("soundings", (id) => map.setLayoutProperty(id, "icon-image", this.soundingsIconImage()));
      }
      // Depth units (metric ↔ imperial): re-layout the sounding numbers and the
      // depth-contour value labels into the chosen unit. Instant client restyle.
      if (keys.includes("depthUnit")) {
        this._eachLayer("soundings", (id) => map.setLayoutProperty(id, "icon-image", this.soundingsIconImage()));
        this._eachLayer("contour-labels", (id) => map.setLayoutProperty(id, "text-field", this.contourLabelField()));
        this._eachLayer("drgare-labels", (id) => map.setLayoutProperty(id, "text-field", this.drgareLabelField()));
      }
      // Distance unit: the S-52 scalebar reads in NM / km / mi — redraw it.
      if (keys.includes("distanceUnit")) this._renderScalebar();
      // Shallow pattern: visibility on its toggle (a fill layer).
      if (keys.includes("shallowPattern")) {
        this._eachLayer("shallow-pattern", (id) => this._setVis(id, this._mariner.shallowPattern ? "visible" : "none"));
      }
      // Safety contour: the shallow pattern, safety-contour line, and danger
      // foul boundary all key off it. Re-derive their base filters (setBaseFilter
      // keeps the category filter combined). Danger symbols swap
      // DANGER01↔DANGER02 — icon-image is a LAYOUT property (symbol re-layout),
      // but only danger features change image, far cheaper than the re-bake
      // this used to trigger.
      if (keys.includes("safetyContour")) {
        this.setBaseFilter("shallow-pattern", this.shallowPatternFilter());
        this.setBaseFilter("safety-contour", this.safetyContourFilter());
        this.setBaseFilter("danger-boundary", this.dangerBoundaryFilter());
        this._eachLayer("point_symbols", (id) => map.setLayoutProperty(id, "icon-image", this.pointSymbolImage()));
      }
      // Contour labels: just a visibility toggle on the DEPCNT label layer.
      if (keys.includes("showContourLabels")) {
        this._eachLayer("contour-labels", (id) => this._setVis(id, this._mariner.showContourLabels ? "visible" : "none"));
      }
      // No-data hatch (NODATA03 fill where there's no chart coverage): a plain
      // visibility toggle. Off → the basemap shows through where data ends.
      if (keys.includes("showNoData")) {
        this._eachLayer("nodata", (id) => this._setVis(id, this._mariner.showNoData === false ? "none" : "visible"));
      }
      if (keys.includes("showScaleBoundaries")) {
        this._eachLayer("scale-boundaries", (id) => this._setVis(id, this._mariner.showScaleBoundaries === false ? "none" : "visible"));
      }
      // S-52 individually-selectable "Other" items, each a plain visibility
      // toggle on its own layer (all default on): spot soundings, light
      // descriptions (LIGHTS06 text), and geographic names / object labels.
      if (keys.includes("showSoundings")) {
        this._eachLayer("soundings", (id) => this._setVis(id, this._mariner.showSoundings === false ? "none" : "visible"));
      }
      if (keys.includes("showLightDescriptions")) {
        this._eachLayer("light-text", (id) => this._setVis(id, this._mariner.showLightDescriptions === false ? "none" : "visible"));
      }
      // S-52 §14.5 text groups: re-derive the text layer's BASE filter (so it
      // survives a later applyFeatureFilters category re-apply) when any group
      // toggle (or light descriptions, which also feeds the general group-23
      // clause) changes. Instant — no re-bake.
      if (keys.some((k) => k === "textImportant" || k === "textNames" || k === "textOther" || k === "showLightDescriptions")) {
        const notLight = ["!=", ["get", "class"], "LIGHTS"];
        this.setBaseFilter("text", ["all", notLight, this.textGroupFilter()]);
      }
      // Display category (multi-select) and boundary symbolization both filter
      // every chart layer by a baked per-feature tag (cat / bnd) — re-apply the
      // combined feature filter. Instant — no re-bake.
      if (keys.some((k) => k === "displayBase" || k === "displayStandard" || k === "displayOther" || k === "boundaryStyle" || k === "simplifiedPoints" || k === "showFullSectorLines" || k === "showIsolatedDangersShallow" || k === "dataQuality" || k === "showMetaBounds" || k === "dateDependent" || k === "dateView" || k === "highlightDateDependent" || k === "showInformCallouts" || k === "viewingGroupsOff")) {
        this.applyFeatureFilters();
      }
  }

  // -- sprite / pattern registration --------------------------------------
  addImageData(id, imgData) {
    if (!imgData || this._map.hasImage(id)) return;
    try { this._map.addImage(id, imgData, { pixelRatio: 1 }); } catch (e) { console.warn("addImage", id, e); }
  }
  // Black-box triage: dump the GPU texture limit, the renderer, the number of
  // images packed into MapLibre's icon atlas, and the biggest one — plus any
  // pending WebGL error. If MAX_TEXTURE_SIZE is small (2048/4096) and the atlas
  // (or its widest/tallest packed row) approaches it, the atlas upload fails and
  // every symbol/pattern renders black. Logged once on load and on demand.
  _logGLDiag() {
    try {
      const map = this._map;
      const gl = map && map.painter && map.painter.context && map.painter.context.gl;
      if (!gl) { console.warn("[gldiag] no GL context"); return; }
      const dbg = gl.getExtension("WEBGL_debug_renderer_info");
      const renderer = dbg ? gl.getParameter(dbg.UNMASKED_RENDERER_WEBGL) : "(masked)";
      const maxTex = gl.getParameter(gl.MAX_TEXTURE_SIZE);
      let n = 0, big = "", bw = 0, bh = 0, maxW = 0, maxH = 0;
      for (const id of map.listImages() || []) {
        n++;
        const im = map.getImage(id);
        const d = (im && (im.data || im)) || {};
        const w = d.width || 0, h = d.height || 0;
        maxW = Math.max(maxW, w); maxH = Math.max(maxH, h);
        if (w * h > bw * bh) { big = id; bw = w; bh = h; }
      }
      console.log(
        `[gldiag] MAX_TEXTURE_SIZE=${maxTex} renderer="${renderer}" | ` +
        `images=${n} widest=${maxW} tallest=${maxH} biggest=${big}(${bw}x${bh})`
      );
      if (maxW > maxTex || maxH > maxTex) {
        console.error(`[gldiag] an image exceeds MAX_TEXTURE_SIZE (${maxTex}) → it WILL render black`);
      }
      const err = gl.getError();
      if (err) console.warn(`[gldiag] gl.getError()=0x${err.toString(16)} (1281=INVALID_VALUE, e.g. texture too large)`);
    } catch (e) { console.warn("[gldiag] failed", e); }
  }

  registerImage(id) {
    if (!this._sprites || this._map.hasImage(id)) return;
    let img = null;
    // Image CONSTRUCTION is delegated to SpriteBuilder; the element keeps only
    // the MapLibre registration (hasImage guard + addImageData side effect).
    try {
      img = this._sprites.imageFor(id);
    } catch (e) { console.warn("registerImage", id, e); }
    if (window.__chartImgLog) console.log(`[img] symbol ${id} ${img ? img.width + "x" + img.height : "BUILD-FAILED"}`);
    // NEVER leave a referenced icon-image unresolved — MapLibre's symbol
    // renderer can crash on a missing image (the `getx` atlas-lookup crash).
    // A failed/unknown symbol falls back to a blank 1×1 so the layer is inert.
    this.addImageData(id, img || new ImageData(1, 1));
  }
  registerAllSymbols() {
    if (!this._sprites) return;
    for (const name in this._sprite) {
      if (name === "_meta" || this._map.hasImage(name)) continue;
      try {
        const img = this._sprites.centredSymbol(name);
        if (img) this._map.addImage(name, img, { pixelRatio: 1 });
      } catch (e) { /* skip one bad symbol */ }
    }
  }
  // `name` is the bare PresLib pattern name; the image is registered under the
  // `pat:` namespace so it can't clash with a same-named point symbol.
  registerPattern(name) {
    if (!this._sprites) return;
    const id = PAT_PREFIX + name;
    if (this._map.hasImage(id)) return;
    const cell = this._patterns[name];
    if (!cell || cell.w === undefined) return;
    try { this._map.addImage(id, this._sprites.rawCell(this._patternsImg, cell), { pixelRatio: this._patternPixelRatio }); }
    catch (e) { console.warn("registerPattern", id, e); }
    if (window.__chartImgLog) console.log(`[img] pattern ${id} ${cell.w}x${cell.h}`);
  }
  registerAllPatterns() {
    if (!this._patternsImg) return;
    for (const name in this._patterns) {
      if (name === "_meta") continue;
      this.registerPattern(name);
    }
  }

  // Remove every registered pattern image so the styleimagemissing handler
  // re-registers it at the CURRENT _patternPixelRatio. Used after a calibration
  // change (setPxPitch) recomputes that ratio: the physical-size correction is
  // baked into a pattern's pixelRatio at registration and never updated in place
  // (registerPattern's hasImage guard), so a stale image keeps the old size.
  _dropPatternImages() {
    if (!this._map || !this._patterns) return;
    for (const name in this._patterns) {
      if (name === "_meta") continue;
      const id = PAT_PREFIX + name;
      if (this._map.hasImage(id)) {
        try { this._map.removeImage(id); } catch (e) { /* already gone */ }
      }
    }
  }

  // S-52 PresLib §14.5 text-group selection. Each text feature carries the baked
  // `tgrp` tag (the DISPLAY param of its TX/TE, §14.4); the mariner toggles which
  // groups are visible, independent of display category. Returns a MapLibre filter
  // expression selecting the enabled groups (false = hide all). Light descriptions
  // (group 23) are the LIGHTS layer's own toggle (showLightDescriptions); a stray
  // non-light group-23 label is folded in here too. Used by setMariner's text-group
  // re-derive; the layer builder calls S52.textGroupFilter directly.
  textGroupFilter() { return S52.textGroupFilter(this._mariner); }

  // The MapLibre chart layer/style BUILDING (layer templates, per-band/per-set
  // expansion, SCAMIN buckets, the _layerBase/_variants/_layerVis bookkeeping) is a
  // PURE function in chart-style.mjs now — buildChartLayers(state). See buildStyle().

  // Live layer ids for a template base id (one per band), for per-layer setting
  // updates. Falls back to the base id itself (basemap layers aren't expanded).
  _variantIds(baseId) {
    return (this._variants && this._variants[baseId]) || [baseId];
  }

  // Run fn(layerId) for each live band variant of a chart base layer id.
  _eachLayer(baseId, fn) {
    const map = this._map;
    if (!map) return;
    for (const id of this._variantIds(baseId)) if (map.getLayer(id)) fn(id);
  }

  // -- band on/off -----------------------------------------------------------
  // The usage band a chart layer belongs to, from its "<base>@<set-or-band>" id:
  // the per-band-pmtiles path suffixes the band slug directly; server mode
  // suffixes the set name ("noaa-d5-harbor"), decoded via bandOfSet.
  _bandOfLayerId(id) {
    const s = id.slice(id.lastIndexOf("@") + 1);
    return BAND_SLUGS.includes(s) ? s : bandOfSet(s);
  }

  // Set a chart layer's visibility, recording the intended (mariner) value and
  // forcing "none" while its band is turned off — so a mariner toggle can't
  // re-show a layer that sits inside a hidden band. Used by the mariner setters.
  _setVis(id, vis) {
    this._layerVis[id] = vis;
    if (this._bandsHidden.has(this._bandOfLayerId(id))) vis = "none";
    if (this._map) this._map.setLayoutProperty(id, "visibility", vis);
  }

  // Turn a whole usage band's chart layers on/off. Works in server and per-band
  // pmtiles modes (both render one source per band); the hidden set is also folded
  // into buildStyle so a basemap/set rebuild keeps the band off. Host-persisted.
  setBandVisible(band, visible) {
    if (visible) this._bandsHidden.delete(band); else this._bandsHidden.add(band);
    const map = this._map;
    if (!map || !map.getStyle) return;
    for (const l of (map.getStyle()?.layers || [])) { // style may be mid-reload (diff:false)
      if (!l.source || !String(l.source).startsWith("chart-")) continue;
      if (this._bandOfLayerId(l.id) !== band) continue;
      map.setLayoutProperty(l.id, "visibility", visible ? (this._layerVis[l.id] || "visible") : "none");
    }
  }

  // The usage bands currently turned off (for the host to persist / reflect in UI).
  bandsHidden() { return [...this._bandsHidden]; }

  // Build a chart variant's layout, folding band on/off into the template's
  // intended visibility and recording that intent for later restore — so a style
  // rebuild (basemap/server-set swap) keeps a turned-off band off.
  _variantLayout(L, band, id) {
    const vis = (L.layout && L.layout.visibility) || "visible";
    this._layerVis[id] = vis;
    return { ...(L.layout || {}), visibility: this._bandsHidden.has(band) ? "none" : vis };
  }
  buildStyle() {
    // tile57 ENGINE-STYLE MODE: on the native tile57 backend the MapLibre style comes
    // from the engine (/api/style.json), not the JS builder — ONE style source, no
    // drift (see _engineStyleMerged + tile57-style-adoption). The client grafts only its
    // own basemap + no-data + overlays onto the engine's chart layers. Otherwise (the Go
    // backend) build the style in JS as before.
    if (this._engineMode && this._engineStyle) return this._engineStyleMerged();
    return this._buildStyleJS();
  }

  // ---- tile57 engine-style mode -----------------------------------------

  // Merge the engine's /api/style.json (chart source + layers, sprite, glyphs) with the
  // client's own basemap + no-data + overlay chrome: reuse the JS builder purely for
  // that chrome (its chart layers/sources are dropped) and graft it UNDER the engine's
  // chart layers. The engine's own background is dropped — the chrome supplies one.
  _engineStyleMerged() {
    const engine = this._engineStyle;
    const js = this._buildStyleJS(); // bg + basemap + no-data (+ JS chart layers we drop)
    const isChart = (s) => typeof s === "string" && (s === "chart" || s.startsWith("chart-"));
    const chrome = js.layers.filter((l) => !isChart(l.source));
    const chromeSources = {};
    for (const [k, v] of Object.entries(js.sources)) if (!isChart(k)) chromeSources[k] = v;
    return {
      version: 8,
      glyphs: engine.glyphs || js.glyphs,
      sprite: engine.sprite,
      sources: { ...chromeSources, ...engine.sources },
      // chrome (bg → basemap → no-data) UNDER the engine chart layers; drop the engine's
      // own background so there is a single (client, scheme-aware) sea background.
      layers: [...chrome, ...engine.layers.filter((l) => l.type !== "background")],
    };
  }

  // Serialize the current mariner + scheme to the query /api/style.json & /api/style-diff
  // read (server marinerFromQuery): bools as 1/0, contours as numbers, viewingGroupsOff
  // as CSV, sizeScale from the calibrated physical scale.
  _marinerQuery() {
    const m = this._mariner, p = new URLSearchParams();
    p.set("scheme", this._active || "day");
    if (this._engineSet) p.set("set", this._engineSet); // target set (live "tile57" or a baked pack) → tiles URL + scamin

    const numK = (k) => { if (m[k] != null) p.set(k, String(m[k])); };
    const boolK = (k) => { if (m[k] != null) p.set(k, m[k] ? "1" : "0"); };
    numK("shallowContour"); numK("safetyContour"); numK("deepContour"); numK("safetyDepth");
    boolK("fourShadeWater");
    if (m.depthUnit) p.set("depthUnit", m.depthUnit);
    boolK("displayBase"); boolK("displayStandard"); boolK("displayOther");
    boolK("dataQuality"); boolK("showInformCallouts"); boolK("showMetaBounds"); boolK("showIsolatedDangersShallow");
    if (m.boundaryStyle) p.set("boundaryStyle", m.boundaryStyle);
    boolK("simplifiedPoints"); boolK("showFullSectorLines");
    boolK("textNames"); boolK("showLightDescriptions"); boolK("textOther");
    boolK("dateDependent"); boolK("highlightDateDependent");
    if (m.dateView) p.set("dateView", m.dateView);
    if (this._ignoreScamin) p.set("ignoreScamin", "1");
    if (this._scaminGate) p.set("scaminFilterGate", "1");
    p.set("sizeScale", String(this._featureSizeScale()));
    if (m.viewingGroupsOff && m.viewingGroupsOff.length) p.set("viewingGroupsOff", m.viewingGroupsOff.join(","));
    return p.toString();
  }

  // Fetch the full engine style for the current mariner (null on any failure).
  async _fetchEngineStyle() {
    try {
      const r = await fetch(this._assets + "api/style.json?" + this._marinerQuery(), { cache: "no-store" });
      if (!r.ok) return null;
      return await r.json();
    } catch { return null; }
  }

  // Probe + adopt the engine style at boot: if /api/style.json serves one (a -tags tile57
  // backend), cache it + record the mariner it reflects. 501/error → Go path (no-op).
  async _initEngineStyle(setNames) {
    // Target set for the engine style: the live "tile57" serve set, OR a single active
    // tile57-baked pack (so bake-and-serve tiles ALSO render from the engine style, not
    // the JS builder — one style, no drift). The engine style is one-source, so adopt it
    // only for a single set; a multi-pack install (no "tile57" set) falls back to the JS
    // builder. The server 404s /api/style.json for a non-tile57 pack, so the probe below
    // is the real gate — this just picks which set to ask about.
    const sets = setNames || ((this._sources && this._sources.serverSets && this._sources.serverSets()) || []);
    this._engineSet = sets.length === 1 ? sets[0] : (sets.includes("tile57") || sets.length === 0 ? "tile57" : null);
    if (!this._engineSet) return;
    const style = await this._fetchEngineStyle();
    if (style && Array.isArray(style.layers)) {
      this._engineStyle = style;
      this._engineMode = true;
      this._lastMariner = this._marinerQuery();
      // SCAMIN ladder (the boundary-crossing set) from the set's TileJSON, for the
      // filter-gate controller (_scaminUpdate). Best-effort — empty → gate shows all.
      this._engineScaminValues = await this._fetchScaminValues();
    }
  }

  // The distinct SCAMIN denominators the live tile57 set carries, ascending — the ~19
  // ladder boundaries the display scale crosses. From the set's TileJSON `scamin` array.
  async _fetchScaminValues() {
    try {
      const r = await fetch(this._assets + "tiles/" + (this._engineSet || "tile57") + ".json", { cache: "no-store" });
      if (!r.ok) return [];
      const tj = await r.json();
      return Array.isArray(tj.scamin) ? tj.scamin.slice().sort((a, b) => a - b) : [];
    } catch (e) { return []; }
  }

  // A mariner/scheme change in engine mode: fetch the engine-computed diff (last-applied
  // → current mariner) and apply the ops IN PLACE — no setStyle, no tile reload, no
  // overlay churn, no flicker. A `rebuild` op (layer set changed) falls back to a full
  // re-fetch. Keeps _engineStyle current so a later full rebuild is correct.
  // DEBOUNCED trigger. The shell pushes several settings at boot (scheme, mariner,
  // pxPitch) and a user can flip toggles fast; without coalescing, EACH call would fetch
  // + apply a full diff — on the old bucket style that was ~1200 setFilter ops per call,
  // ×N calls = the "thousands of setFilter, page won't render" storm. Coalesce to ONE
  // diff against the last-applied mariner.
  _engineRestyle() {
    if (!this._engineMode || !this._map) return;
    clearTimeout(this._engineRestyleT);
    this._engineRestyleT = setTimeout(() => this._engineRestyleNow(), 40);
  }

  async _engineRestyleNow() {
    if (!this._engineMode || !this._map) return;
    // Don't mutate the style mid-load (setFilter/setStyle re-enters MapLibre's run loop →
    // "already running"). Defer once the map settles; the boot style already reflects the
    // boot mariner, so nothing is lost by waiting.
    if (!this._map.isStyleLoaded()) { this._map.once("idle", () => this._engineRestyle()); return; }
    const to = this._marinerQuery();
    const from = this._lastMariner != null ? this._lastMariner : to;
    if (from === to) { this._lastMariner = to; return; }
    try {
      const r = await fetch(this._assets + "api/style-diff", {
        method: "POST", headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ from, to }),
      });
      if (!r.ok) throw new Error("style-diff HTTP " + r.status);
      const ops = await r.json();
      if (ops.length === 1 && ops[0].op === "rebuild") { await this._engineRebuild(); return; }
      for (const op of ops) this._applyStyleOp(op);
      this._applyOpsToCached(ops);
      this._lastMariner = to;
      // The diff rewrote gated layers' filters back to curDenom=0 (show-all) — re-inject
      // the live SCAMIN cutoff so a toggle doesn't reveal everything until the next zoom.
      this._scaminLayersCache = null;
      this._scaminUpdate(true);
    } catch (e) {
      console.warn("[engine-restyle] full rebuild:", e.message);
      await this._engineRebuild();
    }
  }

  // Re-inject the current display-scale denominator (curDenom) into every gated chart
  // layer's SCAMIN clause, but ONLY when curDenom has crossed a SCAMIN-ladder boundary
  // since the last apply (≤19 boundaries across a full zoom sweep — usually 0 per move).
  // curDenom is the physical display-scale denominator (zoom + lat + calibrated pxPitch —
  // the same scale the HUD readout shows). This is the client half of scamin-layers.md.
  _scaminUpdate(force) {
    // Only in engine mode, and only once the style is loaded (getStyle() is undefined
    // and setFilter throws before that — the `move`/`load` hooks can fire mid-load).
    if (!this._scaminGate || !this._engineMode || !this._map) return;
    if (!this._map.isStyleLoaded || !this._map.isStyleLoaded()) return;
    // Engine mode gets the ladder from the tile57 set TileJSON; the JS builder gets it
    // from the chart-source manager (values discovered from the loaded tiles' manifest).
    const values = this._engineMode ? this._engineScaminValues : ((this._sources && this._sources.scaminValues) || []);
    const denom = scaleDenomPhysical(this._map.getZoom(), this._map.getCenter().lat, this._pxPitch);
    let band = 0;
    for (const v of values) if (v < denom) band++;
    if (!force && band === this._scaminBandLast) return;
    this._scaminBandLast = band;
    const map = this._map;
    for (const id of this._scaminGatedLayers()) {
      const f = map.getFilter(id);
      if (!f) continue;
      const nf = JSON.parse(JSON.stringify(f));
      if (setScaminDenom(nf, denom)) { try { map.setFilter(id, nf); } catch { /* ignore */ } }
    }
  }

  // Cached ids of the chart layers whose filter carries the SCAMIN clause (the gated
  // layers). Recomputed after a diff/rebuild (cache nulled) — ~16 layers, cheap to scan.
  _scaminGatedLayers() {
    if (this._scaminLayersCache) return this._scaminLayersCache;
    const style = this._map && this._map.getStyle();
    if (!style || !style.layers) return []; // style not ready yet — don't cache the empty result
    const out = [];
    for (const l of style.layers) {
      if (l.filter && setScaminDenom(JSON.parse(JSON.stringify(l.filter)), 0, true)) out.push(l.id);
    }
    return (this._scaminLayersCache = out);
  }

  // Apply one engine diff op to the live map (ops only reference engine chart layers;
  // a stray unknown layer just no-ops in the try/catch).
  _applyStyleOp(op) {
    const map = this._map; if (!map) return;
    try {
      if (op.op === "setFilter") map.setFilter(op.layer, op.value ?? null);
      else if (op.op === "setPaintProperty") map.setPaintProperty(op.layer, op.property, op.value ?? null);
      else if (op.op === "setLayoutProperty") map.setLayoutProperty(op.layer, op.property, op.value ?? null);
    } catch { /* layer not in the live style — ignore */ }
  }

  // Keep the cached engine style consistent with applied ops, so a later FULL rebuild
  // (basemap change, calibration) reflects the current mariner, not the boot one.
  _applyOpsToCached(ops) {
    const byId = {};
    for (const l of this._engineStyle.layers) byId[l.id] = l;
    for (const op of ops) {
      const l = byId[op.layer]; if (!l) continue;
      if (op.op === "setFilter") { if (op.value == null) delete l.filter; else l.filter = op.value; }
      else if (op.op === "setPaintProperty") { l.paint = l.paint || {}; if (op.value == null) delete l.paint[op.property]; else l.paint[op.property] = op.value; }
      else if (op.op === "setLayoutProperty") { l.layout = l.layout || {}; if (op.value == null) delete l.layout[op.property]; else l.layout[op.property] = op.value; }
    }
  }

  // Full engine-style rebuild: re-fetch for the current mariner + setStyle(diff:true) so
  // MapLibre applies the delta without flicker; overlays self-heal on style.load.
  async _engineRebuild() {
    const style = await this._fetchEngineStyle();
    if (!style || !this._map) return;
    this._engineStyle = style;
    // Don't setStyle mid-load (re-entrancy → "already running"); defer to the next idle.
    if (!this._map.isStyleLoaded()) { this._map.once("idle", () => { this._map.setStyle(this.buildStyle(), { diff: true }); this._lastMariner = this._marinerQuery(); this._scaminLayersCache = null; this._scaminUpdate(true); }); return; }
    this._map.setStyle(this.buildStyle(), { diff: true });
    this._lastMariner = this._marinerQuery();
    this._scaminLayersCache = null;
    this._scaminUpdate(true); // re-inject the SCAMIN cutoff after the new style loads
  }

  // The JS S-52 style builder — the Go-backend render path. Assembles the chart layers
  // (buildChartLayers, from baked per-feature tags) + basemap + no-data. On the tile57
  // backend this is superseded by the engine style; _engineStyleMerged still calls it to
  // reuse the client's own basemap/no-data chrome (grafting it onto the engine layers).
  _buildStyleJS() {
    // The CHART band + server-set sources (per-band prebaked sources in BOTH modes,
    // plus one source per active server pack) are assembled by the chart-source
    // manager, keyed by its cache-bust token. The element then adds the basemap +
    // no-data sources/layers below (which read coastline/osmvec/palette state, not
    // chart-source state).
    const sources = this._sources.sourcesDict(this._sources.ver);
    const layers = [{ id: "bg", type: "background", paint: { "background-color": this.seaColor() } }];

    // CHART layers + the three bookkeeping maps come from the PURE builder. The
    // element resolves the SCAMIN reference latitude (manager value, else the map
    // centre, else 0) before calling, then ASSIGNS the returned maps so the live
    // updaters (applyFeatureFilters/setBaseFilter/_eachLayer/_setVis/setBandVisible/
    // setScheme/setMariner) keep reading them unchanged.
    const scaminLat = this._sources.scaminLat != null ? this._sources.scaminLat
                      : (this._map ? this._map.getCenter().lat : 0);
    // True-physical feature sizing: scale the baked (point-pixel) sizes to this
    // screen. Recompute the pattern raster ratio to match, so AP fills register at
    // the same physical size when styleimagemissing re-fires after setStyle.
    const sizeScale = this._featureSizeScale();
    this._patternPixelRatio = (0.08 / FEATURE_SCALE) / sizeScale;
    const { layers: chartLayers, layerBase, variants, layerVis } = buildChartLayers({
      mariner: this._mariner, palette: this._palette(), atlasPpu: this._atlasPpu, osm: this._osmBasemap(),
      scheme: this._active,
      server: this._sources.server, serverSets: this._sources.sets,
      scaminValues: this._sources.scaminValues, scaminLat, bandsHidden: this._bandsHidden,
      bandsPresent: new Set(this._sources.loadedBands()),
      ignoreScamin: this._ignoreScamin, sizeScale, pxPitch: this._pxPitch,
    });
    this._layerBase = layerBase; this._variants = variants; this._layerVis = layerVis;

    const basemap = this.getAttribute("basemap") || "none";
    if (basemap === "osm") {
      sources.osm = { type: "raster", tileSize: 256, maxzoom: 19, tiles: ["https://tile.openstreetmap.org/{z}/{x}/{y}.png"], attribution: "© OpenStreetMap contributors" };
      layers.push({ id: "osm", type: "raster", source: "osm", paint: this._osmRasterPaint() });
    } else if (basemap === "osmvec" && this._osmvecArchive) {
      // Hosted OSM vector (Protomaps schema). Styled per source-layer (no kind
      // filters) so it works across Protomaps schema versions, tinted to the
      // active S-52 scheme so it reads as a muted underlay beneath the chart.
      sources.osmvec = { type: "vector", tiles: ["osmvec://{z}/{x}/{y}"], minzoom: this._osmvecArchive.minZoom, maxzoom: this._osmvecArchive.maxZoom, attribution: "© OpenStreetMap contributors" };
      const ink = this.coastColor();
      layers.push(
        { id: "ov-earth", type: "fill", source: "osmvec", "source-layer": "earth", paint: { "fill-color": this.landColor() } },
        { id: "ov-landuse", type: "fill", source: "osmvec", "source-layer": "landuse", minzoom: 6, paint: { "fill-color": this.landColor(), "fill-opacity": 0.5 } },
        { id: "ov-water", type: "fill", source: "osmvec", "source-layer": "water", paint: { "fill-color": this.seaColor() } },
        { id: "ov-roads", type: "line", source: "osmvec", "source-layer": "roads", minzoom: 7, paint: { "line-color": ink, "line-opacity": 0.35, "line-width": ["interpolate", ["linear"], ["zoom"], 7, 0.3, 14, 1.4] } },
        { id: "ov-boundaries", type: "line", source: "osmvec", "source-layer": "boundaries", paint: { "line-color": ink, "line-opacity": 0.4, "line-dasharray": [2, 2], "line-width": 0.7 } },
        { id: "ov-places", type: "symbol", source: "osmvec", "source-layer": "places", layout: { "text-field": ["coalesce", ["get", "name:en"], ["get", "name"]], "text-font": ["Noto Sans Regular"], "text-size": ["interpolate", ["linear"], ["zoom"], 4, 10, 10, 13] }, paint: { "text-color": this.textColor(), "text-halo-color": this.textHaloColor(), "text-halo-width": 1.2 } },
      );
    } else if ((basemap === "coastline" || basemap === "gshhg") && (this._coastlineArchive || this._coastline)) {
      // Offline GSHHG land/lake polygons (see emit-basemap*). Land fills over
      // the sea-coloured background; lakes (level 2) punch back to sea; a thin
      // coastline stroke traces level-1 shores. All scheme-aware. Prefer the
      // tiled vector basemap (coastline.pmtiles, source-layer "coastline",
      // overzoomed above its baked max); fall back to the flat geojson blob.
      const srcLayer = {};
      if (this._coastlineArchive) {
        sources.coastline = { type: "vector", tiles: ["coastline://{z}/{x}/{y}"], minzoom: this._coastlineArchive.minZoom, maxzoom: this._coastlineArchive.maxZoom };
        srcLayer["source-layer"] = "coastline";
      } else {
        sources.coastline = { type: "geojson", data: this._coastline };
      }
      layers.push(
        { id: "coast-land", type: "fill", source: "coastline", ...srcLayer, filter: ["==", ["get", "level"], 1], paint: { "fill-color": this.landColor() } },
        { id: "coast-lake", type: "fill", source: "coastline", ...srcLayer, filter: ["==", ["get", "level"], 2], paint: { "fill-color": this.seaColor() } },
        // Coastline stroke matched to the NOAA ENC coastline (CSTLN): heavier at
        // the general/overview scale where the chart's coastline reads thick, then
        // tapered as you zoom in so detailed shores don't get clobbered.
        { id: "coast-line", type: "line", source: "coastline", ...srcLayer, filter: ["<=", ["get", "level"], 2], paint: { "line-color": this.coastColor(), "line-width": ["interpolate", ["linear"], ["zoom"], 3, 2.2, 8, 1.8, 12, 1.4, 16, 1.2] } },
      );
    }

    // S-52 "no data" pattern (NODATA03) across the whole world, ABOVE the basemap
    // but BELOW the chart layers: ENC area fills paint over it wherever we have
    // cell coverage, so any area WITHOUT chart data reads as the standard no-data
    // hatch instead of looking like surveyed sea. (The pattern image loads lazily
    // via `styleimagemissing` → registerPattern.)
    sources.nodata = { type: "geojson", data: { type: "Feature", properties: {}, geometry: { type: "Polygon", coordinates: [[[-180, -85.0511], [180, -85.0511], [180, 85.0511], [-180, 85.0511], [-180, -85.0511]]] } } };
    // No-data hatch is hidden over OSM (its land/water fills the gaps instead).
    const hideNoData = this._mariner.showNoData === false || basemap === "osm" || basemap === "osmvec";
    layers.push({ id: "nodata", type: "fill", source: "nodata", layout: { visibility: hideNoData ? "none" : "visible" }, paint: { "fill-pattern": PAT_PREFIX + "NODATA03" } });

    return {
      version: 8,
      glyphs: this._assets + "glyphs/{fontstack}/{range}.pbf",
      sources,
      layers: layers.concat(chartLayers),
    };
  }
}

// The S-52 halign/valign → text-anchor mapping and the collision sort-key live in
// chart-style.mjs now (one data-driven `text` layer); setScheme/setMariner restyle
// and refilter that single id directly.

// Custom element names must contain a hyphen (HTML spec) — `<chart-plotter>`.
// Find the tile57 filter-gate SCAMIN clause `[">=", ["coalesce",["get","scamin"],1e12], N]`
// anywhere in a MapLibre filter and set its literal N to `denom` (the current display-scale
// denominator). detectOnly=true just reports whether the clause is present (no mutation).
// Returns true if the clause was found. Mutates `node` in place.
function setScaminDenom(node, denom, detectOnly) {
  if (!Array.isArray(node)) return false;
  if (node[0] === ">=" && Array.isArray(node[1]) && node[1][0] === "coalesce"
      && Array.isArray(node[1][1]) && node[1][1][0] === "get" && node[1][1][1] === "scamin") {
    if (!detectOnly) node[2] = denom;
    return true;
  }
  let found = false;
  for (const c of node) if (setScaminDenom(c, denom, detectOnly)) found = true;
  return found;
}

customElements.define("chart-canvas", ChartCanvas);
