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
