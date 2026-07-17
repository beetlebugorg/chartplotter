// Package sdk is the thin Go client for writing chartplotter plugins (spec §11). A
// plugin implements Plugin and calls Run; the SDK owns the handshake, ping/shutdown,
// data-plane batching (spec §10), and request/response plumbing, so an author writes
// only their logic. It builds unchanged for `GOOS=wasip1 GOARCH=wasm` (Tier A) and a
// normal native build (Tier B).
//
// The model is single-threaded and event-driven — a hard requirement for Tier A: a
// wasip1 module is one cooperatively-scheduled thread, and a blocking stdin read
// halts it, so the SDK must never depend on a background goroutine or timer running
// concurrently with the read loop. Everything happens on the read loop: incoming host
// messages drive callbacks, plugin→host requests resolve asynchronously when their
// reply arrives, and buffered publishes flush after each message is handled (a chunk
// of inbound bytes → one batched publish, which is exactly the batching the spec
// asks for). Producer plugins are always driven by host-delivered I/O (tcp/serial
// data on stdin), so per-message flushing is sufficient.
//
// For the in-tree reference plugin this SDK reuses the host's wire types from
// internal/engine/plugin (DRY); extracting a standalone module for third-party
// authors is Phase-3 polish behind the same JSON contract.
package sdk

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sync/atomic"

	proto "github.com/beetlebugorg/chartplotter/internal/engine/plugin"
)

// Re-exported wire types so plugin authors import only the SDK.
type (
	Capability   = proto.Capability
	Delta        = proto.Delta
	AISTarget    = proto.AISTargetDTO
	HTTPResponse = proto.HTTPResponse
)

// Plugin is the interface an author implements. Start runs once after the handshake,
// on the read-loop goroutine — it must NOT block (register handlers / kick off async
// connects and return). Stop is called on graceful shutdown.
type Plugin interface {
	Start(h *Host)
	Stop()
}

// ConfigWatcher is optionally implemented by plugins that react to live settings
// edits (the host hot-applies config without a restart). Called on the read-loop
// goroutine after Host.Config() reflects the new values.
type ConfigWatcher interface {
	ConfigChanged()
}

// TCPHandlers are the callbacks for a host-mediated TCP connection. All fire on the
// read-loop goroutine.
type TCPHandlers struct {
	OnConnect func(handle int)
	OnData    func(handle int, data []byte)
	OnError   func(handle int, err error)
}

// Host is the plugin's handle to the broker.
type Host struct {
	out    *bufio.Writer
	nextID int64

	grants []Capability
	config map[string]any

	resolvers map[int64]func(json.RawMessage, *proto.RPCError)
	handlers  map[int]TCPHandlers

	vBuf []Delta
	aBuf []AISTarget
	rBuf []string
}

func newHost() *Host {
	return &Host{
		out:       bufio.NewWriter(os.Stdout),
		resolvers: map[int64]func(json.RawMessage, *proto.RPCError){},
		handlers:  map[int]TCPHandlers{},
	}
}

// Run is the plugin main loop. It returns when the host closes stdin or sends
// plugin.shutdown. Everything below runs on this single goroutine.
func Run(p Plugin) error {
	h := newHost()
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 64<<10), 16<<20)

	for sc.Scan() {
		var m proto.Message
		if json.Unmarshal(sc.Bytes(), &m) != nil {
			continue
		}
		stop := h.dispatch(p, &m)
		h.flush() // batch: one flush per handled host message
		if stop {
			return nil
		}
	}
	return sc.Err()
}

// dispatch handles one message; it returns true when the plugin should stop.
func (h *Host) dispatch(p Plugin, m *proto.Message) bool {
	switch {
	case m.Method == "" && len(m.ID) > 0: // response to a plugin→host request
		h.resolve(m)
	case m.Method != "" && len(m.ID) > 0: // host→plugin request
		switch m.Method {
		case proto.MethodHostHello:
			h.onHello(m)
			p.Start(h)
		case proto.MethodPluginPing:
			h.replyOK(m.ID)
		case proto.MethodPluginShutdown:
			p.Stop()
			h.replyOK(m.ID)
			return true
		default:
			h.replyErr(m.ID, proto.CodeMethodNotFound, "unknown method: "+m.Method)
		}
	default: // host→plugin notification
		h.onNotify(p, m)
	}
	return false
}

// --- lifecycle -------------------------------------------------------------

func (h *Host) onHello(m *proto.Message) {
	var hello proto.HostHello
	_ = json.Unmarshal(m.Params, &hello)
	h.grants = hello.Grants
	h.config = hello.Config
	h.reply(m.ID, proto.HelloResult{APIVersion: proto.APIVersion, Framing: "ndjson"})
}

func (h *Host) onNotify(p Plugin, m *proto.Message) {
	switch m.Method {
	case proto.MethodGrantsChanged:
		var g proto.GrantsChanged
		if json.Unmarshal(m.Params, &g) == nil {
			h.grants = g.Grants
			h.config = g.Config
			if w, ok := p.(ConfigWatcher); ok {
				w.ConfigChanged()
			}
		}
	case proto.MethodTCPData, proto.MethodSerialData:
		var d proto.IOData
		if json.Unmarshal(m.Params, &d) == nil {
			if hnd, ok := h.handlers[d.Handle]; ok && hnd.OnData != nil {
				hnd.OnData(d.Handle, d.Data)
			}
		}
	case proto.MethodIOClosed:
		var ho proto.IOHandle
		if json.Unmarshal(m.Params, &ho) == nil {
			if hnd, ok := h.handlers[ho.Handle]; ok {
				delete(h.handlers, ho.Handle)
				if hnd.OnError != nil {
					hnd.OnError(ho.Handle, fmt.Errorf("%s", ho.Reason))
				}
			}
		}
	}
}

// --- config / grants -------------------------------------------------------

// Config returns the plugin's current settings (a copy is unnecessary — single-
// threaded — but callers should treat it as read-only).
func (h *Host) Config() map[string]any { return h.config }

// ConfigString returns a string setting, or "" if unset.
func (h *Host) ConfigString(key string) string {
	if v, ok := h.config[key].(string); ok {
		return v
	}
	return ""
}

// HasGrant reports whether the plugin was granted cap.
func (h *Host) HasGrant(cap string) bool {
	for _, g := range h.grants {
		if g.Cap == cap {
			return true
		}
	}
	return false
}

// --- data plane (batched) --------------------------------------------------

// PublishVessel queues vessel deltas; they flush at the end of the current read-loop
// iteration (spec §10).
func (h *Host) PublishVessel(deltas ...Delta) { h.vBuf = append(h.vBuf, deltas...) }

// PublishAIS queues AIS target updates.
func (h *Host) PublishAIS(targets ...AISTarget) { h.aBuf = append(h.aBuf, targets...) }

// PublishRaw queues raw sentence lines for the sniffer.
func (h *Host) PublishRaw(lines ...string) { h.rBuf = append(h.rBuf, lines...) }

// flush sends whatever is buffered as batched notifications.
func (h *Host) flush() {
	if len(h.vBuf) > 0 {
		h.notify(proto.MethodVesselPublish, proto.VesselPublish{Deltas: h.vBuf})
		h.vBuf = nil
	}
	if len(h.aBuf) > 0 {
		h.notify(proto.MethodAISPublish, proto.AISPublish{Targets: h.aBuf})
		h.aBuf = nil
	}
	if len(h.rBuf) > 0 {
		h.notify(proto.MethodRawPublish, proto.RawPublish{Lines: h.rBuf})
		h.rBuf = nil
	}
}

// Status reports plugin health (spec §4 status.update).
func (h *Host) Status(state, detail string) {
	h.notify(proto.MethodStatusUpdate, proto.StatusUpdate{State: state, Detail: detail})
}

// Log writes a structured log record to stderr (the host tags it with the plugin id).
func (h *Host) Log(level, msg string) {
	b, _ := json.Marshal(map[string]string{"level": level, "msg": msg})
	fmt.Fprintln(os.Stderr, string(b))
}

// --- transports (async) ----------------------------------------------------

// TCPConnect asks the host to dial host:port (subject to the net.tcp-client
// allowlist). The result is delivered to hnd.OnConnect (or OnError); inbound chunks
// to OnData; peer close/error to OnError.
func (h *Host) TCPConnect(host string, port int, hnd TCPHandlers) {
	h.request(proto.MethodTCPConnect, proto.TCPConnect{Host: host, Port: port}, func(res json.RawMessage, rerr *proto.RPCError) {
		if rerr != nil {
			if hnd.OnError != nil {
				hnd.OnError(0, rerr)
			}
			return
		}
		var r proto.HandleResult
		if json.Unmarshal(res, &r) != nil {
			return
		}
		h.handlers[r.Handle] = hnd
		if hnd.OnConnect != nil {
			hnd.OnConnect(r.Handle)
		}
	})
}

// TCPSend writes outbound bytes to a handle.
func (h *Host) TCPSend(handle int, data []byte) {
	h.notify(proto.MethodTCPSend, proto.IOData{Handle: handle, Data: data, N: len(data)})
}

// CloseHandle releases a transport handle.
func (h *Host) CloseHandle(handle int) {
	delete(h.handlers, handle)
	h.request(proto.MethodIOClose, proto.IOHandle{Handle: handle}, nil)
}

// --- storage (async) -------------------------------------------------------

// StorageGet reads a key; cb receives the raw JSON value + found flag (or an error).
func (h *Host) StorageGet(key string, cb func(value json.RawMessage, found bool, err error)) {
	h.request(proto.MethodStorageGet, proto.StorageKey{Key: key}, func(res json.RawMessage, rerr *proto.RPCError) {
		if cb == nil {
			return
		}
		if rerr != nil {
			cb(nil, false, rerr)
			return
		}
		var v proto.StorageValue
		if err := json.Unmarshal(res, &v); err != nil {
			cb(nil, false, err)
			return
		}
		cb(v.Value, v.Found, nil)
	})
}

// StorageSet writes a key; cb (optional) receives any error.
func (h *Host) StorageSet(key string, value json.RawMessage, cb func(error)) {
	h.request(proto.MethodStorageSet, proto.StorageSet{Key: key, Value: value}, func(_ json.RawMessage, rerr *proto.RPCError) {
		if cb != nil {
			if rerr != nil {
				cb(rerr)
			} else {
				cb(nil)
			}
		}
	})
}

// --- served artifacts + http ------------------------------------------------

// ServeSet publishes data at GET /plugins/<id>/serve/<name>, host-served with Range +
// caching. The zero-RPC path for tile archives, weather grids, and other static
// products (spec §4). cb receives the served URL (or an error).
func (h *Host) ServeSet(name string, data []byte, cb func(url string, err error)) {
	h.request(proto.MethodServeSet, proto.ServeSet{Name: name, Data: data}, func(res json.RawMessage, rerr *proto.RPCError) {
		if cb == nil {
			return
		}
		if rerr != nil {
			cb("", rerr)
			return
		}
		var r struct {
			URL string `json:"url"`
		}
		_ = json.Unmarshal(res, &r)
		cb(r.URL, nil)
	})
}

// ServeClear removes a published artifact.
func (h *Host) ServeClear(name string) {
	h.request(proto.MethodServeClear, proto.ServeClear{Name: name}, nil)
}

// Fetch makes a host-mediated outbound GET (subject to the net.http allowlist). cb
// receives the response (or an error).
func (h *Host) Fetch(url string, cb func(*HTTPResponse, error)) { h.FetchOpts(url, nil, cb) }

// FetchOpts is Fetch with request headers — e.g. a Range header to byte-range a large
// file without downloading all of it.
func (h *Host) FetchOpts(url string, headers map[string]string, cb func(*HTTPResponse, error)) {
	h.request(proto.MethodHTTPFetch, proto.HTTPFetch{URL: url, Headers: headers}, func(res json.RawMessage, rerr *proto.RPCError) {
		if cb == nil {
			return
		}
		if rerr != nil {
			cb(nil, rerr)
			return
		}
		var r HTTPResponse
		if err := json.Unmarshal(res, &r); err != nil {
			cb(nil, err)
			return
		}
		cb(&r, nil)
	})
}

// --- transport plumbing ----------------------------------------------------

// request sends a plugin→host request and registers a resolver for its reply. resolver
// may be nil (fire-and-forget request). It runs later, on the read loop, when the
// response arrives.
func (h *Host) request(method string, params any, resolver func(json.RawMessage, *proto.RPCError)) {
	id := atomic.AddInt64(&h.nextID, 1)
	if resolver != nil {
		h.resolvers[id] = resolver
	}
	idb, _ := json.Marshal(id)
	pb, _ := json.Marshal(params)
	h.send(&proto.Message{JSONRPC: "2.0", ID: idb, Method: method, Params: pb})
}

func (h *Host) resolve(m *proto.Message) {
	var id int64
	if json.Unmarshal(m.ID, &id) != nil {
		return
	}
	fn := h.resolvers[id]
	delete(h.resolvers, id)
	if fn != nil {
		fn(m.Result, m.Error)
	}
}

func (h *Host) notify(method string, params any) {
	pb, _ := json.Marshal(params)
	h.send(&proto.Message{JSONRPC: "2.0", Method: method, Params: pb})
}

func (h *Host) reply(id json.RawMessage, result any) {
	rb, _ := json.Marshal(result)
	h.send(&proto.Message{JSONRPC: "2.0", ID: id, Result: rb})
}

func (h *Host) replyOK(id json.RawMessage) { h.reply(id, map[string]any{"ok": true}) }

func (h *Host) replyErr(id json.RawMessage, code int, msg string) {
	h.send(&proto.Message{JSONRPC: "2.0", ID: id, Error: &proto.RPCError{Code: code, Message: msg}})
}

func (h *Host) send(m *proto.Message) {
	b, _ := json.Marshal(m)
	h.out.Write(b)
	h.out.WriteByte('\n')
	h.out.Flush()
}

// DeltaOf builds a vessel delta from a path and a JSON-serialisable value.
func DeltaOf(path string, value any) Delta {
	b, _ := json.Marshal(value)
	return Delta{Path: path, Value: b}
}
