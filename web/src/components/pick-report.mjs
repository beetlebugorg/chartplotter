// <pick-report> — the ECDIS cursor-pick report panel (S-52 PresLib §10.8).
//
// A self-contained, draggable floating panel. The shell (<chart-plotter-app>)
// gathers the feature stack under a tapped point and hands it here; this element
// owns the decode (full names, enumerated values, units, dates, the category-C
// administrative split) and all panel behaviour. It:
//   • renders the report from the baked S-57 attribute blob + the catalogue,
//   • lets the mariner drag it anywhere (grab the header),
//   • auto-places itself in the screen corner farthest from the picked point so
//     it stays out of the way of what was just tapped,
//   • emits "pick-feature" (the displayed feature, for the map highlight) and
//     "pick-close" so the shell can sync the map.
//
// Public API: setCatalogue(cat) · setUnits(prefs) · show(feats, anchor{x,y}) · hide().

import { convertHeight, convertDistance, convertSpeed, unitSuffix, M_TO_FT } from "../lib/units.mjs";

// Attributes whose numeric value carries a physical unit we let the mariner pick.
// Each maps to a category whose canonical source unit matches the ENC encoding:
// heights/clearances are metres, VALNMR is nautical miles, CURVEL is knots, and
// the depth attributes are metres (shown in the depth unit). Everything else keeps
// the catalogue's own unit string.
const PICK_HEIGHT_ATTRS = new Set(["HEIGHT", "ELEVAT", "VERCLR", "VERCCL", "VERCOP", "VERCSA"]);
const PICK_DEPTH_ATTRS = new Set(["VALSOU", "VALDCO", "DRVAL1", "DRVAL2"]);
const PICK_DIST_ATTRS = new Set(["VALNMR"]);
const PICK_SPEED_ATTRS = new Set(["CURVEL"]);

// Format a converted number compactly + its unit suffix.
function fmtUnitNum(v, unit) {
  if (!isFinite(v)) return "";
  const dec = Math.abs(v) >= 100 ? 0 : Math.abs(v) >= 10 ? 1 : 2;
  let s = v.toFixed(dec);
  if (s.includes(".")) s = s.replace(/\.?0+$/, "");
  return s + " " + unitSuffix(unit);
}

// S-57 date attributes; rendered "DD-MMM-YYYY" (PresLib §10.8 rule 6).
const PICK_DATE_ATTRS = new Set(["SORDAT", "RECDAT", "DATSTA", "DATEND", "PERSTA", "PEREND", "SURSTA", "SUREND"]);
// Attributes whose value is an external filename (shipped in the aux zip): the
// textual descriptions and the pictorial representation. Resolved to inline
// content from the AuxStore when one is loaded; otherwise shown as the filename.
const PICK_TEXT_ATTRS = new Set(["TXTDSC", "NTXTDS"]);
const PICK_PIC_ATTRS = new Set(["PICREP"]);
const PICK_MONTHS = ["JAN", "FEB", "MAR", "APR", "MAY", "JUN", "JUL", "AUG", "SEP", "OCT", "NOV", "DEC"];

function esc(s) {
  return String(s).replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]));
}

// Format an S-57 date value (YYYYMMDD / YYYYMM / YYYY) as the spec form (rule 6).
function fmtS57Date(v) {
  const s = String(v).trim();
  const m = /^(\d{4})(\d{2})?(\d{2})?$/.exec(s);
  if (!m) return s;
  const [, y, mo, d] = m;
  if (d && mo) return `${d}-${PICK_MONTHS[+mo - 1] || mo}-${y}`;
  if (mo) return `${PICK_MONTHS[+mo - 1] || mo}-${y}`;
  return y;
}

// Does an S-57 list/enumerated value (comma-separated) contain `id`?
function attrHas(raw, id) {
  return raw != null && String(raw).split(",").map((s) => s.trim()).includes(id);
}

// SORDAT is administrative (category C, normally withheld) but PresLib §10.8 rule 5
// requires it shown — even with admin attributes hidden — for these objects.
function sordatException(cls, attrs) {
  if (["WRECKS", "OBSTRN", "UWTROC", "SWPARE"].includes(cls)) return true;
  if (cls === "SOUNDG" && attrHas(attrs.QUASOU, "9")) return true;
  if (cls === "DRGARE" && attrHas(attrs.QUASOU, "11")) return true;
  return attrHas(attrs.CONDTN, "1") || attrHas(attrs.CONDTN, "3") || attrHas(attrs.CONDTN, "5");
}

const STYLE = `
  :host { position:absolute; z-index:8; width:340px; max-width:calc(100% - 24px);
    display:flex; flex-direction:column; background:var(--ui-surface,#fff); color:var(--ui-text,#2a2f35);
    border:1px solid var(--ui-border,#e2e2e2); border-radius:12px; box-shadow:0 8px 30px var(--ui-shadow,rgba(0,0,0,.22));
    overflow:hidden; font:13px/1.45 system-ui,sans-serif; }
  :host([hidden]) { display:none; }
  /* Consistent 16px horizontal gutter throughout; even vertical rhythm. */
  .head { display:flex; align-items:center; gap:10px; padding:13px 16px; border-bottom:1px solid var(--ui-border-2,#ededed);
    cursor:grab; touch-action:none; user-select:none; }
  .head.drag { cursor:grabbing; }
  .grip { flex:none; color:var(--ui-text-faint,#9aa0a8); font-size:14px; line-height:1; letter-spacing:-1px; }
  .title { flex:1; min-width:0; font-weight:600; font-size:14.5px; line-height:1.3; }
  .acr { font-size:10.5px; font-weight:600; color:var(--ui-text-faint,#9aa0a8); letter-spacing:.05em; }
  .title .acr { margin-left:8px; }
  .x { flex:none; border:none; background:none; color:var(--ui-text-dim,#7a828b); cursor:pointer; font-size:17px; line-height:1;
    padding:3px 5px; border-radius:7px; margin:-3px -5px -3px 0; }
  .x:hover { background:var(--ui-hover,#f0f3f6); color:var(--ui-text,#2a2f35); }
  .name { padding:13px 16px 0; font-size:13.5px; color:var(--ui-accent,#1565c0); font-weight:600; line-height:1.3; }
  .meta { display:flex; align-items:center; flex-wrap:wrap; gap:10px; padding:11px 16px; }
  .cell { font-size:12px; color:var(--ui-text-dim,#7a828b); }
  .cyc { display:inline-flex; align-items:center; gap:8px; margin-left:auto; }
  .nav { border:1px solid var(--ui-border-strong,#cfcfcf); background:var(--ui-surface,#fff); color:var(--ui-text,#2a2f35);
    border-radius:7px; cursor:pointer; width:26px; height:24px; font-size:11px; padding:0; display:inline-flex; align-items:center; justify-content:center; }
  .nav:hover { background:var(--ui-hover,#f0f3f6); }
  .count { font-size:12px; color:var(--ui-text-dim,#7a828b); min-width:46px; text-align:center; font-variant-numeric:tabular-nums; }
  /* Stacked attribute rows: a dim label line (full name + acronym) over the value,
     so long names and long values each get the full width and the rhythm stays even. */
  .kv { overflow:auto; padding:4px 0; display:flex; flex-direction:column; }
  .row { padding:7px 16px; }
  .row + .row { border-top:1px solid var(--ui-border-2,#ededed); }
  .k { color:var(--ui-text-dim,#7a828b); font-size:11.5px; line-height:1.3; margin-bottom:3px; }
  .k .acr { margin-left:7px; }
  .v { font-size:13.5px; line-height:1.35; word-break:break-word; }
  .empty { color:var(--ui-text-faint,#9aa0a8); font-size:12.5px; padding:8px 16px 12px; }
  /* Aux content (TXTDSC text / PICREP picture) resolved from the companion zip. */
  .aux-text { white-space:pre-wrap; font-size:12.5px; line-height:1.4; }
  .aux-img { display:block; max-width:100%; height:auto; margin-top:2px; border-radius:6px; border:1px solid var(--ui-border-2,#ededed); }
  .aux-pending { color:var(--ui-text-faint,#9aa0a8); }
  .admin { margin:8px 16px 16px; align-self:flex-start; border:1px solid var(--ui-border-strong,#cfcfcf);
    background:var(--ui-surface,#fff); color:var(--ui-text-dim,#7a828b); border-radius:8px; padding:7px 13px;
    font:inherit; font-size:12px; cursor:pointer; }
  .admin:hover { background:var(--ui-hover,#f0f3f6); color:var(--ui-text,#2a2f35); }
`;

export class PickReport extends HTMLElement {
  constructor() {
    super();
    if (!this.shadowRoot) this.attachShadow({ mode: "open" }); // guard double-upgrade
    this._cat = { classes: {}, attributes: {} };
    this._feats = [];
    this._idx = 0;
    this._admin = false;
    this._userPos = null; // {left,top} once the mariner drags it; cleared on close
    this._aux = null;     // AuxStore for TXTDSC/PICREP external files (optional)
    this._renderSeq = 0;  // guards async aux fills against a newer render
    // NB: a custom-element constructor must not set attributes (incl. `hidden`) —
    // the spec forbids it ("result must not have attributes"). Hide in connectedCallback.
  }

  connectedCallback() {
    this.hidden = true;
    this.shadowRoot.innerHTML = `<style>${STYLE}</style>
      <div class="head" part="head">
        <span class="grip" aria-hidden="true">⠿</span>
        <div class="title" id="title"></div>
        <button class="x" id="close" title="Close" type="button">✕</button>
      </div>
      <div id="name"></div>
      <div id="meta" class="meta"></div>
      <div class="kv" id="kv"></div>
      <div id="adminWrap"></div>`;
    const $ = (id) => this.shadowRoot.getElementById(id);
    $("close").onclick = () => this.hide();
    this._initDrag(this.shadowRoot.querySelector(".head"));
    // Delegate clicks for the dynamic nav / admin controls.
    this.shadowRoot.addEventListener("click", (e) => {
      const b = e.target.closest("button");
      if (!b) return;
      if (b.id === "prev") this._step(-1);
      else if (b.id === "next") this._step(1);
      else if (b.id === "admin") { this._admin = !this._admin; this._render(); }
    });
    // Escape closes the report (captured so it pre-empts other shortcuts).
    this._onKey = (e) => { if (e.key === "Escape" && !this.hidden) { e.stopPropagation(); this.hide(); } };
    window.addEventListener("keydown", this._onKey, true);
  }

  disconnectedCallback() {
    if (this._onKey) window.removeEventListener("keydown", this._onKey, true);
  }

  setCatalogue(cat) { if (cat) this._cat = cat; }
  // Mariner display-unit preferences (depthUnit/heightUnit/distanceUnit/…), so
  // height/depth/range/speed attributes render in the chosen unit. See units.mjs.
  setUnits(prefs) { this._units = prefs || null; }

  // If `acr` is a unit-bearing attribute, return {to, fn} to convert its canonical
  // numeric value into the mariner's chosen unit; else null (keep catalogue unit).
  _unitConv(acr) {
    const u = this._units;
    if (!u) return null;
    if (PICK_HEIGHT_ATTRS.has(acr)) return { to: u.heightUnit || "m", fn: convertHeight };
    if (PICK_DEPTH_ATTRS.has(acr)) return { to: u.depthUnit || "ft", fn: (v, unit) => (unit === "ft" ? v * M_TO_FT : v) };
    if (PICK_DIST_ATTRS.has(acr)) return { to: u.distanceUnit || "NM", fn: convertDistance };
    if (PICK_SPEED_ATTRS.has(acr)) return { to: u.speedUnit || "kn", fn: convertSpeed };
    return null;
  }

  // Provide the AuxStore so TXTDSC/PICREP filenames resolve to inline text/picture.
  // Optional: without it (or for a feature whose file isn't in the set) the report
  // falls back to showing the raw filename.
  setAux(aux) { this._aux = aux || null; if (!this.hidden) this._render(); }

  // Show the report for a feature stack; `anchor` is the picked point {x,y} in
  // viewport pixels, used for out-of-the-way auto-placement.
  show(feats, anchor) {
    this._feats = feats || [];
    this._idx = 0;
    this._admin = false;
    this._userPos = null;
    if (!this._feats.length) { this.hide(); return; }
    this.hidden = false;
    this._render();
    this._place(anchor);
    this._emitFeature();
  }

  hide() {
    if (this.hidden) return;
    this.hidden = true;
    this._feats = [];
    this._userPos = null;
    this.dispatchEvent(new CustomEvent("pick-close"));
  }

  _step(d) {
    const n = this._feats.length;
    if (!n) return;
    this._idx = (this._idx + d + n) % n;
    this._render();
    this._emitFeature();
  }

  _emitFeature() {
    const f = this._feats[this._idx];
    if (f) this.dispatchEvent(new CustomEvent("pick-feature", { detail: { feature: f } }));
  }

  _render() {
    const cat = this._cat;
    const seq = ++this._renderSeq;
    const f = this._feats[Math.min(this._idx, this._feats.length - 1)];
    if (!f) return;
    const p = f.properties || {};
    const cls = p.class || "";
    const $ = (id) => this.shadowRoot.getElementById(id);

    const title = esc((cat.classes && cat.classes[cls]) || cls || "Feature");
    $("title").innerHTML = `${title}${cls ? `<span class="acr">${esc(cls)}</span>` : ""}`;

    let attrs = {};
    if (p.s57) { try { attrs = JSON.parse(p.s57); } catch { attrs = {}; } }
    $("name").innerHTML = attrs.OBJNAM ? `<div class="name">${esc(attrs.OBJNAM)}</div>` : "";

    const rows = [];
    let adminTotal = 0, adminHidden = 0;
    for (const acr of Object.keys(attrs).sort()) {
      if (acr === "OBJNAM") continue; // shown as the subtitle
      const meta = cat.attributes && cat.attributes[acr];
      const isAdmin = !!(meta && meta.admin);
      if (isAdmin) adminTotal++;
      const show = !isAdmin || this._admin || (acr === "SORDAT" && sordatException(cls, attrs));
      if (!show) { adminHidden++; continue; }
      rows.push(this._row(acr, attrs[acr], meta));
    }
    $("kv").innerHTML = rows.length ? rows.join("") : `<div class="empty">No attributes encoded.</div>`;
    this._fillAux(seq); // resolve any TXTDSC/PICREP rows from the aux zip

    const n = this._feats.length;
    const cyc = n > 1
      ? `<span class="cyc"><button id="prev" class="nav" title="Previous" type="button">◀</button>`
        + `<span class="count">${this._idx + 1} / ${n}</span>`
        + `<button id="next" class="nav" title="Next" type="button">▶</button></span>`
      : "";
    const cell = p.cell ? `<span class="cell" title="Source ENC cell">▦ ${esc(p.cell)}</span>` : "";
    $("meta").innerHTML = cell + cyc;
    $("meta").style.display = (cell || cyc) ? "" : "none";

    $("adminWrap").innerHTML = adminTotal
      ? `<button id="admin" class="admin" type="button">${this._admin ? "Hide" : "Show"} administrative${this._admin ? "" : ` (${adminHidden})`}</button>`
      : "";
  }

  // One decoded attribute row: full name + acronym; value with enumerated names
  // (rule 2), units (rule 4) and dates as DD-MMM-YYYY (rule 6). Numbers arrive
  // unpadded from the tile (rule 3). Unknown attributes still show, by acronym (§10.8.6).
  _row(acr, raw, meta) {
    const name = esc((meta && meta.name) || acr);
    // External-file attributes: render the filename now, tag the value cell so
    // _fillAux can swap in the actual text/picture once the aux zip resolves it.
    const isText = PICK_TEXT_ATTRS.has(acr), isPic = PICK_PIC_ATTRS.has(acr);
    if (isText || isPic) {
      const ref = String(raw).trim();
      const tag = this._aux && this._aux.has(ref) ? ` data-aux="${esc(ref)}" data-auxkind="${isPic ? "image" : "text"}"` : "";
      return `<div class="row"><div class="k">${name}<span class="acr">${esc(acr)}</span></div><div class="v"${tag}>${esc(ref)}</div></div>`;
    }
    let val;
    if (PICK_DATE_ATTRS.has(acr)) {
      val = String(raw).split(",").map((s) => fmtS57Date(s.trim())).join("; ");
    } else if (meta && meta.values) {
      val = String(raw).split(",").map((s) => { const id = s.trim(); return meta.values[id] || id; }).join("; ");
    } else {
      const conv = this._unitConv(acr);
      if (conv) {
        // Convert each (possibly comma-listed) numeric into the mariner's unit.
        val = String(raw).split(",").map((s) => {
          const n = parseFloat(s);
          return isFinite(n) ? fmtUnitNum(conv.fn(n, conv.to), conv.to) : s.trim();
        }).join("; ");
      } else {
        val = String(raw).trim();
        if (meta && meta.unit) val += " " + meta.unit;
      }
    }
    return `<div class="row"><div class="k">${name}<span class="acr">${esc(acr)}</span></div><div class="v">${esc(val)}</div></div>`;
  }

  // Swap external-file filenames for their resolved content. Async (the aux zip
  // inflates on demand); a `seq` guard drops the result if a newer render (a step
  // or admin toggle) has since replaced the rows.
  async _fillAux(seq) {
    if (!this._aux) return;
    const nodes = this.shadowRoot.querySelectorAll("[data-aux]");
    for (const node of nodes) {
      const ref = node.getAttribute("data-aux");
      node.classList.add("aux-pending");
      const res = await this._aux.resolve(ref).catch(() => null);
      if (seq !== this._renderSeq) return; // a newer render owns the panel now
      node.classList.remove("aux-pending");
      if (!res) continue;
      if (res.type === "image") {
        node.innerHTML = `<img class="aux-img" src="${res.url}" alt="${esc(ref)}" loading="lazy">`;
      } else {
        node.innerHTML = `<div class="aux-text">${esc(res.text)}</div>`;
      }
    }
  }

  // --- placement & drag ----------------------------------------------------
  // Host (the chart-plotter-app) is position:relative, so left/top are relative
  // to it. Returns the host's content box for clamping.
  _frame() {
    const host = this.parentNode && this.parentNode.host;
    const r = host ? host.getBoundingClientRect() : { left: 0, top: 0, width: window.innerWidth, height: window.innerHeight };
    return { left: r.left, top: r.top, w: r.width, h: r.height };
  }

  // Place the panel right NEXT TO the picked point (a small gap to one side) so the
  // report reads as attached to what was tapped — unless the mariner has dragged it
  // (then keep that). Prefers the right of the point, flips left if it won't fit, and
  // centres vertically on the point; _apply clamps it inside the viewport.
  _place(anchor) {
    const fr = this._frame();
    const w = this.offsetWidth || 340, ht = this.offsetHeight || 240;
    const M = 12, botbar = 66, GAP = 16; // leave room above the bottom tab bar
    if (this._userPos) {
      this._apply(this._userPos.left, this._userPos.top, fr, w, ht, M, botbar);
      return;
    }
    // anchor is viewport-relative; convert to host-relative.
    const ax = anchor ? anchor.x - fr.left : fr.w / 2;
    const ay = anchor ? anchor.y - fr.top : fr.h / 2;
    let left = ax + GAP; // to the right of the point…
    if (left + w + M > fr.w) left = ax - GAP - w; // …unless it overflows → to the left
    const top = ay - ht / 2; // vertically centred on the point
    this._apply(left, top, fr, w, ht, M, botbar);
  }

  _apply(left, top, fr, w, ht, M, botbar) {
    fr = fr || this._frame();
    w = w || this.offsetWidth || 340;
    ht = ht || this.offsetHeight || 240;
    M = M || 12; botbar = botbar || 66;
    const maxLeft = Math.max(M, fr.w - w - M);
    const maxTop = Math.max(M, fr.h - ht - M - botbar);
    this.style.left = Math.min(Math.max(M, left), maxLeft) + "px";
    this.style.top = Math.min(Math.max(M, top), maxTop) + "px";
    this.style.right = "auto";
  }

  _initDrag(head) {
    let sx = 0, sy = 0, sl = 0, st = 0, dragging = false;
    head.addEventListener("pointerdown", (e) => {
      if (e.target.closest("button")) return; // let the close button work
      dragging = true;
      head.classList.add("drag");
      head.setPointerCapture(e.pointerId);
      sx = e.clientX; sy = e.clientY;
      const r = this.getBoundingClientRect(), fr = this._frame();
      sl = r.left - fr.left; st = r.top - fr.top;
      e.preventDefault();
    });
    head.addEventListener("pointermove", (e) => {
      if (!dragging) return;
      this._apply(sl + (e.clientX - sx), st + (e.clientY - sy));
    });
    const end = (e) => {
      if (!dragging) return;
      dragging = false;
      head.classList.remove("drag");
      try { head.releasePointerCapture(e.pointerId); } catch {}
      this._userPos = { left: parseFloat(this.style.left) || 0, top: parseFloat(this.style.top) || 0 };
    };
    head.addEventListener("pointerup", end);
    head.addEventListener("pointercancel", end);
  }
}

customElements.define("pick-report", PickReport);
