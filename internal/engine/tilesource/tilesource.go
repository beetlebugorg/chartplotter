// Package tilesource serves MVT tiles from one of three interchangeable backends
// behind a single interface, selected per tile set:
//
//   - dynamic  — bake-on-demand from cached ENC cells (an in-memory baker + tile
//     LRU); see [Dynamic].
//   - pmtiles  — a prebaked .pmtiles archive (random access via pmtiles.Reader).
//   - mbtiles  — a prebaked .mbtiles SQLite archive (pure-Go modernc.org/sqlite).
//
// Every backend returns the DECOMPRESSED MVT body for a tile (the pmtiles/mbtiles
// readers gunzip stored-gzipped tiles transparently), so the HTTP layer can serve
// the bytes as-is or apply its own transfer encoding. A nil body with a nil error
// means "no tile here" — a blank/missing tile, not a failure.
package tilesource

import (
	"fmt"
	"io"
	"strings"

	"github.com/beetlebugorg/chartplotter/internal/engine/pmtiles"
)

// TileMeta is a tile set's display metadata (bounds, zoom range, gzip flag). It is
// an alias for pmtiles.TileMeta so the pmtiles.Reader satisfies TileSource with no
// adapter, and so every backend reports the same shape.
type TileMeta = pmtiles.TileMeta

// TileSource is read access to one tile set. Tile returns the decompressed MVT for
// z/x/y in XYZ addressing (the MapLibre/pmtiles convention; mbtiles' TMS y-flip is
// handled inside that backend). (nil, nil) means a blank/missing tile.
type TileSource interface {
	Tile(z uint8, x, y uint32) ([]byte, error)
	Meta() TileMeta
}

// Open opens a prebaked archive as a TileSource, dispatching on the file
// extension (.pmtiles or .mbtiles). The returned source is an io.Closer; callers
// own closing it. Use NewDynamic for the bake-on-demand backend.
func Open(path string) (TileSource, error) {
	switch {
	case strings.HasSuffix(path, ".pmtiles"):
		return pmtiles.Open(path)
	case strings.HasSuffix(path, ".mbtiles"):
		return OpenMBTiles(path)
	default:
		return nil, fmt.Errorf("tilesource: unrecognised archive %q (want .pmtiles or .mbtiles)", path)
	}
}

// Close closes src if it owns resources (a file handle or DB). Backends built
// in-memory (Dynamic) are not closers, so Close is a no-op for them.
func Close(src TileSource) error {
	if c, ok := src.(io.Closer); ok {
		return c.Close()
	}
	return nil
}
