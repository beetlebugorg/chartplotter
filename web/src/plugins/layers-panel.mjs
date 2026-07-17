// <layers-panel> + LayersController — the "Layers" settings tab. Lists every
// registered map overlay (core: own-ship vectors, AIS targets; plus plugin overlays
// like Wind) grouped by source, each with a show/hide switch. Hiding an overlay is
// visual only — the underlying plugin keeps running. Mounted into the settings dialog
// via the render(host) escape hatch, like Connections/Plugins.

import "./layers-panel-el.mjs"; // defines <layers-panel>

export class LayersController {
  constructor({ registry, layers }) {
    this._layers = layers;
    this._panel = null;
    this._unregister = registry.register({
      id: "layers",
      tab: { id: "layers", label: "Layers", tabOrder: 3 },
      order: 3,
      render: (host) => this._render(host),
    });
  }

  _render(host) {
    if (!this._panel) {
      this._panel = document.createElement("layers-panel");
      this._panel.configure({ layers: this._layers });
    }
    host.appendChild(this._panel);
  }

  destroy() {
    if (this._unregister) this._unregister();
    this._unregister = null;
  }
}
