package server

import (
	"fmt"
	"strings"
	"sync"
)

// taskStatus / taskPhase mirror the CHARTS-UI-SPEC §3 task model.
type taskStatus int

const (
	statusDone taskStatus = iota
	statusRunning
	statusErr
)

type taskPhase int

const (
	phaseDownload taskPhase = iota
	phaseImport
)

func (p taskPhase) slug() string {
	if p == phaseImport {
		return "import"
	}
	return "download"
}

// task is the single background-provision job's shared state. Provisioning is a
// long server job: POST /api/provision starts it, the client polls GET
// /api/tasks for progress (so a page refresh never cancels or loses the bake).
// Only one job runs at a time; it owns charts-user.{pmtiles,json}.
type task struct {
	mu     sync.Mutex
	id     uint64 // 0 → no task has run yet (GET /api/tasks → {"task":null})
	status taskStatus
	phase  taskPhase
	done   int
	total  int
	cells  int // total cells in the job (for the client's size estimate)
	cell   string
	errMsg string
}

// tryBegin atomically claims the single job slot, returning the new id or (0,
// false) if a job is already running.
func (t *task) tryBegin(cells int) (uint64, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.status == statusRunning {
		return 0, false
	}
	t.id++
	t.status = statusRunning
	t.phase = phaseDownload
	t.done = 0
	t.total = cells
	t.cells = cells
	t.cell = ""
	t.errMsg = ""
	return t.id, true
}

func (t *task) setDownload(done, total int, cell string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.phase = phaseDownload
	t.done = done
	t.total = total
	t.cell = cell
}

func (t *task) setImport(done, total int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.phase = phaseImport
	t.done = done
	t.total = total
}

func (t *task) finishOk() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.status = statusDone
}

func (t *task) finishErr(msg string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.status = statusErr
	t.errMsg = msg
}

func (t *task) isRunning() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.status == statusRunning
}

func (t *task) currentID() uint64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.id
}

// jsonString writes a minimally-escaped JSON string (cell names and error
// identifiers are already constrained to safe characters).
func jsonString(b *strings.Builder, s string) {
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '"':
			b.WriteString("\\\"")
		case '\\':
			b.WriteString("\\\\")
		case '\n', '\r', '\t':
			b.WriteByte(' ')
		default:
			b.WriteByte(c)
		}
	}
	b.WriteByte('"')
}

// json renders the GET /api/tasks payload.
func (t *task) json() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.id == 0 {
		return `{"task":null}`
	}
	st := "done"
	switch t.status {
	case statusRunning:
		st = "running"
	case statusErr:
		st = "error"
	}
	var b strings.Builder
	fmt.Fprintf(&b, `{"task":%d,"kind":"provision","status":"%s","phase":"%s","done":%d,"total":%d,"cell":`,
		t.id, st, t.phase.slug(), t.done, t.total)
	jsonString(&b, t.cell)
	fmt.Fprintf(&b, `,"cells":%d,"error":`, t.cells)
	if t.errMsg != "" {
		jsonString(&b, t.errMsg)
	} else {
		b.WriteString("null")
	}
	b.WriteByte('}')
	return b.String()
}
