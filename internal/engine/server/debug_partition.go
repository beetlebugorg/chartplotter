package server

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/beetlebugorg/chartplotter/internal/engine/tilesource"
	tile57 "github.com/beetlebugorg/tile57/bindings/go"
)

// partitionSuffix names a provider's ownership-partition debug tile set: it is served
// at /tiles/{provider}-partition/{z}/{x}/{y}.mvt, separate from the real chart set.
const partitionSuffix = "-partition"

// Zoom range for the debug partition. z0..12 covers overview→approach across a whole
// provider quickly; harbor-level detail (z13+) multiplies the tile count ~4×/zoom, so
// the overlay overzooms past 12 rather than baking it.
const (
	partitionMinZoom = 0
	partitionMaxZoom = 12
)

// handleDebugPartition drives the ownership-partition DEBUG overlay.
//
//	POST /api/debug/partition            → (re)generate the partition for every provider,
//	                                        in the background; returns the provider list.
//	GET  /api/debug/partition            → per provider: whether its partition tile set is
//	                                        ready + the tile URL template to overlay.
func (s *Server) handleDebugPartition(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		providers := s.installedProviders()
		// Bake off the request goroutine — a whole provider is seconds, several add up.
		go func() {
			for _, p := range providers {
				if err := s.bakeProviderPartition(p); err != nil {
					log.Printf("partition-debug %s: %v", p, err)
				}
			}
		}()
		writeJSON(w, map[string]any{"ok": true, "providers": providers})
	case http.MethodGet:
		type item struct {
			Provider string `json:"provider"`
			Ready    bool   `json:"ready"`
			Tiles    string `json:"tiles"`
		}
		items := []item{}
		for _, p := range s.installedProviders() {
			_, ready := s.sets.get(p + partitionSuffix)
			items = append(items, item{
				Provider: p,
				Ready:    ready,
				Tiles:    "tiles/" + p + partitionSuffix + "/{z}/{x}/{y}.mvt",
			})
		}
		writeJSON(w, map[string]any{"partitions": items})
	default:
		apiErr(w, http.StatusMethodNotAllowed, "POST or GET")
	}
}

// bakeProviderPartition bakes ONE provider's ENC_ROOT into an ownership-partition debug
// PMTiles (which cell renders which ground per band; NO portrayed content) and registers
// it as the "{provider}-partition" tile set. Reuses the same ENC_ROOT + output dir as the
// real bake; the partition math is entirely in the engine (tile57.BakePartitionDebug).
func (s *Server) bakeProviderPartition(provider string) error {
	if len(s.providerDistricts(provider)) == 0 {
		return fmt.Errorf("provider %q has no districts", provider)
	}
	encRoot := s.encRootDir(provider)
	outPath := filepath.Join(s.setDir(provider), "tiles", provider+partitionSuffix+".pmtiles")
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return err
	}
	n, err := tile57.BakePartitionDebug(encRoot, outPath, partitionMinZoom, partitionMaxZoom, tile57.BandGoverning)
	if err != nil {
		return fmt.Errorf("bake partition: %w", err)
	}
	src, err := tilesource.Open(outPath)
	if err != nil {
		return fmt.Errorf("open partition pmtiles: %w", err)
	}
	s.sets.register(provider+partitionSuffix, src)
	log.Printf("partition-debug: %q (%d cell(s)) → %s", provider, n, outPath)
	return nil
}
