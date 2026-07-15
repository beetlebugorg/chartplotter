// layer-registry.mjs — the map-overlay visibility registry.
//
// Overlays (core and plugin) register here with a title and a setter; the Layers
// control renders the list with show/hide switches and persists each choice. This is
// deliberately distinct from enabling/disabling a plugin: hiding an overlay is purely
// visual — the plugin keeps running (AIS keeps tracking for CPA, an anchor watch stays
// armed), and the overlay is told about the change so it can pause expensive work
// (a weather particle animation) while staying loaded.

const LS_KEY = "cp.layers.hidden"; // persisted set of hidden layer ids

export class LayerRegistry {
  constructor() {
    this._layers = new Map(); // id -> { id, title, group, visible, apply }
    this._listeners = new Set();
    this._hidden = loadHidden();
  }

  // register adds an overlay. desc: { id, title, group?, defaultVisible?, onVisible }.
  // onVisible(visible) is called immediately with the resolved state and on each
  // toggle. Returns an unregister function.
  register(desc) {
    if (!desc || !desc.id) throw new Error("layer needs an id");
    const visible = this._hidden.has(desc.id) ? false : desc.defaultVisible !== false;
    const rec = { id: desc.id, title: desc.title || desc.id, group: desc.group || "Overlays", visible, apply: desc.onVisible };
    this._layers.set(desc.id, rec);
    safeApply(rec, visible);
    this._emit();
    return () => {
      this._layers.delete(desc.id);
      this._emit();
    };
  }

  setVisible(id, visible) {
    const rec = this._layers.get(id);
    if (!rec || rec.visible === visible) return;
    rec.visible = visible;
    if (visible) this._hidden.delete(id);
    else this._hidden.add(id);
    saveHidden(this._hidden);
    safeApply(rec, visible);
    this._emit();
  }

  toggle(id) {
    const rec = this._layers.get(id);
    if (rec) this.setVisible(id, !rec.visible);
  }

  // list returns the registered overlays grouped, in registration order.
  list() {
    return [...this._layers.values()];
  }

  onChange(fn) {
    this._listeners.add(fn);
    return () => this._listeners.delete(fn);
  }

  _emit() {
    for (const fn of this._listeners) {
      try {
        fn();
      } catch (e) {
        console.warn("[layers] listener", e);
      }
    }
  }
}

function safeApply(rec, visible) {
  try {
    rec.apply && rec.apply(visible);
  } catch (e) {
    console.warn(`[layers] ${rec.id} apply`, e);
  }
}

function loadHidden() {
  try {
    return new Set(JSON.parse(localStorage.getItem(LS_KEY) || "[]"));
  } catch {
    return new Set();
  }
}

function saveHidden(set) {
  try {
    localStorage.setItem(LS_KEY, JSON.stringify([...set]));
  } catch {
    /* private mode / quota — visibility just won't persist */
  }
}
