package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

type state int

const (
	stateStarting state = iota
	stateRunning
	stateAdopted // a chartplotter we didn't spawn owns the port; Quit ≠ stop
	stateStopped
	stateError
)

// manager owns the launcher state machine: probe 8080,
// adopt an existing chartplotter or spawn `chartplotter serve` on the first
// free port, watch its health, and stop it on quit.
type manager struct {
	log    *os.File
	engine string // --engine override; "" = sibling of this executable

	mu       sync.Mutex
	st       state
	url      string
	child    *exec.Cmd
	done     chan struct{} // closed when the child exits
	onChange func()
}

const (
	basePort     = 8080
	maxPort      = 8090
	startTimeout = 15 * time.Second
	stopGrace    = 5 * time.Second
)

func (m *manager) state() (state, string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.st, m.url
}

func (m *manager) set(st state, url string) {
	m.mu.Lock()
	m.st, m.url = st, url
	cb := m.onChange
	m.mu.Unlock()
	if cb != nil {
		cb()
	}
}

func (m *manager) logf(format string, args ...any) {
	fmt.Fprintf(m.log, time.Now().Format("2006-01-02 15:04:05")+" "+format+"\n", args...)
}

// startup implements the launch flow: adopt our own server if one is already
// on 8080, otherwise spawn on the first free port in [8080, 8090].
func (m *manager) startup() {
	m.set(stateStarting, "")
	for port := basePort; port <= maxPort; port++ {
		switch probe(port) {
		case probeOurs:
			url := fmt.Sprintf("http://127.0.0.1:%d/", port)
			m.logf("adopted running chartplotter at %s", url)
			m.set(stateAdopted, url)
			return
		case probeFree:
			m.spawn(port)
			return
		default: // something else owns this port — keep walking
			m.logf("port %d taken by another server, trying next", port)
		}
	}
	m.logf("no free port in %d..%d", basePort, maxPort)
	m.set(stateError, "")
}

// spawn starts `chartplotter serve` on port and polls /api/health until it is
// ready (→ RUNNING, open the browser) or it dies / times out (→ ERROR).
func (m *manager) spawn(port int) {
	bin, err := m.enginePath()
	if err != nil {
		m.logf("engine binary: %v", err)
		m.set(stateError, "")
		return
	}
	cmd := exec.Command(bin, "serve", "--host", "127.0.0.1", "--port", fmt.Sprint(port))
	cmd.Stdout = m.log
	cmd.Stderr = m.log
	cmd.SysProcAttr = sysProcAttr()
	m.logf("spawning %s serve --port %d", bin, port)
	if err := cmd.Start(); err != nil {
		m.logf("start: %v", err)
		m.set(stateError, "")
		return
	}
	done := make(chan struct{})
	go func() { cmd.Wait(); close(done) }()

	m.mu.Lock()
	m.child, m.done = cmd, done
	m.mu.Unlock()

	deadline := time.Now().Add(startTimeout)
	for time.Now().Before(deadline) {
		select {
		case <-done:
			m.logf("server exited during startup — see log above")
			m.set(stateError, "")
			return
		case <-time.After(250 * time.Millisecond):
		}
		if probe(port) == probeOurs {
			url := fmt.Sprintf("http://127.0.0.1:%d/", port)
			m.logf("running at %s", url)
			m.set(stateRunning, url)
			openBrowser(url)
			go m.watch(done)
			return
		}
	}
	m.logf("server not healthy after %s, giving up", startTimeout)
	m.stop()
	m.set(stateError, "")
}

// watch flips to ERROR if the child dies while we think it's running.
func (m *manager) watch(done chan struct{}) {
	<-done
	m.mu.Lock()
	crashed := m.done == done && m.st == stateRunning
	m.mu.Unlock()
	if crashed {
		m.logf("server exited unexpectedly")
		m.set(stateError, "")
	}
}

// stop gracefully terminates the child: SIGTERM (CTRL_BREAK on Windows), a
// grace period, then kill. Adopted servers are never touched.
func (m *manager) stop() {
	m.mu.Lock()
	cmd, done := m.child, m.done
	m.child, m.done = nil, nil
	m.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		if st, _ := m.state(); st != stateError {
			m.set(stateStopped, "")
		}
		return
	}
	if err := terminate(cmd); err != nil {
		cmd.Process.Kill()
	}
	select {
	case <-done:
	case <-time.After(stopGrace):
		m.logf("server did not exit within %s, killing", stopGrace)
		cmd.Process.Kill()
		<-done
	}
	m.logf("server stopped")
	m.set(stateStopped, "")
}

// enginePath resolves the chartplotter binary: --engine override, else the
// sibling of this executable (inside the macOS bundle both live in
// Contents/MacOS/).
func (m *manager) enginePath() (string, error) {
	if m.engine != "" {
		return m.engine, nil
	}
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	name := "chartplotter"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	bin := filepath.Join(filepath.Dir(self), name)
	if _, err := os.Stat(bin); err != nil {
		return "", errors.New(bin + " not found beside the launcher (or pass --engine)")
	}
	return bin, nil
}

// findRunning scans the probe range for an already-running chartplotter and
// returns its URL, or "". Used by a second launcher instance before exiting.
func findRunning() string {
	for port := basePort; port <= maxPort; port++ {
		if probe(port) == probeOurs {
			return fmt.Sprintf("http://127.0.0.1:%d/", port)
		}
	}
	return ""
}
