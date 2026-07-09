package server

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/beetlebugorg/chartplotter/internal/engine/baker"
	tile57 "github.com/beetlebugorg/tile57/bindings/go"
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
	ID        string `json:"id"`
	Set       string `json:"set"`
	State     string `json:"state"`     // "running" | "done" | "error"
	Phase     string `json:"phase"`     // "download" | "extract" | "bake"
	Band      string `json:"band"`      // usage band being baked (e.g. "coastal"); "" outside the bake phase
	Pack      string `json:"pack"`      // set key of the pack being processed now (multi-pack import); "" for a single set
	PackNum   int    `json:"packNum"`   // 1-based position of the current pack in the batch (0 = n/a)
	PackTotal int    `json:"packTotal"` // packs in the batch (0/1 = single, no "N of M" shown)
	Note      string `json:"note"`      // human-readable current step (e.g. "downloading US5MD1MC")
	Done      int    `json:"done"`      // phase units done (bytes/cells downloaded, then tiles emitted)
	Total     int    `json:"total"`     // phase total (0 until known)
	ETA       int    `json:"eta,omitempty"` // seconds remaining in this phase (0 = unknown/none)
	Unit      string `json:"unit"`      // what done/total count: "bytes" | "cells" | "tiles"
	Cells     int    `json:"cells"`     // cells successfully parsed
	Err       string `json:"error,omitempty"`
	Started   string `json:"started"`
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

// running returns a copy of the most-recently-started still-running job, if any.
// The client uses this to RE-ATTACH after a page refresh, when it doesn't hold
// the job id but a bake/download may still be in flight.
func (j *importJobs) running() (importJob, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	var best *importJob
	for _, job := range j.m {
		if job.State != "running" {
			continue
		}
		if best == nil || job.Started > best.Started || (job.Started == best.Started && job.ID > best.ID) {
			best = job
		}
	}
	if best == nil {
		return importJob{}, false
	}
	return *best, true
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
	if r.URL.Path == "/api/import/packs" {
		s.handleImportPacks(w, r)
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
	// "auto" (or empty) means "name this upload from its CATALOG identity" — the
	// one-district-per-upload path (under the "user" provider). The real name is
	// derived below, after the zip is parsed (we need its catalogue / cell names first).
	autoName := set == "" || set == "auto"
	if !autoName && !isSetName(set) {
		apiErr(w, http.StatusBadRequest, "set must be a valid name")
		return
	}
	applyUpdates := r.URL.Query().Get("updates") != "0" // default: apply .001+ (NtM corrections)

	cells, aux, cat, err := s.importInputs(r)
	if err != nil {
		apiErr(w, http.StatusBadRequest, err.Error())
		return
	}
	// No cells supplied in the request → re-bake the provider from its cached ENC_ROOT
	// (a cache re-bake; ?set names the provider, e.g. "noaa").
	if len(cells) == 0 {
		provider := providerOf(set)
		if autoName || len(s.providerDistricts(provider)) == 0 {
			apiErr(w, http.StatusBadRequest, "no ENC base cells (.000) in input")
			return
		}
		job := s.imports.create(provider)
		go s.runImport(job.ID, provider)
		w.Header().Set("Content-Type", jsonCT)
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprintf(w, `{"ok":true,"job":%q,"set":%q}`, job.ID, provider)
		return
	}
	if autoName {
		set = s.deriveUploadSet(cat, cells)
	}
	if !applyUpdates { // bake the base .000 edition — persist base-only so the disk-read bake matches
		cells = baseOnly(cells)
	}
	// Persist the cells into the district's ENC_ROOT subfolder now that the name is
	// known, then bake the whole provider (all districts) into its one archive.
	provider, district := providerOf(set), districtOf(set)
	if district == "" {
		district = provider // a bare-provider upload → one district named for the provider
	}
	s.cacheDistrict(provider, district, cells, aux, cat)

	job := s.imports.create(provider)
	go s.runImport(job.ID, provider)

	w.Header().Set("Content-Type", jsonCT)
	w.WriteHeader(http.StatusAccepted)
	fmt.Fprintf(w, `{"ok":true,"job":%q,"set":%q}`, job.ID, provider)
}

// deriveUploadSet picks a stable, friendly pack key for an uploaded exchange set from
// its CATALOG identity (longest common cell-name prefix), falling back to the cells'
// shared prefix when there's no catalogue, then to "upload". Every upload is a DISTRICT
// under the "user" provider ("user-<id>"), uniquified against existing user districts.
func (s *Server) deriveUploadSet(cat []tile57.CatalogEntry, cells map[string]baker.CellData) string {
	id := catalogPackIdentity(cat)
	if id == "" {
		stems := make([]string, 0, len(cells))
		for n := range cells {
			stems = append(stems, strings.TrimSuffix(n, ".000"))
		}
		id = commonPrefixIdentity(stems)
	}
	if id == "" {
		id = "upload"
	}
	return "user-" + s.uniqueDistrict("user", id)
}

// uniqueDistrict returns base, or base-2/base-3/… if a district folder of that name
// already exists under the provider's ENC_ROOT, so a second upload of the same area
// doesn't clobber the first.
func (s *Server) uniqueDistrict(provider, base string) string {
	if _, err := os.Stat(s.districtDir(provider, base)); err != nil {
		return base
	}
	for i := 2; i < 1000; i++ {
		cand := base + "-" + itoa(i)
		if _, err := os.Stat(s.districtDir(provider, cand)); err != nil {
			return cand
		}
	}
	return base
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
	if req.ZipURL != "" && !isChartURL(req.ZipURL) {
		apiErr(w, http.StatusBadRequest, "zipUrl must be a charts.noaa.gov or ienccloud.us URL")
		return
	}
	for _, c := range req.Cells {
		if c.URL != "" && !isChartURL(c.URL) {
			apiErr(w, http.StatusBadRequest, "cell url must be a charts.noaa.gov or ienccloud.us URL")
			return
		}
	}

	job := s.imports.create(req.Set)
	go s.runImportFetch(job.ID, req)

	w.Header().Set("Content-Type", jsonCT)
	w.WriteHeader(http.StatusAccepted)
	fmt.Fprintf(w, `{"ok":true,"job":%q,"set":%q}`, job.ID, req.Set)
}

// runImportFetch downloads the requested cells from NOAA into the district's ENC_ROOT
// subfolder (reporting download progress on the job), then bakes + registers the
// provider (its whole ENC_ROOT) as one archive.
func (s *Server) runImportFetch(jobID string, req importFetchReq) {
	fail := func(err error) {
		log.Printf("import %s (%s): %v", jobID, req.Set, err)
		s.imports.update(jobID, func(j *importJob) { j.State = "error"; j.Err = err.Error() })
	}
	applyUpdates := req.Updates == nil || *req.Updates // default: apply .001+
	provider, district := providerOf(req.Set), districtOf(req.Set)
	if district == "" {
		district = provider
	}

	var cells map[string]baker.CellData
	var aux map[string][]byte
	var cat []tile57.CatalogEntry

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
		cells, aux, cat, err = extractZipCells(data)
		if err != nil {
			fail(err)
			return
		}
		if len(req.Names) > 0 {
			cells = filterCells(cells, req.Names)
		}
		if !applyUpdates {
			cells = baseOnly(cells)
		}
		// Persist the extracted cells to the district's ENC_ROOT subfolder (the bake reads them there).
		s.cacheDistrict(provider, district, cells, aux, cat)
	} else {
		// Per-cell: download each into the district's ENC_ROOT subfolder, then bake from there.
		cells = map[string]baker.CellData{}
		total := len(req.Cells)
		s.imports.update(jobID, func(j *importJob) { j.Phase, j.Unit, j.Total = "download", "cells", total })
		for i, c := range req.Cells {
			if !isCellName(c.Name) {
				continue
			}
			// SSRF guard: skip any cell whose download URL no provider handles.
			if c.URL != "" && !allowedChartURL(c.URL) {
				log.Printf("import %s: skip %s: disallowed url", jobID, c.Name)
				continue
			}
			s.imports.update(jobID, func(j *importJob) { j.Note = "Downloading " + c.Name; j.Done = i })
			base, _, err := loadCellCached(chartHTTPClient, s.districtDir(provider, district), c.Name, c.URL)
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
	// Download-only: the cells are now cached in the district's ENC_ROOT subfolder; the
	// client triggers the bake separately (e.g. via /api/import/packs). Done.
	if req.DownloadOnly {
		log.Printf("import %s: downloaded %d cell(s) into %s", jobID, len(cells), s.districtDir(provider, district))
		s.imports.update(jobID, func(j *importJob) { j.Cells = len(cells); j.State = "done" })
		return
	}
	s.bakeAndRegister(jobID, provider)
}

// baseOnly returns cells with their .001+ updates dropped — for a base-.000-edition
// bake (the ?updates=0 mode). Applied BEFORE caching, so the persisted ENC_ROOT holds
// base-only and the disk-read bake matches the choice.
func baseOnly(cells map[string]baker.CellData) map[string]baker.CellData {
	out := make(map[string]baker.CellData, len(cells))
	for n, cd := range cells {
		out[n] = baker.CellData{Base: cd.Base}
	}
	return out
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

// isChartURL reports whether raw is an http(s) URL a registered chart provider
// handles. Thin alias over the shared allowlist (providers.go).
func isChartURL(raw string) bool { return allowedChartURL(raw) }

// fetchURLProgress downloads raw (capped at maxImportBytes) and returns the bytes,
// calling onProgress(bytesSoFar, contentLength) as it streams (contentLength is 0
// when the server sends no Content-Length).
func fetchURLProgress(raw string, onProgress func(done, total int)) ([]byte, error) {
	resp, err := chartHTTPClient.Get(raw)
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

// importInputs gathers the cells to bake from the request itself: an uploaded zip (raw
// body or a multipart "file" field), or specific named LOOSE cells (?cells=csv). It
// returns nil cells when the request carries none — the signal for handleImport to
// re-bake the provider from its already-cached ENC_ROOT.
func (s *Server) importInputs(r *http.Request) (map[string]baker.CellData, map[string][]byte, []tile57.CatalogEntry, error) {
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "multipart/form-data") {
		f, _, err := r.FormFile("file")
		if err != nil {
			return nil, nil, nil, fmt.Errorf("multipart: %w", err)
		}
		defer f.Close()
		data, err := io.ReadAll(io.LimitReader(f, maxImportBytes))
		if err != nil {
			return nil, nil, nil, err
		}
		return extractZipCells(data)
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxImportBytes))
	if err != nil {
		return nil, nil, nil, err
	}
	if isZip(body) {
		return extractZipCells(body)
	}
	// No (zip) body → bake specific named LOOSE cells (a lone .000 drop PUT via /api/cell,
	// or a hand-picked list), which handleImport then writes into the district's ENC_ROOT
	// subfolder; or, with no ?cells list, return nil → re-bake the provider's cached ENC_ROOT.
	if csv := r.URL.Query().Get("cells"); csv != "" {
		return s.looseCellData(csv), nil, nil, nil
	}
	return nil, nil, nil, nil
}

// looseCellData reads the named base cells (+ their .001… updates) from the loose-cell
// dir — where /api/cell uploads/proxies land — for the "bake these specific cells into a
// pack" import (a lone .000 drop, or a re-bake of a hand-picked cell list).
func (s *Server) looseCellData(csv string) map[string]baker.CellData {
	dir := s.looseCellsDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	cells := map[string]baker.CellData{}
	for name := range strings.SplitSeq(csv, ",") {
		name = strings.TrimSpace(name)
		if name == "" || !isCellName(name) {
			continue
		}
		base, err := os.ReadFile(filepath.Join(dir, name+".000"))
		if err != nil {
			continue
		}
		cd := baker.CellData{Base: base, Updates: map[string][]byte{}}
		for _, e := range entries { // pick up this stem's updates sitting flat beside the base
			if e.IsDir() || strings.TrimSuffix(e.Name(), filepath.Ext(e.Name())) != name {
				continue
			}
			if ext := encExtServer(e.Name()); ext != "" && ext != ".000" {
				if b, e2 := os.ReadFile(filepath.Join(dir, e.Name())); e2 == nil {
					cd.Updates[e.Name()] = b
				}
			}
		}
		cells[name+".000"] = cd
	}
	return cells
}

// maxImportBytes caps an uploaded exchange set (a single NOAA district zip is well
// under this; the whole-nation All_ENCs.zip is multi-GB and is not an upload case).
const maxImportBytes = 2 << 30 // 2 GiB

// runImport (re-)bakes the provider's whole ENC_ROOT into its one archive and registers
// it — the shared tail for every single-set import path (upload, loose cells, cache
// re-bake). The cells are already persisted under the provider's ENC_ROOT.
func (s *Server) runImport(jobID, provider string) {
	s.bakeAndRegister(jobID, provider)
}

// bakeAndRegister bakes a provider (its whole ENC_ROOT) into ONE archive and records
// the terminal job state. Serializes with the packs path — a bake rewrites bundle
// output in place, which concurrent bakes must not interleave.
func (s *Server) bakeAndRegister(jobID, provider string) {
	s.bakeMu.Lock()
	defer s.bakeMu.Unlock()
	if s.bakeProvider(jobID, provider) {
		s.imports.update(jobID, func(j *importJob) { j.State = "done" })
	}
}

// setDir is the provider's baked-bundle output dir under the (regenerable) cache:
// <CACHE>/<PROVIDER>/ holding tiles/chart.pmtiles + assets + manifest + the <provider>
// sidecars (.aux.zip, .cells.json, .meta.json). ONE archive per provider
// (provider-enc-root); `set` is the provider name.
func (s *Server) setDir(set string) string {
	return filepath.Join(s.cacheDir, strings.ToUpper(set))
}

// looseCellsDir holds cells not tied to any pack — the /api/cell download proxy's
// cache and share-published hand-imported cells. A scoped replacement for the old flat
// ENC_ROOT's loose-cell role, still a cells/ dir so the cell index picks it up.
func (s *Server) looseCellsDir() string {
	return filepath.Join(s.dataDir, "loose", "cells")
}

// writeSetCells records the cell stems baked into `set` beside its pmtiles
// (<setDir>/<set>.cells.json). /api/cells?active reads these to return exactly the
// installed cells, instead of every cached cell whose bounds overlap the pack's
// (often global, for a worldwide-scattered import) bounding box.
func (s *Server) writeSetCells(set string, cells map[string]baker.CellData) error {
	stems := make([]string, 0, len(cells))
	for n := range cells {
		stems = append(stems, strings.TrimSuffix(n, ".000"))
	}
	sort.Strings(stems)
	dir := s.setDir(set)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(stems)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, set+".cells.json"), b, 0o644)
}

// setCells reads the cell-stem manifest written by writeSetCells for `set`, or nil
// (with ok=false) if the pack has none — a legacy pack baked before per-pack cell
// tracking, for which the caller falls back to bbox-overlap.
func (s *Server) setCells(set string) ([]string, bool) {
	data, err := os.ReadFile(filepath.Join(s.setDir(set), set+".cells.json"))
	if err != nil {
		return nil, false
	}
	var stems []string
	if json.Unmarshal(data, &stems) != nil {
		return nil, false
	}
	return stems, true
}

// statusJSON renders a job snapshot as the status JSON line (shared by the polling
// endpoint and the SSE stream).
func (j importJob) statusJSON() string {
	pct := 0
	if j.Total > 0 {
		pct = j.Done * 100 / j.Total
	}
	return fmt.Sprintf(
		`{"ok":true,"id":%q,"set":%q,"state":%q,"phase":%q,"band":%q,"pack":%q,"packNum":%d,"packTotal":%d,"note":%q,"done":%d,"total":%d,"unit":%q,"percent":%d,"cells":%d,"error":%q}`,
		j.ID, j.Set, j.State, j.Phase, j.Band, j.Pack, j.PackNum, j.PackTotal, j.Note, j.Done, j.Total, j.Unit, pct, j.Cells, j.Err)
}

// importStatus returns a job's state as JSON (one-shot poll).
func (s *Server) importStatus(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("job")
	if id == "" {
		// No id → "what's running right now?" (a refresh re-attach query). Report the
		// current running job so the client can resume tracking it, or idle.
		w.Header().Set("Content-Type", jsonCT)
		if job, ok := s.imports.running(); ok {
			io.WriteString(w, job.statusJSON())
		} else {
			io.WriteString(w, `{"ok":true,"state":"idle"}`)
		}
		return
	}
	job, ok := s.imports.snapshot(id)
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
func extractZipCells(data []byte) (map[string]baker.CellData, map[string][]byte, []tile57.CatalogEntry, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("not a valid zip: %w", err)
	}
	type acc struct {
		base    []byte
		updates map[string][]byte
	}
	byCell := map[string]*acc{}
	aux := map[string][]byte{}
	var catalogBytes []byte // CATALOG.031 — parsed after the loop for per-cell metadata
	for _, e := range zr.File {
		// CATALOG.031 must be tested FIRST: its ".031" extension otherwise looks like
		// an ENC update file to encExtServer and gets grouped as a baseless update.
		isCat := isCatalogFile(e.Name)
		ext := ""
		if !isCat {
			ext = encExtServer(e.Name)
		}
		isAux := ext == "" && isAuxContentServer(e.Name)
		if !isCat && ext == "" && !isAux {
			continue
		}
		rc, err := e.Open()
		if err != nil {
			return nil, nil, nil, err
		}
		b, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, nil, nil, err
		}
		if isCat {
			if catalogBytes == nil {
				catalogBytes = b
			}
			continue
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
	var cat []tile57.CatalogEntry
	if catalogBytes != nil {
		if entries, err := tile57.CatalogEntries(catalogBytes); err == nil {
			cat = entries
		} else {
			log.Printf("import: CATALOG.031 parse failed (ignored): %v", err)
		}
	}
	return cells, aux, cat, nil
}

// isCatalogFile reports whether a zip entry is an S-57 exchange-set catalogue
// (CATALOG.031). Matched by basename so it's found wherever it sits (ENC_ROOT/…).
func isCatalogFile(name string) bool {
	return strings.HasPrefix(strings.ToUpper(filepath.Base(name)), "CATALOG.")
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
