// <plugins-panel> — the Plugins settings UI. Lists installed plugins with live
// status badges (fed by the /api/plugins/stream SSE), an "Install plugin" upload,
// enable/disable, a per-plugin capability-grant editor, and remove. Mounted into the
// "Plugins" settings tab by PluginsController via the settings-dialog render(host)
// escape hatch.
//
// Status ticks patch each row's badge in place; structural changes (install / enable
// / grant edits / remove) re-render the list.

const STYLE = `
  :host { display: block; color: var(--ui-text, #e6edf3); font-size: 13px; }
  .bar { display: flex; justify-content: space-between; align-items: center; margin-bottom: 10px; gap: 10px; }
  .bar .hint { color: var(--ui-text-dim, #8b949e); font-size: 12px; }
  button {
    background: var(--ui-surface-2, #21262d); color: var(--ui-text, #e6edf3);
    border: 1px solid var(--ui-border, #30363d); border-radius: 6px;
    padding: 5px 10px; font: inherit; cursor: pointer;
    touch-action: manipulation; -webkit-user-select: none; user-select: none;
  }
  button:hover { background: var(--ui-hover, #30363d); }
  button.primary { background: var(--ui-accent, #2f81f7); color: var(--ui-accent-text, #fff); border-color: transparent; }
  button.danger { color: #f85149; }
  .empty { color: var(--ui-text-dim, #8b949e); padding: 18px 4px; text-align: center; }

  .row {
    display: flex; align-items: center; gap: 10px;
    padding: 9px 10px; border: 1px solid var(--ui-border, #30363d);
    border-radius: 8px; margin-bottom: 8px; background: var(--ui-surface, #161b22);
  }
  .dot { width: 10px; height: 10px; border-radius: 50%; flex: none; background: #6e7681; }
  .info { flex: 1; min-width: 0; }
  .name { font-weight: 600; display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
  .badge { font-size: 11px; font-weight: 500; color: var(--ui-text-dim, #8b949e); text-transform: uppercase; letter-spacing: .03em; }
  .tag { font-size: 10px; font-weight: 600; padding: 1px 6px; border-radius: 10px; background: var(--ui-surface-2,#21262d); color: var(--ui-text-dim,#8b949e); text-transform: uppercase; letter-spacing:.03em; }
  .meta { color: var(--ui-text-dim, #8b949e); font-size: 12px; margin-top: 2px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .actions { display: flex; align-items: center; gap: 8px; flex: none; }

  .grants { border: 1px solid var(--ui-border, #30363d); border-radius: 8px; padding: 10px 12px; margin: -4px 0 10px; background: var(--ui-bg, #0d1117); }
  .grants h5 { margin: 0 0 8px; font-size: 12px; color: var(--ui-text-dim,#8b949e); text-transform: uppercase; letter-spacing:.03em; }
  .cap { display: flex; align-items: flex-start; gap: 8px; margin-bottom: 6px; }
  .cap input { margin-top: 3px; }
  .cap .desc { color: var(--ui-text-dim, #8b949e); font-size: 12px; }
  .cap code { color: var(--ui-text, #e6edf3); }
  .grants-actions { display: flex; justify-content: flex-end; gap: 8px; margin-top: 8px; }
  .err { color: #f85149; font-size: 12px; margin-top: 6px; }
  input[type=file] { display: none; }
`;

// plugin status.State → [dot colour, badge label]
const BADGE = {
  running: ["#3fb950", "running"],
  enabled: ["#3fb950", "enabled"],
  degraded: ["#d29922", "degraded"],
  error: ["#f85149", "error"],
  disabled: ["#6e7681", "disabled"],
};

// Plain-language descriptions of what each capability grants (for the grant editor).
const CAP_DESC = {
  "vessel.read": "Read live vessel state (position, heading, speed…).",
  "vessel.write": "Publish vessel data (position/heading/…) into the shared state.",
  "ais.read": "Read AIS targets.",
  "ais.write": "Publish AIS target updates.",
  serial: "Open serial devices (host-owned).",
  "net.tcp-client": "Make outbound TCP connections (host-dialed, allowlisted).",
  "net.udp": "Send/receive UDP (host-mediated).",
  "net.http": "Make outbound HTTP requests to allowlisted origins.",
  storage: "A private key/value + blob store.",
  notify: "Post to the notification centre.",
  "http.register": "Serve routes under /plugins/<id>/api/.",
  "ui.settings": "Add a settings panel.",
  "ui.panel": "Add a side panel.",
  "ui.map-layer": "Draw a map layer.",
  "ui.hud": "Add a HUD widget.",
};

export class PluginsPanel extends HTMLElement {
  constructor() {
    super();
    if (!this.shadowRoot) this.attachShadow({ mode: "open" });
    this._service = null;
    this._notify = null;
    this._plugins = []; // [{record, manifest, status, running}]
    this._grantsOpen = null; // id whose grant editor is open
    this._stop = null;
    this._err = "";
  }

  configure({ service, notify }) {
    this._service = service;
    this._notify = notify;
  }

  connectedCallback() {
    this.shadowRoot.innerHTML = `<style>${STYLE}</style><div id="root"></div>`;
    this._root = this.shadowRoot.getElementById("root");
    this._load();
    // Live updates: patch badges when only status changed, else re-render.
    this._stop = this._service.stream((plugins) => this._onStream(plugins));
  }

  disconnectedCallback() {
    if (this._stop) this._stop();
    this._stop = null;
  }

  async _load() {
    try {
      this._plugins = await this._service.list();
      this._err = "";
    } catch (e) {
      this._err = e.message || String(e);
    }
    this._render();
  }

  _onStream(plugins) {
    const sameStructure = structureSig(plugins) === structureSig(this._plugins);
    this._plugins = plugins;
    if (sameStructure && !this._grantsOpen) {
      this._patchBadges();
    } else {
      this._render();
    }
  }

  _render() {
    const rows = this._plugins.map((p) => this._rowHTML(p)).join("");
    this._root.innerHTML = `
      <div class="bar">
        <span class="hint">Plugins add data sources, map layers, and panels. Installed plugins are trusted.</span>
        <button class="primary" id="install">Install plugin…</button>
      </div>
      <input type="file" id="file" accept=".zip">
      ${this._err ? `<div class="err">${esc(this._err)}</div>` : ""}
      ${this._plugins.length ? rows : `<div class="empty">No plugins installed.</div>`}
    `;
    this._root.querySelector("#install").onclick = () => this._root.querySelector("#file").click();
    this._root.querySelector("#file").onchange = (e) => this._install(e.target.files[0]);
    for (const el of this._root.querySelectorAll("[data-act]")) {
      el.onclick = () => this._action(el.dataset.act, el.dataset.id);
    }
    if (this._grantsOpen) this._renderGrants(this._grantsOpen);
  }

  _rowHTML(p) {
    const id = p.record.id;
    const man = p.manifest || {};
    const name = man.name || id;
    const state = p.record.enabled ? (p.status && p.status.state) || "enabled" : "disabled";
    const [color, label] = BADGE[state] || ["#6e7681", state];
    const tier = man.entry && man.entry.wasm ? "wasm" : man.entry && man.entry.native ? "native" : "ui";
    const detail = (p.status && p.status.detail) || "";
    const toggle = p.record.enabled ? "Disable" : "Enable";
    return `
      <div class="row" data-row="${esc(id)}">
        <span class="dot" style="background:${color}"></span>
        <div class="info">
          <div class="name">${esc(name)} <span class="badge">${esc(label)}</span> <span class="tag">${tier}</span></div>
          <div class="meta">${esc(id)} · v${esc(man.version || p.record.version || "?")}${detail ? " · " + esc(detail) : ""}</div>
        </div>
        <div class="actions">
          ${man.capabilities && man.capabilities.length ? `<button data-act="grants" data-id="${esc(id)}">Grants</button>` : ""}
          <button data-act="toggle" data-id="${esc(id)}">${toggle}</button>
          <button class="danger" data-act="remove" data-id="${esc(id)}">Remove</button>
        </div>
      </div>`;
  }

  _patchBadges() {
    for (const p of this._plugins) {
      const row = this._root.querySelector(`[data-row="${cssEsc(p.record.id)}"]`);
      if (!row) return this._render(); // structure drifted; full render
      const state = p.record.enabled ? (p.status && p.status.state) || "enabled" : "disabled";
      const [color, label] = BADGE[state] || ["#6e7681", state];
      row.querySelector(".dot").style.background = color;
      const badge = row.querySelector(".badge");
      if (badge) badge.textContent = label;
    }
  }

  async _action(act, id) {
    try {
      if (act === "toggle") {
        const p = this._plugins.find((x) => x.record.id === id);
        await (p && p.record.enabled ? this._service.disable(id) : this._service.enable(id));
        await this._load();
      } else if (act === "remove") {
        if (!confirm(`Remove plugin ${id}?`)) return;
        await this._service.remove(id, false);
        this._grantsOpen = null;
        await this._load();
      } else if (act === "grants") {
        this._grantsOpen = this._grantsOpen === id ? null : id;
        this._render();
      }
    } catch (e) {
      this._err = e.message || String(e);
      this._render();
      if (this._notify) this._notify.error && this._notify.error(this._err);
    }
  }

  // _renderGrants injects the grant editor beneath the open plugin's row.
  _renderGrants(id) {
    const p = this._plugins.find((x) => x.record.id === id);
    const row = this._root.querySelector(`[data-row="${cssEsc(id)}"]`);
    if (!p || !row) return;
    const caps = (p.manifest && p.manifest.capabilities) || [];
    const granted = new Set((p.record.grants || []).map((g) => g.cap));
    const box = document.createElement("div");
    box.className = "grants";
    box.innerHTML = `
      <h5>Capabilities</h5>
      ${caps
        .map(
          (c, i) => `
        <label class="cap">
          <input type="checkbox" data-cap="${i}" ${granted.has(c.cap) ? "checked" : ""}>
          <span><code>${esc(c.cap)}</code>${c.hosts ? ` <span class="desc">(${esc(c.hosts.join(", "))})</span>` : ""}
          <div class="desc">${esc(CAP_DESC[c.cap] || "")}</div></span>
        </label>`,
        )
        .join("")}
      <div class="grants-actions">
        <button data-g="cancel">Cancel</button>
        <button class="primary" data-g="save">Save grants</button>
      </div>`;
    row.after(box);
    box.querySelector('[data-g="cancel"]').onclick = () => { this._grantsOpen = null; this._render(); };
    box.querySelector('[data-g="save"]').onclick = async () => {
      const chosen = [];
      box.querySelectorAll("input[data-cap]").forEach((cb) => {
        if (cb.checked) chosen.push(caps[Number(cb.dataset.cap)]);
      });
      try {
        await this._service.setGrants(id, chosen, null);
        this._grantsOpen = null;
        await this._load();
      } catch (e) {
        this._err = e.message || String(e);
        this._render();
      }
    };
  }

  async _install(file) {
    if (!file) return;
    try {
      const man = await this._service.install(file);
      this._err = "";
      if (this._notify && this._notify.info) this._notify.info(`Installed ${man.name || man.id}. Review its capabilities, then enable it.`);
      await this._load();
    } catch (e) {
      this._err = e.message || String(e);
      this._render();
    }
  }
}

// structureSig is a signature of everything that requires a re-render (not status).
function structureSig(plugins) {
  return JSON.stringify(
    plugins.map((p) => [p.record.id, p.record.version, p.record.enabled, (p.record.grants || []).map((g) => g.cap)]),
  );
}

function esc(s) {
  return String(s).replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]));
}

// cssEsc escapes a plugin id for use inside an attribute selector.
function cssEsc(s) {
  return String(s).replace(/["\\]/g, "\\$&");
}

customElements.define("plugins-panel", PluginsPanel);
