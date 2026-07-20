package server

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/beetlebugorg/chartplotter/internal/engine/baker"
	"github.com/beetlebugorg/chartplotter/internal/engine/tilesource"
)

// liveCellsDir is <setDir>/tiles — the KEPT per-cell PMTiles the runtime compositor mmaps.
// This is the same layout `tile57 bake -o <dir>` writes (<dir>/tiles/<STEM>.pmtiles), so a
// CLI-baked structure drops straight into a provider's set dir. The engine keeps its own
// ownership-partition sidecar beside these archives; the host neither writes nor reads it.
func (s *Server) liveCellsDir(provider string) string {
	return filepath.Join(s.setDir(provider), "tiles")
}

// liveGenPath is <setDir>/live.gen — it holds the provider's CONTENT cache-bust token (a
// decimal of the sha-of-shas over its per-cell archives; see liveGenToken), which packGen reads
// and stamps into tile URLs as ?g. It changes exactly when the cell set or any cell's content
// changes, so a no-op re-bake keeps the client's cached tiles. Its existence also marks "an
// import registered" — registerLiveProviders only re-serves a provider that has one, so a set
// left partial by an interrupted bake is completed by rebakeMissingProviders instead of served.
func (s *Server) liveGenPath(provider string) string {
	return filepath.Join(s.setDir(provider), "live.gen")
}

// liveGenToken is the provider's CONTENT cache-bust token: a sha-of-shas over its per-cell
// archives (each archive's content sha, from the .sha sidecar written at bake) plus the engine
// commit serving them. The lines are sorted so the token is order-independent, and the low 63
// bits become the positive ?g int. It changes exactly when the set of cells, any cell's
// content, or the engine build changes — the engine composes live tiles at SERVE time, so its
// identity is part of a tile's content address: a serve-path fix must bust client caches even
// when the baked archives are byte-identical.
func (s *Server) liveGenToken(provider string) int64 {
	paths := s.liveCellArchives(provider)
	if len(paths) == 0 {
		return 0
	}
	lines := make([]string, 0, len(paths)+1)
	if s.EngineCommit != "" {
		lines = append(lines, "engine:"+s.EngineCommit)
	}
	for _, p := range paths {
		stem := strings.TrimSuffix(filepath.Base(p), ".pmtiles")
		sha, err := os.ReadFile(p + ".sha")
		if err != nil { // no sidecar (shouldn't happen post-bake) — hash the archive itself
			if b, e := os.ReadFile(p); e == nil {
				sum := sha256.Sum256(b)
				sha = []byte(hex.EncodeToString(sum[:]))
			}
		}
		lines = append(lines, stem+":"+strings.TrimSpace(string(sha)))
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return int64(binary.BigEndian.Uint64(sum[:8]) & 0x7fffffffffffffff) // 63 bits → non-negative
}

// bumpLiveGen recomputes and persists the live provider's content token (writes live.gen).
// Called when an import registration completes.
func (s *Server) bumpLiveGen(provider string) {
	p := s.liveGenPath(provider)
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	_ = os.WriteFile(p, []byte(strconv.FormatInt(s.liveGenToken(provider), 10)), 0o644)
}

// liveCellArchives lists a provider's kept per-cell PMTiles paths (sorted). The bake mirrors the
// ENC tree, so the archives live in subdirs (<tiles>/d1/US4CT1AA.pmtiles, …) — walk, don't ReadDir.
func (s *Server) liveCellArchives(provider string) []string {
	return archivesUnder(s.liveCellsDir(provider))
}

// archivesUnder lists the per-cell PMTiles under any tree (sorted) — the kept live
// dir or an engine re-bake's staging dir.
func archivesUnder(root string) []string {
	var paths []string
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() && strings.HasSuffix(p, ".pmtiles") {
			paths = append(paths, p)
		}
		return nil
	})
	sort.Strings(paths)
	return paths
}

// liveBakeWorkers is how many cells bake in parallel — a MEMORY bound (each concurrent bake holds a
// whole cell's parse+portray+raster working set), so the CPU count capped modestly, overridable via
// CHARTPLOTTER_BAKE_WORKERS.
func liveBakeWorkers() int {
	if v := os.Getenv("CHARTPLOTTER_BAKE_WORKERS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	if n := runtime.NumCPU(); n < 8 {
		return n
	}
	return 8
}

// openLiveComposer opens a runtime compositor over a provider's kept per-cell archives, loading the
// partition sidecar when present (else building it and saving one for next time). Returns nil when
// the provider has no live-cells dir yet.
func (s *Server) openLiveComposer(provider string) (*tilesource.Composer, error) {
	paths := s.liveCellArchives(provider)
	if len(paths) == 0 {
		return nil, nil
	}
	// The engine owns the ownership partition now: NewComposer finds it beside the archives,
	// reuses it when it still matches the cell set, and rebuilds + refreshes it on disk when it
	// does not. Nothing to load or save here.
	return tilesource.NewComposer(paths)
}

// progressiveReKey re-opens the live compositor over the cells baked SO FAR, registers it as the
// provider's (enabled) set, and advances the content token — so a long import fills in on the map
// batch by batch instead of appearing only when the whole provider finishes. Called synchronously
// from the bake loop (which is paused, so it never races the writer). Best-effort: a transient
// open failure just skips this batch; the next one (or the final register) catches up.
func (s *Server) progressiveReKey(provider string) {
	c, err := s.openLiveComposer(provider)
	if err != nil || c == nil {
		if err != nil {
			log.Printf("live %s: progressive re-key: %v", provider, err)
		}
		return
	}
	s.sets.register(provider, c)
	s.prefs.setDisabled(provider, false)
	s.bumpLiveGen(provider)
}

// stagingSuffix names the engine re-bake staging tree beside the kept live dir
// (<setDir>/tiles.next): a full re-bake lands there while the previous archives
// keep serving, and the swap into place is the LAST step.
const stagingSuffix = ".next"

// prepareLiveProvider bakes the provider's cells to its kept live-cells dir and opens a runtime
// compositor over them — the live counterpart of composeProvider, with NO district compose pass
// (tiles compose on demand). Same-engine bakes are incremental IN PLACE (adding a district bakes
// only its new cells). An ENGINE change re-bakes everything into a staging tree (tiles.next)
// while the previous archives keep serving, and replaces the served tree only as the LAST step —
// a failed or interrupted re-bake leaves the old tiles in place (and the staging tree resumes
// incrementally on the next run under the same build). Returns the contributing cell count +
// the Composer.
func (s *Server) prepareLiveProvider(jobID, encRoot, provider string) (int, tilesource.TileSource, error) {
	cellsDir := s.liveCellsDir(provider)
	bakeDir := cellsDir
	staged := !s.liveEngineCurrent(cellsDir)
	if staged {
		bakeDir = cellsDir + stagingSuffix
		// A staging tree left by a DIFFERENT engine build is itself stale — start over.
		if !s.liveEngineCurrent(bakeDir) {
			_ = os.RemoveAll(bakeDir)
		}
	}
	if err := os.MkdirAll(bakeDir, 0o755); err != nil {
		return 0, nil, err
	}
	// Stamp the tree with the baking engine up front: an interrupted run resumes
	// incrementally under the same build and restarts under a different one.
	_ = os.WriteFile(filepath.Join(bakeDir, ".enginever"), []byte(s.EngineCommit), 0o644)
	start := time.Now()
	if _, err := baker.PrepareLive(encRoot, bakeDir, liveBakeWorkers(), func(done, total int) {
		s.imports.update(jobID, func(j *importJob) {
			j.Phase, j.Band, j.Zoom = "bake", "", 0
			eta := 0
			if done > 0 && total > done {
				per := time.Since(start) / time.Duration(done)
				eta = int((per * time.Duration(total-done)).Round(time.Second).Seconds())
			}
			j.Unit, j.Note, j.Done, j.Total, j.ETA = "cells", "Baking charts", done, total, eta
		})
	}); err != nil {
		return 0, nil, err // old tiles untouched; a staging tree stays for resume
	}
	if staged {
		// Everything baked — swap the staged tree into place. This is the ONLY point
		// the previous archives are dropped.
		if len(archivesUnder(bakeDir)) == 0 {
			_ = os.RemoveAll(bakeDir) // staged bake produced nothing — keep serving the old tiles
			return 0, nil, nil
		}
		if err := os.RemoveAll(cellsDir); err != nil {
			return 0, nil, err
		}
		if err := os.Rename(bakeDir, cellsDir); err != nil {
			return 0, nil, err
		}
	}
	// The mirrored tree holds every kept per-cell archive (baked + reused).
	paths := s.liveCellArchives(provider)
	if len(paths) == 0 {
		_ = os.RemoveAll(cellsDir) // nothing valid parsed — clean the empty tree
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

// liveEngineCurrent reports whether the per-cell archives in a tree were baked by the
// RUNNING engine (the .enginever stamp matches the linked tile57 commit). An unstamped
// tree counts as stale — prepareLiveProvider re-bakes it to staging and swaps. With no
// engine commit linked in, everything counts as current (nothing to compare against).
func (s *Server) liveEngineCurrent(cellsDir string) bool {
	if s.EngineCommit == "" {
		return true
	}
	b, err := os.ReadFile(filepath.Join(cellsDir, ".enginever"))
	return err == nil && string(b) == s.EngineCommit
}

// registerLiveProviders re-registers, at boot, a runtime compositor for every installed provider
// that has kept per-cell archives (a live provider from a previous run) and isn't disabled — so
// live layers survive a restart without re-baking. A batch pack registered for the same provider is
// replaced (live wins). Runs before rebakeMissingProviders, which then skips the ones registered here.
func (s *Server) registerLiveProviders() {
	for _, prov := range s.installedProviders() {
		// Finish an interrupted engine re-bake swap: a crash between dropping the old
		// tree and renaming the staged one leaves tiles/ missing with tiles.next
		// present (the staged tree was complete — the swap only runs after a full
		// bake). Move it into place; dropping live.gen routes the set through the
		// self-heal bake (a fast incremental no-op) before it re-registers.
		cellsDir := s.liveCellsDir(prov)
		if _, err := os.Stat(cellsDir); os.IsNotExist(err) {
			if _, e := os.Stat(cellsDir + stagingSuffix); e == nil {
				log.Printf("live %s: recovering an interrupted engine re-bake swap", prov)
				_ = os.Rename(cellsDir+stagingSuffix, cellsDir)
				_ = os.Remove(s.liveGenPath(prov))
			}
		}
		if s.prefs.isDisabled(prov) || len(s.liveCellArchives(prov)) == 0 {
			continue
		}
		// Only re-serve a provider whose import COMPLETED (live.gen written at registration). A set
		// left partial by an interrupted bake has tiles/ but no live.gen → skip it here, and
		// rebakeMissingProviders finishes it (incremental) instead of serving a partial map.
		if _, err := os.Stat(s.liveGenPath(prov)); err != nil {
			continue
		}
		// Archives baked by an older engine are stale (a new engine portrays
		// different tiles) — but stale tiles beat NO tiles: keep serving them while
		// rebakeMissingProviders re-bakes to staging and swaps when done.
		if !s.liveEngineCurrent(s.liveCellsDir(prov)) {
			log.Printf("live %s: kept archives are from another engine build — serving them while the re-bake runs", prov)
		}
		c, err := s.openLiveComposer(prov)
		if err != nil || c == nil {
			if err != nil {
				log.Printf("live %s: reopen at boot: %v", prov, err)
			}
			continue
		}
		s.sets.register(prov, c)
		// Re-persist the token: the archives are unchanged, but the serving
		// engine may not be the one that wrote live.gen, and the token embeds it.
		s.bumpLiveGen(prov)
		m := c.Meta()
		log.Printf("live: registered %q from kept per-cell archives (z%d..%d)", prov, m.MinZoom, m.MaxZoom)
	}
}
