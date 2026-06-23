// dev-tools.view.mjs — CHROME for dev-tools.mjs (the contributed Advanced-tab dev
// tools). Pure, stateless markup builders + the CSS for the two kept tools:
// "Rebuild all charts" (rebake) and the feature inspector. No `this`, no DOM, no
// injected deps — every export is (args) → HTML string. The logic half
// (dev-tools.mjs) gathers state, calls these to build HTML into the settings
// dialog's custom-render host, then wires the rendered nodes by their ids/classes.
//
// Convention: same logic↔view split as chart-library / settings-dialog. The CSS
// lifted here is exactly the dev-panel + inspector chrome that used to live in the
// shell <style> (the `.dev-*` rules for the two kept sections + all `.ins-*`).

import { esc } from "../lib/util.mjs";

// Dev-tools chrome, lifted from the shell <style> (only the rules the two kept
// tools use). The host is a `.set-host` div in the dialog's shadow; the dialog's
// own sheet already styles `.set-host .dev-tools` (border-top/margin) — this
// component renders into that host, so we restate the dev-specific + button rules
// here (the dialog's `.btn` family lived in the shell sheet, not the dialog's).
export const STYLE = `
  .dev-tools { display:flex; flex-direction:column; }
  .dev-tools .btn { cursor:pointer; border:1px solid var(--ui-border-strong); background:var(--ui-surface); border-radius:6px; padding:6px 10px; font:inherit; color:var(--ui-text); }
  .dev-tools .btn:hover { background:var(--ui-hover); }
  .dev-sec { display:flex; flex-direction:column; gap:8px; padding:16px 0; border-top:1px solid var(--ui-border); }
  .dev-sec:first-child { padding-top:14px; border-top:none; }
  .dev-h { font-weight:600; font-size:11px; text-transform:uppercase; letter-spacing:.06em; color:var(--ui-text-faint); }
  .dev-note { margin:0; color:var(--ui-text-dim); font-size:12px; line-height:1.45; }
  .dev-row { display:flex; align-items:center; justify-content:space-between; gap:10px; min-height:24px; }
  .btn.wide { width:100%; text-align:center; }
  .btn.on { background:var(--ui-accent); color:#fff; border-color:var(--ui-accent); }
  .btn[disabled] { opacity:.45; cursor:default; }
  .btn.sm { padding:3px 10px; font-size:12px; white-space:nowrap; }

  /* Feature inspector result panel (the picked feature card(s) + cycler). */
  .ins-body { overflow:auto; overscroll-behavior:contain; -webkit-overflow-scrolling:touch; padding:12px 0 0; }
  .ins-empty { color:var(--ui-text-faint); text-align:center; padding:24px 10px; }
  .ins-lock { color:var(--ui-text-dim); font-size:12px; text-align:center; padding:0 0 8px; }
  .ins-cycler { display:flex; align-items:center; justify-content:center; gap:10px; padding:0 0 10px; color:var(--ui-text-dim); font-size:12px; }
  .ins-feat { margin:0 0 14px; border:1px solid var(--ui-border-2); border-radius:8px; overflow:hidden; }
  .ins-feat .ins-title { padding:8px 10px; background:var(--ui-surface-2); font-weight:600; display:flex; align-items:baseline; gap:8px; flex-wrap:wrap; }
  .ins-feat .ins-acr { color:var(--ui-text-dim); font:11px/1 ui-monospace,SFMono-Regular,Menlo,monospace; font-weight:500; }
  .ins-feat .ins-layer { margin-left:auto; color:var(--ui-text-faint); font-size:11px; }
  .ins-feat.ins-clickable { cursor:pointer; }
  .ins-feat.ins-clickable:hover { border-color:#00b8d4; }
  .ins-feat.active { border-color:#00b8d4; box-shadow:0 0 0 1px #00b8d4 inset; }
  .ins-feat.active .ins-title { background:rgba(0,184,212,.14); }
  .ins-pills { padding:6px 10px 0; }
  .ins-cell { display:inline-flex; align-items:center; gap:4px; background:var(--ui-accent); color:var(--ui-accent-text);
    border-radius:11px; padding:2px 9px; font:600 11px/1.4 ui-monospace,SFMono-Regular,Menlo,monospace; letter-spacing:.02em; }
  .ins-name { padding:2px 10px 0; font-weight:600; }
  .ins-light { display:inline-flex; align-items:center; gap:4px; background:#7e3ff2; color:#fff;
    border-radius:11px; padding:2px 9px; font:600 11px/1.4 ui-monospace,SFMono-Regular,Menlo,monospace; }
  .ins-kv { display:grid; grid-template-columns:minmax(80px,auto) 1fr; gap:3px 12px; padding:8px 10px; font:12px/1.5 ui-monospace,SFMono-Regular,Menlo,monospace; }
  .ins-kv .k { color:var(--ui-text-dim); }
  .ins-kv .v { color:var(--ui-text); word-break:break-word; }
  .dev-tools .btn, .ins-feat.ins-clickable { touch-action:manipulation; -webkit-touch-callout:none; -webkit-user-select:none; user-select:none; }
  /* Touch hit areas: cycler/area buttons and clickable feature cards reach 44px. */
  @media (pointer:coarse) {
    .dev-tools .btn, .dev-tools .btn.sm { min-height:var(--tap-min,44px); }
    .ins-cycler .btn { min-width:var(--tap-min,44px); }
    .ins-feat.ins-clickable .ins-title { min-height:var(--tap-min,44px); align-items:center; }
  }
  /* Touch-only inspector instructions (hover/SHIFT have no analogue). */
  .ins-touch-help { display:none; }
  @media (hover:none) {
    .ins-mouse-help { display:none; }
    .ins-touch-help { display:block; }
  }
`;

// The Rebake section: one wide "Rebuild all charts" button (disabled while busy).
export function rebakeSection(busy) {
  return `<section class="dev-sec">
    <div class="dev-h">Charts</div>
    <button id="dev-rebuild" class="btn wide"${busy ? " disabled" : ""}>↻ Rebuild all charts</button>
    <p class="dev-note">Re-bake every installed NOAA / IENC district into per-band tile sets from the cells already on the server — <b>no re-download</b>. Use after a baking change. Progress shows in the notification pill.</p>
  </section>`;
}

// The Feature-inspector section: the on/off toggle button + the "Copy feature
// debug" button (enabled only while inspecting). `inspecting` drives the labels.
// `selectingArea` drives the touch "Select area" toggle (box-capture on touch,
// where there is no SHIFT+drag). Help text is split mouse/touch (CSS-gated).
export function inspectorSection(inspecting, selectingArea) {
  return `<section class="dev-sec">
    <div class="dev-h">Feature inspector</div>
    <button id="dev-inspect" class="btn wide${inspecting ? " on" : ""}">${inspecting ? "● Inspecting — tap to stop" : "Inspect features"}</button>
    <button id="dev-area" class="btn wide${selectingArea ? " on" : ""}"${inspecting ? "" : " disabled"} title="Drag a box on the map to capture every feature inside it">${selectingArea ? "● Drag a box on the map" : "Select area"}</button>
    <button id="dev-feat" class="btn wide"${inspecting ? "" : " disabled"} title="Copy the selected feature's source/geometry/attributes to clipboard + server">Copy feature debug</button>
    <p class="dev-note ins-mouse-help">Hover a feature to highlight it · click to lock · SHIFT+drag to capture an area.</p>
    <p class="dev-note ins-touch-help">Tap a feature to inspect it · tap again to release · use “Select area” then drag a box to capture a region.</p>
  </section>`;
}

// The whole dev-tools panel skeleton: rebake + inspector sections + the
// inspect-result container (filled separately by the logic on hover/click).
export function devToolsPanel(busy, inspecting, selectingArea) {
  return `<div class="dev-tools">${rebakeSection(busy)}${inspectorSection(inspecting, selectingArea)}<div id="inspect-body" class="ins-body"></div></div>`;
}

// One inspected feature card. `label`/`acr`/`named` come from the logic's injected
// label lookups; `idx` (when given) makes it a clickable area-list item. `fmtVal`
// is a pure value formatter supplied by the logic.
export function featureCard(f, { label, acr, named }, fmtVal, idx) {
  const p = f.properties || {};
  const name = p.objnam ? `<div class="ins-name">${esc(p.objnam)}</div>` : "";
  const cellPill = p.cell ? `<span class="ins-cell" title="Source ENC cell">▦ ${esc(p.cell)}</span>` : "";
  const lightPill = p.light ? `<span class="ins-light" title="Light characteristic">✦ ${esc(p.light)}</span>` : "";
  const pills = cellPill || lightPill ? `<div class="ins-pills">${cellPill}${lightPill}</div>` : "";
  const keys = Object.keys(p).filter((k) => !["cell", "class", "objnam", "light"].includes(k)).sort();
  const rows = keys.map((k) => `<div class="k">${esc(k)}</div><div class="v">${esc(fmtVal(k, p[k]))}</div>`).join("")
    || `<div class="k" style="grid-column:1/-1;color:var(--ui-text-faint)">no attributes</div>`;
  const clickable = idx != null ? ` data-fi="${idx}" class="ins-feat ins-clickable"` : ` class="ins-feat"`;
  return `<div${clickable}>
    <div class="ins-title">${esc(label)}${named && acr ? `<span class="ins-acr">${esc(acr)}</span>` : ""}<span class="ins-layer">${esc(f.sourceLayer || "")}</span></div>
    ${name}
    ${pills}
    <div class="ins-kv">${rows}</div>
  </div>`;
}

export function lockNote() { return `<div class="ins-lock">🔒 Locked — click the map to release</div>`; }
export function emptyHint(msg) { return `<div class="ins-empty">${esc(msg)}</div>`; }
export function areaHint(n) { return `<div class="ins-cycler"><span>${n} features in area · click one to isolate it</span></div>`; }
export function areaMore(n) { return `<div class="ins-empty">…and ${n} more</div>`; }
export function cycler(i, n) {
  return `<div class="ins-cycler"><button id="ins-prev" class="btn" title="Previous">◀</button><span>${i + 1} / ${n} here</span><button id="ins-next" class="btn" title="Next">▶</button></div>`;
}
