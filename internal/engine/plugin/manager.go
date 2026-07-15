package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/beetlebugorg/chartplotter/internal/engine/plugin/runtime/native"
	"github.com/beetlebugorg/chartplotter/internal/engine/plugin/runtime/wasm"
)

// manager.go owns install/verify/unpack, the plugins.json state, and the lifecycle of
// one runner per active plugin — mirroring nmea.Manager's config-vs-runner shape. It
// reuses the 1 s→30 s reconnect backoff (nmea/manager.go) and adds a circuit breaker
// (N crashes in M minutes → disabled + notification, spec §9).

// Lifecycle timings.
const (
	defaultBackoff    = 1 * time.Second
	defaultMaxBackoff = 30 * time.Second
	handshakeTimeout  = 10 * time.Second
	shutdownGrace     = 5 * time.Second
	pingInterval      = 15 * time.Second
	pingTimeout       = 5 * time.Second
	maxPingMisses     = 3
	// Circuit breaker: this many crashes within breakerWindow disables the plugin.
	breakerCrashes = 5
	breakerWindow  = 2 * time.Minute
)

// ManagerOpts configures a Manager.
type ManagerOpts struct {
	DataDir string // <dataDir>; plugins unpack under <dataDir>/plugins, state at <dataDir>/plugins.json
	Host    Host   // capability backend (the server's shared stores); required unless NoStart
	Logf    func(format string, args ...any)
	// NoStart makes the Manager a pure state manipulator (CLI one-shots): it never
	// spawns runners, so install/list/enable/disable/remove just edit plugins.json.
	NoStart bool
}

// Manager is the host-side plugin engine.
type Manager struct {
	ctx        context.Context
	opts       ManagerOpts
	pluginsDir string
	state      *stateStore

	mu      sync.Mutex
	runners map[string]*pluginRunner
}

// PluginInfo is a plugin's install/grant state plus its manifest summary and live
// status, the unit the /api/plugins UI lists.
type PluginInfo struct {
	Record   PluginRecord `json:"record"`
	Manifest *Manifest    `json:"manifest,omitempty"`
	Status   PluginStatus `json:"status"`
	Running  bool         `json:"running"`
}

// NewManager builds the engine rooted at opts.DataDir and starts a runner for every
// enabled, installed plugin. ctx bounds every runner.
func NewManager(ctx context.Context, opts ManagerOpts) *Manager {
	if opts.Logf == nil {
		opts.Logf = func(string, ...any) {}
	}
	pluginsDir := filepath.Join(opts.DataDir, "plugins")
	m := &Manager{
		ctx:        ctx,
		opts:       opts,
		pluginsDir: pluginsDir,
		state:      newStateStore(filepath.Join(opts.DataDir, "plugins.json")),
		runners:    map[string]*pluginRunner{},
	}
	if !opts.NoStart {
		for _, rec := range m.state.list() {
			if rec.Enabled {
				m.startRunner(rec)
			}
		}
	}
	return m
}

// PluginsDir is <dataDir>/plugins, where archives unpack.
func (m *Manager) PluginsDir() string { return m.pluginsDir }

// Install verifies + unpacks an archive and records it (disabled, ungranted) so the
// user can review its capabilities before enabling. opts.AllowCore gates core.* ids.
func (m *Manager) Install(archivePath string, opts InstallOptions) (*Manifest, error) {
	man, err := Install(archivePath, m.pluginsDir, opts)
	if err != nil {
		return nil, err
	}
	m.state.put(PluginRecord{ID: man.ID, Version: man.Version, Enabled: false})
	return man, nil
}

// List returns every installed plugin with its manifest + live status.
func (m *Manager) List() []PluginInfo {
	recs := m.state.list()
	out := make([]PluginInfo, 0, len(recs))
	for _, rec := range recs {
		man, _ := m.loadManifest(rec.ID, rec.Version)
		info := PluginInfo{Record: rec, Manifest: man}
		m.mu.Lock()
		r := m.runners[rec.ID]
		m.mu.Unlock()
		switch {
		case r != nil:
			info.Running = true
			info.Status = r.currentStatus()
		case m.opts.NoStart && rec.Enabled:
			// A state-only (CLI) manager doesn't run plugins; enabled means "will run".
			info.Status = PluginStatus{State: "enabled"}
		case rec.Enabled:
			info.Status = PluginStatus{State: "error", Detail: "not running"}
		default:
			info.Status = PluginStatus{State: "disabled"}
		}
		out = append(out, info)
	}
	return out
}

// Enable marks a plugin enabled and starts its runner (if it has a host-side entry).
func (m *Manager) Enable(id string) error {
	rec, ok := m.state.mutate(id, func(r *PluginRecord) { r.Enabled = true })
	if !ok {
		return fmt.Errorf("no such plugin %q", id)
	}
	m.startRunner(rec)
	return nil
}

// Disable stops the runner and marks the plugin disabled.
func (m *Manager) Disable(id string) error {
	if _, ok := m.state.mutate(id, func(r *PluginRecord) { r.Enabled = false }); !ok {
		return fmt.Errorf("no such plugin %q", id)
	}
	m.stopRunner(id)
	return nil
}

// SetGrants swaps a plugin's grant set (and optionally config), hot-applying it to a
// running plugin via host.grantsChanged (spec §4).
func (m *Manager) SetGrants(id string, grants []Capability, config map[string]any) error {
	rec, ok := m.state.mutate(id, func(r *PluginRecord) {
		r.Grants = grants
		if config != nil {
			r.Config = config
		}
	})
	if !ok {
		return fmt.Errorf("no such plugin %q", id)
	}
	m.mu.Lock()
	r := m.runners[id]
	m.mu.Unlock()
	if r != nil {
		r.applyGrants(rec.Grants, rec.Config)
	}
	return nil
}

// Remove stops, disables, and deletes a plugin. purgeData additionally removes its
// per-plugin storage (spec §2).
func (m *Manager) Remove(id string, purgeData bool) error {
	m.stopRunner(id)
	if !m.state.remove(id) {
		return fmt.Errorf("no such plugin %q", id)
	}
	dir := filepath.Join(m.pluginsDir, id)
	if purgeData {
		return os.RemoveAll(dir)
	}
	// Keep <id>/data; remove only the unpacked version dirs. Simplest: remove all but
	// the data subdir.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() != "data" {
			_ = os.RemoveAll(filepath.Join(dir, e.Name()))
		}
	}
	return nil
}

// Statuses returns live status keyed by plugin id (for the SSE stream).
func (m *Manager) Statuses() map[string]PluginStatus {
	out := map[string]PluginStatus{}
	for _, info := range m.List() {
		out[info.Record.ID] = info.Status
	}
	return out
}

// Close stops every runner.
func (m *Manager) Close() {
	m.mu.Lock()
	runners := m.runners
	m.runners = map[string]*pluginRunner{}
	m.mu.Unlock()
	for _, r := range runners {
		r.stop()
	}
}

// VersionDir returns the unpacked directory of a plugin's active version.
func (m *Manager) VersionDir(id string) (string, bool) {
	rec, ok := m.state.get(id)
	if !ok {
		return "", false
	}
	return filepath.Join(m.pluginsDir, id, rec.Version), true
}

// DataDir returns a plugin's persistent per-plugin storage root (survives upgrades).
func (m *Manager) DataDir(id string) string { return filepath.Join(m.pluginsDir, id, "data") }

func (m *Manager) loadManifest(id, version string) (*Manifest, error) {
	b, err := os.ReadFile(filepath.Join(m.pluginsDir, id, version, "plugin.json"))
	if err != nil {
		return nil, err
	}
	return ParseManifest(b)
}

// startRunner starts (or restarts) the runner for rec, if it has a host-side entry
// point. UI-only plugins (no wasm/native) have no host runner — the frontend loads
// them; enabling them is purely a state flag.
func (m *Manager) startRunner(rec PluginRecord) {
	if m.opts.NoStart {
		return // pure state manipulator (CLI); the server's Manager runs plugins
	}
	man, err := m.loadManifest(rec.ID, rec.Version)
	if err != nil {
		m.opts.Logf("plugin %s: load manifest: %v", rec.ID, err)
		return
	}
	if man.Entry.WASM == "" && len(man.Entry.Native) == 0 {
		return // UI-only; no host runner
	}
	m.stopRunner(rec.ID)
	r := &pluginRunner{
		mgr:      m,
		id:       rec.ID,
		record:   rec,
		manifest: man,
		dir:      filepath.Join(m.pluginsDir, rec.ID, rec.Version),
		done:     make(chan struct{}),
	}
	ctx, cancel := context.WithCancel(m.ctx)
	r.cancel = cancel
	m.mu.Lock()
	m.runners[rec.ID] = r
	m.mu.Unlock()
	go r.run(ctx)
}

func (m *Manager) stopRunner(id string) {
	m.mu.Lock()
	r := m.runners[id]
	delete(m.runners, id)
	m.mu.Unlock()
	if r != nil {
		r.stop()
	}
}

// --- runner ----------------------------------------------------------------

// rtInstance is the common shape of a wasm/native runtime instance (satisfied
// structurally by both, so the manager needs no cross-package interface import).
type rtInstance interface {
	Stdin() io.WriteCloser
	Stdout() io.Reader
	Kill()
	Wait() error
}

type pluginRunner struct {
	mgr      *Manager
	id       string
	record   PluginRecord
	manifest *Manifest
	dir      string

	cancel context.CancelFunc
	done   chan struct{}

	mu      sync.Mutex
	broker  *brokerSession
	status  PluginStatus
	crashes []time.Time
}

func (r *pluginRunner) storeDir() string { return filepath.Join(r.mgr.pluginsDir, r.id, "data") }

func (r *pluginRunner) run(ctx context.Context) {
	defer close(r.done)
	backoff := defaultBackoff
	for ctx.Err() == nil {
		err := r.runOnce(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			r.setStatus(PluginStatus{State: "error", Detail: err.Error()})
			r.mgr.opts.Logf("plugin %s exited: %v", r.id, err)
		}
		if r.tripBreaker() {
			r.setStatus(PluginStatus{State: "error", Detail: "disabled: crash loop"})
			r.mgr.opts.Logf("plugin %s: too many crashes, disabling", r.id)
			_, _ = r.mgr.state.mutate(r.id, func(rec *PluginRecord) { rec.Enabled = false })
			r.mgr.stopRunner(r.id)
			return
		}
		if !sleepCtx(ctx, backoff) {
			return
		}
		backoff = minDur(backoff*2, defaultMaxBackoff)
	}
}

// runOnce starts one instance, handshakes, pings, and serves until it exits.
func (r *pluginRunner) runOnce(parent context.Context) error {
	inst, err := r.startInstance(parent)
	if err != nil {
		return err
	}
	sess := newStdioSession(inst.Stdout(), inst.Stdin(), inst.Kill)
	b := newBrokerSession(r.id, sess, r.mgr.opts.Host, r.storeDir(), r.record.Grants, r.record.Config)
	r.setBroker(b)
	defer r.setBroker(nil)

	// The read loop must be running for handshake's reply (and every later response)
	// to be delivered, so start serve first, then handshake over it.
	runCtx, cancel := context.WithCancel(parent)
	defer cancel()
	serveDone := make(chan error, 1)
	go func() { serveDone <- b.serve(runCtx) }()

	hctx, hcancel := context.WithTimeout(runCtx, handshakeTimeout)
	_, err = b.handshake(hctx)
	hcancel()
	if err != nil {
		sess.Kill()
		<-serveDone
		_ = inst.Wait()
		return fmt.Errorf("handshake: %w", err)
	}
	r.setStatus(PluginStatus{State: "running"})
	go r.pingLoop(runCtx, b, sess)

	serveErr := <-serveDone
	sess.Kill()
	_ = inst.Wait()
	return serveErr
}

// startInstance selects the runtime for this runner.
func (r *pluginRunner) startInstance(ctx context.Context) (rtInstance, error) {
	logw := &lineLogger{logf: func(level, msg string) { r.mgr.opts.Host.Log(r.id, level, msg) }}
	return startInstance(ctx, r.dir, r.manifest, r.record.ForceNative, r.storeDir(), logw)
}

// startInstance selects the runtime — WASM (Tier A, preferred) unless the plugin has
// no wasm entry or native is forced (spec §2) — and starts an instance from an
// unpacked plugin dir. Shared by the Manager's runners and DevRun.
func startInstance(ctx context.Context, dir string, man *Manifest, forceNative bool, storeDir string, stderr io.Writer) (rtInstance, error) {
	useNative := forceNative || man.Entry.WASM == ""
	if !useNative {
		b, err := os.ReadFile(filepath.Join(dir, man.Entry.WASM))
		if err != nil {
			return nil, fmt.Errorf("read wasm: %w", err)
		}
		return wasm.Start(ctx, b, wasm.Config{Name: man.ID, Stderr: stderr})
	}
	rel, ok := man.Entry.Native[platformKey()]
	if !ok {
		return nil, fmt.Errorf("no native entry for %s", platformKey())
	}
	return native.Start(ctx, native.Config{
		Path:   filepath.Join(dir, rel),
		Dir:    storeDir,
		Env:    []string{}, // minimal env (spec §9)
		Stderr: stderr,
	})
}

func (r *pluginRunner) pingLoop(ctx context.Context, b *brokerSession, sess Session) {
	t := time.NewTicker(pingInterval)
	defer t.Stop()
	misses := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			pctx, cancel := context.WithTimeout(ctx, pingTimeout)
			_, err := b.request(pctx, MethodPluginPing, nil)
			cancel()
			if err != nil {
				if misses++; misses >= maxPingMisses {
					r.mgr.opts.Logf("plugin %s: %d missed pings, restarting", r.id, misses)
					sess.Kill() // unblocks serve → restart
					return
				}
			} else {
				misses = 0
			}
		}
	}
}

// stop cancels the runner, asking the plugin to shut down gracefully first.
func (r *pluginRunner) stop() {
	if b := r.getBroker(); b != nil {
		sctx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
		b.shutdown(sctx)
		cancel()
	}
	if r.cancel != nil {
		r.cancel()
	}
	<-r.done
}

func (r *pluginRunner) applyGrants(grants []Capability, config map[string]any) {
	r.mu.Lock()
	r.record.Grants = grants
	r.record.Config = config
	b := r.broker
	r.mu.Unlock()
	if b != nil {
		b.setGrants(context.Background(), grants, config)
	}
}

func (r *pluginRunner) setBroker(b *brokerSession) { r.mu.Lock(); r.broker = b; r.mu.Unlock() }
func (r *pluginRunner) getBroker() *brokerSession  { r.mu.Lock(); defer r.mu.Unlock(); return r.broker }

func (r *pluginRunner) setStatus(s PluginStatus) {
	r.mu.Lock()
	r.status = s
	r.mu.Unlock()
	r.mgr.opts.Host.UpdateStatus(r.id, s)
}

func (r *pluginRunner) currentStatus() PluginStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.status
}

// tripBreaker records a crash and reports whether the breaker should open.
func (r *pluginRunner) tripBreaker() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	kept := r.crashes[:0]
	for _, t := range r.crashes {
		if now.Sub(t) < breakerWindow {
			kept = append(kept, t)
		}
	}
	kept = append(kept, now)
	r.crashes = kept
	return len(kept) >= breakerCrashes
}

// --- helpers ---------------------------------------------------------------

// platformKey is the "<goos>-<goarch>" native-entry key, e.g. "linux-amd64".
func platformKey() string { return runtime.GOOS + "-" + runtime.GOARCH }

// lineLogger splits a plugin's stderr into log records tagged with the plugin id. A
// line that parses as {"level":…,"msg":…} keeps its structure; anything else logs at
// info verbatim (spec §4).
type lineLogger struct {
	logf func(level, msg string)
	buf  []byte
}

func (l *lineLogger) Write(p []byte) (int, error) {
	l.buf = append(l.buf, p...)
	for {
		i := indexByte(l.buf, '\n')
		if i < 0 {
			break
		}
		line := strings.TrimRight(string(l.buf[:i]), "\r")
		l.buf = l.buf[i+1:]
		l.emit(line)
	}
	return len(p), nil
}

func (l *lineLogger) emit(line string) {
	if line == "" {
		return
	}
	var rec struct {
		Level string `json:"level"`
		Msg   string `json:"msg"`
	}
	if json.Unmarshal([]byte(line), &rec) == nil && rec.Msg != "" {
		level := rec.Level
		if level == "" {
			level = "info"
		}
		l.logf(level, rec.Msg)
		return
	}
	l.logf("info", line)
}

func indexByte(b []byte, c byte) int {
	for i, x := range b {
		if x == c {
			return i
		}
	}
	return -1
}

// sleepCtx / minDur mirror nmea's cancellable-sleep helpers.
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
