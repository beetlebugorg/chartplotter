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

// One shared read path for every core contribution. App-level flags live on the
// shell; everything else is a mariner setting (pre-merged with DEFAULT_MARINER,
// so the stored value is always present — `def` is only a belt-and-suspenders
// fallback).
function coreGet(app, key, def) {
  if (key === "basemap") return app._basemap;
  if (key === "scheme") return app._scheme;
  if (key === "showCellBounds") return app._showCellBounds;
  if (key === "showChartRadar") return app._showChartRadar;
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
    items: [
      { key: "showLightDescriptions", type: "toggle", label: "Light descriptions", desc: "Light characteristics, e.g. Fl(2)R 10s", default: true },
      { key: "textImportant", type: "toggle", label: "Important text", desc: "Bridge/cable clearances & route bearings", default: true },
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
  // B). The shell reveals #dev-region only when this tab is active. In prod there
  // are no dev tools, so the tab is just the toggle.
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
    ],
  };

  return [general, text, units, depths, advanced];
}
