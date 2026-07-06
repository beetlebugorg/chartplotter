package server

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"

	"github.com/beetlebugorg/chartplotter/internal/engine/baker"
	tile57 "github.com/beetlebugorg/tile57/bindings/go"
)

// Multi-district import (POST /api/import/packs). The chart-library selects SEVERAL
// districts and downloads them together; this endpoint fetches each district's cells
// into its ENC_ROOT subfolder (<data>/<provider>/ENC_ROOT/<district>/) and then bakes
// each touched PROVIDER as ONE archive from its whole ENC_ROOT. Best-available across
// districts is a per-feature decision inside that single archive (the baker's
// finestCsclAt) — no cross-pack peer context, no repacks. This is the primary download
// path; the single-set POST /api/import stays for uploads + legacy.

// importPacksReq is the JSON body: the set of district packs to download + bake.
type importPacksReq struct {
	Packs []importPackSpec `json:"packs"`
}

// importPackSpec is one selected district: its pack key ("<provider>-<district>", e.g.
// "noaa-d5") plus how to fetch its cells — a bulk district zip (zipUrl, optionally
// narrowed to names) or a list of per-cell NOAA zips.
type importPackSpec struct {
	Set     string   `json:"set"`
	ZipURL  string   `json:"zipUrl"`
	Names   []string `json:"names"`
	Updates *bool    `json:"updates"` // nil → apply .001+ (default)
	Cells   []struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	} `json:"cells"`
}

// handleImportPacks validates a multi-district fetch spec and starts a single
// background job that downloads every district then re-bakes each touched provider.
func (s *Server) handleImportPacks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		apiErr(w, http.StatusMethodNotAllowed, "POST /api/import/packs")
		return
	}
	var req importPacksReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		apiErr(w, http.StatusBadRequest, "bad JSON: "+err.Error())
		return
	}
	if len(req.Packs) == 0 {
		apiErr(w, http.StatusBadRequest, "no packs")
		return
	}
	for _, p := range req.Packs {
		if !isSetName(p.Set) || districtOf(p.Set) == "" {
			apiErr(w, http.StatusBadRequest, "each pack needs a valid <provider>-<district> key")
			return
		}
		if p.ZipURL == "" && len(p.Cells) == 0 {
			apiErr(w, http.StatusBadRequest, "pack "+p.Set+" needs zipUrl or cells")
			return
		}
		if p.ZipURL != "" && !isChartURL(p.ZipURL) {
			apiErr(w, http.StatusBadRequest, "zipUrl must be a charts.noaa.gov or ienccloud.us URL")
			return
		}
		for _, c := range p.Cells {
			if c.URL != "" && !isChartURL(c.URL) {
				apiErr(w, http.StatusBadRequest, "cell url must be from a known chart provider")
				return
			}
		}
	}

	label := req.Packs[0].Set
	if len(req.Packs) > 1 {
		label = fmt.Sprintf("%d packs", len(req.Packs))
	}
	job := s.imports.create(label)
	go s.runImportPacks(job.ID, req)

	w.Header().Set("Content-Type", jsonCT)
	w.WriteHeader(http.StatusAccepted)
	fmt.Fprintf(w, `{"ok":true,"job":%q,"packs":%d}`, job.ID, len(req.Packs))
}

// runImportPacks downloads each selected district's cells into its ENC_ROOT subfolder,
// then bakes each touched provider ONCE from its whole ENC_ROOT. A district whose
// download fails is skipped, not fatal.
func (s *Server) runImportPacks(jobID string, req importPacksReq) {
	fail := func(err error) {
		log.Printf("import %s (packs): %v", jobID, err)
		s.imports.update(jobID, func(j *importJob) { j.State = "error"; j.Err = err.Error() })
	}

	// 1. Download each district's cells into <data>/<provider>/ENC_ROOT/<district>/.
	providers := map[string]bool{}
	for i, p := range req.Packs {
		provider, district := providerOf(p.Set), districtOf(p.Set)
		s.imports.update(jobID, func(j *importJob) { j.Pack, j.PackNum, j.PackTotal = p.Set, i+1, len(req.Packs) })
		cells, aux, cat, err := s.fetchPackCells(jobID, p)
		if err != nil {
			log.Printf("import %s: pack %s download: %v", jobID, p.Set, err) // skip, keep going
			continue
		}
		if len(cells) == 0 {
			log.Printf("import %s: pack %s: no cells", jobID, p.Set)
			continue
		}
		if p.Updates != nil && !*p.Updates {
			cells = baseOnly(cells)
		}
		s.cacheDistrict(provider, district, cells, aux, cat)
		providers[provider] = true
	}
	if len(providers) == 0 {
		fail(fmt.Errorf("no cells downloaded for any pack"))
		return
	}

	// 2. Bake each touched provider once (serialized: a bake rewrites bundle output in
	// place, which concurrent bakes must not interleave). Downloads above run unlocked.
	s.bakeMu.Lock()
	defer s.bakeMu.Unlock()
	s.imports.update(jobID, func(j *importJob) {
		j.Phase, j.Unit, j.Note, j.Done, j.Total = "bake", "cells", "Preparing charts", 0, 0
	})
	names := make([]string, 0, len(providers))
	for prov := range providers {
		names = append(names, prov)
	}
	sort.Strings(names)
	baked := 0
	for _, prov := range names {
		if s.bakeProvider(jobID, prov) {
			baked++
		}
	}
	if baked == 0 {
		return // bakeProvider recorded the error
	}
	s.imports.update(jobID, func(j *importJob) {
		j.Pack, j.Band, j.Phase, j.State, j.Note = "", "", "done", "done", ""
		j.PackNum, j.PackTotal = 0, 0
	})
	log.Printf("import %s: baked %d provider(s) from %d district(s)", jobID, baked, len(req.Packs))
}

// fetchPackCells downloads ONE district's cells (bulk zipUrl or per-cell) into memory,
// reporting download progress on the job. It does not persist or bake — the caller
// writes them to the district's ENC_ROOT subfolder (cacheDistrict) and bakes the
// provider. The per-cell path streams each cell straight into the district dir as it
// goes (via loadCellCached) so a re-run resumes from what's already on disk.
func (s *Server) fetchPackCells(jobID string, p importPackSpec) (map[string]baker.CellData, map[string][]byte, []tile57.CatalogEntry, error) {
	if p.ZipURL != "" {
		name := p.ZipURL[strings.LastIndexByte(p.ZipURL, '/')+1:]
		s.imports.update(jobID, func(j *importJob) {
			j.Pack, j.Phase, j.Unit, j.Note, j.Done, j.Total = p.Set, "download", "bytes", "Downloading "+name, 0, 0
		})
		data, err := fetchURLProgress(p.ZipURL, func(done, total int) {
			s.imports.update(jobID, func(j *importJob) { j.Done, j.Total = done, total })
		})
		if err != nil {
			return nil, nil, nil, fmt.Errorf("download %s: %w", p.ZipURL, err)
		}
		s.imports.update(jobID, func(j *importJob) {
			j.Phase, j.Unit, j.Note, j.Done, j.Total = "extract", "cells", "Extracting "+name, 0, 0
		})
		cells, aux, cat, err := extractZipCells(data)
		if err != nil {
			return nil, nil, nil, err
		}
		if len(p.Names) > 0 {
			cells = filterCells(cells, p.Names)
		}
		return cells, aux, cat, nil
	}

	// Per-cell: download each into the district's ENC_ROOT subfolder, then bake from there.
	cells := map[string]baker.CellData{}
	dir := s.districtDir(providerOf(p.Set), districtOf(p.Set))
	total := len(p.Cells)
	s.imports.update(jobID, func(j *importJob) { j.Pack, j.Phase, j.Unit, j.Total = p.Set, "download", "cells", total })
	for i, c := range p.Cells {
		if !isCellName(c.Name) {
			continue
		}
		if c.URL != "" && !allowedChartURL(c.URL) {
			continue
		}
		s.imports.update(jobID, func(j *importJob) { j.Note = "Downloading " + c.Name; j.Done = i })
		base, _, err := loadCellCached(chartHTTPClient, dir, c.Name, c.URL)
		if err != nil {
			log.Printf("import %s: download %s: %v", jobID, c.Name, err) // skip, keep going
		} else {
			cells[c.Name+".000"] = baker.CellData{Base: base}
		}
		s.imports.update(jobID, func(j *importJob) { j.Done = i + 1 })
	}
	return cells, nil, nil, nil
}
