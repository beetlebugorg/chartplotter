package server

import (
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/beetlebugorg/chartplotter/internal/engine/baker"
	"github.com/beetlebugorg/chartplotter/internal/engine/tilesource"
)

// liveCellsDir is <setDir>/cells-pm — the KEPT per-cell PMTiles the runtime compositor mmaps.
func (s *Server) liveCellsDir(provider string) string {
	return filepath.Join(s.setDir(provider), "cells-pm")
}

// livePartitionPath is <setDir>/partition.tpart — the saved ownership-partition sidecar.
func (s *Server) livePartitionPath(provider string) string {
	return filepath.Join(s.setDir(provider), "partition.tpart")
}

// liveGenPath is <setDir>/live.gen — its mtime is the provider's tile GENERATION token (packGen
// reads it), bumped on each completed import so the client's tile URLs change and it re-fetches,
// invalidating tiles it cached against a previous, less-complete cell set. Its existence also marks
// "an import completed" — registerLiveProviders only re-serves a provider that has one, so a set
// left partial by an interrupted bake is completed by rebakeMissingProviders instead of served.
func (s *Server) liveGenPath(provider string) string {
	return filepath.Join(s.setDir(provider), "live.gen")
}

// bumpLiveGen advances the live provider's generation token (writes live.gen; the MTIME is the
// token). Called when an import registration completes.
func (s *Server) bumpLiveGen(provider string) {
	p := s.liveGenPath(provider)
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	_ = os.WriteFile(p, []byte(time.Now().UTC().Format(time.RFC3339Nano)), 0o644)
}

// liveCellArchives lists a provider's kept per-cell PMTiles paths (sorted).
func (s *Server) liveCellArchives(provider string) []string {
	dir := s.liveCellsDir(provider)
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var paths []string
	for _, e := range ents {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".pmtiles") {
			paths = append(paths, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(paths)
	return paths
}

// openLiveComposer opens a runtime compositor over a provider's kept per-cell archives, loading the
// partition sidecar when present (else building it and saving one for next time). Returns nil when
// the provider has no live-cells dir yet.
func (s *Server) openLiveComposer(provider string) (*tilesource.Composer, error) {
	paths := s.liveCellArchives(provider)
	if len(paths) == 0 {
		return nil, nil
	}
	sidecar := s.livePartitionPath(provider)
	load := ""
	if fi, err := os.Stat(sidecar); err == nil && fi.Size() > 0 {
		load = sidecar
	}
	c, err := tilesource.NewComposer(paths, load)
	if err != nil {
		return nil, err
	}
	if load == "" { // freshly built the partition — persist it so the next open is a fast load
		if err := c.SavePartition(sidecar); err != nil {
			log.Printf("live %s: save partition sidecar: %v", provider, err)
		}
	}
	return c, nil
}

// prepareLiveProvider bakes the provider's cells to its kept live-cells dir (incremental) and opens
// a runtime compositor over them — the live counterpart of composeProvider, with NO district
// compose pass (tiles compose on demand). Returns the contributing cell count + the Composer.
func (s *Server) prepareLiveProvider(jobID, encRoot, provider string) (int, tilesource.TileSource, error) {
	cellsDir := s.liveCellsDir(provider)
	s.invalidateLiveOnEngineChange(cellsDir)
	start := time.Now()
	paths, err := baker.PrepareLive(encRoot, cellsDir,
		func(done, total int, cell string) {
			s.imports.update(jobID, func(j *importJob) {
				j.Phase, j.Band, j.Zoom = "bake", "", 0
				if done >= total {
					j.Unit, j.Note, j.Done, j.Total, j.ETA = "", "Preparing live tiles", 0, 0, 0
					return
				}
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
		func(cell string, err error) { log.Printf("live %s: bake cell %s: %v (skipping)", provider, cell, err) })
	if err != nil {
		return 0, nil, err
	}
	if len(paths) == 0 {
		return 0, nil, nil
	}
	c, err := s.openLiveComposer(provider)
	if err != nil {
		return 0, nil, err
	}
	if c == nil {
		return 0, nil, nil
	}
	return len(paths), c, nil
}

// invalidateLiveOnEngineChange drops the kept per-cell archives when the tile57 engine commit
// changed since they were baked (a new engine portrays different tiles), so a binary upgrade
// re-bakes them. The partition sidecar is coverage-derived (engine-independent) and self-validates
// via its input key, so it is left in place. First bake just records the stamp.
func (s *Server) invalidateLiveOnEngineChange(cellsDir string) {
	if s.EngineCommit == "" {
		return
	}
	stamp := filepath.Join(cellsDir, ".enginever")
	if b, err := os.ReadFile(stamp); err == nil && string(b) == s.EngineCommit {
		return
	}
	if _, err := os.Stat(cellsDir); err == nil {
		_ = os.RemoveAll(cellsDir)
	}
	_ = os.MkdirAll(cellsDir, 0o755)
	_ = os.WriteFile(stamp, []byte(s.EngineCommit), 0o644)
}

// registerLiveProviders re-registers, at boot, a runtime compositor for every installed provider
// that has kept per-cell archives (a live provider from a previous run) and isn't disabled — so
// live layers survive a restart without re-baking. A batch pack registered for the same provider is
// replaced (live wins). Runs before rebakeMissingProviders, which then skips the ones registered here.
func (s *Server) registerLiveProviders() {
	for _, prov := range s.installedProviders() {
		if s.prefs.isDisabled(prov) || len(s.liveCellArchives(prov)) == 0 {
			continue
		}
		// Only re-serve a provider whose import COMPLETED (live.gen written at registration). A set
		// left partial by an interrupted bake has cells-pm but no live.gen → skip it here, and
		// rebakeMissingProviders finishes it (incremental) instead of serving a partial map.
		if _, err := os.Stat(s.liveGenPath(prov)); err != nil {
			continue
		}
		c, err := s.openLiveComposer(prov)
		if err != nil || c == nil {
			if err != nil {
				log.Printf("live %s: reopen at boot: %v", prov, err)
			}
			continue
		}
		s.sets.register(prov, c)
		m := c.Meta()
		log.Printf("live: registered %q from kept per-cell archives (z%d..%d)", prov, m.MinZoom, m.MaxZoom)
	}
}
