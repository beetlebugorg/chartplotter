package tilesource

import (
	tile57 "github.com/beetlebugorg/tile57/bindings/go"
)

// Composer is a TileSource backed by the tile57 runtime compositor: the per-cell PMTiles
// held mmap'd and the ownership partition resident, composing each tile on demand. It is the
// live counterpart of a prebaked .pmtiles archive — no district bake; tiles are built as the
// camera asks (a classify + one decode/clip or a decompress per tile). Serve is serialised
// inside tile57.ComposeSource, so this satisfies TileSource with no extra locking.
type Composer struct {
	src  *tile57.ComposeSource
	meta TileMeta
}

// NewComposer opens a runtime compositor over the per-cell PMTiles at paths (each from
// `tile57 compose --keep-cells` / tile57.BakeCell). partitionPath (or "") names a partition
// sidecar (`tile57 compose --save-partition`) to load and skip the owned-face build. Close it
// when done — callers must not Close while any request can still call Tile.
func NewComposer(paths []string, partitionPath string) (*Composer, error) {
	src, err := tile57.OpenCompose(paths, partitionPath)
	if err != nil {
		return nil, err
	}
	m := src.Meta()
	return &Composer{
		src: src,
		meta: TileMeta{
			MinZoom:  m.MinZoom,
			MaxZoom:  m.MaxZoom,
			W:        m.West,
			S:        m.South,
			E:        m.East,
			N:        m.North,
			Gzipped:  false, // Serve returns decompressed MLT; the HTTP layer gzips on the wire
			TileType: "mlt",
		},
	}, nil
}

// OwnershipTiler is a TileSource that also reports tile OWNERSHIP: whether its data model says a
// cell SHOULD render at (z,x,y). A blank (nil body) from an OWNED tile is transient (a cell's bake
// is still running) or an error (bake done) — the HTTP layer must not cache it, so it re-fetches
// once content lands; a blank from an UNOWNED tile is true empty ocean (safe to cache). The runtime
// Composer implements this; prebaked archives do not (their blanks are always true empty).
type OwnershipTiler interface {
	TileOwned(z uint8, x, y uint32) (body []byte, owned bool, err error)
}

// Tile composes (z,x,y) on demand, returning decompressed MLT, or (nil, nil) for a blank tile.
func (c *Composer) Tile(z uint8, x, y uint32) ([]byte, error) {
	body, _, err := c.src.Tile(z, x, y)
	return body, err
}

// TileOwned is Tile plus the ownership flag (implements OwnershipTiler).
func (c *Composer) TileOwned(z uint8, x, y uint32) (body []byte, owned bool, err error) {
	return c.src.Tile(z, x, y)
}

// Meta returns the compositor's display metadata (zoom range + coverage bounds).
func (c *Composer) Meta() TileMeta { return c.meta }

// SavePartition persists the resident ownership partition to path, so a later NewComposer can load
// it (as partitionPath) and skip the owned-face build.
func (c *Composer) SavePartition(path string) error { return c.src.SavePartition(path) }

// Close releases the compositor (io.Closer, so tilesource.Close finds it).
func (c *Composer) Close() error { return c.src.Close() }
