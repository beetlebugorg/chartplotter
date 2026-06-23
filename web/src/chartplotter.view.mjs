// <chart-plotter> VIEW — the render chrome for the shell (chartplotter.mjs).
//
// The CHROME half of the shell split: the entire shadow-DOM <style> sheet (the
// STYLE constant, lifted verbatim from renderChrome) plus the static markup
// template (CHROME). Both are PURE — no `this`, no DOM, no event wiring. The
// LOGIC half (chartplotter.mjs) drops `<style>${STYLE}</style>${CHROME}` into the
// shadow root, then wires the rendered nodes by id. Keep this markup EXACTLY in
// sync with that wiring (ids, classes, data-attrs).
//
// Convention reference: chart-library.mjs / chart-library.view.mjs.

import { NOAA_ENC_URL } from "./plugins/chart-library.mjs"; // NOAA ENC page (static attribution link)
import { SEARCH_ICON, CHART_ICON, SETTINGS_ICON } from "./lib/openbridge-icons.mjs"; // vendored OpenBridge glyphs

export const STYLE = `
        :host { display:block; position:relative; width:100%; height:100%; font:13px/1.4 system-ui,sans-serif;
          /* The map is the UI: it fills the whole element. All chrome floats over
             it — four round buttons in the corners and one data card at the bottom
             centre. Panels drop down from their corner button as caret popovers. */
          --botbar-h:env(safe-area-inset-bottom,0px);
          --tap-min:44px;                                   /* min touch target */
          --sa-top:env(safe-area-inset-top,0px);
          --sa-right:env(safe-area-inset-right,0px);
          --sa-bottom:env(safe-area-inset-bottom,0px);
          --sa-left:env(safe-area-inset-left,0px);
          --input-font:16px;                                /* iOS focus-zoom floor */
          --ui-bg:#fafafa; --ui-surface:#fff; --ui-surface-2:#eef1f4; --ui-text:#2a2f35; --ui-text-dim:#7a828b; --ui-text-faint:#9aa0a8; --ui-border:#e2e2e2; --ui-border-2:#ededed; --ui-border-strong:#cfcfcf; --ui-hover:#f0f3f6; --ui-accent:#1565c0; --ui-accent-hover:#1257a8; --ui-accent-text:#fff; --ui-shadow:rgba(0,0,0,.2);
          --ownship-fill:#1f5fa0; --ownship-edge:#0a2c47; --ownship-halo:#fff;
          --ais-fill:#0a7d55; --ais-halo:#fff; --ais-danger:#e23b2e; }
        :host([data-scheme="dusk"]) {
          --ui-bg:#20262b; --ui-surface:#2a3137; --ui-surface-2:#333b42; --ui-text:#cdd6dc; --ui-text-dim:#9aa6ae; --ui-text-faint:#7d8990; --ui-border:#3a434a; --ui-border-2:#333b42; --ui-border-strong:#4a555d; --ui-hover:#353f47; --ui-accent:#4f9be6; --ui-accent-hover:#69abe9; --ui-accent-text:#0c1318; --ui-shadow:rgba(0,0,0,.5);
          --ownship-fill:#3a7ba6; --ownship-edge:#0a2230; --ownship-halo:#dde6ec;
          --ais-fill:#2f9f78; --ais-halo:#e8f0ec; --ais-danger:#ff6b5e; }
        :host([data-scheme="night"]) {
          --ui-bg:#14181b; --ui-surface:#1b2024; --ui-surface-2:#232a2f; --ui-text:#aeb8be; --ui-text-dim:#7e898f; --ui-text-faint:#626c72; --ui-border:#2a3137; --ui-border-2:#232a2f; --ui-border-strong:#38424a; --ui-hover:#232a30; --ui-accent:#3f7fb5; --ui-accent-hover:#4d8cc2; --ui-accent-text:#0a0e11; --ui-shadow:rgba(0,0,0,.6);
          --ownship-fill:#b04632; --ownship-edge:#3a120c; --ownship-halo:#e6c6a0;
          --ais-fill:#3fae84; --ais-halo:#d9ece3; --ais-danger:#ff7a6a; }
        /* Full-bleed map; everything else floats over it. */
        #map { position:absolute; inset:0; }
        #map chart-canvas { width:100%; height:100%; }
        /* Chart radar: edge chips pointing at off-screen installed charts. The
           overlay is click-through; chips opt back into pointer events. */
        #chart-finder { position:absolute; inset:0; z-index:5; pointer-events:none; overflow:hidden; }
        .finder-chip { position:absolute; transform:translate(-50%,-50%); display:flex; align-items:center; gap:6px;
          padding:5px 9px 5px 7px; border-radius:999px; background:var(--ui-surface); color:var(--ui-text);
          border:1px solid var(--ui-border-strong); box-shadow:0 2px 8px var(--ui-shadow); cursor:pointer;
          font:600 12px/1 system-ui,sans-serif; white-space:nowrap; pointer-events:auto; user-select:none;
          max-width:42vw; transition:background .1s; }
        @media (hover:hover) { .finder-chip:hover { background:var(--ui-hover); } }
        .finder-chip .fc-arrow { flex:none; width:14px; height:14px; color:var(--ui-accent); }
        .finder-chip .fc-band { flex:none; width:8px; height:8px; border-radius:50%; box-shadow:0 0 0 1.5px var(--ui-surface); }
        .finder-chip .fc-name { overflow:hidden; text-overflow:ellipsis; }
        .finder-chip .fc-dist { flex:none; color:var(--ui-text-dim); font-weight:500; }
        .btn { cursor:pointer; border:1px solid var(--ui-border-strong); background:var(--ui-surface); border-radius:6px; padding:6px 10px; font:inherit; color:var(--ui-text); }
        @media (hover:hover) { .btn:hover { background:var(--ui-hover); } }
        /* Shared touch chrome: kill 300ms delay + double-tap zoom on controls,
           suppress long-press callout/selection. (Convention E) */
        .rbtn,.btn,.cta,.cc-btn,.pk-btn,.m-row,.cov-cell,.sb-band,.region-row,.sr-item,.finder-chip,.linkbtn,.add-clear {
          touch-action:manipulation; -webkit-touch-callout:none; -webkit-user-select:none; user-select:none; }
        /* Floating corner controls — a top-left group (search) and a top-right
           group (charts · scheme · settings). Each is a round button; the active
           section's button lights up while its panel is open. */
        #tl-controls, #tr-controls { position:absolute; top:calc(12px + env(safe-area-inset-top,0px)); z-index:7;
          display:flex; align-items:center; gap:8px; }
        #tl-controls { left:calc(12px + env(safe-area-inset-left,0px)); }
        #tr-controls { right:calc(12px + env(safe-area-inset-right,0px)); }
        /* Bottom-right cluster: charts · scheme · settings (compass stays top-right;
           NOAA attribution sits just above this row). */
        #br-controls { position:absolute; right:calc(12px + env(safe-area-inset-right,0px));
          bottom:calc(var(--botbar-h) + 12px); z-index:7; display:flex; align-items:center; gap:8px; }
        .rbtn { flex:none; width:44px; height:44px; border-radius:50%; cursor:pointer; padding:0;
          display:flex; align-items:center; justify-content:center; color:var(--ui-text);
          background:color-mix(in srgb, var(--ui-surface) 90%, transparent); border:1px solid var(--ui-border);
          box-shadow:0 2px 10px rgba(0,0,0,.18); backdrop-filter:blur(6px);
          transition:background .12s, color .12s, box-shadow .12s, transform .08s; }
        @media (hover:hover) { .rbtn:hover { color:var(--ui-accent); border-color:var(--ui-accent); box-shadow:0 3px 14px rgba(0,0,0,.24); } }
        .rbtn:active { transform:scale(.94); }
        .rbtn.on { background:var(--ui-accent); color:var(--ui-accent-text); border-color:var(--ui-accent); }
        .rbtn svg { width:21px; height:21px; display:block; }
        /* Prod / prebaked deployment: charts load from a configured hosted archive
           (pmtiles="…" / catalog="…"); there's no NOAA download and no Dev tools.
           The Charts button stays but its panel becomes import-only (drop your own
           ENC, baked server-side) — see renderCharts. */
        :host([prod]) #empty-add, :host([prod]) #empty .welcome-sub { display:none; }
        .box-sel { position:absolute; z-index:5; border:2px solid var(--ui-accent); background:rgba(21,101,192,.12); pointer-events:none; }
        /* charts panel: action header + "your charts" cards */
        .charts-actions { display:flex; gap:8px; margin-bottom:10px; }
        .cta { flex:1; background:var(--ui-accent); color:var(--ui-accent-text); border:none; border-radius:8px; padding:11px 12px; font:inherit;
          font-weight:600; cursor:pointer; display:inline-flex; align-items:center; justify-content:center; gap:7px; }
        @media (hover:hover) { .cta:hover { background:var(--ui-accent-hover); } }
        .cta svg { width:17px; height:17px; }
        .upd { display:inline-flex; align-items:center; gap:6px; white-space:nowrap; }
        .charts-summary { color:var(--ui-text-dim); font-size:12px; margin:0 0 12px; }
        .charts-empty { text-align:center; color:var(--ui-text-faint); padding:26px 10px; }
        .chart-card { display:flex; align-items:flex-start; gap:10px; padding:11px 0; border-bottom:1px solid var(--ui-border-2); }
        .chart-card .cc-dot { width:10px; height:10px; border-radius:3px; flex:none; margin-top:3px; }
        .chart-card .cc-main { flex:1; min-width:0; }
        .chart-card .cc-title { font-weight:600; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
        .chart-card .cc-meta { color:var(--ui-text-dim); font-size:12px; margin-top:1px; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
        .chart-card .cc-edition { font-size:12px; color:var(--ui-text-faint); margin-top:4px; display:flex; align-items:center; gap:7px; flex-wrap:wrap; }
        .chart-card .cc-actions { flex:none; display:flex; align-items:center; gap:4px; }
        .cc-btn { border:1px solid var(--ui-border-strong); background:var(--ui-surface); color:var(--ui-text-dim); border-radius:7px; width:40px; height:40px; cursor:pointer;
          font-size:14px; display:inline-flex; align-items:center; justify-content:center; }
        @media (pointer:coarse) { .cc-btn { width:var(--tap-min); height:var(--tap-min); } }
        .cc-btn:hover { background:var(--ui-hover); color:var(--ui-accent); border-color:#b9c0c8; }
        .cc-btn.cc-rm:hover { color:#c0392b; border-color:#e2b6b1; background:#fdeceb; }
        /* freshness pill */
        .fresh { font-size:10.5px; font-weight:600; padding:1px 8px; border-radius:10px; }
        .fresh.current { background:#e4f5ea; color:#1f7a36; }
        .fresh.aging { background:#fbf0d8; color:#8a6000; }
        .fresh.stale { background:#fbe3e1; color:#c0392b; }
        .chart-card.focus { background:var(--ui-hover); box-shadow:inset 3px 0 0 var(--ui-accent); }
        .chart-card.clickable { cursor:pointer; }
        .chart-card.clickable:hover { background:var(--ui-hover); }
        /* in-drawer "Add charts" view */
        .add-head { display:flex; align-items:center; justify-content:space-between; margin-bottom:6px; }
        .add-head strong { font-size:15px; }
        .add-hint { color:var(--ui-text-dim); font-size:12px; line-height:1.5; margin:0 0 12px; }
        .add-tools { display:flex; gap:8px; margin-bottom:4px; }
        .add-tools .tool { flex:1; border:1px solid var(--ui-border-strong); background:var(--ui-surface); border-radius:8px; padding:9px; font:inherit; font-size:13px; cursor:pointer; color:var(--ui-text); }
        .add-tools .tool:hover { background:var(--ui-hover); }
        .add-tools .tool.on { background:var(--ui-accent); color:var(--ui-accent-text); border-color:var(--ui-accent); }
        .add-sel { border-top:1px solid var(--ui-border-2); margin-top:14px; padding-top:14px; }
        .add-sel .empty { color:var(--ui-text-faint); font-size:13px; text-align:center; padding:6px 0; }
        .add-sel .sel-line { display:flex; align-items:center; justify-content:space-between; margin-bottom:10px; font-weight:600; }
        .add-clear { background:none; border:none; color:var(--ui-accent); cursor:pointer; font:inherit; }
        .linkbtn:disabled { color:var(--ui-text-faint); cursor:default; text-decoration:none; }
        .pack-search { width:100%; box-sizing:border-box; border:1px solid var(--ui-border-strong); border-radius:8px; padding:9px 12px; font:inherit; margin-bottom:10px; background:var(--ui-surface); color:var(--ui-text); }
        .pack-search:focus { outline:none; border-color:var(--ui-accent); }
        /* region browser */
        .region-list { display:flex; flex-direction:column; }
        .region-row { display:flex; align-items:center; gap:9px; width:100%; text-align:left;
          border:none; background:none; border-bottom:1px solid var(--ui-border-2); padding:13px 4px; min-height:var(--tap-min); box-sizing:border-box; font:inherit; cursor:pointer; }
        @media (hover:hover) { .region-row:hover { background:var(--ui-hover); } }
        .region-row .region-name { font-weight:600; color:var(--ui-text); flex:1; min-width:0; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
        .region-row .region-meta { flex:none; color:var(--ui-text-faint); font-size:12px; }
        .rdot { flex:none; width:9px; height:9px; border-radius:50%; box-shadow:inset 0 0 0 1.5px #c2c8cf; }
        .rdot.full { background:#1f9d4d; box-shadow:none; }
        .rdot.partial { background:#f0a500; box-shadow:none; }
        .rdot.none { background:transparent; }
        .region-empty { color:var(--ui-text-faint); text-align:center; padding:20px; }
        .region-title { margin:4px 0 2px; font-size:16px; }
        .region-status { background:#e4f5ea; color:#1f7a36; font-weight:600; font-size:12.5px; padding:6px 10px; border-radius:8px; margin:2px 0 4px; }
        .linkbtn { background:none; border:none; color:var(--ui-accent); cursor:pointer; font:inherit; padding:8px 0; min-height:var(--tap-min); display:block; }
        .linkbtn.danger { color:#c0392b; }
        /* Job activity is signalled by the top notification pill; the dlspin
           keyframes drive its spinner. */
        @keyframes dlspin { to { transform:rotate(360deg); } }
        /* The ECDIS cursor-pick report (S-52 PresLib §10.8) is its own element,
           <pick-report> — see pick-report.mjs (styled via the inherited --ui-* tokens). */
        /* chart info pill (map popup when focusing a chart from the list) */
        .chart-pill { font:13px/1.4 system-ui,sans-serif; min-width:170px; }
        .chart-pill .cp-title { font-weight:600; margin-bottom:2px; }
        .chart-pill .cp-meta { color:var(--ui-text-dim); font-size:12px; }
        .chart-pill .cp-ed { margin-top:5px; display:flex; align-items:center; gap:6px; flex-wrap:wrap; font-size:12px; color:var(--ui-text-dim); }
        /* settings */
        .set-section { margin:0 0 28px; }
        .set-section > h3 { font-size:11px; text-transform:uppercase; letter-spacing:.05em; color:var(--ui-text-faint); margin:0 0 6px; padding-bottom:6px; border-bottom:1px solid var(--ui-border-2); font-weight:700; }
        /* chart download: Finder-style 3-pane drill-down */
        .miller { display:flex; align-items:stretch; border:1px solid var(--ui-border-2); border-radius:10px; overflow:hidden; min-height:300px; max-height:min(62vh,560px); max-height:min(62dvh,560px); margin:2px 0 12px; }
        .mcol { flex:0 0 26%; min-width:0; overflow-y:auto; overscroll-behavior:contain; -webkit-overflow-scrolling:touch; border-right:1px solid var(--ui-border-2); padding:6px; }
        .mcol:nth-child(2) { flex:0 0 32%; }
        .mcol.mcol-detail { flex:1 1 0; border-right:none; padding:12px; }
        .mcol-h { font-size:11px; font-weight:700; color:var(--ui-text); padding:1px 6px 0; }
        .mcol-head { position:sticky; top:0; background:var(--ui-surface); padding:4px 0 7px; margin-bottom:2px; border-bottom:1px solid var(--ui-border-2); z-index:1; }
        .mcol-meta { font-size:10.5px; color:var(--ui-text-faint); padding:1px 6px 0; line-height:1.35; }
        .m-row { display:flex; align-items:center; gap:8px; padding:8px; border-radius:7px; cursor:pointer; transition:background .1s; }
        @media (hover:hover) { .m-row:hover { background:var(--ui-hover); } }
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
        /* detail pane — real OSM preview map with the pack's coverage outlined */
        .prev-map { width:100%; height:260px; border:1px solid var(--ui-border-2); border-radius:8px; background:var(--ui-surface-2); overflow:hidden; }
        .prev-map canvas { border-radius:8px; }
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
        /* find-a-chart search results */
        .pkr-row { display:flex; align-items:center; gap:10px; padding:9px 4px; border-bottom:1px solid var(--ui-border-2); cursor:pointer; }
        .pkr-row:last-child { border-bottom:none; }
        .pkr-row:hover { background:var(--ui-hover); }
        .pkr-info { flex:1; min-width:0; display:flex; flex-direction:column; }
        .pkr-title { font-weight:600; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
        .pkr-sub { color:var(--ui-text-faint); font-size:12px; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
        .pkr-have { flex:none; color:#1f7a36; font-size:12px; font-weight:600; }
        .pkr-dl { flex:none; padding:7px 11px; }
        .pkr-empty { color:var(--ui-text-faint); font-size:13px; text-align:center; padding:14px 0; }
        /* NOAA data freshness footer */
        .data-fresh { color:var(--ui-text-faint); font-size:11.5px; text-align:center; line-height:1.5; padding:14px 0 4px; border-top:1px solid var(--ui-border-2); margin-top:4px; }
        /* The settings-row + control look (.set-row/.switch/.seg/.unit) moved into
           <settings-dialog>'s shadow (settings-dialog.view.mjs STYLE). */
        /* NOAA attribution + "not for navigation" — subtle one-line text tucked
           into the bottom-right corner (no box), kept legible over the chart with a
           soft halo in the current surface colour. */
        #noaa-attr { position:absolute; right:calc(12px + env(safe-area-inset-right,0px)); bottom:calc(var(--botbar-h) + 64px); z-index:5; pointer-events:auto;
          font:500 10px/1.35 system-ui,sans-serif; letter-spacing:.01em; white-space:nowrap; text-align:right;
          color:var(--ui-text-dim);
          text-shadow:0 0 3px var(--ui-surface), 0 0 3px var(--ui-surface), 0 1px 1px var(--ui-surface); }
        #noaa-attr a, #noaa-attr .attr-link { color:inherit; text-shadow:inherit; cursor:pointer;
          text-decoration:underline; text-decoration-color:var(--ui-text-faint); text-underline-offset:2px; }
        #noaa-attr a:hover, #noaa-attr .attr-link:hover { color:var(--ui-accent); }
        #noaa-attr .attr-link { background:none; border:none; padding:0; font:inherit; }
        /* NOAA ENC user-agreement gate (shown before the first download). */
        .modal { position:absolute; inset:0; z-index:30; display:flex; align-items:center; justify-content:center;
          background:rgba(15,20,26,.55); backdrop-filter:blur(2px); }
        .modal[hidden] { display:none; }
        .modal-card { background:var(--ui-surface); max-width:520px; width:calc(100% - 40px);
          max-height:86%;
          max-height:calc(100dvh - var(--sa-top) - var(--sa-bottom) - 32px);
          display:flex; flex-direction:column; overflow:hidden;
          border-radius:12px; padding:20px 22px; box-shadow:0 12px 40px rgba(0,0,0,.3); font:14px/1.5 system-ui,sans-serif; color:var(--ui-text); }
        .modal-card h2 { margin:0 0 10px; font-size:18px; flex:none; }
        .modal-card .agree-body { overflow:auto; overscroll-behavior:contain; -webkit-overflow-scrolling:touch; flex:1 1 auto; min-height:0; }
        .modal-card .agree-body ul { margin:8px 0; padding-left:20px; }
        .modal-card .agree-body li { margin:5px 0; }
        .modal-card a { color:var(--ui-accent); }
        /* Sticky non-scrolling footer so Agree/Cancel stay on-screen. */
        .agree-actions { flex:none; display:flex; gap:10px; justify-content:flex-end; margin-top:16px; padding-top:12px; }
        /* Live band·scale·zoom readout (left of the statusbar), one line. Each
           field has a fixed width + tabular figures so the bar never reflows. */
        .ins-lock { background:var(--ui-surface-2); color:var(--ui-text-dim); border-radius:6px; padding:6px 9px; margin-bottom:10px; font-size:12px; }
        .ins-cycler { display:flex; align-items:center; justify-content:center; gap:10px; margin-bottom:10px; font-size:12px; color:var(--ui-text-dim); }
        .ins-cycler .btn { padding:2px 9px; line-height:1.3; }
        /* Bottom-centre DATA CARD — adopts the surface look of the old sidebar: a
           solid rounded card pinned to the bottom middle. Holds ONLY the live nav
           readout (band · scale · zoom · position). Purely presentational — no
           buttons, no transient status (activity lives in the notification pill). */
        #databox { position:absolute; left:50%; bottom:calc(var(--botbar-h) + 14px);
          transform:translateX(-50%); z-index:6; box-sizing:border-box;
          display:flex; flex-direction:column; align-items:center; gap:6px; padding:8px 14px;
          width:min(94vw, 420px);
          background:color-mix(in srgb, var(--ui-surface) 92%, transparent); border:1px solid var(--ui-border);
          border-radius:13px; backdrop-filter:blur(7px); overflow:hidden;
          box-shadow:0 4px 18px rgba(0,0,0,.18);
          font:11px system-ui,sans-serif; color:var(--ui-text); }
        #databox[hidden] { display:none; }
        /* Live band·scale·zoom·position readout — fixed-width fields + tabular
           figures so panning/zooming never reflows the card. The card width is
           FIXED (above) so it never grows/shrinks as the message changes; the
           overscale chip wraps to its own centred line rather than widening it. */
        .db-readout { display:flex; align-items:center; width:100%; justify-content:center; }
        .db-readout .hud-main { display:flex; align-items:center; justify-content:center; flex-wrap:wrap; gap:6px; row-gap:5px;
          font-weight:600; font-size:12px; white-space:nowrap; font-variant-numeric:tabular-nums; }
        .db-readout .hud-dot { width:8px; height:8px; border-radius:50%; flex:none; box-shadow:0 0 0 2px rgba(255,255,255,.6); margin-right:1px; }
        .db-readout .hud-band { display:inline-block; width:56px; }
        .db-readout .hud-scale { display:inline-block; width:74px; color:var(--ui-accent); }
        .db-readout .hud-z { display:inline-block; width:40px; color:var(--ui-text-dim); }
        .db-readout .hud-coord { display:inline-block; width:150px; color:var(--ui-text-dim); }
        .db-readout .hud-sep { color:var(--ui-text-faint); }
        /* Overscale indication (S-52 §10.1.10.1) — a full-width amber band filling
           the bottom of the card (its own warning notification area). Negative
           margins cancel the card's padding so it reaches the rounded edges; the
           card's overflow:hidden clips it to the bottom corners. */
        .db-warn { box-sizing:border-box; width:calc(100% + 28px); margin:1px -14px -8px; padding:5px 12px;
          display:flex; align-items:center; justify-content:center; gap:5px;
          background:#e8820c; color:#fff; font:700 11.5px/1.25 system-ui,sans-serif; text-align:center; }
        .db-warn[hidden] { display:none; }
        .db-warn-ico { width:14px; height:14px; flex:none; }
        /* Cell-list popup above a band pill (hover on desktop; tap to pin on touch). */
        .band-pop { display:none; position:absolute; bottom:calc(100% + 6px); right:0; z-index:10;
          background:var(--ui-surface); border:1px solid rgba(0,0,0,.1); border-radius:9px; padding:8px 9px;
          box-shadow:0 6px 22px rgba(0,0,0,.22); width:max-content; max-width:280px; }
        @media (hover:hover) {
          .band-pop::before { content:""; position:absolute; left:0; right:0; bottom:-6px; height:6px; } /* hover bridge over the gap */
          .sb-band-wrap:hover .band-pop, .band-pop:hover { display:block; }
        }
        .sb-band-wrap.open .band-pop { display:block; } /* tap-pin path works on touch */
        .band-pop-h { font:600 11px/1.3 system-ui,sans-serif; color:var(--ui-text-dim); margin-bottom:6px; }
        .band-pop-cells { display:flex; flex-wrap:wrap; gap:4px; max-height:210px; overflow-y:auto; overscroll-behavior:contain; -webkit-overflow-scrolling:touch; }
        .cov-cell { font:11px/1.6 ui-monospace,SFMono-Regular,Menlo,monospace; padding:7px 9px; border-radius:5px;
          display:inline-flex; align-items:center; min-height:36px; box-sizing:border-box;
          border:1px solid rgba(0,0,0,.12); background:var(--ui-surface-2); color:var(--ui-text); cursor:pointer; }
        @media (hover:hover) { .cov-cell:hover { border-color:var(--ui-accent); color:var(--ui-accent); } }
        .cov-cell.missing { background:repeating-linear-gradient(45deg,var(--ui-surface),var(--ui-surface) 4px,var(--ui-surface-2) 4px,var(--ui-surface-2) 8px);
          color:var(--ui-text-faint); border-style:dashed; }
        .cov-cell.missing::after { content:" ↓"; color:var(--ui-accent); }
        .cov-cell.missing:hover { color:var(--ui-accent); border-color:var(--ui-accent); }
        .cov-empty { font:12px system-ui,sans-serif; color:var(--ui-text-faint); }
        #loading { position:absolute; top:calc(12px + var(--sa-top)); left:50%; transform:translateX(-50%); z-index:5; background:rgba(0,0,0,.72);
          color:#fff; border-radius:14px; padding:5px 12px; font-size:12px; box-shadow:0 1px 4px rgba(0,0,0,.3); }
        /* Panels are dialog popovers that DROP DOWN from their corner button, with a
           caret on the top edge pointing back up at it. The Charts/Settings panel
           anchors top-right; search anchors top-left. The caret's horizontal offset
           (--caret-left) is set in JS to the originating button's centre. Pops in
           with a fade+scale from the caret edge; fully hidden when closed. */
        /* --panel-bottom: viewport-bottom space reserved for the data card so a
           panel never covers it (the card is bottom-centre, panels drop from the
           top — capping their height keeps the two from overlapping). */
        #drawer, #search { --caret:9px; --ctrl-top:calc(64px + env(safe-area-inset-top,0px));
          --panel-bottom:calc(var(--botbar-h) + 92px); }
        /* NB: no overflow:hidden on the popover itself — it would clip the caret.
           Inner scroll areas (.body / #search-results) round their own corners. */
        #drawer { position:absolute; right:calc(12px + env(safe-area-inset-right,0px)); bottom:calc(var(--botbar-h) + 66px);
          width:min(440px, calc(100vw - 24px)); max-height:calc(100dvh - var(--botbar-h) - 90px); z-index:9;
          background:var(--ui-bg); color:var(--ui-text); border:1px solid var(--ui-border); border-radius:14px;
          box-shadow:0 12px 38px rgba(0,0,0,.30); display:flex; flex-direction:column;
          transform-origin:bottom right; transform:translateY(6px) scale(.97); opacity:0; visibility:hidden;
          transition:opacity .15s ease, transform .15s ease, visibility 0s linear .15s; }
        #drawer.open { opacity:1; transform:none; visibility:visible; transition:opacity .15s ease, transform .15s ease; }
        #drawer.wide { width:min(86vw, 940px); } /* charts: two-pane list + map */
        #drawer.set-wide { width:min(520px, calc(100vw - 24px)); } /* settings: rail + content */
        #drawer.wide .miller { height:calc(100dvh - var(--botbar-h) - 208px); max-height:none; }
        #drawer .body { border-radius:0 0 13px 13px; }
        /* caret on the TOP edge, pointing up at the button above */
        #search::after { content:""; position:absolute; top:calc(-1 * var(--caret)); left:var(--caret-left,50%); transform:translateX(-50%);
          width:0; height:0; border-left:var(--caret) solid transparent; border-right:var(--caret) solid transparent;
          border-bottom:var(--caret) solid var(--ui-surface); filter:drop-shadow(0 -2px 1px rgba(0,0,0,.08)); }
        /* Charts/Settings panel pops UP from the bottom-right cluster: caret on the
           BOTTOM edge, pointing down at the button below. */
        #drawer::after { content:""; position:absolute; bottom:calc(-1 * var(--caret)); left:var(--caret-left,50%); transform:translateX(-50%);
          width:0; height:0; border-left:var(--caret) solid transparent; border-right:var(--caret) solid transparent;
          border-top:var(--caret) solid var(--ui-bg); filter:drop-shadow(0 2px 1px rgba(0,0,0,.08)); }
        /* The settings panel + its control look (toggle/segmented/number/select)
           now live in <settings-dialog> (settings-dialog.view.mjs STYLE); the
           Advanced-tab dev tools (rebake + feature inspector) carry their own CSS in
           dev-tools.view.mjs and render into the dialog's shadow. Nothing dev-side
           remains in the shell sheet. */
        .dhead { display:flex; align-items:center; gap:8px; padding:10px 12px; border-bottom:1px solid var(--ui-border); }
        .dhead strong { flex:1; font-size:14px; }
        .body { overflow:auto; overscroll-behavior:contain; -webkit-overflow-scrolling:touch; padding:14px 16px; flex:1; }
        .panel { display:none; } .panel.sel { display:block; }
        .drop { border:2px dashed var(--ui-border-strong); border-radius:8px; padding:18px; text-align:center; color:var(--ui-text-dim); margin-bottom:10px; }
        .drop.over { border-color:var(--ui-accent); background:var(--ui-hover); color:var(--ui-accent); }
        .row { display:flex; align-items:center; gap:8px; padding:4px 0; border-bottom:1px solid var(--ui-border-2); }
        .row .name { font-weight:600; } .row .meta { color:var(--ui-text-dim); font-size:12px; }
        .grow { flex:1; }
        .muted { color:var(--ui-text-dim); }
        label.fld { display:block; margin:8px 0; }
        label.fld span { display:inline-block; min-width:135px; }
        input[type=number] { width:64px; }
        /* Top-centre notification pill — non-blocking "work happening" indicator.
           Hidden unless a job is active; "lights up" (pulsing ring + spinner) while
           busy; click drops a detail panel. */
        /* Job-progress row inside the bottom status card — sits ABOVE the nav
           readout, separated by a hairline divider. One width with the card so the
           label can run full-width; the percentage is pinned right. */
        .db-prog { width:100%; box-sizing:border-box; display:flex; flex-direction:column; gap:6px;
          padding-bottom:7px; margin-bottom:1px; border-bottom:1px solid var(--ui-border); }
        .db-prog[hidden] { display:none; }
        .db-prog-head { display:flex; align-items:center; gap:8px; font:600 12px/1.2 system-ui,sans-serif; color:var(--ui-text); }
        .db-prog-label { flex:1; min-width:0; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
        .db-prog-pct { flex:none; font-weight:700; color:var(--ui-accent); font-variant-numeric:tabular-nums; }
        .db-prog.error .db-prog-pct { color:#c0392b; }
        .db-prog.error .db-prog-label { color:#c0392b; }
        /* Spinner shows only while actively working (not on the done/error frame). */
        .db-prog-spin { width:13px; height:13px; flex:none; border-radius:50%; display:none;
          border:2px solid color-mix(in srgb, var(--ui-accent) 30%, transparent); border-top-color:var(--ui-accent);
          animation:dlspin .8s linear infinite; }
        .db-prog.busy .db-prog-spin { display:inline-block; }
        .db-prog-track { position:relative; width:100%; height:5px; border-radius:3px; overflow:hidden; background:var(--ui-surface-2); }
        .db-prog-fill { position:absolute; left:0; top:0; bottom:0; width:0; border-radius:3px; background:var(--ui-accent); transition:width .25s ease; }
        .db-prog.error .db-prog-fill { background:#c0392b; }
        /* Indeterminate (no known fraction): a sweeping segment instead of a fill. */
        .db-prog-fill.indet { width:35% !important; animation:db-sweep 1.1s ease-in-out infinite; }
        @keyframes db-sweep { 0% { left:-35%; } 100% { left:100%; } }
        @media (prefers-reduced-motion: reduce) { .db-prog-spin { animation-duration:2s; } .db-prog-fill.indet { animation:none; left:0; width:100% !important; } }
        /* NotificationCenter banners: a bottom-stacked toast list (non-task messages). */
        #toasts { position:absolute; left:50%; bottom:calc(var(--botbar-h) + 14px); transform:translateX(-50%); z-index:9; display:flex; flex-direction:column; gap:8px; align-items:center; pointer-events:none; }
        .toast { pointer-events:auto; max-width:80vw; padding:9px 14px; border-radius:8px; font:600 12.5px/1.3 system-ui,sans-serif; color:var(--ui-text); background:var(--ui-surface); border:1px solid var(--ui-border-2); box-shadow:0 4px 16px rgba(0,0,0,.28); opacity:1; transition:opacity .3s ease, transform .3s ease; }
        .toast.warn { border-color:#c0922f; } .toast.error { border-color:#c0392b; color:#e06b5c; }
        .toast.out { opacity:0; transform:translateY(6px); }
        progress { width:100%; height:8px; -webkit-appearance:none; appearance:none; border:none; border-radius:5px; overflow:hidden; background:var(--ui-surface-2); }
        progress::-webkit-progress-bar { background:var(--ui-surface-2); border-radius:5px; }
        progress::-webkit-progress-value { background:var(--ui-accent); border-radius:5px; }
        progress::-moz-progress-bar { background:var(--ui-accent); border-radius:5px; }
        /* collapsible "import from a file" */
        .import-more { margin-top:18px; border-top:1px solid var(--ui-border-2); padding-top:6px; }
        .import-more > summary { cursor:pointer; color:var(--ui-text-dim); font-weight:500; padding:6px 0; list-style:none; }
        .import-more > summary::-webkit-details-marker { display:none; }
        .import-more > summary:before { content:"▸ "; color:var(--ui-text-faint); }
        .import-more[open] > summary:before { content:"▾ "; }
        .legend { display:flex; gap:12px; font-size:12px; margin-bottom:10px; flex-wrap:wrap; }
        .legend i { display:inline-block; width:11px; height:11px; border-radius:2px; margin-right:4px; vertical-align:-1px; }
        #empty { position:absolute; inset:0 0 var(--botbar-h) 0; display:flex; align-items:center; justify-content:center; z-index:4; pointer-events:none; }
        #empty[hidden] { display:none; }
        #empty .card { pointer-events:auto; background:var(--ui-surface); color:var(--ui-text); border-radius:16px; padding:30px 30px 24px; max-width:360px;
          text-align:center; box-shadow:0 8px 34px rgba(0,0,0,.22); }
        #empty .welcome-mark { width:44px; height:44px; margin-bottom:10px; }
        #empty h2 { margin:0 0 8px; font-size:21px; }
        #empty p { color:var(--ui-text-dim); margin:0 0 18px; line-height:1.5; }
        #empty .welcome-cta { display:inline-flex; align-items:center; gap:8px; width:auto; padding:11px 22px; font-size:15px; }
        #empty .welcome-sub { margin-top:12px; font-size:13px; color:var(--ui-text-faint); }
        #empty .linkbtn { background:none; border:none; color:var(--ui-accent); cursor:pointer; font:inherit; padding:4px 0; min-height:var(--tap-min); display:inline-flex; align-items:center; text-decoration:underline; }
        #search[hidden] { display:block; } /* defeat UA hidden so the popover can fade out (base styles keep it invisible/non-interactive) */
        /* Search: same caret-popover as the panels — a dialog card with the input
           on top and results filling in underneath, dropping from the search button
           at the top-left. */
        #search { position:absolute; left:calc(12px + env(safe-area-inset-left,0px)); right:auto; top:var(--ctrl-top); z-index:8; width:min(360px, calc(100vw - 24px));
          background:var(--ui-surface); border:1px solid var(--ui-border); border-radius:14px;
          box-shadow:0 12px 38px rgba(0,0,0,.30);
          transform-origin:top left; transform:translateY(-6px) scale(.97); opacity:0; visibility:hidden;
          transition:opacity .15s ease, transform .15s ease, visibility 0s linear .15s; }
        #search:not([hidden]) { opacity:1; transform:none; visibility:visible; transition:opacity .15s ease, transform .15s ease; }
        #search input { width:100%; box-sizing:border-box; border:none; border-radius:14px; padding:11px 16px;
          font:inherit; font-size:16px; background:transparent; color:var(--ui-text); outline:none; }
        #search-results { border-top:1px solid var(--ui-border-2); max-height:min(360px, calc(100dvh - var(--ctrl-top) - var(--panel-bottom) - 52px)); overflow-y:auto; overscroll-behavior:contain; -webkit-overflow-scrolling:touch; border-radius:0 0 13px 13px; }
        #search-results[hidden] { display:none; }
        .sr-item { padding:8px 16px; min-height:var(--tap-min); box-sizing:border-box; display:flex; flex-direction:column; justify-content:center; cursor:pointer; border-bottom:1px solid var(--ui-border-2); }
        .sr-item:last-child { border-bottom:none; }
        .sr-item:hover, .sr-item.sel { background:var(--ui-hover); }
        .sr-item .t { font-weight:600; } .sr-item .s { color:var(--ui-text-faint); font-size:12px; }
        /* Subtle "loading more while data is shown" cue: a hairline indeterminate
           bar riding the top edge of the viewport. Opacity-controlled (always in
           DOM) so it fades in/out; the slide animation runs continuously. */
        .load-bar { position:absolute; top:0; left:0; right:0; height:3px; z-index:25; pointer-events:none; overflow:hidden;
          opacity:0; transition:opacity .2s ease; background:rgba(13,71,161,.3); }
        .load-bar.on { opacity:1; }
        .load-bar::before { content:""; position:absolute; top:0; height:100%; width:40%;
          background:linear-gradient(90deg, transparent, #0d47a1 45%, #0d47a1 55%, transparent);
          box-shadow:0 0 8px rgba(13,71,161,.7); animation:load-slide 1.1s ease-in-out infinite; }
        :host([data-scheme="night"]) .load-bar, :host([data-scheme="dusk"]) .load-bar { background:rgba(90,155,216,.22); }
        :host([data-scheme="night"]) .load-bar::before, :host([data-scheme="dusk"]) .load-bar::before {
          background:linear-gradient(90deg, transparent, #6aaef0 45%, #6aaef0 55%, transparent); box-shadow:0 0 8px rgba(106,174,240,.6); }
        @keyframes load-slide { 0% { left:-40%; } 100% { left:100%; } }
        /* ---- Phone (base): popover content reflow --------------------------
           Chart packs go one-per-row. (Settings rows wrap their control inside
           <settings-dialog>'s own responsive STYLE now.) */
        @media (max-width: 640px) {
          #empty .card { max-width:min(360px, calc(100vw - 48px)); }
          .pack-grid { grid-template-columns:1fr; }
        }
        /* ---- Phone reflow (<=560px): collapse the Finder-style 3-pane chart
           download (region · pack · detail) into a single stacked column so each
           pane is usable full-width. iPad portrait (>=768px) keeps the 3-pane. */
        @media (max-width: 560px) {
          #drawer.wide { width:min(96vw, calc(100vw - 16px)); }
          .miller { flex-direction:column; max-height:none;
            height:calc(100dvh - var(--botbar-h) - 230px); min-height:0; }
          #drawer.wide .miller { height:calc(100dvh - var(--botbar-h) - 200px); }
          /* Drill-down: <chart-library> shows ONE column at a time, driven by its
             own shadow STYLE + .miller[data-level]. These shell rules only set the
             column sizing; visibility is owned by the component (see
             chart-library.view.mjs). */
          .mcol { flex:1 1 auto; width:100%; border-right:none; border-bottom:none; }
          .mcol:nth-child(2) { flex:1 1 auto; }
          .mcol.mcol-detail { flex:1 1 auto; border-bottom:none; }
          /* The bottom-centre data card and the bottom-right button cluster sit at
             the same height and collide on a narrow screen. Lift the card ABOVE the
             44px button row, and move the NOAA attribution to the (free) bottom-left
             corner so the three no longer overlap. */
          #databox { width:min(96vw, 420px); bottom:calc(var(--botbar-h) + 64px); }
          /* Attribution can't share the bottom row (scale bar left · buttons right ·
             card centred all collide), so float it on its own centred line just
             above the data card. */
          #noaa-attr { left:50%; right:auto; transform:translateX(-50%); text-align:center;
            bottom:calc(var(--botbar-h) + 112px); max-width:96vw; }
        }
        /* On a narrow phone, drop the zoom from the readout so the band·scale·
           position line never runs past the card edge (scale is what matters). */
        @media (max-width: 430px) {
          .db-readout .hud-z, .db-readout .hud-scale + .hud-sep { display:none; }
        }
`;

export const CHROME = `
      <div id="map"></div>
      <!-- Off-screen installed-chart pointers ("chart radar"): edge chips pointing
           toward enabled chart packs that aren't currently in view. Overlay is
           click-through; only the chips themselves take pointer events. -->
      <div id="chart-finder" aria-hidden="true"></div>
      <div id="load-bar" class="load-bar" aria-hidden="true"></div>
      <!-- The map is the UI. Chrome is reduced to four round buttons floating in
           the corners — search alone top-left; charts · scheme · settings top-right
           — plus a read-only data card pinned to the bottom centre. -->
      <div id="tl-controls" class="ctrl-group">
        <button class="rbtn" id="search-tab" type="button" title="Search charts &amp; features" aria-label="Search">${SEARCH_ICON}</button>
      </div>
      <!-- Top-right holds the orientation compass only (mounted at runtime). -->
      <div id="tr-controls" class="ctrl-group"></div>
      <!-- Charts · scheme · settings relocated to a bottom-right cluster. -->
      <div id="br-controls" class="ctrl-group">
        <button class="rbtn" id="charts-btn" type="button" title="Get &amp; manage charts" aria-label="Charts">${CHART_ICON}</button>
        <button class="rbtn" id="scheme-toggle" type="button" title="Colour scheme — tap to cycle Day · Dusk · Night" aria-label="Colour scheme">
          <svg id="scheme-svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"></svg>
        </button>
        <button class="rbtn" id="settings-btn" type="button" title="Settings" aria-label="Settings">${SETTINGS_ICON}</button>
      </div>
      <!-- NotificationCenter banner stack (non-task messages: failures, alerts). -->
      <div id="toasts"></div>
      <!-- Bottom-centre status card — the SINGLE surface for both the live nav
           readout (band · scale · zoom · position · overscale, always shown) and
           job progress (download / import / bake), which grows a row above the
           readout while a job runs. Driven by _updateHud + _setNotification. -->
      <div id="databox" hidden>
        <div id="db-prog" class="db-prog" hidden>
          <div class="db-prog-head">
            <span class="db-prog-spin"></span>
            <span id="db-prog-label" class="db-prog-label"></span>
            <span id="db-prog-pct" class="db-prog-pct"></span>
          </div>
          <div class="db-prog-track"><span id="db-prog-fill" class="db-prog-fill"></span></div>
        </div>
        <span id="cov-readout" class="db-readout"></span>
        <div id="db-warn" class="db-warn" hidden></div>
      </div>
      <div id="search" hidden><input id="search-input" type="search" placeholder="Search charts & features…" autocomplete="off" spellcheck="false"><div id="search-results" hidden></div></div>
      <div id="noaa-attr"><a href="${NOAA_ENC_URL}" target="_blank" rel="noopener">NOAA ENC®</a> · <button id="attr-terms" class="attr-link" type="button">Terms</button> · not for navigation</div>
      <!-- The NOAA ENC User Agreement modal moved into <chart-library> (it owns the
           download flow); the "Terms" link reaches into it. -->
      <div id="empty" hidden><div class="card">
        <svg class="welcome-mark" viewBox="0 0 24 24" fill="none" stroke="#1565c0" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="5" r="2"/><path d="M12 7v14M5 12a7 7 0 0 0 14 0M3 12h2m14 0h2M12 21a7 7 0 0 1-5-2m10 0a7 7 0 0 1-5 2"/></svg>
        <h2>Welcome aboard</h2>
        <p>No charts yet. Pick a cruising region and download a pack — official NOAA charts are fetched and baked right here on your machine, ready to use offline.</p>
        <button id="empty-add" class="cta welcome-cta">⚓ Browse chart regions</button>
        <div class="welcome-sub">or <button id="empty-import" class="linkbtn">import from a file</button></div>
      </div></div>
      <div id="drawer">
        <div class="dhead"><strong id="dtitle">Chart library</strong><button id="close" class="btn">✕</button></div>
        <div class="body">
          <div class="panel sel" data-panel="charts">
            <!-- Job progress lives in the bottom data card + top notification pill,
                 not in the drawer. The whole charts UI is <chart-library>. -->
            <chart-library id="chart-lib"></chart-library>
          </div>
          <div class="panel" data-panel="settings">
            <!-- The settings panel is a HOST: it renders the registry's
                 contributions, including the Advanced-tab dev tools (DevTools'
                 render() escape hatch) — no shell-side dev region any more. -->
            <settings-dialog id="settings-dlg"></settings-dialog>
          </div>
        </div>
      </div>
`;
