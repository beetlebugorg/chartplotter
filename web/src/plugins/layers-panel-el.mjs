// <layers-panel> — the element rendered into the Layers settings tab. It lists the
// LayerRegistry's overlays grouped by source, each with a switch, and re-renders when
// the registry changes (an overlay registers/unregisters or is toggled elsewhere).

const STYLE = `
  :host { display:block; color:var(--ui-text,#e6edf3); font-size:13px; }
  .empty { color:var(--ui-text-dim,#8b949e); padding:18px 4px; text-align:center; }
  .group { margin-bottom:14px; }
  .group h4 { margin:0 0 8px; font-size:11px; text-transform:uppercase; letter-spacing:.04em; color:var(--ui-text-dim,#8b949e); }
  .row { display:flex; align-items:center; gap:10px; padding:9px 11px; border:1px solid var(--ui-border,#30363d);
         border-radius:9px; margin-bottom:8px; background:var(--ui-surface,#161b22); }
  .row .t { flex:1; min-width:0; font-weight:600; }
  .switch { position:relative; width:38px; height:22px; flex:none; }
  .switch input { opacity:0; width:0; height:0; }
  .sl { position:absolute; inset:0; background:var(--ui-surface-2,#30363d); border-radius:22px; cursor:pointer; transition:.15s; }
  .sl:before { content:""; position:absolute; width:16px; height:16px; left:3px; top:3px; background:#fff; border-radius:50%; transition:.15s; box-shadow:0 1px 2px rgba(0,0,0,.3); }
  input:checked + .sl { background:var(--ui-accent,#2f81f7); }
  input:checked + .sl:before { transform:translateX(16px); }
`;

export class LayersPanel extends HTMLElement {
  constructor() {
    super();
    if (!this.shadowRoot) this.attachShadow({ mode: "open" });
    this._layers = null;
    this._off = null;
  }

  configure({ layers }) {
    this._layers = layers;
  }

  connectedCallback() {
    this.shadowRoot.innerHTML = `<style>${STYLE}</style><div id="root"></div>`;
    this._root = this.shadowRoot.getElementById("root");
    this._render();
    if (this._layers) this._off = this._layers.onChange(() => this._render());
  }

  disconnectedCallback() {
    if (this._off) this._off();
    this._off = null;
  }

  _render() {
    const list = this._layers ? this._layers.list() : [];
    if (!list.length) {
      this._root.innerHTML = `<div class="empty">No map overlays yet.</div>`;
      return;
    }
    // Group by `group`, preserving first-seen order.
    const groups = [];
    const byName = new Map();
    for (const l of list) {
      if (!byName.has(l.group)) {
        byName.set(l.group, { name: l.group, items: [] });
        groups.push(byName.get(l.group));
      }
      byName.get(l.group).items.push(l);
    }
    this._root.innerHTML = groups
      .map(
        (g) => `<div class="group"><h4>${esc(g.name)}</h4>${g.items
          .map(
            (l) => `<div class="row"><span class="t">${esc(l.title)}</span>
              <label class="switch"><input type="checkbox" data-id="${esc(l.id)}" ${l.visible ? "checked" : ""}><span class="sl"></span></label></div>`,
          )
          .join("")}</div>`,
      )
      .join("");
    for (const cb of this._root.querySelectorAll("input[data-id]")) {
      cb.addEventListener("change", () => this._layers.setVisible(cb.dataset.id, cb.checked));
    }
  }
}

function esc(s) {
  return String(s).replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]));
}

customElements.define("layers-panel", LayersPanel);
