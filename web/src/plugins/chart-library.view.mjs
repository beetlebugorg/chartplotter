// <chart-library> VIEW — the render chrome for chart-library.mjs.
//
// This file is the CHROME half of the chart-library split: the component's
// entire CSS (the STYLE constant, lifted verbatim from the old element) plus a
// set of PURE, STATELESS markup builders. Every export here is a pure function
// of its arguments → HTML string: no `this`, no element/DOM access, no injected
// deps, no event wiring. The LOGIC half (chart-library.mjs) gathers the state
// and data these functions need, calls them to produce HTML, drops it into the
// shadow DOM, then wires the rendered nodes by the data-* attributes emitted
// here. Keep the HTML output here EXACTLY in sync with that wiring (classes,
// data-attrs, ids, text) — these functions decide markup, the logic decides
// behaviour.
//
// Convention reference: this is the first component split logic↔view. Other
// components follow the same shape — `<name>.view.mjs` exports `STYLE` + pure
// markup builders; `<name>.mjs` keeps the element class, state, deps, lifecycle,
// data methods and event wiring, importing the builders from the view file.

import { esc, fmtIssue } from "../lib/util.mjs";

// Charts-panel styles, lifted verbatim from the shell <style> (the rules that
// only ever applied inside #charts-body). The element has its own shadow DOM, so
// a handful of generic helpers (.btn, .muted, .row, .grow) are duplicated here
// rather than relying on the shell's sheet — they do not cross the shadow boundary.
export const STYLE = `
  :host { display:block; }
  .btn { cursor:pointer; border:1px solid var(--ui-border-strong); background:var(--ui-surface); border-radius:6px; padding:6px 10px; font:inherit; color:var(--ui-text); }
  .btn:hover { background:var(--ui-hover); }
  .add-hint { color:var(--ui-text-dim); font-size:12px; line-height:1.5; margin:0 0 12px; }
  .pack-search { width:100%; box-sizing:border-box; border:1px solid var(--ui-border-strong); border-radius:8px; padding:9px 12px; font:inherit; font-size:16px; margin-bottom:10px; background:var(--ui-surface); color:var(--ui-text); }
  .pack-search:focus { outline:none; border-color:var(--ui-accent); }
  @keyframes dlspin { to { transform:rotate(360deg); } }
  /* chart download: Finder-style 3-pane drill-down */
  .miller { display:flex; align-items:stretch; border:1px solid var(--ui-border-2); border-radius:10px; overflow:hidden; min-height:300px; max-height:min(62vh,560px); max-height:min(62dvh,560px); margin:2px 0 12px; }
  .mcol { flex:0 0 26%; min-width:0; overflow-y:auto; overscroll-behavior:contain; -webkit-overflow-scrolling:touch; border-right:1px solid var(--ui-border-2); padding:6px; }
  /* The packs column is the 3rd child of .miller (back bar · provider · packs · detail). */
  .mcol:nth-child(3) { flex:0 0 32%; }
  .mcol.mcol-detail { flex:1 1 0; border-right:none; padding:12px; }
  .mcol-h { font-size:11px; font-weight:700; color:var(--ui-text); padding:1px 6px 0; }
  .mcol-head { position:sticky; top:0; background:var(--ui-surface); padding:4px 0 7px; margin-bottom:2px; border-bottom:1px solid var(--ui-border-2); z-index:1; }
  .mcol-meta { font-size:10.5px; color:var(--ui-text-faint); padding:1px 6px 0; line-height:1.35; }
  .m-row { display:flex; align-items:center; gap:8px; padding:8px; border-radius:7px; cursor:pointer; transition:background .1s; }
  .btn, .pk-btn, .m-row, .cta { touch-action:manipulation; -webkit-touch-callout:none; -webkit-user-select:none; user-select:none; }
  .m-row:hover { background:var(--ui-hover); }
  .m-row:focus-visible { outline:none; box-shadow:inset 0 0 0 2px var(--ui-accent); }
  .m-row.sel { background:var(--ui-accent); }
  .m-row.sel .m-name, .m-row.sel .m-sub, .m-row.sel .m-chev { color:var(--ui-accent-text); }
  .m-row.sel .m-badge.on { background:rgba(255,255,255,.25); color:var(--ui-accent-text); }
  .m-row.dim { opacity:.4; }
  .m-row.match { background:rgba(21,101,192,.10); }
  .m-row.match.sel { background:var(--ui-accent); }
  .m-info { flex:1; min-width:0; display:flex; flex-direction:column; gap:1px; }
  .m-name { font-weight:600; font-size:13px; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
  .m-sub { color:var(--ui-text-faint); font-size:11px; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
  .m-chev { flex:none; color:var(--ui-text-faint); font-size:16px; }
  .m-badge { flex:none; font-size:9.5px; font-weight:700; text-transform:uppercase; letter-spacing:.03em; padding:2px 7px; border-radius:10px; display:inline-flex; align-items:center; gap:5px; }
  .m-badge.on { background:#e4f5ea; color:#1f7a36; }
  .m-badge.off { background:var(--ui-surface-2); color:var(--ui-text-faint); }
  .m-badge.dl { background:color-mix(in srgb, var(--ui-accent) 16%, transparent); color:var(--ui-accent); }
  .m-badge.queued { background:var(--ui-surface-2); color:var(--ui-text-dim); }
  .m-row.dim .m-badge { opacity:.7; }
  .m-empty { color:var(--ui-text-faint); font-size:12px; padding:14px 8px; text-align:center; line-height:1.5; }
  /* detail pane — a STATIC coverage snapshot rendered once over our own coastline
     basemap (no live embedded map), shown small and clickable to enlarge. */
  .prev-map { width:100%; height:84px; border:1px solid var(--ui-border-2); border-radius:8px; background:var(--ui-surface-2); overflow:hidden; position:relative; }
  .prev-img { display:block; width:100%; height:100%; object-fit:cover; cursor:zoom-in; touch-action:manipulation; }
  .prev-ph { width:100%; height:100%; display:flex; align-items:center; justify-content:center; color:var(--ui-text-faint); font-size:12px; }
  .m-detail-body { padding:12px 2px 2px; }
  .m-detail-title { font-weight:700; font-size:15px; }
  .m-detail-sub { color:var(--ui-text-dim); font-size:12px; line-height:1.45; margin-top:3px; }
  .m-detail-meta { color:var(--ui-text-faint); font-size:11.5px; font-variant-numeric:tabular-nums; margin-top:5px; }
  .m-detail-act { margin-top:12px; display:flex; gap:8px; flex-wrap:wrap; }
  .pk-btn.danger { color:#c0392b; }
  .pk-btn.danger:hover { background:#fdeceb; border-color:#e2b6b1; }
  .pk-btn { display:inline-flex; align-items:center; justify-content:center; gap:7px; border:none; background:var(--ui-accent); color:var(--ui-accent-text); border-radius:7px; padding:8px 14px; font:inherit; font-size:13px; font-weight:600; cursor:pointer; white-space:nowrap; }
  .pk-btn:hover { background:var(--ui-accent-hover); }
  .pk-btn:disabled { background:#9fb6cf; cursor:default; }
  /* Downloading now: greyed, spinner, no hover lift. Queued: muted, waiting. */
  .pk-btn.downloading, .pk-btn.downloading:hover { background:#9fb6cf; cursor:default; }
  .pk-btn.queued, .pk-btn.queued:hover { background:var(--ui-surface-2); color:var(--ui-text-dim); border:1px solid var(--ui-border-strong); }
  .pk-btn.ghost { background:var(--ui-surface); color:var(--ui-text-dim); border:1px solid var(--ui-border-strong); }
  .pk-btn.ghost:hover { background:#fdeceb; color:#c0392b; border-color:#e2b6b1; }
  .pk-btn.mini { padding:5px 9px; font-size:11.5px; }
  /* Spinner used in the Downloading button + list badge. */
  .pk-spin { width:12px; height:12px; flex:none; border-radius:50%;
    border:2px solid rgba(255,255,255,.45); border-top-color:#fff; animation:dlspin .8s linear infinite; }
  .m-badge.dl .pk-spin { width:9px; height:9px; border-width:2px; border-color:color-mix(in srgb, var(--ui-accent) 35%, transparent); border-top-color:var(--ui-accent); }
  @media (prefers-reduced-motion: reduce) { .pk-spin { animation-duration:2s; } }
  /* NOAA data freshness footer */
  .data-fresh { color:var(--ui-text-faint); font-size:11.5px; text-align:center; line-height:1.5; padding:14px 0 4px; border-top:1px solid var(--ui-border-2); margin-top:4px; }
  /* import drop zone + archive list */
  .drop { border:2px dashed var(--ui-border-strong); border-radius:8px; padding:18px; text-align:center; color:var(--ui-text-dim); margin-bottom:10px; cursor:pointer; touch-action:manipulation; }
  .drop.over { border-color:var(--ui-accent); background:var(--ui-hover); color:var(--ui-accent); }
  .row { display:flex; align-items:center; gap:8px; padding:4px 0; border-bottom:1px solid var(--ui-border-2); }
  .row .name { font-weight:600; } .row .meta { color:var(--ui-text-dim); font-size:12px; }
  .grow { flex:1; }
  .muted { color:var(--ui-text-dim); }
  /* Per-cell show/hide list (under the pack's enable/disable/remove actions). */
  .cell-list { margin-top:14px; border-top:1px solid var(--ui-border-2); padding-top:10px; }
  .cell-list-head { display:flex; align-items:baseline; justify-content:space-between; gap:8px; margin-bottom:6px; }
  .cell-list-title { font-size:12.5px; font-weight:600; color:var(--ui-text-dim); }
  .cell-list-acts { font-size:12px; color:var(--ui-text-faint); white-space:nowrap; }
  .cl-link { border:none; background:none; padding:0; font:inherit; font-size:12px; color:var(--ui-accent); cursor:pointer; }
  .cl-link:hover { text-decoration:underline; }
  .cell-list-body { max-height:240px; overflow:auto; overscroll-behavior:contain; -webkit-overflow-scrolling:touch; }
  .cell-row { display:flex; align-items:center; gap:8px; padding:5px 2px; border-bottom:1px solid var(--ui-border-2); cursor:pointer; }
  .cell-row .name { font-weight:600; font-size:13px; } .cell-row .meta { color:var(--ui-text-dim); font-size:12px; }
  @media (pointer:coarse) { .cell-row { min-height:var(--tap-min,44px); } }
  /* NOAA ENC user-agreement gate (shown before the first download). */
  .modal { position:fixed; inset:0; z-index:30; display:flex; align-items:center; justify-content:center;
    padding:calc(var(--sa-top,0px) + 12px) calc(var(--sa-right,0px) + 12px) calc(var(--sa-bottom,0px) + 12px) calc(var(--sa-left,0px) + 12px);
    box-sizing:border-box; background:rgba(15,20,26,.55); backdrop-filter:blur(2px); }
  .modal[hidden] { display:none; }
  .modal-card { background:var(--ui-surface); max-width:520px; width:calc(100% - 40px); max-height:86%; overflow:auto;
    overscroll-behavior:contain; -webkit-overflow-scrolling:touch;
    border-radius:12px; padding:20px 22px; box-shadow:0 12px 40px rgba(0,0,0,.3); font:14px/1.5 system-ui,sans-serif; color:var(--ui-text); }
  .modal-card h2 { margin:0 0 10px; font-size:18px; }
  .modal-card .agree-body ul { margin:8px 0; padding-left:20px; }
  .modal-card .agree-body li { margin:5px 0; }
  .modal-card a { color:var(--ui-accent); }
  .agree-actions { display:flex; gap:10px; justify-content:flex-end; margin-top:16px; }
  .cta { background:var(--ui-accent); color:var(--ui-accent-text); border:none; border-radius:8px; padding:11px 12px; font:inherit;
    font-weight:600; cursor:pointer; display:inline-flex; align-items:center; justify-content:center; gap:7px; }
  .cta:hover { background:var(--ui-accent-hover); }
  /* Phone-only back bar: hidden on desktop/tablet (the 3 panes show at once). */
  .miller-back { display:none; }
  /* Phone drill-down: show ONE .mcol at a time (provider→pack→detail) with a
     back bar to step UP a level. Which column shows is driven by .miller's
     data-level; the others are hidden. Desktop/tablet (>560px) keep all three
     side-by-side, so none of this applies there. */
  @media (max-width:560px) {
    .miller { flex-direction:column; max-height:none; min-height:0; }
    .mcol, .mcol:nth-child(2), .mcol.mcol-detail { flex:1 1 auto; width:100%; box-sizing:border-box; border-right:none; border-bottom:none; max-height:none; }
    /* One column at a time: hide all, then reveal the one matching the level. */
    .miller > .mcol { display:none; }
    .miller[data-level="provider"] > .mcol:nth-child(2),
    .miller[data-level="pack"] > .mcol:nth-child(3),
    .miller[data-level="detail"] > .mcol:nth-child(4) { display:flex; flex-direction:column; }
    /* The back bar is the miller's first child, so the visible column is the
       1st/2nd/3rd .mcol → nth-child 2/3/4 (provider/pack/detail). */
    .miller[data-level="provider"] .miller-back { display:none; }
    .miller-back { display:flex; align-items:center; gap:5px; flex:none; position:sticky; top:0; z-index:2;
      padding:1px 2px 5px; background:var(--ui-surface); }
    /* Subtle: a borderless text chevron, not a boxed button. Keeps a 44px tap
       area via min-height but reads as a quiet "back" link. */
    .miller-back .mb-btn { display:inline-flex; align-items:center; gap:3px; min-height:var(--tap-min,44px);
      padding:4px 6px; border:none; background:none; color:var(--ui-text-dim); font:inherit; font-size:13px;
      font-weight:500; cursor:pointer; touch-action:manipulation; -webkit-user-select:none; user-select:none; }
    .miller-back .mb-btn:active { color:var(--ui-text); }
    @media (hover:hover) { .miller-back .mb-btn:hover { color:var(--ui-text); } }
    .miller-back .mb-crumb { color:var(--ui-text-faint); font-size:12.5px; font-weight:600; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
  }
  /* Touch sizing: primary nav rows, buttons and the archive cell list reach 44px. */
  @media (pointer:coarse) {
    .m-row { min-height:var(--tap-min,44px); }
    .pk-btn, .pk-btn.mini { min-height:var(--tap-min,44px); }
    .m-detail-act { gap:12px; }
    .row { min-height:var(--tap-min,44px); padding:8px 0; }
  }
  /* No file drag-and-drop on touch: hide the "Drop … here" prefix; the pick button is the target. */
  @media (hover:none) {
    .drop .drop-hint { display:none; }
  }
`;

// The widget (prebaked) Library body: import-only — no NOAA download / region
// picker. Wired by _wireImport via #file/#drop/#pick.
export function widgetBody() {
  return `
        <p class="add-hint">Add your own charts — drop a NOAA <code>.zip</code> / <code>.000</code>, or a baked <code>.pmtiles</code>. They're baked right here in your browser and kept offline alongside the prebaked charts.</p>
        <div id="drop" class="drop"><span class="drop-hint">Drop a <code>.zip</code>, <code>.000</code> or <code>.pmtiles</code> here, or<br></span><button id="pick" class="btn" style="margin-top:6px">Choose files…</button></div>
        <input id="file" type="file" accept=".zip,.000,.pmtiles" multiple hidden>
        <div id="import-log" class="muted"></div>
        <div id="archive-list"></div>`;
}

// The full (non-widget) Library body: search box + 3-pane miller + freshness
// footer. The three columns are passed in pre-rendered (the logic computes the
// data they need).
export function libraryBody({ searchHtml, providersCol, packsCol, detailCol, freshnessHtml, level, backLabel }) {
  return `
      ${searchHtml}
      <div class="miller" data-level="${level || "provider"}">
        ${millerBack(backLabel)}
        ${providersCol}
        ${packsCol}
        ${detailCol}
      </div>
      ${freshnessHtml}`;
}

// Phone-only back bar (first child of .miller). CSS hides it on desktop/tablet
// and at the provider (top) level; the logic flips .miller's data-level and the
// crumb text on selection / back. `back` is the parent-level title shown after
// the ‹ chevron. Wired by the logic via #miller-back.
export function millerBack(back) {
  return `<div id="miller-back" class="miller-back" role="button" tabindex="0">
      <span class="mb-btn">‹ Back</span><span class="mb-crumb">${esc(back || "")}</span></div>`;
}

// Find-a-chart search box.
export function packSearch(q) {
  return `<input id="pack-search" class="pack-search" type="search" placeholder="Find a chart, port, or region…" autocomplete="off" spellcheck="false" value="${esc(q || "")}">`;
}

// Pane 1: providers. Each provider carries a precomputed class suffix (sel/
// match/dim) computed by the logic from the selection + search hits.
//   providers: [{ id, name, sub, cls }]
export function providersCol(providers) {
  const rows = providers.map((p) => {
    return `<div class="m-row${p.cls}" data-prov="${p.id}" role="button" tabindex="0">
        <span class="m-info"><span class="m-name">${esc(p.name)}</span><span class="m-sub">${esc(p.sub)}</span></span><span class="m-chev">›</span></div>`;
  }).join("");
  return `<div class="mcol"><div class="mcol-h">Source</div>${rows}</div>`;
}

// Pane-2 header: provider name + one-line meta (catalogue date/count etc).
export function packsHeader({ providerName, line }) {
  return `<div class="mcol-head"><div class="mcol-h">${esc(providerName)}</div><div class="mcol-meta">${esc(line)}</div></div>`;
}

// Status pill for a pack row. State is fully resolved by the logic:
//   { installed, disabled, downloadState }  (downloadState: "downloading"|"queued"|null)
export function packBadge({ installed, disabled, downloadState }) {
  if (!installed) {
    if (downloadState === "downloading") return '<span class="m-badge dl"><span class="pk-spin"></span>Downloading</span>';
    if (downloadState === "queued") return '<span class="m-badge queued">Queued</span>';
    return "";
  }
  return disabled
    ? '<span class="m-badge off">Disabled</span>'
    : '<span class="m-badge on">Active</span>';
}

// A user-imported pack row. `badge` is the pre-rendered packBadge HTML.
export function userPackRow(pk, { selPack, badge }) {
  return `<div class="m-row on${selPack === pk.key ? " sel" : ""}" data-pack="${esc(pk.key)}" role="button" tabindex="0">
      <span class="m-info"><span class="m-name">${esc(pk.title)}</span><span class="m-sub">${esc(pk.sub)}</span></span>${badge}</div>`;
}

// One ordinary pack row (NOAA/IENC). The logic resolves the class suffix, the
// (possibly search-rewritten) sub line, the data-cg attr, and the badge HTML.
//   row: { key, title, cls, sub, cg, badge }
export function packRow(row) {
  return `<div class="m-row${row.cls}" data-pack="${esc(row.key)}"${row.cg ? ` data-cg="${row.cg}"` : ""} role="button" tabindex="0">
        <span class="m-info"><span class="m-name">${esc(row.title)}</span><span class="m-sub">${row.sub}</span></span>${row.badge}</div>`;
}

// Pane 2 wrapper: the header + the already-rendered rows (or an .m-empty).
export function packsCol({ header, rows }) {
  return `<div class="mcol">${header}${rows}</div>`;
}

// A standalone empty-state row (loading / nothing installed / no packs).
export function emptyRow(text) {
  return `<div class="m-empty">${text}</div>`;
}

// The Download button for a pack key, by queue state.
//   { downloading, queued }  (booleans; not-both)
export function downloadBtn(key, { downloading, queued }) {
  if (downloading)
    return `<button class="pk-btn downloading" data-getpack="${esc(key)}" disabled><span class="pk-spin"></span>Downloading…</button>`;
  if (queued)
    return `<button class="pk-btn queued" data-getpack="${esc(key)}" disabled>Queued</button>`;
  return `<button class="pk-btn" data-getpack="${esc(key)}">⬇ Download</button>`;
}

// Detail pane: empty-state ("Select a chart pack.").
export function detailEmpty() {
  return `<div class="mcol mcol-detail"><div class="m-empty">Select a chart pack.</div></div>`;
}

// Detail pane: an installed pack NOT in the current catalogue (remove-only).
//   { label, key, installed, busy }
export function detailUnknownSet({ label, key, installed, busy }) {
  return `<div class="mcol mcol-detail"><div class="m-detail-body">
        <div class="m-detail-title">${esc(label)}${installed ? ' <span class="pl-tick">✓</span>' : ""}</div>
        <div class="m-detail-sub">${esc(key)}</div>
        <div class="m-detail-act"><button class="pk-btn ghost" data-uninstall-set="${esc(key)}"${busy ? " disabled" : ""}>Remove</button></div>
      </div></div>`;
}

// Detail pane: the full pack detail (coverage preview placeholder + actions).
// Everything is resolved by the logic; `act` is the pre-rendered action HTML
// (enable/disable/remove buttons, or the download button), `tick` the status
// pill, `previewMap` the (possibly empty) #preview-map placeholder.
//   { title, tick, sub, meta, act, previewMap, extra }
// `extra` (optional) is block HTML placed after the action buttons — e.g. the
// per-cell show/hide list for an installed pack.
export function detailPack({ title, tick, sub, meta, act, previewMap, extra }) {
  return `<div class="mcol mcol-detail">
      ${previewMap}
      <div class="m-detail-body">
        <div class="m-detail-title">${esc(title)}${tick}</div>
        <div class="m-detail-sub">${sub}</div>
        ${meta ? `<div class="m-detail-meta">${esc(meta)}</div>` : ""}
        <div class="m-detail-act">${act}</div>
        ${extra || ""}
      </div></div>`;
}

// The enable/disable + remove action buttons for an installed pack.
//   { key, disabled, busy }
export function installedActions({ key, disabled, busy }) {
  return `<button class="pk-btn ghost" data-${disabled ? "enable" : "disable"}="${esc(key)}"${busy ? " disabled" : ""}>${disabled ? "Enable" : "Disable"}</button>
         <button class="pk-btn ghost danger" data-uninstall-set="${esc(key)}"${busy ? " disabled" : ""}>Remove</button>`;
}

// The per-cell show/hide list for an installed pack, shown under installedActions.
// Each row is one chart cell with its title and a checkbox; CHECKED = shown,
// unchecked = hidden from the map (a client filter on the baked `cell` id — no
// re-bake). `Select all` / `Clear all` toggle the whole pack.
//   items: [{ name, title, shown }]   nShown: count currently shown
export function packCellList({ items, nShown }) {
  if (!items.length) return "";
  const rows = items.map((it) =>
    `<label class="cell-row"><input type="checkbox" data-cell="${esc(it.name)}"${it.shown ? " checked" : ""}>
       <span class="grow"><span class="name">${esc(it.name)}</span> <span class="meta">${esc(it.title || "")}</span></span></label>`
  ).join("");
  return `<div class="cell-list">
      <div class="cell-list-head">
        <span class="cell-list-title">Charts in this pack (${nShown}/${items.length})</span>
        <span class="cell-list-acts"><button class="cl-link" data-cells-all>Select all</button> · <button class="cl-link" data-cells-none>Clear all</button></span>
      </div>
      <div class="cell-list-body">${rows}</div>
    </div>`;
}

// The #preview-map placeholder (the logic mounts a MapLibre map into it).
export function previewMapHost() {
  return `<div id="preview-map" class="prev-map"></div>`;
}

// The User-Charts import detail pane (drop zone baked into the "import" pack).
export function importDetail() {
  return `<div class="mcol mcol-detail"><div class="m-detail-body">
      <div class="m-detail-title">Import your charts</div>
      <div class="m-detail-sub">Add ENC you already have — a NOAA/IENC exchange-set <code>.zip</code>, individual <code>.000</code> cells, or a baked <code>.pmtiles</code>. They're baked on the server and kept under User Charts.</div>
      <div id="drop" class="drop"><span class="drop-hint">Drop a <code>.zip</code>, <code>.000</code> or <code>.pmtiles</code> here, or<br></span><button id="pick" class="btn" style="margin-top:8px">Choose files…</button></div>
      <input id="file" type="file" accept=".zip,.000,.pmtiles" multiple hidden>
      <div id="import-log" class="muted"></div>
      <div id="archive-list"></div>
    </div></div>`;
}

// NOAA data freshness footer (date + chart count).
export function dataFreshness({ catalogDate, total }) {
  if (!catalogDate) return "";
  return `<div class="data-fresh">NOAA chart data current as of <b>${fmtIssue(catalogDate)}</b> · ${total} charts available</div>`;
}

// The NOAA ENC user-agreement modal markup. Logic owns show/hide + promise
// resolution; it wires #agree-accept / #agree-decline. The two NOAA URLs are
// passed in (they live in the logic file's exports).
export function agreementModal({ encUrl, agreementUrl }) {
  return `
        <div id="agree" class="modal">
          <div class="modal-card">
            <h2>NOAA ENC® — User Agreement</h2>
            <div class="agree-body">
              <p>NOAA Electronic Navigational Charts (NOAA ENC®) are downloaded directly from NOAA. By continuing you acknowledge that you have read, understood, and accepted NOAA's User Agreement.</p>
              <ul>
                <li><b>Not for navigation.</b> Charts downloaded and baked here are processed for display and are <b>not</b> the official NOAA ENC; they do not meet chart-carriage regulations. Use official, up-to-date charts for navigation.</li>
                <li><b>Updates.</b> NOAA updates ENCs weekly on a best-efforts basis. You are responsible for ensuring you have the current edition and latest updates.</li>
                <li><b>Origin.</b> Charts are sourced from <a href="${encUrl}" target="_blank" rel="noopener">NOAA Office of Coast Survey</a>. NOAA makes no warranty and assumes no liability for their use.</li>
              </ul>
              <p>Read the full terms: <a href="${agreementUrl}" target="_blank" rel="noopener">NOAA ENC User Agreement</a>.</p>
            </div>
            <div class="agree-actions">
              <button id="agree-decline" class="btn" type="button">Decline</button>
              <button id="agree-accept" class="cta" type="button">Accept &amp; continue</button>
            </div>
          </div>
        </div>`;
}

// The "from archive" selectable cell list (after a .zip is opened).
//   items: [{ name, label, checked }]   nSel: count of selected
export function archiveList({ items, nSel }) {
  return `<h4>From archive (${items.length})</h4>` + items.map((it) => {
    const checked = it.checked ? "checked" : "";
    return `<label class="row"><input type="checkbox" data-name="${it.name}" ${checked}>
        <span class="grow"><span class="name">${it.name}</span> <span class="meta">${it.label || ""}</span></span></label>`;
  }).join("") +
    `<div style="margin-top:8px"><button id="import-btn" class="btn">Import ${nSel} chart(s)</button></div>`;
}
