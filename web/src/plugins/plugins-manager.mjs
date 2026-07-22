// PluginsController contributes the "Plugins" settings tab — install and manage
// plugins. A plain class built once the app is ready; it registers a settings
// contribution whose render(host) mounts a persistent <plugins-panel> into the tab.
// The panel keeps its own state (open grant editor / connections drill-down) across
// re-renders because the same element instance is moved into each fresh host.

import "./plugins-panel.mjs";
import { PluginsService } from "../data/plugins-service.mjs";

export class PluginsController {
  constructor(deps = {}) {
    this._assets = deps.assets || "/";
    this._service = new PluginsService({ assets: this._assets });
    this._notify = deps.notify;
    this._uiLogs = deps.uiLogs || null;
    this._panel = null;
    this._unregister = deps.registry.register({
      id: "plugins",
      tab: { id: "plugins", label: "Plugins", tabOrder: 6 },
      order: 5,
      render: (host) => this._render(host),
    });
  }

  _render(host) {
    if (!this._panel) {
      this._panel = document.createElement("plugins-panel");
      this._panel.configure({ service: this._service, notify: this._notify, assets: this._assets, uiLogs: this._uiLogs });
    }
    host.appendChild(this._panel); // moving preserves the panel's state
  }

  destroy() {
    if (this._unregister) this._unregister();
    this._unregister = null;
  }
}
