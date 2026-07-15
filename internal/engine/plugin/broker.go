package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"sync"
	"time"

	"github.com/beetlebugorg/chartplotter/internal/engine/nmea"
)

// broker.go is the host end of the JSON-RPC session with one plugin. It performs the
// handshake, runs the read loop, correlates host→plugin requests with their replies,
// dispatches plugin→host requests/notifications to the capability handlers
// (capabilities.go), and enforces the granted capability set on every inbound call
// (spec §6). One brokerSession per active plugin runner; the Host it calls into is
// provided by the server (the shared vessel/AIS/raw stores).

// PluginStatus mirrors the plugin's status.update (spec §4). The server maps State
// (running|degraded|error) onto the connections UI's SourceStatus enum.
type PluginStatus struct {
	State   string         `json:"state"`
	Detail  string         `json:"detail,omitempty"`
	Metrics map[string]any `json:"metrics,omitempty"`
}

// Host is the capability backend the broker calls into for effects that touch the
// app's shared state. Keeping it an interface lets the plugin package stay decoupled
// from the server; the shared nmea stores are passed through as concrete types since
// nmea is a low-level dependency of both. Host-mediated I/O (TCP dial, storage) is
// owned by the broker itself and is not part of this interface.
type Host interface {
	PublishVessel(source string, deltas []nmea.Delta)
	PublishAIS(source string, targets []nmea.AISTarget)
	PublishRaw(source string, lines []string)
	UpdateStatus(source string, st PluginStatus)
	Log(pluginID, level, msg string)
}

// ioHandle is one open host-mediated transport (a dialed TCP conn), addressed by an
// opaque per-plugin integer handle.
type ioHandle struct {
	conn   net.Conn
	cancel context.CancelFunc
}

// brokerSession drives the protocol over one Session.
type brokerSession struct {
	id       string
	sess     Session
	host     Host
	storeDir string // <dataDir>/plugins/<id>/data — storage KV root
	dialer   net.Dialer

	mu      sync.Mutex
	grants  []Capability
	config  map[string]any
	quota   int64
	nextID  int64
	pending map[int64]chan *Message
	nextH   int
	handles map[int]*ioHandle
	closed  bool
}

func newBrokerSession(id string, sess Session, host Host, storeDir string, grants []Capability, config map[string]any) *brokerSession {
	return &brokerSession{
		id:       id,
		sess:     sess,
		host:     host,
		storeDir: storeDir,
		dialer:   net.Dialer{Timeout: 10 * time.Second},
		grants:   grants,
		config:   config,
		quota:    storageQuota(grants),
		pending:  map[int64]chan *Message{},
		handles:  map[int]*ioHandle{},
	}
}

// handshake sends host.hello and awaits the plugin's HelloResult (spec §4).
func (b *brokerSession) handshake(ctx context.Context) (HelloResult, error) {
	raw, err := b.request(ctx, MethodHostHello, HostHello{
		APIVersions: []int{APIVersion},
		PluginID:    b.id,
		Grants:      b.grantsCopy(),
		Config:      b.configCopy(),
		Framing:     []string{"ndjson"},
	})
	if err != nil {
		return HelloResult{}, err
	}
	var res HelloResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return HelloResult{}, err
	}
	return res, nil
}

// serve runs the read loop until the session ends (plugin exit → io.EOF) or ctx is
// cancelled. Responses are correlated to pending host requests; plugin→host requests
// are handled in goroutines (they may block on I/O) and notifications inline (they are
// fast store writes whose order should be preserved).
func (b *brokerSession) serve(ctx context.Context) error {
	for {
		m, err := b.sess.Recv()
		if err != nil {
			return err
		}
		switch {
		case m.isResponse():
			b.deliverResponse(m)
		case m.isRequest():
			go b.handleRequest(ctx, m)
		case m.isNotification():
			b.handleNotification(m)
		}
	}
}

// --- host→plugin request/response plumbing ---------------------------------

// request sends a host→plugin request and blocks for its response.
func (b *brokerSession) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil, errors.New("session closed")
	}
	b.nextID++
	id := b.nextID
	ch := make(chan *Message, 1)
	b.pending[id] = ch
	b.mu.Unlock()

	defer func() {
		b.mu.Lock()
		delete(b.pending, id)
		b.mu.Unlock()
	}()

	msg, err := newRequest(id, method, params)
	if err != nil {
		return nil, err
	}
	if err := b.sess.Send(msg); err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case resp := <-ch:
		if resp.Error != nil {
			return nil, resp.Error
		}
		return resp.Result, nil
	}
}

// notify sends a host→plugin notification (no reply).
func (b *brokerSession) notify(method string, params any) error {
	msg, err := newNotification(method, params)
	if err != nil {
		return err
	}
	return b.sess.Send(msg)
}

func (b *brokerSession) deliverResponse(m *Message) {
	var id int64
	if json.Unmarshal(m.ID, &id) != nil {
		return
	}
	b.mu.Lock()
	ch := b.pending[id]
	b.mu.Unlock()
	if ch != nil {
		ch <- m
	}
}

// reply sends a success response to a plugin→host request.
func (b *brokerSession) reply(id json.RawMessage, result any) {
	msg, err := newResult(id, result)
	if err != nil {
		b.replyErr(id, CodeInternalError, err.Error())
		return
	}
	_ = b.sess.Send(msg)
}

// replyErr sends an error response.
func (b *brokerSession) replyErr(id json.RawMessage, code int, msg string) {
	_ = b.sess.Send(newErrorResponse(id, code, msg))
}

// --- grant / config helpers ------------------------------------------------

func (b *brokerSession) hasCap(cap string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, ok := HasCap(b.grants, cap)
	return ok
}

func (b *brokerSession) grantFor(cap string) (Capability, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return HasCap(b.grants, cap)
}

func (b *brokerSession) grantsCopy() []Capability {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]Capability(nil), b.grants...)
}

func (b *brokerSession) configCopy() map[string]any {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make(map[string]any, len(b.config))
	for k, v := range b.config {
		out[k] = v
	}
	return out
}

// setGrants swaps the grant set + config at runtime and notifies the plugin
// (host.grantsChanged, spec §4).
func (b *brokerSession) setGrants(ctx context.Context, grants []Capability, config map[string]any) {
	b.mu.Lock()
	b.grants = grants
	b.config = config
	b.quota = storageQuota(grants)
	b.mu.Unlock()
	_ = b.notify(MethodGrantsChanged, GrantsChanged{Grants: grants, Config: config})
}

// shutdown asks the plugin to stop gracefully, then closes the transport. The runner
// force-kills after a timeout if the plugin doesn't exit (spec §4: 5 s).
func (b *brokerSession) shutdown(ctx context.Context) {
	_, _ = b.request(ctx, MethodPluginShutdown, nil)
	b.closeHandles()
	b.mu.Lock()
	b.closed = true
	b.mu.Unlock()
	_ = b.sess.Close()
}

func (b *brokerSession) closeHandles() {
	b.mu.Lock()
	handles := b.handles
	b.handles = map[int]*ioHandle{}
	b.mu.Unlock()
	for _, h := range handles {
		if h.cancel != nil {
			h.cancel()
		}
		if h.conn != nil {
			_ = h.conn.Close()
		}
	}
}
