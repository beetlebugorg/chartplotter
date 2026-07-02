package s57

import (
	"os"
	"testing"
)

func TestParseCatalog_NOAA(t *testing.T) {
	data, err := os.ReadFile("testdata/US5MD1MC_CATALOG.031")
	if err != nil {
		t.Fatal(err)
	}
	cat, err := ParseCatalog(data)
	if err != nil {
		t.Fatal(err)
	}

	// The fixture is a single-cell NOAA exchange set: the catalogue itself,
	// several .TXT descriptions, and one .000 base cell.
	cells := cat.Cells()
	if len(cells) != 1 {
		t.Fatalf("want 1 base cell, got %d (%d total entries)", len(cells), len(cat.Entries))
	}
	c := cells[0]
	if c.CellStem() != "US5MD1MC" {
		t.Errorf("CellStem = %q, want US5MD1MC", c.CellStem())
	}
	if c.LongName != "Annapolis Harbor" {
		t.Errorf("LongName = %q, want %q", c.LongName, "Annapolis Harbor")
	}
	if c.Impl != "BIN" {
		t.Errorf("Impl = %q, want BIN", c.Impl)
	}
	if !c.HasBBox {
		t.Fatal("cell entry should carry a bbox")
	}
	// From the CATD record: SLAT 38.925, WLON -76.5, NLAT 39.0, ELON -76.425.
	wantBox := []struct {
		name string
		got  float64
		want float64
	}{
		{"South", c.South, 38.925000},
		{"West", c.West, -76.500000},
		{"North", c.North, 39.000000},
		{"East", c.East, -76.425000},
	}
	for _, b := range wantBox {
		if d := b.got - b.want; d > 1e-6 || d < -1e-6 {
			t.Errorf("%s = %f, want %f", b.name, b.got, b.want)
		}
	}
	// The auxiliary text descriptions are present but are NOT cells.
	var txt int
	for _, e := range cat.Entries {
		if e.Impl == "TXT" {
			txt++
			if e.HasBBox {
				t.Errorf("TXT entry %s should have no bbox", e.Base())
			}
		}
	}
	if txt == 0 {
		t.Error("expected at least one TXT auxiliary entry")
	}
}
