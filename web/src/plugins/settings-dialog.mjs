// <settings-dialog> — the settings panel HOST. It owns no settings of its own;
// it renders whatever is registered in the injected SettingsRegistry, so the
// app's display settings and every plugin's settings share one panel with one
// look. Mounted by the shell inside the drawer's #settings-body (sibling to how
// <chart-library> mounts in #charts-body); inherits the --ui-* theme tokens.
//
//   const dlg = document.createElement("settings-dialog");
//   dlg.configure({ registry });   // a SettingsRegistry
//   dlg.show("general");           // open on a tab (optional) + render
//   dlg.refresh();                 // re-render (state changed elsewhere)
//
// Logic only: state, deps, event wiring, and the render ORCHESTRATION that feeds
// each contribution's items to the pure view builders. See settings-dialog.view.

import { STYLE, settingRow, groupHead, tabRail, shell, customHost } from "./settings-dialog.view.mjs";

export class SettingsDialog extends HTMLElement {
  constructor() {
    super();
    if (!this.shadowRoot) this.attachShadow({ mode: "open" });
    this._registry = null;
    this._activeTab = null;
    this._active = false;
    this._offChange = null;
  }

  connectedCallback() {
    this.shadowRoot.innerHTML = `<style>${STYLE}</style><div id="body"></div>`;
  }

  disconnectedCallback() { if (this._offChange) this._offChange(); }

  // Inject the registry (call once). Re-renders whenever contributions change.
  configure({ registry } = {}) {
    if (this._offChange) this._offChange();
    this._registry = registry || null;
    this._offChange = registry ? registry.onChange(() => { if (this._active) this.render(); }) : null;
    return this;
  }

  // Make the panel active (optionally on a given tab) and render.
  show(tab) {
    this._active = true;
    if (tab) this._activeTab = tab;
    this.render();
  }

  refresh() { if (this._active) this.render(); }

  // The currently-shown tab id (e.g. so the shell can reveal tab-specific chrome).
  get activeTab() { return this._activeTab; }

  // Current items for a contribution (its `items` may be an array or a function),
  // each tagged with the owning contribution id so the view can route changes.
  _items(c) {
    let items = typeof c.items === "function" ? c.items() : c.items;
    return (items || []).map((it) => ({ ...it, _cid: c.id }));
  }

  render() {
    const body = this.shadowRoot && this.shadowRoot.getElementById("body");
    if (!body || !this._registry) return;
    // Toggling a control re-renders the whole pane; preserve the scroll position
    // so the dialog doesn't jump back to the top on every change.
    const prevPane = body.querySelector(".set-pane");
    const prevScroll = prevPane ? prevPane.scrollTop : 0;
    const tabs = this._registry.tabs();
    if (!tabs.length) { body.innerHTML = `<div class="set-empty">No settings available.</div>`; this._emitTab(); return; }
    if (!tabs.some((t) => t.id === this._activeTab)) this._activeTab = tabs[0].id;

    const contribs = this._registry.forTab(this._activeTab);
    let pane = "";
    for (const c of contribs) {
      pane += groupHead(c.group);
      for (const it of this._items(c)) {
        const raw = c.get ? c.get(it.key, it.default) : it.default;
        const value = it.transform ? it.transform.toView(raw) : raw;
        const on = (key) => (c.get ? !!c.get(key, false) : false);
        pane += settingRow(it, value, on);
      }
      if (typeof c.render === "function") pane += customHost(c.id);
    }
    body.innerHTML = shell(tabRail(tabs, this._activeTab), pane);
    const newPane = body.querySelector(".set-pane");
    if (newPane && prevScroll) newPane.scrollTop = prevScroll;

    // Fill any custom-render slots (e.g. the Advanced tab's dev tools).
    for (const c of contribs) {
      if (typeof c.render !== "function") continue;
      const host = body.querySelector(`.set-host[data-host="${cssEsc(c.id)}"]`);
      if (host) { try { c.render(host, { get: c.get, set: c.set }); } catch (e) { console.warn("[settings] render", c.id, e); } }
    }
    this._wire(body, contribs);
    this._emitTab();
  }

  // Announce the active tab so the host can reveal tab-specific chrome (e.g. the
  // shell's developer-tools region under the Advanced tab).
  _emitTab() {
    if (this._lastEmittedTab === this._activeTab) return;
    this._lastEmittedTab = this._activeTab;
    this.dispatchEvent(new CustomEvent("tab-change", { detail: { tab: this._activeTab } }));
  }

  _wire(body, contribs) {
    const byId = new Map(contribs.map((c) => [c.id, c]));
    const itemOf = (c, key) => this._items(c).find((it) => it.key === key);
    // Resolve a control's raw view value back through the item's transform (if
    // any) before handing it to the contribution, then re-render to reflect.
    const apply = (cid, key, rawView) => {
      const c = byId.get(cid);
      if (c && c.set) {
        const it = itemOf(c, key);
        const value = it && it.transform ? it.transform.fromView(rawView) : rawView;
        try { c.set(key, value); } catch (e) { console.warn("[settings] set", c.id, key, e); }
      }
      this.render();
    };

    body.querySelectorAll("[data-tab]").forEach((b) =>
      (b.onclick = () => { this._activeTab = b.dataset.tab; this.render(); }));

    body.querySelectorAll('input[data-type="toggle"]').forEach((inp) =>
      (inp.onchange = () => apply(inp.dataset.contrib, inp.dataset.key, inp.checked)));

    body.querySelectorAll('button[data-type="segmented"]').forEach((b) =>
      (b.onclick = () => apply(b.dataset.contrib, b.dataset.key, b.dataset.val)));

    // A `multi` button is an independent boolean keyed by itself; toggle current.
    body.querySelectorAll('button[data-type="multi"]').forEach((b) =>
      (b.onclick = () => { const c = byId.get(b.dataset.contrib); apply(b.dataset.contrib, b.dataset.key, !(c && c.get && c.get(b.dataset.key, false))); }));

    body.querySelectorAll('select[data-type="select"]').forEach((s) =>
      (s.onchange = () => apply(s.dataset.contrib, s.dataset.key, s.value)));

    body.querySelectorAll('input[data-type="number"]').forEach((inp) =>
      (inp.onchange = () => {
        const v = parseFloat(inp.value);
        if (!isFinite(v)) { this.render(); return; }
        apply(inp.dataset.contrib, inp.dataset.key, v);
      }));

    // A date input stores the compact "YYYYMMDD" the mariner model uses; blank
    // clears it (unset = real today).
    body.querySelectorAll('input[data-type="date"]').forEach((inp) =>
      (inp.onchange = () => apply(inp.dataset.contrib, inp.dataset.key, inp.value ? inp.value.replace(/-/g, "") : undefined)));
  }
}

// Escape a contribution id for use in a CSS attribute selector.
function cssEsc(s) { return String(s).replace(/["\\]/g, "\\$&"); }

customElements.define("settings-dialog", SettingsDialog);
