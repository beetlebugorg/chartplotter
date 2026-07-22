---
id: plugins-overview
title: Plugins Overview
sidebar_position: 1
---

# Plugins

Plugins extend chartplotter with new **data sources** (a NMEA feed, a sensor
bridge) and new **UI** (a map overlay, a HUD widget) without forking the app.
A plugin is a small, self-contained package the host installs, verifies, and
runs under an explicit set of **capabilities** the user grants.

This section documents what the plugin system does **today**. The design spec
(`specs/plugin-system.md`) describes a larger surface; features that are not yet
built are called out as **Roadmap** on each page so you never write against an
API that isn't there.

## Two tiers

A host-side plugin ships its logic as one of two runtimes. Both speak the same
[wire protocol](./protocol.md) over stdio, so the code and the [Go SDK](./sdk.md)
are identical between them — only the packaging differs.

| Tier | Runtime | Sandbox | When |
| --- | --- | --- | --- |
| **A — WASM** (default) | `wasip1` module run in-process by [wazero](https://wazero.io) | Strong: the module's only syscall surface is stdio + a coarse clock. No filesystem, no network, no random source. | The default and recommended tier. Portable — one `plugin.wasm` runs on every host OS/arch. |
| **B — Native** (escape hatch) | A native executable the host spawns as a child process | Weak in this build: no OS-level sandbox yet. Trusted like any local binary. | Only when a plugin needs a native library WASM can't reach. Per-platform binaries. |

Tier A is preferred because the module is contained: it cannot open a file or a
socket on its own. Every effect — publishing vessel data, dialing a TCP server,
reading storage — flows through a host-mediated capability, so the host is always
in the loop. See the [capability model](./capabilities.md).

There is also a third, purely front-end kind of plugin — a **UI plugin** — that
ships no host-side code at all, only a browser controller. The built-in own-ship
and AIS overlays are UI plugins. See [UI plugins](./ui.md).

## The capability model

Everything a plugin can do is **opt-in**. A manifest *declares* the capabilities
it wants; the user *grants* a subset at install time; and the host **mediates**
every use of a granted capability at runtime. A plugin with no grants can do
nothing but exist.

Capabilities that are enforced today:

- `vessel.write` — publish SignalK-style vessel deltas into the shared state.
- `ais.write` — publish AIS target updates.
- `net.tcp-client` — ask the host to dial an allow-listed TCP `host:port`. The
  host owns the socket; the plugin only ever sees bytes.
- `storage` — a per-plugin key/value store with a byte quota.

`raw.publish` (feeding the raw-sentence sniffer) is gated behind holding
`vessel.write` or `ais.write`. Config read/write and status reporting are always
available to a running plugin.

Declared but **not yet enforced or wired**: `serial` (device I/O returns "not
available in this build"), `net.udp`, `net.http`, `http.register`, and the
`ui.*` capabilities. See the [capability reference](./capabilities.md) for the
full table and status of each.

## The trust & security model

- **Content-hash packaging.** A plugin archive's `plugin.json` carries a `files`
  map of `path → sha256` covering every other file. Verifying the manifest
  verifies the whole zip. This is implemented today.
- **Signing is Roadmap.** ed25519 signatures + trust-on-first-use (TOFU) key
  pinning are **not implemented**. v1 does content-hash verification only; there
  is no publisher-identity check.
- **WASM containment (Tier A).** A wasip1 module has no ambient authority beyond
  stdio and a clock. This is the real sandbox in this build.
- **Native plugins are not sandboxed.** A Tier-B binary runs with your user's
  privileges. OS-level native sandboxing is Roadmap. Install native plugins only
  from sources you trust.
- **UI plugins run trusted in the main document.** There is no iframe/browser
  sandbox around a UI controller; the security gate is at install time, not in
  the browser. UI plugins can only touch the map through the declarative `ctx`,
  which keeps them off safety-critical S-52 layers, but they share the page.
- **`core.*` is reserved.** Ids under the `core.` prefix are the host's built-in
  plugins; third-party archives claiming that prefix are rejected by the installer.
- **Circuit breaker.** A host-side plugin that crashes 5 times within 2 minutes
  is auto-disabled.

## Where plugins live on disk

Installed plugins live under the server's data directory (`<dataDir>`, the XDG
data dir by default):

```
<dataDir>/
  plugins.json                     # install + grant state (enabled, grants, config)
  plugins/
    <id>/
      <version>/                   # the unpacked archive (plugin.json, plugin.wasm, ui/…)
      data/                        # per-plugin persistent storage (survives upgrades)
        storage.json               # the `storage` capability's KV file
```

- `plugins.json` is the state of record: which plugins are installed, enabled,
  and what each was granted. See [packaging](./packaging.md).
- Each `<id>/<version>/` directory is a verified, unpacked archive.
- `<id>/data/` persists across version upgrades and is only deleted with
  `--purge-data`.

## Read next

- [Getting started](./getting-started.md) — write, build, and run your first plugin.
- [Manifest reference](./manifest.md) — every field of `plugin.json`.
- [Capabilities](./capabilities.md) — the full capability table.
- [Go SDK](./sdk.md) — the `Plugin` interface and every `Host` method.
- [Wire protocol](./protocol.md) — for authors not using the Go SDK.
- [UI plugins](./ui.md) — the `ctx` surface for browser controllers.
- [Packaging & CLI](./packaging.md) — archives, install, and the CLI verbs.
- [Examples](./examples.md) — complete, runnable worked examples.
