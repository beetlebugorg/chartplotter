// util.mjs — pure, state-free helpers shared across the web app: HTML escaping,
// localStorage JSON, Web-Mercator scale maths, date/size/scale/position
// formatting, share-link parsing, clipboard, and small DOM niceties. No app or
// map state — safe to import anywhere.

// Escape text for safe innerHTML insertion (panels render tile-derived strings).
export function esc(s) {
  return String(s).replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]));
}

// Parse a JSON value from localStorage, or return `fallback` on miss/parse error.
export function loadJSON(key, fallback) {
  try { return JSON.parse(localStorage.getItem(key)) || fallback; } catch { return fallback; }
}

// Web-Mercator map scale denominator at zoom z / latitude lat (OGC 0.28mm pixel).
// This is the app's ONE display scale — the PHYSICAL on-screen scale, matching what
// MapLibre actually renders (512-tile geometry: ~2× finer per CSS pixel than the
// classic 256-tile slippy convention, hence the 512 metres-per-pixel constant). It
// is the single coordinate that SCAMIN gating (chart-sources.mjs / baker), the
// overscale ×n indication, and go-to-scale all reason in — producer scales (SCAMIN,
// CSCL) are real 1:N paper scales, so they MUST be compared against this true scale,
// not a relabelled 2×-coarse one. A single physical coordinate (engine and readout
// alike) keeps SCAMIN features visible to exactly their stated scale, rather than
// vanishing at ~½ of it. The BAND zoom ranges (bands.mjs / baker ZoomRange) are raw integer zooms and are
// independent of this constant — they pin each usage band to a tile-pyramid level.
// scaleDenomPhysical (below) is the same physical scale but with a per-screen,
// calibratable pixel pitch for an exact ruler-on-glass readout; this OGC-pixel form
// is the deterministic version the baker (which has no screen to measure) shares.
const M_PER_PX_Z0 = 78271.516964020485; // metres/CSS-px at z0, equator (512-tile)
const OGC_PX_M = 0.00028; // 0.28 mm — the OGC "standardized rendering pixel"

export function scaleDenom(z, lat) {
  const mpp = M_PER_PX_Z0 * Math.cos((lat * Math.PI) / 180) / Math.pow(2, z);
  return mpp / OGC_PX_M;
}

// Inverse of scaleDenom: the (fractional) Web-Mercator zoom that renders display
// scale 1:`scale` at `lat`. Used to "go to" a scale and to clamp by scale.
export function zoomForScale(scale, lat) {
  if (!(scale > 0)) return 0;
  const z = Math.log2(M_PER_PX_Z0 * Math.cos((lat * Math.PI) / 180) / (OGC_PX_M * scale));
  return Math.max(0, Math.min(24, z));
}

// --- PHYSICAL (ruler-on-glass) scale --------------------------------------
// Same 512-tile resolution as scaleDenom above (M_PER_PX_Z0), but with a per-screen,
// calibratable pixel pitch instead of the OGC reference pixel — for an exact
// ruler-on-glass readout. (512-tile geometry verified by unprojecting two screen
// points in the running map.) scaleDenom is the deterministic OGC-pixel form the
// engine/baker share; this is the calibrated readout layered on top.
// Physical size of one CSS pixel, in mm. The CSS reference pixel is 1/96 inch ≈
// 0.2645 mm and browsers keep CSS px near that regardless of device-pixel-ratio, so
// it's a good DEFAULT; a per-screen calibration (settings) overrides it for exact
// ruler accuracy. Clamped to a sane range so a bad calibration can't break the HUD.
export const DEFAULT_PX_PITCH_MM = 0.2645;
export function clampPxPitch(mm) {
  const v = Number(mm);
  return isFinite(v) && v >= 0.05 && v <= 1 ? v : DEFAULT_PX_PITCH_MM;
}

// PHYSICAL paper-scale denominator — what a ruler laid on the screen measures.
// Uses MapLibre's real per-CSS-pixel resolution and the (calibrated) physical size
// of a CSS pixel. This is the user-facing readout / "go to scale": it differs from
// scaleDenom only by the calibrated px pitch (vs the OGC 0.28mm reference) — both are
// now the same true 512-tile scale, since the engine moved onto the physical scale.
export function scaleDenomPhysical(z, lat, pxPitchMm = DEFAULT_PX_PITCH_MM) {
  const mPerCssPx = M_PER_PX_Z0 * Math.cos((lat * Math.PI) / 180) / Math.pow(2, z);
  return mPerCssPx / (clampPxPitch(pxPitchMm) / 1000);
}
// Inverse: the (fractional) zoom that renders physical scale 1:`scale` at `lat`.
export function zoomForScalePhysical(scale, lat, pxPitchMm = DEFAULT_PX_PITCH_MM) {
  if (!(scale > 0)) return 0;
  const z = Math.log2(M_PER_PX_Z0 * Math.cos((lat * Math.PI) / 180) / ((clampPxPitch(pxPitchMm) / 1000) * scale));
  return Math.max(0, Math.min(24, z));
}

// Finest map scale we allow: don't magnify charts past 1:MIN_DETAIL_SCALE (past
// this it's just blocky overzoom). Inverse of scaleDenom — the (fractional) zoom
// whose scale at `lat` equals the floor (latitude-dependent). This is a PHYSICAL
// scale now that scaleDenom is physical: 1:2000 is the old 1:4000-nominal cap (the
// engine moved to the true 512-tile scale, ~2× finer), so the max-zoom-IN level the
// user can reach is unchanged — only the scale NUMBER for the same zoom halved.
export const MIN_DETAIL_SCALE = 2000;
export function maxZoomForScaleFloor(lat) {
  const z = Math.log2(M_PER_PX_Z0 * Math.cos((lat * Math.PI) / 180) / (OGC_PX_M * MIN_DETAIL_SCALE));
  return Math.max(1, Math.min(18, z));
}

// NOAA ENC freshness from an issue date "YYYY-MM-DD". ENCs have no hard expiry —
// this grades by edition age (they're kept current via Notices to Mariners).
export function freshness(d) {
  const t = d ? Date.parse(d + "T00:00:00Z") : NaN;
  if (!isFinite(t)) return { cls: "aging", label: "Age unknown" };
  const months = (Date.now() - t) / (1000 * 60 * 60 * 24 * 30.44);
  if (months < 6) return { cls: "current", label: "Current" };
  if (months < 12) return { cls: "aging", label: "Aging" };
  return { cls: "stale", label: "Out of date" };
}

// "2025-12-09" → "Dec 2025" (UTC, locale month). Falls back to the raw string.
export function fmtIssue(d) {
  const t = d ? Date.parse(d + "T00:00:00Z") : NaN;
  if (!isFinite(t)) return d || "unknown date";
  return new Date(t).toLocaleDateString(undefined, { year: "numeric", month: "short", timeZone: "UTC" });
}

// Bytes → a compact "12 MB" / "1.4 MB" string.
export function fmtMB(bytes) {
  const mb = (bytes || 0) / (1024 * 1024);
  return (mb < 10 ? mb.toFixed(1) : Math.round(mb)) + " MB";
}

// Bytes → a compact "12 MB" / "1.4 KB" string, auto-scaling the unit (B/KB/MB/GB).
export function fmtBytes(n) {
  if (!n) return "0 B";
  const u = ["B", "KB", "MB", "GB"]; let i = 0;
  while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
  return `${n.toFixed(n < 10 && i > 0 ? 1 : 0)} ${u[i]}`;
}

// A scale denominator rounded to 3 significant figures and thousands-grouped.
export function fmtScale(d) {
  if (!isFinite(d) || d <= 0) return "—";
  const mag = Math.pow(10, Math.max(0, Math.floor(Math.log10(d)) - 2));
  return (Math.round(d / mag) * mag).toLocaleString();
}

// Map-centre position as fixed-width degrees-decimal-minutes, e.g.
// "39°27.6′N 104°39.6′W" — zero-padded so the status pill never reflows as you
// pan (with tabular figures). Longitude is normalised to ±180.
export function fmtLatLon(lat, lng) {
  const dm = (v, degDigits) => {
    let a = Math.abs(v);
    let d = Math.floor(a);
    let m = (a - d) * 60;
    if (m >= 59.95) { m = 0; d += 1; } // round-up carry: 59.95′ rolls to next degree
    return String(d).padStart(degDigits, "0") + "°" + m.toFixed(1).padStart(4, "0") + "′";
  };
  const x = ((((lng + 180) % 360) + 360) % 360) - 180; // wrap to [-180, 180)
  return dm(lat, 2) + (lat >= 0 ? "N" : "S") + " " + dm(x, 3) + (x >= 0 ? "E" : "W");
}

// True when the page was opened as a snapshot share link (<origin>/#share or
// ?share) — boot() then reconstructs the publisher's scene from /api/share.
export function isShareUrl() {
  const h = (location.hash || "").replace(/^#/, "");
  return h === "share" || new URLSearchParams(location.search).has("share");
}

// A lightweight share link carries only the camera in the URL hash
// (#v=lon,lat,zoom[,bearing,pitch]). Returns {center:[lon,lat],zoom,bearing,pitch}
// or null if the hash isn't a view link or is malformed.
export function parseViewHash() {
  const h = (location.hash || "").replace(/^#/, "");
  if (!h.startsWith("v=")) return null;
  const p = h.slice(2).split(",").map(Number);
  if (p.length < 3 || p.some((n) => !isFinite(n))) return null;
  const [lon, lat, zoom, bearing = 0, pitch = 0] = p;
  return { center: [lon, lat], zoom, bearing, pitch };
}

// Copy `text` to the clipboard, returning whether it worked. Prefers the async
// Clipboard API (needs https/localhost); falls back to a hidden-textarea
// execCommand so it still works on a plain-http LAN origin.
export async function copyText(text) {
  try {
    if (navigator.clipboard && window.isSecureContext) { await navigator.clipboard.writeText(text); return true; }
  } catch (e) { /* fall through to the textarea fallback */ }
  try {
    const ta = document.createElement("textarea");
    ta.value = text;
    ta.style.cssText = "position:fixed;top:0;left:0;opacity:0;pointer-events:none";
    document.body.appendChild(ta);
    ta.focus(); ta.select();
    const ok = document.execCommand("copy");
    ta.remove();
    return ok;
  } catch (e) { return false; }
}

// Briefly show `msg` on a button, then restore its original label.
export function flashBtn(btn, msg) {
  if (!btn) return;
  const prev = btn.textContent;
  btn.textContent = msg;
  setTimeout(() => { btn.textContent = prev; }, 1400);
}
