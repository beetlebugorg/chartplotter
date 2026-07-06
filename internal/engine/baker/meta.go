// Package baker holds the S-57 cell metadata helpers shared by the server chart
// library, the cell index, and the tile57 bake paths — per-cell header metadata
// via the native engine (tile57_chart_cells) and the compilation-scale →
// navigational-band mapping (bands.go) — plus the CellData staging type. It does
// not parse S-57 itself: libtile57 is the sole S-57 reader and tile/portrayal
// engine; the Go side only stages .000/.NNN bytes for it.
package baker

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	tile57 "github.com/beetlebugorg/tile57/bindings/go"
)

// CellData is a base cell (.000) plus its sequential update files (.001, .002, …)
// keyed by filename. Updates are applied in order to bring the cell to its current
// edition.
type CellData struct {
	Base    []byte
	Updates map[string][]byte
}

// CellMeta is the per-cell metadata extracted at import time for the chart
// library to display: the cell's S-57 header identity (DSID/DSPM after the
// update chain) plus its coverage bbox, as reported by the native engine
// (tile57_chart_cells). Title is left to the caller to fill from the
// exchange-set catalogue (CATALOG.031 LFIL), since S-57 headers carry no human
// chart name.
type CellMeta struct {
	Name      string     `json:"name"`            // cell stem, e.g. "US5MD1MC"
	Title     string     `json:"title,omitempty"` // human chart name (from CATALOG.031 LFIL); empty if none — consumers show Name
	Scale     int        `json:"scale,omitempty"` // compilation scale denominator (CSCL)
	Edition   string     `json:"edition,omitempty"`
	Update    string     `json:"update,omitempty"`
	IssueDate string     `json:"issueDate,omitempty"` // YYYYMMDD
	Agency    int        `json:"agency,omitempty"`    // IHO producing-agency code (550 = NOAA)
	BBox      [4]float64 `json:"bbox,omitempty"`      // [west, south, east, north]
	HasBBox   bool       `json:"-"`
}

// ExtractCellMeta stages the cells (base + updates) to a temporary ENC dir,
// opens it with the native engine, and returns per-cell metadata keyed by cell
// stem (the DSNM stem the engine reports). Cells that fail to parse are
// reported via onSkip and omitted. Title is left empty (S-57 headers carry no
// human chart name — only the cell code); the caller overlays the CATALOG.031
// long name where the exchange set provides one.
func ExtractCellMeta(cells map[string]CellData, onSkip func(name string, err error)) map[string]CellMeta {
	names := make([]string, 0, len(cells))
	for n := range cells {
		names = append(names, n)
	}
	sort.Strings(names)
	skipAll := func(err error) map[string]CellMeta {
		if onSkip != nil {
			for _, n := range names {
				onSkip(n, err)
			}
		}
		return map[string]CellMeta{}
	}
	if len(cells) == 0 {
		return map[string]CellMeta{}
	}

	dir, err := os.MkdirTemp("", "cp-cellmeta-")
	if err != nil {
		return skipAll(err)
	}
	defer os.RemoveAll(dir)
	for _, name := range names {
		cd := cells[name]
		if err := os.WriteFile(filepath.Join(dir, filepath.Base(name)), cd.Base, 0o644); err != nil {
			return skipAll(err)
		}
		for un, ub := range cd.Updates {
			if err := os.WriteFile(filepath.Join(dir, filepath.Base(un)), ub, 0o644); err != nil {
				return skipAll(err)
			}
		}
	}

	src, err := tile57.Open(dir)
	if err != nil {
		return skipAll(err)
	}
	defer src.Close()
	infos, err := src.Cells()
	if err != nil {
		return skipAll(err)
	}

	out := make(map[string]CellMeta, len(infos))
	for _, ci := range infos {
		out[ci.Name] = CellMeta{
			Name:      ci.Name,
			Scale:     ci.Scale,
			Edition:   ci.Edition,
			Update:    ci.Update,
			IssueDate: ci.IssueDate,
			Agency:    ci.Agency,
			BBox:      ci.BBox,
			HasBBox:   ci.HasBBox,
		}
	}
	// Report the input cells the engine skipped (didn't parse into the inventory).
	if onSkip != nil {
		for _, name := range names {
			if _, ok := out[cellStem(name)]; !ok {
				onSkip(name, fmt.Errorf("cell did not parse"))
			}
		}
	}
	return out
}

// cellStem trims a trailing ".000"/".NNN" or directory path from a cell name.
func cellStem(name string) string {
	// Strip any directory.
	for i := len(name) - 1; i >= 0; i-- {
		if name[i] == '/' || name[i] == '\\' {
			name = name[i+1:]
			break
		}
	}
	// Strip a 3-digit S-57 extension.
	if n := len(name); n >= 4 && name[n-4] == '.' {
		if _, err := strconv.Atoi(name[n-3:]); err == nil {
			return name[:n-4]
		}
	}
	return name
}
