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
export function scaleDenom(z, lat) {
  const mpp = 156543.03392804097 * Math.cos((lat * Math.PI) / 180) / Math.pow(2, z);
  return mpp / 0.00028;
}

// Finest map scale we allow: don't magnify charts past 1:MIN_DETAIL_SCALE (past
// this it's just blocky overzoom). Inverse of scaleDenom — the (fractional) zoom
// whose scale at `lat` equals the floor (latitude-dependent).
export const MIN_DETAIL_SCALE = 4000;
export function maxZoomForScaleFloor(lat) {
  const z = Math.log2(156543.03392804097 * Math.cos((lat * Math.PI) / 180) / (0.00028 * MIN_DETAIL_SCALE));
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

// True when the page was opened as a legacy snapshot link (<origin>/#share or
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
  } catch (e) { /* fall through to the legacy path */ }
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
