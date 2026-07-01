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
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/beetlebugorg/chartplotter/internal/engine/auxfiles"
	"github.com/beetlebugorg/chartplotter/internal/engine/baker"
	"github.com/beetlebugorg/chartplotter/internal/engine/pmtiles"
	"github.com/beetlebugorg/chartplotter/internal/engine/tilesource"
	"github.com/beetlebugorg/chartplotter/pkg/s57"
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
	Band    string `json:"band"`  // usage band being baked (e.g. "coastal"); "" outside the bake phase
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
	// one-pack-per-upload path. The real name is derived below, after the zip is
	// parsed (we need its catalogue / cell names first).
	autoName := set == "" || set == "auto"
	if !autoName && !isSetName(set) {
		apiErr(w, http.StatusBadRequest, "set must be a valid name")
		return
	}
	overzoom := r.URL.Query().Get("overzoom") == "1"
	applyUpdates := r.URL.Query().Get("updates") != "0" // default: apply .001+ (NtM corrections)

	cells, aux, cat, err := s.importInputs(r)
	if err != nil {
		apiErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(cells) == 0 {
		apiErr(w, http.StatusBadRequest, "no ENC base cells (.000) in input")
		return
	}
	if autoName {
		set = s.deriveUploadSet(cat, cells)
	}

	job := s.imports.create(set)
	go s.runImport(job.ID, set, cells, aux, cat, overzoom, applyUpdates)

	w.Header().Set("Content-Type", jsonCT)
	w.WriteHeader(http.StatusAccepted)
	fmt.Fprintf(w, `{"ok":true,"job":%q,"set":%q}`, job.ID, set)
}

// deriveUploadSet picks a stable, friendly pack name for an uploaded exchange set
// from its CATALOG identity (longest common cell-name prefix), falling back to the
// cells' shared prefix when there's no catalogue, then to "upload". Namespaced
// under the "user" provider and uniquified against existing packs.
func (s *Server) deriveUploadSet(cat *s57.Catalog, cells map[string]baker.CellData) string {
	id := ""
	if cat != nil {
		id = catalogPackIdentity(cat)
	}
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
	return s.uniqueSet("user-" + id)
}

// uniqueSet returns base, or base-2/base-3/… if a pack (any band-set of that
// district) already exists, so a second upload of the same area doesn't clobber
// the first.
func (s *Server) uniqueSet(base string) string {
	taken := func(name string) bool { return len(s.setsForDistrict(name)) > 0 }
	if !taken(base) {
		return base
	}
	for i := 2; i < 1000; i++ {
		cand := base + "-" + itoa(i)
		if !taken(cand) {
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

// runImportFetch downloads the requested cells from NOAA into the server cache
// (reporting download progress on the job), then bakes + registers the set.
func (s *Server) runImportFetch(jobID string, req importFetchReq) {
	fail := func(err error) {
		log.Printf("import %s (%s): %v", jobID, req.Set, err)
		s.imports.update(jobID, func(j *importJob) { j.State = "error"; j.Err = err.Error() })
	}
	applyUpdates := req.Updates == nil || *req.Updates // default: apply .001+

	var cells map[string]baker.CellData
	var aux map[string][]byte
	var cat *s57.Catalog

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
			// SSRF guard: skip any cell whose download URL no provider handles.
			if c.URL != "" && !allowedChartURL(c.URL) {
				log.Printf("import %s: skip %s: disallowed url", jobID, c.Name)
				continue
			}
			s.imports.update(jobID, func(j *importJob) { j.Note = "Downloading " + c.Name; j.Done = i })
			base, _, err := loadCellCached(chartHTTPClient, s.dataDir, c.Name, c.URL)
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
	s.bakeAndRegister(jobID, req.Set, bakeMap, aux, cat, req.Overzoom, applyUpdates)
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
	if s.cellIdx != nil {
		stems := make([]string, 0, len(cells))
		for name := range cells {
			stems = append(stems, strings.TrimSuffix(name, ".000"))
		}
		s.cellIdx.forget(stems) // re-imported cells: drop stale bounds so the rebuild re-parses
		s.cellIdx.rebuild()     // re-index in the background (kick spawns its own goroutine; dirty re-run picks up a reindex that lands mid-scan)
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

// importInputs gathers the cells to bake: from an uploaded zip (raw zip body or a
// multipart "file" field) when one is present, else from the ENC_ROOT cache
// (optionally narrowed by ?cells=A,B,C).
func (s *Server) importInputs(r *http.Request) (map[string]baker.CellData, map[string][]byte, *s57.Catalog, error) {
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
		return s.cacheExtracted(extractZipCells(data))
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxImportBytes))
	if err != nil {
		return nil, nil, nil, err
	}
	if isZip(body) {
		return s.cacheExtracted(extractZipCells(body))
	}
	// No (zip) body → bake from the cached cells (already on disk).
	return s.cachedCellData(r.URL.Query().Get("cells")), nil, nil, nil
}

// cacheExtracted persists freshly-extracted upload cells to the ENC_ROOT source
// cache before baking, so the ORIGINAL cell files are always kept (re-bakeable
// after a tile-cache wipe) rather than discarded after an in-memory bake. Passes
// the (cells, aux, err) triple straight through.
func (s *Server) cacheExtracted(cells map[string]baker.CellData, aux map[string][]byte, cat *s57.Catalog, err error) (map[string]baker.CellData, map[string][]byte, *s57.Catalog, error) {
	if err == nil && len(cells) > 0 {
		s.cacheCells(cells)
	}
	return cells, aux, cat, err
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

// bakeEngine returns the baker used for server imports. libtile57 is the only
// supported engine: a tile57-capable binary ALWAYS bakes with it. The old
// per-client "bakeEngine" setting (which could pin "go" and silently downgrade a
// tile57 server after a few UI bakes — the "reverts to go" bug) is gone. A
// CGO-free build has no native baker, so it falls back to the Go per-band baker.
func (s *Server) bakeEngine() string {
	if bakeTile57Available {
		return "tile57"
	}
	return "go"
}

// runImport bakes cells into <cache>/tiles/<set>.pmtiles and registers the set.
func (s *Server) runImport(jobID, set string, cells map[string]baker.CellData, aux map[string][]byte, cat *s57.Catalog, overzoom, applyUpdates bool) {
	s.bakeAndRegister(jobID, set, cells, aux, cat, overzoom, applyUpdates)
}

// bakeAndRegister is the shared bake → write → register tail for every import
// path (upload, cached, server-fetch). The district `set` is baked into ONE archive
// PER navigational-purpose band (set-overview, set-general, …), so a coarse-band-only
// offshore area keeps tiles above the old merged archive's single maxzoom (no more
// no-data hatch holes). Each band that produced tiles is written + registered as its
// own set; the district aux.zip is written once (with the first band). Progress and
// the terminal state are recorded on the job.
func (s *Server) bakeAndRegister(jobID, set string, cells map[string]baker.CellData, aux map[string][]byte, cat *s57.Catalog, overzoom, applyUpdates bool) {
	fail := func(err error) {
		log.Printf("import %s (%s): %v", jobID, set, err)
		s.imports.update(jobID, func(j *importJob) { j.State = "error"; j.Err = err.Error() })
	}

	if !applyUpdates { // bake at the base .000 edition — strip updates first
		base := make(map[string]baker.CellData, len(cells))
		for n, cd := range cells {
			base[n] = baker.CellData{Base: cd.Base}
		}
		cells = base
	}

	// Native libtile57 bundle bake (opt-in): one self-describing bundle per set
	// (tiles + SCAMIN-bucketed styles + assets), registered as a single set. The
	// engine is the "Advanced → bake engine" setting (capability-gated). Falls through
	// to the Go per-band path if unhandled.
	if s.bakeEngine() == "tile57" && s.bakeBundleTile57(jobID, set, cells, aux, cat, applyUpdates) {
		return
	}

	_ = overzoom // the per-band streaming bake has no all-bands-to-z0 overzoom mode
	s.imports.update(jobID, func(j *importJob) {
		// Open on the "prepare" stage (unit "cells"): the bake starts by parsing
		// cells for coverage, well before the first tile emits.
		j.Phase, j.Unit, j.Band, j.Note, j.Done, j.Total = "bake", "cells", "", fmt.Sprintf("Baking %d cell(s)", len(cells)), 0, 0
	})

	// Drop any STALE merged archive named exactly `set` from a prior (pre-per-band)
	// bake, so the old single-maxzoom set isn't left serving alongside the new bands.
	s.removeMergedSet(set)

	// ONE bake path: the exact streaming per-band bake the CLI (`chartplotter bake
	// --bands`) uses — same cross-band suppression, same zoom ranges. No server-only
	// baker variant to drift out of sync.
	bands, tiles, first := 0, 0, true
	_, nCells, err := baker.BakeToPMTilesBandsStreaming(cells, 0,
		func(name string, e error) { log.Printf("import %s: skip %s: %v", jobID, name, e) },
		func(stage string, done, total int, band string) {
			// "prepare" = parsing + portraying a band's cells (the gap before any
			// tile emits); "tiles" = emitting that band's tiles. The unit lets the
			// client name the stage (Preparing … charts vs Generating … tiles).
			unit := "tiles"
			if stage == "prepare" {
				unit = "cells"
			}
			s.imports.update(jobID, func(j *importJob) { j.Done, j.Total, j.Band, j.Unit = done, total, band, unit })
		},
		func(slug string, pb *pmtiles.Builder) error {
			bandSet := set + "-" + slug
			bandAux := aux
			if !first { // ship the district aux.zip ONCE, with the first band
				bandAux = nil
			}
			if err := s.writeAndRegister(bandSet, pb, bandAux); err != nil {
				return err
			}
			// Record which cells went into this pack (beside its pmtiles), so
			// /api/cells?active returns exactly the installed cells — not every
			// cached cell that overlaps the pack's (often global) bounding box.
			if err := s.writeSetCells(bandSet, cells); err != nil {
				log.Printf("import %s: cell manifest %q: %v", jobID, bandSet, err)
			}
			first = false
			bands++
			tiles += pb.Count()
			log.Printf("import %s: baked %q (%d tiles)", jobID, bandSet, pb.Count())
			return nil
		})
	if err != nil {
		fail(err)
		return
	}
	if bands == 0 {
		fail(fmt.Errorf("no bands produced tiles"))
		return
	}
	s.imports.update(jobID, func(j *importJob) { j.Cells = nCells })
	s.auxIdx.invalidate() // the district's companion aux.zip changed — re-index /api/aux

	// Per-pack metadata sidecar for the chart library: per-cell scale/edition/date/
	// agency/coverage (cheap coverage-only parse) overlaid with the catalogue's chart
	// titles + coverage. Best-effort — a write failure only costs the extracted detail.
	s.imports.update(jobID, func(j *importJob) { j.Phase, j.Note = "meta", "Reading chart metadata" })
	cellMeta := baker.ExtractCellMeta(cells, func(name string, e error) {
		log.Printf("import %s: meta skip %s: %v", jobID, name, e)
	})
	meta := buildSetMeta(set, cellMeta, cat)
	meta.Imported = time.Now().UTC().Format(time.RFC3339)
	if err := s.writeSetMeta(set, meta); err != nil {
		log.Printf("import %s: write meta %q: %v", jobID, set, err)
	}

	log.Printf("import %s: baked district %q (%d cells, %d bands, %d tiles)", jobID, set, nCells, bands, tiles)
	s.imports.update(jobID, func(j *importJob) { j.State = "done" })
}

// removeMergedSet drops a stale MERGED archive named exactly `set` (the pre-per-band
// layout) if one is still registered: unregister, untrack, and delete its
// <set>.pmtiles/.aux.zip. The per-band sets ("set-<slug>") are left alone. Best-effort.
func (s *Server) removeMergedSet(set string) {
	if _, ok := s.packPath(set); !ok {
		if _, live := s.sets.get(set); !live {
			return // no merged set on disk or registered
		}
	}
	s.sets.remove(set)
	s.packDel(set)
	s.prefs.setDisabled(set, false)
	dir := s.setDir(set)
	_ = os.Remove(filepath.Join(dir, set+".pmtiles"))
	_ = os.Remove(filepath.Join(dir, set+".aux.zip"))
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
	// Stamp the build version beside the pack (<pack>.bakever) so startup can flag a
	// cache baked by an OLDER binary — the stale-tile trap where the server serves
	// tiles from before a baker/portrayal change. Best-effort; absence reads as stale.
	if s.Version != "" {
		_ = os.WriteFile(final+bakeVerExt, []byte(s.Version), 0o644)
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
	s.packAdd(set, final)           // track for /api/packs + enable/disable
	s.prefs.setDisabled(set, false) // a freshly baked pack is enabled
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
		`{"ok":true,"id":%q,"set":%q,"state":%q,"phase":%q,"band":%q,"note":%q,"done":%d,"total":%d,"unit":%q,"percent":%d,"cells":%d,"error":%q}`,
		j.ID, j.Set, j.State, j.Phase, j.Band, j.Note, j.Done, j.Total, j.Unit, pct, j.Cells, j.Err)
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
func extractZipCells(data []byte) (map[string]baker.CellData, map[string][]byte, *s57.Catalog, error) {
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
	var cat *s57.Catalog
	if catalogBytes != nil {
		if c, err := s57.ParseCatalog(catalogBytes); err == nil {
			cat = c
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
