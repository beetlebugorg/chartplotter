---
id: plugins-getting-started
title: Getting Started
sidebar_position: 2
---

# Getting started

This page walks a minimal host-side plugin from source to running, end to end.
We'll build a Tier-A (WASM) plugin that publishes a fixed vessel position, run
it with the live dev harness, then install and enable it.

For a full, realistic plugin see the [tcp-client example](./examples.md); this
page keeps the logic trivial so the mechanics are clear.

## Prerequisites

- Go (the same toolchain the repo pins). The SDK builds unchanged for
  `GOOS=wasip1 GOARCH=wasm`.
- A built `chartplotter` binary (`make build`) for the `chartplotter plugin` verbs.

## 1. Write the plugin

A host-side plugin is a Go `main` package that implements
[`sdk.Plugin`](./sdk.md) and calls `sdk.Run`. Create a directory for it:

```bash
mkdir -p plugins/example.heartbeat
```

`plugins/example.heartbeat/main.go`:

```go
// example.heartbeat — a minimal plugin: on start it publishes one fixed vessel
// position, then reports healthy. Demonstrates the SDK lifecycle and the vessel
// data plane; it does no I/O of its own.
package main

import "github.com/beetlebugorg/chartplotter/sdk"

type heartbeat struct{ h *sdk.Host }

// Start runs once after the handshake, on the read-loop goroutine. It must not
// block — register handlers / kick off work and return.
func (p *heartbeat) Start(h *sdk.Host) {
	p.h = h
	if !h.HasGrant("vessel.write") {
		h.Status("degraded", "vessel.write not granted")
		return
	}
	// Publish a position. Queued now, flushed at the end of this iteration.
	h.PublishVessel(
		sdk.DeltaOf("navigation.position", map[string]float64{"lat": 38.9784, "lon": -76.4922}),
		sdk.DeltaOf("navigation.sog", 0.0),
	)
	h.Status("running", "published home position")
}

func (p *heartbeat) Stop() {}

func main() {
	if err := sdk.Run(&heartbeat{}); err != nil {
		panic(err)
	}
}
```

Key points, expanded in the [SDK reference](./sdk.md):

- The model is **single-threaded and event-driven**. Do not start background
  goroutines or timers — a Tier-A wasip1 module is one cooperatively-scheduled
  thread. Everything happens on the read loop.
- Publishes are **batched**: `PublishVessel`/`PublishAIS`/`PublishRaw` queue,
  and the SDK flushes once per handled host message.
- Only the [writable vessel paths](./sdk.md#vessel-paths) are accepted; unknown
  paths are dropped by the host.

## 2. Write the manifest

`plugins/example.heartbeat/plugin.json` — see the full [manifest reference](./manifest.md):

```jsonc
{
  "manifestVersion": 1,
  "id": "example.heartbeat",
  "name": "Heartbeat",
  "version": "1.0.0",
  "apiVersion": 1,
  "entry": { "wasm": "plugin.wasm" },
  "capabilities": [
    { "cap": "vessel.write" }
  ]
}
```

The `id` must be reverse-DNS (must contain a dot) and must **not** start with
`core.` (reserved for built-ins). `apiVersion` must match the host's — `1` today.

## 3. Build to WASM

Compile the plugin to a `wasip1` module beside its manifest:

```bash
GOOS=wasip1 GOARCH=wasm CGO_ENABLED=0 go build -o plugins/example.heartbeat/plugin.wasm ./plugins/example.heartbeat
```

The in-tree reference plugins use exactly this, wrapped in a Makefile target:

```bash
make build-plugins   # builds the CORE_PLUGINS list to plugin.wasm
```

To add your plugin to that target, append its id to `CORE_PLUGINS` in the
`Makefile`. (`build-plugins` is pure Go, CGO off — it doesn't need the tile57
engine.)

## 4. Run it live with `plugin dev`

`chartplotter plugin dev` runs an **unpacked** directory under the real broker
with auto-restart — no packaging needed. It grants every capability the manifest
declares, so it's the fast iteration loop:

```bash
chartplotter plugin dev plugins/example.heartbeat
```

`dev` prints capability effects to the console. You'll see the vessel delta and
the status update:

```
dev: running plugins/example.heartbeat (Ctrl-C to stop)
plugin example.heartbeat: starting (dir plugins/example.heartbeat)
status[example.heartbeat]: running published home position
vessel[example.heartbeat]: 2 delta(s)
```

Pass runtime config as JSON with `--config`:

```bash
chartplotter plugin dev plugins/example.heartbeat --config '{"greeting":"hi"}'
```

The config map is delivered to your plugin via `h.Config()`.

## 5. Package, install, grant, enable

Once it works under `dev`, package it as a zip and install it into the real
server. Packaging is covered in full on the [packaging page](./packaging.md);
the short version:

1. Compute the sha256 of `plugin.wasm` and list it in the manifest's `files` map.
2. Zip `plugin.json` + `plugin.wasm` (paths relative to the archive root).
3. Install, grant, and enable:

```bash
# Install (verifies the content hashes, unpacks — but leaves it disabled/ungranted)
chartplotter plugin install example.heartbeat-1.0.0.zip

# Grant everything the manifest asks for and enable in one step:
chartplotter plugin install example.heartbeat-1.0.0.zip --grant-all

# Or, after a plain install, enable it (grants are set via the HTTP API / UI):
chartplotter plugin enable example.heartbeat
```

Enable/disable take effect for the **running server** on the next start (the CLI
edits `plugins.json`); when the server itself manages the plugin, enable starts
the runner immediately. See [packaging & CLI](./packaging.md).

## Where to go next

- [SDK reference](./sdk.md) — every `Host` method with examples.
- [Capabilities](./capabilities.md) — what each grant lets you do.
- [Examples](./examples.md) — the full tcp-client NMEA source, walked through.
