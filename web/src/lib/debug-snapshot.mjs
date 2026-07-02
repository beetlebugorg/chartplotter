// debug-snapshot.mjs — machine-readable map/feature debug snapshots, shared by
// the dev-tools Inspect copy (dev-tools.mjs) and the pick report's copy-feature
// button (pick-report.mjs via the shell). Pure functions over a MapLibre map and
// queryRenderedFeatures results — no app state, safe to import anywhere.

// The map camera, compact (share-view precision). Null without a map.
export function viewSnapshot(map) {
  if (!map) return null;
  const c = map.getCenter();
  return { center: [+c.lng.toFixed(6), +c.lat.toFixed(6)], zoom: +map.getZoom().toFixed(3), bearing: +map.getBearing().toFixed(1) };
}

// Live SCAMIN/smax gate denominators per gated layer — a feature "in the tile
// but not rendered" is almost always a gate question, so snapshots should answer
// it directly (frozen denoms diagnosed a phantom-cutoff bug).
export function gatesSnapshot(map) {
  const gates = {};
  if (!map || !map.getStyle) return gates;
  try {
    for (const l of (map.getStyle().layers || [])) {
      if (!l.filter) continue;
      const s = JSON.stringify(l.filter);
      if (!s.includes('"scamin"') && !s.includes('"smax"')) continue;
      let sc = null, sm = null;
      (function walk(n) {
        if (!Array.isArray(n)) return;
        if (n[0] === ">=" && JSON.stringify(n[1]).includes('"scamin"')) sc = n[2];
        if (n[0] === "<" && JSON.stringify(n[1]).includes('"smax"')) sm = n[2];
        n.forEach(walk);
      })(l.filter);
      gates[l.id] = { scamin: typeof sc === "number" ? Math.round(sc) : sc, smax: typeof sm === "number" ? Math.round(sm) : sm };
    }
  } catch (e) { gates.error = String(e); }
  return gates;
}

// One queried feature, reduced to its transportable identity + shape + baked data.
export function featureSnapshot(f) {
  return { source: f.source, sourceLayer: f.sourceLayer, geometry: f.geometry, properties: f.properties };
}

// The single-feature snapshot the pick report copies: enough to re-find the object
// (source/layer/geometry/properties) plus the live gates that decide whether it
// renders — so a pasted report is self-diagnosing.
export function featureDebugSnapshot(map, feature) {
  return {
    when: new Date().toISOString(),
    view: viewSnapshot(map),
    feature: featureSnapshot(feature),
    gates: gatesSnapshot(map),
  };
}
