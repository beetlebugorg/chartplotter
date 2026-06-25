// AISOverlay renders other vessels from the live AIS feed (/api/ais/stream). Like
// own-ship it is a pure consumer of server-decoded data — the server turns VDM/VDO
// (or, in future, NMEA2000 PGNs) into AIS targets; this just draws them.
//
// Each target is a heading-rotated OpenBridge AIS glyph: the directional triangle
// when COG/heading is known, the no-heading variant otherwise. Colour + halo come
// from --ais-* CSS vars (set per scheme on the app host), so targets honour
// day/dusk/night. The server prunes stale targets, so a target dropping out of the
// list removes its marker.

import {
  AIS_TARGET_ICON, AIS_TARGET_NODIR_ICON,
  AIS_TARGET_DANGER_ICON, AIS_TARGET_DANGER_NODIR_ICON,
} from "../lib/openbridge-icons.mjs";
import { fmtLatLon } from "./target-info.mjs";
import { format } from "../lib/units.mjs";

// Pick the AIS glyph for a target's directionality + collision danger.
function glyphFor(hasDir, danger) {
  if (danger) return hasDir ? AIS_TARGET_DANGER_ICON : AIS_TARGET_DANGER_NODIR_ICON;
  return hasDir ? AIS_TARGET_ICON : AIS_TARGET_NODIR_ICON;
}

const GLYPH_STYLE =
  "line-height:0;pointer-events:auto;cursor:pointer;will-change:transform;" +
  "color:var(--ais-fill,#0a7d55);" +
  "filter:drop-shadow(0 0 1px var(--ais-halo,#fff)) drop-shadow(0 0 1px var(--ais-halo,#fff));";

export class AISOverlay {
  constructor({ map, assets = "/", widget = false, onSelect, units } = {}) {
    this._map = map;
    this._assets = assets;
    this._widget = widget;
    this._units = units; // () => mariner prefs, for SOG/CPA/draught units (live)
    this._onSelect = onSelect; // tap → info picker
    this._markers = new Map(); // mmsi -> {marker, el, hasDir}
    this._es = null;
    this._polling = false;
    this.start();
  }

  start() {
    if (this._widget || this._es || this._polling) return; // AIS feed needs the server
    if (!window.EventSource) {
      this._poll();
      return;
    }
    const es = new EventSource(this._assets + "api/ais/stream");
    es.onmessage = (ev) => {
      let d;
      try {
        d = JSON.parse(ev.data);
      } catch {
        return;
      }
      this._apply(d.targets || []);
    };
    es.onerror = () => {}; // EventSource auto-reconnects
    this._es = es;
  }

  _apply(targets) {
    const seen = new Set();
    for (const t of targets) {
      // Skip targets we can't place: no position yet (e.g. static-only msg 24/5
      // before a position report), which would otherwise render at 0,0.
      if (typeof t.lat !== "number" || (!t.lat && !t.lon)) continue;
      seen.add(t.mmsi);
      this._upsert(t);
    }
    for (const [mmsi, rec] of this._markers) {
      if (!seen.has(mmsi)) {
        rec.marker.remove();
        this._markers.delete(mmsi);
      }
    }
  }

  _upsert(t) {
    let rec = this._markers.get(t.mmsi);
    if (!rec) {
      const el = document.createElement("div");
      el.style.cssText = GLYPH_STYLE;
      el.title = t.name || String(t.mmsi);
      const marker = new window.maplibregl.Marker({ element: el, rotationAlignment: "map", anchor: "center" });
      marker.setLngLat([t.lon, t.lat]); // must set a location before addTo (maplibre reads it)
      rec = { marker, el, hasDir: undefined, t };
      el.addEventListener("click", (e) => {
        e.stopPropagation(); // don't let the map's click handler dismiss the picker
        this._select(rec, e);
      });
      this._markers.set(t.mmsi, rec);
      marker.addTo(this._map);
    }
    rec.t = t; // latest data for the picker
    const dir = num(t.cog) ?? num(t.heading);
    const hasDir = dir != null;
    const danger = !!t.danger;
    if (rec.hasDir !== hasDir || rec.danger !== danger) {
      rec.el.innerHTML = glyphFor(hasDir, danger);
      rec.el.style.color = danger ? "var(--ais-danger, #e23b2e)" : "var(--ais-fill, #0a7d55)";
      rec.hasDir = hasDir;
      rec.danger = danger;
    }
    if (t.name) rec.el.title = t.name;
    rec.marker.setLngLat([t.lon, t.lat]).setRotation(hasDir ? dir : 0);
  }

  // Tap → info picker for this target.
  _select(rec, e) {
    if (!this._onSelect || !rec.t) return;
    const t = rec.t;
    const rows = [["MMSI", String(t.mmsi)]];
    if (t.callSign) rows.push(["Call sign", t.callSign]);
    if (t.typeName) rows.push(["Type", t.typeName]);
    else if (t.shipType) rows.push(["Type", String(t.shipType)]);
    if (t.status) rows.push(["Status", t.status]);
    if (t.destination) rows.push(["Destination", t.destination]);
    const u = (this._units && this._units()) || null;
    if (t.length && t.beam) rows.push(["Size", `${t.length} × ${t.beam} m`]);
    if (num(t.draught) != null) rows.push(["Draught", format("depth", t.draught, u)]);
    if (typeof t.lat === "number") rows.push(["Position", fmtLatLon(t.lat, t.lon)]);
    if (num(t.cog) != null) rows.push(["COG", Math.round(t.cog) + "°T"]);
    if (num(t.sog) != null) rows.push(["SOG", format("speed", t.sog, u)]);
    if (num(t.heading) != null) rows.push(["Heading", Math.round(t.heading) + "°T"]);
    if (num(t.cpaNm) != null) rows.push(["CPA", format("distance", t.cpaNm, u)]);
    if (num(t.tcpaMin) != null) rows.push(["TCPA", t.tcpaMin < 0 ? "passed" : t.tcpaMin.toFixed(1) + " min"]);
    this._onSelect({
      title: (t.danger ? "⚠ " : "") + (t.name || `MMSI ${t.mmsi}`),
      subtitle: t.danger ? "COLLISION RISK" : "AIS" + (t.class ? " " + t.class : ""),
      rows,
      x: e.clientX,
      y: e.clientY,
    });
  }

  async _poll() {
    this._polling = true;
    while (this._polling) {
      try {
        const r = await fetch(this._assets + "api/ais", { cache: "no-store" });
        if (r.ok) this._apply((await r.json()).targets || []);
      } catch {
        // ignore; retry
      }
      await new Promise((res) => setTimeout(res, 2000));
    }
  }

  destroy() {
    if (this._es) this._es.close();
    this._polling = false;
    for (const rec of this._markers.values()) rec.marker.remove();
    this._markers.clear();
  }
}

function num(v) {
  return typeof v === "number" && isFinite(v) ? v : null;
}
