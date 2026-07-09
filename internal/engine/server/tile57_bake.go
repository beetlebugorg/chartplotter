package server

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/beetlebugorg/chartplotter/internal/engine/auxfiles"
	"github.com/beetlebugorg/chartplotter/internal/engine/baker"
	"github.com/beetlebugorg/chartplotter/internal/engine/tilesource"
	tile57 "github.com/beetlebugorg/tile57/bindings/go"
)

// bakeProgress returns a per-band progress callback for a provider bake. BakeBundle
// fires stage-0 (portraying a band's cells) then stage-1 (writing that band's tiles)
// events per navigational-purpose band. A calm per-band bar: it fills 0→100% per band
// and resets at each band — clamped monotonic WITHIN a band (the engine double-counts a
// band's tiles across its parallel-gen + serial-write phases, which would otherwise
// rewind). The multi-district DOWNLOAD phase sets pack "N of M" on the job separately.
func (s *Server) bakeProgress(jobID string) func(tile57.BakeProgress) {
	curBand, bandDoneMax := -1, 0
	return func(p tile57.BakeProgress) {
		s.imports.update(jobID, func(j *importJob) {
			j.Phase, j.Band = "bake", p.BandName
			if p.Stage == 0 { // portraying this band's cells
				j.Unit, j.Note, j.Done, j.Total = "cells", "Preparing charts", p.Done, p.Total
				return
			}
			if p.BandIndex != curBand { // new band → reset the within-band floor
				curBand, bandDoneMax = p.BandIndex, 0
			}
			if p.Done > bandDoneMax {
				bandDoneMax = p.Done
			}
			j.Unit, j.Note, j.Done, j.Total = "tiles", "Generating tiles", bandDoneMax, p.Total
		})
	}
}

// registerBakedSet registers a freshly-baked PROVIDER's chart.pmtiles as the live tile
// set and writes its tail — bake stamps, cell manifest, companion aux.zip, metadata
// sidecar — so a tile57 provider is as complete in the chart library as any pack.
// Returns false (recording a job error) if the bundle can't be opened. `set` is the
// provider name (one archive per provider).
func (s *Server) registerBakedSet(jobID, set string, cells map[string]baker.CellData, aux map[string][]byte, cat []tile57.CatalogEntry, created string) bool {
	outDir := s.setDir(set)
	chart := filepath.Join(outDir, "tiles", "chart.pmtiles")
	src, err := tilesource.Open(chart)
	if err != nil {
		log.Printf("import %s (%s): open baked bundle: %v", jobID, set, err)
		return false
	}
	s.sets.register(set, src)
	s.packAdd(set, chart)
	s.prefs.setDisabled(set, false)
	if s.Version != "" {
		_ = os.WriteFile(chart+bakeVerExt, []byte(s.Version), 0o644)
	}
	// Bake-time engine stamp (<pack>.enginever): the tile57 commit THIS binary links,
	// recorded beside the pack so the set's TileJSON can report which engine baked
	// these tiles even after the binary is upgraded. Best-effort, like .bakever.
	if s.EngineCommit != "" {
		_ = os.WriteFile(chart+engineVerExt, []byte(s.EngineCommit), 0o644)
	}
	if err := s.writeSetCells(set, cells); err != nil {
		log.Printf("import %s: cell manifest %q: %v", jobID, set, err)
	}

	// Companion aux/ dir (TXTDSC/PICREP) beside the set: loose static files + an
	// index.json, so feature attachments serve via /aux AND resolve offline as
	// plain files (no zip to unpack, no server needed) — one aux dir per provider.
	if len(aux) > 0 {
		if _, e := auxfiles.WriteDir(filepath.Join(outDir, "aux"), aux); e != nil {
			log.Printf("import %s: aux %q: %v", jobID, set, e)
		}
		_ = os.Remove(filepath.Join(outDir, set+".aux.zip")) // drop a stale legacy zip from a pre-loose bake
		s.auxIdx.invalidate()
	}

	// Per-pack metadata sidecar for the chart library (per-cell scale/edition/date/
	// agency/coverage + catalogue titles).
	cellMeta := baker.ExtractCellMeta(cells, func(name string, e error) {
		log.Printf("import %s: meta skip %s: %v", jobID, name, e)
	})
	meta := buildSetMeta(set, cellMeta, cat)
	meta.Imported = created
	if err := s.writeSetMeta(set, meta); err != nil {
		log.Printf("import %s: write meta %q: %v", jobID, set, err)
	}
	return true
}

// bakeProvider bakes a provider's WHOLE ENC_ROOT (all installed district subfolders)
// into its ONE self-contained tile57 chart bundle under the provider's cache dir
// (tiles/chart.pmtiles + per-scheme style-*.json + assets + manifest.json) and
// registers it. The baker's within-archive best-available (finestCsclAt: finest
// M_COVR-covering cell wins per point; coarser shows only in holes; per-cell oscl
// overscale hatch) does all cross-cell / cross-district composition — there is no
// cross-pack context, no peer folding. Any provider change (download/delete a
// district) triggers a full re-bake: the archive is a pure function of the ENC_ROOT.
// Returns true on success; on failure it records the job error and returns false. The
// caller sets the terminal "done" state (a multi-provider batch bakes several first).
func (s *Server) bakeProvider(jobID, provider string) bool {
	fail := func(err error) bool {
		log.Printf("import %s (%s): provider bake: %v", jobID, provider, err)
		s.imports.update(jobID, func(j *importJob) { j.State = "error"; j.Err = err.Error() })
		return false
	}
	// No districts left (e.g. the last was just deleted) → drop the provider set.
	if len(s.providerDistricts(provider)) == 0 {
		s.dropProviderSet(provider)
		return true
	}
	encRoot := s.encRootDir(provider)
	outDir := s.setDir(provider)
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fail(err)
	}
	s.imports.update(jobID, func(j *importJob) {
		j.Phase, j.Band, j.Unit, j.Note, j.Done, j.Total = "bake", "", "cells", "Preparing charts", 0, 0
	})
	created := time.Now().UTC().Format(time.RFC3339)

	// The per-cell COMPOSITE model (default): bake each cell to its own native-scale PMTiles,
	// then combine them via the engine's ownership partition into tiles/chart.pmtiles. This
	// replaces the in-bake cross-cell combiner (BakeBundle) — kept behind TILE57_LEGACY_BAKE=1
	// as an escape hatch while the composite model beds in. The served bundle only needs
	// tiles/chart.pmtiles: the MapLibre style is built dynamically (tile57.Style) and the
	// sprite/glyphs/colortables are global server assets, so the composite path skips the
	// bundle's (unused) assets/style/manifest emission.
	var n int
	var err error
	if os.Getenv("TILE57_LEGACY_BAKE") != "" {
		// MaxZoom 24 = the ABI's "no clamp" (each cell's full native band).
		var bbox [4]float64
		n, bbox, err = tile57.BakeBundle(encRoot, outDir, tile57.BakeOpts{Created: created, MaxZoom: 24}, s.bakeProgress(jobID))
		if err == nil && (n == 0 || bbox[2] <= bbox[0] || bbox[3] <= bbox[1]) {
			os.RemoveAll(outDir)
			return fail(fmt.Errorf("import produced no coverage (%d cell(s), no valid S-57 data)", n))
		}
	} else {
		n, err = s.composeProvider(jobID, encRoot, outDir)
	}
	if err != nil {
		return fail(err)
	}
	// Zero cells means nothing valid parsed → a failed import (don't register an empty pack).
	if n == 0 {
		os.RemoveAll(outDir)
		return fail(fmt.Errorf("import produced no coverage (no valid S-57 data)"))
	}

	s.imports.update(jobID, func(j *importJob) {
		j.Phase, j.Note, j.Zoom, j.Unit, j.Done, j.Total, j.ETA = "meta", "Reading chart metadata", 0, "", 0, 0, 0
	})
	cells := s.providerCellData(provider)
	aux := s.providerAux(provider)
	cat := s.providerCatalog(provider)
	if !s.registerBakedSet(jobID, provider, cells, aux, cat, created) {
		return fail(fmt.Errorf("could not register baked bundle for %q", provider))
	}
	s.imports.update(jobID, func(j *importJob) { j.Cells = n })
	log.Printf("import %s: baked provider %q (%d cell(s)) → %s", jobID, provider, n, outDir)
	return true
}

// composeProvider bakes each cell under encRoot to its own native-scale PMTiles (coverage
// embedded in the metadata) and streams them through the engine's ownership partition into
// <outDir>/tiles/chart.pmtiles. Per-cell archives go to a temp dir (mmap'd by the compositor,
// then discarded), so the whole cell set is never resident. Returns the count of cells that
// contributed to the composite, or 0 if none produced coverage.
func (s *Server) composeProvider(jobID, encRoot, outDir string) (int, error) {
	tilesPath := filepath.Join(outDir, "tiles", "chart.pmtiles")
	start := time.Now()
	var composeStart time.Time // set when the per-cell bakes finish and the compose begins
	return baker.ComposeENCRoot(encRoot, tilesPath,
		func(done, total int, cell string) {
			s.imports.update(jobID, func(j *importJob) {
				j.Phase, j.Band, j.Zoom = "bake", "", 0
				if done >= total {
					// Per-cell bakes done → the ownership-partition compose runs. Until its first
					// zoom-progress arrives, sweep (total 0) under "Composing tiles".
					composeStart = time.Now()
					j.Unit, j.Note, j.Done, j.Total, j.ETA = "", "Composing tiles", 0, 0, 0
					return
				}
				// Per-cell portrayal: name the chart being baked, plus a determinate bar and an ETA
				// from the mean per-cell rate so far.
				note := "Baking charts"
				if cell != "" {
					note = "Baking " + cell
				}
				eta := 0
				if done > 0 {
					per := time.Since(start) / time.Duration(done)
					eta = int((per * time.Duration(total-done)).Round(time.Second).Seconds())
				}
				j.Unit, j.Note, j.Done, j.Total, j.ETA = "cells", note, done, total, eta
			})
		},
		func(p tile57.ComposeProgress) {
			// Live compose progress: the engine weights zooms by tile count, so p.Done/p.Total is
			// a smooth fraction. ETA extrapolates from elapsed compose time and that fraction.
			if composeStart.IsZero() {
				composeStart = time.Now()
			}
			eta := 0
			if p.Total > 0 && p.Done > 0 && p.Done < p.Total {
				frac := float64(p.Done) / float64(p.Total)
				remain := time.Duration(float64(time.Since(composeStart)) * (1 - frac) / frac)
				eta = int(remain.Round(time.Second).Seconds())
			}
			s.imports.update(jobID, func(j *importJob) {
				j.Phase, j.Band, j.Unit, j.Note = "bake", "", "", "Composing tiles"
				j.Done, j.Total, j.ETA = int(p.Done), int(p.Total), eta
				j.Zoom, j.ZoomMin, j.ZoomMax = p.Zoom, p.MinZoom, p.MaxZoom
			})
		},
		func(cell string, err error) {
			log.Printf("import %s: bake cell %s: %v (skipping)", jobID, cell, err)
		})
}

// dropProviderSet unregisters a provider whose ENC_ROOT is now empty and removes its
// (regenerable) baked bundle + its (now-empty) provider data tree. The cell index is
// rebuilt so its cells stop counting as installed.
func (s *Server) dropProviderSet(provider string) {
	s.sets.remove(provider)
	s.packDel(provider)
	s.prefs.setDisabled(provider, false)
	_ = os.RemoveAll(s.setDir(provider))          // baked bundle (cache)
	_ = os.RemoveAll(s.providerDataDir(provider)) // ENC_ROOT source tree (now empty)
	s.auxIdx.invalidate()
	s.cellIdx.rebuild()
}
