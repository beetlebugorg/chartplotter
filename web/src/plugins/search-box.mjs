// search-box.mjs — the offline search controller: matches a typed query against
// the chart catalog (place / chart titles + numbers) and the loaded chart vector
// features, renders the results dropdown, and flies the map to a picked hit.
//
// Controller pattern (like radar.mjs / hud.mjs): a plain class with injected
// accessors — it owns the search LOGIC + results rendering, but the input + the
// dropdown markup live in the shell chrome (the shell wires its <input> handlers
// to delegate here). Inject:
//
//   new SearchBox({
//     getMap,                     // () => MapLibre map (fly-to + querySourceFeatures), late-bound
//     getResultsEl, getInput,     // () => shadow-root elements (input + dropdown)
//     getSearchPop, getSearchTab, // () => the flyout + its nav tab
//     getCatalog,                 // () => iterable of catalog cells ({n,l,s,bb})
//     isChartSource,              // (srcName) => is this a chart vector source?
//     classLabel, layerLabel,     // S-57 acronym / MVT source-layer → human label
//     positionCaret,              // (pop, tab) => re-anchor the flyout caret
//   });
//   sb.doSearch(query);           // input → render results
//   sb.gotoHit(i);                // result-click / Enter → fly + close

import { esc } from "../lib/util.mjs";

export class SearchBox {
  constructor(opts) {
    this._getMap = opts.getMap || (() => null);
    this._getResultsEl = opts.getResultsEl;
    this._getInput = opts.getInput;
    this._getSearchPop = opts.getSearchPop;
    this._getSearchTab = opts.getSearchTab;
    this._getCatalog = opts.getCatalog || (() => []);
    this._isChartSource = opts.isChartSource || (() => false);
    this._classLabel = opts.classLabel || (() => "");
    this._layerLabel = opts.layerLabel || (() => "");
    this._positionCaret = opts.positionCaret || (() => {});
    this._hits = [];
  }

  // -- search: catalog (places/charts) + loaded chart feature data ---------
  doSearch(q) {
    const el = this._getResultsEl();
    if (!el) return;
    const needle = q.trim().toLowerCase();
    // BROWSE mode: nothing typed yet (or too short to fuzzy-match). Instead of a
    // blank box, list the active charts — so you can find one without knowing its
    // name. Ordered NEAREST-to-view first (most relevant to where you're looking),
    // capped, with a "type to narrow" footer when there are more.
    const browse = needle.length < 2;
    const BROWSE_LIMIT = 40;
    let center = null;
    try { const c = this._getMap().getCenter(); center = [c.lng, c.lat]; } catch {}

    // 1) Catalog cells (active installed charts), fuzzy-matched — or all, in browse.
    const cells = [];
    for (const c of this._getCatalog()) {
      if (!Array.isArray(c.bb) || c.bb.length !== 4) continue;
      if (browse) { cells.push({ c, score: 0 }); continue; }
      const score = Math.max(fuzzyScore(needle, (c.l || "").toLowerCase()), fuzzyScore(needle, c.n.toLowerCase()));
      if (score >= 0) cells.push({ c, score });
    }
    if (browse) {
      const d2 = (c) => center ? ((c.bb[0] + c.bb[2]) / 2 - center[0]) ** 2 + ((c.bb[1] + c.bb[3]) / 2 - center[1]) ** 2 : 0;
      cells.sort((a, b) => (d2(a.c) - d2(b.c)) || (a.c.n < b.c.n ? -1 : 1)); // nearest first, then by name
    } else {
      cells.sort((a, b) => (b.score - a.score) || ((b.c.s || 0) - (a.c.s || 0))); // best match; ties → coarser chart
    }
    // 2) Loaded chart features (skip in browse — there's no query to match).
    const feats = browse ? [] : this._searchFeatures(needle);
    const more = browse && cells.length > BROWSE_LIMIT;
    const hits = [...cells.slice(0, browse ? BROWSE_LIMIT : 5).map(({ c }) => ({ type: "cell", c })), ...feats.slice(0, 8)];
    this._hits = hits;
    const rows = hits.map((h, i) => {
      const sel = i === 0 ? " sel" : "";
      if (h.type === "cell") { const sub = h.c.s ? `Chart · ${esc(h.c.n)} · 1:${h.c.s.toLocaleString()}` : `Chart · ${esc(h.c.n)}`; return `<div class="sr-item${sel}" data-i="${i}"><div class="t">${esc(h.c.l || h.c.n)}</div><div class="s">${sub}</div></div>`; }
      return `<div class="sr-item${sel}" data-i="${i}"><div class="t">${esc(h.label)}</div><div class="s">${esc(h.sub)}</div></div>`;
    });
    if (hits.length) {
      if (more) rows.push(`<div class="sr-item"><span class="muted">${cells.length} charts — type to narrow</span></div>`);
      el.innerHTML = rows.join("");
    } else {
      el.innerHTML = `<div class="sr-item"><span class="muted">${browse ? "No installed charts" : "No matches in view"}</span></div>`;
    }
    el.hidden = false;
    el.querySelectorAll(".sr-item[data-i]").forEach((d) => (d.onmousedown = (e) => { e.preventDefault(); this.gotoHit(+d.dataset.i); }));
    this.position(); // re-align to the search tab as the result count changes the height
  }

  // Search the loaded chart vector tiles across EVERY attribute value (name, class,
  // readable type, and any other string field). Limited to currently-loaded tiles
  // (roughly the area you've viewed), since that's all the data the client holds.
  _searchFeatures(needle) {
    const map = this._getMap(); if (!map) return [];
    let sources;
    try { sources = Object.keys(map.getStyle().sources || {}).filter(this._isChartSource); } catch { return []; }
    const layers = ["point_symbols", "soundings", "areas", "area_patterns", "lines", "complex_lines", "text"];
    const seen = new Set(), out = [];
    for (const src of sources) {
      for (const layer of layers) {
        let feats; try { feats = map.querySourceFeatures(src, { sourceLayer: layer }); } catch { continue; }
        for (const f of feats) {
          const p = f.properties || {};
          const objnam = p.objnam || "", cls = p.class || "";
          const typeName = this._classLabel(cls) || this._layerLabel(layer) || cls || layer;
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

  gotoHit(i) {
    const h = (this._hits || [])[i];
    const map = this._getMap();
    if (!h || !map) return;
    if (h.type === "feat") map.flyTo({ center: [h.lng, h.lat], zoom: Math.max(map.getZoom(), 14), duration: 800 });
    else { const c = h.c; map.fitBounds([[c.bb[0], c.bb[1]], [c.bb[2], c.bb[3]]], { padding: 80, maxZoom: 13, duration: 800 }); }
    const el = this._getResultsEl(); if (el) el.hidden = true;
    // Keep the query (and selected highlight) so reopening search returns you to
    // the same results — the input is persisted, not cleared.
    const si = this._getInput(); if (si) si.blur();
    const pop = this._getSearchPop(); if (pop) pop.hidden = true;
    const tab = this._getSearchTab(); if (tab) tab.classList.remove("on");
  }

  // Re-point the flyout caret at the search button (called on open and after each
  // query, since the result count changes the flyout height).
  position() {
    const pop = this._getSearchPop(), tab = this._getSearchTab();
    if (!pop || pop.hidden || !tab) return;
    this._positionCaret(pop, tab);
  }
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
