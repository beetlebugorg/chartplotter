// ConnectionsService is the client for the server's NMEA0183 connection API:
// CRUD over /api/connections, the live status badge stream, and the per-
// connection raw-sentence sniffer stream. It holds no UI state — the
// <connections-panel> component drives it.

export class ConnectionsService {
  constructor({ assets = "/" } = {}) {
    this._assets = assets;
  }

  /** List configured connections, each as {source, status}. */
  async list() {
    const r = await fetch(this._assets + "api/connections", { cache: "no-store" });
    const j = await r.json();
    return j.connections || [];
  }

  /** Create a connection from a config object; returns the stored {source,status}. */
  create(cfg) {
    return this._send("POST", "api/connections", cfg);
  }

  /** Update a connection's config; returns the stored {source,status}. */
  update(id, cfg) {
    return this._send("PUT", "api/connections/" + encodeURIComponent(id), cfg);
  }

  /** Delete a connection. */
  async remove(id) {
    const r = await fetch(this._assets + "api/connections/" + encodeURIComponent(id), {
      method: "DELETE",
    });
    const j = await r.json();
    if (!j.ok) throw new Error(j.error || "delete failed");
  }

  async _send(method, path, body) {
    const r = await fetch(this._assets + path, {
      method,
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    const j = await r.json();
    if (!j.ok) throw new Error(j.error || "request failed");
    return j.connection;
  }

  // --- plugin-provided sources ---------------------------------------------
  // Plugins that declare `provides: [{service:"nmea.source"}]` are data sources
  // too; the Connections panel is the single place they're configured. Their
  // config form comes from the manifest's ui.settings.items schema; state maps
  // onto the plugin lifecycle (pause = disable plugin).

  /** Installed plugins that provide nmea.source: [{id, name, config, enabled,
      status, items}] (items = the manifest settings schema for the form). */
  async pluginSources() {
    const r = await fetch(this._assets + "api/plugins", { cache: "no-store" });
    const j = await r.json();
    return (j.plugins || [])
      .filter((p) => ((p.manifest && p.manifest.provides) || []).some((x) => x.service === "nmea.source"))
      .map((p) => ({
        id: p.record.id,
        name: (p.manifest && p.manifest.name) || p.record.id,
        config: p.record.config || {},
        enabled: !!p.record.enabled,
        status: p.status || {},
        items: (p.manifest && p.manifest.ui && p.manifest.ui.settings && p.manifest.ui.settings.items) || [],
      }));
  }

  /** Save a plugin source's connection config and make sure it's running. */
  async savePluginSource(id, config) {
    await this._sendOK("POST", `api/plugins/${encodeURIComponent(id)}/config`, config);
    await this._sendOK("POST", `api/plugins/${encodeURIComponent(id)}/enable`, null);
  }

  /** Pause/resume a plugin source (disable/enable the plugin). */
  pausePluginSource(id, run) {
    return this._sendOK("POST", `api/plugins/${encodeURIComponent(id)}/${run ? "enable" : "disable"}`, null);
  }

  /** Subscribe to plugin status updates (for plugin-source badges). */
  streamPluginStatuses(onPlugins) {
    if (!window.EventSource) return () => {};
    const es = new EventSource(this._assets + "api/plugins/stream");
    es.onmessage = (ev) => {
      let s;
      try {
        s = JSON.parse(ev.data);
      } catch {
        return;
      }
      onPlugins(s.plugins || []);
    };
    return () => es.close();
  }

  async _sendOK(method, path, body) {
    const r = await fetch(this._assets + path, {
      method,
      headers: body != null ? { "Content-Type": "application/json" } : {},
      body: body != null ? JSON.stringify(body) : undefined,
    });
    const j = await r.json();
    if (!j.ok) throw new Error(j.error || "request failed");
  }

  /**
   * Subscribe to the live status map (id → SourceStatus). Returns a stop function.
   */
  streamStatuses(onStatuses) {
    if (!window.EventSource) return () => {};
    const es = new EventSource(this._assets + "api/connections/stream");
    es.onmessage = (ev) => {
      let s;
      try {
        s = JSON.parse(ev.data);
      } catch {
        return;
      }
      onStatuses(s.statuses || {});
    };
    return () => es.close();
  }

  /**
   * Subscribe to raw sentences from one connection (the wiring sniffer). Returns
   * a stop function.
   */
  streamRaw(id, onLine) {
    if (!window.EventSource) return () => {};
    const es = new EventSource(this._assets + "api/connections/" + encodeURIComponent(id) + "/raw");
    es.onmessage = (ev) => {
      let line;
      try {
        line = JSON.parse(ev.data);
      } catch {
        return;
      }
      onLine(line);
    };
    return () => es.close();
  }
}
