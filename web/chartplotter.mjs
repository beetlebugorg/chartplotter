// <chartplotter> — the public web component.
//
// A self-contained S-52 ENC chart plotter: a MapLibre map whose vector tiles
// are generated IN-BROWSER by the wasm engine (chartplotter.wasm) from ENC cell
// bytes kept in OPFS. No tile server. Drop the tag on a page:
//
//   <script type="module" src="chartplotter.mjs"></script>
//   <chart-plotter center="-76.4875,38.975" zoom="13" charts="US5MD1MC"></chart-plotter>
//
// Attributes (all optional):
//   center   "lon,lat"            initial view centre (default Annapolis)
//   zoom     number               initial zoom (default 13)
//   charts   "CELL1,CELL2,…"      cells to load on start (downloaded into OPFS
//                                 from cell-url if not already stored)
//   assets   base URL             where the generated assets live (default "./":
//                                 chartplotter.wasm, colortables.json, sprite.*,
//                                 linestyles.json, patterns.*, glyphs/)
//   cell-url template             URL to fetch a cell's .000, {name} substituted
//                                 (default "{assets}cells/{name}.000")
//   basemap  "osm" | "none"       street raster under the chart (default none)
//
// The full S-52 style (areas, patterns, lines, complex lines, point symbols,
// soundings, text) is assembled client-side from the baker's JSON assets, the
// same way the dev index.html does — colour is never baked, so Day/Dusk/Night
// is a restyle. This module supersedes index.html as the shipped surface.

// NOTE: in-browser baking (the TinyGo WASM EngineClient) is NOT part of the Go
// build — all tile generation is a server-side task (`chartplotter provision` /
// POST /api/provision). The browser only renders pre-baked archives. The
// `bakePmtiles`/`_engineClient` methods below therefore throw; the optional
// in-browser import-bake path is Phase 9.
import { ChartStore } from "./chart-store.mjs";
import { PMTilesArchive, MultiArchive, registerPmtilesProtocol } from "./pmtiles-source.mjs";

const FALLBACK = "#ff00ff";
const FEATURE_SCALE = 0.01 / 0.35278;
const FONT = ["Noto Sans Regular"];
const ZMIN = 5, ZMAX = 16;
const M_TO_FT = 3.280839895; // depth-unit conversion (metric ↔ imperial)

// NOAA ENC navigational-purpose bands (the rescheming standard) → one vector
// source each, baked over [min,max] and overzoomed above max (see bake.zig
// `Band`). Stacked coarse→fine: where a finer band has data its fill covers the
// coarser one; where it doesn't, the coarser shows through (overzoomed). `all`
// is the merged single archive (an upload / `--emit-pmtiles`) — one full-range
// source, drawn on top. Order here IS the draw order (bottom→top).
const CHART_BANDS = [
  { slug: "overview", min: 0, max: 7 },
  { slug: "general", min: 7, max: 9 },
  { slug: "coastal", min: 9, max: 11 },
  { slug: "approach", min: 11, max: 13 },
  { slug: "harbor", min: 13, max: 16 },
  { slug: "berthing", min: 16, max: 18 },
  { slug: "all", min: 0, max: 18 },
];

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
    style.textContent = ":host{display:block;position:relative}#map{position:absolute;inset:0;background:#93aebb}";
    const css = document.createElement("link");
    css.rel = "stylesheet";
    css.href = assets + "vendor/maplibre-gl.css";
    const mapEl = document.createElement("div");
    mapEl.id = "map";
    this.shadowRoot.append(style, css, mapEl);

    // The bake engine (wasm in a Web Worker) is created LAZILY — only when a
    // bake is actually requested (see _engineClient/bakePmtiles). Rendering
    // reads from a prebaked .pmtiles archive, so the map never waits on the
    // worker spinning up (wasm load + PresLib/catalog parse).

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

    // Cells live in the store (OPFS/IndexedDB); the worker bakes tiles from them
    // on demand. For the `charts` attribute, just make sure each is downloaded
    // into the store — the worker finds + bakes it by viewport (needs the cell
    // in the catalog for its coverage/scale).
    this._store = new ChartStore();
    this._cellTmpl = this.getAttribute("cell-url") || assets + "cells/{name}.000";
    const cells = (this.getAttribute("charts") || "")
      .split(",").map((s) => s.trim()).filter(Boolean);
    for (const name of cells) {
      try { await this._store.ensure(name, (n) => this._cellTmpl.replace("{name}", n)); }
      catch (e) { console.warn("[chartplotter] cell", name, e.message); }
    }

    // One protocol + source per NOAA band: each serves that band's loaded
    // archive(s) (bake-once), or blank when none is loaded. Missing tile → blank.
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
      minZoom: 1,
      maxZoom: 18,
      // Attribution bottom-left so the bottom-right corner is free for the app's
      // scale/zoom readout.
      attributionControl: { position: "bottom-left" },
    });
    this._map = map;
    this.map = map; // public handle

    // Graphical bar scale (ECDIS/S-52 expectation): a linear distance scale in
    // nautical miles, complementing the numeric 1:N readout in the app HUD.
    map.addControl(new maplibregl.ScaleControl({ maxWidth: 140, unit: "nautical" }), "bottom-left");

    map.on("styleimagemissing", (e) => {
      if (this._patterns[e.id]) this.registerPattern(e.id);
      else this.registerImage(e.id);
    });
    map.on("load", async () => {
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
      // Bare-component usage (<chart-plotter charts="…">): bake the listed cells
      // into an archive so they render. The shell drives this itself for uploads.
      if (!pmUrl && cells.length) {
        try {
          const bytes = await this.bakePmtiles(cells);
          if (bytes.length) await this.setArchive(new Blob([bytes]));
        } catch (e) { console.warn("[chartplotter] auto-bake", e); }
      }
      this.dispatchEvent(new CustomEvent("ready", { detail: { map }, bubbles: true }));
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
    const shc = m.shallowContour ?? 2, sfc = m.safetyContour ?? 10, dpc = m.deepContour ?? 20;
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
    const sd = this._mariner.safetyDepth ?? 30;
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
    const inCat = ["in", ["coalesce", ["get", "cat"], 1], ["literal", en]];
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

  // Combine a layer's intrinsic (base) filter with the live category +
  // boundary-style filters (the two client-side portrayal axes baked as
  // per-feature `cat`/`bnd`).
  combineFilters(base) {
    const parts = ["all", this.categoryFilter(), this.boundaryFilter()];
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
    for (const name2 in this._linestyles) setIf("lc-line-" + name2, "line-color", this.colorExpr("color_token"));
    for (const v of TEXT_VARIANTS) {
      setIf(v.id, "text-color", this.textColor());
      setIf(v.id, "text-halo-color", this.textHaloColor());
    }
    // Basemap (sea background + offline coastline) is scheme-aware too.
    setIf("bg", "background-color", this.seaColor());
    setIf("coast-land", "fill-color", this.landColor());
    setIf("coast-lake", "fill-color", this.seaColor());
    setIf("coast-line", "line-color", this.coastColor());
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
    for (const band of CHART_BANDS) {
      const src = map.getSource("chart-" + band.slug);
      if (src) src.setTiles([`chart-${band.slug}://${this._ver}/{z}/{x}/{y}`]);
    }
    map.triggerRepaint();
  }

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

  // The bake engine (worker + wasm), created on first use. Rendering doesn't
  // need it — only baking (upload-ENCs / charts= attribute) does — so a
  // prebaked-only viewer never spins up the worker, loads the wasm, or parses
  // the catalog.
  _engineClient() {
    throw new Error(
      "in-browser baking is not available in this build — tiles are baked " +
      "server-side (chartplotter provision / POST /api/provision)",
    );
  }

  // In-browser baking is not part of the Go build (see the import note at the
  // top): all tile generation is a server-side task. This throws so the (rare)
  // bare-component charts="…" / in-browser .zip-import paths fail loudly rather
  // than silently; the primary flows render a hosted/provisioned archive via
  // pmtiles="…" or setArchive(). Restoring this is Phase 9 (TinyGo WASM).
  bakePmtiles(names, onProgress) {
    return Promise.reject(new Error("in-browser baking unavailable; use server provisioning"));
  }

  // Names of every locally stored chart.
  listCharts() {
    return this._store ? this._store.list() : Promise.resolve([]);
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
      }
      // Shallow pattern: visibility on its toggle (a fill layer).
      if (keys.includes("shallowPattern")) {
        this._eachLayer("shallow-pattern", (id) => map.setLayoutProperty(id, "visibility", this._mariner.shallowPattern ? "visible" : "none"));
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
        this._eachLayer("contour-labels", (id) => map.setLayoutProperty(id, "visibility", this._mariner.showContourLabels ? "visible" : "none"));
      }
      // Display category (multi-select) and boundary symbolization both filter
      // every chart layer by a baked per-feature tag (cat / bnd) — re-apply the
      // combined feature filter. Instant — no re-bake.
      if (keys.some((k) => k === "displayBase" || k === "displayStandard" || k === "displayOther" || k === "boundaryStyle" || k === "dataQuality")) {
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
  registerPattern(id) {
    if (!this._patternsImg || this._map.hasImage(id)) return;
    const cell = this._patterns[id];
    if (!cell || cell.w === undefined) return;
    try { this._map.addImage(id, this.rawCell(this._patternsImg, cell), { pixelRatio: this._patternPixelRatio }); }
    catch (e) { console.warn("registerPattern", id, e); }
  }
  registerAllPatterns() {
    if (!this._patternsImg) return;
    for (const name in this._patterns) {
      if (name === "_meta" || this._map.hasImage(name)) continue;
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
  complexLineLayers() {
    const layers = [];
    // Linestyle symbols are drawn at the S-52 feature scale (the size the atlas
    // marks were rasterised for); the sprite atlas is px_per_unit = _atlasPpu.
    const symSize = FEATURE_SCALE / this._atlasPpu;
    // One dashed-stroke LINE layer per linestyle (each carries its own dash +
    // colour), plus ONE data-driven SYMBOL layer for the embedded marks. (Per-
    // linestyle symbol layers — hundreds once band-expanded — overwhelm the
    // symbol-placement pass and starve other symbols like buoys/soundings.)
    const symNames = [];
    const symMatch = ["match", ["get", "linestyle_name"]];
    for (const name in this._linestyles) {
      const ls = this._linestyles[name];
      const w = Math.max(0.6, ls.width_px || 1);
      const dash = (ls.dash && ls.dash.length ? ls.dash : [1]).map((d) => Math.max(0.01, d / w));
      layers.push({
        id: "lc-line-" + name, type: "line", source: "chart", "source-layer": "complex_lines",
        filter: ["==", ["get", "linestyle_name"], name],
        paint: { "line-color": this.colorExpr("color_token"), "line-width": ["coalesce", ["get", "width_px"], w], "line-dasharray": dash },
      });
      const sym = (ls.symbols || [])[0];
      if (sym && sym.n) { symNames.push(name); symMatch.push(name, sym.n); }
    }
    // The linestyle's embedded symbol along the line — what makes a SYMBOLIZED
    // boundary (e.g. RESARE's EMAREMG1) visibly different from a plain one. The
    // baker emits only the polyline (no per-zoom marks), so place the primary
    // symbol client-side with symbol-placement:line. icon-image is data-driven
    // (linestyle → its mark); a multi-symbol period can't reproduce its exact
    // intra-period layout this way (a known tradeoff).
    if (symNames.length) {
      symMatch.push(""); // linestyles without a mark → no icon
      layers.push({
        id: "lc-marks", type: "symbol", source: "chart", "source-layer": "complex_lines",
        filter: ["in", ["get", "linestyle_name"], ["literal", symNames]],
        layout: {
          "symbol-placement": "line", "symbol-spacing": 40,
          "icon-image": symMatch, "icon-size": symSize,
          "icon-rotation-alignment": "map",
          "icon-allow-overlap": true, "icon-ignore-placement": true,
        },
      });
    }
    return layers;
  }
  textLayers() {
    return TEXT_VARIANTS.map((v) => ({
      id: v.id, type: "symbol", source: "chart", "source-layer": "text", filter: v.filter,
      layout: {
        "text-field": ["coalesce", ["get", "text"], ""], "text-font": FONT,
        "text-size": ["coalesce", ["get", "font_size_px"], 11], "text-anchor": v.anchor,
        "text-allow-overlap": false, "text-optional": true,
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
    const base = [
      { id: "areas", type: "fill", source: "chart", "source-layer": "areas", paint: { "fill-color": this.areasFillColor() } },
      { id: "area_patterns", type: "fill", source: "chart", "source-layer": "area_patterns", paint: { "fill-pattern": ["coalesce", ["get", "pattern_name"], ""] } },
      // SHALLOW_PATTERN (SEABED01, client-side): DIAMOND1 over depth areas on
      // the shallow side of the live safety contour, shown only when the
      // mariner toggle is on. Filter/visibility update on safetyContour /
      // shallowPattern — no re-bake.
      { id: "shallow-pattern", type: "fill", source: "chart", "source-layer": "areas", filter: this.shallowPatternFilter(), layout: { visibility: this._mariner.shallowPattern ? "visible" : "none" }, paint: { "fill-pattern": "DIAMOND1" } },
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
    ];
    const top = [
      { id: "point_symbols", type: "symbol", source: "chart", "source-layer": "point_symbols", layout: { "icon-image": this.pointSymbolImage(), "icon-size": this.iconSizeForScale(), "icon-rotate": ["coalesce", ["get", "rotation_deg"], 0], "icon-rotation-alignment": "map", "icon-allow-overlap": true, "icon-ignore-placement": true, "symbol-z-order": "source" } },
      { id: "soundings", type: "symbol", source: "chart", "source-layer": "soundings", layout: { "icon-image": this.soundingsIconImage(), "icon-size": this.iconSizeForScale(), "icon-allow-overlap": false } },
      // Contour labels (SAFCON01, client-side): VALDCO along DEPCNT lines,
      // toggled by the mariner's "contour labels" setting — no re-bake.
      { id: "contour-labels", type: "symbol", source: "chart", "source-layer": "lines",
        filter: ["all", ["==", ["get", "class"], "DEPCNT"], ["has", "valdco"]],
        layout: { "symbol-placement": "line", "text-field": this.contourLabelField(), "text-font": FONT, "text-size": 10, "text-max-angle": 30, "symbol-spacing": 300, "text-allow-overlap": false, "text-optional": true, visibility: this._mariner.showContourLabels ? "visible" : "none" },
        paint: { "text-color": this.contourLabelColor(), "text-halo-color": this.textHaloColor(), "text-halo-width": 1.2 } },
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
  expandChartLayers() {
    const tmpl = this.buildLayers();
    this._layerBase = {};
    this._variants = {};
    const out = [];
    for (const band of CHART_BANDS) {
      for (const L of tmpl) {
        const id = L.id + "@" + band.slug;
        const base = L.filter ?? null;
        this._layerBase[id] = base;
        (this._variants[L.id] ||= []).push(id);
        const v = { ...L, id, source: "chart-" + band.slug, filter: this.combineFilters(base) };
        // Symbol layout/placement is the bake-render bottleneck (one provisioned
        // archive fanned across every band re-places the same marks at each zoom),
        // so SYMBOL layers are bounded to a few levels of overscale past their band
        // (keeping ~3 bands laying out at any zoom). The margin must be wide enough
        // to bridge a SKIPPED band: where an area has e.g. general (max z9) then
        // harbor (min z13) cells but no coastal/approach in between, a tight +3 cap
        // hid general's overzoom at z≥12 while harbor only starts at z13 — leaving
        // soundings/buoys/lights blank at z12 (and vanishing as you zoomed in). +4
        // makes every band reach the next-present band's start across the realistic
        // single/double NOAA band skips. Area/line FILLS keep overzooming (cheap).
        if (L.type === "symbol" && band.slug !== "all") v.maxzoom = Math.min(ZMAX + 2, band.max + 4);
        out.push(v);
      }
    }
    return out;
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
  buildStyle() {
    // `{v}` is a cache-busting version token (see registerPmtilesProtocol /
    // refresh): bumping it forces MapLibre to re-request chart tiles.
    const v = this._ver;
    // One vector source per NOAA band, each serving the `chart-<band>` protocol
    // over its fixed baked zoom range (overzoomed above max). `{v}` is a
    // cache-bust token bumped by setArchive/refresh. Sources for not-yet-loaded
    // bands resolve to blank tiles (harmless) until an archive is added.
    const sources = {};
    for (const band of CHART_BANDS) {
      sources["chart-" + band.slug] = {
        type: "vector",
        tiles: [`chart-${band.slug}://${v}/{z}/{x}/{y}`],
        minzoom: band.min,
        maxzoom: band.max,
      };
    }
    const layers = [{ id: "bg", type: "background", paint: { "background-color": this.seaColor() } }];

    const basemap = this.getAttribute("basemap") || "none";
    if (basemap === "osm") {
      sources.osm = { type: "raster", tileSize: 256, maxzoom: 19, tiles: ["https://tile.openstreetmap.org/{z}/{x}/{y}.png"], attribution: "© OpenStreetMap contributors" };
      layers.push({ id: "osm", type: "raster", source: "osm" });
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
        { id: "coast-line", type: "line", source: "coastline", ...srcLayer, filter: ["<=", ["get", "level"], 2], paint: { "line-color": this.coastColor(), "line-width": 0.6 } },
      );
    }

    // S-52 "no data" pattern (NODATA03) across the whole world, ABOVE the basemap
    // but BELOW the chart layers: ENC area fills paint over it wherever we have
    // cell coverage, so any area WITHOUT chart data reads as the standard no-data
    // hatch instead of looking like surveyed sea. (The pattern image loads lazily
    // via `styleimagemissing` → registerPattern.)
    sources.nodata = { type: "geojson", data: { type: "Feature", properties: {}, geometry: { type: "Polygon", coordinates: [[[-180, -85.0511], [180, -85.0511], [180, 85.0511], [-180, 85.0511], [-180, -85.0511]]] } } };
    layers.push({ id: "nodata", type: "fill", source: "nodata", paint: { "fill-pattern": "NODATA03" } });

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
