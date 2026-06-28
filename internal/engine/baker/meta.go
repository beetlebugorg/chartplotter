package baker

import (
	"path"
	"sort"
	"strconv"

	"github.com/beetlebugorg/chartplotter/pkg/iso8211"
	"github.com/beetlebugorg/chartplotter/pkg/s57"
)

// CellMeta is the per-cell metadata extracted at import time for the chart
// library to display. It comes from the cell's S-57 header (DSID/DSPM) plus its
// M_COVR coverage — gathered with the same cheap coverage-only parse the bake's
// pass 1 uses, so it adds no full re-parse. Title is left to the caller to fill
// from the exchange-set catalogue (CATALOG.031 LFIL), since S-57 headers carry
// no human chart name.
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

// ExtractCellMeta returns per-cell metadata keyed by cell stem. Identity and scale
// come from each cell's S-57 header (DSID/DSPM); coverage comes from the exchange
// -set catalogue when it covers the cell — sparing a parse — and otherwise from an
// M_COVR-only coverage parse. Pass cat=nil when there is no catalogue.
//
// Cells that fail to parse are reported via onSkip and omitted. Title is left empty
// (S-57 headers carry no human chart name — only the cell code); the caller overlays
// the CATALOG.031 long name where the exchange set provides one.
func ExtractCellMeta(cells map[string]CellData, cat *s57.Catalog, onSkip func(name string, err error)) map[string]CellMeta {
	catBBox := catalogBBoxes(cat)
	out := make(map[string]CellMeta, len(cells))
	names := make([]string, 0, len(cells))
	for n := range cells {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		m, err := cellMetaFor(name, cells[name], catBBox)
		if err != nil {
			if onSkip != nil {
				onSkip(name, err)
			}
			continue
		}
		out[m.Name] = m
	}
	return out
}

// catalogBBoxes indexes an exchange-set catalogue's per-cell coverage by cell stem,
// or returns nil when there's no catalogue / no coverage in it.
func catalogBBoxes(cat *s57.Catalog) map[string][4]float64 {
	if cat == nil {
		return nil
	}
	out := map[string][4]float64{}
	for _, e := range cat.Cells() {
		if e.HasBBox {
			out[e.CellStem()] = [4]float64{e.West, e.South, e.East, e.North}
		}
	}
	return out
}

// cellMetaFor builds one cell's metadata. When the catalogue already supplies the
// cell's coverage AND the cell has no updates (so its base-cell header still carries
// the current identity), it reads only the header — DSID/DSPM, no geometry — and
// takes the bbox from the catalogue, skipping the M_COVR coverage parse entirely.
// Otherwise it falls back to the coverage parse, which also applies updates so the
// reported edition/update/date reflect the cell's current state.
func cellMetaFor(name string, cd CellData, catBBox map[string][4]float64) (CellMeta, error) {
	stem := cellStem(name)
	if len(cd.Updates) == 0 {
		if box, ok := catBBox[stem]; ok {
			p := "/" + path.Base(name)
			if h, err := s57.ReadHeaderFS(iso8211.MemFS{p: cd.Base}, p); err == nil {
				return CellMeta{
					Name:      stem,
					Scale:     int(h.CompilationScale),
					Edition:   h.Edition,
					Update:    h.UpdateNumber,
					IssueDate: h.IssueDate,
					Agency:    h.ProducingAgency,
					BBox:      box,
					HasBBox:   true,
				}, nil
			}
			// Header read failed (malformed front matter) — fall through to a full parse.
		}
	}

	chart, err := ParseCellCoverage(name, cd.Base, cd.Updates)
	if err != nil {
		return CellMeta{}, err
	}
	s := cellStem(chart.DatasetName())
	if s == "" {
		s = stem
	}
	m := CellMeta{
		Name:      s,
		Scale:     int(chart.CompilationScale()),
		Edition:   chart.Edition(),
		Update:    chart.UpdateNumber(),
		IssueDate: chart.IssueDate(),
		Agency:    chart.ProducingAgency(),
	}
	b := chart.Bounds()
	if b.MaxLon > b.MinLon && b.MaxLat > b.MinLat {
		m.BBox = [4]float64{b.MinLon, b.MinLat, b.MaxLon, b.MaxLat}
		m.HasBBox = true
	}
	return m, nil
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
