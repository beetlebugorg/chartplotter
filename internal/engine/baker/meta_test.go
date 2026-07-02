package baker

import (
	"os"
	"testing"

	"github.com/beetlebugorg/chartplotter/pkg/s57"
)

func TestExtractCellMeta(t *testing.T) {
	data, err := os.ReadFile("../../../testdata/US5MD1MC.000")
	if err != nil {
		t.Fatal(err)
	}
	meta := ExtractCellMeta(map[string]CellData{"US5MD1MC.000": {Base: data}}, nil, nil)
	m, ok := meta["US5MD1MC"]
	if !ok {
		t.Fatalf("no metadata for US5MD1MC; got keys %v", keys(meta))
	}
	if m.Scale != 12000 {
		t.Errorf("Scale = %d, want 12000", m.Scale)
	}
	if m.Agency != 550 { // 550 = NOAA (US)
		t.Errorf("Agency = %d, want 550 (NOAA)", m.Agency)
	}
	if m.IssueDate == "" {
		t.Error("expected an issue date")
	}
	if !m.HasBBox {
		t.Error("expected a coverage bbox")
	}
	// Annapolis Harbor is around 38.9–39.0 N, -76.5–-76.4 W.
	if m.BBox[0] < -77 || m.BBox[0] > -76 || m.BBox[3] < 38 || m.BBox[3] > 40 {
		t.Errorf("bbox looks wrong: %v", m.BBox)
	}
}

// TestExtractCellMeta_CatalogFastPath proves the catalogue short-circuit: when the
// exchange-set catalogue already carries a (base) cell's coverage, identity is read
// from the cheap header and the bbox is taken verbatim from the catalogue — no
// M_COVR coverage parse. The stored bbox being the catalogue's exact rectangle (not
// the geometry-derived M_COVR extent) is what confirms the fast path engaged.
func TestExtractCellMeta_CatalogFastPath(t *testing.T) {
	data, err := os.ReadFile("../../../testdata/US5MD1MC.000")
	if err != nil {
		t.Fatal(err)
	}
	catData, err := os.ReadFile("../../../pkg/s57/testdata/US5MD1MC_CATALOG.031")
	if err != nil {
		t.Fatal(err)
	}
	cat, err := s57.ParseCatalog(catData)
	if err != nil {
		t.Fatal(err)
	}
	var catBox [4]float64
	for _, e := range cat.Cells() {
		if e.CellStem() == "US5MD1MC" && e.HasBBox {
			catBox = [4]float64{e.West, e.South, e.East, e.North}
		}
	}
	if catBox == ([4]float64{}) {
		t.Fatal("catalogue fixture lacks US5MD1MC coverage")
	}

	meta := ExtractCellMeta(map[string]CellData{"US5MD1MC.000": {Base: data}}, cat, nil)
	m, ok := meta["US5MD1MC"]
	if !ok {
		t.Fatalf("no metadata for US5MD1MC; got %v", keys(meta))
	}
	if m.Scale != 12000 || m.Agency != 550 {
		t.Errorf("identity = scale %d agency %d, want 12000 / 550", m.Scale, m.Agency)
	}
	if !m.HasBBox || m.BBox != catBox {
		t.Errorf("BBox = %v (has=%v), want catalogue box %v verbatim", m.BBox, m.HasBBox, catBox)
	}
}

func keys(m map[string]CellMeta) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
