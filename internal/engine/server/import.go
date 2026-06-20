package server

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/beetlebugorg/chartplotter/internal/engine/bake"
	"github.com/beetlebugorg/chartplotter/internal/engine/baker"
	"github.com/beetlebugorg/chartplotter/internal/engine/pmtiles"
	"github.com/beetlebugorg/chartplotter/internal/engine/tilesource"
)

// Phase 2: server-side import/bake. POST /api/import takes ENC input (an uploaded
// exchange-set zip, or — with no body — the cells already in the ENC_ROOT cache),
// bakes it natively into <cache>/tiles/<set>.pmtiles with the same baker the CLI
// uses, and registers it as a tile set served at /tiles/<set>/…. Baking a district
// takes seconds-to-minutes, so it runs as a background job the client polls via
// GET /api/import/status?job=<id>. Aux (TXTDSC/PICREP) files are stashed under
// <cache>/aux/<set>/ for the Phase 4 feature-file API.

// importJob is a single background bake's state.
type importJob struct {
	ID      string `json:"id"`
	Set     string `json:"set"`
	State   string `json:"state"` // "running" | "done" | "error"
	Done    int    `json:"done"`  // tiles emitted
	Total   int    `json:"total"` // tiles to emit (0 until known)
	Cells   int    `json:"cells"` // cells successfully parsed
	Err     string `json:"error,omitempty"`
	Started string `json:"started"`
}

// importJobs is the (in-memory) registry of import jobs.
type importJobs struct {
	mu  sync.Mutex
	m   map[string]*importJob
	seq int
}

func newImportJobs() *importJobs { return &importJobs{m: map[string]*importJob{}} }

// create registers a new running job for set and returns it (a copy-safe pointer
// guarded by the store's lock; mutate only via update).
func (j *importJobs) create(set string) *importJob {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.seq++
	job := &importJob{
		ID:      fmt.Sprintf("imp-%d-%d", time.Now().Unix(), j.seq),
		Set:     set,
		State:   "running",
		Started: time.Now().UTC().Format(time.RFC3339),
	}
	j.m[job.ID] = job
	return job
}

// update mutates the named job under the lock.
func (j *importJobs) update(id string, f func(*importJob)) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if job, ok := j.m[id]; ok {
		f(job)
	}
}

// snapshot returns a copy of the named job's state.
func (j *importJobs) snapshot(id string) (importJob, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	job, ok := j.m[id]
	if !ok {
		return importJob{}, false
	}
	return *job, true
}

// handleImport routes the import endpoints (already past the /api host check).
func (s *Server) handleImport(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/import/status" {
		s.importStatus(w, r)
		return
	}
	if r.URL.Path != "/api/import" || r.Method != http.MethodPost {
		apiErr(w, http.StatusMethodNotAllowed, "POST /api/import")
		return
	}
	set := r.URL.Query().Get("set")
	if !isSetName(set) || set == dynamicSetName {
		apiErr(w, http.StatusBadRequest, "set must be a valid name (and not 'dynamic')")
		return
	}
	overzoom := r.URL.Query().Get("overzoom") == "1"
	applyUpdates := r.URL.Query().Get("updates") != "0" // default: apply .001+

	cells, aux, err := s.importInputs(r)
	if err != nil {
		apiErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(cells) == 0 {
		apiErr(w, http.StatusBadRequest, "no ENC base cells (.000) in input")
		return
	}

	job := s.imports.create(set)
	go s.runImport(job.ID, set, cells, aux, overzoom, applyUpdates)

	w.Header().Set("Content-Type", jsonCT)
	w.WriteHeader(http.StatusAccepted)
	fmt.Fprintf(w, `{"ok":true,"job":%q,"set":%q}`, job.ID, set)
}

// importInputs gathers the cells to bake: from an uploaded zip (raw zip body or a
// multipart "file" field) when one is present, else from the ENC_ROOT cache
// (optionally narrowed by ?cells=A,B,C).
func (s *Server) importInputs(r *http.Request) (map[string]baker.CellData, map[string][]byte, error) {
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "multipart/form-data") {
		f, _, err := r.FormFile("file")
		if err != nil {
			return nil, nil, fmt.Errorf("multipart: %w", err)
		}
		defer f.Close()
		data, err := io.ReadAll(io.LimitReader(f, maxImportBytes))
		if err != nil {
			return nil, nil, err
		}
		return extractZipCells(data)
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxImportBytes))
	if err != nil {
		return nil, nil, err
	}
	if isZip(body) {
		return extractZipCells(body)
	}
	// No (zip) body → bake from the cached cells.
	return s.cachedCellData(r.URL.Query().Get("cells")), nil, nil
}

// maxImportBytes caps an uploaded exchange set (a single NOAA district zip is well
// under this; the whole-nation All_ENCs.zip is multi-GB and is not an upload case).
const maxImportBytes = 2 << 30 // 2 GiB

// cachedCellData builds CellData (base + updates) from the ENC_ROOT cache, for the
// no-upload import mode. csv, when non-empty, narrows to those cell names.
func (s *Server) cachedCellData(csv string) map[string]baker.CellData {
	want := map[string]bool{}
	for n := range strings.SplitSeq(csv, ",") {
		if n = strings.TrimSpace(n); n != "" {
			want[n] = true
		}
	}
	root := filepath.Join(s.cacheDir, "ENC_ROOT")
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	cells := map[string]baker.CellData{}
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || !isCellName(name) || (len(want) > 0 && !want[name]) {
			continue
		}
		base, err := os.ReadFile(filepath.Join(root, name, name+".000"))
		if err != nil {
			continue
		}
		cd := baker.CellData{Base: base, Updates: map[string][]byte{}}
		// Pick up any update files (.001…) sitting beside the base.
		if files, _ := os.ReadDir(filepath.Join(root, name)); files != nil {
			for _, uf := range files {
				if ext := encExtServer(uf.Name()); ext != "" && ext != ".000" {
					if b, e := os.ReadFile(filepath.Join(root, name, uf.Name())); e == nil {
						cd.Updates[uf.Name()] = b
					}
				}
			}
		}
		cells[name+".000"] = cd
	}
	return cells
}

// runImport bakes cells into <cache>/tiles/<set>.pmtiles and registers the set.
// Progress and the terminal state are recorded on the job.
func (s *Server) runImport(jobID, set string, cells map[string]baker.CellData, aux map[string][]byte, overzoom, applyUpdates bool) {
	fail := func(err error) {
		log.Printf("import %s (%s): %v", jobID, set, err)
		s.imports.update(jobID, func(j *importJob) { j.State = "error"; j.Err = err.Error() })
	}

	b, err := bakeCells(cells, overzoom, applyUpdates)
	if err != nil {
		fail(err)
		return
	}
	s.imports.update(jobID, func(j *importJob) { j.Cells = b.ok })

	pb := baker.BakeToPMTiles(b.baker, func(done, total int) {
		s.imports.update(jobID, func(j *importJob) { j.Done, j.Total = done, total })
	})

	if err := s.writeAndRegister(set, pb); err != nil {
		fail(err)
		return
	}
	if len(aux) > 0 {
		if err := s.writeAux(set, aux); err != nil {
			log.Printf("import %s: aux: %v", jobID, err) // non-fatal
		}
	}
	log.Printf("import %s: baked %q (%d cells, %d tiles)", jobID, set, b.ok, pb.Count())
	s.imports.update(jobID, func(j *importJob) { j.State = "done" })
}

// bakerResult bundles a built Baker with the count of cells that parsed.
type bakerResult struct {
	baker *bake.Baker
	ok    int
}

// bakeCells builds a Baker from cells, applying updates unless disabled.
func bakeCells(cells map[string]baker.CellData, overzoom, applyUpdates bool) (*bakerResult, error) {
	if !applyUpdates { // strip updates so cells bake at their base .000 edition
		base := make(map[string]baker.CellData, len(cells))
		for n, cd := range cells {
			base[n] = baker.CellData{Base: cd.Base}
		}
		cells = base
	}
	b, ok, err := baker.BuildBakerWithUpdates(cells, overzoom, func(name string, err error) {
		log.Printf("import: skip %s: %v", name, err)
	})
	if err != nil {
		return nil, err
	}
	if len(ok) == 0 {
		return nil, fmt.Errorf("no cells parsed successfully")
	}
	return &bakerResult{baker: b, ok: len(ok)}, nil
}

// writeAndRegister writes the baked archive to <cache>/tiles/<set>.pmtiles
// atomically (temp + rename) and registers it (replacing any prior set).
func (s *Server) writeAndRegister(set string, pb *pmtiles.Builder) error {
	dir := tilesDir(s.cacheDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	final := filepath.Join(dir, set+".pmtiles")
	tmp, err := os.CreateTemp(dir, set+".*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if err := pb.WriteArchive(tmp); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, final); err != nil {
		os.Remove(tmpName)
		return err
	}
	src, err := tilesource.Open(final)
	if err != nil {
		return err
	}
	s.sets.register(set, src)
	return nil
}

// writeAux stashes the referenced aux files raw under <cache>/aux/<set>/<KEY> for
// the Phase 4 feature-file API (transcoding/serving lands there).
func (s *Server) writeAux(set string, aux map[string][]byte) error {
	dir := filepath.Join(s.cacheDir, "aux", set)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for key, data := range aux {
		if err := os.WriteFile(filepath.Join(dir, filepath.Base(key)), data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// importStatus returns a job's state as JSON.
func (s *Server) importStatus(w http.ResponseWriter, r *http.Request) {
	job, ok := s.imports.snapshot(r.URL.Query().Get("job"))
	if !ok {
		apiErr(w, http.StatusNotFound, "unknown job")
		return
	}
	w.Header().Set("Content-Type", jsonCT)
	pct := 0
	if job.Total > 0 {
		pct = job.Done * 100 / job.Total
	}
	fmt.Fprintf(w,
		`{"ok":true,"id":%q,"set":%q,"state":%q,"done":%d,"total":%d,"percent":%d,"cells":%d,"error":%q}`,
		job.ID, job.Set, job.State, job.Done, job.Total, pct, job.Cells, job.Err)
}

// extractZipCells reads an exchange-set zip held in memory, grouping each cell's
// base (.000) + updates (.001…) by cell stem and collecting referenced aux files.
// It mirrors the CLI's collectCells/addZipCells for an in-memory archive.
func extractZipCells(data []byte) (map[string]baker.CellData, map[string][]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, nil, fmt.Errorf("not a valid zip: %w", err)
	}
	type acc struct {
		base    []byte
		updates map[string][]byte
	}
	byCell := map[string]*acc{}
	aux := map[string][]byte{}
	for _, e := range zr.File {
		ext := encExtServer(e.Name)
		isAux := ext == "" && isAuxContentServer(e.Name)
		if ext == "" && !isAux {
			continue
		}
		rc, err := e.Open()
		if err != nil {
			return nil, nil, err
		}
		b, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, nil, err
		}
		if isAux {
			if k := strings.ToUpper(filepath.Base(e.Name)); aux[k] == nil {
				aux[k] = b
			}
			continue
		}
		base := filepath.Base(e.Name)
		stem := strings.TrimSuffix(base, filepath.Ext(base))
		a := byCell[stem]
		if a == nil {
			a = &acc{updates: map[string][]byte{}}
			byCell[stem] = a
		}
		if ext == ".000" {
			if a.base == nil {
				a.base = b
			}
		} else {
			a.updates[base] = b
		}
	}
	cells := map[string]baker.CellData{}
	for stem, a := range byCell {
		if a.base == nil {
			continue // updates with no base
		}
		cells[stem+".000"] = baker.CellData{Base: a.base, Updates: a.updates}
	}
	return cells, aux, nil
}

// (helpers below)

// encExtServer reports the 3-digit S-57 cell extension (".000"/".001"…) for a
// path, or "" if it isn't an ENC cell file. (Server-side copy of the CLI helper.)
func encExtServer(p string) string {
	ext := strings.ToLower(filepath.Ext(p))
	if len(ext) == 4 && ext[0] == '.' && ext[1] >= '0' && ext[1] <= '9' && ext[2] >= '0' && ext[2] <= '9' && ext[3] >= '0' && ext[3] <= '9' {
		return ext
	}
	return ""
}

// isAuxContentServer reports whether a non-cell file is shippable aux content
// (TXTDSC text / PICREP pictures), excluding set plumbing. Mirrors the CLI helper.
func isAuxContentServer(name string) bool {
	base := strings.ToUpper(filepath.Base(name))
	if strings.HasPrefix(base, "README") || strings.HasPrefix(base, "CATALOG") {
		return false
	}
	switch strings.ToLower(filepath.Ext(name)) {
	case ".txt", ".tif", ".tiff", ".jpg", ".jpeg", ".png":
		return true
	}
	return false
}

// isZip reports whether b begins with the PK zip local-file magic.
func isZip(b []byte) bool {
	return len(b) >= 4 && b[0] == 'P' && b[1] == 'K' && (b[2] == 3 || b[2] == 5 || b[2] == 7)
}
