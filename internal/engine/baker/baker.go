// Package baker wires S-57 cell bytes through parse + S-101 portrayal into a
// PMTiles archive. It is the shared core behind the bake-zip / provision CLI
// paths and the server's background provision job.
package baker

import (
	"bytes"
	"compress/gzip"
	"path"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/beetlebugorg/chartplotter/internal/engine/bake"
	"github.com/beetlebugorg/chartplotter/internal/engine/pmtiles"
	"github.com/beetlebugorg/chartplotter/internal/engine/s101catalog"
	"github.com/beetlebugorg/chartplotter/internal/engine/tile"
	"github.com/beetlebugorg/chartplotter/pkg/geo"
	"github.com/beetlebugorg/chartplotter/pkg/iso8211"
	"github.com/beetlebugorg/chartplotter/pkg/s57"
)

// MVT bake parameters (default values).
const (
	MVTExtent uint32  = 4096
	MVTBuffer float64 = 64
)

// s101Portrayer is an optional external-catalogue override set via
// UseS101Catalog. When set, every Baker built here portrays from that catalogue;
// otherwise baking uses the build-time embedded catalogue.
var s101Portrayer bake.Portrayer

// UseS101Catalog overrides the embedded catalogue, loading the S-101 portrayal
// engine from a PortrayalCatalog directory + a FeatureCatalogue.xml path. Call
// once before baking.
func UseS101Catalog(portrayalCatalogDir, featureCataloguePath string) error {
	p, err := bake.NewS101Portrayer(portrayalCatalogDir, featureCataloguePath)
	if err != nil {
		return err
	}
	s101Portrayer = p
	return nil
}

var (
	embeddedOnce sync.Once
	embeddedPort bake.Portrayer
)

// embeddedPortrayer lazily builds the portrayer from the build-time embedded
// S-101 catalogue (internal/engine/s101catalog), or returns nil if this binary
// was built without it (a plain `go build`, no -tags embed_s101).
func embeddedPortrayer() bake.Portrayer {
	embeddedOnce.Do(func() {
		if !s101catalog.Available() {
			return
		}
		catFS, err := s101catalog.PortrayalFS()
		if err != nil {
			return
		}
		fcXML, err := s101catalog.FeatureCatalogue()
		if err != nil {
			return
		}
		if p, err := bake.NewS101PortrayerFS(catFS, fcXML); err == nil {
			embeddedPort = p
		}
	})
	return embeddedPort
}

func applyPortrayer(b *bake.Baker) {
	// An explicit --s101 override (UseS101Catalog) wins; otherwise use the
	// build-time embedded catalogue.
	p := s101Portrayer
	if p == nil {
		p = embeddedPortrayer()
	}
	if p != nil {
		b.SetPortrayer(p)
	}
}

// ParseCellBytes parses an S-57 base cell held entirely in memory (e.g. a zip
// entry or a downloaded NOAA cell) by staging it on an in-memory filesystem.
// Updates are not applied (the cell is parsed at its base edition).
func ParseCellBytes(name string, data []byte) (*s57.Chart, error) {
	p := "/" + path.Base(name)
	opts := s57.DefaultParseOptions()
	opts.Fs = iso8211.MemFS{p: data}
	opts.ApplyUpdates = false
	opts.MaskCoastlineCoincidentBoundaries = true
	return s57.ParseWithOptions(p, opts)
}

// Session is an incremental Baker builder: cells are parsed and added one at a
// time (AddCell) into a long-lived Baker, instead of all-at-once via BuildBaker.
// This is the real-time wasm path — parsing a single large cell can take seconds
// in wasm, so loading the set one cell per call lets the host yield between cells
// (servicing tile requests / reporting progress) rather than blocking on the
// whole set.
type Session struct {
	Baker *bake.Baker
}

// NewSession returns an empty incremental Session. Add cells with AddCellBytes,
// then bake tiles off Session.Baker.
func NewSession() (*Session, error) {
	b := bake.New()
	applyPortrayer(b)
	b.OverzoomAllBands = true // realtime/upload path: keep a few uploaded cells visible (skeleton) when zoomed out
	return &Session{Baker: b}, nil
}

// AddCellBytes parses one raw cell and adds it to the session's Baker. The caller
// should rebuild the emit index (Baker.BuildEmitIndex) before baking tiles after
// any add.
func (s *Session) AddCellBytes(name string, data []byte) (s57.Bounds, error) {
	chart, err := ParseCellBytes(name, data)
	if err != nil {
		return s57.Bounds{}, err
	}
	s.Baker.AddCell(chart)
	return chart.Bounds(), nil
}

// BuildBaker parses and adds each named cell to a fresh Baker. cells maps a
// cell name (or path) to its raw bytes. onSkip, if non-nil, is called for each
// cell that fails to parse. Returns the Baker and the names successfully added,
// in sorted order for deterministic output.
func BuildBaker(cells map[string][]byte, onSkip func(name string, err error)) (*bake.Baker, []string, error) {
	names := make([]string, 0, len(cells))
	for n := range cells {
		names = append(names, n)
	}
	sort.Strings(names)

	b := bake.New()
	applyPortrayer(b)
	ok := addCellsParallel(b, names, func(name string) (*s57.Chart, error) {
		return ParseCellBytes(name, cells[name])
	}, onSkip)
	return b, ok, nil
}

// addCellsParallel parses and portrays cells on a bounded worker pool, then
// routes them into b serially in `names` order. Parsing and S-101 portrayal are
// the dominant bake cost and are independent per cell, so they run concurrently;
// the stateful route/merge stays single-threaded and ordered, so the archive is
// byte-for-byte identical to the serial path. At most ~NumCPU cells are resident
// at once (the trade for the speedup — the per-band streaming bake remains the
// path for memory-bounded huge districts). onSkip fires in `names` order.
func addCellsParallel(b *bake.Baker, names []string, parse func(name string) (*s57.Chart, error), onSkip func(name string, err error)) []string {
	workers := runtime.NumCPU()
	if workers > len(names) {
		workers = len(names)
	}
	if workers < 1 {
		return nil
	}
	type result struct {
		chart *s57.Chart
		pc    bake.CellPortrayal
		err   error
	}
	slots := make([]chan result, len(names))
	for i := range slots {
		slots[i] = make(chan result, 1)
	}
	// sem bounds in-flight cells; a token is acquired before a worker starts and
	// released by the consumer after that cell is routed, so no more than `workers`
	// parsed+portrayed cells are resident ahead of the routing goroutine.
	sem := make(chan struct{}, workers)
	go func() {
		for i, name := range names {
			sem <- struct{}{}
			go func(i int, name string) {
				chart, err := parse(name)
				if err != nil {
					slots[i] <- result{err: err}
					return
				}
				slots[i] <- result{chart: chart, pc: b.PortrayCell(chart)}
			}(i, name)
		}
	}()
	ok := make([]string, 0, len(names))
	for i, name := range names {
		r := <-slots[i]
		if r.err != nil {
			if onSkip != nil {
				onSkip(name, r.err)
			}
			<-sem
			continue
		}
		b.AddCellPortrayed(r.chart, r.pc)
		ok = append(ok, name)
		<-sem
	}
	return ok
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
	opts.MaskCoastlineCoincidentBoundaries = true
	return s57.ParseWithOptions(p, opts)
}

// BuildBakerWithUpdates is BuildBaker, but each cell's update files are applied.
// cells maps a cell name (the base filename) to its base+update bytes. When
// overzoom is true every band overzooms DOWN to the world view (Baker
// .OverzoomAllBands) so a standalone large-scale set (e.g. an IENC bundle with no
// overview cells) stays visible zoomed out; leave it false for a full NOAA bake
// whose overview/general bands already supply the zoomed-out skeleton.
func BuildBakerWithUpdates(cells map[string]CellData, overzoom bool, onSkip func(name string, err error)) (*bake.Baker, []string, error) {
	names := make([]string, 0, len(cells))
	for n := range cells {
		names = append(names, n)
	}
	sort.Strings(names)

	b := bake.New()
	applyPortrayer(b)
	b.OverzoomAllBands = overzoom
	ok := addCellsParallel(b, names, func(name string) (*s57.Chart, error) {
		cd := cells[name]
		return ParseCellWithUpdates(name, cd.Base, cd.Updates)
	}, onSkip)
	return b, ok, nil
}

// BakeToPMTiles bakes every tile from b into a PMTiles builder. Tiles are emitted
// in parallel across all CPUs (EmitTileInto only reads the Baker) and each worker
// adds its tile to the builder directly under a mutex, so only one encoded tile
// per worker is live at a time instead of holding every tile's bytes in a second
// full-size slice. (Add order is no longer deterministic, so the blob LAYOUT can
// vary run-to-run; the tiles served — sorted by TileID at write — are identical.)
// progress, if non-nil, is called as (tilesEmitted, totalTiles).
func BakeToPMTiles(b *bake.Baker, progress func(done, total int)) *pmtiles.Builder {
	// Build the inverted tile→prim index once (single-threaded) so each parallel
	// worker's EmitTileInto iterates only on-tile prims instead of scanning all of
	// b.prims. Read-only after this point, so concurrent reads are safe.
	b.BuildEmitIndex(MVTExtent, MVTBuffer)
	pb := pmtiles.New()
	emitTiles(b.TileCoords(MVTExtent), pb, progress, func(c tile.TileCoord, ts *bake.TileScratch) []byte {
		return b.EmitTileInto(c, MVTExtent, MVTBuffer, ts)
	})
	// Override the tile-derived bounds (the spec-display z0 world tile would make
	// them global) with the real cell-union extent so clients frame to the charts.
	if bb := b.Bounds(); bb.MinLon <= bb.MaxLon && bb.MinLat <= bb.MaxLat {
		pb.SetBounds(bb.MinLon, bb.MinLat, bb.MaxLon, bb.MaxLat)
	}
	return pb
}

// emitTiles bakes coords in parallel and adds each non-empty tile to pb under a
// mutex — no intermediate full-archive slice of encoded bytes (only one tile per
// worker is live at a time). emit returns the encoded tile (or nil to skip).
func emitTiles(coords []tile.TileCoord, pb *pmtiles.Builder, progress func(done, total int), emit func(tile.TileCoord, *bake.TileScratch) []byte) {
	total := len(coords)
	workers := runtime.NumCPU()
	if workers > total {
		workers = total
	}
	if workers < 1 {
		workers = 1
	}
	pb.SetTilesGzipped() // each tile is gzipped in the worker below
	var mu sync.Mutex
	var next, done int64 = -1, 0
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			var ts bake.TileScratch // reused across every tile this worker bakes
			gz := gzip.NewWriter(nil)
			var buf bytes.Buffer
			defer wg.Done()
			for {
				i := int(atomic.AddInt64(&next, 1))
				if i >= total {
					return
				}
				c := coords[i]
				if data := emit(c, &ts); data != nil {
					// gzip in the worker (parallel); the archive stores compressed
					// tiles (~3–5× smaller blob + file). Deterministic (no mtime), so
					// identical tiles still dedup.
					buf.Reset()
					gz.Reset(&buf)
					gz.Write(data)
					gz.Close()
					mu.Lock()
					pb.AddTile(uint8(c.Z), c.X, c.Y, buf.Bytes())
					mu.Unlock()
				}
				if progress != nil {
					progress(int(atomic.AddInt64(&done, 1)), total)
				}
			}
		}()
	}
	wg.Wait()
}

// BakeToPMTilesBandsStreaming bakes per-band archives while holding only ONE
// band's parsed geometry in memory at a time — the key to baking a large district
// without keeping every cell's prims resident. It works in two passes:
//
//	Pass 1 — parse each cell and extract only its coverage + native band (no
//	  feature routing), building the global covMeta once so best-available
//	  suppression and scale boundaries have full cross-band coverage.
//	Pass 2 — for each band, re-parse just that band's cells, route them, bake the
//	  band's archive (emit), then drop the prims before the next band.
//
// Cells are therefore parsed twice, but pass 1 skips the expensive portrayal +
// routing, so the overhead is small relative to the memory saved. emit(slug,
// builder) is called per band that produced tiles; returns the cell-union bounds
// and the number of cells parsed.
func BakeToPMTilesBandsStreaming(cells map[string]CellData, maxZoom uint32, onSkip func(name string, err error), progress func(done, total int), emit func(slug string, pb *pmtiles.Builder) error) (geo.BoundingBox, int, error) {
	b := bake.New()
	applyPortrayer(b)
	if maxZoom > 0 {
		b.MaxBakeZoom = maxZoom
	}

	names := make([]string, 0, len(cells))
	for n := range cells {
		names = append(names, n)
	}
	sort.Strings(names)

	// Pass 1: coverage + band per cell (no routing).
	byBand := map[uint32][]string{}
	parsed := 0
	for _, name := range names {
		cd := cells[name]
		chart, err := ParseCellWithUpdates(name, cd.Base, cd.Updates)
		if err != nil {
			if onSkip != nil {
				onSkip(name, err)
			}
			continue
		}
		band := b.AddCellCoverage(chart)
		byBand[band.ZoomRange().Max] = append(byBand[band.ZoomRange().Max], name)
		parsed++
	}
	b.SetSkipCoverage(true) // covMeta is now global; don't re-derive it per band

	// Pass 2: per band, re-parse + route + bake + free.
	for _, bd := range bake.BakeBands() {
		bandCells := byBand[bd.Max]
		if len(bandCells) == 0 {
			continue
		}
		b.ResetPrims()
		for _, name := range bandCells {
			cd := cells[name]
			chart, err := ParseCellWithUpdates(name, cd.Base, cd.Updates)
			if err != nil {
				if onSkip != nil {
					onSkip(name, err)
				}
				continue
			}
			b.AddCell(chart)
		}
		coords := b.TileCoordsBand(MVTExtent, bd.Min, bd.Max)
		if len(coords) == 0 {
			continue
		}
		b.BuildEmitIndexBand(MVTExtent, MVTBuffer, bd.Max)
		pb := pmtiles.New()
		emitTiles(coords, pb, progress, func(c tile.TileCoord, ts *bake.TileScratch) []byte {
			return b.EmitTileBandInto(c, MVTExtent, MVTBuffer, ts, bd.Max)
		})
		b.ClearEmitIndex()
		pb.SetScamin(b.ScaminValues()) // publish this band's SCAMIN manifest in the archive metadata
		if bb := b.Bounds(); bb.MinLon <= bb.MaxLon && bb.MinLat <= bb.MaxLat {
			pb.SetBounds(bb.MinLon, bb.MinLat, bb.MaxLon, bb.MaxLat)
		}
		if pb.Count() == 0 {
			continue
		}
		if err := emit(bd.Slug, pb); err != nil { // write + free this band before the next
			return b.Bounds(), parsed, err
		}
	}
	return b.Bounds(), parsed, nil
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
