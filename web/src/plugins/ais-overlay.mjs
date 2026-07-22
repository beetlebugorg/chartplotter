// ais-overlay — other vessels from the live AIS feed, a builtin plugin (core.ais).
// Like own-ship it is a pure ctx consumer: targets come from ctx.ais (the coalesced
// server feed), each drawn as a heading-rotated OpenBridge AIS glyph via ctx.markers.
// Colour + halo come from --ais-* CSS vars (set per scheme on the app host), so
// targets honour day/dusk/night. The server prunes stale targets, so a target
// dropping out of the feed removes its marker.

import {
  AIS_TARGET_ICON, AIS_TARGET_NODIR_ICON,
  AIS_TARGET_DANGER_ICON, AIS_TARGET_DANGER_NODIR_ICON,
} from "../lib/openbridge-icons.mjs";
import { fmtLatLon } from "./target-info.mjs";

// Pick the AIS glyph for a target's directionality + collision danger.
function glyphFor(hasDir, danger) {
  if (danger) return hasDir ? AIS_TARGET_DANGER_ICON : AIS_TARGET_DANGER_NODIR_ICON;
  return hasDir ? AIS_TARGET_ICON : AIS_TARGET_NODIR_ICON;
}

const GLYPH_STYLE =
  "line-height:0;pointer-events:auto;cursor:pointer;will-change:transform;" +
  "color:var(--ais-fill,#0a7d55);" +
  "filter:drop-shadow(0 0 1px var(--ais-halo,#fff)) drop-shadow(0 0 1px var(--ais-halo,#fff));";

export default class AISOverlay {
  constructor(ctx) {
    this.ctx = ctx;
    this._markers = new Map(); // mmsi -> {marker, hasDir, danger, t}
    this._visible = true;
  }

  start() {
    this._off = this.ctx.ais.subscribe((targets) => this._apply(targets || []));
    // Show/hide all AIS glyphs from the Layers control — the feed keeps running
    // (CPA/collision tracking is unaffected), only the markers hide.
    this.ctx.overlays.register({
      id: "targets",
      title: "AIS targets",
      group: "AIS",
      onVisible: (v) => this._setVisible(v),
    });
  }

  _setVisible(v) {
    this._visible = v;
    for (const rec of this._markers.values()) rec.marker.element.style.display = v ? "" : "none";
  }

  _apply(targets) {
    const seen = new Set();
    for (const t of targets) {
      // Skip targets we can't place: no position yet (static-only msg 24/5 before a
      // position report), which would otherwise render at 0,0.
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
      const marker = this.ctx.markers.add(`ais-${t.mmsi}`, { rotationAlignment: "map", anchor: "center" });
      marker.setStyle(GLYPH_STYLE);
      if (!this._visible) marker.element.style.display = "none"; // honor a hidden overlay
      marker.element.title = t.name || String(t.mmsi);
      marker.setLngLat([t.lon, t.lat]);
      rec = { marker, hasDir: undefined, danger: undefined, t };
      marker.onClick((e) => { e.stopPropagation(); this._select(rec, e); });
      this._markers.set(t.mmsi, rec);
    }
    rec.t = t; // latest data for the picker
    const dir = num(t.cog) ?? num(t.heading);
    const hasDir = dir != null;
    const danger = !!t.danger;
    if (rec.hasDir !== hasDir || rec.danger !== danger) {
      rec.marker.setHTML(glyphFor(hasDir, danger));
      rec.marker.element.style.color = danger ? "var(--ais-danger, #e23b2e)" : "var(--ais-fill, #0a7d55)";
      rec.hasDir = hasDir;
      rec.danger = danger;
    }
    if (t.name) rec.marker.element.title = t.name;
    rec.marker.setLngLat([t.lon, t.lat]).setRotation(hasDir ? dir : 0);
  }

  // Tap → info picker for this target.
  _select(rec, e) {
    if (!rec.t) return;
    const t = rec.t;
    const rows = [["MMSI", String(t.mmsi)]];
    if (t.callSign) rows.push(["Call sign", t.callSign]);
    if (t.typeName) rows.push(["Type", t.typeName]);
    else if (t.shipType) rows.push(["Type", String(t.shipType)]);
    if (t.status) rows.push(["Status", t.status]);
    if (t.destination) rows.push(["Destination", t.destination]);
    if (t.length && t.beam) rows.push(["Size", `${t.length} × ${t.beam} m`]);
    if (num(t.draught) != null) rows.push(["Draught", this.ctx.units.format("depth", t.draught)]);
    if (typeof t.lat === "number") rows.push(["Position", fmtLatLon(t.lat, t.lon)]);
    if (num(t.cog) != null) rows.push(["COG", Math.round(t.cog) + "°T"]);
    if (num(t.sog) != null) rows.push(["SOG", this.ctx.units.format("speed", t.sog)]);
    if (num(t.heading) != null) rows.push(["Heading", Math.round(t.heading) + "°T"]);
    if (num(t.cpaNm) != null) rows.push(["CPA", this.ctx.units.format("distance", t.cpaNm)]);
    if (num(t.tcpaMin) != null) rows.push(["TCPA", t.tcpaMin < 0 ? "passed" : t.tcpaMin.toFixed(1) + " min"]);
    this.ctx.callout.show({
      title: (t.danger ? "⚠ " : "") + (t.name || `MMSI ${t.mmsi}`),
      subtitle: t.danger ? "COLLISION RISK" : "AIS" + (t.class ? " " + t.class : ""),
      rows,
      x: e.clientX,
      y: e.clientY,
    });
  }

  destroy() {
    if (this._off) this._off();
    for (const rec of this._markers.values()) rec.marker.remove();
    this._markers.clear();
  }
}

function num(v) {
  return typeof v === "number" && isFinite(v) ? v : null;
}
