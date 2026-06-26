package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/beetlebugorg/chartplotter/internal/engine/baker"
	"github.com/beetlebugorg/chartplotter/pkg/s57"
)

// setMetaExt is the per-pack metadata sidecar written beside the band archives
// in the pack's setDir (e.g. <cache>/USER/US5MD1MC/user-us5md1mc.meta.json).
const setMetaExt = ".meta.json"

// SetMeta is the per-pack metadata the chart library displays: the aggregate
// (title, agency, scale range, coverage, counts) plus the per-cell detail. Built
// at import time from the cells' S-57 headers (baker.ExtractCellMeta) overlaid
// with the exchange-set catalogue (CATALOG.031 long names + coverage).
type SetMeta struct {
	Set       string           `json:"set"`
	Title     string           `json:"title,omitempty"`
	Agency    string           `json:"agency,omitempty"`
	CellCount int              `json:"cellCount"`
	ScaleMin  int              `json:"scaleMin,omitempty"` // finest (smallest denom)
	ScaleMax  int              `json:"scaleMax,omitempty"` // coarsest (largest denom)
	BBox      []float64        `json:"bbox,omitempty"`     // [w,s,e,n] union, or nil
	Imported  string           `json:"imported,omitempty"` // RFC3339, stamped by the caller
	Cells     []baker.CellMeta `json:"cells,omitempty"`
}

// agencyName maps an IHO producing-agency code to a display name. Only the codes
// we expect from US ENC/IENC sources are named; others fall through to "Agency N".
func agencyName(code int) string {
	switch code {
	case 0:
		return ""
	case 550:
		return "NOAA (US)"
	default:
		return "Agency " + itoa(code)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// catalogPackIdentity derives a stable, friendly pack identity from an
// exchange-set catalogue: the longest common (alphanumeric) prefix of the base
// cell names, lowercased — e.g. cells US5MD1MC/US5MD2NW → "us5md". Returns ""
// when there's no usable shared prefix (≥3 chars) so the caller can fall back to
// the upload filename. A single cell yields that cell's full stem.
func catalogPackIdentity(cat *s57.Catalog) string {
	stems := make([]string, 0)
	for _, c := range cat.Cells() {
		if s := c.CellStem(); s != "" {
			stems = append(stems, s)
		}
	}
	return commonPrefixIdentity(stems)
}

func commonPrefixIdentity(stems []string) string {
	if len(stems) == 0 {
		return ""
	}
	if len(stems) == 1 {
		return slug(stems[0])
	}
	sort.Strings(stems)
	first, last := stems[0], stems[len(stems)-1]
	n := 0
	for n < len(first) && n < len(last) && first[n] == last[n] {
		n++
	}
	prefix := first[:n]
	if len(prefix) < 3 {
		return ""
	}
	return slug(prefix)
}

// slug lowercases and keeps only [a-z0-9-], collapsing other runs to nothing —
// safe for a set name (isSetName) and a directory component.
func slug(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// buildSetMeta assembles a pack's SetMeta from the per-cell header metadata and
// (optionally) the exchange-set catalogue. The catalogue supplies human chart
// titles (LFIL) and a coverage bbox even for cells whose header lacks M_COVR.
func buildSetMeta(set string, cellMeta map[string]baker.CellMeta, cat *s57.Catalog) SetMeta {
	// Catalogue overlay: stem → long name, stem → bbox.
	catTitle := map[string]string{}
	catBox := map[string][4]float64{}
	if cat != nil {
		for _, e := range cat.Cells() {
			stem := e.CellStem()
			if e.LongName != "" {
				catTitle[stem] = e.LongName
			}
			if e.HasBBox {
				catBox[stem] = [4]float64{e.West, e.South, e.East, e.North}
			}
		}
	}

	m := SetMeta{Set: set}
	agencyVotes := map[int]int{}
	var haveBox bool
	var bb [4]float64
	stems := make([]string, 0, len(cellMeta))
	for stem := range cellMeta {
		stems = append(stems, stem)
	}
	sort.Strings(stems)
	for _, stem := range stems {
		c := cellMeta[stem]
		if t := catTitle[stem]; t != "" {
			c.Title = t // prefer the catalogue's human name over the dataset name
		}
		if !c.HasBBox {
			if box, ok := catBox[stem]; ok {
				c.BBox, c.HasBBox = box, true
			}
		}
		if c.Scale > 0 {
			if m.ScaleMin == 0 || c.Scale < m.ScaleMin {
				m.ScaleMin = c.Scale
			}
			if c.Scale > m.ScaleMax {
				m.ScaleMax = c.Scale
			}
		}
		if c.Agency != 0 {
			agencyVotes[c.Agency]++
		}
		if c.HasBBox {
			if !haveBox {
				bb, haveBox = c.BBox, true
			} else {
				bb[0] = minF(bb[0], c.BBox[0])
				bb[1] = minF(bb[1], c.BBox[1])
				bb[2] = maxF(bb[2], c.BBox[2])
				bb[3] = maxF(bb[3], c.BBox[3])
			}
		}
		m.Cells = append(m.Cells, c)
	}
	m.CellCount = len(m.Cells)
	if haveBox {
		m.BBox = []float64{bb[0], bb[1], bb[2], bb[3]}
	}
	m.Agency = agencyName(topVote(agencyVotes))

	// Title: a single cell → its chart name; otherwise the most common catalogue
	// title if any cells share one, else the set name.
	switch {
	case len(m.Cells) == 1 && m.Cells[0].Title != "":
		m.Title = m.Cells[0].Title
	default:
		m.Title = set
	}
	return m
}

func topVote(votes map[int]int) int {
	best, bestN := 0, 0
	for k, n := range votes {
		if n > bestN {
			best, bestN = k, n
		}
	}
	return best
}

func minF(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func maxF(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

// writeSetMeta writes a pack's metadata sidecar into its setDir. Best-effort; a
// missing sidecar just means the library shows the pack without extracted detail.
func (s *Server) writeSetMeta(set string, m SetMeta) error {
	dir := s.setDir(set)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, set+setMetaExt), data, 0o644)
}

// readSetMeta loads a pack's metadata sidecar, or (nil, false) if absent/unreadable.
func (s *Server) readSetMeta(set string) (*SetMeta, bool) {
	data, err := os.ReadFile(filepath.Join(s.setDir(set), set+setMetaExt))
	if err != nil {
		return nil, false
	}
	var m SetMeta
	if json.Unmarshal(data, &m) != nil {
		return nil, false
	}
	return &m, true
}
