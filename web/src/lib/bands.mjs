// bands.mjs — NOAA navigational-purpose "usage bands" (coarse→fine) shared across
// the web app: the band list, display colours/labels, the native Web-Mercator zoom
// range per band (mirrors CHART_BANDS in chart-canvas.mjs), and the scale↔band and
// zoom↔band mappings. One source of truth so the shell, the renderer overlays, and
// the feature controllers all agree.

export const BANDS = ["overview", "general", "coastal", "approach", "harbor", "berthing"];
export const BAND_LABEL = { overview: "Overview", general: "General", coastal: "Coastal", approach: "Approach", harbor: "Harbor", berthing: "Berthing" };
// Colour for the picker so overlapping cells are distinguishable and each band's
// toggle is identifiable.
export const BAND_COLOR = { overview: "#7e57c2", general: "#5c6bc0", coastal: "#26a69a", approach: "#9ccc65", harbor: "#ffa726", berthing: "#ef5350" };
// Native MIN display zoom per band. Below it a cell's chart detail isn't baked, so
// the shell draws its coverage outline instead. General is overzoomed out to z0 (it
// renders where no overview covers — see generalOverzoomMin in the baker).
export const BAND_MINZOOM = { overview: 0, general: 0, coastal: 9, approach: 11, harbor: 13, berthing: 16 };
// Native MAX display zoom per band. Drives the overscale cap: zooming past a band's
// native max + OVERSCALE_MARGIN over open water just enlarges blank water, so
// _updateZoomCap clamps to the finest band that actually covers the view.
export const BAND_MAXZOOM = { overview: 7, general: 9, coastal: 11, approach: 13, harbor: 16, berthing: 18 };
export const OVERSCALE_MARGIN = 2; // zoom-in levels allowed past the finest covering band
// Usage bands in coarse→fine order, for the dev band-filter rows.
export const DEV_BANDS = BANDS;

// A cell's compilation scale (1:N denominator) → its NOAA band. Mirrors
// bandForScale in the baker (bake.zig).
export function bandForScale(s) {
  const n = s || 0;
  if (n <= 8000) return "berthing";
  if (n <= 32000) return "harbor";
  if (n <= 130000) return "approach";
  if (n <= 500000) return "coastal";
  if (n <= 2300000) return "general";
  return "overview";
}

// The finest band whose source paints at zoom z (Band.zoomRange mins in bake.zig:
// overview 0, general 7, coastal 9, approach 11, harbor 13, berthing 16 — rounded
// to the client's display thresholds here).
export function bandForZoom(z) {
  if (z >= 16) return "berthing";
  if (z >= 14) return "harbor";
  if (z >= 12) return "approach";
  if (z >= 10) return "coastal";
  if (z >= 8) return "general";
  return "overview";
}
