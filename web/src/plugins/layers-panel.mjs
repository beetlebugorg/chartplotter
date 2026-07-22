// <layers-panel> + LayersController — the map-layers popover. Layers are a
// FIRST-CLASS control (their own round button in the bottom-right cluster), not a
// settings page: they're something you toggle while navigating, not configuration.
// Lists every registered map overlay (core: own-ship vectors, AIS targets; plus
// plugin overlays like Wind) grouped by source, each with a show/hide switch.
// Hiding an overlay is visual only — the underlying plugin keeps running.

import "./layers-panel-el.mjs"; // defines <layers-panel>

export class LayersController {
  // deps: { layers, button, pop } — the registry, the #layers-btn round button,
  // and the #layers-pop popover container in the shell chrome.
  constructor({ layers, button, pop }) {
    this._layers = layers;
    this._panel = null;
    this._btn = button || null;
    this._pop = pop || null;
    if (this._btn && this._pop) {
      this._btn.addEventListener("click", () => this.toggle(!this._pop.classList.contains("open")));
    }
  }

  // Show/hide the popover (mounting the panel lazily on first open).
  toggle(open) {
    if (!this._pop) return;
    if (open && !this._panel) {
      this._panel = document.createElement("layers-panel");
      this._panel.configure({ layers: this._layers });
      this._pop.appendChild(this._panel);
    }
    this._pop.classList.toggle("open", !!open);
    if (this._btn) this._btn.classList.toggle("on", !!open);
  }

  destroy() {
    if (this._unregister) this._unregister();
    this._unregister = null;
  }
}
