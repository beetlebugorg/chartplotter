---
id: plugins-ui
title: UI Plugins
sidebar_position: 7
---

# UI plugins

A **UI plugin** is a browser-side controller that draws on the chart — a map
overlay, a HUD widget, a settings form. It ships as an ES module and runs in the
frontend, driven only by a small **declarative `ctx`**. It can carry host-side
code too (a WASM/native entry), but the UI half is independent: the built-in
own-ship and AIS overlays are pure UI plugins with no host-side runtime.

> **Trust model.** UI plugins run **trusted in the main document** — there is no
> iframe or browser sandbox. The security gate is at install time. What keeps a UI
> plugin contained is the `ctx`: it never gets the raw MapLibre `map` or the
> plotter, so it can't paint over safety-critical S-52 layers or reach app
> internals. See [capabilities](./capabilities.md#a-note-on-the-ui-capabilities).

## The controller convention

A UI plugin's module **default-exports a class** following the same convention
the built-ins use:

```js
export default class MyOverlay {
  constructor(ctx) {
    this.ctx = ctx;      // your only handle to the app
  }
  start() {              // called once after construction; set up here
    // subscribe to data, add layers/markers, mount HUD…
  }
  destroy() {            // called on unload; tear down what start() created
  }
}
```

- `constructor(ctx)` — stash `ctx`; do not do heavy work yet.
- `start()` — subscribe, add layers/markers, mount UI. May be async.
- `destroy()` — release anything the host doesn't auto-track (see below).

The host loads the module, builds the `ctx`, `new`s the class, and calls
`start()`. On unload it calls `destroy()`, then runs every `ctx`-tracked cleanup
and removes all your layers.

### What the host cleans up vs. what you own

The host **auto-tracks and tears down**: layers you `add`, gesture/anchor
listeners, mounted HUD/panel elements, vessel subscriptions, and settings
registrations. **You own** markers and any timers / `requestAnimationFrame` you
start — a busy AIS feed creates and drops many markers, so the host doesn't track
them. Remove your markers and clear your timers in `destroy()`.

## Loading & serving

- **Built-ins** are statically imported and registered by the shell (own-ship as
  `core.own-ship`, AIS as `core.ais`).
- **Installed UI plugins** declare `ui.entry` in the [manifest](./manifest.md);
  the frontend dynamically `import()`s it from `/plugins/<id>/ui/…`. That path
  serves the plugin's unpacked `ui/` directory (static files, with the right
  `.mjs`/`.wasm` MIME types and range support). A separate `/plugins/<id>/serve/…`
  path serves runtime-published artifacts from the plugin's data dir.

```jsonc
"ui": {
  "entry": "ui/index.mjs",
  "mapLayers": [{ "id": "my-overlay", "title": "My overlay" }]
}
```

The default export of `ui/index.mjs` is the controller class.

## The `ctx` reference

`ctx` is the entire surface a controller gets. Every handle below is real and
implemented in `web/src/core/plugin-host.mjs`.

### `ctx.plugin`

`ctx.plugin.log(level, ...args)` writes to the console **and** a per-plugin ring
the Plugins panel's Logs viewer merges (tagged `[ui]`) with your WASM half's
server-captured `Log` lines — one timeline for both halves of the plugin.


| Member | Signature | Notes |
| --- | --- | --- |
| `id` | string | the plugin id |
| `version` | string | |
| `log` | `log(level, ...args)` | console log tagged `[plugin <id>]`; `level` `"error"`/`"warn"`/other |

### `ctx.vessel` — live own-ship state

| Member | Signature | Notes |
| --- | --- | --- |
| `get` | `get() → state` | current vessel state (`{ navigation, environment, route }`) or undefined |
| `subscribe` | `subscribe(fn) → off` | `fn(state)` on change (≤ 4 Hz coalesced). Auto-tracked. |

```js
this.ctx.vessel.subscribe((s) => {
  const pos = s?.navigation?.position;
  if (pos) this.marker.setLngLat([pos.lon, pos.lat]);
});
this._update(this.ctx.vessel.get()); // prime with the current snapshot
```

### `ctx.ais` — live AIS targets

| Member | Signature | Notes |
| --- | --- | --- |
| `subscribe` | `subscribe(fn) → off` | `fn(targets[])` from the coalesced server feed (EventSource + poll fallback). Auto-tracked. |

Targets carry `mmsi`, `lat`, `lon`, `cog`, `sog`, `heading`, `name`, `shipType`,
`typeName`, `destination`, `length`, `beam`, `draught`, `status`, `class`, and
(when computed) `danger`/`cpaNm`/`tcpaMin`. Skip targets with no position.

### `ctx.layers` — declarative GeoJSON layers

| Member | Signature | Returns |
| --- | --- | --- |
| `add` | `add(layerId, spec) → { setData, remove }` | a layer handle |

You describe layers; you never touch MapLibre. The host creates the GeoJSON
source + style layers, inserts them in the chosen **z-band**, and re-adds +
re-seeds them after a style rebuild — so you never hand-roll style-reload
self-healing.

`spec`:

```js
{
  band: "overlay" | "top",              // z-band; default "overlay"
  // single-layer shorthand:
  type, paint, layout,
  // …or several layers sharing one source:
  layers: [ { type, paint, layout }, … ],
}
```

The handle:

- `setData(featureCollection)` — swap the GeoJSON.
- `remove()` — drop it (also auto-removed on unload).

```js
this.line = this.ctx.layers.add("track", {
  band: "overlay",
  layers: [
    { type: "line", paint: { "line-color": "#fff", "line-width": 4 } },
    { type: "line", paint: { "line-color": "#16324f", "line-width": 1.8, "line-dasharray": [2, 1.8] } },
  ],
});
this.line.setData({ type: "FeatureCollection", features: [ /* … */ ] });
```

**Z-bands** (fixed — you select, never extend):

| Band | Sits |
| --- | --- |
| `overlay` | beneath chart text/symbol labels — the **safe default** (own-ship's predictor lives here) |
| `top` | above everything |

Plugin layers can't be placed above the S-52 labels; that's deliberate.

### `ctx.markers` — DOM glyph markers

| Member | Signature | Returns |
| --- | --- | --- |
| `add` | `add(markerId, opts) → handle` | a chainable marker handle |

`opts`: `{ rotationAlignment?: "map"|"viewport", anchor?: "center"|… }`.

Handle methods (chainable): `element` (the DOM node), `setHTML(html)`,
`setStyle(css)`, `setLngLat([lng,lat])`, `setRotation(deg)`, `onClick(fn)`,
`show()`, `hide()`, `remove()`.

> **You own marker teardown** — call `remove()` in `destroy()` (or when a target
> drops out).

```js
this.marker = this.ctx.markers.add("me", { rotationAlignment: "map", anchor: "center" });
this.marker.setHTML(OWN_SHIP_MARKER).setStyle("pointer-events:auto;cursor:pointer");
this.marker.onClick((e) => { e.stopPropagation(); this._select(e); });
this.marker.setLngLat([lon, lat]).setRotation(headingDeg);
```

### `ctx.camera` — camera & follow

| Member | Signature | Notes |
| --- | --- | --- |
| `follow` | `follow(fix)` | keep the camera on `fix` (`{ lng, lat, … }`) |
| `easeTo` | `easeTo(opts)` | animate the camera (`{ center, zoom, duration }`) |
| `getZoom` | `getZoom() → number` | |
| `project` | `project([lng,lat]) → {x,y}` | geo → screen pixels |
| `containerHeight` | `containerHeight() → px` | map container height |
| `onGesture` | `onGesture(fn) → off` | fires on **real** user pan/rotate (not programmatic eases) — for follow break-out. Auto-tracked. |
| `registerFollowAnchor` | `registerFollowAnchor(fn) → off` | contribute the point wheel-zoom should keep fixed; `fn()` returns `[lng,lat]` or null. Auto-tracked. |

### `ctx.hud` / `ctx.panels` — floating overlay UI

| Member | Signature | Returns |
| --- | --- | --- |
| `hud.mount` | `mount(slotId) → element` | a fresh element in the shell chrome |
| `panels.mount` | `mount(slotId) → element` | same |

The returned element lives in the shell's shadow DOM, so **theme CSS variables
inherit** (style with `var(--ui-accent, …)`, `var(--topbar-h)`, etc.). The mount
is auto-removed on unload.

```js
const mount = this.ctx.hud.mount("wind");
mount.innerHTML = `<div class="wind-hud">—</div>`;
```

### `ctx.settings` — settings contributions

| Member | Signature | Notes |
| --- | --- | --- |
| `register` | `register(descriptor) → off` | contribute a settings entry; the id is auto-scoped to your plugin. Auto-tracked. |

### `ctx.notify` — notification center

`notify.info(msg)`, `notify.warn(msg)`, `notify.error(msg)`.

### `ctx.callout` — info picker / pick report

| Member | Signature | Notes |
| --- | --- | --- |
| `show` | `show(info)` | pop the target-info callout: `{ title, subtitle?, rows: [[label, value], …], x, y }` |

```js
this.ctx.callout.show({
  title: "Own ship",
  rows: [["Position", fmtLatLon(lat, lng)], ["SOG", this.ctx.units.format("speed", sog)]],
  x: e.clientX, y: e.clientY,
});
```

### `ctx.taps` — chart tap arbitration

| Member | Signature | Notes |
| --- | --- | --- |
| `claim` | `claim(fn) → unregister` | `fn(e)` is offered every chart tap (`e` is the MapLibre click event: `lngLat`, `point`, `originalEvent`). Return `true` to consume it — the ECDIS pick report and later claimants don't fire. Return anything else to pass. |

Claim taps only while your overlay is **active**; pass when it isn't, so the
chart behaves as if your plugin weren't installed. The wind probe is the model:

```js
ctx.taps.claim((e) => {
  if (!this._on) return false;      // layer hidden → not our tap
  this._setProbe(e.lngLat);
  return true;                      // consumed: no pick report
});
```

The unregister is tracked by the host and runs on plugin unload.

### `ctx.units` — unit-aware formatting

| Member | Signature | Notes |
| --- | --- | --- |
| `format` | `format(kind, value) → string` | formats honoring the live mariner prefs; `kind` e.g. `"speed"`, `"depth"`, `"distance"` |

## Theming

Style with the app's CSS custom properties so your UI tracks day/dusk/night and
the shell layout. Commonly used: `--ui-accent` / `--ui-accent-text`, `--topbar-h`
/ `--botbar-h`, and (for AIS-style glyphs) `--ais-fill` / `--ais-halo` /
`--ais-danger`. HUD/panel mounts and markers inherit these.

## Worked example: a HUD + map-layer overlay

A compact controller that draws a track line and a SOG HUD chip. It mirrors the
shape of the built-in [own-ship](./examples.md#ui-only-plugin-track-line--sog-hud)
module — read that for the full, production version.

```js
export default class TrackHud {
  constructor(ctx) { this.ctx = ctx; this._pts = []; }

  start() {
    // Map layer in the safe overlay band.
    this._line = this.ctx.layers.add("track", {
      band: "overlay",
      layers: [{ type: "line", paint: { "line-color": "var(--ui-accent,#2f81f7)", "line-width": 2 } }],
    });

    // HUD chip in the shell chrome (theme vars inherit).
    const mount = this.ctx.hud.mount("track");
    this._chip = document.createElement("div");
    this._chip.style.cssText =
      "position:absolute;right:12px;bottom:calc(var(--botbar-h,0px)+12px);" +
      "padding:6px 12px;border-radius:16px;background:var(--ui-accent,#2f81f7);" +
      "color:var(--ui-accent-text,#fff);font:600 12px system-ui";
    mount.appendChild(this._chip);

    // Live data.
    this.ctx.vessel.subscribe((s) => this._update(s));
    this._update(this.ctx.vessel.get());
  }

  _update(s) {
    const nav = s?.navigation, pos = nav?.position;
    if (!pos) return;
    this._pts.push([pos.lon, pos.lat]);
    if (this._pts.length > 500) this._pts.shift();
    this._line.setData({
      type: "FeatureCollection",
      features: [{ type: "Feature", geometry: { type: "LineString", coordinates: this._pts } }],
    });
    this._chip.textContent = "SOG " + this.ctx.units.format("speed", nav.sog ?? 0);
  }

  destroy() { /* host removes the layer + HUD mount + subscription; nothing else to own */ }
}
```

## See also

- [Examples](./examples.md) — own-ship and AIS, the real built-ins.
- [Manifest](./manifest.md) — the `ui` block.
- [Packaging](./packaging.md) — how the `ui/` directory ships and is served.
