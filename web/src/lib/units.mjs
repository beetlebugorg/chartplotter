// units.mjs — configurable display units for the chartplotter shell + components.
//
// Depth keeps its own metric/imperial toggle (`depthUnit`, handled inline by the
// renderer's MapLibre expressions). The five categories here — distance, height,
// speed, wind speed, temperature — each store a unit key in the mariner settings
// and are formatted through this one module so the scalebar, pick report, and any
// future readouts all agree. Each category has a CANONICAL source unit (the unit
// the underlying value is already in) that everything converts FROM.

export const M_TO_FT = 3.280839895;
const NM_TO_KM = 1.852;
const NM_TO_MI = 1.150779448;
const KN_TO_KMH = 1.852;
const KN_TO_MPH = 1.150779448;
const KN_TO_MS = 0.514444;

// Per-category config: setting key, default, option list (canonical unit first),
// and the human label for the settings UI. Canonical source units: distance=NM,
// height=m, speed=kn, wind=kn, temperature=°C.
export const UNIT_CATEGORIES = [
  { cat: "depth", key: "depthUnit", def: "ft", opts: [["m", "m"], ["ft", "ft"]], label: "Depth" },
  { cat: "distance", key: "distanceUnit", def: "NM", opts: [["NM", "NM"], ["km", "km"], ["mi", "mi"]], label: "Distance" },
  { cat: "height", key: "heightUnit", def: "ft", opts: [["m", "m"], ["ft", "ft"]], label: "Height" },
  { cat: "speed", key: "speedUnit", def: "kn", opts: [["kn", "kn"], ["km/h", "km·h⁻¹"], ["mph", "mph"]], label: "Speed" },
  { cat: "wind", key: "windUnit", def: "kn", opts: [["kn", "kn"], ["m/s", "m·s⁻¹"], ["mph", "mph"], ["Bft", "Bft"]], label: "Wind speed" },
  { cat: "temp", key: "tempUnit", def: "F", opts: [["C", "°C"], ["F", "°F"]], label: "Temperature" },
];

// The mariner-settings defaults contributed by the unit categories (depth excluded
// — it's seeded with DEFAULT_MARINER's own value). Spread into DEFAULT_MARINER.
export const UNIT_DEFAULTS = Object.fromEntries(
  UNIT_CATEGORIES.filter((c) => c.cat !== "depth").map((c) => [c.key, c.def]),
);

// Display suffix for a unit key (what reads in the UI).
const UNIT_SUFFIX = { NM: "NM", km: "km", mi: "mi", m: "m", ft: "ft", kn: "kn", "km/h": "km/h", mph: "mph", "m/s": "m/s", Bft: "Bft", C: "°C", F: "°F" };
export function unitSuffix(u) { return UNIT_SUFFIX[u] ?? u; }

// kn → Beaufort force (0–12), the standard upper-bound thresholds.
function beaufort(kn) {
  const t = [1, 3, 6, 10, 16, 21, 27, 33, 40, 47, 55, 63];
  let f = 0;
  while (f < t.length && kn >= t[f]) f++;
  return f;
}

// Convert a value from its canonical source unit to `unit`. Unknown unit → identity.
export function convertDistance(nm, unit) {
  return unit === "km" ? nm * NM_TO_KM : unit === "mi" ? nm * NM_TO_MI : nm;
}
export function convertHeight(m, unit) {
  return unit === "ft" ? m * M_TO_FT : m;
}
export function convertSpeed(kn, unit) {
  return unit === "km/h" ? kn * KN_TO_KMH : unit === "mph" ? kn * KN_TO_MPH : kn;
}
export function convertWind(kn, unit) {
  return unit === "m/s" ? kn * KN_TO_MS : unit === "mph" ? kn * KN_TO_MPH : unit === "Bft" ? beaufort(kn) : kn;
}
export function convertTemp(c, unit) {
  return unit === "F" ? c * 9 / 5 + 32 : c;
}

// Format a canonical value for a category as "<value> <suffix>", picking a sane
// number of decimals. `prefs` is the mariner settings object. Returns a string.
export function format(cat, value, prefs) {
  const unit = (prefs && prefs[catKey(cat)]) || defFor(cat);
  let v;
  switch (cat) {
    case "distance": v = convertDistance(value, unit); break;
    case "height": v = convertHeight(value, unit); break;
    case "depth": v = unit === "ft" ? value * M_TO_FT : value; break;
    case "speed": v = convertSpeed(value, unit); break;
    case "wind": v = convertWind(value, unit); break;
    case "temp": v = convertTemp(value, unit); break;
    default: v = value;
  }
  if (unit === "Bft") return "Bft " + Math.round(v);
  const dec = Math.abs(v) >= 100 ? 0 : Math.abs(v) >= 10 ? 1 : 2;
  return `${trim(v.toFixed(dec))} ${unitSuffix(unit)}`;
}

function trim(s) { return s.includes(".") ? s.replace(/\.?0+$/, "") : s; }
function catKey(cat) { return (UNIT_CATEGORIES.find((c) => c.cat === cat) || {}).key; }
function defFor(cat) { return (UNIT_CATEGORIES.find((c) => c.cat === cat) || {}).def; }
