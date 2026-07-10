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


// registerProviderSet registers `src` as provider `set`'s live tile set and writes its tail (bake
// stamps, cell manifest, companion aux, metadata sidecar) so the provider is as complete in the
// chart library as any pack. `packPath` is the on-disk chart.pmtiles of a BATCH-baked provider
// (recorded for the pack list + stamped with the bake/engine version), or "" for a LIVE
// runtime-compositor provider (no disk archive — its bounds come from the TileSource Meta/TileJSON,
// and it is surfaced in /api/packs straight from the registry).
func (s *Server) registerProviderSet(jobID, set string, src tilesource.TileSource, packPath string, aux map[string][]byte, cat []tile57.CatalogEntry, created string) bool {
	outDir := s.setDir(set)
	s.sets.register(set, src)
	s.prefs.setDisabled(set, false)
	if packPath != "" {
		s.packAdd(set, packPath)
		if s.Version != "" {
			_ = os.WriteFile(packPath+bakeVerExt, []byte(s.Version), 0o644)
		}
		// Bake-time engine stamp (<pack>.enginever): the tile57 commit THIS binary links,
		// recorded beside the pack so the set's TileJSON can report which engine baked
		// these tiles even after the binary is upgraded. Best-effort, like .bakever.
		if s.EngineCommit != "" {
			_ = os.WriteFile(packPath+engineVerExt, []byte(s.EngineCommit), 0o644)
		}
	} else {
		// Live compositor: advance the generation token so the client re-fetches with a fresh ?g,
		// invalidating any tiles (incl. blank 204s) it cached against a previous, less-complete set.
		s.bumpLiveGen(set)
	}
	// Per-cell metadata straight from the provider's on-disk ENC tree — the whole
	// point of the path-based read is that a catalogue-sized import never holds
	// cell bytes in memory (the previous in-RAM staging peaked in the gigabytes).
	cellMeta := baker.ExtractCellMetaDir(s.encRootDir(set), func(name string, e error) {
		log.Printf("import %s: meta skip %s: %v", jobID, name, e)
	})
	stems := make([]string, 0, len(cellMeta))
	for n := range cellMeta {
		stems = append(stems, n)
	}
	if err := s.writeSetCells(set, stems); err != nil {
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

	// Live runtime compositor — the ONLY tile-production path. Bake each cell to its own
	// native-scale PMTiles (KEPT under <setDir>/tiles), build + save the ownership-partition
	// sidecar, and register a Composer that composes tiles on demand. Adding a district re-bakes
	// only its new cells. There is no district compose pass and no chart.pmtiles bundle; the
	// MapLibre style + sprite/glyph/colortable assets are served at runtime (tile57.Style + the
	// asset endpoints), not emitted per provider.
	n, liveSrc, err := s.prepareLiveProvider(jobID, encRoot, provider)
	if err != nil {
		return fail(err)
	}
	// Zero cells means nothing valid parsed → a failed import (don't register an empty set).
	// Clean only the (empty) baked tiles dir, NOT the whole set dir — in single-dir mode the set
	// dir also holds the source ENC_ROOT, which must survive a failed bake.
	if n == 0 {
		os.RemoveAll(s.liveCellsDir(provider))
		return fail(fmt.Errorf("import produced no coverage (no valid S-57 data)"))
	}

	s.imports.update(jobID, func(j *importJob) {
		j.Phase, j.Note, j.Zoom, j.Unit, j.Done, j.Total, j.ETA = "meta", "Reading chart metadata", 0, "", 0, 0, 0
	})
	aux := s.providerAux(provider)
	cat := s.providerCatalog(provider)
	if !s.registerProviderSet(jobID, provider, liveSrc, "", aux, cat, created) {
		return fail(fmt.Errorf("could not register set for %q", provider))
	}
	s.imports.update(jobID, func(j *importJob) { j.Cells = n })
	log.Printf("import %s: baked provider %q (%d cell(s)) → %s", jobID, provider, n, outDir)
	return true
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
