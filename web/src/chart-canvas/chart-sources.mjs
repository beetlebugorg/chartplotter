// <ChartSources> — the chart SOURCE / ARCHIVE manager extracted from <chart-canvas>.
//
// A stateful collaborator that owns everything about WHERE chart tiles come from —
// the server-tiles mode + per-set metadata, the per-band prebaked PMTiles archives,
// the tile cache-bust token, and the SCAMIN-bucket discovery state — plus the public
// chart-source API the shell drives (setServerSets, addArchive, loadRegions, refresh,
// flushTiles, …). The element keeps these methods as thin delegators so external
// callers are unchanged; this class holds the state + does the work.
//
// It talks back to the element through three callbacks passed at construction:
//   • assets      — the resolved assets base URL (trailing "/")
//   • getMap()    — the live MapLibre map (or null before boot)
//   • rebuild()   — re-apply the full style (map.setStyle(buildStyle(), {diff:false}))
//                   when the SET of sources changes (a new source must be created).
// Where a method used to do `this._map.setStyle(this.buildStyle(), …)` it calls
// `this.rebuild()`; where it needs the map it calls `this.getMap()`.
import { PMTilesArchive, MultiArchive } from "./pmtiles-source.mjs";
import { zoomForScalePhysical } from "../lib/util.mjs";

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
export const CHART_BANDS = [
  { slug: "overview", min: 0, max: 7, bake: 7 },
  { slug: "general", min: 7, max: 9, bake: 9 },
  { slug: "coastal", min: 9, max: 11, bake: 13 },
  { slug: "approach", min: 11, max: 13, bake: 15 },
  { slug: "harbor", min: 13, max: 16, bake: 16 },
  { slug: "berthing", min: 16, max: 18, bake: 18 },
  { slug: "all", min: 0, max: 18, bake: 18 },
];

// Lowest display zoom each band's chart layers actually DRAW at — the zoom whose
// physical scale equals the band's COARSE NOAA standard scale, where the band first
// becomes the best-available chart (ENC Design Handbook Table 1, at ~40°N):
// coastal 1:350k≈z9, approach 1:90k≈z11, harbor 1:22k≈z13, berthing 1:4k≈z16.
// Overview/general draw from z0 so they gap-fill on zoom-out. Must match the baker's
// Band.ZoomRange() (a re-bake aligns the tiles); applied as a LAYER minzoom.
export const BAND_DISPLAY_MIN = { overview: 0, general: 0, coastal: 9, approach: 11, harbor: 13, berthing: 16, all: 0 };

// Vector SOURCE-LAYERS whose features carry SCAMIN and are split into per-SCAMIN
// bucket layers (each with a native fractional minzoom) so SCAMIN is honored
// EXACTLY with zero per-zoom work. Point symbols + soundings are the marks that
// "disappear too soon / too late" at scale boundaries; `text` carries the labels
// (incl. light characteristics) baked from the same features, so a label must
// track the display scale of the object it annotates — keyed on the SOURCE-LAYER,
// not the style-layer id, because the text labels fan out into many style layers
// (text-<halign>-<valign>, light-text) that all read the one `text` source-layer.
// `sector_lines` is the LIGHTS06 sector figure (arcs/legs), baked into its own
// source-layer (not the shared `lines`) precisely so it can be bucketed here
// without fanning every coastline/contour into per-SCAMIN variants — the sector
// then cuts at the same exact scale as its light's flare + characteristic text.
// The four area/line *_scamin source-layers carry SCAMIN-bearing AREA/LINE
// primitives (DEPCNT contours, PIPARE, etc.), routed there by the baker's
// scaminLayer() so they bucket exactly like the point/text marks above. Their
// no-SCAMIN counterparts stay in the original (areas/area_patterns/lines/
// complex_lines) layers — single, always-in-band, NOT bucketed.
export const SCAMIN_BUCKET_LAYERS = new Set(["point_symbols", "soundings", "text", "sector_lines",
  "areas_scamin", "area_patterns_scamin", "lines_scamin", "complex_lines_scamin"]);

// Centre-latitude drift (degrees) that triggers a SCAMIN bucket-minzoom rebuild.
// The cutoff zoom shifts with cos(lat); 2° keeps the error under ~0.05 zoom at
// typical latitudes without rebuilding the style on every small pan.
const SCAMIN_LAT_REBUILD_DEG = 2;

// The display zoom at which a 1:N (scamin) feature first becomes visible at the
// given latitude: the zoom whose PHYSICAL display-scale denominator equals scamin.
// FRACTIONAL — used directly as a MapLibre layer minzoom, which gives the exact
// S-52 cutoff with no client-side per-zoom computation.
//
// SCAMIN is "the minimum scale at which the object may be displayed" (S-57 attr
// 133); S-57 Appendix B.1 §2.2.7 defines it as "the display scale below which the
// object is no longer displayed", and S-52 6.1.1 defines Display Scale as the TRUE
// on-glass ratio [distance on display]/[distance on earth]. So we gate against the
// physical display scale at the (calibrated) screen pixel pitch — the SAME scale the
// HUD readout and over-scale use — NOT a fixed web/OGC pixel. zoomForScalePhysical is
// the inverse of that scale, so a SCAMIN 1:N feature vanishes exactly when the screen
// reads 1:N. pxPitch omitted → the CSS-reference pixel (util default).
//
// The baker floors each SCAMIN feature into tiles at floor(scaminZoom) using the
// deterministic OGC pixel (it has no screen). Real screens are FINER than that pixel,
// so this client gate lands at/above the baked floor — the tile always carries the
// feature where we reveal it (gating later than the floor is the safe direction).
export function scaminDisplayZoom(scamin, lat, pxPitch) {
  if (!scamin) return 0;
  return zoomForScalePhysical(scamin, lat, pxPitch);
}

// Server sets are baked PER BAND, named "<district>-<band>" (e.g. noaa-d5-general).
// bandOfSet recovers the band slug from a set name ("all" for a bandless/merged set
// — a user upload or a non-banded pack). BAND_RANK orders sets coarse→fine so a finer
// band's fill draws over a coarser one (the same stacking the per-band pmtiles path
// gets for free). Both let server mode do per-band overzoom + suppression, so a
// coarse-only spot (open water) is filled by the general/overview source overzooming
// instead of blanking to the S-52 no-data hatch.
export const BAND_SLUGS = CHART_BANDS.map((b) => b.slug).filter((s) => s !== "all");
export const BAND_RANK = Object.fromEntries(CHART_BANDS.map((b, i) => [b.slug, i]));
export function bandOfSet(name) {
  const i = name.lastIndexOf("-");
  if (i > 0) { const s = name.slice(i + 1); if (BAND_SLUGS.includes(s)) return s; }
  return "all";
}

export class ChartSources {
  constructor({ assets, getMap, rebuild, getPxPitch }) {
    this.assets = assets;     // resolved assets base URL (trailing "/")
    this.getMap = getMap;     // () => live MapLibre map (or null)
    this.rebuild = rebuild;   // () => map.setStyle(buildStyle(), {diff:false,validate:false})
    this.getPxPitch = getPxPitch || (() => undefined); // () => calibrated CSS-pixel pitch (mm); drives SCAMIN gating
    this._ver = 0;            // chart-tile cache-bust token (see refresh)
    this._bands = {};         // band slug → MultiArchive of that band's loaded packs (chart-<slug> source)
    this._scaminValues = [];  // distinct SCAMIN denominators seen in tiles → per-SCAMIN bucket layers
    this._scaminLat = null;   // latitude the bucket minzooms were computed at (rebuild on big change)
    this._server = false;     // server-tiles mode (tiles="server"): chart sources are /tiles/{set}
    this._serverSets = [];    // active server packs: [{name, min, max}] — one vector source each
    this._scaminRebuildT = null; // debounce timer for the SCAMIN bucket rebuild
  }

  // -- accessors the element (buildStyle / expandChartLayers) reads through ----
  get ver() { return this._ver; }
  bumpVer() { return ++this._ver; }
  get server() { return this._server; }
  get sets() { return this._serverSets; }
  get scaminValues() { return this._scaminValues; }
  // Band slugs that currently have data — server: the active sets' bands; pmtiles:
  // the loaded per-band archives. Drives the overscale-pattern gate (a band gets the
  // AP(OVERSC01) hatch only when a FINER band is present, i.e. a real scale boundary).
  loadedBands() {
    if (this._server) return [...new Set(this._serverSets.map((s) => s.band).filter(Boolean))];
    return Object.keys(this._bands);
  }
  // The latitude the SCAMIN bucket minzooms are computed at. Falls back to the
  // map's LIVE centre latitude until the first idle pass sets it — without this,
  // the initial style (and server mode, which never ran the discovery loop that
  // sets it) computed scaminDisplayZoom at lat 0 (the equator, cos=1), gating
  // every SCAMIN feature ~0.4 zoom too late at mid-latitudes (more further north).
  get scaminLat() {
    if (this._scaminLat != null) return this._scaminLat;
    const m = this.getMap();
    return m ? m.getCenter().lat : 0;
  }
  // The MultiArchive backing the chart-<slug> PMTiles protocol (registered in boot()).
  bandArchive(slug) { return this._bands[slug]; }

  // Set server-tiles mode from the element's `tiles` attribute, and (in server mode)
  // learn each declared set's real zoom range BEFORE the first buildStyle so the
  // source maxzoom is truthful (overzoom, not empty-tile holes). Called from boot().
  async initServerMode(isServer, names = []) {
    this._server = isServer;
    if (this._server) this._serverSets = await this._loadSetMetas(names);
  }

  // Absolute tile-URL template for a server set. MUST be absolute: MapLibre fetches
  // tiles in a Web Worker that has no document base, so a relative "/tiles/…" URL
  // throws "Failed to parse URL".
  _serverTilesUrl(name) {
    const base = new URL(this.assets, location.href).href; // absolute, trailing "/"
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
    const meta = { name, band: bandOfSet(name), min: 0, max: 18, bounds: null, scamin: [], tiles: this._serverTilesUrl(name) };
    try {
      const base = new URL(this.assets, location.href).href;
      const tj = await fetch(`${base}tiles/${name}.json`).then((r) => (r.ok ? r.json() : null));
      if (tj) {
        if (Number.isFinite(tj.minzoom)) meta.min = tj.minzoom;
        if (Number.isFinite(tj.maxzoom)) meta.max = tj.maxzoom;
        if (Array.isArray(tj.bounds) && tj.bounds.length === 4) meta.bounds = tj.bounds; // [w,s,e,n] — host zoom-cap
        if (Array.isArray(tj.scamin)) meta.scamin = tj.scamin; // SCAMIN manifest → per-set bucket layers (no runtime collect)
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

  // Collect the distinct SCAMIN denominators present in the loaded chart tiles and,
  // when that set grows (or the centre latitude shifts enough to move the bucket
  // minzooms), rebuild the style so buildLayers regenerates the per-SCAMIN bucket
  // layers (each gated by a native fractional minzoom). Runs on idle only; the
  // rebuild converges (no new values ⇒ no rebuild), and steady-state SCAMIN gating
  // is then 100% native — zero per-zoom JS.
  _refreshScaminBuckets() {
    const m = this.getMap();
    if (!m) return;
    const lat = m.getCenter().lat;
    // The bucket minzooms are latitude-dependent (scaminDisplayZoom uses cos lat),
    // so rebuild when the centre latitude has drifted enough to shift a cutoff by a
    // sub-perceptible amount. 2° keeps the error well under ~0.05 zoom at typical
    // latitudes (was 5° ≈ 0.1 zoom, which read as features popping a touch early/late).
    const latShift = this._scaminLat == null || Math.abs(lat - this._scaminLat) > SCAMIN_LAT_REBUILD_DEG;
    // Server mode publishes the SCAMIN value set in each set's TileJSON (set.scamin),
    // so it skips the runtime tile discovery below — but the minzooms STILL depend on
    // latitude, so it must track it and rebuild on a shift (this is what previously
    // pinned server buckets to the initial/equator latitude).
    if (this._server) {
      if (!latShift) return;
      // The SCAMIN value set is fixed (from each set's TileJSON); latitude drift only
      // shifts the per-value bucket MINZOOMS — re-gate them in place (no flicker)
      // rather than a full style rebuild.
      clearTimeout(this._scaminRebuildT);
      this._scaminRebuildT = setTimeout(() => this._reapplyScaminMinzooms(), 120);
      return;
    }
    // The SCAMIN value set is PUBLISHED in each PMTiles archive's JSON metadata
    // (baker SetScamin), so read it from the loaded bands — known at LOAD, not
    // scanned from tiles per frame. This is the key flicker fix: zooming surfaces
    // no "new" values, so it never triggers a rebuild. (Older archives without the
    // manifest publish nothing, so the set stays empty and SCAMIN gating is off —
    // re-bake to restore it; this never re-introduces the per-zoom rebuild.)
    const seen = new Set();
    for (const slug of Object.keys(this._bands)) {
      for (const v of this._bands[slug].scamin || []) seen.add(+v);
    }
    const next = [...seen].sort((a, b) => a - b);
    const changed = next.length !== this._scaminValues.length || next.some((v, i) => v !== this._scaminValues[i]);
    if (!changed && !latShift) return;
    this._scaminValues = next;
    clearTimeout(this._scaminRebuildT);
    if (changed) {
      // The value set changed (a pack loaded/unloaded) → new bucket LAYERS are
      // needed, so this case takes the full style rebuild. It fires at pack
      // load/unload, NOT during zoom, so it doesn't flicker the zoom interaction.
      this._scaminLat = lat;
      this._scaminRebuildT = setTimeout(() => { if (this.getMap()) this.rebuild(); }, 450);
    } else {
      // Latitude drift only (same value set): re-gate the existing buckets in place.
      this._scaminRebuildT = setTimeout(() => this._reapplyScaminMinzooms(), 120);
    }
  }

  // Re-apply the latitude-dependent SCAMIN bucket minzooms IN PLACE — no style
  // rebuild, so no flicker / tile reload. Each per-value bucket layer id ends in
  // "#sm<scamin>"; its native minzoom is scaminDisplayZoom(scamin, lat), which
  // drifts slightly with the centre latitude (cos-lat). setLayerZoomRange re-gates
  // each without tearing down sources/sprites (unlike rebuild()'s full setStyle).
  // A value crossing the band floor into the #no bucket self-corrects on the next
  // genuine rebuild; for the sub-2° drift this runs on, the error is < 0.05 zoom.
  _reapplyScaminMinzooms() {
    const m = this.getMap();
    if (!m) return;
    const lat = m.getCenter().lat;
    const pitch = this.getPxPitch();
    let style;
    try { style = m.getStyle(); } catch (e) { return; }
    for (const L of (style && style.layers) || []) {
      const hit = /#sm(\d+(?:\.\d+)?)$/.exec(L.id);
      if (!hit) continue;
      try { m.setLayerZoomRange(L.id, scaminDisplayZoom(+hit[1], lat, pitch), L.maxzoom != null ? L.maxzoom : 24); } catch (e) { /* layer removed mid-update */ }
    }
    this._scaminLat = lat;
  }

  // -- runtime chart API (driven by the <chart-plotter-app> shell, via the element) --

  // Force the chart source to re-request its tiles (after the loaded archive
  // changes). Bumps the version token so the tile URL changes → cache miss →
  // refetch through the chart:// (PMTiles) protocol. Cleaner than rebuilding the
  // whole style (which would re-register sprites/patterns).
  refresh() {
    this._ver++;
    const map = this.getMap();
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
    const map = this.getMap();
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

  // Every MapLibre SourceCache backing the chart source(s). v4 had one at
  // map.style.sourceCaches[id]; v5 renamed that property and can hold a separate
  // paint + symbol cache, so duck-type any cache-shaped dict keyed by a chart
  // source rather than hardcoding the name. (See [[wasm-z7-tile-hole]].)
  _chartSourceCaches() {
    const map = this.getMap();
    const style = map && map.style;
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
    const map = this.getMap();
    // Rebuild the style when the set OF sets changes (sources must be created/
    // recreated). A same-set rebake (same names) just bumps the tile version.
    const changed = !wasServer || this._serverSets.map((s) => s.name).sort().join(",") !== prevKey;
    // diff:false → full _load (build layers directly, validate once + skip). A
    // server install has THOUSANDS of SCAMIN-bucket layers; setStyle's default DIFF
    // applies them via per-op addLayer calls that re-validate + re-serialize the
    // whole style (~28s in a startup profile) — and {validate:false} doesn't reach
    // those internal calls. A full load bypasses all of it.
    if (map && changed) this.rebuild();
    else if (map) this.refresh();
    return this._serverSets.map((s) => s.name);
  }

  // Convenience: render a single server set (or none). See setServerSets.
  setServerSet(name) { return this.setServerSets(name ? [name] : []); }

  // The active server tile-set names ([] when not in server mode).
  serverSets() { return this._server ? this._serverSets.map((s) => s.name) : []; }

  // The active server sets' metadata ({name,band,min,max,bounds}) — so the host's
  // zoom-cap can tell which finest band covers the view centre for imported/inland
  // sets that aren't in the NOAA catalogue. [] when not in server mode.
  serverSetMetas() { return this._server ? this._serverSets.map((s) => ({ ...s })) : []; }

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
    const map = this.getMap();
    if (!map) return;
    // Hold every loaded band source's maxzoom at its archive's REAL deepest baked
    // zoom (PMTiles header), in place — so MapLibre overzooms the deepest tile it
    // has instead of requesting empty tiles past the bake (which read as a blank
    // band when the static band.bake and the actual archive drift, e.g. after a
    // band-range change before a re-bake). No restyle. The merged "all" source has
    // no per-band overzoom so its minzoom tracks the archive too; the per-band
    // sources keep minzoom 0 for the sub-band SCAMIN features.
    for (const slug of Object.keys(this._bands)) {
      const arc = this._bands[slug];
      const src = map.getSource("chart-" + slug);
      if (!src || !arc || src.maxzoom === undefined) continue;
      src.maxzoom = arc.maxZoom;
      if (slug === "all") src.minzoom = arc.minZoom;
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

  // The CHART band sources + server-set sources object — the `sources` half of
  // buildStyle. `v` is the cache-bust token (see refresh): bumping it forces
  // MapLibre to re-request chart tiles. The element merges this with the basemap +
  // nodata sources to assemble the full style.
  sourcesDict(v) {
    // One vector source per NOAA band, each serving the `chart-<band>` protocol
    // over its fixed baked zoom range (overzoomed above max). `{v}` is a
    // cache-bust token bumped by setArchive/refresh. Sources for not-yet-loaded
    // bands resolve to blank tiles (harmless) until an archive is added.
    const sources = {};
    // Per-band prebaked sources. The source maxzoom is the loaded archive's REAL
    // deepest baked zoom (from its PMTiles header), NOT the static band.bake — so
    // MapLibre overzooms the deepest tile it actually has instead of requesting
    // empty tiles past the bake (which read as a whole blank band when band.bake
    // and the archive drift, e.g. after a band-range change before a re-bake). The
    // client overzooms above it (base fills + the finest band fill the finer zooms
    // for free). Falls back to band.bake until an archive is loaded.
    for (const band of CHART_BANDS) {
      const archive = this._bands[band.slug];
      sources["chart-" + band.slug] = {
        type: "vector",
        tiles: [`chart-${band.slug}://${v}/{z}/{x}/{y}`],
        // minzoom 0, not band.min: SCAMIN-bearing features are baked into sub-band
        // tiles (they cross bands down to their SCAMIN scale), so the source must be
        // allowed to fetch those. They're sparse (only SCAMIN objects live below the
        // band min), and minzoom only adds requests when the VIEW is coarse (few
        // tiles), so it's cheap. Per-SCAMIN bucket layers gate the exact display scale.
        minzoom: 0,
        maxzoom: (archive && archive.maxZoom) || band.bake,
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
    return sources;
  }
}
