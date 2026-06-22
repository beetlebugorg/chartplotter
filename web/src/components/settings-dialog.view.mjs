// settings-dialog.view.mjs — CHROME for <settings-dialog>: the CSS + pure markup
// builders that turn a contribution's declarative items into the same control
// look the app has always used (toggle switch, segmented single/multi, number +
// unit, select). No `this`, no DOM, no state — (args) → HTML string.
//
// Item types (see SettingsRegistry):
//   toggle    — a switch; value is boolean
//   segmented — single-select segmented buttons; options [[value,label],…]
//   multi     — independent boolean buttons in one segmented strip; options are
//               [[key,label],…] (each its OWN boolean key) + optional locked[]
//   number    — numeric input + optional unit; optional transform for display
//   select    — a <select> dropdown; options [[value,label],…]
// A control carries data-contrib (owning contribution id) + data-key so the host
// can route a change back to contribution.set(key, value).

import { esc } from "../lib/util.mjs";

export const STYLE = `
  :host { display:block; }
  #body { padding-top:2px; }
  .set-shell { display:flex; align-items:stretch; border:1px solid var(--ui-border-2); border-radius:11px; overflow:hidden; min-height:360px; max-height:min(66vh,620px); }
  .set-rail { flex:0 0 124px; display:flex; flex-direction:column; gap:3px; padding:8px 7px; border-right:1px solid var(--ui-border-2); background:var(--ui-surface-2); }
  .set-rail button { text-align:left; border:none; background:none; color:var(--ui-text-dim); font:inherit; font-size:13px; font-weight:600; padding:9px 11px; border-radius:8px; cursor:pointer; transition:background .1s,color .1s; }
  .set-rail button:hover { background:var(--ui-surface); color:var(--ui-text); }
  .set-rail button.sel { background:var(--ui-accent); color:var(--ui-accent-text); }
  .set-pane { flex:1 1 0; min-width:0; overflow-y:auto; padding:4px 18px 10px; }
  .set-group { margin-top:14px; font-size:11.5px; font-weight:700; letter-spacing:.04em; text-transform:uppercase; color:var(--ui-text-faint); padding:0 2px 2px; }
  .set-group:first-child { margin-top:4px; }
  .set-host { /* a contribution's custom-render slot */ }
  .set-host .dev-tools { border-top:1px solid var(--ui-border-2); margin-top:8px; }

  .set-row { display:flex; align-items:center; gap:14px; padding:13px 2px; border-bottom:1px solid var(--ui-border-2); }
  .set-row:last-child { border-bottom:none; }
  .set-row .lbl { display:flex; flex-direction:column; min-width:0; flex:1 1 auto; }
  .set-row .lbl .t { font-weight:600; font-size:13.5px; }
  .set-row .lbl .d { font-size:12px; color:var(--ui-text-faint); margin-top:3px; line-height:1.45; }
  .set-row .ctl { flex:none; margin-left:auto; display:flex; align-items:center; gap:6px; }
  .set-row .ctl input[type=number] { width:58px; text-align:right; border:1px solid var(--ui-border-strong); border-radius:6px; padding:5px 7px; font:inherit; background:var(--ui-surface); color:var(--ui-text); }
  .set-row .ctl .unit { color:var(--ui-text-faint); font-size:12px; min-width:14px; }
  .set-row .ctl select { border:1px solid var(--ui-border-strong); border-radius:6px; padding:5px 8px; font:inherit; background:var(--ui-surface); color:var(--ui-text); }

  .switch { position:relative; width:38px; height:22px; display:inline-block; flex:none; }
  .switch input { opacity:0; width:0; height:0; }
  .switch .sl { position:absolute; inset:0; background:var(--ui-border-strong); border-radius:22px; cursor:pointer; transition:.15s; }
  .switch .sl:before { content:""; position:absolute; width:16px; height:16px; left:3px; top:3px; background:#fff; border-radius:50%; transition:.15s; box-shadow:0 1px 2px rgba(0,0,0,.3); }
  .switch input:checked + .sl { background:var(--ui-accent); }
  .switch input:checked + .sl:before { transform:translateX(16px); }

  .seg { display:inline-flex; border:1px solid var(--ui-border-strong); border-radius:7px; overflow:hidden; }
  .seg button { border:none; background:var(--ui-surface); padding:6px 11px; font:inherit; font-size:13px; cursor:pointer; border-left:1px solid var(--ui-border-2); color:var(--ui-text); }
  .seg button:first-child { border-left:none; }
  .seg button.sel { background:var(--ui-accent); color:var(--ui-accent-text); }
  .seg button:disabled { cursor:default; }

  .set-empty { padding:24px 2px; color:var(--ui-text-faint); font-size:13px; }
  @media (max-width:560px) {
    .set-row { flex-wrap:wrap; gap:8px 14px; }
    .set-row .lbl { flex:1 1 60%; }
  }
`;

// One control, dispatched by item.type. `value` is the item's current value (the
// host read it from contribution.get). `on` reads a boolean key for `multi`.
function control(item, value, on) {
  const k = `data-contrib="${esc(item._cid)}" data-key="${esc(item.key)}"`;
  switch (item.type) {
    case "toggle":
      return `<label class="switch"><input type="checkbox" ${k} data-type="toggle" ${value ? "checked" : ""}><span class="sl"></span></label>`;
    case "segmented":
      return `<div class="seg">${(item.options || []).map(([v, lbl]) =>
        `<button ${k} data-type="segmented" data-val="${esc(v)}" class="${value === v ? "sel" : ""}">${esc(lbl)}</button>`).join("")}</div>`;
    case "multi":
      return `<div class="seg">${[
        ...(item.locked || []).map(([lbl]) => `<button disabled class="sel" title="Always on">${esc(lbl)}</button>`),
        ...(item.options || []).map(([key, lbl]) =>
          `<button data-contrib="${esc(item._cid)}" data-key="${esc(key)}" data-type="multi" class="${on(key) ? "sel" : ""}">${esc(lbl)}</button>`),
      ].join("")}</div>`;
    case "number":
      return `<input type="number" ${k} data-type="number" step="${esc(item.step || "any")}" value="${esc(value)}">${item.unit ? `<span class="unit">${esc(item.unit)}</span>` : ""}`;
    case "select":
      return `<select ${k} data-type="select">${(item.options || []).map(([v, lbl]) =>
        `<option value="${esc(v)}" ${value === v ? "selected" : ""}>${esc(lbl)}</option>`).join("")}</select>`;
    default:
      return "";
  }
}

// A labelled settings row wrapping one control.
export function settingRow(item, value, on) {
  const desc = item.desc ? `<span class="d">${esc(item.desc)}</span>` : "";
  return `<div class="set-row"><div class="lbl"><span class="t">${esc(item.label)}</span>${desc}</div>
    <div class="ctl">${control(item, value, on)}</div></div>`;
}

// A group subheading (only when a group has a title).
export function groupHead(title) { return title ? `<div class="set-group">${esc(title)}</div>` : ""; }

// The left tab rail.
export function tabRail(tabs, activeId) {
  return `<div class="set-rail">${tabs.map((t) =>
    `<button data-tab="${esc(t.id)}" class="${t.id === activeId ? "sel" : ""}">${esc(t.label)}</button>`).join("")}</div>`;
}

// The whole dialog shell: rail + the active pane's content.
export function shell(railHtml, paneHtml) {
  return `<div class="set-shell">${railHtml}<div class="set-pane">${paneHtml || `<div class="set-empty">Nothing to configure here.</div>`}</div></div>`;
}

// A host element for a contribution's custom render() — the host fills it after
// mounting (e.g. the Advanced tab's dev tools). `id` lets the logic find it.
export function customHost(id) { return `<div class="set-host" data-host="${esc(id)}"></div>`; }
