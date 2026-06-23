package nmea

import (
	"context"
	"sync"
	"time"
)

// Default reconnect/health timings. Overridable via ManagerOpts (tests use tiny
// backoffs to exercise reconnection quickly).
const (
	defaultBackoff    = 1 * time.Second
	defaultMaxBackoff = 30 * time.Second
	defaultStaleAfter = 10 * time.Second
)

// ManagerOpts configures a Manager. The zero value is usable; each field falls
// back to a sensible default.
type ManagerOpts struct {
	Parser     *Parser
	Dial       DialFunc              // default: tcpDial
	OnRaw      func(id, line string) // optional raw-sentence tap for the sniffer
	Backoff    time.Duration
	MaxBackoff time.Duration
	StaleAfter time.Duration
}

// Manager owns the live runners for the configured Sources and writes every
// parsed sentence into a single shared Store. The server layer owns persistence
// (connections.json) and drives the Manager via Apply/Remove.
type Manager struct {
	store *Store
	opts  ManagerOpts
	ctx   context.Context

	mu      sync.Mutex
	sources map[string]Source
	runners map[string]*runner
}

// NewManager creates a Manager writing into store. ctx bounds the lifetime of
// every runner; cancelling it (or calling Close) stops them all.
func NewManager(ctx context.Context, store *Store, opts ManagerOpts) *Manager {
	if opts.Parser == nil {
		opts.Parser = &Parser{}
	}
	if opts.Dial == nil {
		opts.Dial = tcpDial
	}
	if opts.Backoff <= 0 {
		opts.Backoff = defaultBackoff
	}
	if opts.MaxBackoff <= 0 {
		opts.MaxBackoff = defaultMaxBackoff
	}
	if opts.StaleAfter <= 0 {
		opts.StaleAfter = defaultStaleAfter
	}
	return &Manager{
		store:   store,
		opts:    opts,
		ctx:     ctx,
		sources: map[string]Source{},
		runners: map[string]*runner{},
	}
}

// Store returns the shared vessel-state store the Manager feeds.
func (m *Manager) Store() *Store { return m.store }

// Apply adds or updates a Source: any existing runner for the id is stopped and,
// if the source is enabled and its transport is supported, a fresh runner is
// started. Disabled or unsupported sources are remembered (so they list with a
// "disabled" status) but run nothing.
func (m *Manager) Apply(src Source) {
	m.mu.Lock()
	m.sources[src.ID] = src
	if old := m.runners[src.ID]; old != nil {
		delete(m.runners, src.ID)
		m.mu.Unlock()
		old.stop() // stop outside the lock; readLoop may still call store
		m.mu.Lock()
	}
	if src.Enabled && src.Transport == TransportTCPClient {
		r := &runner{
			src:        src,
			store:      m.store,
			parser:     m.opts.Parser,
			dial:       m.opts.Dial,
			onRaw:      m.opts.OnRaw,
			staleAfter: m.opts.StaleAfter,
			backoff:    m.opts.Backoff,
			maxBackoff: m.opts.MaxBackoff,
		}
		r.start(m.ctx)
		m.runners[src.ID] = r
	}
	m.mu.Unlock()
}

// Remove stops and forgets a Source.
func (m *Manager) Remove(id string) {
	m.mu.Lock()
	r := m.runners[id]
	delete(m.runners, id)
	delete(m.sources, id)
	m.mu.Unlock()
	if r != nil {
		r.stop()
	}
}

// Status returns the live status of one Source. A known-but-not-running source
// reports StateDisabled.
func (m *Manager) Status(id string) (SourceStatus, bool) {
	m.mu.Lock()
	r := m.runners[id]
	_, known := m.sources[id]
	m.mu.Unlock()
	if r != nil {
		return r.status(), true
	}
	if known {
		return SourceStatus{State: StateDisabled}, true
	}
	return SourceStatus{}, false
}

// Statuses returns the live status of every known Source, keyed by id.
func (m *Manager) Statuses() map[string]SourceStatus {
	m.mu.Lock()
	ids := make([]string, 0, len(m.sources))
	for id := range m.sources {
		ids = append(ids, id)
	}
	runners := make(map[string]*runner, len(m.runners))
	for id, r := range m.runners {
		runners[id] = r
	}
	m.mu.Unlock()

	out := make(map[string]SourceStatus, len(ids))
	for _, id := range ids {
		if r := runners[id]; r != nil {
			out[id] = r.status()
		} else {
			out[id] = SourceStatus{State: StateDisabled}
		}
	}
	return out
}

// Close stops every runner. The Manager is unusable afterward.
func (m *Manager) Close() {
	m.mu.Lock()
	runners := m.runners
	m.runners = map[string]*runner{}
	m.mu.Unlock()
	for _, r := range runners {
		r.stop()
	}
}
