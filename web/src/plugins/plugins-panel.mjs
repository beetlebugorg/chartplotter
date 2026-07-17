// <plugins-panel> — the Plugins settings UI. Lists installed plugins with live
// status badges (fed by the /api/plugins/stream SSE), an "Install plugin" upload,
// enable/disable, a per-plugin capability-grant editor, a per-plugin settings
// (config) editor, and remove. Mounted into the "Plugins" settings tab by
// PluginsController via the settings-dialog render(host) escape hatch.
//
// Status ticks patch each row's badge in place; structural changes (install / enable
// / grant or config edits / remove) re-render the list.
//
// Plugins that provide nmea.source DEFINE a connection type: their Configure button
// drills into a pushed <connections-panel> view (back returns to the list) — there
// is no separate Connections tab.

import "./connections-panel.mjs";
import { ConnectionsService } from "../data/connections-service.mjs";

const STYLE = `
  :host { display: block; color: var(--ui-text, #e6edf3); font-size: 13px; }
  .bar { display: flex; justify-content: space-between; align-items: center; margin-bottom: 14px; gap: 14px; }
  .bar .hint { color: var(--ui-text-dim, #8b949e); font-size: 12px; line-height: 1.5; flex: 1; min-width: 0; }
  button {
    background: var(--ui-surface-2, #21262d); color: var(--ui-text, #e6edf3);
    border: 1px solid var(--ui-border, #30363d); border-radius: 6px;
    padding: 6px 12px; font: inherit; cursor: pointer; white-space: nowrap;
    touch-action: manipulation; -webkit-user-select: none; user-select: none;
  }
  button:hover { background: var(--ui-hover, #30363d); }
  button.primary { background: var(--ui-accent, #2f81f7); color: var(--ui-accent-text, #fff); border-color: transparent; }
  button.danger { color: #f85149; }
  button.danger:hover { background: rgba(248,81,73,.12); }
  .empty { color: var(--ui-text-dim, #8b949e); padding: 26px 4px; text-align: center; border: 1px dashed var(--ui-border, #30363d); border-radius: 10px; }

  .row {
    display: flex; flex-direction: column; gap: 8px;
    padding: 12px 14px; border: 1px solid var(--ui-border, #30363d);
    border-radius: 10px; margin-bottom: 10px; background: var(--ui-surface, #161b22);
  }
  .row.open { border-bottom-left-radius: 0; border-bottom-right-radius: 0; margin-bottom: 0; }
  .row .head { display: flex; align-items: center; gap: 12px; min-width: 0; }
  .dot { width: 10px; height: 10px; border-radius: 50%; flex: none; background: #6e7681; }
  .info { flex: 1; min-width: 0; }
  .name { font-weight: 600; font-size: 13.5px; display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
  .badge { font-size: 11px; font-weight: 500; color: var(--ui-text-dim, #8b949e); text-transform: uppercase; letter-spacing: .03em; }
  /* The description spans the FULL row width under the header line — room to say
     what the plugin actually does instead of an ellipsised fragment. */
  .desc { color: var(--ui-text-dim, #8b949e); font-size: 12px; line-height: 1.5; max-width: 64ch; }
  .meta { color: var(--ui-text-faint, #6e7681); font-size: 11.5px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .actions { display: flex; align-items: center; gap: 7px; flex: none; flex-wrap: wrap; justify-content: flex-end; }

  .editor {
    border: 1px solid var(--ui-border, #30363d); border-top: none;
    border-radius: 0 0 10px 10px; padding: 14px 16px; margin: 0 0 10px;
    background: var(--ui-bg, #0d1117);
  }
  .editor h5 { margin: 0 0 12px; font-size: 12px; color: var(--ui-text-dim,#8b949e); text-transform: uppercase; letter-spacing:.03em; }
  .cap { display: flex; align-items: flex-start; gap: 9px; margin-bottom: 10px; }
  .cap input[type=checkbox] { margin-top: 3px; flex: none; }
  .cap .desc { color: var(--ui-text-dim, #8b949e); font-size: 12px; margin-top: 2px; }
  .cap code { color: var(--ui-text, #e6edf3); }

  .field { display: flex; align-items: center; gap: 12px; margin-bottom: 12px; }
  .field label { width: 110px; flex: none; color: var(--ui-text-dim, #8b949e); }
  .field .input { flex: 1; min-width: 0; }
  .field input[type=text], .field input[type=number], .field input[type=password], .field select, .editor textarea {
    width: 100%; box-sizing: border-box;
    background: var(--ui-surface, #161b22); color: var(--ui-text, #e6edf3);
    border: 1px solid var(--ui-border-strong, #444c56); border-radius: 6px; padding: 7px 9px; font: inherit;
    font-size: 16px; /* >=16px or iOS zooms on focus */
  }
  .editor textarea { min-height: 120px; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 13px; line-height: 1.5; white-space: pre; }
  .field .unit { color: var(--ui-text-dim, #8b949e); font-size: 12px; flex: none; }
  .switch { display: inline-flex; align-items: center; }
  .editor-actions { display: flex; justify-content: flex-end; gap: 8px; margin-top: 4px; }
  .err { color: #f85149; font-size: 12px; margin: 8px 0; }
  input[type=file] { display: none; }
  @media (pointer:coarse) { button { min-height: var(--tap-min, 44px); } }
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
    this._open = null; // { id, mode: "grants"|"config" }
    this._stop = null;
    this._err = "";
  }

  configure({ service, notify, assets }) {
    this._service = service;
    this._notify = notify;
    this._assets = assets || "/";
  }

  // A plugin that provides nmea.source defines a connection type — its Configure
  // drills into the connections view instead of a raw config form.
  _isSource(p) {
    return ((p.manifest && p.manifest.provides) || []).some((x) => x.service === "nmea.source");
  }

  connectedCallback() {
    this.shadowRoot.innerHTML = `<style>${STYLE}</style><div id="root"></div>`;
    this._root = this.shadowRoot.getElementById("root");
    this._load();
    // Poll (transient fetches) instead of SSE: the app is HTTP/1.1 with a 6-socket
    // budget per origin, and the core streams (vessel, AIS, plugin-host) already
    // hold three. A settings panel doesn't justify pinning a socket while open.
    this._stop = this._pollLoop(2000, async () => this._onStream(await this._service.list()));
  }

  disconnectedCallback() {
    if (this._stop) this._stop();
    this._stop = null;
  }

  _pollLoop(ms, fn) {
    const t = setInterval(() => { fn().catch(() => {}); }, ms);
    return () => clearInterval(t);
  }

  async _load() {
    this._render(); // paint the frame (with a loading hint) before data lands
    try {
      this._plugins = await this._service.list();
      this._err = "";
    } catch (e) {
      this._err = e.message || String(e);
    }
    this._loaded = true;
    this._render();
  }

  _onStream(plugins) {
    const same = structureSig(plugins) === structureSig(this._plugins);
    this._plugins = plugins;
    // Drill-down open: feed the child panel from THIS stream (it opens none of its
    // own — HTTP/1.1 socket budget) and leave the DOM alone; re-mounting the child
    // on every status tick would reset it mid-interaction.
    if (this._open && this._open.mode === "connections") {
      if (this._connPanel) this._connPanel.pluginsPush(plugins);
      return;
    }
    if (same && !this._open) this._patchBadges();
    else this._render();
  }

  _render() {
    // Connections drill-down: a plugin that defines a connection type pushes its
    // connections view over the list (back returns). The <connections-panel> is a
    // persistent element so its state (open form/sniffer) survives re-renders.
    if (this._open && this._open.mode === "connections") {
      const p = this._plugins.find((x) => x.record.id === this._open.id);
      const name = (p && p.manifest && p.manifest.name) || this._open.id;
      this._root.innerHTML = `
        <div class="bar">
          <button id="back">← Plugins</button>
          <span class="hint" style="text-align:right">${esc(name)} — connections</span>
        </div>
        <div id="conn-host"></div>`;
      this._root.querySelector("#back").onclick = () => { this._open = null; this._render(); };
      if (!this._connPanel) {
        this._connPanel = document.createElement("connections-panel");
        this._connPanel.configure({
          service: new ConnectionsService({ assets: this._assets }),
          notify: this._notify,
        });
      }
      this._connPanel.setPlugin(this._open.id);
      this._root.querySelector("#conn-host").appendChild(this._connPanel);
      return;
    }
    const rows = this._plugins.map((p) => this._rowHTML(p)).join("");
    this._root.innerHTML = `
      <div class="bar">
        <span class="hint">Plugins add data sources, map layers, and panels. Installed plugins run trusted — review their capabilities before enabling.</span>
        <button class="primary" id="install">Install plugin…</button>
      </div>
      <input type="file" id="file" accept=".zip">
      ${this._err ? `<div class="err">${esc(this._err)}</div>` : ""}
      ${this._plugins.length ? rows : `<div class="empty">${this._loaded ? "No plugins installed yet." : "Loading…"}</div>`}
    `;
    this._root.querySelector("#install").onclick = () => this._root.querySelector("#file").click();
    this._root.querySelector("#file").onchange = (e) => this._install(e.target.files[0]);
    for (const el of this._root.querySelectorAll("[data-act]")) {
      el.onclick = () => this._action(el.dataset.act, el.dataset.id);
    }
    if (this._open) this._renderEditor();
  }

  _rowHTML(p) {
    const id = p.record.id;
    const man = p.manifest || {};
    const name = man.name || id;
    const state = p.record.enabled ? (p.status && p.status.state) || "enabled" : "disabled";
    const [color, label] = BADGE[state] || ["#6e7681", state];
    const detail = (p.status && p.status.detail) || "";
    const toggle = p.record.enabled ? "Disable" : "Enable";
    const openHere = this._open && this._open.id === id;
    const hasCaps = man.capabilities && man.capabilities.length;
    return `
      <div class="row${openHere ? " open" : ""}" data-row="${esc(id)}">
        <div class="head">
          <span class="dot" style="background:${color}"></span>
          <div class="info">
            <div class="name">${esc(name)} <span class="badge">${esc(label)}</span></div>
          </div>
          <div class="actions">
            <button data-act="config" data-id="${esc(id)}">${this._isSource(p) ? "Connections" : "Configure"}</button>
            ${hasCaps ? `<button data-act="grants" data-id="${esc(id)}">Grants</button>` : ""}
            <button data-act="toggle" data-id="${esc(id)}">${toggle}</button>
            <button class="danger" data-act="remove" data-id="${esc(id)}">Remove</button>
          </div>
        </div>
        ${man.description ? `<div class="desc">${esc(man.description)}</div>` : ""}
        <div class="meta">${esc(id)} · v${esc(man.version || p.record.version || "?")}${detail ? " · " + esc(detail) : ""}</div>
      </div>`;
  }

  _patchBadges() {
    for (const p of this._plugins) {
      const row = this._root.querySelector(`[data-row="${cssEsc(p.record.id)}"]`);
      if (!row) return this._render();
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
        this._open = null;
        await this._load();
      } else if (act === "grants" || act === "config") {
        // Data-source plugins drill into their connections view; others get the
        // inline config/grants editor.
        const p = this._plugins.find((x) => x.record.id === id);
        const mode = act === "config" && p && this._isSource(p) ? "connections" : act;
        this._open = this._open && this._open.id === id && this._open.mode === mode ? null : { id, mode };
        this._render();
      }
    } catch (e) {
      this._err = e.message || String(e);
      this._render();
      if (this._notify && this._notify.error) this._notify.error(this._err);
    }
  }

  _renderEditor() {
    const p = this._plugins.find((x) => x.record.id === this._open.id);
    const row = this._root.querySelector(`[data-row="${cssEsc(this._open.id)}"]`);
    if (!p || !row) return;
    const box = document.createElement("div");
    box.className = "editor";
    if (this._open.mode === "grants") this._buildGrants(box, p);
    else this._buildConfig(box, p);
    row.after(box);
  }

  // --- grant editor ---
  _buildGrants(box, p) {
    const id = p.record.id;
    const caps = (p.manifest && p.manifest.capabilities) || [];
    const granted = new Set((p.record.grants || []).map((g) => g.cap));
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
      <div class="editor-actions">
        <button data-x="cancel">Cancel</button>
        <button class="primary" data-x="save">Save grants</button>
      </div>`;
    box.querySelector('[data-x="cancel"]').onclick = () => { this._open = null; this._render(); };
    box.querySelector('[data-x="save"]').onclick = async () => {
      const chosen = [];
      box.querySelectorAll("input[data-cap]").forEach((cb) => { if (cb.checked) chosen.push(caps[Number(cb.dataset.cap)]); });
      await this._save(() => this._service.setGrants(id, chosen, null));
    };
  }

  // --- config editor: schema-driven form, or raw-JSON fallback ---
  _buildConfig(box, p) {
    const id = p.record.id;
    const cfg = p.record.config || {};
    const schema = p.manifest && p.manifest.ui && p.manifest.ui.settings;
    const items = schema && Array.isArray(schema.items) ? schema.items : null;

    if (items) {
      box.innerHTML = `<h5>Settings</h5>${items.map((it) => this._fieldHTML(it, cfg)).join("")}
        <div class="editor-actions"><button data-x="cancel">Cancel</button><button class="primary" data-x="save">Save settings</button></div>`;
      box.querySelector('[data-x="cancel"]').onclick = () => { this._open = null; this._render(); };
      box.querySelector('[data-x="save"]').onclick = async () => {
        const out = {};
        for (const it of items) out[it.key] = this._fieldValue(box, it);
        await this._save(() => this._service.setConfig(id, out));
      };
    } else {
      // No schema: a raw JSON editor so any plugin's config is still editable.
      box.innerHTML = `<h5>Settings (JSON)</h5>
        <textarea data-json spellcheck="false">${esc(JSON.stringify(cfg, null, 2))}</textarea>
        <div class="editor-actions"><button data-x="cancel">Cancel</button><button class="primary" data-x="save">Save settings</button></div>`;
      box.querySelector('[data-x="cancel"]').onclick = () => { this._open = null; this._render(); };
      box.querySelector('[data-x="save"]').onclick = async () => {
        let parsed;
        try {
          parsed = JSON.parse(box.querySelector("[data-json]").value || "{}");
        } catch (e) {
          this._err = "Invalid JSON: " + e.message;
          this._render();
          return;
        }
        await this._save(() => this._service.setConfig(id, parsed));
      };
    }
  }

  _fieldHTML(it, cfg) {
    const v = cfg[it.key] ?? it.default ?? "";
    const label = esc(it.label || it.key);
    let ctl;
    if (it.type === "toggle") {
      ctl = `<span class="switch"><input type="checkbox" data-k="${esc(it.key)}" ${v ? "checked" : ""}></span>`;
    } else if (it.type === "select" && Array.isArray(it.options)) {
      ctl = `<select data-k="${esc(it.key)}">${it.options
        .map((o) => `<option value="${esc(o.value ?? o)}" ${String(v) === String(o.value ?? o) ? "selected" : ""}>${esc(o.label ?? o)}</option>`)
        .join("")}</select>`;
    } else {
      const type = it.type === "number" ? "number" : it.type === "password" ? "password" : "text";
      ctl = `<input type="${type}" data-k="${esc(it.key)}" value="${esc(v)}" placeholder="${esc(it.placeholder || "")}">`;
    }
    return `<div class="field"><label>${label}</label><div class="input">${ctl}</div>${it.unit ? `<span class="unit">${esc(it.unit)}</span>` : ""}</div>`;
  }

  _fieldValue(box, it) {
    const el = box.querySelector(`[data-k="${cssEsc(it.key)}"]`);
    if (!el) return it.default;
    if (it.type === "toggle") return el.checked;
    if (it.type === "number") return el.value === "" ? null : Number(el.value);
    return el.value;
  }

  async _save(fn) {
    try {
      await fn();
      this._open = null;
      this._err = "";
      await this._load();
    } catch (e) {
      this._err = e.message || String(e);
      this._render();
    }
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
    plugins.map((p) => [p.record.id, p.record.version, p.record.enabled, (p.record.grants || []).map((g) => g.cap), p.record.config]),
  );
}

function esc(s) {
  return String(s).replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]));
}

// cssEsc escapes a value for use inside an attribute selector.
function cssEsc(s) {
  return String(s).replace(/["\\]/g, "\\$&");
}

customElements.define("plugins-panel", PluginsPanel);
