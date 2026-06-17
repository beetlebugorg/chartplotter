// Package baker wires S-57 cell bytes through parse + S-52 portrayal into a
// PMTiles archive. It is the shared core behind the bake-zip / provision CLI
// paths and the server's background provision job.
package baker

import (
	"path"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/spf13/afero"

	"github.com/beetlebugorg/chartplotter/internal/engine/bake"
	"github.com/beetlebugorg/chartplotter/internal/engine/pmtiles"
	"github.com/beetlebugorg/chartplotter/pkg/s52"
	"github.com/beetlebugorg/chartplotter/pkg/s52/preslib"
	"github.com/beetlebugorg/chartplotter/pkg/s57"
)

// MVT bake parameters (default values).
const (
	MVTExtent uint32  = 4096
	MVTBuffer float64 = 64
)

// ParseCellBytes parses an S-57 base cell held entirely in memory (e.g. a zip
// entry or a downloaded NOAA cell) by staging it on an in-memory filesystem.
// Updates are not applied (the cell is parsed at its base edition).
func ParseCellBytes(name string, data []byte) (*s57.Chart, error) {
	fs := afero.NewMemMapFs()
	p := "/" + path.Base(name)
	if err := afero.WriteFile(fs, p, data, 0o644); err != nil {
		return nil, err
	}
	opts := s57.DefaultParseOptions()
	opts.Fs = fs
	opts.ApplyUpdates = false
	return s57.ParseWithOptions(p, opts)
}

// BuildBaker parses and adds each named cell to a fresh Baker. cells maps a
// cell name (or path) to its raw bytes. onSkip, if non-nil, is called for each
// cell that fails to parse. Returns the Baker and the names successfully added,
// in sorted order for deterministic output.
func BuildBaker(cells map[string][]byte, onSkip func(name string, err error)) (*bake.Baker, []string, error) {
	lib, err := s52.LoadLibraryFromBytes(preslib.DAI)
	if err != nil {
		return nil, nil, err
	}
	mariner := s52.DefaultMarinerSettings()

	names := make([]string, 0, len(cells))
	for n := range cells {
		names = append(names, n)
	}
	sort.Strings(names)

	b := bake.New()
	var ok []string
	for _, name := range names {
		chart, err := ParseCellBytes(name, cells[name])
		if err != nil {
			if onSkip != nil {
				onSkip(name, err)
			}
			continue
		}
		b.AddCell(chart, lib, mariner)
		ok = append(ok, name)
	}
	return b, ok, nil
}

// BakeToPMTiles bakes every tile from b into a PMTiles builder. Tiles are
// emitted in parallel across all CPUs (EmitTile only reads the Baker, so it is
// safe to run concurrently); the encoded bytes are added to the builder in
// deterministic coord order, so the resulting archive is independent of
// scheduling. progress, if non-nil, is called as (tilesEmitted, totalTiles).
func BakeToPMTiles(b *bake.Baker, progress func(done, total int)) *pmtiles.Builder {
	coords := b.TileCoords(MVTExtent)
	total := len(coords)
	encoded := make([][]byte, total)

	workers := runtime.NumCPU()
	if workers > total {
		workers = total
	}
	if workers < 1 {
		workers = 1
	}

	var next int64 = -1
	var done int64
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			var ts bake.TileScratch // reused across every tile this worker bakes
			defer wg.Done()
			for {
				i := int(atomic.AddInt64(&next, 1))
				if i >= total {
					return
				}
				encoded[i] = b.EmitTileInto(coords[i], MVTExtent, MVTBuffer, &ts)
				if progress != nil {
					progress(int(atomic.AddInt64(&done, 1)), total)
				}
			}
		}()
	}
	wg.Wait()

	pb := pmtiles.New()
	for i, c := range coords {
		if encoded[i] != nil {
			pb.AddTile(uint8(c.Z), c.X, c.Y, encoded[i])
		}
	}
	return pb
}

// IsBaseCell reports whether name is an S-57 base cell (…/<CELL>.000).
func IsBaseCell(name string) bool { return cellExtension(name) == ".000" }

// IsUpdateCell reports whether name is an S-57 update (…/<CELL>.NNN, NNN != 000).
func IsUpdateCell(name string) bool {
	ext := cellExtension(name)
	if len(ext) != 4 || ext[0] != '.' {
		return false
	}
	for _, c := range ext[1:] {
		if c < '0' || c > '9' {
			return false
		}
	}
	return ext != ".000"
}

func cellExtension(name string) string {
	i := strings.LastIndexByte(name, '.')
	if i < 0 {
		return ""
	}
	return name[i:]
}
