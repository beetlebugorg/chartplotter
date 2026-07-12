package server

import (
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/beetlebugorg/chartplotter/internal/engine/tilesource"
)

// registerLiveCompose registers a live runtime-composited tile set when TILE57_LIVE_COMPOSE names
// a directory of per-cell PMTiles (each from `tile57 compose --keep-cells`). It serves tiles by
// on-demand composition (the tile57 ComposeSource) instead of a prebaked archive. Optional env:
//
//	TILE57_LIVE_PARTITION  a partition sidecar (`tile57 compose --save-partition`) to load
//	                       instead of building the ownership partition at open;
//	TILE57_LIVE_NAME       the set name (default "live"), served at /tiles/<name>/{z}/{x}/{y}.
//
// A dev/test hook for the on-demand compositor — a no-op when TILE57_LIVE_COMPOSE is unset.
func (s *Server) registerLiveCompose() {
	dir := os.Getenv("TILE57_LIVE_COMPOSE")
	if dir == "" {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		log.Printf("live-compose: cannot read %s: %v", dir, err)
		return
	}
	var paths []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if n := e.Name(); strings.HasSuffix(n, ".cell.tmp") || strings.HasSuffix(n, ".pmtiles") {
			paths = append(paths, filepath.Join(dir, n))
		}
	}
	sort.Strings(paths)
	if len(paths) == 0 {
		log.Printf("live-compose: no per-cell archives (*.cell.tmp / *.pmtiles) in %s", dir)
		return
	}
	name := os.Getenv("TILE57_LIVE_NAME")
	if name == "" {
		name = "live"
	}
	src, err := tilesource.NewComposer(paths, os.Getenv("TILE57_LIVE_PARTITION"))
	if err != nil {
		log.Printf("live-compose: open %s failed: %v", dir, err)
		return
	}
	s.sets.register(name, src)
	m := src.Meta()
	log.Printf("live-compose: registered %q — %d archive(s), z%d..%d, bounds [%.3f, %.3f, %.3f, %.3f] (GET /tiles/%s/{z}/{x}/{y})",
		name, len(paths), m.MinZoom, m.MaxZoom, m.W, m.S, m.E, m.N, name)
}
