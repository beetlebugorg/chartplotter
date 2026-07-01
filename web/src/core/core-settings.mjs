// core-settings.mjs — the app's own display settings, expressed as <settings-dialog>
// contributions. Tabs: General (app chrome), Display (the S-52 display category +
// viewing-group toggles, grouped), Text, Units, Depths, Advanced. Everything here
// is a plain contribution (same path a plugin would take) — there is no privileged
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
import { VIEWING_GROUP_SECTIONS, VG_BY_GROUP_ID } from "./viewing-groups.mjs";

// One shared read path for every core contribution. App-level flags live on the
// shell; everything else is a mariner setting (pre-merged with DEFAULT_MARINER,
// so the stored value is always present — `def` is only a belt-and-suspenders
// fallback).
function coreGet(app, key, def) {
  if (key === "basemap") return app._basemap;
  if (key === "scheme") return app._scheme;
  if (key === "showCellBounds") return app._showCellBounds;
  if (key === "showChartRadar") return app._showChartRadar;
  // Synthetic S-52 display-category level (§10.2). Base → Standard → All are
  // CUMULATIVE, so the three mariner booleans collapse to one of three levels.
  if (key === "detailLevel") return app._mariner.displayOther ? "other" : (app._mariner.displayStandard ? "standard" : "base");
  // Synthetic viewing-group toggle (S-52 §14.5): "vg:<groupId>" reads ON (shown)
  // iff NONE of the group's vg ids are in the mariner's deny-list. See viewing-groups.mjs.
  if (key.startsWith("vg:")) {
    const vgs = VG_BY_GROUP_ID[key.slice(3)] || [];
    const off = app._mariner.viewingGroupsOff || [];
    return !vgs.some((v) => off.includes(v));
  }
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
  // Cumulative display category (S-52 §10.2): each level implies the ones below
  // it. Base is permanent (displayBase always true); Standard adds the standard
  // set; All adds the "other" category on top. This can never produce the
  // non-conformant Standard-off / Other-on state the old multi-select allowed.
  if (key === "detailLevel") return app.applyMariner({ displayBase: true, displayStandard: val !== "base", displayOther: val === "other" });
  // Viewing-group toggle (S-52 §14.5): turning a group OFF adds its vg ids to the
  // deny-list; ON removes them. Stored sorted for a stable persisted value.
  if (key.startsWith("vg:")) {
    const vgs = VG_BY_GROUP_ID[key.slice(3)] || [];
    const off = new Set(app._mariner.viewingGroupsOff || []);
    for (const v of vgs) val ? off.delete(v) : off.add(v);
    return app.applyMariner({ viewingGroupsOff: [...off].sort((a, b) => a - b) });
  }
  return app.applyMariner({ [key]: val });
}

// Build the array of core contributions for `app` (the <chart-plotter> shell).
export function coreSettingsContributions(app) {
  const get = (k, d) => coreGet(app, k, d);
  const set = (k, v) => coreSet(app, k, v);

  // GENERAL — app chrome only: the basemap drawn under the chart and the
  // off-screen chart pointers. Everything S-52 (the display category + the
  // viewing-group toggles) now lives on the dedicated DISPLAY tab below; screen
  // calibration has its own "Calibration" tab (plugins/calibration.mjs).
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
        key: "showChartRadar", type: "toggle", label: "Off-screen chart pointers",
        desc: "Edge arrows to installed charts you can't currently see — tap one to fly there",
      },
    ],
  };

  // DISPLAY — the S-52 portrayal controls, split into five sub-groups that mirror
  // the spec's mental model: pick a detail level (display category, §10.2), then
  // add or remove the individual viewing groups within it. Each contribution
  // renders as one sub-heading on the shared Display tab.

  // Detail level (display category, S-52 §10.2 / §10.3.4). CUMULATIVE: Base is the
  // permanent safe-navigation minimum, Standard adds the normal chart content, All
  // adds every other feature. Backed by displayBase/Standard/Other via the
  // synthetic "detailLevel" key (coreGet/coreSet), so the control can never reach
  // the non-conformant Standard-off / Other-on state the old multi-select allowed.
  const displayDetail = {
    id: "core-display-detail",
    tab: { id: "display", label: "Display" },
    order: 0.6,
    group: "Detail level",
    get, set,
    items: [
      {
        key: "detailLevel", type: "segmented", label: "Detail level",
        desc: "Display Base is always shown — Standard adds normal chart content, Other adds every remaining feature",
        options: [["base", "Base"], ["standard", "Standard"], ["other", "Other"]],
      },
    ],
  };

  // Water, depth areas & soundings.
  const displayWater = {
    id: "core-display-water",
    tab: "display",
    order: 0.7,
    group: "Water & soundings",
    get, set,
    items: [
      { key: "fourShadeWater", type: "toggle", label: "Four-shade water", desc: "Use four depth shades instead of two", default: true },
      { key: "shallowPattern", type: "toggle", label: "Shallow pattern", desc: "Diagonal fill in shallow water" },
      { key: "showSoundings", type: "toggle", label: "Spot soundings", desc: "Individual depth soundings", default: true },
      { key: "showNoData", type: "toggle", label: "No-data hatch", desc: "Hatch areas that have no chart data", default: true },
    ],
  };

  // Point symbols & line styling.
  const displaySymbols = {
    id: "core-display-symbols",
    tab: "display",
    order: 0.8,
    group: "Symbols & lines",
    get, set,
    items: [
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
      { key: "showFullSectorLines", type: "toggle", label: "Full sector lines", desc: "Draw light sectors to full range, not short stubs" },
    ],
  };

  // Dangers & data quality.
  const displayDangers = {
    id: "core-display-dangers",
    tab: "display",
    order: 0.9,
    group: "Dangers & quality",
    get, set,
    items: [
      { key: "showIsolatedDangersShallow", type: "toggle", label: "Isolated dangers (shallow)", desc: "Only flag isolated dangers in shallow water" },
      { key: "dataQuality", type: "toggle", label: "Data quality", desc: "Survey zones-of-confidence overlay" },
    ],
  };

  // Chart boundaries & informational callouts.
  const displayBounds = {
    id: "core-display-bounds",
    tab: "display",
    order: 0.95,
    group: "Boundaries & callouts",
    get, set,
    items: [
      { key: "showScaleBoundaries", type: "toggle", label: "Scale boundaries", desc: "Outline where more detailed charts exist" },
      { key: "showMetaBounds", type: "toggle", label: "Metadata boundaries", desc: "Chart coverage & region indicator lines" },
      { key: "showInformCallouts", type: "toggle", label: "Information callouts", desc: "“Additional information available” (i) markers on features that carry notes" },
      { key: "highlightDateDependent", type: "toggle", label: "Highlight date-dependent", desc: "Mark features that carry date conditions with the “d” symbol" },
    ],
  };

  // VIEWING GROUPS — S-52 §14.5 fine-grained content selection. One contribution
  // per taxonomy section (→ one sub-heading); each item is a toggle keyed
  // "vg:<groupId>" that adds/removes the group's raw vg ids from the deny-list
  // (mariner.viewingGroupsOff; see coreGet/coreSet + viewing-groups.mjs). All
  // default ON. Only Standard/Other groups are listed — Display Base is the
  // mandatory minimum and never selectable (S-52 §10.2). The first section declares
  // the tab; the rest slot into it. Ordered (0.96+) so the tab sits after Display.
  const viewingGroups = VIEWING_GROUP_SECTIONS.map((s, i) => ({
    id: `core-vg-${s.id}`,
    tab: i === 0 ? { id: "viewing-groups", label: "Viewing groups" } : "viewing-groups",
    order: 0.96 + i * 0.001,
    group: s.label,
    get, set,
    items: s.groups.map((g) => ({ key: `vg:${g.id}`, type: "toggle", label: g.label, desc: g.desc, default: true })),
  }));

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
    items: () => [
      {
        key: "showCellBounds", type: "toggle", label: "Cell boundaries",
        desc: "Outline installed charts when zoomed out — tap one to jump to it",
      },
      // Date-dependency (S-52 §10.4.1.1). "Hide out-of-date features" is the
      // mandatory current-date filter (default on); turning it off shows
      // seasonal/expired features regardless of their validity dates. The viewing
      // date evaluates that filter (and the "Highlight date-dependent" markers,
      // toggled under Display) against a chosen date for passage planning.
      {
        key: "dateDependent", type: "toggle", label: "Hide out-of-date features",
        desc: "Hide seasonal or expired features outside their validity dates", default: true,
      },
      {
        key: "dateView", type: "date", label: "Viewing date",
        desc: "Evaluate date-dependent features against this date for passage planning (blank = today)",
      },
    ],
  };

  // (Screen calibration lives in its own "Calibration" tab — plugins/calibration.mjs,
  // the S-52 CHKSYM 5 mm ruler-measure method. The old screen-diagonal duplicate that
  // used to sit here under General was removed.)

  return [general, displayDetail, displayWater, displaySymbols, displayDangers, displayBounds, ...viewingGroups, text, units, depths, advanced];
}
