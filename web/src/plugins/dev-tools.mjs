// dev-tools.mjs — the slim, contributed developer tools (Advanced tab).
//
// This is the FIRST non-core contributor to the SettingsRegistry: on construction
// it registers itself as an Advanced-tab contribution with a `render(host)` escape
// hatch, so it appears in Settings → Advanced purely through the contribution
// mechanism (validating that architecture). It is a plain class — NOT a custom
// element — like the map controllers (HudController / CoverageBoxes): constructed
// with injected accessors only, no shell coupling, no shell import.
//
// It keeps just TWO tools (the rest were pitched):
//   • Rebuild all charts — re-bake every installed NOAA/IENC district into per-band
//     tile sets from the cells already on the server (no NOAA re-download).
//   • Feature inspector — CSS-devtools-style S-57 attribute inspection: hover to
//     highlight + preview the feature under the cursor, click to lock, SHIFT+drag to
//     capture every chart feature in a region. Renders its result into the settings
//     dialog's custom-render host while the Advanced tab is open.
//
//   const dt = new DevTools({ registry, map, plotter, api, notify, ...accessors });
//   dt.setActive(true);   // the Advanced tab is showing (re-render gating)
//   dt.setInspectMode(false); // shell disarms inspect on section/drawer change
//   dt.destroy();         // teardown (unregister + drop map listeners)
//
// The inspector's map listeners (hover / click-to-lock / SHIFT+drag) live here and
// no-op unless inspect mode is on, so the shell's own click handler only needs to
// defer to `dt.inspecting`. The inspect highlight sources (`inspect`,
// `inspect-focus`) are added by the shell's addCatalogOverlay (they must survive a
// style rebuild, which the shell re-runs); this just sets their data.

import { esc, copyText, flashBtn } from "../lib/util.mjs";
import { devToolsPanel, featureCard, lockNote, emptyHint, areaHint, areaMore, cycler, STYLE } from "./dev-tools.view.mjs";

export { STYLE };

const INSPECT_HINT = "Hover to inspect · click to lock · SHIFT+drag to capture an area.";

// Is this MapLibre source one of our chart vector sources? (realtime "chart" or a
// legacy per-band "chart-<band>".) Local copy so this module needn't import the shell.
function isChartSource(s) { return typeof s === "string" && (s === "chart" || s.startsWith("chart-")); }

// Does geometry `g` overlap the lon/lat box [W,S,E,N]? (bbox test; points exact.)
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

export class DevTools {
  // Injected deps (all that the two kept tools touch):
  //   registry  — SettingsRegistry (this registers itself as an Advanced contribution)
  //   map       — the MapLibre map (inspect query/highlight + listeners)
  //   plotter   — the renderer (flushTiles after a rebake)
  //   notify    — NotificationCenter (unused directly; progress flows via setProgress)
  //   isBusy()  — a download/rebake/task is running (disables the rebake button)
  //   assets    — base URL prefix for api/ fetches (rebake hits api/packs, api/import)
  //   setProgress(p|null) — post rebake progress to the notification pill
  //   setTask(running)    — mark the shell's task running (so isBusy reflects rebake)
  //   pollImport(job, onProg, label) — poll a server import job to completion
  //   districtCellNames(cg) — the catalog cell names in a NOAA CG district
  //   setLabel(name)       — human label for a set name (provider · pack)
  //   chartLib()           — the <chart-library> element (ienc catalogue/packs/refresh/busy)
  //   renderInstalledSets() — re-render installed packs onto the map after a rebake
  //   s57Label(acr)        — S-57 class acronym → human label (for inspector cards)
  //   layerLabel(srcLayer) — MVT source-layer → fallback label
  //   onInspectOn()        — shell hook fired when inspect arms (close the user pick
  //                          report + cancel any armed box-download — mutual exclusion)
  constructor(deps = {}) {
    this._d = deps;
    this._map = deps.map || null;
    this._host = null;     // the contribution's custom-render host (a div in the dialog shadow)
    this._active = false;  // the Advanced tab is currently showing
    // Inspector state (lifted from the shell).
    this._inspectMode = false;
    this._inspectLocked = false;
    this._inspectFeats = [];
    this._inspectIdx = 0;
    this._inspectMulti = false;
    this._inspectLastKey = "";
    this._areaCleanup = null; // mirrors the shell's box-download guard (kept null here)
    this._busy = false;       // a rebake is running (in addition to the injected isBusy)

    this._registerSelf(deps.registry);
    this._wireMap();
  }

  // --- contribution registration ------------------------------------------
  _registerSelf(registry) {
    if (!registry) return;
    this._unregister = registry.register({
      id: "dev-tools",
      tab: { id: "advanced", label: "Advanced" },
      order: 5, // after the core "Cell boundaries" toggle (order 4) on the same tab
      render: (host) => this._render(host),
    });
  }

  // The Advanced tab is showing (or not). Gates the live inspector re-render.
  setActive(on) { this._active = !!on; }

  get inspecting() { return this._inspectMode; }

  destroy() {
    try { this.setInspectMode(false); } catch (e) { /* map gone */ }
    this._unwireMap();
    if (this._unregister) { this._unregister(); this._unregister = null; }
    this._host = null;
  }

  // --- render into the settings dialog host --------------------------------
  // Build the panel skeleton into `host` (a div in the dialog's shadow) and wire
  // the buttons. Keep the host ref so live inspector updates can re-render it.
  _render(host) {
    if (!host) return;
    this._host = host;
    const busy = this._isBusy();
    // The dialog's shadow has its own sheet; inject our chrome once per render so
    // the dev-tools + inspector classes resolve inside it.
    host.innerHTML = `<style>${STYLE}</style>${devToolsPanel(busy, this._inspectMode)}`;
    const q = (id) => host.querySelector("#" + id);
    const rebuild = q("dev-rebuild"); if (rebuild && !rebuild.disabled) rebuild.onclick = (e) => this._rebuildAllPerBand(e.currentTarget);
    const inspect = q("dev-inspect"); if (inspect) inspect.onclick = () => this.setInspectMode(!this._inspectMode);
    const feat = q("dev-feat"); if (feat && !feat.disabled) feat.onclick = (e) => this._copyInspectDebug(e.currentTarget);
    // If inspect is on with a live selection, repaint the result panel.
    if (this._inspectMode && this._inspectFeats.length) this._renderInspect();
    else if (this._inspectMode) this._inspectHint(INSPECT_HINT);
  }

  _isBusy() {
    if (this._busy) return true;
    try { return !!(this._d.isBusy && this._d.isBusy()); } catch { return false; }
  }

  // Re-render the panel (and thus reflect inspect/busy state) if it's mounted.
  _refreshPanel() { if (this._host) this._render(this._host); }

  // --- rebake (Rebuild all charts) -----------------------------------------
  // Re-bake every installed NOAA/IENC district into per-band tile sets from the
  // cells ALREADY on the server (no NOAA re-download). The CLIENT supplies each
  // district's cell list (from its catalogue) since the server doesn't track
  // membership. Runs the districts one at a time, surfacing progress through the
  // notification pill. user/import/legacy packs are skipped (no client cell list).
  async _rebuildAllPerBand(btn) {
    const d = this._d;
    if (this._isBusy()) { if (btn) flashBtn(btn, "busy"); return; }
    const assets = d.assets || "";
    const chartLib = d.chartLib ? d.chartLib() : null;
    let packs = [];
    try { packs = ((await fetch(`${assets}api/packs`).then((r) => (r.ok ? r.json() : null))) || {}).packs || []; } catch (e) { /* offline */ }
    // Load the IENC catalogue so we know each installed river pack's cells.
    if (packs.some((p) => p.name.startsWith("ienc-"))) { try { await (chartLib ? chartLib._iencCatalog() : Promise.resolve()); } catch (e) { /* skip ienc */ } }
    const iencPacks = (chartLib ? chartLib._providerPacks("ienc") : null) || [];
    const todo = [];
    for (const p of packs) {
      const m = /^noaa-d(\d+)$/.exec(p.name);
      if (m) { const names = d.districtCellNames ? d.districtCellNames(+m[1]) : []; if (names.length) todo.push({ set: p.name, label: d.setLabel(p.name), names }); continue; }
      if (p.name.startsWith("ienc-")) {
        const pk = iencPacks.find((x) => x.key === p.name);
        const names = pk && pk.cells ? pk.cells.map((c) => c.name) : [];
        if (names.length) todo.push({ set: p.name, label: d.setLabel(p.name), names });
      }
    }
    if (!todo.length) { if (btn) flashBtn(btn, "nothing to rebuild"); return; }
    this._busy = true;
    if (d.setTask) d.setTask(true);
    this._refreshPanel(); // disable the button while running
    let done = 0;
    for (const j of todo) {
      if (d.setProgress) d.setProgress({ label: "Rebuilding charts", pill: `Rebuilding ${j.label}`, sub: `${done + 1} of ${todo.length} · ${j.names.length} charts`, frac: done / todo.length });
      try {
        const res = await fetch(`${assets}api/import?set=${encodeURIComponent(j.set)}&cells=${encodeURIComponent(j.names.join(","))}`, { method: "POST" });
        const job = await res.json().catch(() => ({}));
        if (job.job && d.pollImport) await d.pollImport(job.job, (p) => d.setProgress && d.setProgress(p), j.label);
      } catch (e) { console.warn("[rebuild]", j.set, e); }
      done++;
    }
    this._busy = false;
    if (d.setTask) d.setTask(false);
    if (d.setProgress) d.setProgress(null);
    if (d.renderInstalledSets) { try { await d.renderInstalledSets(); } catch (e) { /* ignore */ } }
    const pl = d.plotter;
    if (pl && pl.flushTiles) { try { await pl.flushTiles(); } catch (e) { /* ignore */ } } // re-fetch freshly-baked tiles
    if (chartLib) chartLib.refresh();
    this._refreshPanel();
    if (btn) flashBtn(btn, `✓ rebuilt ${todo.length}`);
  }

  // --- feature inspector ---------------------------------------------------
  // Add the inspector's map listeners ONCE. They all no-op unless inspect mode is
  // on (so the shell's own click handler need only defer to `inspecting`). The
  // inspect highlight sources are added by the shell (survive style rebuild).
  _wireMap() {
    const map = this._map;
    if (!map) return;
    let boxStart = null, boxEl = null;
    this._onMouseDown = (e) => {
      if (!this._inspectMode || this._areaCleanup || !e.originalEvent.shiftKey) return;
      e.preventDefault();
      map.dragPan.disable();
      boxStart = e.point;
      boxEl = document.createElement("div");
      boxEl.style.cssText = "position:absolute;z-index:1000;border:2px solid #ff5252;background:rgba(255,82,82,.12);pointer-events:none;box-sizing:border-box;border-radius:2px;";
      map.getContainer().appendChild(boxEl);
    };
    this._onMouseMove = (e) => {
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
    };
    this._onMouseUp = (e) => {
      if (!boxStart) return;
      const a = boxStart, b = e.point;
      if (boxEl && boxEl.parentNode) boxEl.parentNode.removeChild(boxEl);
      boxEl = null; boxStart = null;
      map.dragPan.enable();
      if (Math.abs(b.x - a.x) < 3 || Math.abs(b.y - a.y) < 3) return; // too small → ignore
      this._showInspectArea(this._captureArea(a, b));
    };
    this._onClick = (e) => {
      if (!this._inspectMode) return; // shell handles non-inspect clicks (pick/coverage)
      if (this._areaCleanup || e.originalEvent.shiftKey) return; // shift = box
      if (this._inspectLocked) { this._inspectLocked = false; this._inspectAt(e.point, false); return; }
      this._inspectAt(e.point, true); // lock onto whatever's here
    };
    map.on("mousedown", this._onMouseDown);
    map.on("mousemove", this._onMouseMove);
    map.on("mouseup", this._onMouseUp);
    map.on("click", this._onClick);
  }

  _unwireMap() {
    const map = this._map;
    if (!map) return;
    if (this._onMouseDown) map.off("mousedown", this._onMouseDown);
    if (this._onMouseMove) map.off("mousemove", this._onMouseMove);
    if (this._onMouseUp) map.off("mouseup", this._onMouseUp);
    if (this._onClick) map.off("click", this._onClick);
  }

  // Arm/disarm feature-inspect interaction (crosshair, hover/click capture,
  // SHIFT+drag area select). The inspector panel lives in the settings host.
  setInspectMode(on) {
    on = !!on;
    if (on === this._inspectMode) return;
    this._inspectMode = on;
    if (on && this._d.onInspectOn) this._d.onInspectOn(); // close pick report + cancel box-download (mutual exclusion)
    this._inspectLocked = false;
    this._inspectLastKey = "";
    const map = this._map;
    if (map) {
      map.getCanvas().style.cursor = "crosshair"; // chart default is also crosshair
      // Free SHIFT+drag for area capture (MapLibre uses it for box-zoom by default).
      if (on) map.boxZoom.disable(); else map.boxZoom.enable();
    }
    if (on) this._inspectHint(INSPECT_HINT);
    else this._closeInspect();
    // The in-panel "Inspect features" button reflects on/off — repaint it.
    this._refreshPanel();
  }

  // Inspect the chart features at a canvas point. `lock` freezes the panel on a
  // hit; a no-hit lock is a no-op; a no-hit hover shows the hint.
  _inspectAt(point, lock) {
    const map = this._map;
    if (!map) return;
    const feats = map.queryRenderedFeatures(point).filter((f) => isChartSource(f.source) && !f.layer.id.startsWith("scaminprobe"));
    if (!feats.length) {
      if (lock) return;
      this._inspectLastKey = "";
      this._inspectFeats = [];
      const src = map.getSource("inspect");
      if (src) src.setData({ type: "FeatureCollection", features: [] });
      this._inspectHint(INSPECT_HINT);
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

  // Capture EVERY chart feature in the dragged pixel box (corners a,b) via
  // querySourceFeatures across every loaded chart band, deduped, geo-filtered.
  _captureArea(a, b) {
    const map = this._map;
    const tl = map.unproject([Math.min(a.x, b.x), Math.min(a.y, b.y)]);
    const br = map.unproject([Math.max(a.x, b.x), Math.max(a.y, b.y)]);
    const W = Math.min(tl.lng, br.lng), E = Math.max(tl.lng, br.lng);
    const S = Math.min(tl.lat, br.lat), N = Math.max(tl.lat, br.lat);
    const inBox = (g) => geomIntersectsBox(g, W, S, E, N);
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
  // a list (locked), highlighting them all.
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

  // Render the inspected feature(s) into the settings host's #inspect-body: a
  // single card + cycler for a point pick, or the full list for an area capture.
  _renderInspect() {
    const feats = this._inspectFeats || [];
    if (!feats.length) return;
    const map = this._map;
    const src = map && map.getSource("inspect");
    const body = this._host && this._host.querySelector("#inspect-body");
    if (!body) return; // the inspector panel only exists while Settings → Advanced is open
    const note = this._inspectLocked ? lockNote() : "";
    if (this._inspectMulti) {
      const cap = 80;
      const shown = feats.slice(0, cap);
      if (src) src.setData({ type: "FeatureCollection", features: feats.map((f) => ({ type: "Feature", properties: {}, geometry: f.geometry })) });
      this._clearInspectFocus();
      const more = feats.length > cap ? areaMore(feats.length - cap) : "";
      body.innerHTML = note + areaHint(feats.length) + shown.map((f, i) => this._featureCard(f, i)).join("") + more;
      body.querySelectorAll(".ins-feat[data-fi]").forEach((el) => (el.onclick = () => this._focusInspectFeature(+el.dataset.fi)));
      return;
    }
    const i = Math.min(this._inspectIdx, feats.length - 1);
    const f = feats[i];
    if (src) src.setData({ type: "FeatureCollection", features: [{ type: "Feature", properties: {}, geometry: f.geometry }] });
    const cyc = feats.length > 1 ? cycler(i, feats.length) : "";
    body.innerHTML = note + cyc + this._featureCard(f);
    if (feats.length > 1) {
      const prev = body.querySelector("#ins-prev"), next = body.querySelector("#ins-next");
      if (prev) prev.onclick = () => this._inspectStep(-1);
      if (next) next.onclick = () => this._inspectStep(1);
    }
  }

  // Build one feature card (resolves the labels via injected lookups).
  _featureCard(f, idx) {
    const acr = (f.properties || {}).class || "";
    const named = this._d.s57Label ? this._d.s57Label(acr) : "";
    const label = named || (this._d.layerLabel ? this._d.layerLabel(f.sourceLayer) : "") || acr || f.sourceLayer || "Feature";
    return featureCard(f, { label, acr, named }, (k, v) => this._fmtInspectVal(k, v), idx);
  }

  // Friendlier rendering for a few baked enum/typed attributes.
  _fmtInspectVal(k, v) {
    if (k === "cat") return ["base", "standard", "other"][v] ?? String(v);
    if (k === "bnd") return ["plain", "symbolized", "common"][v] ?? String(v);
    if ((k === "depth" || k === "danger_depth" || k === "drval1" || k === "drval2") && v !== "" && v != null && !isNaN(v)) return `${v} m`;
    return String(v);
  }

  // Isolate one feature from the area list: paint it cyan over the dim red set.
  _focusInspectFeature(i) {
    const f = (this._inspectFeats || [])[i];
    if (!f || !this._map) return;
    const src = this._map.getSource("inspect-focus");
    if (src) src.setData({ type: "FeatureCollection", features: [{ type: "Feature", properties: {}, geometry: f.geometry }] });
    if (this._host) this._host.querySelectorAll(".ins-feat[data-fi]").forEach((el) => el.classList.toggle("active", +el.dataset.fi === i));
  }

  _clearInspectFocus() {
    const src = this._map && this._map.getSource("inspect-focus");
    if (src) src.setData({ type: "FeatureCollection", features: [] });
  }

  // Step the cycler through overlapping features (wraps).
  _inspectStep(dir) {
    const n = (this._inspectFeats || []).length;
    if (!n) return;
    this._inspectIdx = (this._inspectIdx + dir + n) % n;
    this._renderInspect();
  }

  _inspectHint(msg) {
    const body = this._host && this._host.querySelector("#inspect-body");
    if (body) body.innerHTML = emptyHint(msg);
  }

  _closeInspect() {
    this._inspectLocked = false;
    this._inspectLastKey = "";
    this._inspectFeats = [];
    this._inspectIdx = 0;
    this._inspectMulti = false;
    const body = this._host && this._host.querySelector("#inspect-body");
    if (body) body.innerHTML = ""; // drop any feature cards
    const map = this._map;
    if (map) {
      map.getCanvas().style.cursor = "crosshair"; // back to the chart pick cursor
      const src = map.getSource("inspect");
      if (src) src.setData({ type: "FeatureCollection", features: [] });
      this._clearInspectFocus();
    }
  }

  // Copy a debug snapshot of the current inspector selection — source/layer, baked
  // properties, GeoJSON geometry, plus the map view — to the clipboard AND POST it
  // to api/debug so it can be pulled server-side.
  async _copyInspectDebug(btn) {
    const m = this._map;
    let view = null;
    if (m) {
      const c = m.getCenter();
      view = { center: [+c.lng.toFixed(6), +c.lat.toFixed(6)], zoom: +m.getZoom().toFixed(3), bearing: +m.getBearing().toFixed(1) };
    }
    const feats = this._inspectFeats || [];
    const pick = this._inspectMulti ? feats.slice(0, 80) : (feats.length ? [feats[Math.min(this._inspectIdx, feats.length - 1)]] : []);
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
    const assets = this._d.assets || "";
    const text = JSON.stringify(snap, null, 2);
    const ok = await copyText(text);
    fetch(`${assets}api/debug`, { method: "POST", headers: { "content-type": "application/json" }, body: text }).catch(() => {});
    flashBtn(btn, ok ? "✓" : "✗");
  }
}
