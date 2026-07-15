// plugin-layers.mjs — the declarative map-layer host for plugins (spec §8, §11).
//
// Plugins draw by describing GeoJSON layers, not by touching MapLibre directly:
// `layers.add(id, spec)` returns `{ setData, remove }`. This host owns the parts
// every overlay used to hand-roll (see the old own-ship `_ensureLayers`):
//
//   • it creates the GeoJSON source + style layers, inserting them in the chosen
//     z-band via the plotter's overlay API (so plugin layers can't paint over the
//     safety-critical S-52 labels), and
//   • it re-adds them after a style rebuild (`setStyle({diff:false})` drops every
//     plugin source/layer) and re-seeds the last `setData` payload — so a plugin
//     never reimplements style-reload self-healing.
//
// It is modeled on the AIS overlay's source-`setData` path and is the single place
// that reaches the plotter's overlay API, keeping the plugin `ctx` free of it.

const EMPTY = { type: "FeatureCollection", features: [] };

// Named z-bands plugins select from (never extend, spec §7). Each maps to the
// plotter's belowLabels flag: "overlay" sits beneath chart text/symbol labels (the
// safe default — own-ship's predictor/heading lines live here); "top" floats above
// everything.
const BANDS = { overlay: { belowLabels: true }, top: { belowLabels: false } };

export class PluginLayers {
  constructor({ map, plotter }) {
    this._map = map;
    this._plotter = plotter;
    this._layers = new Map(); // key -> record
    // A style rebuild drops every plugin source/layer; re-add them all. Registered
    // once at construction so it catches rebuilds that fire after a plugin loads.
    this._onStyle = () => {
      for (const rec of this._layers.values()) this._ensure(rec);
    };
    map.on("style.load", this._onStyle);
  }

  // add registers a declarative layer for a plugin. `id` is namespaced by the host
  // with the plugin id (identity-scoped, spec §7). spec:
  //   { band?: "overlay"|"top",
  //     type, paint, layout,                      // single-layer shorthand
  //     layers: [{ type, paint, layout }, …] }    // or several sharing one source
  // Returns { setData(featureCollection), remove() }.
  add(pluginId, id, spec) {
    const key = `${pluginId}:${id}`;
    const rec = { key, srcId: `${key}-src`, spec, data: EMPTY, layerIds: [] };
    this._layers.set(key, rec);
    this._ensure(rec);
    return {
      setData: (fc) => {
        rec.data = fc || EMPTY;
        // Self-heal: a style rebuild may have dropped the source between the
        // style.load listener and this call (own-ship hit exactly this), so
        // re-ensure before writing.
        this._ensure(rec);
        const src = this._map.getSource(rec.srcId);
        if (src) src.setData(rec.data);
      },
      remove: () => this._remove(key),
    };
  }

  // _ensure (re)creates the source + layers idempotently, seeding the last data.
  _ensure(rec) {
    const map = this._map;
    if (!map.isStyleLoaded()) return; // style.load will call back
    if (!map.getSource(rec.srcId)) {
      map.addSource(rec.srcId, { type: "geojson", data: rec.data });
    }
    const band = BANDS[rec.spec.band] || BANDS.overlay;
    const specs = rec.spec.layers || [rec.spec];
    rec.layerIds = [];
    specs.forEach((ls, i) => {
      const lid = `${rec.key}-l${i}`;
      rec.layerIds.push(lid);
      this._plotter.addOverlayLayer(
        { id: lid, type: ls.type, source: rec.srcId, layout: ls.layout || {}, paint: ls.paint || {} },
        { belowLabels: band.belowLabels },
      );
    });
  }

  _remove(key) {
    const rec = this._layers.get(key);
    if (!rec) return;
    this._layers.delete(key);
    this._plotter.removeOverlay(rec.layerIds, rec.srcId);
  }

  // Tear down every layer for one plugin (called when the plugin unloads).
  removeAll(pluginId) {
    const prefix = pluginId + ":";
    for (const key of [...this._layers.keys()]) {
      if (key.startsWith(prefix)) this._remove(key);
    }
  }

  destroy() {
    if (this._onStyle) this._map.off("style.load", this._onStyle);
    for (const key of [...this._layers.keys()]) this._remove(key);
  }
}
