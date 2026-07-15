---
id: plugins-capabilities
title: Capabilities
sidebar_position: 4
---

# Capability reference

Everything a host-side plugin can do is a **capability**: declared in the
[manifest](./manifest.md), granted (as a subset) by the user, and mediated by
the host **broker** at runtime. A plugin with no grants can do nothing.

This page lists every capability the host knows about, how it's mediated, its
manifest parameters, and — honestly — whether it is **implemented** or
**Roadmap** in this build.

## How mediation works

For a plugin→host **request** to a capability the plugin wasn't granted, the
broker replies with error code `-32000` (`CapabilityDenied`). For a **notification**
(fire-and-forget, e.g. `vessel.publish`) there is no reply channel, so an
ungranted call is **silently dropped**. Grants can change at runtime without a
restart — the host pushes the new set via a `host.grantsChanged` notification.

## Implemented capabilities

| Capability | Grants | Mediation | Manifest params |
| --- | --- | --- | --- |
| `vessel.write` | Publish SignalK-style vessel deltas into the shared vessel state, attributed to the plugin id. | `vessel.publish` notifications are applied only if granted; unknown paths in a batch are rejected, valid siblings still apply. | — |
| `ais.write` | Publish AIS target updates (upserted into the shared AIS store, attributed to the plugin). | `ais.publish` notifications applied only if granted. | — |
| `net.tcp-client` | Ask the host to **dial** an allow-listed `host:port`. The host owns the socket and streams inbound chunks back; the plugin never opens a socket itself. | `tcp.connect` checks the grant, then the `hosts` allowlist; a non-matching target is denied `-32000`. | `hosts` (required to be useful) |
| `storage` | A per-plugin key/value store (`storage.json` in the plugin's data dir), with a byte quota. | `storage.*` requests require the grant; writes past the quota fail. | `quota` (default 5 MiB) |

Two more effects are always available to a **running** plugin and need no explicit grant:

| Effect | Notes |
| --- | --- |
| **config get/set** | `config.get` returns the plugin's own settings; `config.set` stores plugin-learned values. Not capability-gated. |
| **status.update** | Report health (`running` / `degraded` / `error` + detail) to the connections/plugins UI. Not capability-gated. |

### `raw.publish` (derived gate)

`raw.publish` feeds the **raw-sentence sniffer**. It is not a standalone
capability you request; the broker accepts it only if the plugin holds
`vessel.write` **or** `ais.write` — the rationale being that a plugin that is
neither a vessel nor an AIS source has no business injecting raw sentences.

### `net.tcp-client` host allowlist

The `hosts` allowlist patterns support:

- An exact host: `nmea.example.com`.
- A `*.` wildcard prefix: `*.example.com` matches `example.com` and any subdomain.
- An optional `:port` suffix: `nmea.example.com:2000` restricts to that port.
- `${config:key}` substitution: `${config:host}` is filled from the plugin's
  config at grant time, so the user's chosen server is the allowed one. (This is
  how the reference NMEA 0183 plugin scopes its dial to the configured host.)

An **empty** `hosts` list denies every connection.

### `storage` quota

The `quota` string parses as bytes: `"10MB"`, `"512KB"`, `"1024"` (bare = bytes).
A `storage` grant with no `quota` gets a conservative **5 MiB** default. A
`storage.set` that would push the KV file past the quota fails with an internal
error; the key is not written.

## Roadmap capabilities

These capability **names are defined** and appear in the manifest schema, but the
host does **not** enforce or wire them in this build. Do not write how-to guides
against them yet.

| Capability | Intended grant | Current status |
| --- | --- | --- |
| `vessel.read` | Read the shared vessel state host-side. | **Declared, no host RPC.** There is no read method in the protocol; host-side plugins are producers. (The frontend `ctx.vessel` is how UI plugins read vessel state — see [UI plugins](./ui.md).) |
| `ais.read` | Read the shared AIS targets host-side. | **Declared, no host RPC.** Same as above; UI plugins read AIS via `ctx.ais`. |
| `serial` | Open an allow-listed serial device. | **Gated but not wired.** `serial.list` returns an empty port list; `serial.open` returns `-32601` "serial transport not available in this build". |
| `net.udp` | UDP send/receive. | **Not implemented** — no host handler. |
| `net.http` | Host-mediated HTTP fetches. | **Not implemented** — no host handler. |
| `http.register` | Register a plugin HTTP endpoint under the server. | **Not implemented** — no host handler. |
| `notify` | Post host notifications from a host-side plugin. | **Declared only.** (UI plugins post notifications via `ctx.notify` — see [UI plugins](./ui.md).) |
| `ui.settings` | Contribute a settings form. | **Not gated.** UI contributions run trusted in the frontend; see below. |
| `ui.panel` | Contribute a panel slot. | **Not gated.** |
| `ui.map-layer` | Contribute a map layer. | **Not gated.** |
| `ui.hud` | Contribute a HUD widget. | **Not gated.** |

### A note on the `ui.*` capabilities

UI plugins run **trusted in the main document** — the security gate is at install
time, not a browser sandbox. The `ui.*` capability names exist so a manifest can
declare its UI surface, but the frontend plugin host does not check them before
loading a controller. What keeps a UI plugin contained is the **declarative
`ctx`**: it can only touch the map through host-owned handles and can't paint over
safety-critical S-52 layers. See [UI plugins](./ui.md).

## Related

- [Manifest reference](./manifest.md) — how capabilities are declared.
- [SDK](./sdk.md) — the Go methods that exercise each capability.
- [Protocol](./protocol.md) — the wire methods behind each capability.
