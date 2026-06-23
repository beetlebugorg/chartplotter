package nmea

import (
	"bufio"
	"context"
	"io"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Transport identifies how a Source receives sentences. Only tcp-client exists
// in v1; serial/udp/file land later behind the same Source/runner abstraction.
type Transport string

const TransportTCPClient Transport = "tcp-client"

// Source is a configured connection. It is plain config (persisted as JSON by
// the server); the live connection is run by a runner.
type Source struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Transport Transport `json:"transport"`
	Host      string    `json:"host"`
	Port      int       `json:"port"`
	Protocol  string    `json:"protocol"`  // "nmea0183"
	Direction string    `json:"direction"` // "in" (v1)
	Enabled   bool      `json:"enabled"`
}

// SourceState is the coarse connection health shown as a status badge.
type SourceState string

const (
	StateDisabled   SourceState = "disabled"
	StateConnecting SourceState = "connecting"
	StateConnected  SourceState = "connected"
	StateStale      SourceState = "stale" // connected but no data within staleAfter
	StateError      SourceState = "error"
)

// SourceStatus is a point-in-time snapshot of a connection's health, suitable
// for JSON serialization to the Connections UI.
type SourceStatus struct {
	State     SourceState `json:"state"`
	LastRx    time.Time   `json:"lastRx,omitzero"`
	RateHz    float64     `json:"rateHz"`          // smoothed sentences/sec
	Errors    int         `json:"errors"`          // framing/checksum failures
	Sentences []string    `json:"sentences"`       // sentence types seen, sorted
	Talkers   []string    `json:"talkers"`         // talker ids seen, sorted
	LastError string      `json:"lastError,omitempty"`
}

// DialFunc opens a byte stream for a host:port. Injectable so the run loop is
// testable with an in-memory connection.
type DialFunc func(ctx context.Context, host string, port int) (io.ReadCloser, error)

// tcpDial is the default DialFunc: a plain TCP client connection.
func tcpDial(ctx context.Context, host string, port int) (io.ReadCloser, error) {
	d := net.Dialer{Timeout: 10 * time.Second}
	return d.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
}

// runner owns the live connection for one Source: it dials, reads line-framed
// sentences into the shared Store, reconnects with capped backoff, and tracks
// health stats behind its own mutex.
type runner struct {
	src        Source
	store      *Store
	parser     *Parser
	dial       DialFunc
	onRaw      func(id, line string) // optional raw-sentence tap (for the sniffer)
	staleAfter time.Duration
	backoff    time.Duration
	maxBackoff time.Duration

	cancel context.CancelFunc
	done   chan struct{}

	mu        sync.Mutex
	state     SourceState
	lastRx    time.Time
	rate      float64
	rxCount   int       // sentences since rateStart (for the windowed rate)
	rateStart time.Time // start of the current rate window
	errors    int
	lastError string
	sentences map[string]struct{}
	talkers   map[string]struct{}
}

func (r *runner) start(parent context.Context) {
	ctx, cancel := context.WithCancel(parent)
	r.cancel = cancel
	r.done = make(chan struct{})
	r.sentences = map[string]struct{}{}
	r.talkers = map[string]struct{}{}
	go r.run(ctx)
}

func (r *runner) stop() {
	if r.cancel != nil {
		r.cancel()
		<-r.done
	}
}

func (r *runner) run(ctx context.Context) {
	defer close(r.done)
	backoff := r.backoff
	for ctx.Err() == nil {
		r.setState(StateConnecting)
		rc, err := r.dial(ctx, r.src.Host, r.src.Port)
		if err != nil {
			r.recordError(err)
			if !sleepCtx(ctx, backoff) {
				return
			}
			backoff = minDur(backoff*2, r.maxBackoff)
			continue
		}
		backoff = r.backoff // reset on a successful dial
		r.setState(StateConnected)

		// Closing the conn on cancellation unblocks the blocking Scan.
		watch := make(chan struct{})
		go func() {
			select {
			case <-ctx.Done():
				rc.Close()
			case <-watch:
			}
		}()
		r.readLoop(ctx, rc)
		close(watch)
		rc.Close()

		if ctx.Err() != nil {
			return
		}
		// Connection dropped (EOF/error); pause before reconnecting.
		if !sleepCtx(ctx, backoff) {
			return
		}
		backoff = minDur(backoff*2, r.maxBackoff)
	}
}

func (r *runner) readLoop(ctx context.Context, rc io.Reader) {
	sc := bufio.NewScanner(rc)
	sc.Buffer(make([]byte, 0, 4096), 1<<20)
	for sc.Scan() {
		if ctx.Err() != nil {
			return
		}
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if r.onRaw != nil {
			r.onRaw(r.src.ID, line)
		}
		s, err := ParseSentence(line)
		if err != nil {
			r.recordError(err)
			continue
		}
		r.recordRx(s)
		r.store.Apply(r.parser, s)
	}
	if err := sc.Err(); err != nil {
		r.recordError(err)
	}
}

func (r *runner) setState(s SourceState) {
	r.mu.Lock()
	r.state = s
	r.mu.Unlock()
}

func (r *runner) recordError(err error) {
	r.mu.Lock()
	r.errors++
	r.lastError = err.Error()
	if r.state != StateConnected {
		r.state = StateError
	}
	r.mu.Unlock()
}

func (r *runner) recordRx(s Sentence) {
	now := time.Now()
	r.mu.Lock()
	// Smoothed sentences/sec over ~1s windows. A per-sentence 1/dt EWMA blows up
	// when sentences arrive in bursts (the normal NMEA pattern: a whole cycle of
	// talkers, then a gap), so count per window instead.
	r.rxCount++
	if r.rateStart.IsZero() {
		r.rateStart = now
	} else if el := now.Sub(r.rateStart).Seconds(); el >= 1 {
		inst := float64(r.rxCount) / el
		if r.rate == 0 {
			r.rate = inst
		} else {
			r.rate = r.rate*0.6 + inst*0.4
		}
		r.rxCount = 0
		r.rateStart = now
	}
	r.lastRx = now
	r.state = StateConnected
	r.sentences[s.Type] = struct{}{}
	if s.Talker != "" {
		r.talkers[s.Talker] = struct{}{}
	}
	r.mu.Unlock()
}

// status builds a snapshot, deriving "stale" when connected but no recent data.
func (r *runner) status() SourceStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	st := SourceStatus{
		State:     r.state,
		LastRx:    r.lastRx,
		RateHz:    r.rate,
		Errors:    r.errors,
		LastError: r.lastError,
		Sentences: sortedKeys(r.sentences),
		Talkers:   sortedKeys(r.talkers),
	}
	if r.state == StateConnected && !r.lastRx.IsZero() && time.Since(r.lastRx) > r.staleAfter {
		st.State = StateStale
	}
	return st
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func minDur(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
