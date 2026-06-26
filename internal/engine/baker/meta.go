package baker

import (
	"sort"
	"strconv"
)

// CellMeta is the per-cell metadata extracted at import time for the chart
// library to display. It comes from the cell's S-57 header (DSID/DSPM) plus its
// M_COVR coverage — gathered with the same cheap coverage-only parse the bake's
// pass 1 uses, so it adds no full re-parse. Title is left to the caller to fill
// from the exchange-set catalogue (CATALOG.031 LFIL), since S-57 headers carry
// no human chart name.
type CellMeta struct {
	Name      string     `json:"name"`            // cell stem, e.g. "US5MD1MC"
	Title     string     `json:"title,omitempty"` // long name (from CATALOG.031), else dataset name
	Scale     int        `json:"scale,omitempty"` // compilation scale denominator (CSCL)
	Edition   string     `json:"edition,omitempty"`
	Update    string     `json:"update,omitempty"`
	IssueDate string     `json:"issueDate,omitempty"` // YYYYMMDD
	Agency    int        `json:"agency,omitempty"`    // IHO producing-agency code (550 = NOAA)
	BBox      [4]float64 `json:"bbox,omitempty"`      // [west, south, east, north]
	HasBBox   bool       `json:"-"`
}

// ExtractCellMeta parses each cell's header + coverage (coverage-only, cheap) and
// returns per-cell metadata keyed by cell stem. Cells that fail to parse are
// reported via onSkip and omitted. Title is populated with the dataset name as a
// fallback; the caller overlays the catalogue long name where available.
func ExtractCellMeta(cells map[string]CellData, onSkip func(name string, err error)) map[string]CellMeta {
	out := make(map[string]CellMeta, len(cells))
	names := make([]string, 0, len(cells))
	for n := range cells {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		cd := cells[name]
		chart, err := ParseCellCoverage(name, cd.Base, cd.Updates)
		if err != nil {
			if onSkip != nil {
				onSkip(name, err)
			}
			continue
		}
		stem := cellStem(chart.DatasetName())
		if stem == "" {
			stem = cellStem(name)
		}
		m := CellMeta{
			Name:      stem,
			Title:     chart.DatasetName(),
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
		out[stem] = m
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
