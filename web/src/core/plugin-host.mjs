// plugin-host.mjs — the frontend plugin host (spec §8, §11, Appendix A.3).
//
// It loads plugin UI controllers and gives each one a `ctx`: the same handles the
// built-in modules use, but as a small, declarative surface with NO raw `map` or
// `plotter` object. A controller follows the established convention —
// `constructor(ctx)`, `start()`, `destroy()` — exactly like the built-ins, so an
// author reads own-ship.mjs and writes the same kind of module.
//
// In this build the registry is the in-tree `core.*` controllers (own-ship, AIS),
// statically imported and registered by the shell; dynamic `import()` of
// `/plugins/<id>/ui/…` for installed plugins is the extension path that reuses this
// exact ctx. The host is the single place that touches the plotter/map internals —
// via ctx.layers / ctx.markers / ctx.camera — so plugin code stays renderer-agnostic
// and can't paint over safety-critical layers.

import { PluginLayers } from "../plugins/plugin-layers.mjs";
import { format } from "../lib/units.mjs";

export class PluginHost {
  // services are the shell handles the ctx wraps:
  //   map        — the MapLibre instance (wrapped, never exposed raw)
  //   plotter    — <chart-canvas> (its overlay/camera API, wrapped)
  //   vessel     — VesselStateStore
  //   aisStreamURL / aisPollURL — the AIS feed endpoints (or null in widget mode)
  //   chrome     — an element in the shell shadow DOM for floating overlay UI
  //   showInfo   — (info) => the target-info picker
  //   getUnits   — () => live mariner prefs
  //   registerZoomAnchor — (fn) => unregister; the shell aggregates for wheel-zoom
  //   settings   — SettingsRegistry
  //   notify     — NotificationCenter
  constructor(services) {
    this._svc = services;
    this._layers = new PluginLayers({ map: services.map, plotter: services.plotter });
    this._ais = new AISFeed(services.aisStreamURL, services.aisPollURL);
    this._loaded = new Map(); // id -> { controller, cleanups }
    this._installed = new Set(); // ids loaded dynamically from installed archives
    this._es = null;
  }

  // register loads a controller for a plugin: builds its ctx, instantiates the
  // controller, and calls start(). ControllerClass is the module's default export.
  async register({ id, version, ControllerClass }) {
    if (this._loaded.has(id)) return;
    const cleanups = [];
    const ctx = this._buildCtx({ id, version, cleanups });
    const controller = new ControllerClass(ctx);
    this._loaded.set(id, { controller, cleanups });
    try {
      await controller.start?.();
    } catch (e) {
      console.warn(`[plugin ${id}] start failed`, e);
    }
  }

  // start discovers installed, enabled plugins that ship a UI (manifest ui.entry),
  // dynamically imports each one's entry module from its archive, and keeps the set
  // in sync as plugins are enabled/disabled (via the /api/plugins SSE). Builtins are
  // registered separately by the shell before this runs.
  start() {
    this._syncInstalled();
    if (typeof EventSource !== "undefined") {
      this._es = new EventSource(this._svc.assets + "api/plugins/stream");
      this._es.onmessage = (ev) => {
        let d;
        try {
          d = JSON.parse(ev.data);
        } catch {
          return;
        }
        this._syncInstalled(d.plugins);
      };
      this._es.onerror = () => {};
    }
  }

  async _syncInstalled(list) {
    let plugins = list;
    if (!plugins) {
      try {
        plugins = (await (await fetch(this._svc.assets + "api/plugins", { cache: "no-store" })).json()).plugins || [];
      } catch {
        return;
      }
    }
    const want = new Set();
    for (const p of plugins) {
      const ui = p.manifest && p.manifest.ui;
      if (p.record.enabled && ui && ui.entry) {
        want.add(p.record.id);
        this._loadInstalled(p.record.id, p.record.version, ui.entry);
      }
    }
    // Unload any installed (non-builtin) plugin UI that is no longer enabled.
    for (const id of [...this._installed]) {
      if (!want.has(id)) {
        this._installed.delete(id);
        this.unregister(id);
      }
    }
  }

  async _loadInstalled(id, version, entry) {
    if (this._loaded.has(id) || this._installed.has(id)) return;
    this._installed.add(id);
    const rel = String(entry).replace(/^ui\//, "");
    const url = `${this._svc.assets}plugins/${encodeURIComponent(id)}/ui/${rel}`;
    try {
      const mod = await import(url);
      if (mod && mod.default) await this.register({ id, version, ControllerClass: mod.default });
      else console.warn(`[plugin ${id}] ui entry has no default export`);
    } catch (e) {
      this._installed.delete(id);
      console.warn(`[plugin ${id}] UI load failed`, e);
    }
  }

  unregister(id) {
    const rec = this._loaded.get(id);
    if (!rec) return;
    this._loaded.delete(id);
    try {
      rec.controller.destroy?.();
    } catch (e) {
      console.warn(`[plugin ${id}] destroy failed`, e);
    }
    for (const fn of rec.cleanups) {
      try {
        fn();
      } catch {
        /* best-effort teardown */
      }
    }
    this._layers.removeAll(id);
  }

  destroy() {
    if (this._es) this._es.close();
    for (const id of [...this._loaded.keys()]) this.unregister(id);
    this._ais.destroy();
    this._layers.destroy();
  }

  // _buildCtx assembles the declarative context for one plugin.
  _buildCtx({ id, version, cleanups }) {
    const svc = this._svc;
    const map = svc.map;

    const track = (fn) => {
      cleanups.push(fn);
      return fn;
    };

    return {
      plugin: {
        id,
        version,
        log: (level, ...args) => console[level === "error" ? "error" : level === "warn" ? "warn" : "log"](`[plugin ${id}]`, ...args),
      },

      // Live vessel state (≤4 Hz coalesced) — same store the built-ins read.
      vessel: {
        get: () => svc.vessel && svc.vessel.state,
        subscribe: (fn) => {
          if (!svc.vessel) return () => {};
          const off = svc.vessel.onChange(fn);
          return track(off);
        },
      },

      // Live AIS targets, wrapped over the server feed (EventSource + poll fallback).
      ais: {
        subscribe: (fn) => track(this._ais.subscribe(fn)),
      },

      // Declarative GeoJSON layers with z-bands + style-reload self-healing.
      layers: {
        add: (layerId, spec) => this._layers.add(id, layerId, spec),
      },

      // Register a show/hide-able overlay in the Layers control. desc:
      // { id, title, group?, defaultVisible?, onVisible(visible) }. The id is
      // namespaced by plugin; onVisible fires immediately + on each toggle, so the
      // plugin can pause expensive work while hidden (spec §8, visibility signal).
      overlays: {
        register: (desc) => {
          if (!svc.overlays) return () => {};
          return track(svc.overlays.register({ ...desc, id: `${id}:${desc.id}`, group: desc.group || (svc.pluginTitle && svc.pluginTitle(id)) || "Overlays" }));
        },
      },

      // The raw MapLibre instance — the use-at-your-own-risk tier (spec §8, §13).
      // The declarative handles (layers/markers/camera) cover the common cases and
      // carry the compatibility promise; a controller that needs more (a custom
      // WebGL/canvas overlay like the wind-particle layer) uses this and accepts the
      // same contract the built-ins live with (handle palette/style re-adds, stay in
      // its z-band). own-ship/AIS deliberately do NOT use it.
      map,

      // App asset base, so a plugin can fetch its own served artifacts
      // (GET /plugins/<id>/serve/…, same-origin under the app CSP).
      assets: svc.assets || "/",

      // DOM markers (rotated glyphs) without handing the plugin the raw map. The
      // controller owns marker teardown (own-ship removes its one marker; AIS its
      // per-target set) — the host does not auto-track them, since a busy AIS feed
      // creates and drops many over a session.
      markers: {
        add: (markerId, opts) => this._makeMarker(map, opts),
      },

      // Camera/follow contract (wraps the plotter + map camera; no raw handle out).
      camera: {
        follow: (fix) => svc.plotter.updateFollow(fix),
        easeTo: (opts) => map.easeTo(opts),
        getZoom: () => map.getZoom(),
        project: (lnglat) => map.project(lnglat),
        containerHeight: () => (map.getContainer() && map.getContainer().clientHeight) || 0,
        // User pan/rotate gestures (real ones only — programmatic eases have no
        // originalEvent), for follow break-out.
        onGesture: (fn) => {
          const h = (e) => {
            if (!e || e.originalEvent) fn(e);
          };
          map.on("dragstart", h);
          map.on("rotatestart", h);
          return track(() => {
            map.off("dragstart", h);
            map.off("rotatestart", h);
          });
        },
        // Register the point wheel-zoom should keep fixed (own-ship's anchor). The
        // shell aggregates these for WheelZoom.getAnchor.
        registerFollowAnchor: (fn) => track(svc.registerZoomAnchor ? svc.registerZoomAnchor(fn) : () => {}),
      },

      // Floating overlay-UI mount in the shell chrome (theme CSS vars inherit).
      hud: { mount: (slotId) => this._mount(id, slotId, track) },
      panels: { mount: (slotId) => this._mount(id, slotId, track) },

      // Settings contribution registry, pre-scoped to the plugin id.
      settings: svc.settings
        ? {
            register: (desc) => {
              const scoped = { ...desc, id: desc.id ? `${id}.${desc.id}` : id };
              const off = svc.settings.register(scoped);
              return track(off);
            },
          }
        : { register: () => () => {} },

      // Notification center.
      notify: svc.notify
        ? {
            info: (m) => svc.notify.info(m),
            warn: (m) => svc.notify.warn(m),
            error: (m) => svc.notify.error(m),
          }
        : { info() {}, warn() {}, error() {} },

      // Info callout / pick report.
      callout: {
        show: (info) => svc.showInfo && svc.showInfo(info),
      },

      // Unit formatting honoring the live mariner prefs (closed over, not exposed).
      units: {
        format: (kind, value) => format(kind, value, svc.getUnits ? svc.getUnits() : null),
      },
    };
  }

  // _makeMarker wraps a MapLibre Marker in a small chainable handle.
  _makeMarker(map, opts = {}) {
    const el = document.createElement("div");
    const marker = new window.maplibregl.Marker({
      element: el,
      rotationAlignment: opts.rotationAlignment || "map",
      anchor: opts.anchor || "center",
    });
    let added = false;
    const handle = {
      element: el,
      setHTML: (html) => ((el.innerHTML = html), handle),
      setStyle: (css) => ((el.style.cssText = css), handle),
      setLngLat: (ll) => {
        marker.setLngLat(ll);
        if (!added) {
          marker.addTo(map);
          added = true;
        }
        return handle;
      },
      setRotation: (deg) => (marker.setRotation(deg), handle),
      onClick: (fn) => (el.addEventListener("click", fn), handle),
      show: () => {
        if (!added) {
          marker.addTo(map);
          added = true;
        }
        return handle;
      },
      hide: () => {
        if (added) {
          marker.remove();
          added = false;
        }
        return handle;
      },
      remove: () => {
        if (added) {
          marker.remove();
          added = false;
        }
      },
    };
    return handle;
  }

  // _mount returns a fresh element in the shell chrome for a plugin's overlay UI.
  _mount(pluginId, slotId, track) {
    const el = document.createElement("div");
    el.dataset.plugin = pluginId;
    el.dataset.slot = slotId || "";
    if (this._svc.chrome) this._svc.chrome.appendChild(el);
    track(() => el.remove());
    return el;
  }
}

// AISFeed subscribes to the server's AIS stream once and fans targets out to every
// ctx.ais subscriber (the same EventSource + poll-fallback logic the AIS overlay used
// to own privately). Lazily started on the first subscription.
class AISFeed {
  constructor(streamURL, pollURL) {
    this._streamURL = streamURL;
    this._pollURL = pollURL;
    this._subs = new Set();
    this._es = null;
    this._polling = false;
    this._last = [];
  }

  subscribe(fn) {
    this._subs.add(fn);
    if (this._last.length) fn(this._last); // prime with the latest snapshot
    this._start();
    return () => this._subs.delete(fn);
  }

  _start() {
    if (!this._streamURL || this._es || this._polling) return;
    if (typeof EventSource === "undefined") {
      this._poll();
      return;
    }
    const es = new EventSource(this._streamURL);
    es.onmessage = (ev) => {
      let d;
      try {
        d = JSON.parse(ev.data);
      } catch {
        return;
      }
      this._emit(d.targets || []);
    };
    es.onerror = () => {}; // EventSource auto-reconnects
    this._es = es;
  }

  async _poll() {
    if (!this._pollURL) return;
    this._polling = true;
    while (this._polling) {
      try {
        const r = await fetch(this._pollURL, { cache: "no-store" });
        if (r.ok) this._emit((await r.json()).targets || []);
      } catch {
        /* ignore; retry */
      }
      await new Promise((res) => setTimeout(res, 2000));
    }
  }

  _emit(targets) {
    this._last = targets;
    for (const fn of this._subs) {
      try {
        fn(targets);
      } catch (e) {
        console.warn("[ais feed] subscriber", e);
      }
    }
  }

  destroy() {
    if (this._es) this._es.close();
    this._polling = false;
    this._subs.clear();
  }
}
