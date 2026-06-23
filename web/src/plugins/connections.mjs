// ConnectionsController contributes the "Connections" settings tab — the NMEA0183
// data-source manager. Like DevTools, it is a plain class (not a custom element)
// built once the app is ready; it registers a settings contribution whose
// render(host) mounts a persistent <connections-panel> into the tab. The panel
// keeps its own state (open form/sniffer) across re-renders because the same
// element instance is moved into each fresh host.

import "./connections-panel.mjs";
import { ConnectionsService } from "../data/connections-service.mjs";

export class ConnectionsController {
  constructor(deps = {}) {
    this._service = new ConnectionsService({ assets: deps.assets || "/" });
    this._notify = deps.notify;
    this._panel = null;
    this._unregister = deps.registry.register({
      id: "connections",
      tab: { id: "connections", label: "Connections" },
      order: 4,
      render: (host) => this._render(host),
    });
  }

  _render(host) {
    if (!this._panel) {
      this._panel = document.createElement("connections-panel");
      this._panel.configure({ service: this._service, notify: this._notify });
    }
    host.appendChild(this._panel); // moving preserves the panel's state
  }

  destroy() {
    if (this._unregister) this._unregister();
    this._unregister = null;
  }
}
