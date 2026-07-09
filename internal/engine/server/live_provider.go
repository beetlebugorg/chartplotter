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
// This is the same layout `tile57 bake -o <dir>` writes (<dir>/tiles/<STEM>.pmtiles next to
// <dir>/partition.tpart), so a CLI-baked structure drops straight into a provider's set dir.
func (s *Server) liveCellsDir(provider string) string {
	return filepath.Join(s.setDir(provider), "tiles")
}

// livePartitionPath is <setDir>/partition.tpart — the saved ownership-partition sidecar.
func (s *Server) livePartitionPath(provider string) string {
	return filepath.Join(s.setDir(provider), "partition.tpart")
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
// archives (each archive's content sha, from the .sha sidecar written at bake). The lines are
// sorted so the token is order-independent, and the low 63 bits become the positive ?g int. It
// changes exactly when the set of cells or any cell's content changes.
func (s *Server) liveGenToken(provider string) int64 {
	paths := s.liveCellArchives(provider)
	if len(paths) == 0 {
		return 0
	}
	lines := make([]string, 0, len(paths))
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
	root := s.liveCellsDir(provider)
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
	sidecar := s.livePartitionPath(provider)
	load := ""
	if fi, err := os.Stat(sidecar); err == nil && fi.Size() > 0 {
		load = sidecar
	}
	c, err := tilesource.NewComposer(paths, load)
	if err != nil {
		return nil, err
	}
	// Persist the (possibly rebuilt) partition so the on-disk sidecar always matches the current
	// cell set: a progressive re-key or an added/removed district changes the inputs, the
	// compositor rebuilds from a stale sidecar, and saving keeps the sidecar current for a fast
	// boot. (When the sidecar was loaded intact this re-writes identical bytes.)
	if err := c.SavePartition(sidecar); err != nil {
		log.Printf("live %s: save partition sidecar: %v", provider, err)
	}
	return c, nil
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

// prepareLiveProvider bakes the provider's cells to its kept live-cells dir (incremental) and opens
// a runtime compositor over them — the live counterpart of composeProvider, with NO district
// compose pass (tiles compose on demand). Returns the contributing cell count + the Composer.
func (s *Server) prepareLiveProvider(jobID, encRoot, provider string) (int, tilesource.TileSource, error) {
	cellsDir := s.liveCellsDir(provider)
	s.invalidateLiveOnEngineChange(cellsDir)
	start := time.Now()
	if _, err := baker.PrepareLive(encRoot, cellsDir, liveBakeWorkers(), func(done, total int) {
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
		return 0, nil, err
	}
	// The mirrored tree holds every kept per-cell archive (baked + reused).
	paths := s.liveCellArchives(provider)
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
		// left partial by an interrupted bake has tiles/ but no live.gen → skip it here, and
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
