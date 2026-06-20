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

	"github.com/beetlebugorg/chartplotter/internal/engine/bake"
	"github.com/beetlebugorg/chartplotter/internal/engine/pmtiles"
	"github.com/beetlebugorg/chartplotter/internal/engine/tile"
	"github.com/beetlebugorg/chartplotter/pkg/iso8211"
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
	p := "/" + path.Base(name)
	opts := s57.DefaultParseOptions()
	opts.Fs = iso8211.MemFS{p: data}
	opts.ApplyUpdates = false
	return s57.ParseWithOptions(p, opts)
}

// Session is an incremental Baker builder: cells are parsed and added one at a
// time (AddCell) into a long-lived Baker, instead of all-at-once via BuildBaker.
// This is the real-time wasm path — parsing a single large cell can take seconds
// in wasm, so loading the set one cell per call lets the host yield between cells
// (servicing tile requests / reporting progress) rather than blocking on the
// whole set. The S-52 library + mariner settings are loaded once and reused.
type Session struct {
	Baker   *bake.Baker
	lib     *s52.Library
	mariner *s52.MarinerSettings
}

// NewSession returns an empty incremental Session (loads the S-52 presentation
// library once). Add cells with AddCellBytes, then bake tiles off Session.Baker.
func NewSession() (*Session, error) {
	lib, err := s52.LoadLibraryFromBytes(preslib.DAI)
	if err != nil {
		return nil, err
	}
	mariner := s52.DefaultMarinerSettings()
	b := bake.New()
	b.OverzoomAllBands = true // realtime/upload path: keep a few uploaded cells visible (skeleton) when zoomed out
	return &Session{Baker: b, lib: lib, mariner: mariner}, nil
}

// AddCellBytes parses one raw cell and adds it to the session's Baker. The caller
// should rebuild the emit index (Baker.BuildEmitIndex) before baking tiles after
// any add.
func (s *Session) AddCellBytes(name string, data []byte) (s57.Bounds, error) {
	chart, err := ParseCellBytes(name, data)
	if err != nil {
		return s57.Bounds{}, err
	}
	s.Baker.AddCell(chart, s.lib, s.mariner)
	return chart.Bounds(), nil
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

// CellData is a base cell (.000) plus its sequential update files (.001, .002, …)
// keyed by filename. Updates are applied in order to bring the cell to its current
// edition.
type CellData struct {
	Base    []byte
	Updates map[string][]byte
}

// ParseCellWithUpdates parses a base cell with its update files applied. The base
// + every update are staged on an in-memory filesystem so the parser discovers
// and applies the .001/.002/… chain (vs ParseCellBytes, which parses base-only).
func ParseCellWithUpdates(name string, base []byte, updates map[string][]byte) (*s57.Chart, error) {
	p := "/" + path.Base(name)
	fsys := iso8211.MemFS{p: base}
	dir := path.Dir(p)
	for un, ub := range updates {
		fsys[path.Join(dir, path.Base(un))] = ub
	}
	opts := s57.DefaultParseOptions()
	opts.Fs = fsys
	opts.ApplyUpdates = true
	return s57.ParseWithOptions(p, opts)
}

// BuildBakerWithUpdates is BuildBaker, but each cell's update files are applied.
// cells maps a cell name (the base filename) to its base+update bytes. When
// overzoom is true every band overzooms DOWN to the world view (Baker
// .OverzoomAllBands) so a standalone large-scale set (e.g. an IENC bundle with no
// overview cells) stays visible zoomed out; leave it false for a full NOAA bake
// whose overview/general bands already supply the zoomed-out skeleton.
func BuildBakerWithUpdates(cells map[string]CellData, overzoom bool, onSkip func(name string, err error)) (*bake.Baker, []string, error) {
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
	b.OverzoomAllBands = overzoom
	var ok []string
	for _, name := range names {
		cd := cells[name]
		chart, err := ParseCellWithUpdates(name, cd.Base, cd.Updates)
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

	// Build the inverted tile→prim index once (single-threaded) so each parallel
	// worker's EmitTileInto iterates only on-tile prims instead of scanning all
	// of b.prims. Read-only after this point, so concurrent reads are safe.
	b.BuildEmitIndex(MVTExtent, MVTBuffer)

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
	// Override the tile-derived bounds (the spec-display z0 world tile would make
	// them global) with the real cell-union extent so clients frame to the charts.
	if bb := b.Bounds(); bb.MinLon <= bb.MaxLon && bb.MinLat <= bb.MaxLat {
		pb.SetBounds(bb.MinLon, bb.MinLat, bb.MaxLon, bb.MaxLat)
	}
	return pb
}

// BakeToPMTilesBands bakes one PMTiles builder PER navigational-purpose band,
// keyed by band slug (only bands that produced tiles are returned). Each band's
// archive carries only that band's own data (EmitTileBandInto filters on natMax),
// gap-clipped a couple zooms past its native max, so the frontend can load it into
// a chart-<slug> source whose maxzoom = band.max + margin and client-overzoom it
// up. A coarser band's source fills a finer band's gaps via its own overzoom; the
// gap-clipping keeps it from bleeding where the finer band actually has data. This
// reproduces the realtime/wasm best-available result in a bounded eager bake. The
// shared margin-extended emit index is built once.
func BakeToPMTilesBands(b *bake.Baker, progress func(done, total int)) map[string]*pmtiles.Builder {
	b.BuildEmitIndexBands(MVTExtent, MVTBuffer) // built once; read-only, shared across bands
	type job struct {
		slug    string
		bandMax uint32
		coords  []tile.TileCoord
	}
	var jobs []job
	total := 0
	for _, bd := range bake.BakeBands() {
		if c := b.TileCoordsBand(MVTExtent, bd.Min, bd.Max); len(c) > 0 {
			jobs = append(jobs, job{bd.Slug, bd.Max, c})
			total += len(c)
		}
	}
	bb := b.Bounds()
	out := map[string]*pmtiles.Builder{}
	var done int64
	for _, j := range jobs {
		encoded := make([][]byte, len(j.coords))
		workers := runtime.NumCPU()
		if workers > len(j.coords) {
			workers = len(j.coords)
		}
		if workers < 1 {
			workers = 1
		}
		var next int64 = -1
		var wg sync.WaitGroup
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func(bandMax uint32) {
				var ts bake.TileScratch
				defer wg.Done()
				for {
					i := int(atomic.AddInt64(&next, 1))
					if i >= len(j.coords) {
						return
					}
					encoded[i] = b.EmitTileBandInto(j.coords[i], MVTExtent, MVTBuffer, &ts, bandMax)
					if progress != nil {
						progress(int(atomic.AddInt64(&done, 1)), total)
					}
				}
			}(j.bandMax)
		}
		wg.Wait()
		pb := pmtiles.New()
		for i, c := range j.coords {
			if encoded[i] != nil {
				pb.AddTile(uint8(c.Z), c.X, c.Y, encoded[i])
			}
		}
		if bb.MinLon <= bb.MaxLon && bb.MinLat <= bb.MaxLat {
			pb.SetBounds(bb.MinLon, bb.MinLat, bb.MaxLon, bb.MaxLat)
		}
		if pb.Count() > 0 {
			out[j.slug] = pb
		}
	}
	return out
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
