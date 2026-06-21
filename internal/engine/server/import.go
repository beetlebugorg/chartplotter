package server

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"maps"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/beetlebugorg/chartplotter/internal/engine/auxfiles"
	"github.com/beetlebugorg/chartplotter/internal/engine/bake"
	"github.com/beetlebugorg/chartplotter/internal/engine/baker"
	"github.com/beetlebugorg/chartplotter/internal/engine/pmtiles"
	"github.com/beetlebugorg/chartplotter/internal/engine/tilesource"
)

// Server-side import/bake. POST /api/import takes ENC input — an uploaded
// exchange-set zip, a JSON server-fetch spec (the SERVER pulls the cells from
// NOAA), or the cells already in the data store — and bakes a named tile SET with
// the same baker the CLI uses. Source cells live in the DATA dir (ENC_ROOT/, safe);
// the baked set + its companion aux.zip go to the regenerable CACHE under a
// provider/pack tree keyed by the set name ("<provider>-<pack>" → <PROVIDER>/<PACK>/;
// e.g. noaa-d17 → NOAA/D17/noaa-d17.{pmtiles,aux.zip}). Baking takes
// seconds-to-minutes, so it runs as a background job the client follows via
// GET /api/import/status (poll) or /api/import/events (SSE).

// importJob is a single background bake's state.
type importJob struct {
	ID      string `json:"id"`
	Set     string `json:"set"`
	State   string `json:"state"` // "running" | "done" | "error"
	Phase   string `json:"phase"` // "download" | "extract" | "bake"
	Note    string `json:"note"`  // human-readable current step (e.g. "downloading US5MD1MC")
	Done    int    `json:"done"`  // phase units done (bytes/cells downloaded, then tiles emitted)
	Total   int    `json:"total"` // phase total (0 until known)
	Unit    string `json:"unit"`  // what done/total count: "bytes" | "cells" | "tiles"
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
	if r.URL.Path == "/api/import/events" {
		s.importEvents(w, r)
		return
	}
	if r.URL.Path != "/api/import" || r.Method != http.MethodPost {
		apiErr(w, http.StatusMethodNotAllowed, "POST /api/import")
		return
	}
	// JSON body → server-side fetch+bake (the download path: the server pulls the
	// cells from NOAA itself rather than the client downloading + re-uploading).
	if strings.HasPrefix(r.Header.Get("Content-Type"), jsonCT) {
		s.handleImportFetch(w, r)
		return
	}

	set := r.URL.Query().Get("set")
	if !isSetName(set) {
		apiErr(w, http.StatusBadRequest, "set must be a valid name")
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

// importFetchReq is the JSON body of a server-side download+bake. Either zipURL
// (one NOAA exchange-set/district zip the server fetches + extracts) or cells (a
// list of per-cell NOAA zip URLs) supplies the cells; the server downloads them
// from NOAA itself, then bakes set.
type importFetchReq struct {
	Set      string   `json:"set"`
	Overzoom bool     `json:"overzoom"`
	Updates  *bool    `json:"updates"` // nil → apply .001+ (default)
	ZipURL   string   `json:"zipUrl"`  // bulk: one NOAA zip to fetch + extract
	Names    []string `json:"names"`   // for zipUrl: keep only these base cells (empty → all)
	Cells    []struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	} `json:"cells"` // per-cell: each cell's own NOAA zip URL
	// Bake, if set, is the FULL set of cell names to bake from the cache (the union
	// of everything installed) after the download — so adding one district rebakes
	// the whole "user" set, not just the new cells. Empty → bake only what was
	// downloaded this call.
	Bake []string `json:"bake"`
	// DownloadOnly fetches the cells into the server cache and finishes WITHOUT
	// baking — the client then triggers a single union bake (POST /api/import
	// cells=…). This keeps one bake path while moving the NOAA fetch server-side.
	DownloadOnly bool `json:"downloadOnly"`
}

// handleImportFetch accepts a JSON fetch spec, validates it, and starts a job that
// downloads the cells from NOAA server-side and bakes them.
func (s *Server) handleImportFetch(w http.ResponseWriter, r *http.Request) {
	var req importFetchReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		apiErr(w, http.StatusBadRequest, "bad JSON: "+err.Error())
		return
	}
	if req.Set == "" {
		req.Set = r.URL.Query().Get("set")
	}
	if !isSetName(req.Set) {
		apiErr(w, http.StatusBadRequest, "set must be a valid name")
		return
	}
	if req.ZipURL == "" && len(req.Cells) == 0 {
		apiErr(w, http.StatusBadRequest, "need zipUrl or cells")
		return
	}
	if req.ZipURL != "" && !isNOAAURL(req.ZipURL) {
		apiErr(w, http.StatusBadRequest, "zipUrl must be a charts.noaa.gov URL")
		return
	}
	for _, c := range req.Cells {
		if c.URL != "" && !isNOAAURL(c.URL) {
			apiErr(w, http.StatusBadRequest, "cell url must be a charts.noaa.gov URL")
			return
		}
	}

	job := s.imports.create(req.Set)
	go s.runImportFetch(job.ID, req)

	w.Header().Set("Content-Type", jsonCT)
	w.WriteHeader(http.StatusAccepted)
	fmt.Fprintf(w, `{"ok":true,"job":%q,"set":%q}`, job.ID, req.Set)
}

// runImportFetch downloads the requested cells from NOAA into the server cache
// (reporting download progress on the job), then bakes + registers the set.
func (s *Server) runImportFetch(jobID string, req importFetchReq) {
	fail := func(err error) {
		log.Printf("import %s (%s): %v", jobID, req.Set, err)
		s.imports.update(jobID, func(j *importJob) { j.State = "error"; j.Err = err.Error() })
	}
	applyUpdates := req.Updates == nil || *req.Updates

	var cells map[string]baker.CellData
	var aux map[string][]byte

	if req.ZipURL != "" {
		// Bulk: stream the one zip (byte progress), then extract + cache its cells.
		name := req.ZipURL[strings.LastIndexByte(req.ZipURL, '/')+1:]
		s.imports.update(jobID, func(j *importJob) {
			j.Phase, j.Unit, j.Note, j.Done, j.Total = "download", "bytes", "Downloading "+name, 0, 0
		})
		data, err := fetchURLProgress(req.ZipURL, func(done, total int) {
			s.imports.update(jobID, func(j *importJob) { j.Done, j.Total = done, total })
		})
		if err != nil {
			fail(fmt.Errorf("download %s: %w", req.ZipURL, err))
			return
		}
		s.imports.update(jobID, func(j *importJob) {
			j.Phase, j.Unit, j.Note, j.Done, j.Total = "extract", "cells", "Extracting "+name, 0, 0
		})
		cells, aux, err = extractZipCells(data)
		if err != nil {
			fail(err)
			return
		}
		if len(req.Names) > 0 {
			cells = filterCells(cells, req.Names)
		}
		// Persist the extracted cells to the ENC_ROOT cache so a later rebake of the
		// installed union (req.Bake) finds them (per-cell downloads cache themselves).
		s.cacheCells(cells)
	} else {
		// Per-cell: download each into the ENC_ROOT cache, then bake from there.
		cells = map[string]baker.CellData{}
		total := len(req.Cells)
		s.imports.update(jobID, func(j *importJob) { j.Phase, j.Unit, j.Total = "download", "cells", total })
		for i, c := range req.Cells {
			if !isCellName(c.Name) {
				continue
			}
			s.imports.update(jobID, func(j *importJob) { j.Note = "Downloading " + c.Name; j.Done = i })
			base, _, err := loadCellCached(http.DefaultClient, s.dataDir, c.Name, c.URL)
			if err != nil {
				log.Printf("import %s: download %s: %v", jobID, c.Name, err) // skip, keep going
			} else {
				cells[c.Name+".000"] = baker.CellData{Base: base}
			}
			s.imports.update(jobID, func(j *importJob) { j.Done = i + 1 })
		}
	}

	if len(cells) == 0 {
		fail(fmt.Errorf("no cells downloaded"))
		return
	}
	// Download-only: the cells are now in the XDG cache (ENC_ROOT/); the client
	// triggers the union bake separately. Done.
	if req.DownloadOnly {
		log.Printf("import %s: downloaded %d cell(s) into the cache", jobID, len(cells))
		s.imports.update(jobID, func(j *importJob) { j.Cells = len(cells); j.State = "done" })
		return
	}
	// Bake the full installed union (req.Bake) from the cache, with the freshly
	// downloaded cells merged in; or just the downloaded set when Bake is empty.
	bakeMap := cells
	if len(req.Bake) > 0 {
		bakeMap = s.cachedCellData(strings.Join(req.Bake, ","))
		maps.Copy(bakeMap, cells)
	}
	s.bakeAndRegister(jobID, req.Set, bakeMap, aux, req.Overzoom, applyUpdates)
}

// cacheCells writes each cell's base (+updates) into the ENC_ROOT cache layout so
// a later cache bake (cachedCellData) finds it. Best-effort; write errors are logged.
func (s *Server) cacheCells(cells map[string]baker.CellData) {
	root := filepath.Join(s.dataDir, "ENC_ROOT")
	for name, cd := range cells {
		stem := strings.TrimSuffix(name, ".000")
		if !isCellName(stem) {
			continue
		}
		dir := filepath.Join(root, stem)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			log.Printf("cache %s: %v", stem, err)
			continue
		}
		if err := os.WriteFile(filepath.Join(dir, stem+".000"), cd.Base, 0o644); err != nil {
			log.Printf("cache %s: %v", stem, err)
		}
		for un, ub := range cd.Updates {
			_ = os.WriteFile(filepath.Join(dir, filepath.Base(un)), ub, 0o644)
		}
	}
}

// filterCells keeps only the cells whose stem (name sans .000) is in names.
func filterCells(cells map[string]baker.CellData, names []string) map[string]baker.CellData {
	want := make(map[string]bool, len(names))
	for _, n := range names {
		want[strings.TrimSuffix(n, ".000")] = true
	}
	out := make(map[string]baker.CellData, len(want))
	for k, v := range cells {
		if want[strings.TrimSuffix(k, ".000")] {
			out[k] = v
		}
	}
	return out
}

// isNOAAURL reports whether raw is an http(s) URL on a NOAA chart host.
func isNOAAURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && isNOAAHost(u.Hostname())
}

// fetchURLProgress downloads raw (capped at maxImportBytes) and returns the bytes,
// calling onProgress(bytesSoFar, contentLength) as it streams (contentLength is 0
// when the server sends no Content-Length).
func fetchURLProgress(raw string, onProgress func(done, total int)) ([]byte, error) {
	resp, err := http.DefaultClient.Get(raw)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http %d", resp.StatusCode)
	}
	total := max(int(resp.ContentLength), 0)
	var out bytes.Buffer
	buf := make([]byte, 256<<10)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			out.Write(buf[:n])
			if out.Len() > maxImportBytes {
				return nil, fmt.Errorf("download exceeds %d bytes", maxImportBytes)
			}
			if onProgress != nil {
				onProgress(out.Len(), total)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return nil, rerr
		}
	}
	return out.Bytes(), nil
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
	root := filepath.Join(s.dataDir, "ENC_ROOT")
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
func (s *Server) runImport(jobID, set string, cells map[string]baker.CellData, aux map[string][]byte, overzoom, applyUpdates bool) {
	s.bakeAndRegister(jobID, set, cells, aux, overzoom, applyUpdates)
}

// bakeAndRegister is the shared bake → write → register tail for every import
// path (upload, cached, server-fetch). Progress and the terminal state are
// recorded on the job.
func (s *Server) bakeAndRegister(jobID, set string, cells map[string]baker.CellData, aux map[string][]byte, overzoom, applyUpdates bool) {
	fail := func(err error) {
		log.Printf("import %s (%s): %v", jobID, set, err)
		s.imports.update(jobID, func(j *importJob) { j.State = "error"; j.Err = err.Error() })
	}

	b, err := bakeCells(cells, overzoom, applyUpdates)
	if err != nil {
		fail(err)
		return
	}
	s.imports.update(jobID, func(j *importJob) {
		j.Cells = b.ok
		j.Phase, j.Unit, j.Note, j.Done, j.Total = "bake", "tiles", fmt.Sprintf("Baking %d cell(s)", b.ok), 0, 0
	})

	pb := baker.BakeToPMTiles(b.baker, func(done, total int) {
		s.imports.update(jobID, func(j *importJob) { j.Done, j.Total = done, total })
	})

	if err := s.writeAndRegister(set, pb, aux); err != nil {
		fail(err)
		return
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

// setDir is the per-set output directory under the (regenerable) cache. A set name
// is "<provider>-<pack>" (e.g. "noaa-d17", "ienc-overview"), which maps to
// <CACHE>/<PROVIDER>/<PACK>/ so packs from different providers (NOAA districts, IENC
// waterways, …) live in their own trees. A name with no provider prefix (a local
// import, e.g. "import") goes to <CACHE>/import/. The set's pmtiles + aux.zip live
// together there: <dir>/<set>.{pmtiles,aux.zip}.
func (s *Server) setDir(set string) string {
	if i := strings.IndexByte(set, '-'); i > 0 && i < len(set)-1 {
		provider, pack := strings.ToUpper(set[:i]), strings.ToUpper(set[i+1:])
		return filepath.Join(s.cacheDir, provider, pack)
	}
	return filepath.Join(s.cacheDir, "import")
}

// writeAndRegister writes the baked archive to <setDir>/<set>.pmtiles atomically
// (temp + rename), writes the companion <set>.aux.zip beside it (TXTDSC/PICREP, via
// the auxfiles package), and registers the set (replacing any prior one).
func (s *Server) writeAndRegister(set string, pb *pmtiles.Builder, aux map[string][]byte) error {
	dir := s.setDir(set)
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
	// Companion aux.zip (best-effort — a missing aux archive only disables pictures
	// in the pick report, it doesn't break tiles).
	if len(aux) > 0 {
		if f, e := os.Create(filepath.Join(dir, set+".aux.zip")); e == nil {
			if _, e := auxfiles.WriteZip(f, aux); e != nil {
				log.Printf("aux %s: %v", set, e)
			}
			f.Close()
		}
	}
	src, err := tilesource.Open(final)
	if err != nil {
		return err
	}
	s.sets.register(set, src)
	return nil
}

// statusJSON renders a job snapshot as the status JSON line (shared by the polling
// endpoint and the SSE stream).
func (j importJob) statusJSON() string {
	pct := 0
	if j.Total > 0 {
		pct = j.Done * 100 / j.Total
	}
	return fmt.Sprintf(
		`{"ok":true,"id":%q,"set":%q,"state":%q,"phase":%q,"note":%q,"done":%d,"total":%d,"unit":%q,"percent":%d,"cells":%d,"error":%q}`,
		j.ID, j.Set, j.State, j.Phase, j.Note, j.Done, j.Total, j.Unit, pct, j.Cells, j.Err)
}

// importStatus returns a job's state as JSON (one-shot poll).
func (s *Server) importStatus(w http.ResponseWriter, r *http.Request) {
	job, ok := s.imports.snapshot(r.URL.Query().Get("job"))
	if !ok {
		apiErr(w, http.StatusNotFound, "unknown job")
		return
	}
	w.Header().Set("Content-Type", jsonCT)
	io.WriteString(w, job.statusJSON())
}

// importEvents streams a job's progress as Server-Sent Events, so the client opens
// ONE long-lived connection instead of polling. It emits a "data:" event whenever
// the status line changes (checked on a short server-side tick — cheap in-memory
// reads, no per-update fan-out), and a final event when the job ends. Closes on
// completion or client disconnect.
func (s *Server) importEvents(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("job")
	if _, ok := s.imports.snapshot(id); !ok {
		apiErr(w, http.StatusNotFound, "unknown job")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		apiErr(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	last := ""
	for {
		job, ok := s.imports.snapshot(id)
		if !ok {
			return
		}
		if line := job.statusJSON(); line != last {
			last = line
			fmt.Fprintf(w, "data: %s\n\n", line)
			flusher.Flush()
		}
		if job.State != "running" {
			return // terminal state emitted; close the stream
		}
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		}
	}
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
