package baker

import (
	"os"
	"testing"
)

func TestExtractCellMeta(t *testing.T) {
	data, err := os.ReadFile("../../../testdata/US5MD1MC.000")
	if err != nil {
		t.Fatal(err)
	}
	meta := ExtractCellMeta(map[string]CellData{"US5MD1MC.000": {Base: data}}, nil)
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

func keys(m map[string]CellMeta) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
