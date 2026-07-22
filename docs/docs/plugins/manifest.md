---
id: plugins-manifest
title: Manifest (plugin.json)
sidebar_position: 3
---

# Manifest reference

Every plugin has a `plugin.json` at its archive root. It is the
**content-addressed root** of the package: its `files` map lists every other
archive file with its sha256, so verifying the manifest verifies the whole zip.

The manifest is parsed with **unknown fields rejected** — a typo'd key is an
install error, not a silent no-op. The schema below is exactly the Go
`Manifest` struct (`internal/engine/plugin/manifest.go`).

## Full annotated example

```jsonc
{
  // Schema version of this file. Must be 1.
  "manifestVersion": 1,

  // Reverse-DNS id: [a-z0-9.-], must contain a dot, no leading/trailing dot.
  // Must NOT start with "core." (reserved for built-ins).
  "id": "com.example.windbridge",

  // Human-facing name shown in the plugins UI / CLI list.
  "name": "Wind Bridge",

  // Semver MAJOR.MINOR.PATCH, optional -pre / +build.
  "version": "1.2.0",

  // Optional metadata.
  "description": "Publishes true/apparent wind from a serial anemometer.",
  "publisher": "example.com",
  "license": "MIT",
  "homepage": "https://example.com/windbridge",

  // Plugin protocol + capability-schema major. Must equal the host's (1 today);
  // a mismatch is rejected at install/parse time.
  "apiVersion": 1,

  // Runtime entry points. At least one of wasm / native is required for a
  // host-side plugin; a pure-UI plugin may carry only `ui` instead.
  "entry": {
    // Tier A: the wasip1 module path within the archive (preferred).
    "wasm": "plugin.wasm",
    // Tier B: per-platform native binaries, keyed "<goos>-<goarch>".
    "native": {
      "linux-amd64": "bin/windbridge-linux-amd64",
      "darwin-arm64": "bin/windbridge-darwin-arm64"
    }
  },

  // Capabilities the plugin REQUESTS. The user grants a subset at install time.
  // See the capability reference for each cap's parameters.
  "capabilities": [
    { "cap": "vessel.write" },
    { "cap": "net.tcp-client", "hosts": ["*.example.com:2000", "${config:host}"] },
    { "cap": "storage", "quota": "2MB" }
  ],

  // Optional UI contribution (see the UI plugins page).
  "ui": {
    "entry": "ui/index.mjs",
    "settings": { "type": "object", "properties": { "host": { "type": "string" } } },
    "panels":    [{ "id": "wind", "title": "Wind", "icon": "wind" }],
    "mapLayers": [{ "id": "wind-arrow", "title": "Wind arrow" }],
    "hud":       [{ "id": "wind-hud", "title": "Wind HUD" }]
  },

  // Service declarations (Roadmap — see notes below).
  "provides": [{ "service": "nmea.source", "apiVersion": 1 }],
  "consumes": [],

  // Content-hash map: path -> sha256 for every OTHER file in the archive.
  // Verifying the manifest against these verifies the whole zip.
  "files": {
    "plugin.wasm": "sha256:9f2b…",
    "ui/index.mjs": "3ab1…"
  }
}
```

## Field reference

### Top level

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `manifestVersion` | int | yes | Must be `1`. |
| `id` | string | yes | Reverse-DNS, `[a-z0-9.-]`, must contain a dot, no leading/trailing dot. `core.*` is rejected for third-party archives. |
| `name` | string | — | Display name. |
| `version` | string | yes | Semver `MAJOR.MINOR.PATCH` with optional `-pre`/`+build`. |
| `description` | string | — | Free text. |
| `publisher` | string | — | Informational only today (no signing/identity check). |
| `license` | string | — | SPDX id or free text. |
| `homepage` | string | — | URL. |
| `apiVersion` | int | yes | Must equal the host's `APIVersion` (**1**). Mismatch → install error. |
| `entry` | object | yes* | Runtime entry points. Required unless the manifest is UI-only. |
| `capabilities` | array | — | Requested capabilities (see below). |
| `ui` | object | — | UI contribution (see [UI plugins](./ui.md)). |
| `provides` | array | — | Service declarations. `nmea.source` is live (see below); others are roadmap. |
| `consumes` | array | — | Service dependencies. **Roadmap**. |
| `files` | object | — | `path → sha256` for every other archive file. Verified on install. |

\* Validation requires at least one of `entry.wasm`, a non-empty `entry.native`,
or a `ui` block — a manifest with none is rejected ("declares no entry point").

### `entry`

| Field | Type | Notes |
| --- | --- | --- |
| `wasm` | string | Tier A. Path to the `wasip1` module in the archive. Preferred; portable. |
| `native` | object | Tier B. `"<goos>-<goarch>" → path`, e.g. `"linux-amd64"`. The host picks the entry matching its platform. |

The host selects **WASM by default**, falling back to (or forced onto) native
only when there is no `wasm` entry or `forceNative` is set in the plugin's state
record. Files under `bin/` in the archive get the executable bit on unpack.

### `capabilities[]`

Each entry is a `Capability` object. `cap` is required; the other fields are
parameters whose applicability depends on `cap`:

| Field | Type | Applies to | Notes |
| --- | --- | --- | --- |
| `cap` | string | all | The capability name. Empty `cap` is a validation error. |
| `hosts` | string[] | `net.*` | Allowlist of `host` or `host:port` patterns. Supports a `*.` wildcard prefix and `${config:key}` substitution. |
| `devices` | string[] | `serial` | Device allowlist (resolved at grant). **Serial is Roadmap.** |
| `quota` | string | `storage` | Byte quota, e.g. `"10MB"`, `"512KB"`, `"1024"`. Default 5 MiB if omitted. |

See the [capability reference](./capabilities.md) for what each cap grants and
which are enforced today.

### `ui`

| Field | Type | Notes |
| --- | --- | --- |
| `entry` | string | Path to the UI controller module (served at `/plugins/<id>/ui/…`). |
| `settings` | JSON | Settings schema/descriptor for the plugin's config form. |
| `panels` | UISlot[] | Declared panel slots. |
| `mapLayers` | UISlot[] | Declared map-layer slots. |
| `hud` | UISlot[] | Declared HUD slots. |

A `UISlot` is `{ "id": string, "title"?: string, "icon"?: string }`. See
[UI plugins](./ui.md).

## Roadmap fields

These fields parse and validate today but are **not acted on** by the current
host:

### `provides: nmea.source` — data-source plugins own a connection type

A plugin declaring `provides: [{ "service": "nmea.source" }]` is treated as a
**connection type**, and the app routes its configuration through the
Connections UI rather than a generic settings form:

- its row in Settings → Plugins shows a **Connections** button that drills into
  the connections view (status badge, pause/resume, raw-sentence sniffer, edit);
- its `ui.settings.items` schema becomes the connection form (text/number
  fields), offered in "+ Add connection" alongside the built-in TCP client;
- pausing the connection disables the plugin, and the host clears every vessel
  reading the plugin wrote (no phantom own-ship);
- the raw sentences it publishes (`PublishRaw`) feed the same sniffer stream as
  built-in connections.

Declare it plus `vessel.write` / `ais.write` capabilities and publish deltas —
the shell does the rest.

- **`provides` / `consumes`** — service declarations for cross-plugin
  `services.*` calls. The `services.*` call surface is not implemented; declaring
  a service has no runtime effect yet. (The reference plugin declares
  `provides: [{ "service": "nmea.source" }]` as forward-looking metadata only.)
- **Signing** — there is no `signature`/key field consumed today. ed25519
  signing + TOFU key pinning are Roadmap; `plugins.json` reserves a `pinnedKey`
  slot but nothing populates it.

## See also

- [Capabilities](./capabilities.md)
- [Packaging & CLI](./packaging.md) — how `files` hashes are verified on install.
- [UI plugins](./ui.md)
