// settings-registry.mjs — the settings contribution registry.
//
// The settings panel (<settings-dialog>) is a HOST: it renders whatever is
// registered here, so a plugin owns its own settings AND gets space in the
// global panel without the dialog knowing anything about it. The app's own
// display settings are registered the same way (a "core" contribution), so
// there is no privileged built-in path — everything is a contribution.
//
// A contribution is a plain descriptor:
//
//   registry.register({
//     id: "ais",                              // unique; re-registering replaces
//     tab: { id: "ais", label: "AIS" },       // a new tab — OR tab: "general" to
//                                             //   slot into an existing tab
//     group: "Targets",                        // optional subheading within the tab
//     order: 100,                              // sort order among siblings
//     items: [
//       { key, type, label, desc?, default?, options?, unit?, step?, transform?,
//         locked?, when? },                    // see <settings-dialog>.view for types
//     ],
//     get: (key) => value,                     // current value (drives selection)
//     set: (key, value) => {},                 // apply + persist (the plugin owns it)
//     render: (host, ctx) => {},               // OPTIONAL escape hatch: a contribution
//                                             //   that needs fully custom UI provides
//                                             //   this instead of (or beside) items;
//                                             //   `host` is a container element in the
//                                             //   dialog's shadow DOM.
//   });
//
// The dialog groups contributions by tab → group → order and renders `items`
// with its shared control library, calling get()/set() per item. Plugins
// register on attach and unregister(id) on detach; the registry notifies the
// dialog to re-render via onChange.

export class SettingsRegistry {
  constructor() {
    this._byId = new Map();      // id → contribution
    this._listeners = new Set(); // () => void, fired when the set of contributions changes
  }

  // Add (or replace) a contribution. Returns an unregister function.
  register(contribution) {
    if (!contribution || !contribution.id) throw new Error("settings contribution needs an id");
    this._byId.set(contribution.id, contribution);
    this._emit();
    return () => this.unregister(contribution.id);
  }

  unregister(id) {
    if (this._byId.delete(id)) this._emit();
  }

  // All contributions, sorted by their order (then id for stability).
  list() {
    return [...this._byId.values()].sort((a, b) => (a.order ?? 0) - (b.order ?? 0) || String(a.id).localeCompare(String(b.id)));
  }

  // The tabs to show, derived from contributions. A contribution's `tab` is
  // either a string id (slot into a tab declared elsewhere) or {id,label} (which
  // also DECLARES that tab). Tabs appear in first-declaration/contribution order;
  // an explicit `tabOrder` on the {id,label} form overrides. Returns [{id,label}].
  tabs() {
    const seen = new Map();
    for (const c of this.list()) {
      const t = c.tab;
      if (!t) continue;
      const id = typeof t === "string" ? t : t.id;
      if (!id) continue;
      if (!seen.has(id)) seen.set(id, { id, label: (typeof t === "object" && t.label) || id, order: (typeof t === "object" && t.tabOrder) ?? seen.size });
      else if (typeof t === "object" && t.label && seen.get(id).label === id) seen.get(id).label = t.label; // a later {id,label} names a tab first seen as a bare string
    }
    return [...seen.values()].sort((a, b) => a.order - b.order);
  }

  // Contributions targeting a given tab id (in render order).
  forTab(tabId) {
    return this.list().filter((c) => {
      const t = c.tab;
      return t && (typeof t === "string" ? t : t.id) === tabId;
    });
  }

  onChange(fn) { this._listeners.add(fn); return () => this._listeners.delete(fn); }
  _emit() { for (const fn of this._listeners) { try { fn(); } catch (e) { console.warn("[settings] listener", e); } } }
}
