---
id: plugins-examples
title: Examples
sidebar_position: 9
---

# Worked examples

Two complete, real examples: a **backend Tier-A** data source (the reference
plugin that ships in-tree), and a **UI-only** overlay in the built-in style.
Both are runnable and live in the repo.

## Backend (Tier A): the NMEA 0183 source

`plugins/core.nmea0183` is the reference plugin — the built-in NMEA 0183 (tcp-client)
source, reimplemented as a WASM plugin. It owns **no I/O of its own**: the host
dials the socket (`net.tcp-client`) and streams bytes; the plugin frames lines,
parses them with the same `nmea` package the built-in runner uses, and publishes
vessel/AIS/raw deltas back. Parity between the two is the acceptance test.

### The manifest

```jsonc
{
  "manifestVersion": 1,
  "id": "core.nmea0183",
  "name": "TCP Client (NMEA0183)",
  "version": "1.0.0",
  "description": "Reads NMEA0183 from a TCP server and publishes vessel/AIS state.",
  "publisher": "beetlebug.org",
  "license": "MIT",
  "apiVersion": 1,
  "entry": { "wasm": "plugin.wasm" },
  "capabilities": [
    { "cap": "vessel.write" },
    { "cap": "ais.write" },
    { "cap": "net.tcp-client", "hosts": ["${config:host}"] }
  ],
  "provides": [
    { "service": "nmea.source", "apiVersion": 1 }
  ]
}
```

Notes on what's real here:

- The `net.tcp-client` allowlist uses **`${config:host}`** — the dial is scoped to
  whatever host the user configured, resolved at grant time.
- `provides: [{ "service": "nmea.source" }]` is **forward-looking metadata**. The
  `services.*` cross-plugin surface is [Roadmap](./capabilities.md#roadmap-capabilities);
  declaring the service has no runtime effect today.
- It carries no `files` map because it's built and run in-tree (via
  `chartplotter plugin dev` / `make build-plugins`); a distributed archive would
  add the `plugin.wasm` sha256.

### The logic, walked through

**Lifecycle & connect.** `Start` reads the target from config, then asks the host
to dial. All callbacks fire on the read loop — no goroutines.

```go
func (p *tcpClient) Start(h *sdk.Host) {
	p.h = h
	p.store = &nmea.Store{}
	p.parser = &nmea.Parser{}
	p.ais = nmea.NewAISStore(0)
	p.aisFeed = p.ais.Feeder()
	p.prev = map[string]any{}

	host, port := target(h.Config())     // "host"+"port", or "server" = "host:port"
	if host == "" || port == 0 {
		h.Status("degraded", "no server configured")
		return
	}
	h.TCPConnect(host, port, sdk.TCPHandlers{
		OnConnect: func(handle int) { p.handle = handle; h.Status("running", "connected to "+host) },
		OnData:    func(_ int, data []byte) { p.onData(data) },
		OnError:   func(_ int, err error) { h.Status("degraded", "connection closed") },
	})
}

func (p *tcpClient) Stop() {
	if p.handle != 0 { p.h.CloseHandle(p.handle) }
}
```

**Framing.** The host delivers **chunks**, not lines, so the plugin buffers and
splits on `\n` itself:

```go
func (p *tcpClient) onData(chunk []byte) {
	p.buf += string(chunk)
	for {
		i := strings.IndexByte(p.buf, '\n')
		if i < 0 { break }
		line := strings.TrimSpace(p.buf[:i])
		p.buf = p.buf[i+1:]
		if line != "" { p.handleLine(line) }
	}
}
```

**Parse & route.** Each line goes raw → sniffer, VDM/VDO → AIS decode, everything
else → the vessel store — mirroring the built-in runner:

```go
func (p *tcpClient) handleLine(line string) {
	p.h.PublishRaw(line)                 // raw.publish (gated behind vessel/ais write)
	s, err := nmea.ParseSentence(line)
	if err != nil { return }
	if s.Type == "VDM" || s.Type == "VDO" {
		p.aisFeed(line)
		p.publishAIS()
		return
	}
	p.store.Apply(p.parser, s)
	p.publishVessel()
}
```

**Publish (diffed & batched).** `publishVessel` diffs the current snapshot against
the last published set and emits only changed paths; the SDK batches the queued
deltas and flushes at the end of the read-loop iteration:

```go
func (p *tcpClient) publishVessel() {
	cur := vesselPaths(p.store.Snapshot())        // flatten to writable dotted paths
	var deltas []sdk.Delta
	for path, v := range cur {
		if prev, ok := p.prev[path]; !ok || !reflect.DeepEqual(prev, v) {
			deltas = append(deltas, sdk.DeltaOf(path, v))
		}
	}
	if len(deltas) > 0 {
		p.h.PublishVessel(deltas...)
		p.prev = cur
	}
}
```

`vesselPaths` only ever emits the [writable vessel paths](./sdk.md#vessel-paths) —
unknown paths would be dropped by the host anyway.

### Build & run

```bash
# Build to WASM (this is what `make build-plugins` runs)
GOOS=wasip1 GOARCH=wasm CGO_ENABLED=0 go build -o plugins/core.nmea0183/plugin.wasm ./plugins/core.nmea0183

# Run live against a local NMEA source; dev grants all declared caps
chartplotter plugin dev plugins/core.nmea0183 --config '{"host":"127.0.0.1","port":10110}'
```

`dev` prints the effects as they happen:

```
status[core.nmea0183]: running connected to 127.0.0.1:10110
raw[core.nmea0183]: $GPGGA,123519,4807.038,N,01131.000,E,…
vessel[core.nmea0183]: 3 delta(s)
ais[core.nmea0183]: 1 target(s)
```

## UI-only plugin: track line + SOG HUD

The built-in **own-ship** (`web/src/plugins/own-ship.mjs`) and **AIS overlay**
(`web/src/plugins/ais-overlay.mjs`) are real UI plugins — read them as the
canonical examples. They're pure `ctx` consumers: no raw `map`/`plotter`. Here's
the shape, distilled.

### own-ship, in essence

own-ship renders a heading-rotated glyph marker, a dashed COG/SOG predictor + a
solid heading line, follows the vessel with break-out, and shows a GPS-freshness
pill. The mechanics that matter for authors:

```js
export default class OwnShip {
  constructor(ctx) { this.ctx = ctx; /* … */ }

  start() {
    const ctx = this.ctx;

    // A rotated DOM marker (author owns its teardown).
    this._marker = ctx.markers.add("own-ship", { rotationAlignment: "map", anchor: "center" });
    this._marker.setStyle("pointer-events:auto;cursor:pointer").setHTML(OWN_SHIP_MARKER);
    this._marker.onClick((e) => { e.stopPropagation(); this._select(e); });

    // Vectors in the safe overlay band (host self-heals across style rebuilds).
    this._predLayer = ctx.layers.add("predictor", { band: "overlay", layers: [ /* casing + dashed line */ ] });
    this._headLayer = ctx.layers.add("heading",   { band: "overlay", layers: [ /* casing + solid line */ ] });

    // Floating chrome (theme vars inherit): a re-centre chip + a GPS pill.
    const mount = ctx.hud.mount("own-ship");
    // …append chip + pill…

    // Follow break-out on real gestures; keep the vessel fixed under wheel-zoom.
    ctx.camera.onGesture(() => this._setFollow(false));
    ctx.camera.registerFollowAnchor(() => (this._follow && this._fix) ? [this._fix.lng, this._fix.lat] : null);

    // Live data.
    ctx.vessel.subscribe((s) => this._update(s));
    this._update(ctx.vessel.get());
  }

  destroy() {
    // Host tears down layers, gesture/anchor listeners, the mount, and the
    // subscription (all ctx-tracked). We own the marker + timers/RAF.
    if (this._gpsTimer) clearInterval(this._gpsTimer);
    if (this._marker) this._marker.remove();
  }
}
```

The key patterns to copy: markers for glyphs (**you** remove them), `ctx.layers`
for vectors in the `overlay` band, `ctx.hud.mount` for floating chrome that
inherits the theme, `ctx.camera` for follow, and `ctx.vessel.subscribe` +
priming with `ctx.vessel.get()`.

### AIS overlay, in essence

The AIS overlay subscribes to `ctx.ais`, keeps a `mmsi → marker` map, upserts a
glyph per target (rotated by COG/heading, coloured via `--ais-*` CSS vars), and
removes markers for targets that drop out of the feed:

```js
export default class AISOverlay {
  constructor(ctx) { this.ctx = ctx; this._markers = new Map(); }

  start() {
    this._off = this.ctx.ais.subscribe((targets) => this._apply(targets || []));
  }

  _apply(targets) {
    const seen = new Set();
    for (const t of targets) {
      if (typeof t.lat !== "number" || (!t.lat && !t.lon)) continue; // skip position-less
      seen.add(t.mmsi);
      this._upsert(t);
    }
    for (const [mmsi, rec] of this._markers) {
      if (!seen.has(mmsi)) { rec.marker.remove(); this._markers.delete(mmsi); }
    }
  }

  destroy() {
    if (this._off) this._off();
    for (const rec of this._markers.values()) rec.marker.remove();
    this._markers.clear();
  }
}
```

See [UI plugins](./ui.md) for the full `ctx` reference behind these.

## See also

- [SDK](./sdk.md) — the backend API.
- [UI plugins](./ui.md) — the `ctx` surface.
- [Getting started](./getting-started.md) — from zero to running.
