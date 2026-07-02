// Package baker holds the CGO-free S-57 cell metadata + parse helpers shared by
// the server chart library, the cell index, and the tile57 bake path: parsing a
// cell's bytes (base + updates), extracting its header/coverage metadata, and the
// compilation-scale → navigational-band mapping (bands.go). It does not bake
// tiles — the native libtile57 engine is the sole tile/portrayal engine.
package baker

import (
	"path"

	"github.com/beetlebugorg/chartplotter/pkg/iso8211"
	"github.com/beetlebugorg/chartplotter/pkg/s57"
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

// CellData is a base cell (.000) plus its sequential update files (.001, .002, …)
// keyed by filename. Updates are applied in order to bring the cell to its current
// edition.
type CellData struct {
	Base    []byte
	Updates map[string][]byte
}

// ParseCellCoverage parses ONLY a cell's M_COVR coverage features, skipping every
// other feature's geometry construction (the expensive topology/ring assembly) —
// enough for the cell's coverage bbox + header scale, far cheaper than a full
// parse. Updates are still applied (the filter acts after them).
func ParseCellCoverage(name string, base []byte, updates map[string][]byte) (*s57.Chart, error) {
	p := "/" + path.Base(name)
	fsys := iso8211.MemFS{p: base}
	dir := path.Dir(p)
	for un, ub := range updates {
		fsys[path.Join(dir, path.Base(un))] = ub
	}
	opts := s57.DefaultParseOptions()
	opts.Fs = fsys
	opts.ApplyUpdates = true
	opts.ObjectClassFilter = []string{"M_COVR"}
	return s57.ParseWithOptions(p, opts)
}
