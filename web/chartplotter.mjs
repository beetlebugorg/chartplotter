// <chartplotter> — the public web component.
//
// A self-contained S-52 ENC chart plotter: a MapLibre map whose vector tiles are
// baked SERVER-SIDE and served from /tiles/{set} (or read from a hosted .pmtiles).
// Drop the tag on a page:
//
//   <script type="module" src="chartplotter.mjs"></script>
//   <chart-plotter center="-76.4875,38.975" zoom="13" tiles="server" set="charts"></chart-plotter>
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
//   basemap  "osm" | "none"       street raster under the chart (default none)
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
// There is no in-browser baking; the wasm baker has been retired (server migration).
import { PMTilesArchive, MultiArchive, registerPmtilesProtocol } from "./pmtiles-source.mjs";

const FALLBACK = "#ff00ff";
const FEATURE_SCALE = 0.01 / 0.35278;
const FONT = ["Noto Sans Regular"];
const M_TO_FT = 3.280839895; // depth-unit conversion (metric ↔ imperial)
// Web-Mercator zoom that renders a paper scale of 1:`scale` at `lat` — the
// inverse of the HUD's scaleDenom (mpp = 156543.034·cos φ / 2^z; scale = mpp/
// 0.00028). Latitude-dependent because a given scale is a different zoom at each
// latitude. Clamped to [0,24]; the map's own max-zoom further caps over-fine views.
function zoomForScale(scale, lat) {
  if (!(scale > 0)) return 0;
  const z = Math.log2(156543.03392804097 * Math.cos((lat * Math.PI) / 180) / (0.00028 * scale));
  return Math.max(0, Math.min(24, z));
}
// S-57 meta objects whose boundary draws as a region/coverage line (nautical
// publication, nav-system, coverage, compilation scale). These are administrative
// indicators (S-52 PresLib gives M_NPUB a line only as a pick-report hint); they
// read as "cell boundaries", so they get their own gate (mariner.showMetaBounds),
// off by default, rather than riding the "Other" display category. M_QUAL is NOT
// here — it has its own "Data quality" (CATZOC) toggle.
const META_BOUND_CLASSES = ["M_NPUB", "M_NSYS", "M_COVR", "M_CSCL"];
// Fill-pattern (AP) images live under this id prefix so they never collide with
// point-symbol (SY) images of the SAME PresLib name. Several names are BOTH a
// point symbol and an area fill pattern (QUESMRK1, AIRARE02, FSHFAC03, MARCUL02):
// e.g. an unknown object is SY(QUESMRK1) — a 26×46 "?" mark — while an unknown
// AREA could be AP(QUESMRK1) — a 178×392 tiled "?" fill. MapLibre keys images by a
// single id, so without this prefix the pattern atlas cell hijacked the symbol
// (styleimagemissing fires before registerAllSymbols → pattern won, first-wins),
// rendering the point "?" as a stretched fragment. Symbols keep their bare names.
const PAT_PREFIX = "pat:";

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
const CHART_BANDS = [
  { slug: "overview", min: 0, max: 8, bake: 8 },
  { slug: "general", min: 8, max: 10, bake: 10 },
  { slug: "coastal", min: 10, max: 12, bake: 14 },
  { slug: "approach", min: 12, max: 14, bake: 14 },
  { slug: "harbor", min: 14, max: 16, bake: 16 },
  { slug: "berthing", min: 16, max: 18, bake: 18 },
  { slug: "all", min: 0, max: 18, bake: 18 },
];

// Lowest display zoom each band's chart layers actually DRAW at — the scale where
// that band becomes the best-available chart, per the NOAA ENC scheme (ENC Design
// Handbook Table 1: two standard scales per usage band ≈ two web-Mercator zooms;
// e.g. Approach 1:90k/1:45k ⇒ shows ~z12–14 ≈ 1:130k–1:32k at mid-US latitudes).
// Overview/general draw from z0 so they gap-fill on zoom-out; the finer bands start
// at their band so they don't appear a full zoom (≈½ band) too coarse. Applied as a
// LAYER minzoom (the baked source may serve lower) so it works without a re-bake.
const BAND_DISPLAY_MIN = { overview: 0, general: 0, coastal: 10, approach: 12, harbor: 14, berthing: 16, all: 0 };

// Chart layers whose features carry SCAMIN and are split into per-SCAMIN bucket
// layers (each with a native fractional minzoom) so SCAMIN is honored EXACTLY with
// zero per-zoom work. Point symbols + soundings are the marks that "disappear too
// soon / too late" at scale boundaries; their visibility must track display scale.
const SCAMIN_BUCKET_LAYERS = new Set(["point_symbols", "soundings"]);

// The display zoom at which a 1:N (scamin) feature first becomes visible at the
// given latitude: the zoom whose display-scale denominator equals scamin. FRACTIONAL
// — used directly as a MapLibre layer minzoom, which gives the exact S-52 §8.4
// cutoff (display scale ≤ SCAMIN ⇒ shown) with no client-side per-zoom computation.
function scaminDisplayZoom(scamin, lat) {
  if (!scamin) return 0;
  const denomZ0 = 559082264.029 * Math.cos((lat * Math.PI) / 180);
  if (denomZ0 <= scamin) return 0;
  return Math.max(0, Math.min(24, Math.log2(denomZ0 / scamin)));
}

// Server sets are baked PER BAND, named "<district>-<band>" (e.g. noaa-d5-general).
// bandOfSet recovers the band slug from a set name ("all" for a bandless/merged set
// — a user upload or a legacy pack). BAND_RANK orders sets coarse→fine so a finer
// band's fill draws over a coarser one (the same stacking the per-band pmtiles path
// gets for free). Both let server mode do per-band overzoom + suppression, so a
// coarse-only spot (open water) is filled by the general/overview source overzooming
// instead of blanking to the S-52 no-data hatch.
const BAND_SLUGS = CHART_BANDS.map((b) => b.slug).filter((s) => s !== "all");
const BAND_RANK = Object.fromEntries(CHART_BANDS.map((b, i) => [b.slug, i]));
function bandOfSet(name) {
  const i = name.lastIndexOf("-");
  if (i > 0) { const s = name.slice(i + 1); if (BAND_SLUGS.includes(s)) return s; }
  return "all";
}

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

export class ChartPlotter extends HTMLElement {
  constructor() {
    super();
    this.attachShadow({ mode: "open" });
    this._colortables = {};
    this._linestyles = {};
    this._sprite = {};
    this._patterns = {};
    this._atlasPpu = 0.08;
    this._active = "day";
    this._spriteImg = null;
    this._patternsImg = null;
    this._ver = 0;          // chart-tile cache-bust token (see refresh)
    this._coastline = null; // offline GSHHG basemap GeoJSON fallback, if available
    this._coastlineArchive = null; // offline GSHHG coastline PMTiles (preferred vector basemap)
    this._mariner = {};      // current mariner settings (engine-side)
    this._layerBase = {};    // chart layer id → intrinsic (pre-category) filter
    this._bands = {};        // band slug → MultiArchive of that band's loaded packs (chart-<slug> source)
    this._scaminValues = []; // distinct SCAMIN denominators seen in tiles → per-SCAMIN bucket layers
    this._scaminLat = null;  // latitude the bucket minzooms were computed at (rebuild on big change)
    this._server = false;    // server-tiles mode (tiles="server"): chart sources are /tiles/{set}
    this._serverSets = [];   // active server packs: [{name, min, max}] — one vector source each
    this._bandsHidden = new Set(); // usage bands turned off via setBandVisible (host-persisted)
    this._layerVis = {};     // chart layer id → intended (mariner) visibility, so band on/off restores it
  }

  // Absolute tile-URL template for a server set. MUST be absolute: MapLibre fetches
  // tiles in a Web Worker that has no document base, so a relative "/tiles/…" URL
  // throws "Failed to parse URL".
  _serverTilesUrl(name) {
    const base = new URL(this._assets, location.href).href; // absolute, trailing "/"
    return `${base}tiles/${name}/{z}/{x}/{y}.mvt`;
  }

  // Fetch a set's real zoom range from its TileJSON → {name, min, max}. The source
  // maxzoom MUST be the set's actual deepest baked zoom: if it claims more (e.g. a
  // fixed 18 when a harbor cell only bakes to z16), MapLibre requests tiles past the
  // bake (empty → no-data holes) instead of overzooming the deepest real tile.
  async _fetchSetMeta(name) {
    // `tiles` is the server's TileJSON tile-URL template, which carries the bake
    // GENERATION (?g=<mtime>) — re-fetching this JSON (it's no-cache) after a re-bake
    // yields a new URL, so pointing the source at it bypasses every tile cache by
    // content. Falls back to the plain URL if the server omits it.
    const meta = { name, band: bandOfSet(name), min: 0, max: 18, tiles: this._serverTilesUrl(name) };
    try {
      const base = new URL(this._assets, location.href).href;
      const tj = await fetch(`${base}tiles/${name}.json`).then((r) => (r.ok ? r.json() : null));
      if (tj) {
        if (Number.isFinite(tj.minzoom)) meta.min = tj.minzoom;
        if (Number.isFinite(tj.maxzoom)) meta.max = tj.maxzoom;
        if (Array.isArray(tj.tiles) && tj.tiles[0]) meta.tiles = tj.tiles[0];
      }
    } catch (e) { /* keep defaults */ }
    return meta;
  }

  // Fetch every set's zoom range + band, ordered coarse→fine so the per-band fills
  // stack correctly in expandChartLayers (template-outer, set-inner draw order).
  async _loadSetMetas(names) {
    const metas = await Promise.all(names.map((n) => this._fetchSetMeta(n)));
    metas.sort((a, b) => (BAND_RANK[a.band] ?? 99) - (BAND_RANK[b.band] ?? 99));
    return metas;
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

    // Shadow DOM: MapLibre CSS must live inside the shadow root, plus a sized
    // map container.
    const style = document.createElement("style");
    style.textContent =
      ":host{display:block;position:relative}#map{position:absolute;inset:0;background:#93aebb}" +
      // S-52 SCALEB-style scalebar (horizontal striped NM bar, bottom-left).
      ".s52-scalebar{display:flex;flex-direction:column;align-items:flex-start;margin:0 0 8px 10px;pointer-events:none;user-select:none}" +
      ".s52sb-label{font:700 11px/1.2 system-ui,sans-serif;color:#1a2026;background:rgba(255,255,255,.82);padding:1px 5px;border-radius:4px;margin-bottom:3px;box-shadow:0 1px 3px rgba(0,0,0,.2);font-variant-numeric:tabular-nums}" +
      ".s52sb-bar{display:flex;flex-direction:row;height:8px;min-width:8px;border:1px solid #1a2026;box-sizing:border-box;box-shadow:0 1px 3px rgba(0,0,0,.3)}" +
      ".s52sb-bar span{flex:1;display:block}";
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
    const [ct, sj, lsj, pj] = await Promise.all([
      fetch(assets + "colortables.json").then((r) => r.json()),
      fetch(assets + "sprite.json").then((r) => r.json()),
      fetch(assets + "linestyles.json").then((r) => r.json()).catch(() => ({})),
      fetch(assets + "patterns.json").then((r) => r.json()).catch(() => ({})),
    ]);
    this._colortables = ct;
    this._sprite = sj;
    this._linestyles = lsj;
    this._patterns = pj;
    this._atlasPpu = (sj._meta && sj._meta.px_per_unit) || this._atlasPpu;
    this._patternPixelRatio = 0.08 / FEATURE_SCALE;

    this._spriteImg = new Image();
    this._spriteImg.src = assets + "sprite.png";
    this._patternsImg = new Image();
    this._patternsImg.src = assets + "patterns.png";
    await Promise.all([
      this._spriteImg.decode().catch(() => {}),
      this._patternsImg.decode().catch(() => {}),
    ]);

    // Offline basemap: load the GSHHG-derived coastline if this map uses it
    // (best-effort — absent → plain sea bg). Prefer the tiled vector basemap
    // (coastline.pmtiles: sharper, loads by viewport, overzooms crisply); fall
    // back to the flat coastline.geojson blob when the tileset isn't present.
    const basemap = this.getAttribute("basemap") || "none";
    if (basemap === "coastline" || basemap === "gshhg") {
      this._coastlineArchive = await new PMTilesArchive(assets + "basemap/coastline.pmtiles").init().catch(() => null);
      if (!this._coastlineArchive) {
        this._coastline = await fetch(assets + "basemap/coastline.geojson")
          .then((r) => (r.ok ? r.json() : null))
          .catch(() => null);
        if (!this._coastline) console.warn("[chartplotter] no offline coastline basemap (basemap/coastline.pmtiles or .geojson)");
      }
    }
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
    this._server = this.getAttribute("tiles") === "server";
    if (this._server) {
      const names = (this.getAttribute("sets") || this.getAttribute("set") || "")
        .split(",").map((s) => s.trim()).filter(Boolean);
      // Learn each set's real zoom range before the first buildStyle so the source
      // maxzoom is truthful (overzoom, not empty-tile holes).
      this._serverSets = await this._loadSetMetas(names);
    }

    // Per-band prebaked sources (chart-<slug>), one PMTiles protocol each. Each
    // carries its own maxzoom so MapLibre client-overzooms a coarse band up into
    // finer display zooms (coastal z11 → z18 offshore). Used by the prebaked path;
    // harmless (blank) in server mode.
    for (const band of CHART_BANDS) {
      const slug = band.slug;
      registerPmtilesProtocol(maplibregl, "chart-" + slug, () => this._bands[slug]);
    }

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
      // Attribution bottom-left so the bottom-right corner is free for the app's
      // scale/zoom readout.
      attributionControl: { position: "bottom-left" },
    });
    this._map = map;
    this.map = map; // public handle

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
    map.addControl({ onAdd: () => this._scaleEl, onRemove: () => { this._scaleEl = null; } }, "bottom-left");
    map.on("move", () => this._renderScalebar());

    map.on("styleimagemissing", (e) => {
      // Pattern images are requested under the `pat:` namespace (fill-pattern
      // exprs add the prefix); everything else is a point/sounding symbol.
      if (e.id.startsWith(PAT_PREFIX)) this.registerPattern(e.id.slice(PAT_PREFIX.length));
      else this.registerImage(e.id);
    });
    // Learn the distinct SCAMIN values from tiles as they load → per-SCAMIN bucket
    // layers. Only on idle (tiles settled), and it rebuilds the style ONLY when the
    // value set grows or the centre latitude shifts enough to move a bucket minzoom
    // — never per zoom. Once converged, MapLibre gates the buckets natively for free.
    map.on("idle", () => this._refreshScaminBuckets());
    map.on("load", async () => {
      this._renderScalebar(); // initial draw (the move hook only fires on movement)
      try {
        this.registerAllSymbols();
        this.registerAllPatterns();
        map.triggerRepaint();
      } catch (err) {
        console.warn("[chartplotter] register", err);
      }
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

  // -- colour --------------------------------------------------------------
  // Resolve a single S-52 colour token for the active scheme (concrete value,
  // not an expression) — used for basemap layers whose colour is fixed.
  token(name, fallback) {
    const t = this._colortables[this._active] || {};
    return t[name] || fallback;
  }
  seaColor() { return this.token("DEPDW", "#93aebb"); }   // deep water / sea backdrop
  landColor() { return this.token("LANDA", "#e0d9b8"); }  // S-52 land area
  coastColor() { return this.token("CSTLN", "#5a5a44"); } // coastline stroke

  colorExpr(prop, fallback) {
    return this.colorMatch(["coalesce", ["get", prop], ""], fallback);
  }

  // Resolve a colour-token-valued expression to an RGB for the active scheme.
  colorMatch(tokenExpr, fallback) {
    const t = this._colortables[this._active] || {};
    const m = ["match", tokenExpr];
    let n = 0;
    for (const tok in t) { m.push(tok, t[tok]); n++; }
    m.push(fallback || FALLBACK);
    return n ? m : (fallback || FALLBACK);
  }

  // Legible chart-text colour. S-52's dusk/night palettes dim the text inks
  // (CHBLK/CHGRD) to near-black, which is unreadable on the equally dark scheme
  // — a halo can't help because the glyph *body* itself vanishes. So at
  // dusk/night we render text in a bright neutral (legibility over strict
  // night-vision dimming, per user request) and pair it with a dark halo
  // (textHaloColor). Day keeps the per-feature S-52 ink (so coloured labels
  // stay semantic) over a light halo.
  textColor() {
    if (this._active === "day") return this.colorExpr("color_token", "#000000");
    return this._active === "night" ? "#aab7bf" : "#dde7ec";
  }
  // Backing that contrasts with textColor: light under day's dark inks, dark
  // under the bright dusk/night ink. Applied to ALL text — the old bake gated
  // the halo to ≥10 px glyphs, leaving small labels bare.
  textHaloColor() {
    return this._active === "day" ? "rgba(255,255,255,0.9)" : "rgba(0,0,0,0.85)";
  }
  // Contour (depth) labels: S-52 CHGRD by day, bright neutral at dusk/night so
  // they stay legible like the rest of the chart text.
  contourLabelColor() {
    if (this._active === "day") return this.token("CHGRD", "#5a5a44");
    return this._active === "night" ? "#aab7bf" : "#dde7ec";
  }

  // SEABED01 (S-52 §13.2.15) as a data-driven expression: a depth area's
  // DRVAL1/DRVAL2 vs the mariner's shallow/safety/deep contours → a depth
  // colour token. Done client-side so dragging the contours is an instant
  // restyle, not a re-bake. Deepest band first (the spec cascade's last match
  // wins → first match in a `case`). `>= X && > X` on both bounds per the spec.
  seabedTokenExpr() {
    const m = this._mariner;
    const shc = m.shallowContour ?? 2, sfc = m.safetyContour ?? 10, dpc = m.deepContour ?? 30;
    const d1 = ["coalesce", ["get", "drval1"], -1];
    const d2 = ["coalesce", ["get", "drval2"], 0];
    const band = (x) => ["all", [">=", d1, x], [">", d2, x]];
    if (m.fourShadeWater === false) {
      return ["case", band(sfc), "DEPDW", band(0), "DEPVS", "DEPIT"];
    }
    return ["case",
      band(dpc), "DEPDW",
      band(sfc), "DEPMD",
      band(shc), "DEPMS",
      band(0), "DEPVS",
      "DEPIT"];
  }

  // Fill colour for the `areas` layer: depth areas (carry drval1) shade live via
  // SEABED01; everything else uses its baked colour token.
  areasFillColor() {
    return ["case",
      ["has", "drval1"], this.colorMatch(this.seabedTokenExpr()),
      this.colorExpr("color_token")];
  }

  // SHALLOW_PATTERN filter: depth areas on the shallow side of the live safety
  // contour — SEABED01's SHALLOW flag, i.e. NOT (drval1 ≥ SFC && drval2 > SFC).
  shallowPatternFilter() {
    const sfc = this._mariner.safetyContour ?? 10;
    return ["all",
      ["has", "drval1"],
      ["!", ["all", [">=", ["get", "drval1"], sfc], [">", ["coalesce", ["get", "drval2"], ["get", "drval1"]], sfc]]]];
  }

  // Safety-contour line (DEPARE03, client-side): the DEPSC-emphasised edge is
  // approximated by the outline of any depth area whose [DRVAL1, DRVAL2) range
  // straddles the live safety contour (drval1 < SFC ≤ drval2) — the same
  // area-level approximation the engine used to bake, now a filter so moving
  // the safety contour restyles instantly with no re-bake.
  safetyContourFilter() {
    const sfc = this._mariner.safetyContour ?? 10;
    return ["all",
      ["has", "drval1"],
      ["<", ["get", "drval1"], sfc],
      [">=", ["coalesce", ["get", "drval2"], ["get", "drval1"]], sfc]];
  }

  // Bar-scale unit following the mariner depth-unit setting: imperial (ft/mi) when
  // depths are in feet, otherwise metric (m/km). MapLibre picks the small vs large
  // unit by the current distance.
  _scaleUnit() { return this._mariner.depthUnit === "ft" ? "imperial" : "metric"; }

  // Render the S-52 SCALEB-style scalebar: a vertical striped bar of a round NM
  // distance. 1 NM ≡ 1 arcminute of latitude, so px-per-NM is measured along the
  // meridian at the view centre (exact, no Mercator distortion). The distance is the
  // largest "nice" step (… 0.5, 1, 2, 5 …) that stays under a target length; SCALEB
  // colours (SCLBR / CHGRD) are scheme-aware via token().
  _renderScalebar() {
    const m = this._map, el = this._scaleEl;
    if (!m || !el) return;
    const c = m.getCenter();
    const pxPerNM = Math.abs(m.project([c.lng, c.lat]).y - m.project([c.lng, c.lat + 1 / 60]).y);
    if (!pxPerNM || !isFinite(pxPerNM) || pxPerNM < 0.01) { el.innerHTML = ""; return; }
    const MAXPX = 150;
    const STEPS = [0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 25, 50, 100, 250, 500, 1000, 2500];
    let nm = STEPS[0];
    for (const v of STEPS) { if (v * pxPerNM <= MAXPX) nm = v; else break; }
    const totalPx = Math.round(nm * pxPerNM);
    const dark = this.token("SCLBR", "#e8820c"), light = this.token("CHGRD", "#dfe3e7");
    let bar = "";
    for (let i = 0; i < 4; i++) bar += `<span style="background:${i % 2 ? light : dark}"></span>`;
    el.innerHTML = `<div class="s52sb-label">${nm} NM</div><div class="s52sb-bar" style="width:${totalPx}px">${bar}</div>`;
  }

  // SAFCON01 (S-52 §13.2.13): the depth-contour value label. Drawn client-side
  // along DEPCNT lines from the baked VALDCO (whole metres, or whole feet when
  // the mariner picks imperial units), shown only when "contour labels" is on.
  contourLabelField() {
    const v = this._mariner.depthUnit === "ft"
      ? ["round", ["*", ["get", "valdco"], M_TO_FT]]
      : ["round", ["get", "valdco"]];
    return ["case", ["has", "valdco"], ["to-string", v], ""];
  }

  // SNDFRM04 (S-52 §13.2.16): a sounding ≤ the live safety depth uses the bold
  // SOUNDS glyphs, else the faint SOUNDG glyphs — picked client-side from the
  // baked depth + both name variants. Falls back to the baked names if a tile
  // predates the variants. In imperial mode the metres glyphs can't be reused
  // (the number changes), so synthesize a `snd:` image name from the numeric
  // depth + palette; `registerImage` builds the converted glyph composite.
  soundingsIconImage() {
    const sd = this._mariner.safetyDepth ?? 10;
    if (this._mariner.depthUnit === "ft") {
      const pal = ["case", ["<=", ["coalesce", ["get", "depth"], 0], sd], "S", "G"];
      // Key by deci-metres (a stable integer) so MapLibre caches one image per
      // distinct depth/palette rather than per float-string.
      const dm = ["to-string", ["round", ["*", ["coalesce", ["get", "depth"], 0], 10]]];
      return ["case", ["has", "depth"], ["concat", "snd:ft:", pal, ":", dm], ["get", "symbol_names"]];
    }
    return ["case",
      ["has", "sym_s"],
      ["case", ["<=", ["coalesce", ["get", "depth"], 0], sd], ["get", "sym_s"], ["get", "sym_g"]],
      ["get", "symbol_names"]];
  }

  // OBSTRN06/WRECKS05 (S-52 §13.2.6/§13.2.20): a danger symbol carries its
  // VALSOU + the deep-water variant. The baked `symbol_name` is the dangerous
  // (DANGER01) variant; when the depth is DEEPER than the live safety contour
  // swap to the less-prominent `sym_deep` (DANGER02). Picked client-side so the
  // safety contour no longer re-bakes. Non-danger symbols use `symbol_name`.
  pointSymbolImage() {
    const sfc = this._mariner.safetyContour ?? 10;
    return ["case",
      ["all", ["has", "sym_deep"], [">", ["coalesce", ["get", "danger_depth"], 0], sfc]],
      ["get", "sym_deep"],
      ["get", "symbol_name"]];
  }

  // The dotted CHBLK foul boundary (OBSTRN/WRECKS) is shown only where the
  // feature's VALSOU is at/above the live safety contour — a danger.
  dangerBoundaryFilter() {
    const sfc = this._mariner.safetyContour ?? 10;
    return ["all", ["has", "danger_depth"], ["<=", ["get", "danger_depth"], sfc]];
  }

  // Display category (S-52 §10.3.4), client-side + MULTI-SELECT: every feature
  // is baked with its category rank `cat` (0=base,1=standard,2=other); the
  // mariner independently toggles each, so this is a membership test, not a
  // cumulative level. Missing `cat` (stale tile) defaults to standard.
  categoryFilter() {
    const m = this._mariner;
    const en = [];
    if (m.displayBase !== false) en.push(0);
    if (m.displayStandard !== false) en.push(1);
    if (m.displayOther === true) en.push(2);
    // Isolated dangers (ISODGR01, S-52 UDWHAZ05): the mariner picks their display
    // category — DisplayBase (0, always shown; the default) or, when "isolated
    // dangers in shallow water" is on, Standard (1). The symbol is the marker;
    // VALSOU dangers became DANGER01 (live danger_depth swap), so ISODGR01 here
    // is exactly the isolated-danger set. Every other feature uses its baked cat.
    const isoCat = m.showIsolatedDangersShallow ? 1 : 0;
    const cat = ["case", ["==", ["get", "symbol_name"], "ISODGR01"], isoCat, ["coalesce", ["get", "cat"], 1]];
    const inCat = ["in", cat, ["literal", en]];
    // The M_QUAL data-quality overlay (CATZOC DQUAL* area patterns + boundary)
    // is baked display-category Other, so enabling Other dumped it on top of
    // everything — too cluttered. Decouple it into its own `dataQuality` toggle:
    // quality features show IFF dataQuality (independent of Other), and are
    // excluded from the normal category membership so Other no longer carries it.
    const isQual = ["==", ["get", "class"], "M_QUAL"];
    return m.dataQuality
      ? ["any", isQual, ["all", inCat, ["!", isQual]]]
      : ["all", inCat, ["!", isQual]];
  }

  // Boundary symbolization (S-52 §8.6.1), client-side: each primitive is baked
  // with a `bnd` tag — 2 = style-independent (always shown), 0 = plain-boundary
  // only, 1 = symbolized-boundary only. Show common (2) + the active style.
  // Missing `bnd` (non-area / stale tile) defaults to common. Default to
  // SYMBOLIZED (rank 1) per the IMO/S-52 default (the engine also bakes
  // SymbolizedBoundaries=true by default); plain only when explicitly chosen.
  // Symbolized is the variant that carries the embedded LC line symbols (e.g.
  // RESARE's EMAREMG1), so a plain default hid every complex-line symbol.
  boundaryFilter() {
    const rank = this._mariner.boundaryStyle === "plain" ? 0 : 1;
    return ["in", ["coalesce", ["get", "bnd"], 2], ["literal", [2, rank]]];
  }

  // Point-symbol style (S-52 §11.2.2), client-side: point features that resolve
  // differently under the simplified vs paper-chart LUP tables are baked twice,
  // tagged `pts` — 2 = style-independent (always shown), 0 = paper-chart, 1 =
  // simplified. Show common (2) + the active style. Missing `pts` (non-point /
  // identical-in-both / stale tile) defaults to common. Default PAPER (rank 0)
  // per the engine default (SimplifiedPoints=false).
  pointStyleFilter() {
    const rank = this._mariner.simplifiedPoints ? 1 : 0;
    return ["in", ["coalesce", ["get", "pts"], 2], ["literal", [2, rank]]];
  }

  // Light sector leg length (S-52 LIGHTS06 note 1), client-side: each sector
  // light's legs are baked twice, tagged `sleg` — 0 = the 25 mm short leg
  // (default, avoids clutter), 1 = the full VALNMR nominal-range leg. Arcs/rings
  // are untagged (coalesce 2 → always shown). Show common (2) + the active
  // length. Default SHORT (rank 0) per the engine (ShowFullLengthSectorLines=false).
  sectorLegFilter() {
    const rank = this._mariner.showFullSectorLines ? 1 : 0;
    return ["in", ["coalesce", ["get", "sleg"], 2], ["literal", [2, rank]]];
  }

  // Collect the distinct SCAMIN denominators present in the loaded chart tiles and,
  // when that set grows (or the centre latitude shifts enough to move the bucket
  // minzooms), rebuild the style so buildLayers regenerates the per-SCAMIN bucket
  // layers (each gated by a native fractional minzoom). Runs on idle only; the
  // rebuild converges (no new values ⇒ no rebuild), and steady-state SCAMIN gating
  // is then 100% native — zero per-zoom JS.
  _refreshScaminBuckets() {
    const m = this._map;
    if (!m) return;
    const seen = new Set(this._scaminValues);
    const before = seen.size;
    const srcs = this._server ? (this._serverSets || []).map((s) => "chart-" + s.name) : CHART_BANDS.filter((b) => b.slug !== "all").map((b) => "chart-" + b.slug);
    for (const src of srcs) {
      if (!m.getSource(src)) continue;
      for (const sl of SCAMIN_BUCKET_LAYERS) {
        let fs;
        try { fs = m.querySourceFeatures(src, { sourceLayer: sl }); } catch (e) { continue; }
        for (const f of fs) { const s = f.properties && f.properties.scamin; if (s) seen.add(+s); }
      }
    }
    const grew = seen.size !== before;
    const lat = m.getCenter().lat;
    const latShift = this._scaminLat == null || Math.abs(lat - this._scaminLat) > 5;
    if (!grew && !latShift) return;
    this._scaminValues = [...seen].sort((a, b) => a - b);
    this._scaminLat = lat;
    // Debounce the (heavy) style rebuild so a burst of values loaded across several
    // tiles coalesces into ONE rebuild. Converges: once every value in view is known,
    // no further growth ⇒ no rebuild, and SCAMIN gating is then fully native.
    clearTimeout(this._scaminRebuildT);
    this._scaminRebuildT = setTimeout(() => { if (this._map) this._map.setStyle(this.buildStyle()); }, 450);
  }

  // Combine a layer's intrinsic (base) filter with the live category +
  // boundary-style filters (the two client-side portrayal axes baked as
  // per-feature `cat`/`bnd`).
  combineFilters(base) {
    const parts = ["all", this.categoryFilter(), this.boundaryFilter(), this.pointStyleFilter(), this.sectorLegFilter()];
    // Meta-object coverage/region boundary lines are gated separately from the
    // "Other" display category (mariner.showMetaBounds, off by default), since
    // they read as cell boundaries and aren't useful alongside other "Other" data.
    if (!this._mariner.showMetaBounds) parts.push(["!", ["in", ["get", "class"], ["literal", META_BOUND_CLASSES]]]);
    if (base) parts.push(base);
    return parts;
  }

  // Re-apply the combined feature filter to every chart layer (on a category
  // or boundary-style toggle), preserving each layer's recorded base filter.
  applyFeatureFilters() {
    const map = this._map;
    if (!map || !this._layerBase) return;
    for (const id in this._layerBase) {
      if (map.getLayer(id)) map.setFilter(id, this.combineFilters(this._layerBase[id]));
    }
  }

  // Update a chart layer's base filter and re-apply it combined with the live
  // category + boundary filters. Used when a base filter that depends on
  // another mariner setting (e.g. the safety contour) changes.
  setBaseFilter(id, base) {
    const map = this._map;
    for (const lid of this._variantIds(id)) {
      if (this._layerBase) this._layerBase[lid] = base;
      if (map && map.getLayer(lid)) map.setFilter(lid, this.combineFilters(base));
    }
  }

  // Switch Day/Dusk/Night with zero re-tiling (colour is never baked).
  setScheme(name) {
    if (!this._colortables[name]) return;
    this._active = name;
    const map = this._map;
    // A chart base id targets every band variant; basemap ids fall back to self.
    const setIf = (id, prop, val) => { for (const lid of this._variantIds(id)) if (map.getLayer(lid)) map.setPaintProperty(lid, prop, val); };
    setIf("areas", "fill-color", this.areasFillColor());
    for (const id of ["lines-solid", "lines-dashed", "lines-dotted"]) setIf(id, "line-color", this.colorExpr("color_token"));
    setIf("safety-contour", "line-color", this.token("DEPSC", "#3a6a8a"));
    setIf("danger-boundary", "line-color", this.token("CHBLK", "#000000"));
    setIf("contour-labels", "text-color", this.contourLabelColor());
    setIf("contour-labels", "text-halo-color", this.textHaloColor());
    setIf("complex-lines", "line-color", this.colorExpr("color_token"));
    for (const v of TEXT_VARIANTS) {
      setIf(v.id, "text-color", this.textColor());
      setIf(v.id, "text-halo-color", this.textHaloColor());
    }
    setIf("light-text", "text-color", this.textColor());
    setIf("light-text", "text-halo-color", this.textHaloColor());
    // Basemap (sea background + offline coastline) is scheme-aware too.
    setIf("bg", "background-color", this.seaColor());
    setIf("coast-land", "fill-color", this.landColor());
    setIf("coast-lake", "fill-color", this.seaColor());
    setIf("coast-line", "line-color", this.coastColor());
  }

  // Switch the basemap live: "coastline" (offline GSHHG land/lakes), "osm"
  // (online OpenStreetMap raster), or "osmvec" (hosted OSM vector .pmtiles).
  // Rebuilds the style from buildStyle() so the basemap sources/layers swap
  // cleanly; chart sources, loaded archives and the tile protocols persist.
  async setBasemap(mode) {
    const m = mode === "osm" || mode === "osmvec" ? mode : "coastline";
    if ((this.getAttribute("basemap") || "coastline") === m) return;
    if (m === "osmvec" && !this._osmvecArchive && this._osmvecUrl) {
      this._osmvecArchive = await new PMTilesArchive(this._osmvecUrl).init().catch(() => null);
    }
    this.setAttribute("basemap", m);
    if (this._map) this._map.setStyle(this.buildStyle());
  }

  // Open a prebaked source for the hybrid fallback: a single .pmtiles, or a
  // charts-index.json manifest whose district files are opened into one
  // MultiArchive (each file URL resolved relative to the manifest).
  async _openPrebaked(url) {
    if (!url.endsWith(".json")) {
      // A single .pmtiles → the merged "all" band source (no per-band overzoom).
      if (!this._bands.all) this._bands.all = new MultiArchive();
      return this._bands.all.add(url);
    }
    const j = await fetch(url).then((r) => (r.ok ? r.json() : null));
    const districts = (j && j.districts) || [];
    const base = new URL(url, location.href);

    // Open every archive CONCURRENTLY. Each open is two range round-trips (header
    // + root directory); doing ~50 districts serially was the slow initial load.
    // Each unique file is opened ONCE — a bandless ("all") pack FANS across every
    // per-band source (each overzooms its own [min,max]) so a coarse-only spot
    // shows the coarser chart overscale instead of a high-zoom hole, but the
    // underlying archive handle is shared, not re-fetched six times.
    const opened = new Map(); // url → Promise<PMTilesArchive>
    const openOnce = (u) => {
      let p = opened.get(u);
      if (!p) { p = new PMTilesArchive(u).init(); opened.set(u, p); }
      return p;
    };
    const tasks = [];
    for (const d of districts) {
      if (!d.file) continue;
      const u = new URL(d.file, base).href;
      for (const slug of this._fanBands(d.band || "all")) {
        if (!this._bands[slug]) this._bands[slug] = new MultiArchive();
        const band = this._bands[slug];
        tasks.push(openOnce(u)
          .then((a) => band.addOpened(a))
          .catch((e) => { console.warn("[chartplotter] prebaked district", d.file, e); return null; }));
      }
    }
    const results = await Promise.all(tasks);
    return results.find(Boolean) || null;
  }

  // Is the active basemap any OSM variant (raster or vector)? Used to let the OSM
  // land show through (drop the chart's land fill + no-data hatch).
  _osmBasemap() {
    const b = this.getAttribute("basemap") || "none";
    return b === "osm" || b === "osmvec";
  }

  // -- runtime chart & settings API (driven by the <chart-plotter-app> shell) --

  // Force the chart source to re-request its tiles (after the loaded archive
  // changes). Bumps the version token so the tile URL changes → cache miss →
  // refetch through the chart:// (PMTiles) protocol. Cleaner than rebuilding the
  // whole style (which would re-register sprites/patterns).
  refresh() {
    this._ver++;
    const map = this._map;
    if (!map) return;
    if (this._server) {
      // Server URLs carry the bake generation (?g) from the TileJSON; re-apply the
      // current one. A genuine data change comes through flushTiles (re-fetches the
      // generation); this is just a repaint/re-request for the same data.
      for (const set of this._serverSets) {
        const src = map.getSource("chart-" + set.name);
        if (src && set.tiles) src.setTiles([set.tiles]);
      }
    } else {
      for (const band of CHART_BANDS) {
        const src = map.getSource("chart-" + band.slug);
        if (src) src.setTiles([`chart-${band.slug}://${this._ver}/{z}/{x}/{y}`]);
      }
    }
    map.triggerRepaint();
  }

  // Re-request tiles after the SERVER re-bakes a set. Re-fetches each set's TileJSON
  // (no-cache) to pick up the server's fresh bake-generation token, then points the
  // source at the new tile URL — so MapLibre drops the stale tiles and the browser
  // cache misses by content. No client-side counter, no reaching into MapLibre's
  // internal tile caches. Public; the shell calls it when a re-bake completes.
  async flushTiles() {
    const map = this._map;
    if (!map) return;
    if (this._server) {
      const names = this._serverSets.map((s) => s.name);
      this._serverSets = await this._loadSetMetas(names); // new ?g generation per set
      for (const set of this._serverSets) {
        const src = map.getSource("chart-" + set.name);
        if (src) src.setTiles([set.tiles]); // new URL → reload + cache bypass
      }
    } else {
      this._ver++;
      for (const band of CHART_BANDS) {
        const src = map.getSource("chart-" + band.slug);
        if (src) src.setTiles([`chart-${band.slug}://${this._ver}/{z}/{x}/{y}`]);
      }
    }
    map.triggerRepaint();
  }

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
  setCameraMode(mode) {
    this._cameraMode = mode || "free";
    if (this._map && this._cameraMode === "north-up") this._map.easeTo({ bearing: 0, duration: 300 });
    if (this._followFix) this.updateFollow(this._followFix);
    return this._cameraMode;
  }

  // Push the latest target fix {lng, lat, courseDeg?} from a tracking plugin; the
  // camera recentres (and, in course-up, rotates) per the active mode. A no-op in
  // "free" mode, so a plugin can stream fixes regardless of the chosen mode.
  updateFollow(fix) {
    this._followFix = fix || null;
    const map = this._map;
    if (!map || !fix || (this._cameraMode || "free") === "free") return;
    const cam = { center: [fix.lng, fix.lat], duration: 250 };
    if (this._cameraMode === "course-up" && typeof fix.courseDeg === "number") cam.bearing = fix.courseDeg;
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

  // Every MapLibre SourceCache backing the chart source(s). v4 had one at
  // map.style.sourceCaches[id]; v5 renamed that property and can hold a separate
  // paint + symbol cache, so duck-type any cache-shaped dict keyed by a chart
  // source rather than hardcoding the name. (See [[wasm-z7-tile-hole]].)
  _chartSourceCaches() {
    const style = this._map && this._map.style;
    if (!style) return [];
    const out = [];
    const consider = (c) => { if (c && (c._tiles || typeof c.clearTiles === "function") && !out.includes(c)) out.push(c); };
    const keys = this._server ? this._serverSets.map((s) => "chart-" + s.name) : CHART_BANDS.map((b) => "chart-" + b.slug);
    const fromDict = (d) => {
      if (!d || typeof d !== "object") return;
      for (const k of keys) {
        if (d instanceof Map) consider(d.get(k));
        else if (Object.prototype.hasOwnProperty.call(d, k)) consider(d[k]);
      }
    };
    // MapLibre 5.x renamed style.sourceCaches → style.tileManagers; try both (plus a
    // last-ditch scan of every style dict) so a tile flush works across versions.
    fromDict(style.tileManagers);
    fromDict(style.sourceCaches);
    for (const k of Object.keys(style)) { const v = style[k]; if (v && typeof v === "object") fromDict(v); }
    return out;
  }

  // -- server tiles --------------------------------------------------------
  // Render exactly these server tile sets (provider/pack names, the {set} in
  // /tiles/{set}/…), baked + registered by the Go server (POST /api/import). Each
  // becomes its own vector source + S-52 layer set; geographically-disjoint packs
  // (NOAA districts, IENC waterways) sit side-by-side. Switches the renderer into
  // server mode, (re)builds the style so the sources + layers exist, and re-requests
  // tiles. Pass [] to clear. Returns the active set names.
  async setServerSets(names) {
    const want = (names || []).filter(Boolean);
    const prevKey = this._serverSets.map((s) => s.name).sort().join(",");
    const wasServer = this._server;
    this._server = true;
    this._serverSets = await this._loadSetMetas(want);
    const map = this._map;
    // Rebuild the style when the set OF sets changes (sources must be created/
    // recreated). A same-set rebake (same names) just bumps the tile version.
    const changed = !wasServer || this._serverSets.map((s) => s.name).sort().join(",") !== prevKey;
    if (map && changed) map.setStyle(this.buildStyle());
    else if (map) this.refresh();
    return this._serverSets.map((s) => s.name);
  }

  // Convenience: render a single server set (or none). See setServerSets.
  setServerSet(name) { return this.setServerSets(name ? [name] : []); }

  // The active server tile-set names ([] when not in server mode).
  serverSets() { return this._server ? this._serverSets.map((s) => s.name) : []; }

  // Resolve an archive source: a Blob/File is passed through; a URL string is
  // made absolute (relative to the page) for the HTTP-Range reader.
  _resolveSrc(src) {
    return typeof src === "string" ? new URL(src, location.href).href : src;
  }

  // REPLACE the loaded chart coverage with a single archive (a Blob/File or URL
  // string) — used for an uploaded `.pmtiles`. Only the header + directory are
  // read up front (tiles stream on demand), so a multi-GB archive loads instantly.
  // Returns the opened archive (read `.bounds` to frame). Re-requests tiles.
  async setArchive(src) {
    if (this._server) return null; // server mode renders from /tiles, not pmtiles archives
    this._bands = {};
    return this.addArchive(src);
  }

  // The NOAA bands a full-range ("all") archive fans out to. A single full-range
  // source can only overzoom above the archive's GLOBAL max, so a coarse-only
  // spot in a mixed archive (e.g. a region's open water, baked only to the
  // coastal band) would blank to S-52 no-data above that band instead of showing
  // the coarser chart overscale. Serving the one archive through every per-band
  // source — each fixed to its band's [min,max] and overzooming above its own max
  // — gives the spec's overscale (the finest band present shows; coarser fills
  // the rest), exactly like the per-band district path. Explicit bands pass through.
  _fanBands(band) {
    return band === "all" ? CHART_BANDS.filter((b) => b.slug !== "all").map((b) => b.slug) : [band];
  }

  // ADD an archive to the loaded coverage (does not unload the others), into its
  // NOAA band (`overview`…`berthing`), or — for a bandless merged archive (an
  // upload / `--emit-pmtiles` / the provisioned `charts-user.pmtiles`) — fanned
  // across every band so it overzooms correctly (see `_fanBands`). Tiles still
  // stream by viewport.
  async addArchive(src, band = "all") {
    if (this._server) return null; // server mode renders from /tiles, not pmtiles archives
    const resolved = this._resolveSrc(src);
    let a = null;
    for (const b of this._fanBands(band)) {
      if (!this._bands[b]) this._bands[b] = new MultiArchive();
      a = await this._bands[b].add(resolved);
    }
    this._updateSourceZoom();
    this.refresh();
    return a;
  }

  // Replace ALL loaded chart coverage with exactly these region-archive URLs,
  // each fanned across the per-band sources (the per-region provision model:
  // add/remove a region just reloads the manifest's set — no re-bake). An empty
  // list clears the map.
  async loadRegions(urls) {
    if (this._server) return; // server mode renders from /tiles, not pmtiles archives
    this._bands = {};
    for (const u of urls) {
      try { await this.addArchive(u, "all"); } catch (e) { console.warn("[chartplotter] region", u, e); }
    }
    if (!urls.length) this.refresh();
  }

  // REPLACE every archive in ONE band with `src` (a URL or Blob/File) — used to
  // reload the server-provisioned `all` band after a re-bake without disturbing
  // the other bands (e.g. hosted per-band districts). Re-reads the new header +
  // directory and re-requests tiles. A cache-busted URL avoids a stale 304.
  async replaceBand(band, src) {
    const resolved = this._resolveSrc(src);
    let a = null;
    for (const b of this._fanBands(band)) {
      this._bands[b] = new MultiArchive();
      a = await this._bands[b].add(resolved);
    }
    this._updateSourceZoom();
    this.refresh();
    return a;
  }

  // ADD several archives at once (opening each reads only its header + directory,
  // in parallel), then re-request tiles ONCE — far cheaper than adding them one
  // at a time, which would re-request every tile per add. Each entry is a source
  // string or `{src, band}`; bad sources are skipped (logged). Returns the
  // opened archives.
  async addArchives(entries) {
    if (this._server) return []; // server mode renders from /tiles, not pmtiles archives
    const norm = entries.map((e) => (typeof e === "object" && e && e.src !== undefined ? e : { src: e, band: "all" }));
    const arcs = await Promise.all(norm.map((e) => {
      const band = e.band || "all";
      if (!this._bands[band]) this._bands[band] = new MultiArchive();
      return this._bands[band].add(this._resolveSrc(e.src)).catch((err) => { console.warn("[chartplotter] archive", e.src, err); return null; });
    }));
    this._updateSourceZoom();
    this.refresh();
    return arcs.filter(Boolean);
  }

  // NOAA-band sources have fixed zoom ranges (from CHART_BANDS), so only the
  // merged-upload `all` source needs its max synced to the loaded archive (an
  // upload may bake to <18; requesting above its max would read blank).
  _updateSourceZoom() {
    const map = this._map, all = this._bands.all;
    const src = map && map.getSource("chart-all");
    if (src && all && src.maxzoom !== undefined) {
      src.minzoom = all.minZoom;
      src.maxzoom = all.maxZoom;
    }
  }

  // Render a hosted `.pmtiles` by URL — read incrementally via HTTP Range (NOT
  // fetched whole). Resolves to the opened archive (read `.bounds` to frame).
  // Used by the `pmtiles=` attribute and the shell's hosted-default fallback.
  // The host must support byte-range requests (206); most static hosts do, and
  // `chartplotter --serve` does. REPLACES the current coverage (use addArchive to
  // combine).
  loadArchiveUrl(url) {
    return this.setArchive(url);
  }

  // Update S-52 mariner settings. EVERY setting is applied CLIENT-SIDE from
  // baked per-feature attributes — an INSTANT restyle/filter, never a re-bake:
  // depth shading (SEABED01, DRVAL1/DRVAL2), soundings (SNDFRM04), shallow
  // pattern, contour labels, the safety-contour line + danger symbols/boundary,
  // display category (cat), and boundary symbolization (bnd). Tiles are baked
  // once and immutable. Colour scheme is separate (setScheme).
  setMariner(settings) {
    this._mariner = { ...this._mariner, ...settings };
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
        // The S-52 scalebar is always nautical miles (no unit toggle), so nothing to do here.
      }
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
      // S-52 §14.5 text groups: re-derive each text variant's BASE filter (so it
      // survives a later applyFeatureFilters category re-apply) when any group
      // toggle (or light descriptions, which also feeds the general group-23
      // clause) changes. Instant — no re-bake.
      if (keys.some((k) => k === "textImportant" || k === "textNames" || k === "textOther" || k === "showLightDescriptions")) {
        const notLight = ["!=", ["get", "class"], "LIGHTS"];
        const grp = this.textGroupFilter();
        for (const v of TEXT_VARIANTS) this.setBaseFilter(v.id, ["all", notLight, v.filter, grp]);
      }
      // Display category (multi-select) and boundary symbolization both filter
      // every chart layer by a baked per-feature tag (cat / bnd) — re-apply the
      // combined feature filter. Instant — no re-bake.
      if (keys.some((k) => k === "displayBase" || k === "displayStandard" || k === "displayOther" || k === "boundaryStyle" || k === "simplifiedPoints" || k === "showFullSectorLines" || k === "showIsolatedDangersShallow" || k === "dataQuality" || k === "showMetaBounds")) {
        this.applyFeatureFilters();
      }
  }

  // -- sprite / pattern registration --------------------------------------
  addImageData(id, imgData) {
    if (!imgData || this._map.hasImage(id)) return;
    try { this._map.addImage(id, imgData, { pixelRatio: 1 }); } catch (e) { console.warn("addImage", id, e); }
  }
  registerImage(id) {
    if (!this._spriteImg || this._map.hasImage(id)) return;
    let img = null;
    try {
      img = id.startsWith("snd:") ? this.synthSounding(id)
        : id.indexOf(",") >= 0 ? this.compositeSounding(id)
        : this.centredSymbol(id);
    } catch (e) { console.warn("registerImage", id, e); }
    // NEVER leave a referenced icon-image unresolved — MapLibre's symbol
    // renderer can crash on a missing image (the `getx` atlas-lookup crash).
    // A failed/unknown symbol falls back to a blank 1×1 so the layer is inert.
    this.addImageData(id, img || new ImageData(1, 1));
  }

  // Build a sounding number in non-metric units from a synthesized name
  // `snd:<unit>:<palette>:<deci-metres>` (see soundingsIconImage). Converts the
  // baked metres depth, formats it as S-52 SNDFRM04 column glyphs, and reuses
  // the metres compositor. Quality/drying markers (QUASOU) aren't carried in the
  // numeric depth, so imperial soundings are the plain number (+ drying marker).
  synthSounding(id) {
    const [, unit, pal, dm] = id.split(":");           // ["snd","ft","S","123"]
    const meters = (parseInt(dm, 10) || 0) / 10;
    const value = unit === "ft" ? Math.abs(meters) * M_TO_FT : Math.abs(meters);
    let names = this.soundingGlyphs(Math.round(value), pal === "G" ? "G" : "S");
    if (meters < 0) names = "SOUNDSA1," + names;        // drying-height marker (always bold)
    return this.compositeSounding(names);
  }

  // S-52 SNDFRM04 whole-number column classes → a comma-joined glyph list. Each
  // glyph `SOUND<pal><class><digit>` self-positions into its column (the art
  // carries the shift), mirroring soundg03.zig's emitDigits without the metric
  // decimal subscript (imperial soundings are whole units).
  soundingGlyphs(n, pal) {
    const g = (cls, d) => `SOUND${pal}${cls}${d}`;
    n = Math.max(0, n);
    if (n < 10) return g(1, n);
    if (n < 100) return [g(1, (n / 10) | 0), g(0, n % 10)].join(",");
    if (n < 1000) return [g(2, (n / 100) | 0), g(1, ((n / 10) | 0) % 10), g(0, n % 10)].join(",");
    if (n < 10000) return [g(2, (n / 1000) | 0), g(1, ((n / 100) | 0) % 10), g(0, ((n / 10) | 0) % 10), g(4, n % 10)].join(",");
    return [g(3, (n / 10000) | 0), g(2, ((n / 1000) | 0) % 10), g(1, ((n / 100) | 0) % 10), g(0, ((n / 10) | 0) % 10), g(4, n % 10)].join(",");
  }
  registerAllSymbols() {
    if (!this._spriteImg) return;
    for (const name in this._sprite) {
      if (name === "_meta" || this._map.hasImage(name)) continue;
      try {
        const img = this.centredSymbol(name);
        if (img) this._map.addImage(name, img, { pixelRatio: 1 });
      } catch (e) { /* skip one bad symbol */ }
    }
  }
  rawCell(img, cell) {
    const cv = document.createElement("canvas");
    cv.width = cell.w; cv.height = cell.h;
    const ctx = cv.getContext("2d");
    ctx.drawImage(img, cell.x, cell.y, cell.w, cell.h, 0, 0, cell.w, cell.h);
    return ctx.getImageData(0, 0, cell.w, cell.h);
  }
  // `name` is the bare PresLib pattern name; the image is registered under the
  // `pat:` namespace so it can't clash with a same-named point symbol.
  registerPattern(name) {
    if (!this._patternsImg) return;
    const id = PAT_PREFIX + name;
    if (this._map.hasImage(id)) return;
    const cell = this._patterns[name];
    if (!cell || cell.w === undefined) return;
    try { this._map.addImage(id, this.rawCell(this._patternsImg, cell), { pixelRatio: this._patternPixelRatio }); }
    catch (e) { console.warn("registerPattern", id, e); }
  }
  registerAllPatterns() {
    if (!this._patternsImg) return;
    for (const name in this._patterns) {
      if (name === "_meta") continue;
      this.registerPattern(name);
    }
  }
  centredSymbol(name) {
    const c = this._sprite[name];
    if (!c) return null;
    const halfW = Math.max(c.pivot_x, c.w - c.pivot_x);
    const halfH = Math.max(c.pivot_y, c.h - c.pivot_y);
    const w = Math.max(1, Math.ceil(2 * halfW));
    const h = Math.max(1, Math.ceil(2 * halfH));
    const cv = document.createElement("canvas");
    cv.width = w; cv.height = h;
    const ctx = cv.getContext("2d");
    ctx.drawImage(this._spriteImg, c.x, c.y, c.w, c.h, w / 2 - c.pivot_x, h / 2 - c.pivot_y, c.w, c.h);
    return ctx.getImageData(0, 0, w, h);
  }
  compositeSounding(namesStr) {
    const cells = [];
    let minX = Infinity, minY = Infinity, maxX = -Infinity, maxY = -Infinity;
    for (const name of namesStr.split(",")) {
      const c = this._sprite[name];
      if (!c) continue;
      const left = -c.pivot_x, top = -c.pivot_y;
      cells.push({ c, left, top });
      minX = Math.min(minX, left); minY = Math.min(minY, top);
      maxX = Math.max(maxX, left + c.w); maxY = Math.max(maxY, top + c.h);
    }
    if (!cells.length) return null;
    const halfW = Math.max(-minX, maxX), halfH = Math.max(-minY, maxY);
    const w = Math.max(1, Math.ceil(2 * halfW)), h = Math.max(1, Math.ceil(2 * halfH));
    const cv = document.createElement("canvas");
    cv.width = w; cv.height = h;
    const ctx = cv.getContext("2d");
    for (const { c, left, top } of cells) {
      ctx.drawImage(this._spriteImg, c.x, c.y, c.w, c.h, w / 2 + left, h / 2 + top, c.w, c.h);
    }
    return ctx.getImageData(0, 0, w, h);
  }

  // -- layers --------------------------------------------------------------
  iconSizeForScale() {
    return ["/", ["coalesce", ["get", "scale"], this._atlasPpu], this._atlasPpu];
  }
  // Complex (symbolised) linestyles are tessellated in the BAKER per zoom: the
  // baked complex_lines layer carries the dash "on" segments as real geometry
  // (so they're crisp and phase-locked at every zoom — no pattern stretch), and
  // the embedded marks (chevron/anchor/"!") ride the normal point_symbols layer.
  // So here the dashes are just a plain solid stroke coloured by color_token
  // (which restyles live for Day/Dusk/Night).
  complexLineLayers() {
    return [{
      id: "complex-lines", type: "line", source: "chart", "source-layer": "complex_lines",
      paint: { "line-color": this.colorExpr("color_token"), "line-width": ["coalesce", ["get", "width_px"], 1] },
    }];
  }
  // S-52 PresLib §14.5 text-group selection. Each text feature carries the baked
  // `tgrp` tag (the DISPLAY param of its TX/TE, §14.4); the mariner toggles which
  // groups are visible, independent of display category. Returns a MapLibre filter
  // expression selecting the enabled groups (false = hide all). Light descriptions
  // (group 23) are the LIGHTS layer's own toggle (showLightDescriptions); a stray
  // non-light group-23 label is folded in here too.
  textGroupFilter() {
    const m = this._mariner;
    const g = ["coalesce", ["get", "tgrp"], -1];
    const named = ["match", g, [21, 26, 29], true, false]; // §14.5 Names
    const clauses = [];
    if (m.textImportant !== false) clauses.push(["==", g, 11]);     // §14.5 Important text
    if (m.textNames !== false) clauses.push(named);
    if (m.showLightDescriptions !== false) clauses.push(["==", g, 23]); // Light description
    // Other: everything not already claimed above (incl. missing tgrp = -1, so
    // text in tiles baked before tgrp existed stays visible when "Other" is on).
    if (m.textOther !== false) clauses.push(["all", ["!=", g, 11], ["!=", g, 23], ["match", g, [21, 26, 29], false, true]]);
    return clauses.length ? ["any", ...clauses] : false;
  }
  textLayers() {
    // LIGHTS characteristic text is drawn by its OWN always-on layer (see the
    // "light-text" layer in buildLayers) so it can't be decluttered behind a
    // verbose name label — exclude it from the general (collidable) text layers.
    const notLight = ["!=", ["get", "class"], "LIGHTS"];
    return TEXT_VARIANTS.map((v) => ({
      id: v.id, type: "symbol", source: "chart", "source-layer": "text",
      filter: ["all", notLight, v.filter, this.textGroupFilter()],
      layout: {
        "text-field": ["coalesce", ["get", "text"], ""], "text-font": FONT,
        "text-size": ["coalesce", ["get", "font_size_px"], 11], "text-anchor": v.anchor,
        "text-allow-overlap": false, "text-optional": true,
        visibility: "visible",
      },
      paint: {
        // Legible at dusk/night (bright ink + dark halo) — see textColor.
        "text-color": this.textColor(),
        "text-halo-color": this.textHaloColor(),
        "text-halo-width": 1.4,
        "text-halo-blur": 0.5,
      },
    }));
  }
  buildLayers() {
    // Over an OSM basemap (raster or vector), let its detailed land show through:
    // drop the chart's own land fills so OSM land isn't painted over. Filter by
    // colour token, not class, so it catches LNDARE (LANDA) AND built-up land
    // BUAARE (CHBRN) and any other land-coloured area. (No-data hatch hidden too —
    // see buildStyle.)
    const osm = this._osmBasemap();
    const notLand = ["match", ["get", "color_token"], ["LANDA", "CHBRN"], false, true];
    const base = [
      { id: "areas", type: "fill", source: "chart", "source-layer": "areas", ...(osm ? { filter: notLand } : {}), paint: { "fill-color": this.areasFillColor() } },
      { id: "area_patterns", type: "fill", source: "chart", "source-layer": "area_patterns", paint: { "fill-pattern": ["concat", PAT_PREFIX, ["coalesce", ["get", "pattern_name"], ""]] } },
      // SHALLOW_PATTERN (SEABED01, client-side): DIAMOND1 over depth areas on
      // the shallow side of the live safety contour, shown only when the
      // mariner toggle is on. Filter/visibility update on safetyContour /
      // shallowPattern — no re-bake.
      { id: "shallow-pattern", type: "fill", source: "chart", "source-layer": "areas", filter: this.shallowPatternFilter(), layout: { visibility: this._mariner.shallowPattern ? "visible" : "none" }, paint: { "fill-pattern": PAT_PREFIX + "DIAMOND1" } },
      { id: "lines-solid", type: "line", source: "chart", "source-layer": "lines", filter: ["==", ["coalesce", ["get", "dash"], "solid"], "solid"], paint: { "line-color": this.colorExpr("color_token"), "line-width": ["coalesce", ["get", "width_px"], 1] } },
      { id: "lines-dashed", type: "line", source: "chart", "source-layer": "lines", filter: ["==", ["get", "dash"], "dashed"], paint: { "line-color": this.colorExpr("color_token"), "line-width": ["coalesce", ["get", "width_px"], 1], "line-dasharray": [4, 3] } },
      { id: "lines-dotted", type: "line", source: "chart", "source-layer": "lines", filter: ["all", ["==", ["get", "dash"], "dotted"], ["!", ["has", "danger_depth"]]], paint: { "line-color": this.colorExpr("color_token"), "line-width": ["coalesce", ["get", "width_px"], 1], "line-dasharray": [1, 2] } },
      // OBSTRN/WRECKS dotted foul boundary (client-side): shown only when the
      // feature's VALSOU is ≤ the live safety contour. Filter updates on
      // safetyContour — no re-bake. Excluded from lines-dotted above.
      { id: "danger-boundary", type: "line", source: "chart", "source-layer": "lines", filter: this.dangerBoundaryFilter(), paint: { "line-color": this.token("CHBLK", "#000000"), "line-width": 2, "line-dasharray": [1, 2] } },
      // Safety-contour line (DEPARE03, client-side): a heavier DEPSC outline of
      // depth areas straddling the live safety contour, drawn over the plain
      // DEPCN contour lines. Filter updates on safetyContour — no re-bake.
      { id: "safety-contour", type: "line", source: "chart", "source-layer": "areas", filter: this.safetyContourFilter(), paint: { "line-color": this.token("DEPSC", "#3a6a8a"), "line-width": 2 } },
      // Chart scale boundaries (DATCVR §10.1.9.1): a CHGRD line where the
      // navigational purpose changes, baked into the scale_boundaries layer.
      // Standard display, on by default; toggled via mariner.showScaleBoundaries.
      { id: "scale-boundaries", type: "line", source: "chart", "source-layer": "scale_boundaries", layout: { visibility: this._mariner.showScaleBoundaries === false ? "none" : "visible" }, paint: { "line-color": this.colorExpr("color_token"), "line-width": ["coalesce", ["get", "width_px"], 1.5] } },
    ];
    const top = [
      { id: "point_symbols", type: "symbol", source: "chart", "source-layer": "point_symbols", layout: { "icon-image": this.pointSymbolImage(), "icon-size": this.iconSizeForScale(), "icon-rotate": ["coalesce", ["get", "rotation_deg"], 0], "icon-rotation-alignment": "map", "icon-allow-overlap": true, "icon-ignore-placement": true, "symbol-z-order": "source" } },
      // Spot soundings — an individually-selectable "Other" item per S-52/IMO
      // (default on). A plain visibility toggle on showSoundings.
      { id: "soundings", type: "symbol", source: "chart", "source-layer": "soundings", layout: { "icon-image": this.soundingsIconImage(), "icon-size": this.iconSizeForScale(), "icon-allow-overlap": false, visibility: this._mariner.showSoundings === false ? "none" : "visible" } },
      // Contour labels (SAFCON01, client-side): VALDCO along DEPCNT lines,
      // toggled by the mariner's "contour labels" setting — no re-bake.
      { id: "contour-labels", type: "symbol", source: "chart", "source-layer": "lines",
        filter: ["all", ["==", ["get", "class"], "DEPCNT"], ["has", "valdco"]],
        layout: { "symbol-placement": "line", "text-field": this.contourLabelField(), "text-font": FONT, "text-size": 10, "text-max-angle": 30, "symbol-spacing": 300, "text-allow-overlap": false, "text-optional": true, visibility: this._mariner.showContourLabels ? "visible" : "none" },
        paint: { "text-color": this.contourLabelColor(), "text-halo-color": this.textHaloColor(), "text-halo-width": 1.2 } },
      // Light characteristics (LIGHTS06 TX, e.g. "Fl(1)R 3s 4.2m") — their own
      // layer, always drawn (allow/ignore-overlap) so the important nav data is
      // never decluttered behind a name label. Placed below the light flare.
      { id: "light-text", type: "symbol", source: "chart", "source-layer": "text",
        filter: ["==", ["get", "class"], "LIGHTS"],
        layout: { "text-field": ["coalesce", ["get", "text"], ""], "text-font": FONT,
          "text-size": ["coalesce", ["get", "font_size_px"], 10], "text-anchor": "top", "text-offset": [0, 0.4],
          // Left-justify so a merged multi-line light label's lines align on their
          // left edge (e.g. stacked "Mo(U)W 20s 50m 17M" / "Mo(U)R 20s 50m 15M").
          "text-justify": "left",
          "text-allow-overlap": true, "text-ignore-placement": true,
          // Light descriptions (LIGHTS06 characteristics) — individually
          // selectable per S-52 (default on); toggled by showLightDescriptions.
          visibility: this._mariner.showLightDescriptions === false ? "none" : "visible" },
        paint: { "text-color": this.textColor(), "text-halo-color": this.textHaloColor(), "text-halo-width": 1.4, "text-halo-blur": 0.5 } },
    ];
    // Template chart layers (source "chart" is a placeholder rewritten per band
    // by expandChartLayers). Their `filter` is the intrinsic (base) filter.
    return base.concat(this.complexLineLayers(), top, this.textLayers());
  }

  // Expand the chart layer templates into one stacked set per band source
  // (CHART_BANDS order = bottom→top, coarse→fine). Each variant gets id
  // "<baseId>@<band>" and source "chart-<band>". Records every variant's base
  // filter in `_layerBase` (so a category/boundary toggle re-applies
  // combineFilters per layer) and a baseId→[variantId…] map in `_variants` (so
  // mariner/colour updates that target a layer by name hit all its band copies).
  // What's capped to a coarse band's max (so it isn't drawn at a finer zoom): LINES
  // and pattern (hatch) FILLS — the marks that visibly duplicate a finer band's
  // coast/contour/boundary as offset strokes — PLUS `point_symbols`, because the
  // chevron/anchor/"!" marks embedded in complex line styles ride that layer: cap
  // them WITH their line, or (where a coarse band overzooms a finer band's gap, e.g.
  // open water at the approach band) the line is cut but the chevrons float on their
  // own. Base area fills (solid depth/land colour), SOUNDINGS and TEXT keep
  // overzooming: the base fill is the continuous gap-fill (a finer fill draws over
  // it), and soundings/text are their own layers that read fine overscale. Where a
  // finer band exists it supplies its own symbols at the band boundary, so capping
  // the coarse copies there is seamless.
  _capsAtBand(L) {
    return L.type === "line" || L.id === "point_symbols" || (L.type === "fill" && L.paint && L.paint["fill-pattern"] !== undefined);
  }

  expandChartLayers() {
    const tmpl = this.buildLayers();
    this._layerBase = {};
    this._variants = {};
    const out = [];
    // Server mode: one source per active per-band set (chart-<district>-<band>).
    // Iterate template-outer, set-inner — and _serverSets is ordered coarse→fine —
    // so the global draw order is by S-52 class (all fills, then lines, then symbols,
    // then text), with finer bands' fills over coarser ones. Each band's source
    // overzooms above its own max (from its TileJSON), so a coarse-only spot (open
    // water) is filled by the general/overview source instead of blanking. As in the
    // pmtiles path, overview/general LINE + pattern layers are capped at their band
    // max so the coarse marks don't bleed into a finer band's zooms. Variant id is
    // "<id>@<set>" so scheme/mariner updates by base id hit every set's copy.
    if (this._server) {
      for (const L of tmpl) {
        const base = L.filter ?? null;
        for (const set of this._serverSets) {
          const id = L.id + "@" + set.name;
          this._layerBase[id] = base;
          (this._variants[L.id] ||= []).push(id);
          const v = { ...L, id, source: "chart-" + set.name, filter: this.combineFilters(base), layout: this._variantLayout(L, set.band, id) };
          const dmin = BAND_DISPLAY_MIN[set.band];
          if (dmin) v.minzoom = dmin; // band appears at its scale, not the baked floor
          if ((set.band === "overview" || set.band === "general") && this._capsAtBand(L)) {
            v.maxzoom = CHART_BANDS.find((b) => b.slug === set.band).max;
          }
          out.push(v);
          // Right after this band's base depth/land fill, drop the S-52 overscale
          // pattern AP(OVERSC01) for the zooms where the band is GROSSLY overscale
          // (display scale ≥ ~×2 its compilation scale → above its native max). It's
          // interleaved per band, so a finer band's opaque fill covers it where finer
          // data exists — the hatch is left only on the coarse-only (overscale) patches
          // such as open water shown enlarged. S-52 §10.1.10.2.
          if (L.id === "areas") this._pushOverscale(out, "chart-" + set.name, set.band);
        }
      }
      this._pushScaminProbes(out);
      return out;
    }
    // Iterate TEMPLATE-outer, band-inner so the global draw order is by S-52
    // class (all bands' fills, then all bands' lines, then all symbols, then all
    // text) rather than per-band stacks. Band-outer order put a finer band's area
    // FILLS above a coarser band's point SYMBOLS, so a coarse-scale light/beacon
    // that overzoomed past its band got buried under the finer chart's depth-area
    // fill the moment you zoomed in — it "disappeared". Keeping bands coarse→fine
    // WITHIN each class preserves best-available (finer fill covers coarser fill),
    // while symbols/text now always sit above every band's fills.
    const lat = this._scaminLat != null ? this._scaminLat : this._map ? this._map.getCenter().lat : 0;
    for (const L of tmpl) {
      for (const band of CHART_BANDS) {
        const base = L.filter ?? null;
        const dmin = BAND_DISPLAY_MIN[band.slug];
        const capped = (band.slug === "overview" || band.slug === "general") && this._capsAtBand(L);
        // mk pushes one variant: id, the per-layer base filter it should re-combine
        // from (stored in _layerBase so category/boundary toggles re-apply), and a
        // native minzoom. The maxzoom cap (coarse band can't bleed into fine zoom)
        // is mirrored from the unbucketed path.
        const mk = (suffix, baseFilter, minzoom) => {
          const id = L.id + "@" + band.slug + suffix;
          this._layerBase[id] = baseFilter;
          (this._variants[L.id] ||= []).push(id);
          const v = { ...L, id, source: "chart-" + band.slug, filter: this.combineFilters(baseFilter), layout: this._variantLayout(L, band.slug, id) };
          if (minzoom != null) v.minzoom = minzoom;
          if (capped) v.maxzoom = band.max;
          out.push(v);
        };
        const and = (extra) => (base ? ["all", base, extra] : extra);
        // SCAMIN buckets (point symbols / soundings): MapLibre's native fractional
        // layer minzoom does the exact-scale gating with ZERO per-zoom work — a
        // feature with SCAMIN 1:N shows precisely from display scale 1:N, in BOTH
        // directions, crossing bands down to that scale. One bucket per distinct
        // SCAMIN value (collected from the tiles). Out-of-zoom buckets are skipped by
        // MapLibre for free, so the extra layers cost nothing at runtime. Features
        // WITHOUT SCAMIN take the band-gated `#no` variant. Other layers: one variant.
        if (SCAMIN_BUCKET_LAYERS.has(L.id) && this._scaminValues && this._scaminValues.length) {
          mk("#no", and(["!", ["has", "scamin"]]), dmin || undefined);
          for (const sc of this._scaminValues) {
            mk("#sm" + sc, and(["==", ["get", "scamin"], sc]), scaminDisplayZoom(sc, lat));
          }
        } else {
          mk("", base, dmin || undefined);
        }
        if (L.id === "areas") this._pushOverscale(out, "chart-" + band.slug, band.slug);
      }
    }
    this._pushScaminProbes(out);
    return out;
  }

  // Invisible "probe" layers that force the SPARSE sub-band tiles (where SCAMIN
  // features float below their band min) to load at all zooms, so the per-SCAMIN
  // value set can be collected (querySourceFeatures only sees LOADED tiles, and a
  // tile loads only if some visible layer needs it). They render nothing. Without
  // them, the bucket layers can't exist until their tiles load, but those tiles
  // won't load until the buckets exist — a deadlock at sub-band zooms.
  _pushScaminProbes(out) {
    const srcs = this._server ? (this._serverSets || []).map((s) => "chart-" + s.name) : CHART_BANDS.filter((b) => b.slug !== "all").map((b) => "chart-" + b.slug);
    for (const src of srcs) {
      for (const sl of SCAMIN_BUCKET_LAYERS) {
        out.push({ id: "scaminprobe-" + src + "-" + sl, source: src, "source-layer": sl, type: "circle", minzoom: 0, filter: ["has", "scamin"], paint: { "circle-radius": 0, "circle-opacity": 0, "circle-stroke-width": 0 } });
      }
    }
  }

  // Append the S-52 overscale pattern AP(OVERSC01) for a band's source, shown only
  // above the band's native max (where the chart is grossly enlarged, ≥ ~×2 its
  // compilation scale). Inserted right after the band's base fill so a finer band's
  // opaque fill covers it — the hatch survives only on coarse-only overscale patches.
  // No-op for the finest band / merged "all" set (nothing coarser to enlarge). S-52
  // §10.1.10.2; display priority 3, viewing group 21030.
  _pushOverscale(out, source, band) {
    // DISABLED: the old "hatch wherever a band overzooms past its native max"
    // heuristic over-triggers — it paints AP(OVERSC01) on plain zoom-in of the
    // best-available chart, which S-52 §10.1.10.1 says must show ONLY the "×N"
    // indication, never the pattern. Real ECDIS show the area pattern only at a
    // genuine scale boundary (a coarser cell enlarged ≥×2 in a finer cell's hole,
    // §10.1.10.2) — that wants a baked overscale_areas layer (task #3). Until then,
    // no auto-hatch (the HUD still shows the ×N overscale indication).
    return; // eslint-disable-line no-unreachable
    const nm = CHART_BANDS.find((b) => b.slug === band);
    if (!nm || band === "all" || nm.max >= 18) return;
    const id = "overscale@" + source;
    const vis = this._showOverscale === false ? "none" : "visible";
    this._layerVis[id] = vis;
    out.push({
      id,
      type: "fill",
      source,
      "source-layer": "areas",
      minzoom: nm.max + 1,
      layout: { visibility: this._bandsHidden.has(band) ? "none" : vis },
      paint: { "fill-pattern": PAT_PREFIX + "OVERSC01" },
    });
  }

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
    for (const l of map.getStyle().layers) {
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
    // `{v}` is a cache-busting version token (see registerPmtilesProtocol /
    // refresh): bumping it forces MapLibre to re-request chart tiles.
    const v = this._ver;
    // One vector source per NOAA band, each serving the `chart-<band>` protocol
    // over its fixed baked zoom range (overzoomed above max). `{v}` is a
    // cache-bust token bumped by setArchive/refresh. Sources for not-yet-loaded
    // bands resolve to blank tiles (harmless) until an archive is added.
    const sources = {};
    // Per-band prebaked sources in BOTH modes. The source maxzoom is band.bake —
    // the top zoom the archive actually contains — so MapLibre serves real tiles up
    // to there and client-overzooms above it (base fills + the finest band fill the
    // finer zooms for free; coarser bands' lines/patterns are cut in the bake or
    // capped on the layer, so they don't bleed into a finer band's area).
    for (const band of CHART_BANDS) {
      sources["chart-" + band.slug] = {
        type: "vector",
        tiles: [`chart-${band.slug}://${v}/{z}/{x}/{y}`],
        // minzoom 0, not band.min: SCAMIN-bearing features are baked into sub-band
        // tiles (they cross bands down to their SCAMIN scale), so the source must be
        // allowed to fetch those. They're sparse (only SCAMIN objects live below the
        // band min), and minzoom only adds requests when the VIEW is coarse (few
        // tiles), so it's cheap. Per-SCAMIN bucket layers gate the exact display scale.
        minzoom: 0,
        maxzoom: band.bake,
      };
    }
    if (this._server) {
      // One source per active pack, MVT pulled live from /tiles/{set}. minzoom/
      // maxzoom are the set's REAL range (from its TileJSON) so MapLibre overzooms
      // the deepest baked tile instead of requesting empty tiles past the bake. With
      // no packs we add no chart sources (a vector source with an empty `tiles` array
      // makes MapLibre crash); the no-data hatch shows through.
      for (const set of this._serverSets) {
        sources["chart-" + set.name] = { type: "vector", tiles: [set.tiles || this._serverTilesUrl(set.name)], minzoom: set.min, maxzoom: set.max };
      }
    }
    const layers = [{ id: "bg", type: "background", paint: { "background-color": this.seaColor() } }];

    const basemap = this.getAttribute("basemap") || "none";
    if (basemap === "osm") {
      sources.osm = { type: "raster", tileSize: 256, maxzoom: 19, tiles: ["https://tile.openstreetmap.org/{z}/{x}/{y}.png"], attribution: "© OpenStreetMap contributors" };
      layers.push({ id: "osm", type: "raster", source: "osm" });
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
      layers: layers.concat(this.expandChartLayers()),
    };
  }
}

// S-52 halign/valign → MapLibre text-anchor, one decluttered sublayer per
// (halign × valign-group) with a constant anchor (text-anchor isn't data-driven).
function textAnchor(h, v) {
  const vv = v === "top" ? "top" : v === "bottom" ? "bottom" : "center";
  const hh = h === "left" ? "left" : h === "right" ? "right" : "center";
  if (vv === "center" && hh === "center") return "center";
  if (vv === "center") return hh;
  if (hh === "center") return vv;
  return vv + "-" + hh;
}
const TEXT_VARIANTS = (function () {
  const out = [];
  for (const h of ["left", "center", "right"]) {
    for (const vg of ["top", "center", "bottom"]) {
      const anchor = textAnchor(h, vg === "center" ? "middle" : vg);
      const hf = ["==", ["coalesce", ["get", "halign"], "center"], h];
      const vf = vg === "center"
        ? ["match", ["coalesce", ["get", "valign"], "middle"], ["middle", "baseline", "center"], true, false]
        : ["==", ["coalesce", ["get", "valign"], "middle"], vg];
      out.push({ id: "text-" + h + "-" + vg, anchor, filter: ["all", hf, vf] });
    }
  }
  return out;
})();

// Custom element names must contain a hyphen (HTML spec) — `<chart-plotter>`.
customElements.define("chart-plotter", ChartPlotter);
