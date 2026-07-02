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

// bakeBundleTile57 bakes an import's cells into a self-contained tile57 chart
// bundle under the set's directory (tiles/chart.pmtiles + per-scheme SCAMIN-
// bucketed style-*.json + assets + manifest.json) and registers chart.pmtiles as
// the set. It mirrors the Go path's post-bake tail — aux.zip + per-pack metadata
// sidecar + cell manifest — so a tile57-baked pack is as complete in the chart
// library as a Go-baked one. Returns true once it has handled the bake (success OR
// a recorded error); false only if there's nothing to do.
func (s *Server) bakeBundleTile57(jobID, set string, cells map[string]baker.CellData, aux map[string][]byte, cat []tile57.CatalogEntry, applyUpdates bool) bool {
	if len(cells) == 0 {
		return false
	}
	fail := func(err error) bool {
		log.Printf("import %s (%s): tile57 bundle: %v", jobID, set, err)
		s.imports.update(jobID, func(j *importJob) { j.State = "error"; j.Err = err.Error() })
		return true
	}

	// Stage THIS import's cells to a temp ENC dir (the engine reads from disk; the
	// shared <data>/ENC_ROOT holds every import, so we can't point it there).
	encDir, err := os.MkdirTemp("", "cp-tile57-import-")
	if err != nil {
		return fail(err)
	}
	defer os.RemoveAll(encDir)
	for name, cd := range cells { // name == "<stem>.000"
		if err := os.WriteFile(filepath.Join(encDir, name), cd.Base, 0o644); err != nil {
			return fail(err)
		}
		if applyUpdates {
			for un, ub := range cd.Updates {
				if err := os.WriteFile(filepath.Join(encDir, filepath.Base(un)), ub, 0o644); err != nil {
					return fail(err)
				}
			}
		}
	}

	outDir := s.setDir(set)
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fail(err)
	}
	s.imports.update(jobID, func(j *importJob) {
		j.Phase, j.Band, j.Unit, j.Note, j.Done, j.Total = "bake", "", "cells", "Preparing charts", 0, 0
	})
	// Per-band progress with the band name (like the Go baker). A calm per-band bar:
	// it fills 0→100% per band and resets at each band — clamped monotonic WITHIN a
	// band (the engine double-counts a band's tiles across its parallel-gen + serial-
	// write phases, which would otherwise rewind). A smooth GLOBAL bar needs the
	// engine to drive bands in bake order (BandIndex is navigational rank, not bake
	// order, so a global mapping leaps around) — deferred; see ../tile57 spec host §3.
	curBand, bandDoneMax := -1, 0
	note := func(verb, noun, band string) string {
		if band == "" {
			return verb + " " + noun
		}
		return verb + " " + band + " " + noun
	}
	progress := func(p tile57.BakeProgress) {
		s.imports.update(jobID, func(j *importJob) {
			j.Phase, j.Band = "bake", p.BandName
			if p.Stage == 0 { // portraying this band's cells
				j.Unit, j.Note, j.Done, j.Total = "cells", note("Preparing", "charts", p.BandName), p.Done, p.Total
				return
			}
			if p.BandIndex != curBand { // new band → reset the within-band floor
				curBand, bandDoneMax = p.BandIndex, 0
			}
			if p.Done > bandDoneMax {
				bandDoneMax = p.Done
			}
			j.Unit, j.Note, j.Done, j.Total = "tiles", note("Generating", "tiles", p.BandName), bandDoneMax, p.Total
		})
	}
	created := time.Now().UTC().Format(time.RFC3339)
	// MaxZoom 24 = the ABI's "no clamp" (each cell's full native band); MaxZoom 0 would
	// clamp every band down to z0 — an EMPTY archive.
	n, bbox, err := tile57.BakeBundle(encDir, outDir, tile57.BakeOpts{Created: created, MaxZoom: 24}, progress)
	if err != nil {
		return fail(err)
	}
	// An inverted/empty bbox (or zero cells) means nothing valid parsed — e.g. a
	// corrupt cell libtile57 tolerates but that covers nothing. Treat it as a failed
	// import (don't register an empty pack) and drop the stub bundle it wrote.
	if n == 0 || bbox[2] <= bbox[0] || bbox[3] <= bbox[1] {
		os.RemoveAll(outDir)
		return fail(fmt.Errorf("import produced no coverage (%d cell(s), no valid S-57 data)", n))
	}

	// Register the bundle's chart.pmtiles as the set (replacing any prior merged or
	// per-band Go bake of the same district).
	chart := filepath.Join(outDir, "tiles", "chart.pmtiles")
	src, err := tilesource.Open(chart)
	if err != nil {
		return fail(err)
	}
	s.removeMergedSet(set)
	for _, band := range s.setsForDistrict(set) { // drop stale per-band Go sets
		s.sets.remove(band)
		s.packDel(band)
	}
	s.sets.register(set, src)
	s.packAdd(set, chart)
	s.prefs.setDisabled(set, false)
	if s.Version != "" {
		_ = os.WriteFile(chart+bakeVerExt, []byte(s.Version), 0o644)
	}
	if err := s.writeSetCells(set, cells); err != nil {
		log.Printf("import %s: cell manifest %q: %v", jobID, set, err)
	}

	// Companion aux.zip (TXTDSC/PICREP) beside the set, so feature attachments still
	// serve via /api/aux — same as the Go path's writeAndRegister.
	if len(aux) > 0 {
		if f, e := os.Create(filepath.Join(outDir, set+".aux.zip")); e == nil {
			if _, e := auxfiles.WriteZip(f, aux); e != nil {
				log.Printf("import %s: aux %q: %v", jobID, set, e)
			}
			f.Close()
		}
		s.auxIdx.invalidate()
	}

	// Per-pack metadata sidecar for the chart library (per-cell scale/edition/date/
	// agency/coverage + catalogue titles) — same as the Go path, so pack details
	// aren't poorer for a tile57 import.
	s.imports.update(jobID, func(j *importJob) { j.Phase, j.Note = "meta", "Reading chart metadata" })
	cellMeta := baker.ExtractCellMeta(cells, func(name string, e error) {
		log.Printf("import %s: meta skip %s: %v", jobID, name, e)
	})
	meta := buildSetMeta(set, cellMeta, cat)
	meta.Imported = created
	if err := s.writeSetMeta(set, meta); err != nil {
		log.Printf("import %s: write meta %q: %v", jobID, set, err)
	}

	s.imports.update(jobID, func(j *importJob) { j.Cells = n; j.State = "done" })
	log.Printf("import %s: baked tile57 bundle %q (%d cell(s)) → %s", jobID, set, n, outDir)
	return true
}
