package tilesource

import (
	"container/list"
	"sync"

	"github.com/beetlebugorg/chartplotter/internal/engine/bake"
	"github.com/beetlebugorg/chartplotter/internal/engine/baker"
	"github.com/beetlebugorg/chartplotter/internal/engine/pmtiles"
	"github.com/beetlebugorg/chartplotter/internal/engine/tile"
)

// defaultDynamicCache is the default number of baked tiles held in the LRU.
const defaultDynamicCache = 4096

// Dynamic bakes tiles on demand from a set of cached ENC cells. The cells are
// parsed once into a long-lived Baker (with the emit index built) at construction;
// each Tile call bakes the requested z/x/y with the same EmitTileInto the prebake
// path uses, memoised in a bounded LRU so repeat requests are free. EmitTileInto
// is read-only after the emit index is built, so concurrent Tile calls are safe.
type Dynamic struct {
	baker   *bake.Baker
	meta    TileMeta
	scratch sync.Pool // *bake.TileScratch, one per concurrent bake

	mu    sync.Mutex // guards the LRU
	cap   int
	cache map[uint64]*list.Element
	order *list.List // front = most recently used
}

type lruEntry struct {
	id   uint64
	body []byte // nil for an empty (blank) tile — negatives are cached too
}

// NewDynamic builds a bake-on-demand source from raw base cells (name → .000
// bytes). onSkip, if non-nil, is called for each cell that fails to parse. The
// returned source needs no Close. cacheTiles caps the baked-tile LRU; pass 0 for
// the default.
func NewDynamic(cells map[string][]byte, cacheTiles int, onSkip func(name string, err error)) (*Dynamic, error) {
	b, _, err := baker.BuildBaker(cells, onSkip)
	if err != nil {
		return nil, err
	}
	b.BuildEmitIndex(baker.MVTExtent, baker.MVTBuffer)

	if cacheTiles <= 0 {
		cacheTiles = defaultDynamicCache
	}
	d := &Dynamic{
		baker: b,
		cap:   cacheTiles,
		cache: make(map[uint64]*list.Element, cacheTiles),
		order: list.New(),
	}
	d.scratch.New = func() any { return &bake.TileScratch{} }
	d.meta = d.computeMeta()
	return d, nil
}

// computeMeta derives the zoom range from the baker's tile coords and the bounds
// from the cell-union bbox (matching BakeToPMTiles' SetBounds).
func (d *Dynamic) computeMeta() TileMeta {
	m := TileMeta{MinZoom: 255}
	for _, c := range d.baker.TileCoords(baker.MVTExtent) {
		z := uint8(c.Z)
		if z < m.MinZoom {
			m.MinZoom = z
		}
		if z > m.MaxZoom {
			m.MaxZoom = z
		}
	}
	if m.MinZoom == 255 {
		m.MinZoom = 0
	}
	if bb := d.baker.Bounds(); bb.MinLon <= bb.MaxLon && bb.MinLat <= bb.MaxLat {
		m.W, m.S, m.E, m.N = bb.MinLon, bb.MinLat, bb.MaxLon, bb.MaxLat
	}
	return m
}

// Meta returns the set's metadata.
func (d *Dynamic) Meta() TileMeta { return d.meta }

// Tile bakes (or returns the cached) MVT for z/x/y. A tile with no features bakes
// to nil, which is cached and returned as a blank tile.
func (d *Dynamic) Tile(z uint8, x, y uint32) ([]byte, error) {
	id := pmtiles.ZxyToTileID(z, x, y)
	if body, ok := d.cacheGet(id); ok {
		return body, nil
	}
	ts := d.scratch.Get().(*bake.TileScratch)
	body := d.baker.EmitTileInto(tile.TileCoord{Z: uint32(z), X: x, Y: y}, baker.MVTExtent, baker.MVTBuffer, ts)
	d.scratch.Put(ts)

	// EmitTileInto reuses ts's buffers, so copy the body before caching/returning.
	var out []byte
	if len(body) > 0 {
		out = append([]byte(nil), body...)
	}
	d.cachePut(id, out)
	return out, nil
}

func (d *Dynamic) cacheGet(id uint64) ([]byte, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if el, ok := d.cache[id]; ok {
		d.order.MoveToFront(el)
		return el.Value.(*lruEntry).body, true
	}
	return nil, false
}

func (d *Dynamic) cachePut(id uint64, body []byte) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if el, ok := d.cache[id]; ok { // raced with another bake; refresh
		el.Value.(*lruEntry).body = body
		d.order.MoveToFront(el)
		return
	}
	d.cache[id] = d.order.PushFront(&lruEntry{id: id, body: body})
	for len(d.cache) > d.cap {
		oldest := d.order.Back()
		if oldest == nil {
			break
		}
		d.order.Remove(oldest)
		delete(d.cache, oldest.Value.(*lruEntry).id)
	}
}
