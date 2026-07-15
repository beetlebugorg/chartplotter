package plugin

import (
	"bufio"
	"encoding/json"
	"io"
	"sync"
)

// maxLine is the NDJSON line cap from spec §4 (16 MiB). A plugin that needs to move
// more than this per message must negotiate lpbin framing (reserved, not in v1).
const maxLine = 16 << 20

// Session is the host's transport to one plugin instance. Both runtimes — wazero
// (WASM stdio) and os/exec (native pipes) — present the identical interface: the
// runtime owns process/module liveness (Kill), the Session owns framing. A session
// is a session regardless of attachment (spec §11).
type Session interface {
	// Send writes one JSON-RPC message as an NDJSON line. Safe for concurrent use.
	Send(*Message) error
	// Recv reads the next message, blocking until one arrives. Returns io.EOF when
	// the plugin's stdout closes (it exited).
	Recv() (*Message, error)
	// Close closes the host's write side (the plugin sees stdin EOF and should exit).
	Close() error
	// Kill force-terminates the underlying module/process immediately.
	Kill()
}

// stdioSession frames JSON-RPC over a plugin's stdio: it reads the plugin's stdout
// (r) and writes the plugin's stdin (w). kill terminates the runtime that owns them.
type stdioSession struct {
	r    io.Reader // plugin stdout
	sc   *bufio.Scanner
	wmu  sync.Mutex
	w    io.WriteCloser // plugin stdin
	kill func()
	once sync.Once
}

// newStdioSession wraps a plugin's stdout reader and stdin writer. kill is the
// runtime's force-terminate hook (module close / process Kill).
func newStdioSession(stdout io.Reader, stdin io.WriteCloser, kill func()) *stdioSession {
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64<<10), maxLine)
	return &stdioSession{r: stdout, sc: sc, w: stdin, kill: kill}
}

func (s *stdioSession) Send(m *Message) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	s.wmu.Lock()
	defer s.wmu.Unlock()
	_, err = s.w.Write(b)
	return err
}

func (s *stdioSession) Recv() (*Message, error) {
	if !s.sc.Scan() {
		if err := s.sc.Err(); err != nil {
			return nil, err
		}
		return nil, io.EOF
	}
	// Copy the token: bufio.Scanner reuses its buffer on the next Scan.
	line := s.sc.Bytes()
	var m Message
	if err := json.Unmarshal(line, &m); err != nil {
		// A malformed line is a protocol violation; surface it so the runner can
		// decide (log + continue, or restart). We return the error rather than
		// skipping so a wedged encoder is visible.
		return nil, &protocolError{err: err}
	}
	return &m, nil
}

func (s *stdioSession) Close() error {
	return s.w.Close()
}

func (s *stdioSession) Kill() {
	s.once.Do(func() {
		if s.kill != nil {
			s.kill()
		}
		_ = s.w.Close()
	})
}

// protocolError marks an unparseable line so callers can distinguish it from a
// transport EOF/IO error.
type protocolError struct{ err error }

func (e *protocolError) Error() string { return "plugin protocol: " + e.err.Error() }
func (e *protocolError) Unwrap() error { return e.err }
