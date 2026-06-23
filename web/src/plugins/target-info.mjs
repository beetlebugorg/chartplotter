// <target-info> — a small popover shown when own-ship or an AIS target is tapped.
// It's a dumb presenter: own-ship/AIS build a {title, subtitle, rows[[k,v]]}
// payload and call show() with the tap's screen point; this positions + renders.
// Dismissed by its close button, Escape, or a map grab (the shell calls hide()).

const STYLE = `
  :host { position: absolute; inset: 0; z-index: 8; pointer-events: none; }
  .card {
    position: absolute; min-width: 196px; max-width: 280px; pointer-events: auto;
    background: var(--ui-bg, #fff); color: var(--ui-text, #222);
    border: 1px solid var(--ui-border, #ddd); border-radius: 12px;
    box-shadow: 0 12px 32px var(--ui-shadow, rgba(0,0,0,.28));
    font: 13px/1.4 system-ui, sans-serif; overflow: hidden;
  }
  .head { display: flex; align-items: baseline; gap: 8px; padding: 9px 10px 8px 12px;
    border-bottom: 1px solid var(--ui-border, #eee); }
  .title { font-weight: 600; flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .sub { color: var(--ui-text-dim, #888); font-size: 11px; font-weight: 500; }
  .close { flex: none; cursor: pointer; border: none; background: none; color: var(--ui-text-dim, #888);
    font-size: 16px; line-height: 1; padding: 0; margin: -6px -4px -6px 0;
    min-width: var(--tap-min, 44px); min-height: var(--tap-min, 44px);
    display: inline-flex; align-items: center; justify-content: center; border-radius: 8px;
    touch-action: manipulation; -webkit-touch-callout: none; -webkit-user-select: none; user-select: none; }
  .close:hover { color: var(--ui-accent, #1565c0); }
  .close:active { background: var(--ui-hover, rgba(0,0,0,.08)); color: var(--ui-accent, #1565c0); }
  .rows { padding: 8px 12px 10px; display: grid; grid-template-columns: auto 1fr; gap: 3px 14px; }
  .k { color: var(--ui-text-dim, #888); white-space: nowrap; }
  .v { text-align: right; font-variant-numeric: tabular-nums; }
  :host([hidden]) { display: none; }
`;

export class TargetInfo extends HTMLElement {
  constructor() {
    super();
    if (!this.shadowRoot) this.attachShadow({ mode: "open" });
    this._onKey = (e) => {
      if (e.key === "Escape") this.hide();
    };
  }

  connectedCallback() {
    this.hidden = true;
    this.shadowRoot.innerHTML = `<style>${STYLE}</style><div class="card" id="card" hidden>
      <div class="head"><span class="title" id="ti-title"></span><span class="sub" id="ti-sub"></span>
        <button class="close" id="ti-close" aria-label="Close">✕</button></div>
      <div class="rows" id="ti-rows"></div></div>`;
    this.shadowRoot.getElementById("ti-close").onclick = () => this.hide();
    window.addEventListener("keydown", this._onKey, true);
  }

  disconnectedCallback() {
    window.removeEventListener("keydown", this._onKey, true);
  }

  // info: { title, subtitle, rows: [[label, value], …], x, y } (x/y = viewport px)
  show(info) {
    const $ = (id) => this.shadowRoot.getElementById(id);
    $("ti-title").textContent = info.title || "";
    $("ti-sub").textContent = info.subtitle || "";
    $("ti-rows").innerHTML = (info.rows || [])
      .map(([k, v]) => `<div class="k">${esc(k)}</div><div class="v">${esc(v)}</div>`)
      .join("");
    const card = $("card");
    card.hidden = false;
    this.hidden = false;
    // Position near the tap, clamped to stay on screen — and clear of the notch /
    // rounded corners / home indicator / bottom tab bar via the safe-area tokens.
    const sa = this._insets();
    const M = 8;
    const minX = M + sa.left, minY = M + sa.top;
    const r = this.getBoundingClientRect();
    const cw = card.offsetWidth, ch = card.offsetHeight;
    const maxRight = r.width - M - sa.right, maxBottom = r.height - M - sa.bottom;
    let x = (info.x ?? r.width / 2) + 14;
    let y = (info.y ?? r.height / 2) + 14;
    if (x + cw > maxRight) x = (info.x ?? 0) - cw - 14;
    if (y + ch > maxBottom) y = maxBottom - ch;
    card.style.left = Math.max(minX, x) + "px";
    card.style.top = Math.max(minY, y) + "px";
  }

  // Cascading safe-area + bottom-bar tokens (px) for clamping the card off the
  // notch / rounded corners / home indicator / bottom tab bar. --botbar-h already
  // includes env(safe-area-inset-bottom). Falls back to 0.
  _insets() {
    const px = (v) => { const n = parseFloat(v); return isFinite(n) ? n : 0; };
    const cs = getComputedStyle(this);
    return {
      top: px(cs.getPropertyValue("--sa-top")),
      right: px(cs.getPropertyValue("--sa-right")),
      bottom: px(cs.getPropertyValue("--botbar-h")),
      left: px(cs.getPropertyValue("--sa-left")),
    };
  }

  hide() {
    this.hidden = true;
    const card = this.shadowRoot && this.shadowRoot.getElementById("card");
    if (card) card.hidden = true;
  }
}

function esc(s) {
  return String(s).replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]));
}

// fmtLatLon formats a position as degrees-decimal-minutes (e.g. 48°52.74′N 002°22.0′E),
// the marine convention. Shared by the own-ship and AIS pickers.
export function fmtLatLon(lat, lon) {
  const dm = (v, deg, hemis) => {
    const h = hemis[v >= 0 ? 0 : 1];
    v = Math.abs(v);
    const d = Math.floor(v);
    return `${String(d).padStart(deg, "0")}°${((v - d) * 60).toFixed(2).padStart(5, "0")}′${h}`;
  };
  return `${dm(lat, 2, "NS")} ${dm(lon, 3, "EW")}`;
}

customElements.define("target-info", TargetInfo);
