---
id: plugins-protocol
title: Wire Protocol
sidebar_position: 6
---

# Wire protocol

The [Go SDK](./sdk.md) is the easy path, but the protocol is a plain,
language-agnostic contract: **newline-delimited JSON-RPC 2.0 over stdio**. Any
language that can read stdin, write stdout, and emit JSON can implement a plugin
directly. This page documents the wire surface **as implemented today** — only
methods the host actually handles are listed.

## Transport & framing

- The host launches the plugin (a WASM module via wazero, or a native child
  process) and connects to its **stdio**: the plugin reads requests on **stdin**
  and writes replies/notifications on **stdout**.
- **Framing is NDJSON**: one JSON-RPC 2.0 object per line, `\n`-terminated. This
  is the only framing negotiated in this build. (`lpbin`, a length-prefixed
  binary framing, is mentioned in the design but **not implemented** — do not
  offer it.)
- **stderr is a log stream**: one record per line. A line that parses as
  `{"level":"…","msg":"…"}` keeps its structure; anything else is logged at
  `info`. The host tags each with your plugin id.
- Binary payloads (TCP/serial bytes) are carried as JSON base64 strings in the
  `data` field.

## Message shapes

Every line is one JSON-RPC 2.0 object. Which of three shapes it is follows from
the fields present:

| Shape | Has `method` | Has `id` | Meaning |
| --- | --- | --- | --- |
| request | yes | yes | expects a matching response |
| notification | yes | no | fire-and-forget |
| response | no | yes | reply to a prior request (`result` xor `error`) |

```jsonc
// request
{ "jsonrpc": "2.0", "id": 1, "method": "host.hello", "params": { … } }
// notification
{ "jsonrpc": "2.0", "method": "vessel.publish", "params": { … } }
// success response
{ "jsonrpc": "2.0", "id": 1, "result": { … } }
// error response
{ "jsonrpc": "2.0", "id": 1, "error": { "code": -32000, "message": "…" } }
```

`id` may be a string or number and round-trips untouched.

### Error codes

| Code | Name | Meaning |
| --- | --- | --- |
| `-32700` | ParseError | malformed JSON |
| `-32600` | InvalidRequest | not a valid request |
| `-32601` | MethodNotFound | unknown method — SDKs surface this as "capability not available" |
| `-32602` | InvalidParams | bad params |
| `-32603` | InternalError | host-side failure (e.g. dial error) |
| `-32000` | CapabilityDenied | a granted-capability check failed |
| `-32001` | ProviderStopped | service call to a stopped provider (Roadmap) |
| `-32002` | HandleUnknown | `io.close` / `*.send` for an unknown handle |

**Forward-compat rule:** ignore unknown *notifications* and unknown *fields*;
answer unknown *methods* with `MethodNotFound`. Additive changes (new methods,
fields, capabilities) do **not** bump the API version.

## The handshake

The **host speaks first**. It sends `host.hello` (a request) offering the API
majors and framings it supports, plus this plugin's current grants and config:

```jsonc
// host → plugin
{ "jsonrpc": "2.0", "id": 1, "method": "host.hello", "params": {
  "apiVersions": [1],
  "pluginId": "com.example.windbridge",
  "grants": [ { "cap": "vessel.write" }, { "cap": "net.tcp-client", "hosts": ["10.0.0.5:2000"] } ],
  "config": { "server": "10.0.0.5:2000" },
  "framing": ["ndjson"]
} }
```

The plugin replies with the major it picked and the framing it will use — it
**must** answer `"ndjson"`:

```jsonc
// plugin → host
{ "jsonrpc": "2.0", "id": 1, "result": { "apiVersion": 1, "framing": "ndjson" } }
```

After the reply, the plugin is live. The host may then send `plugin.ping`
periodically and `plugin.shutdown` to stop it. Grants/config changes arrive at
runtime as a `host.grantsChanged` notification.

## Method tables

Direction: **→** host-to-plugin, **←** plugin-to-host.

### Lifecycle & meta

| Method | Dir | Kind | Params → Result |
| --- | --- | --- | --- |
| `host.hello` | → | req | `HostHello` → `{ apiVersion, framing }` |
| `plugin.ping` | → | req | `{}` → `{ ok: true }` (liveness) |
| `plugin.shutdown` | → | req | `{}` → `{ ok: true }`, then exit |
| `host.grantsChanged` | → | notif | `{ grants, config }` — new grant set + config |
| `config.get` | ← | req | `{}` → the plugin's config object |
| `config.set` | ← | req | `{ key, value }` → `{ ok: true }` (store a plugin-learned value) |
| `status.update` | ← | notif | `{ state, detail?, metrics? }` |

### Vessel / AIS / raw (data plane)

| Method | Dir | Kind | Params | Requires |
| --- | --- | --- | --- | --- |
| `vessel.publish` | ← | notif | `{ deltas: [ { path, value, ts? } ] }` | `vessel.write` |
| `ais.publish` | ← | notif | `{ targets: [ AISTarget ] }` | `ais.write` |
| `raw.publish` | ← | notif | `{ lines: [ string ] }` | `vessel.write` or `ais.write` |

Deltas use the [writable vessel paths](./sdk.md#vessel-paths). An ungranted
notification is silently dropped (no reply channel).

### Transports (host-mediated)

| Method | Dir | Kind | Params → Result | Requires |
| --- | --- | --- | --- | --- |
| `tcp.connect` | ← | req | `{ host, port }` → `{ handle }` | `net.tcp-client` (+ allowlist) |
| `tcp.send` | ← | notif | `{ handle, data(base64), n }` | (open handle) |
| `tcp.data` | → | notif | `{ handle, data(base64), n }` — inbound chunk | |
| `io.close` | ← | req | `{ handle }` → `{ ok: true }` | |
| `io.closed` | → | notif | `{ handle, reason }` — peer/device closed or errored | |

The host reads **chunks** from the socket, not lines — line framing is the
plugin's job.

### Storage

Requires the `storage` capability.

| Method | Dir | Kind | Params → Result |
| --- | --- | --- | --- |
| `storage.get` | ← | req | `{ key }` → `{ value, found }` |
| `storage.set` | ← | req | `{ key, value }` → `{ ok: true }` (or error past quota) |
| `storage.delete` | ← | req | `{ key }` → `{ ok: true }` |
| `storage.list` | ← | req | `{}` → `{ keys: [ string ] }` |

## Roadmap methods

Defined in the protocol constants but **not handled** by this build — do not rely
on them:

- `config.changed` (→ notif) — settings-edited signal; today config changes are
  delivered via `host.grantsChanged` instead.
- `serial.list` (←) — returns an **empty** port list (serial not wired).
- `serial.open` (←) — returns `-32601` "serial transport not available in this build".
- `serial.data` / any serial I/O — not delivered.
- Anything under `net.udp`, `net.http`, `http.register`, or `services.*` — no
  handler exists.

## Minimal example exchange

```jsonc
// ← the plugin answers the handshake
{ "jsonrpc": "2.0", "id": 1, "result": { "apiVersion": 1, "framing": "ndjson" } }
// ← ask the host to dial
{ "jsonrpc": "2.0", "id": 100, "method": "tcp.connect", "params": { "host": "10.0.0.5", "port": 2000 } }
// → host replies with a handle
{ "jsonrpc": "2.0", "id": 100, "result": { "handle": 1 } }
// → inbound bytes arrive
{ "jsonrpc": "2.0", "method": "tcp.data", "params": { "handle": 1, "data": "JEdQR0dBLC4uLg0K", "n": 12 } }
// ← publish parsed vessel state (batched)
{ "jsonrpc": "2.0", "method": "vessel.publish", "params": { "deltas": [
  { "path": "navigation.position", "value": { "lat": 38.97, "lon": -76.49 } },
  { "path": "navigation.sog", "value": 5.4 }
] } }
```

The protocol is fully implementable from this page and the spec's Appendix A.1 —
you do not need the Go SDK. But if you're writing in Go, [use it](./sdk.md).
