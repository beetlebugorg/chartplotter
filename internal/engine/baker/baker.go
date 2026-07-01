// Package baker holds the CGO-free S-57 cell metadata + parse helpers shared by
// the server chart library, the cell index, and the tile57 bake path: parsing a
// cell's bytes (base + updates), extracting its header/coverage metadata, and the
// compilation-scale → navigational-band mapping (bands.go). It no longer bakes
// tiles — the native libtile57 engine is the sole tile/portrayal engine.
package baker

import (
	"path"
	"strings"

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
	opts.MaskCoastlineCoincidentBoundaries = true
	return s57.ParseWithOptions(p, opts)
}

// CellData is a base cell (.000) plus its sequential update files (.001, .002, …)
// keyed by filename. Updates are applied in order to bring the cell to its current
// edition.
type CellData struct {
	Base    []byte
	Updates map[string][]byte
}

// cellParseOpts stages a base cell + its update files on an in-memory filesystem
// (so the parser discovers and applies the .001/.002/… chain) and returns the
// path + the standard parse options.
func cellParseOpts(name string, base []byte, updates map[string][]byte) (string, s57.ParseOptions) {
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
	return p, opts
}

// ParseCellWithUpdates parses a base cell with its update files applied (vs
// ParseCellBytes, which parses base-only).
func ParseCellWithUpdates(name string, base []byte, updates map[string][]byte) (*s57.Chart, error) {
	p, opts := cellParseOpts(name, base, updates)
	return s57.ParseWithOptions(p, opts)
}

// ParseCellCoverage parses ONLY a cell's M_COVR coverage features, skipping every
// other feature's geometry construction (the expensive topology/ring assembly) —
// enough for the cell's coverage bbox + header scale, far cheaper than a full
// parse. Updates are still applied (the filter acts after them).
func ParseCellCoverage(name string, base []byte, updates map[string][]byte) (*s57.Chart, error) {
	p, opts := cellParseOpts(name, base, updates)
	opts.ObjectClassFilter = []string{"M_COVR"}
	opts.MaskCoastlineCoincidentBoundaries = false // irrelevant to M_COVR rings; skip the coastline-edge setup
	return s57.ParseWithOptions(p, opts)
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
