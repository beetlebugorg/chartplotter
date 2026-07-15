---
id: plugins-sdk
title: Go SDK
sidebar_position: 5
---

# Go SDK

The `sdk` package (`github.com/beetlebugorg/chartplotter/sdk`) is the thin Go
client for writing host-side plugins. You implement the `Plugin` interface and
call `sdk.Run`; the SDK owns the [handshake](./protocol.md), ping/shutdown,
data-plane batching, and request/response plumbing, so you write only your logic.

The same code builds for **Tier A** (`GOOS=wasip1 GOARCH=wasm`) and **Tier B**
(a normal native build).

## The event-driven, single-threaded model

This is the most important thing to understand about the SDK, and it is a **hard
requirement**, not a style preference:

- A Tier-A wasip1 module is **one cooperatively-scheduled thread**. A blocking
  stdin read halts the whole module.
- Therefore the SDK **never** depends on a background goroutine or timer running
  concurrently with the read loop. Everything happens **on the read loop**.
- Incoming host messages drive your callbacks. Plugin→host **requests resolve
  asynchronously** — you pass a callback that runs later, when the reply arrives.
- Buffered publishes **flush after each handled message**: one chunk of inbound
  bytes → one batched publish. That is exactly the batching the protocol wants.

Practical rules:

- **Do not** start goroutines, `time.Sleep`, `time.Ticker`, or block in `Start`
  or any callback. `Start` must register handlers / kick off async work and
  return.
- Producer plugins are always driven by host-delivered I/O (e.g. TCP data on
  stdin), so per-message flushing covers you.
- Host calls that return data (`TCPConnect`, `StorageGet`, …) are **async**:
  they take a callback that fires on the read loop when the response lands.

## The `Plugin` interface

```go
type Plugin interface {
	// Start runs once after the handshake, on the read-loop goroutine.
	// It must NOT block — register handlers / kick off async connects and return.
	Start(h *Host)
	// Stop is called on graceful shutdown.
	Stop()
}
```

Your `main` hands an instance to `sdk.Run`, which drives the loop until the host
closes stdin or sends `plugin.shutdown`:

```go
func main() {
	if err := sdk.Run(&myPlugin{}); err != nil {
		panic(err)
	}
}
```

## Re-exported types

The SDK re-exports the wire types so you import only `sdk`:

```go
type (
	Capability = plugin.Capability   // { Cap, Hosts, Devices, Quota }
	Delta      = plugin.Delta        // { Path, Value(json.RawMessage), Ts }
	AISTarget  = plugin.AISTargetDTO // MMSI, Lat, Lon, COG, SOG, …
)
```

## `Host` methods

`Host` is your handle to the broker. All methods run on the read loop.

### Config & grants

| Method | Signature | Notes |
| --- | --- | --- |
| `Config` | `Config() map[string]any` | The plugin's current settings. Treat as read-only. |
| `ConfigString` | `ConfigString(key string) string` | A string setting, or `""`. |
| `HasGrant` | `HasGrant(cap string) bool` | Whether the plugin holds a capability. Check before using it. |

```go
func (p *myPlugin) Start(h *sdk.Host) {
	server := h.ConfigString("server")     // e.g. "10.0.0.5:2000"
	if !h.HasGrant("net.tcp-client") {
		h.Status("degraded", "net.tcp-client not granted")
		return
	}
	_ = server
}
```

Config and grants are kept live: when the user edits settings or changes grants,
the SDK updates them from the `host.grantsChanged` notification automatically.

### Data plane (batched)

| Method | Signature | Capability |
| --- | --- | --- |
| `PublishVessel` | `PublishVessel(deltas ...Delta)` | `vessel.write` |
| `PublishAIS` | `PublishAIS(targets ...AISTarget)` | `ais.write` |
| `PublishRaw` | `PublishRaw(lines ...string)` | gated behind `vessel.write`/`ais.write` |
| `DeltaOf` | `DeltaOf(path string, value any) Delta` (package func) | build a delta from any JSON-serialisable value |

These **queue**; the SDK flushes the buffers once per handled host message. You
don't call flush yourself.

```go
h.PublishVessel(
	sdk.DeltaOf("navigation.position", map[string]float64{"lat": 38.97, "lon": -76.49}),
	sdk.DeltaOf("navigation.sog", 5.4),
	sdk.DeltaOf("navigation.cogTrue", 271.0),
)
```

### Status & logging

| Method | Signature | Notes |
| --- | --- | --- |
| `Status` | `Status(state, detail string)` | Report health. `state` is `running` / `degraded` / `error`; surfaces in the plugins/connections UI. |
| `Log` | `Log(level, msg string)` | Structured log line to stderr; the host tags it with your plugin id and level. |

```go
h.Status("running", "connected to "+server)
h.Log("warn", "checksum mismatch, dropping sentence")
```

### Transports (async, host-mediated)

The host owns the socket; you only see bytes. `net.tcp-client` is the only
transport wired today.

```go
type TCPHandlers struct {
	OnConnect func(handle int)
	OnData    func(handle int, data []byte)
	OnError   func(handle int, err error)
}
```

| Method | Signature | Notes |
| --- | --- | --- |
| `TCPConnect` | `TCPConnect(host string, port int, hnd TCPHandlers)` | Asks the host to dial (subject to the `hosts` allowlist). The result → `OnConnect`/`OnError`; inbound chunks → `OnData`; peer close/error → `OnError`. |
| `TCPSend` | `TCPSend(handle int, data []byte)` | Write outbound bytes to a handle. |
| `CloseHandle` | `CloseHandle(handle int)` | Release a transport handle. |

The host delivers **chunks, not lines** — line framing is your job. Buffer across
`OnData` calls:

```go
func (p *client) Start(h *sdk.Host) {
	p.h = h
	host, port := "10.0.0.5", 2000
	h.TCPConnect(host, port, sdk.TCPHandlers{
		OnConnect: func(handle int) { p.handle = handle; h.Status("running", "connected") },
		OnData:    func(_ int, data []byte) { p.onChunk(data) },
		OnError:   func(_ int, err error) { h.Status("degraded", "closed") },
	})
}

func (p *client) onChunk(chunk []byte) {
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

### Storage (async)

Requires the `storage` capability. Values are raw JSON.

| Method | Signature |
| --- | --- |
| `StorageGet` | `StorageGet(key string, cb func(value json.RawMessage, found bool, err error))` |
| `StorageSet` | `StorageSet(key string, value json.RawMessage, cb func(error))` |

```go
h.StorageGet("lastMMSI", func(v json.RawMessage, found bool, err error) {
	if err != nil || !found { return }
	var mmsi uint32
	_ = json.Unmarshal(v, &mmsi)
	// … use mmsi on the read loop …
})

b, _ := json.Marshal(uint32(366123456))
h.StorageSet("lastMMSI", b, func(err error) {
	if err != nil { h.Log("warn", "storage quota exceeded") }
})
```

> **Roadmap:** `StorageDelete` and `StorageList` exist as [protocol methods](./protocol.md)
> but the SDK does not expose helper wrappers yet — use them only if you drop to
> the raw protocol.

## Vessel paths

`PublishVessel` accepts only the **writable** dotted paths below (unknown paths
are dropped by the host). This is the `vessel.write` schema
(`internal/engine/nmea/publish.go`).

| Path | Value type |
| --- | --- |
| `navigation.position` | object `{ "lat": number, "lon": number }` |
| `navigation.cogTrue` | number (degrees) |
| `navigation.sog` | number |
| `navigation.headingTrue` | number |
| `navigation.headingMagnetic` | number |
| `navigation.magneticVariation` | number |
| `navigation.rateOfTurn` | number |
| `navigation.speedThroughWater` | number |
| `navigation.datetime` | RFC 3339 timestamp string |
| `environment.depth.belowTransducer` | number |
| `environment.depth.belowKeel` | number |
| `environment.depth.belowSurface` | number |
| `environment.water.temperature` | number |
| `environment.wind.angleApparent` | number |
| `environment.wind.speedApparent` | number |
| `environment.wind.angleTrue` | number |
| `environment.wind.speedTrue` | number |
| `environment.wind.directionTrue` | number |
| `route.xte` | number |
| `route.bearingToWaypoint` | number |
| `route.distanceToWaypoint` | number |
| `route.activeWaypoint` | string |

Writes are **attributed** to your plugin id, so the app can show provenance and
arbitrate against built-in NMEA sources (latest-wins per path today).

## Building

```bash
# Tier A (WASM, preferred)
GOOS=wasip1 GOARCH=wasm CGO_ENABLED=0 go build -o plugin.wasm ./plugins/<id>

# Tier B (native) — a normal build for the target platform
go build -o bin/<id>-linux-amd64 ./plugins/<id>
```

## See also

- [Getting started](./getting-started.md) — end-to-end build & run.
- [Protocol](./protocol.md) — the wire methods the SDK wraps.
- [Examples](./examples.md) — the tcp-client plugin, walked through.
