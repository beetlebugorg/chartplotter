// core-settings.mjs — the app's own display settings, expressed as <settings-dialog>
// contributions. These reproduce the panes the shell used to render inline in
// renderSettings(): General, Text, Units, Depths, Advanced. Everything here is a
// plain contribution (same path a plugin would take) — there is no privileged
// built-in settings code in the dialog.
//
//   const reg = new SettingsRegistry();
//   for (const c of coreSettingsContributions(app)) reg.register(c);
//   dlg.configure({ registry: reg });
//
// Persistence is UNCHANGED: every set routes through the shell's existing
// applyScheme/applyBasemap/applyMariner/_setCellBoundsVisible/_setChartRadarVisible
// methods, which already persist (localStorage + /api/settings). We do NOT use
// SettingsStore here — that's for future plugins.

import { UNIT_CATEGORIES, M_TO_FT } from "../lib/units.mjs";
import { DEFAULT_PX_PITCH_MM } from "../lib/util.mjs";

// One shared read path for every core contribution. App-level flags live on the
// shell; everything else is a mariner setting (pre-merged with DEFAULT_MARINER,
// so the stored value is always present — `def` is only a belt-and-suspenders
// fallback).
function coreGet(app, key, def) {
  if (key === "basemap") return app._basemap;
  if (key === "scheme") return app._scheme;
  if (key === "showCellBounds") return app._showCellBounds;
  if (key === "showChartRadar") return app._showChartRadar;
  if (key === "pxPitch") return app._pxPitch;
  const v = app._mariner[key];
  return v === undefined ? def : v;
}

// One shared write path. Routes each key to the existing setter that applies +
// persists it (see Persistence note above).
function coreSet(app, key, val) {
  if (key === "basemap") return app.applyBasemap(val);
  if (key === "scheme") return app.applyScheme(val);
  if (key === "showCellBounds") return app._setCellBoundsVisible(val);
  if (key === "showChartRadar") return app._setChartRadarVisible(val);
  if (key === "pxPitch") return app.setPxPitch(val);
  return app.applyMariner({ [key]: val });
}

// Build the array of core contributions for `app` (the <chart-plotter> shell).
export function coreSettingsContributions(app) {
  const get = (k, d) => coreGet(app, k, d);
  const set = (k, v) => coreSet(app, k, v);

  // GENERAL — basemap, detail level, area/point style, and the display toggles.
  const general = {
    id: "core-general",
    tab: { id: "general", label: "General" },
    order: 0,
    get, set,
    // A function: the basemap options depend on whether a vector basemap is
    // configured (app._osmVecUrl is resolved during boot).
    items: () => [
      {
        key: "basemap", type: "segmented", label: "Basemap", desc: "Land drawn under the chart",
        options: [["none", "Disabled"], ["coastline", "Offline"], ["osm", "OSM"], ...(app._osmVecUrl ? [["osmvec", "Vector"]] : [])],
      },
      {
        key: "detail", type: "multi", label: "Detail level",
        desc: "How much chart detail to show — Base is always on",
        locked: [["Base"]],
        options: [["displayStandard", "Standard"], ["displayOther", "Other"]],
      },
      {
        key: "boundaryStyle", type: "segmented", label: "Area boundaries", desc: "Line style for area edges",
        default: "symbolized",
        options: [["plain", "Plain"], ["symbolized", "Symbolized"]],
      },
      {
        key: "simplifiedPoints", type: "segmented", label: "Point symbols", desc: "Buoy & beacon symbol style",
        options: [["paper", "Paper-chart"], ["simplified", "Simplified"]],
        transform: { toView: (b) => (b ? "simplified" : "paper"), fromView: (s) => s === "simplified" },
      },
      { key: "fourShadeWater", type: "toggle", label: "Four-shade water", desc: "Use four depth shades instead of two", default: true },
      { key: "showNoData", type: "toggle", label: "No-data hatch", desc: "Hatch areas that have no chart data", default: true },
      { key: "shallowPattern", type: "toggle", label: "Shallow pattern", desc: "Diagonal fill in shallow water" },
      { key: "showSoundings", type: "toggle", label: "Spot soundings", desc: "Individual depth soundings", default: true },
      { key: "showFullSectorLines", type: "toggle", label: "Full sector lines", desc: "Draw light sectors to full range, not short stubs" },
      { key: "showIsolatedDangersShallow", type: "toggle", label: "Isolated dangers (shallow)", desc: "Only flag isolated dangers in shallow water" },
      { key: "dataQuality", type: "toggle", label: "Data quality", desc: "Survey zones-of-confidence overlay" },
      { key: "showInformCallouts", type: "toggle", label: "Information callouts", desc: "“Additional information available” (i) markers on features that carry notes" },
      { key: "showMetaBounds", type: "toggle", label: "Metadata boundaries", desc: "Chart coverage & region indicator lines" },
      { key: "showScaleBoundaries", type: "toggle", label: "Scale boundaries", desc: "Outline where more detailed charts exist", default: true },
      {
        key: "showChartRadar", type: "toggle", label: "Off-screen chart pointers",
        desc: "Edge arrows to installed charts you can't currently see — tap one to fly there",
      },
    ],
  };

  // TEXT — the S-52 text-group toggles.
  const text = {
    id: "core-text",
    tab: { id: "text", label: "Text" },
    order: 1,
    get, set,
    // "Important text" (bridge/cable clearances, route bearings) has no toggle —
    // it is safety-critical and always shown (see textGroupFilter).
    items: [
      { key: "showLightDescriptions", type: "toggle", label: "Light descriptions", desc: "Light characteristics, e.g. Fl(2)R 10s", default: true },
      { key: "textNames", type: "toggle", label: "Names", desc: "Buoy, beacon & place names, berth numbers", default: true },
      { key: "textOther", type: "toggle", label: "Other text", desc: "Notes, seabed, magnetic variation, heights", default: true },
      { key: "showContourLabels", type: "toggle", label: "Contour labels", desc: "Depth values along contour lines" },
    ],
  };

  // UNITS — one segmented picker per unit category (depth + the five others).
  const units = {
    id: "core-units",
    tab: { id: "units", label: "Units" },
    order: 2,
    get, set,
    items: UNIT_CATEGORIES.map((c) => ({ key: c.key, type: "segmented", label: c.label, options: c.opts, default: c.def })),
  };

  // DEPTHS — the four depth contours, shown/edited in the current depth unit
  // (metres under the hood). A function so the unit/label/step track depthUnit.
  const depths = {
    id: "core-depths",
    tab: { id: "depths", label: "Depths" },
    order: 3,
    get, set,
    items: () => {
      const ft = app._mariner.depthUnit === "ft";
      const row = (key, label) => ({
        key, type: "number", label,
        unit: ft ? "ft" : "m",
        step: ft ? "1" : "0.1",
        transform: { toView: (m) => (ft ? Math.round(m * M_TO_FT) : m), fromView: (v) => (ft ? v / M_TO_FT : v) },
      });
      return [
        row("shallowContour", "Shallow contour"),
        row("safetyContour", "Safety contour"),
        row("deepContour", "Deep contour"),
        row("safetyDepth", "Safety depth"),
      ];
    },
  };

  // ADVANCED — the cell-boundary toggle. The developer tools (rebuild / share /
  // inspector / coverage / bands / diagnostics) are rendered by the SHELL in its
  // own shadow (#dev-region) because _renderDevPanel / _renderInspect reach into
  // the shell's shadow by id; keeping them there avoids a risky refactor (option
  // B). The shell reveals #dev-region only when this tab is active. In the widget
  // viewer there are no dev tools, so the tab is just the toggle.
  const advanced = {
    id: "core-advanced",
    tab: { id: "advanced", label: "Advanced" },
    order: 4,
    get, set,
    items: [
      {
        key: "showCellBounds", type: "toggle", label: "Cell boundaries",
        desc: "Outline installed charts when zoomed out — tap one to jump to it",
      },
      // Date-dependency (S-52 §10.4.1.1 / §10.6.1.1). "Hide out-of-date features"
      // is the mandatory current-date filter (default on); turning it off shows
      // seasonal/expired features regardless of their validity dates. "Highlight"
      // adds the CHDATD01 "d" marker to features that carry date conditions.
      {
        key: "dateDependent", type: "toggle", label: "Hide out-of-date features",
        desc: "Hide seasonal or expired features outside their validity dates", default: true,
      },
      {
        key: "highlightDateDependent", type: "toggle", label: "Highlight date-dependent",
        desc: "Mark features that carry date conditions with the “d” symbol",
      },
      {
        key: "dateView", type: "date", label: "Viewing date",
        desc: "Evaluate date-dependent features against this date for passage planning (blank = today)",
      },
    ],
  };

  // SCREEN CALIBRATION — make the on-screen scale (the 1:N readout, overscale, and
  // "go to scale") match a real ruler / other ENCs. We can't know the monitor's
  // physical pixel size, so the user enters their display's diagonal and we derive
  // the CSS-pixel pitch from window.screen. Stored as pxPitch (mm per CSS pixel);
  // the engine scale (bands/SCAMIN/overscale-vs-CSCL) is unaffected.
  const cssDiagPx = () => {
    const s = window.screen || {};
    return Math.hypot(s.width || 1280, s.height || 800) || 1509; // CSS-pixel screen diagonal
  };
  const pitchToInches = (mm) => (cssDiagPx() * mm) / 25.4;        // pitch → implied diagonal (in)
  const inchesToPitch = (inch) => (inch * 25.4) / cssDiagPx();    // diagonal → pitch (mm/CSS px)
  const calibration = {
    id: "core-calibration",
    tab: { id: "general", label: "General" },
    order: 0.5,
    group: "Screen calibration",
    get, set,
    render(host) {
      const pitch = get("pxPitch") || DEFAULT_PX_PITCH_MM;
      host.innerHTML = `
        <style>
          .cal { padding:2px 0 4px; }
          .cal-desc { font-size:12.5px; color:var(--ui-text-dim); line-height:1.45; margin-bottom:11px; }
          .cal-field { display:flex; align-items:center; gap:8px; font-size:13px; color:var(--ui-text); flex-wrap:wrap; }
          .cal-field input { width:74px; text-align:right; border:1px solid var(--ui-border-strong); border-radius:6px;
            padding:5px 7px; font:inherit; font-size:16px; background:var(--ui-surface); color:var(--ui-text); }
          .cal-readout { font-size:12px; color:var(--ui-text-faint); margin-top:9px; display:flex; align-items:center; gap:10px; }
          .cal-readout b { color:var(--ui-text-dim); font-variant-numeric:tabular-nums; }
          .cal-reset { border:1px solid var(--ui-border-strong); background:var(--ui-surface); color:var(--ui-text);
            border-radius:6px; padding:4px 9px; font:inherit; font-size:12px; cursor:pointer; margin-left:auto; }
        </style>
        <div class="cal">
          <div class="cal-desc">Enter your display's diagonal so the scale readout matches a real ruler (and other ENCs). Affects only the on-screen scale numbers — not the charts.</div>
          <label class="cal-field">Screen diagonal <input class="cal-diag" type="number" inputmode="decimal" step="0.1" min="3" max="120" value="${pitchToInches(pitch).toFixed(1)}"> inches</label>
          <div class="cal-readout">Pixel pitch: <b class="cal-pitch">${pitch.toFixed(4)}</b> mm<button class="cal-reset" type="button">Reset</button></div>
        </div>`;
      const diag = host.querySelector(".cal-diag");
      const out = host.querySelector(".cal-pitch");
      const apply = () => {
        const inch = +diag.value;
        if (!(inch >= 3 && inch <= 120)) return;
        const mm = inchesToPitch(inch);
        set("pxPitch", mm);
        out.textContent = mm.toFixed(4);
      };
      diag.addEventListener("change", apply);
      diag.addEventListener("input", apply);
      host.querySelector(".cal-reset").addEventListener("click", () => {
        set("pxPitch", undefined);
        diag.value = pitchToInches(DEFAULT_PX_PITCH_MM).toFixed(1);
        out.textContent = DEFAULT_PX_PITCH_MM.toFixed(4);
      });
    },
  };

  return [general, calibration, text, units, depths, advanced];
}
