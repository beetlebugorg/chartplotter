// Package plugin implements the chartplotter plugin engine: the host side of the
// plugin protocol (specs/plugin-system.md). A plugin is an out-of-process (native)
// or in-process-sandboxed (WASM/wazero) program that speaks newline-delimited
// JSON-RPC 2.0 over stdio against the host Broker. Everything a plugin can do is a
// capability granted at install time and mediated here; a sandboxed WASM plugin has
// no syscall surface beyond stdio + a coarse clock, so the host opens the socket and
// the plugin only ever sees bytes.
//
// This file defines the wire protocol: the JSON-RPC message envelope, the method
// name constants for every RPC in Appendix A.1 of the spec, the handshake payloads,
// and the typed params for the Phase-1 surface (vessel/ais/raw publish, status,
// host-mediated TCP/serial/storage, config). Framing + the read/write loop live in
// session.go; capability enforcement in broker.go/capabilities.go.
package plugin

import (
	"encoding/json"
	"fmt"
)

// APIVersion is the plugin protocol + capability-schema major this host speaks.
// Additive changes (new methods/fields/capabilities) do not bump it; plugins must
// ignore unknown notifications/fields and the host answers unknown methods with
// MethodNotFound. See spec §5.
const APIVersion = 1

// jsonrpcVersion is the required "jsonrpc" field value on every message.
const jsonrpcVersion = "2.0"

// Message is a single JSON-RPC 2.0 object — one per NDJSON line. It is a union of
// the three shapes the transport carries; which one it is follows from the fields
// present:
//
//   - request:      Method set, ID set        (expects a matching response)
//   - notification: Method set, ID absent      (fire-and-forget)
//   - response:     ID set, Result xor Error   (reply to a request)
//
// ID is kept as raw bytes so a peer's string-or-number id round-trips untouched;
// Params/Result stay raw so routing can decode into the concrete type lazily.
type Message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// isRequest reports whether m carries a method call awaiting a reply.
func (m *Message) isRequest() bool { return m.Method != "" && len(m.ID) > 0 }

// isNotification reports whether m is a fire-and-forget method call.
func (m *Message) isNotification() bool { return m.Method != "" && len(m.ID) == 0 }

// isResponse reports whether m is a reply to a prior request.
func (m *Message) isResponse() bool { return m.Method == "" && len(m.ID) > 0 }

// RPCError is the JSON-RPC 2.0 error object.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string { return fmt.Sprintf("rpc %d: %s", e.Code, e.Message) }

// JSON-RPC error codes: the standard set plus chartplotter capability codes. The
// SDK surfaces MethodNotFound as "capability not available" (spec §5); CapabilityDenied
// is returned when a plugin invokes a host method it was not granted (spec §6).
const (
	CodeParseError       = -32700
	CodeInvalidRequest   = -32600
	CodeMethodNotFound   = -32601
	CodeInvalidParams    = -32602
	CodeInternalError    = -32603
	CodeCapabilityDenied = -32000 // granted-capability check failed
	CodeProviderStopped  = -32001 // service call to a stopped/disabled provider (§7)
	CodeHandleUnknown    = -32002 // io.close / *.send referencing an unknown handle
)

// Method names. Direction in the comments is host→plugin (→) or plugin→host (←),
// matching Appendix A.1. Domain prefixes (host/plugin/vessel/ais/serial/tcp/storage/
// config/…) are reserved for the host (§7 "Reserved for the host").
const (
	// Lifecycle & meta.
	MethodHostHello      = "host.hello"         // → req: handshake
	MethodPluginPing     = "plugin.ping"        // → req: liveness
	MethodPluginShutdown = "plugin.shutdown"    // → req: graceful stop
	MethodGrantsChanged  = "host.grantsChanged" // → notif: new grant set + config
	MethodConfigChanged  = "config.changed"     // → notif: settings edited
	MethodConfigGet      = "config.get"         // ← req: own settings
	MethodConfigSet      = "config.set"         // ← req: plugin-learned values
	MethodStatusUpdate   = "status.update"      // ← notif: {state, detail, metrics}

	// Vessel / AIS / raw.
	MethodVesselPublish = "vessel.publish" // ← notif: SignalK-style deltas
	MethodAISPublish    = "ais.publish"    // ← notif: AIS target updates
	MethodRawPublish    = "raw.publish"    // ← notif: raw sentences → sniffer

	// Transports (host-mediated).
	MethodTCPConnect = "tcp.connect" // ← req:  → {handle}
	MethodTCPSend    = "tcp.send"    // ← notif: outbound bytes
	MethodTCPData    = "tcp.data"    // → notif: inbound chunk {handle,data,n}
	MethodSerialList = "serial.list" // ← req: enumerate granted ports
	MethodSerialOpen = "serial.open" // ← req:  → {handle}
	MethodSerialData = "serial.data" // → notif: inbound chunk
	MethodIOClose    = "io.close"    // ← req: release a handle
	MethodIOClosed   = "io.closed"   // → notif: peer/device closed or errored

	// Storage.
	MethodStorageGet    = "storage.get"    // ← req
	MethodStorageSet    = "storage.set"    // ← req
	MethodStorageDelete = "storage.delete" // ← req
	MethodStorageList   = "storage.list"   // ← req

	// Served artifacts: publish a blob at GET /plugins/<id>/serve/<name> (§4).
	MethodServeSet   = "serve.set"   // ← req: {name, data} → served file
	MethodServeClear = "serve.clear" // ← req: {name}

	// Outbound HTTP (host-mediated, allowlisted).
	MethodHTTPFetch = "http.fetch" // ← req: request/response
)

// --- handshake (spec §4) ---------------------------------------------------

// HostHello is the host.hello params: the host speaks first, offering the API
// majors and framings it supports plus the plugin's current grants and config.
type HostHello struct {
	APIVersions []int             `json:"apiVersions"`
	PluginID    string            `json:"pluginId"`
	Grants      []Capability      `json:"grants"`
	Config      map[string]any    `json:"config"`
	Framing     []string          `json:"framing"` // e.g. ["ndjson","lpbin"]
	Env         map[string]string `json:"env,omitempty"`
}

// HelloResult is the plugin's reply: the major it picked and the framing it will use.
type HelloResult struct {
	APIVersion int    `json:"apiVersion"`
	Framing    string `json:"framing"`
}

// GrantsChanged is the host.grantsChanged notification: grants can change at runtime
// without a restart (spec §4).
type GrantsChanged struct {
	Grants []Capability   `json:"grants"`
	Config map[string]any `json:"config"`
}

// --- data plane ------------------------------------------------------------

// Delta is a SignalK-style path/value update applied to the shared VesselState.
// Paths are the dotted form the host validates against nmea/state.go (see
// vesselPaths in capabilities.go); Value is the raw JSON scalar/object.
type Delta struct {
	Path  string          `json:"path"`
	Value json.RawMessage `json:"value"`
	Ts    json.RawMessage `json:"ts,omitempty"`
}

// VesselPublish is the vessel.publish params: a batch of deltas (spec §4 batching).
type VesselPublish struct {
	Deltas []Delta `json:"deltas"`
}

// AISPublish is the ais.publish params: a batch of decoded target updates.
type AISPublish struct {
	Targets []AISTargetDTO `json:"targets"`
}

// AISTargetDTO is the wire shape of an AIS target update; it maps onto nmea.AISTarget
// in the broker (capabilities.go). Optional numeric fields are pointers so "unknown"
// is distinct from zero.
type AISTargetDTO struct {
	MMSI        uint32   `json:"mmsi"`
	Lat         float64  `json:"lat"`
	Lon         float64  `json:"lon"`
	COG         *float64 `json:"cog,omitempty"`
	SOG         *float64 `json:"sog,omitempty"`
	Heading     *float64 `json:"heading,omitempty"`
	Name        string   `json:"name,omitempty"`
	CallSign    string   `json:"callSign,omitempty"`
	ShipType    int      `json:"shipType,omitempty"`
	TypeName    string   `json:"typeName,omitempty"`
	Destination string   `json:"destination,omitempty"`
	Length      int      `json:"length,omitempty"`
	Beam        int      `json:"beam,omitempty"`
	Draught     *float64 `json:"draught,omitempty"`
	Status      string   `json:"status,omitempty"`
	Class       string   `json:"class,omitempty"`
}

// RawPublish is the raw.publish params: raw sentence lines for the sniffer.
type RawPublish struct {
	Lines []string `json:"lines"`
}

// StatusUpdate is the status.update params, mirroring nmea.SourceStatus (spec §4);
// the broker maps State (running|degraded|error) onto the connections UI enum.
type StatusUpdate struct {
	State   string         `json:"state"`
	Detail  string         `json:"detail,omitempty"`
	Metrics map[string]any `json:"metrics,omitempty"`
}

// --- transports ------------------------------------------------------------

// TCPConnect is the tcp.connect params: the host dials, subject to the net.tcp-client
// allowlist (spec §6). Returns HandleResult.
type TCPConnect struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

// SerialOpen is the serial.open params: the host owns the fd; the device must be in
// the granted set (spec §6).
type SerialOpen struct {
	Device string `json:"device"`
	Baud   int    `json:"baud"`
}

// HandleResult is the reply to tcp.connect / serial.open: an opaque per-plugin handle.
type HandleResult struct {
	Handle int `json:"handle"`
}

// IOData is a serial.data / tcp.data notification: inbound bytes for a handle, base64
// in ndjson framing (spec §4). Also the tcp.send / serial.write outbound shape.
type IOData struct {
	Handle int    `json:"handle"`
	Data   []byte `json:"data"` // encoding/json base64-encodes []byte automatically
	N      int    `json:"n,omitempty"`
}

// IOClose / IOClosed reference a handle by number.
type IOHandle struct {
	Handle int    `json:"handle"`
	Reason string `json:"reason,omitempty"` // set on io.closed
}

// --- storage ---------------------------------------------------------------

// StorageKey is the storage.get / storage.delete params.
type StorageKey struct {
	Key string `json:"key"`
}

// StorageSet is the storage.set params.
type StorageSet struct {
	Key   string          `json:"key"`
	Value json.RawMessage `json:"value"`
}

// StorageValue is the storage.get reply.
type StorageValue struct {
	Value json.RawMessage `json:"value"`
	Found bool            `json:"found"`
}

// StorageList is the storage.list reply.
type StorageList struct {
	Keys []string `json:"keys"`
}

// ServeSet publishes a blob at GET /plugins/<id>/serve/<name> (spec §4 "Served
// artifacts"): the zero-RPC path for tile archives, weather grids, and other static
// products. Data is base64 in ndjson framing.
type ServeSet struct {
	Name string `json:"name"`
	Data []byte `json:"data"`
}

// ServeClear removes a published artifact.
type ServeClear struct {
	Name string `json:"name"`
}

// HTTPFetch is the http.fetch params (spec §4): a host-mediated outbound request,
// allowlisted by the net.http grant and size-capped.
type HTTPFetch struct {
	URL     string            `json:"url"`
	Method  string            `json:"method,omitempty"` // default GET
	Headers map[string]string `json:"headers,omitempty"`
	Body    []byte            `json:"body,omitempty"`
}

// HTTPResponse is the http.fetch reply.
type HTTPResponse struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    []byte            `json:"body"`
}

// --- message constructors --------------------------------------------------

// newRequest builds a request message with id and marshaled params.
func newRequest(id int64, method string, params any) (*Message, error) {
	p, err := marshalParams(params)
	if err != nil {
		return nil, err
	}
	return &Message{JSONRPC: jsonrpcVersion, ID: marshalID(id), Method: method, Params: p}, nil
}

// newNotification builds a fire-and-forget message (no id).
func newNotification(method string, params any) (*Message, error) {
	p, err := marshalParams(params)
	if err != nil {
		return nil, err
	}
	return &Message{JSONRPC: jsonrpcVersion, Method: method, Params: p}, nil
}

// newResult builds a success response for the request id.
func newResult(id json.RawMessage, result any) (*Message, error) {
	r, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	return &Message{JSONRPC: jsonrpcVersion, ID: id, Result: r}, nil
}

// newErrorResponse builds an error response for the request id.
func newErrorResponse(id json.RawMessage, code int, msg string) *Message {
	return &Message{JSONRPC: jsonrpcVersion, ID: id, Error: &RPCError{Code: code, Message: msg}}
}

func marshalID(id int64) json.RawMessage {
	b, _ := json.Marshal(id)
	return b
}

// marshalParams marshals params, treating nil as absent.
func marshalParams(params any) (json.RawMessage, error) {
	if params == nil {
		return nil, nil
	}
	return json.Marshal(params)
}
