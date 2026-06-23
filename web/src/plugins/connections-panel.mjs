// <connections-panel> — the Connections settings UI. Lists configured NMEA0183
// connections with live health badges (fed by the status SSE stream), an
// add/edit form, delete, and a per-connection raw-sentence sniffer. It is
// mounted into the "Connections" settings tab by ConnectionsController via the
// settings-dialog's render(host) escape hatch.
//
// Structural changes (add/edit/delete) re-render the list; status updates patch
// each row's badge in place so an open sniffer/form isn't disturbed.

const STYLE = `
  :host { display: block; color: var(--ui-text, #e6edf3); font-size: 13px; }
  .bar { display: flex; justify-content: flex-end; margin-bottom: 10px; }
  button {
    background: var(--ui-surface-2, #21262d); color: var(--ui-text, #e6edf3);
    border: 1px solid var(--ui-border, #30363d); border-radius: 6px;
    padding: 5px 10px; font: inherit; cursor: pointer;
  }
  button:hover { background: var(--ui-hover, #30363d); }
  button.primary { background: var(--ui-accent, #2f81f7); color: var(--ui-accent-text, #fff); border-color: transparent; }
  button.icon { padding: 4px 7px; line-height: 1; }
  .empty { color: var(--ui-text-dim, #8b949e); padding: 18px 4px; text-align: center; }

  .row {
    display: flex; align-items: center; gap: 10px;
    padding: 9px 10px; border: 1px solid var(--ui-border, #30363d);
    border-radius: 8px; margin-bottom: 8px; background: var(--ui-surface, #161b22);
  }
  .dot { width: 10px; height: 10px; border-radius: 50%; flex: none; background: #6e7681; }
  .info { flex: 1; min-width: 0; }
  .name { font-weight: 600; display: flex; align-items: center; gap: 8px; }
  .badge { font-size: 11px; font-weight: 500; color: var(--ui-text-dim, #8b949e); text-transform: uppercase; letter-spacing: .03em; }
  .meta { color: var(--ui-text-dim, #8b949e); font-size: 12px; margin-top: 2px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .actions { display: flex; gap: 5px; flex: none; }

  pre.sniff {
    margin: -4px 0 8px; max-height: 160px; overflow: auto;
    background: var(--ui-bg, #0d1117); border: 1px solid var(--ui-border, #30363d);
    border-radius: 8px; padding: 8px; font-size: 11px; line-height: 1.45;
    color: var(--ui-text-dim, #8b949e); white-space: pre-wrap; word-break: break-all;
  }

  .form { border: 1px solid var(--ui-border, #30363d); border-radius: 8px; padding: 12px; margin-bottom: 12px; background: var(--ui-surface, #161b22); }
  .form h4 { margin: 0 0 10px; font-size: 13px; }
  .field { display: flex; align-items: center; gap: 8px; margin-bottom: 8px; }
  .field label { width: 80px; color: var(--ui-text-dim, #8b949e); flex: none; }
  .field input[type=text], .field input[type=number] {
    flex: 1; background: var(--ui-bg, #0d1117); color: var(--ui-text, #e6edf3);
    border: 1px solid var(--ui-border, #30363d); border-radius: 6px; padding: 5px 8px; font: inherit;
  }
  .field .static { color: var(--ui-text-dim, #8b949e); }
  .form-actions { display: flex; justify-content: flex-end; gap: 8px; margin-top: 4px; }
  .err { color: #f85149; font-size: 12px; margin-top: 6px; }
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
    this._editing = null; // null | "new" | "<id>"
    this._sniffId = null; // id of the connection whose sniffer is open
    this._sniffStop = null;
    this._sniffLines = [];
    this._statusStop = null;
  }

  configure({ service, notify }) {
    this._service = service;
    this._notify = notify;
  }

  connectedCallback() {
    this.shadowRoot.innerHTML = `<style>${STYLE}</style><div id="root"></div>`;
    this.shadowRoot.getElementById("root").addEventListener("click", (e) => this._onClick(e));
    this.refresh();
    // Live badges while the tab is open.
    if (this._service) {
      this._statusStop = this._service.streamStatuses((statuses) => this._applyStatuses(statuses));
    }
  }

  disconnectedCallback() {
    if (this._statusStop) this._statusStop();
    this._statusStop = null;
    this._stopSniff();
  }

  async refresh() {
    if (!this._service) return;
    try {
      this._conns = await this._service.list();
    } catch (e) {
      console.warn("[connections] list", e);
      this._conns = [];
    }
    this._render();
  }

  // --- rendering ------------------------------------------------------------

  _render() {
    const root = this.shadowRoot.getElementById("root");
    if (!root) return;
    let html = "";
    if (this._editing) html += this._formHtml();
    html += `<div class="bar"><button class="primary" data-act="add">+ Add connection</button></div>`;
    if (!this._conns.length) {
      html += `<div class="empty">No connections yet. Add a TCP source (e.g. a multiplexer on your boat network).</div>`;
    } else {
      html += this._conns.map((c) => this._rowHtml(c)).join("");
    }
    root.innerHTML = html;
    for (const c of this._conns) this._patchStatus(c.source.id, c.status);
    if (this._sniffId) this._renderSniff();
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
          <button class="icon" data-act="toggle" data-id="${id}" title="${source.enabled ? "Pause (stop connecting/retrying)" : "Resume"}">${source.enabled ? "⏸" : "▶"}</button>
          <button class="icon" data-act="sniff" data-id="${id}" title="Raw sentences">≋</button>
          <button data-act="edit" data-id="${id}">Edit</button>
          <button class="icon" data-act="del" data-id="${id}" title="Delete">✕</button>
        </div>
      </div>
      <pre class="sniff" id="sniff-${id}" ${this._sniffId === id ? "" : "hidden"}></pre>`;
  }

  _formHtml() {
    // 10110 is the IANA-registered NMEA-0183-over-IP port — the sensible default.
    const src = this._editing === "new" ? { port: 10110 } : (this._conns.find((c) => c.source.id === this._editing) || {}).source || {};
    return `
      <div class="form">
        <h4>${this._editing === "new" ? "Add connection" : "Edit connection"}</h4>
        <div class="field"><label>Name</label><input type="text" id="f-name" value="${esc(src.name || "")}" placeholder="Multiplexer"></div>
        <div class="field"><label>Transport</label><span class="static">TCP client</span></div>
        <div class="field"><label>Host</label><input type="text" id="f-host" value="${esc(src.host || "")}" placeholder="10.0.0.20"></div>
        <div class="field"><label>Port</label><input type="number" id="f-port" value="${src.port || ""}" min="1" max="65535" placeholder="10110"></div>
        <div class="field"><label>Enabled</label><input type="checkbox" id="f-enabled" ${src.enabled !== false ? "checked" : ""}></div>
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
        this._render();
        break;
      case "edit":
        this._stopSniff();
        this._editing = id;
        this._render();
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

  async _save() {
    const cfg = this._formValues();
    const editing = this._editing;
    try {
      if (editing === "new") await this._service.create(cfg);
      else await this._service.update(editing, cfg);
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
