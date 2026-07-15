// PluginsService is the client for the server's plugin-management API: list
// installed plugins, install from an uploaded .zip, enable/disable, edit grants,
// remove, and subscribe to the live status stream. It holds no UI state — the
// <plugins-panel> component drives it. See internal/engine/server/plugins.go.

export class PluginsService {
  constructor({ assets = "/" } = {}) {
    this._assets = assets;
  }

  /** List installed plugins, each as {record, manifest, status, running}. */
  async list() {
    const r = await fetch(this._assets + "api/plugins", { cache: "no-store" });
    const j = await r.json();
    return j.plugins || [];
  }

  /** Install a plugin from a File (a .zip). Returns the parsed manifest. */
  async install(file) {
    const form = new FormData();
    form.append("plugin", file, file.name);
    const r = await fetch(this._assets + "api/plugins/install", { method: "POST", body: form });
    const j = await r.json();
    if (!j.ok) throw new Error(j.error || "install failed");
    return j.manifest;
  }

  enable(id) {
    return this._post("api/plugins/" + encodeURIComponent(id) + "/enable");
  }

  disable(id) {
    return this._post("api/plugins/" + encodeURIComponent(id) + "/disable");
  }

  /** Replace a plugin's granted capabilities (and optionally its config). */
  setGrants(id, grants, config) {
    return this._send("PUT", "api/plugins/" + encodeURIComponent(id) + "/grants", { grants, config });
  }

  async remove(id, purgeData) {
    const q = purgeData ? "?purgeData=1" : "";
    const r = await fetch(this._assets + "api/plugins/" + encodeURIComponent(id) + q, { method: "DELETE" });
    const j = await r.json();
    if (!j.ok) throw new Error(j.error || "remove failed");
  }

  async _post(path) {
    const r = await fetch(this._assets + path, { method: "POST" });
    const j = await r.json();
    if (!j.ok) throw new Error(j.error || "request failed");
  }

  async _send(method, path, body) {
    const r = await fetch(this._assets + path, {
      method,
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    const j = await r.json();
    if (!j.ok) throw new Error(j.error || "request failed");
  }

  /** Subscribe to the live status stream (the full plugin list on any change).
   * Returns a stop function. */
  stream(onPlugins) {
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
}
