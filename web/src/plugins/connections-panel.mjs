// <connections-panel> — the Connections settings UI. Lists configured NMEA0183
// connections with live health badges (fed by the status SSE stream), an
// add/edit form, delete, and a per-connection raw-sentence sniffer. It is
// mounted into the "Connections" settings tab by ConnectionsController via the
// settings-dialog's render(host) escape hatch.
//
// Structural changes (add/edit/delete) re-render the list; status updates patch
// each row's badge in place so an open sniffer/form isn't disturbed.

// Styling follows the shared panel conventions (docs/docs/style-guide.md): 13px
// body type, 8px-radius buttons, 10px-radius row cards on --ui-surface, 12px card
// padding, 92px form labels. The sniffer <pre> is a documented exception to the
// one-scroll-container rule (log viewers may scroll internally, capped height).
const STYLE = `
  :host { display: block; color: var(--ui-text, #e6edf3); font-size: 13px; }
  .bar { display: flex; justify-content: flex-end; margin-bottom: 12px; }
  button {
    background: var(--ui-surface-2, #21262d); color: var(--ui-text, #e6edf3);
    border: 1px solid var(--ui-border, #30363d); border-radius: 8px;
    padding: 6px 12px; font: inherit; cursor: pointer; white-space: nowrap;
    touch-action: manipulation; -webkit-touch-callout: none; -webkit-user-select: none; user-select: none;
  }
  button:hover { background: var(--ui-hover, #30363d); }
  button.primary { background: var(--ui-accent, #2f81f7); color: var(--ui-accent-text, #fff); border-color: transparent; }
  button.icon { padding: 6px 8px; line-height: 1; min-width: 34px; display: inline-flex; align-items: center; justify-content: center; }
  .empty { color: var(--ui-text-dim, #8b949e); padding: 26px 4px; text-align: center; border: 1px dashed var(--ui-border, #30363d); border-radius: 10px; }

  .row {
    display: flex; align-items: center; gap: 12px;
    padding: 12px 14px; border: 1px solid var(--ui-border, #30363d);
    border-radius: 10px; margin-bottom: 10px; background: var(--ui-surface, #161b22);
  }
  .dot { width: 10px; height: 10px; border-radius: 50%; flex: none; background: #6e7681; }
  .info { flex: 1; min-width: 0; }
  .name { font-weight: 600; font-size: 13.5px; display: flex; align-items: center; gap: 8px; }
  .badge { font-size: 11px; font-weight: 500; color: var(--ui-text-dim, #8b949e); text-transform: uppercase; letter-spacing: .03em; }
  .meta { color: var(--ui-text-dim, #8b949e); font-size: 12px; margin-top: 4px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .actions { display: flex; align-items: center; gap: 7px; flex: none; }

  pre.sniff {
    margin: -6px 0 10px; max-height: 160px; overflow: auto;
    overscroll-behavior: contain; -webkit-overflow-scrolling: touch;
    background: var(--ui-bg, #0d1117); border: 1px solid var(--ui-border, #30363d);
    border-radius: 10px; padding: 10px; font-size: 11px; line-height: 1.45;
    color: var(--ui-text-dim, #8b949e); white-space: pre-wrap; word-break: break-all;
  }

  .form { border: 1px solid var(--ui-border, #30363d); border-radius: 10px; padding: 14px 16px; margin-bottom: 12px; background: var(--ui-surface, #161b22); }
  .form h4 { margin: 0 0 12px; font-size: 12px; color: var(--ui-text-dim, #8b949e); text-transform: uppercase; letter-spacing: .03em; }
  .field { display: flex; align-items: center; gap: 12px; margin-bottom: 12px; }
  .field label { width: 92px; color: var(--ui-text-dim, #8b949e); flex: none; }
  .field input[type=text], .field input[type=number], .field select {
    flex: 1; min-width: 0; background: var(--ui-bg, #0d1117); color: var(--ui-text, #e6edf3);
    border: 1px solid var(--ui-border-strong, #444c56); border-radius: 7px; padding: 7px 9px; font: inherit;
    font-size: 16px; /* >=16px or iOS zooms the page on focus */
  }
  .field .static { color: var(--ui-text-dim, #8b949e); }
  .form-actions { display: flex; justify-content: flex-end; gap: 8px; margin-top: 4px; }
  .err { color: #f85149; font-size: 12px; margin-top: 6px; }
  @media (pointer:coarse) { button { min-height: var(--tap-min, 44px); } }
`;

// state → [dot colour, badge label]
const BADGE = {
  connected: ["#3fb950", "live"],
  stale: ["#d29922", "stale"],
  error: ["#f85149", "error"],
  connecting: ["#58a6ff", "connecting"],
  disabled: ["#6e7681", "paused"],
};

const MAX_SNIFF_LINES = 200;

export class ConnectionsPanel extends HTMLElement {
  constructor() {
    super();
    if (!this.shadowRoot) this.attachShadow({ mode: "open" });
    this._service = null;
    this._notify = null;
    this._conns = []; // [{source, status}]
    this._plugSources = []; // plugin-provided sources (see ConnectionsService.pluginSources)
    this._editing = null; // null | "new" | "<id>" | "plugin:<id>"
    this._newKind = "tcp"; // add-form type: "tcp" | "plugin:<id>"
    this._sniffId = null; // id of the connection whose sniffer is open
    this._sniffStop = null;
    this._sniffLines = [];
    this._statusStop = null;
    this._plugStop = null;
  }

  configure({ service, notify }) {
    this._service = service;
    this._notify = notify;
  }

  // Scope the panel to one data-source plugin (the Plugins-tab drill-down): only
  // that plugin's source row and add-form entry are shown. Built-in TCP
  // connections always show — they're NMEA sources too, managed in the same view.
  setPlugin(pluginId) {
    if (this._pluginId !== pluginId) {
      this._pluginId = pluginId || null;
      this._editing = null;
      this.refresh();
    }
  }

  connectedCallback() {
    this.shadowRoot.innerHTML = `<style>${STYLE}</style><div id="root"></div>`;
    const root = this.shadowRoot.getElementById("root");
    root.addEventListener("click", (e) => this._onClick(e));
    // The add-form's type picker swaps the field set (delegated: the form re-renders).
    root.addEventListener("change", (e) => {
      if (e.target && e.target.id === "f-kind") {
        this._newKind = e.target.value;
        this._render();
      }
    });
    this.refresh();
    // Live badges for the built-in runners via POLLING (transient fetches), not SSE:
    // the app is HTTP/1.1 (6 sockets/origin) and the core streams already hold
    // three — a settings panel must not pin more (see style guide). Plugin-source
    // rows are pushed down by the host panel via pluginsPush().
    if (this._service) {
      const tick = async () => {
        const conns = await this._service.list();
        const statuses = {};
        for (const c of conns) statuses[c.source.id] = c.status;
        this._applyStatuses(statuses);
      };
      const t = setInterval(() => { tick().catch(() => {}); }, 2000);
      this._statusStop = () => clearInterval(t);
    }
  }

  disconnectedCallback() {
    if (this._statusStop) this._statusStop();
    this._statusStop = null;
    this._stopSniff();
  }

  // pluginsPush receives the host's live plugin list (from its existing stream) and
  // patches the plugin-source rows in place.
  pluginsPush(plugins) {
    const rows = (plugins || [])
      .filter((p) => ((p.manifest && p.manifest.provides) || []).some((x) => x.service === "nmea.source"))
      .filter((p) => !this._pluginId || p.record.id === this._pluginId)
      .map((p) => ({
        id: p.record.id,
        name: (p.manifest && p.manifest.name) || p.record.id,
        config: p.record.config || {},
        enabled: !!p.record.enabled,
        status: p.status || {},
        items: (p.manifest && p.manifest.ui && p.manifest.ui.settings && p.manifest.ui.settings.items) || [],
      }));
    const structural = rows.length !== this._plugSources.length ||
      rows.some((r, i) => r.id !== this._plugSources[i].id || r.enabled !== this._plugSources[i].enabled);
    this._plugSources = rows;
    if (structural && !this._editing) this._render();
    else for (const p of rows) this._patchPluginStatus(p);
  }

  async refresh() {
    if (!this._service) return;
    try {
      this._conns = await this._service.list();
    } catch (e) {
      console.warn("[connections] list", e);
      this._conns = [];
    }
    try {
      this._plugSources = await this._service.pluginSources();
      if (this._pluginId) this._plugSources = this._plugSources.filter((p) => p.id === this._pluginId);
    } catch (e) {
      console.warn("[connections] plugin sources", e);
      this._plugSources = [];
    }
    this._loaded = true;
    this._render();
  }

  // --- rendering ------------------------------------------------------------

  _render() {
    const root = this.shadowRoot.getElementById("root");
    if (!root) return;
    let html = "";
    if (this._editing) html += this._formHtml();
    else html += `<div class="bar"><button class="primary" data-act="add">+ Add connection</button></div>`;
    // Only plugin sources that are configured (or running) show as rows; an
    // installed-but-unconfigured provider only appears in the add-form type picker.
    const activePlug = this._plugSources.filter((p) => p.enabled || Object.keys(p.config || {}).length);
    if (!this._conns.length && !activePlug.length) {
      html += `<div class="empty">${this._loaded
        ? "No connections yet. Add a TCP source (e.g. a multiplexer on your boat network)."
        : "Loading…"}</div>`;
    } else {
      html += this._conns.map((c) => this._rowHtml(c)).join("");
      html += activePlug.map((p) => this._pluginRowHtml(p)).join("");
    }
    root.innerHTML = html;
    for (const c of this._conns) this._patchStatus(c.source.id, c.status);
    for (const p of activePlug) this._patchPluginStatus(p);
    if (this._sniffId) this._renderSniff();
  }

  // A plugin-provided source row. Same look as a built-in connection; the badge is
  // derived from the plugin lifecycle + its status detail, and "pause" maps to
  // disabling the plugin. Removal lives in the Plugins tab (it uninstalls code, not
  // just a connection), so there's no delete button here.
  _pluginRowHtml(p) {
    const id = p.id;
    return `
      <div class="row" id="row-${esc(id)}">
        <div class="dot" id="dot-${esc(id)}"></div>
        <div class="info">
          <div class="name">${esc(p.name)} <span class="badge" id="badge-${esc(id)}"></span><span class="badge">plugin</span></div>
          <div class="meta" id="meta-${esc(id)}"></div>
        </div>
        <div class="actions">
          <button class="icon" data-act="ptoggle" data-id="${esc(id)}" title="${p.enabled ? "Pause (disable the plugin)" : "Resume"}">${p.enabled ? "❚❚" : "▷"}</button>
          <button class="icon" data-act="sniff" data-id="${esc(id)}" title="Raw sentences">≋</button>
          <button data-act="pedit" data-id="${esc(id)}">Edit</button>
        </div>
      </div>
      <pre class="sniff" id="sniff-${esc(id)}" ${this._sniffId === id ? "" : "hidden"}></pre>`;
  }

  // Map a plugin's lifecycle state onto the connection badge vocabulary.
  _patchPluginStatus(p) {
    const dot = this.shadowRoot.getElementById(`dot-${p.id}`);
    const badge = this.shadowRoot.getElementById(`badge-${p.id}`);
    const meta = this.shadowRoot.getElementById(`meta-${p.id}`);
    if (!dot || !badge || !meta) return;
    const detail = p.status.detail || "";
    const state = !p.enabled ? "disabled"
      : p.status.state === "running" ? (/connected|step\(s\)/.test(detail) ? "connected" : "connecting")
        : p.status.state === "error" || p.status.state === "degraded" ? "error"
          : "connecting";
    const [color, label] = BADGE[state] || BADGE.disabled;
    dot.style.background = color;
    badge.textContent = label;
    const where = [p.config.host, p.config.port].filter((x) => x != null && x !== "").join(":");
    const detailText = !p.enabled ? "paused" : detail;
    // Don't repeat the endpoint when the status line already names it.
    meta.textContent = detailText && detailText.includes(where) && where
      ? detailText : [where, detailText].filter(Boolean).join(" · ");
    meta.title = meta.textContent;
  }

  _rowHtml({ source, status }) {
    const id = source.id;
    const where = `${source.host}:${source.port}`;
    return `
      <div class="row" id="row-${id}">
        <div class="dot" id="dot-${id}"></div>
        <div class="info">
          <div class="name">${esc(source.name || "(unnamed)")} <span class="badge" id="badge-${id}"></span></div>
          <div class="meta" id="meta-${id}">${esc(where)}</div>
        </div>
        <div class="actions">
          <button class="icon" data-act="toggle" data-id="${id}" title="${source.enabled ? "Pause (stop connecting/retrying)" : "Resume"}">${source.enabled ? "❚❚" : "▷"}</button>
          <button class="icon" data-act="sniff" data-id="${id}" title="Raw sentences">≋</button>
          <button data-act="edit" data-id="${id}">Edit</button>
          <button class="icon" data-act="del" data-id="${id}" title="Delete">✕</button>
        </div>
      </div>
      <pre class="sniff" id="sniff-${id}" ${this._sniffId === id ? "" : "hidden"}></pre>`;
  }

  _formHtml() {
    const editingPlugin = String(this._editing).startsWith("plugin:")
      ? this._plugSources.find((p) => p.id === this._editing.slice(7))
      : null;
    const isNew = this._editing === "new";
    const kind = editingPlugin ? `plugin:${editingPlugin.id}` : isNew ? this._newKind : "tcp";
    // The type picker: the built-in TCP client plus every installed plugin that
    // provides nmea.source — connections are configured HERE, the plugin only
    // supplies the transport + its config schema.
    const kindField = isNew
      ? `<div class="field"><label>Type</label><select id="f-kind">
          <option value="tcp" ${kind === "tcp" ? "selected" : ""}>NMEA 0183 · TCP client (built-in)</option>
          ${this._plugSources.map((p) =>
            `<option value="plugin:${esc(p.id)}" ${kind === `plugin:${p.id}` ? "selected" : ""}>${esc(p.name)} · plugin</option>`).join("")}
        </select></div>`
      : `<div class="field"><label>Type</label><span class="static">${editingPlugin ? esc(editingPlugin.name) + " · plugin" : "TCP client"}</span></div>`;

    let fields;
    if (kind.startsWith("plugin:")) {
      const p = editingPlugin || this._plugSources.find((x) => `plugin:${x.id}` === kind);
      fields = (p ? p.items : []).map((it) => {
        const cur = p && p.config[it.key] != null ? p.config[it.key] : it.default != null ? it.default : "";
        const type = it.type === "number" ? "number" : "text";
        return `<div class="field"><label>${esc(it.label || it.key)}</label>
          <input type="${type}" data-cfg-key="${esc(it.key)}" value="${esc(cur)}" placeholder="${esc(it.placeholder || "")}"></div>`;
      }).join("");
    } else {
      // 10110 is the IANA-registered NMEA-0183-over-IP port — the sensible default.
      const src = isNew ? { port: 10110 } : (this._conns.find((c) => c.source.id === this._editing) || {}).source || {};
      fields = `
        <div class="field"><label>Name</label><input type="text" id="f-name" value="${esc(src.name || "")}" placeholder="Multiplexer"></div>
        <div class="field"><label>Host</label><input type="text" id="f-host" value="${esc(src.host || "")}" placeholder="10.0.0.20"></div>
        <div class="field"><label>Port</label><input type="number" id="f-port" value="${src.port || ""}" min="1" max="65535" placeholder="10110"></div>
        <div class="field"><label>Enabled</label><input type="checkbox" id="f-enabled" ${src.enabled !== false ? "checked" : ""}></div>`;
    }
    return `
      <div class="form">
        <h4>${isNew ? "Add connection" : "Edit connection"}</h4>
        ${kindField}
        ${fields}
        <div class="err" id="f-err" hidden></div>
        <div class="form-actions">
          <button data-act="cancel">Cancel</button>
          <button class="primary" data-act="save">Save</button>
        </div>
      </div>`;
  }

  // --- status patching ------------------------------------------------------

  _applyStatuses(statuses) {
    for (const c of this._conns) {
      const st = statuses[c.source.id];
      if (st) {
        c.status = st;
        this._patchStatus(c.source.id, st);
      }
    }
  }

  _patchStatus(id, status) {
    const dot = this.shadowRoot.getElementById(`dot-${id}`);
    const badge = this.shadowRoot.getElementById(`badge-${id}`);
    const meta = this.shadowRoot.getElementById(`meta-${id}`);
    if (!dot || !badge || !meta) return;
    const [color, label] = BADGE[status.state] || BADGE.disabled;
    dot.style.background = color;
    badge.textContent = label;
    const src = (this._conns.find((c) => c.source.id === id) || {}).source || {};
    const where = `${src.host}:${src.port}`;
    // Error/paused/connecting take priority over stale sentence lists so a
    // connection that drops shows WHY, not its last-good data.
    let line = where;
    if (status.state === "disabled") {
      line += " · paused";
    } else if (status.state === "error") {
      line += " · " + (status.lastError || "connection error");
    } else if (status.state === "connecting") {
      line += " · connecting…";
    } else if (status.lastRx && (status.sentences || []).length) {
      const rate = status.rateHz ? status.rateHz.toFixed(1) + " Hz" : "";
      const errs = status.errors ? ` · ${status.errors} err` : "";
      line += ` · ${status.sentences.join(" ")}${rate ? " · " + rate : ""}${errs}`;
    } else if (status.state === "connected") {
      line += " · waiting for data";
    }
    meta.textContent = line;
    meta.title = status.lastError ? `${where} — ${status.lastError}` : line; // full text on hover (meta clips)
  }

  // --- sniffer --------------------------------------------------------------

  _toggleSniff(id) {
    if (this._sniffId === id) {
      this._stopSniff();
      const pre = this.shadowRoot.getElementById(`sniff-${id}`);
      if (pre) pre.hidden = true;
      return;
    }
    this._stopSniff();
    this._sniffId = id;
    this._sniffLines = [];
    const pre = this.shadowRoot.getElementById(`sniff-${id}`);
    if (pre) {
      pre.hidden = false;
      pre.textContent = "waiting for sentences…";
    }
    this._sniffStop = this._service.streamRaw(id, (line) => this._onSniffLine(line));
  }

  _onSniffLine(line) {
    this._sniffLines.push(line);
    if (this._sniffLines.length > MAX_SNIFF_LINES) this._sniffLines.shift();
    this._renderSniff();
  }

  _renderSniff() {
    const pre = this.shadowRoot.getElementById(`sniff-${this._sniffId}`);
    if (!pre) return;
    const atBottom = pre.scrollTop + pre.clientHeight >= pre.scrollHeight - 4;
    pre.textContent = this._sniffLines.join("\n");
    if (atBottom) pre.scrollTop = pre.scrollHeight;
  }

  _stopSniff() {
    if (this._sniffStop) this._sniffStop();
    this._sniffStop = null;
    this._sniffId = null;
    this._sniffLines = [];
  }

  // --- events ---------------------------------------------------------------

  _onClick(e) {
    const btn = e.target.closest("button");
    if (!btn) return;
    const act = btn.dataset.act;
    const id = btn.dataset.id;
    switch (act) {
      case "add":
        this._stopSniff();
        this._editing = "new";
        this._newKind = "tcp";
        this._render();
        break;
      case "edit":
        this._stopSniff();
        this._editing = id;
        this._render();
        break;
      case "pedit":
        this._stopSniff();
        this._editing = "plugin:" + id;
        this._render();
        break;
      case "ptoggle":
        this._togglePluginPause(id);
        break;
      case "cancel":
        this._editing = null;
        this._render();
        break;
      case "save":
        this._save();
        break;
      case "del":
        this._delete(id);
        break;
      case "sniff":
        this._toggleSniff(id);
        break;
      case "toggle":
        this._togglePause(id);
        break;
    }
  }

  // Pause/resume a connection (enabled flag). Pausing stops the runner so it
  // doesn't keep retrying a dead/wrong endpoint.
  async _togglePause(id) {
    const c = this._conns.find((x) => x.source.id === id);
    if (!c) return;
    if (this._sniffId === id) this._stopSniff();
    try {
      await this._service.update(id, { ...c.source, enabled: !c.source.enabled });
      await this.refresh();
    } catch (e) {
      if (this._notify) this._notify.error("Connection: " + (e.message || e));
    }
  }

  _formValues() {
    const $ = (id) => this.shadowRoot.getElementById(id);
    return {
      name: $("f-name").value.trim(),
      transport: "tcp-client",
      host: $("f-host").value.trim(),
      port: parseInt($("f-port").value, 10) || 0,
      protocol: "nmea0183",
      direction: "in",
      enabled: $("f-enabled").checked,
    };
  }

  _formError(msg) {
    const el = this.shadowRoot.getElementById("f-err");
    if (el) {
      el.textContent = msg;
      el.hidden = false;
    }
  }

  // Pause/resume a plugin source (disable/enable the whole plugin — it IS the
  // transport). The server clears its vessel readings on disable.
  async _togglePluginPause(id) {
    const p = this._plugSources.find((x) => x.id === id);
    if (!p) return;
    if (this._sniffId === id) this._stopSniff();
    try {
      await this._service.pausePluginSource(id, !p.enabled);
      await this.refresh();
    } catch (e) {
      if (this._notify) this._notify.error("Connection: " + (e.message || e));
    }
  }

  // _pluginFormValues reads the schema-driven fields into a config object.
  _pluginFormValues() {
    const cfg = {};
    this.shadowRoot.querySelectorAll("[data-cfg-key]").forEach((inp) => {
      const v = inp.type === "number" ? parseFloat(inp.value) : inp.value.trim();
      if (v !== "" && !(typeof v === "number" && !isFinite(v))) cfg[inp.dataset.cfgKey] = v;
    });
    return cfg;
  }

  async _save() {
    const editing = this._editing;
    const pluginId = String(editing).startsWith("plugin:") ? editing.slice(7)
      : editing === "new" && this._newKind.startsWith("plugin:") ? this._newKind.slice(7)
        : null;
    try {
      if (pluginId) {
        const p = this._plugSources.find((x) => x.id === pluginId);
        // Preserve config keys the connection form doesn't own (e.g. non-connection
        // settings a plugin may also have).
        await this._service.savePluginSource(pluginId, { ...((p && p.config) || {}), ...this._pluginFormValues() });
      } else if (editing === "new") {
        await this._service.create(this._formValues());
      } else {
        await this._service.update(editing, this._formValues());
      }
      this._editing = null;
      await this.refresh();
    } catch (e) {
      this._formError(String(e.message || e));
      if (this._notify) this._notify.warn("Connection: " + (e.message || e));
    }
  }

  async _delete(id) {
    if (this._sniffId === id) this._stopSniff();
    try {
      await this._service.remove(id);
      await this.refresh();
    } catch (e) {
      if (this._notify) this._notify.error("Delete failed: " + (e.message || e));
    }
  }
}

function esc(s) {
  return String(s).replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]));
}

customElements.define("connections-panel", ConnectionsPanel);
