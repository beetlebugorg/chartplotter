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
// `tile57 compose --keep-cells` / tile57.BakeChart). The ownership partition is an internal
// detail of the engine now — found beside the archives, reused when it still matches the cell
// set, and rebuilt (and refreshed on disk) when it does not; nothing to pass or manage. Close it
// when done — callers must not Close while any request can still call Tile.
func NewComposer(paths []string) (*Composer, error) {
	src, err := tile57.OpenCompose(paths)
	if err != nil {
		return nil, err
	}
	return newComposerFrom(src), nil
}

// NewComposerTree opens a runtime compositor over EVERY per-cell PMTiles under dir, in
// one engine call: the walk, the mmap+open of each archive and the compose all happen
// inside libtile57 on its batch path. Prefer this over NewComposer for a baked tree —
// per-archive cgo opens cost ~35 ms each and a national library pays minutes for what
// the batch open does in seconds.
func NewComposerTree(dir string) (*Composer, error) {
	src, err := tile57.OpenComposeTree(dir)
	if err != nil {
		return nil, err
	}
	return newComposerFrom(src), nil
}

func newComposerFrom(src *tile57.ComposeSource) *Composer {
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
	}
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

// Close releases the compositor (io.Closer, so tilesource.Close finds it).
func (c *Composer) Close() error { return c.src.Close() }
