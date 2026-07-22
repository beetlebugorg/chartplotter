---
id: plugins-packaging
title: Packaging & CLI
sidebar_position: 8
---

# Packaging, installing & the CLI

This page covers turning a plugin into an installable archive, the install/verify
flow, and the full `chartplotter plugin` CLI and HTTP surface.

## The archive

A plugin ships as a **zip** whose root contains `plugin.json` plus every file it
references. The manifest's `files` map (`path → sha256`) covers every **other**
file in the archive, so verifying the manifest verifies the whole zip.

```
myplugin-1.0.0.zip
├── plugin.json          # the manifest, with a `files` sha256 map
├── plugin.wasm          # Tier-A entry (entry.wasm)
├── bin/                 # Tier-B entries (entry.native), if any — get the exec bit
│   └── myplugin-linux-amd64
└── ui/                  # UI controller + assets (ui.entry), if any
    └── index.mjs
```

- Files listed in `files` **must** be present with a matching sha256, or install
  fails. Files present but **not** listed are allowed (e.g. a future `plugin.sig`).
- Paths are archive-relative; traversal (`..`) is rejected on unpack.
- Files under `bin/` are unpacked executable (`0755`); everything else `0644`.

### Computing the hashes

The `files` values are lowercase hex sha256 (an optional `sha256:` prefix is
accepted). For example:

```bash
sha256sum plugin.wasm ui/index.mjs
```

Put each digest in the manifest:

```jsonc
"files": {
  "plugin.wasm": "9f2b…",
  "ui/index.mjs": "sha256:3ab1…"
}
```

Then zip the tree (manifest at the root):

```bash
zip -r myplugin-1.0.0.zip plugin.json plugin.wasm ui
```

### The `core.*` reservation

Ids under the `core.` prefix are reserved for the host's built-in plugins. A
third-party archive whose `id` starts with `core.` is **rejected by the
installer**. (In-tree tooling can opt in via an internal `AllowCore` flag; the
public install paths never do.)

### Signing — Roadmap

There is **no signing** in this build. Install-time verification is
**content-hash only**. ed25519 signatures + trust-on-first-use (TOFU) key pinning
are planned; `plugins.json` reserves a `pinnedKey` field, but nothing populates
or checks it yet. Treat a plugin archive with the same trust you'd give any
downloaded binary.

## Installing

Two front doors, same engine underneath:

### Via the CLI

```bash
chartplotter plugin install myplugin-1.0.0.zip
```

This **verifies** (hash check), **unpacks** to
`<dataDir>/plugins/<id>/<version>/`, and records the plugin in `plugins.json` as
**disabled and ungranted** — so you can review its requested capabilities before
it runs. It prints the requested capabilities and the next step.

Add `--grant-all` to grant every capability the manifest requests and enable in
one shot (handy for dev / trusted plugins):

```bash
chartplotter plugin install myplugin-1.0.0.zip --grant-all
```

### Via the web UI

Upload the zip through the app's plugins UI, which POSTs it to
`/api/plugins/install`. Same verify + unpack; grants are then set through the UI.

## Enabling & granting

- **Enable/disable** flips the `enabled` flag and (when the server owns the
  plugin) starts/stops its runner. Via the CLI the change lands in `plugins.json`
  and applies on the next server start.
- **Grants** are the user-approved subset of the manifest's capabilities. They're
  set through the HTTP API / UI (`PUT /api/plugins/<id>/grants`), or all at once
  with `install --grant-all`. Grants can change at runtime; a running host-side
  plugin is updated live via `host.grantsChanged` (no restart).

## Removing

```bash
chartplotter plugin remove <id>              # stop, disable, delete the unpacked versions
chartplotter plugin remove <id> --purge-data # also delete the plugin's stored data
```

Without `--purge-data`, the plugin's `data/` directory (its `storage` KV,
learned config) is kept so a reinstall resumes where it left off.

## CLI reference

The `chartplotter plugin` verb group operates on `<dataDir>/plugins.json` and the
unpacked archives. The CLI is a **state-only** manager (it never spawns runners);
the running server is what actually runs plugins. `enable`/`disable`/`remove`
therefore say "restart the server to apply".

| Verb | Args | Flags | Does |
| --- | --- | --- | --- |
| `install` | `<archive.zip>` | `--data <dir>`, `--grant-all` | Verify + unpack + record (disabled). `--grant-all` grants all requested caps and enables. |
| `list` | — | `--data <dir>` | List installed plugins: id, version, state, name. |
| `enable` | `<id>` | `--data <dir>` | Mark enabled. |
| `disable` | `<id>` | `--data <dir>` | Mark disabled. |
| `remove` | `<id>` | `--data <dir>`, `--purge-data` | Uninstall; `--purge-data` also deletes stored data. |
| `dev` | `<dir>` | `--config <json>` | Run an **unpacked** directory under the real broker with auto-restart. Grants everything the manifest declares. Prints capability effects. |

`--data` overrides the data directory (default: the XDG data dir). `dev`'s
`--config` takes the plugin's runtime config as JSON, e.g.
`--config '{"host":"127.0.0.1","port":10110}'`.

```bash
chartplotter plugin list
# example.heartbeat                v1.0.0   enabled    Heartbeat
# com.example.windbridge           v1.2.0   disabled   Wind Bridge
```

## HTTP API reference

The server exposes the management surface under `/api/plugins`, plus per-plugin
static serving. (These back the web UI.)

| Method & path | Does |
| --- | --- |
| `GET /api/plugins` | List installed plugins with manifest + grant state + live status. |
| `POST /api/plugins/install` | Multipart upload (form field `plugin`) → verify → unpack. |
| `GET /api/plugins/stream` | SSE: pushes the plugin status map when it changes. |
| `POST /api/plugins/<id>/enable` | Enable + start the runner. |
| `POST /api/plugins/<id>/disable` | Disable + stop the runner. |
| `PUT`/`POST` `/api/plugins/<id>/grants` | Body `{ grants, config }` — set the grant set (and optionally config); hot-applied to a running plugin. |
| `PUT`/`POST` `/api/plugins/<id>/config` | Body: a config map — update config only, keeping grants. |
| `DELETE /api/plugins/<id>` | Uninstall; `?purgeData=1` also deletes stored data. |
| `GET /plugins/<id>/ui/*` | Serve the plugin's unpacked `ui/` directory (UI controller + assets). |
| `GET /plugins/<id>/serve/*` | Serve runtime-published artifacts from the plugin's `data/serve/` directory. |

## State: `plugins.json`

Install/grant state lives in `<dataDir>/plugins.json` as a sorted JSON array of
records:

```jsonc
[
  {
    "id": "com.example.windbridge",
    "version": "1.2.0",           // the active unpacked version
    "enabled": true,
    "grants": [                   // the user-approved subset of the manifest caps
      { "cap": "vessel.write" },
      { "cap": "net.tcp-client", "hosts": ["10.0.0.5:2000"] }
    ],
    "config": { "server": "10.0.0.5:2000" },
    "forceNative": false,         // prefer the native entry over wasm
    "pinnedKey": ""               // Roadmap: TOFU publisher key (unused today)
  }
]
```

The manifest itself is **not** stored here — it lives with the unpacked archive
on disk and is re-read as needed.

## Lifecycle & resilience

- A host-side plugin runs under a supervised runner: it's (re)started with a
  1 s → 30 s backoff if it exits, pinged every 15 s (3 missed pings → restart),
  and asked to shut down gracefully (5 s grace) on stop.
- **Circuit breaker:** 5 crashes within 2 minutes auto-disables the plugin and
  surfaces an error status.
- **UI-only plugins** (no `wasm`/`native` entry) have no host runner — enabling
  them is purely a state flag; the frontend loads the controller.

## See also

- [Manifest](./manifest.md) — the `files` map and `id` rules.
- [Capabilities](./capabilities.md) — what you're granting.
- [Getting started](./getting-started.md) — the `dev` loop.
