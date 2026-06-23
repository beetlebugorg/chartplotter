package catalog

import (
	"os"
	"path/filepath"
	"testing"
)

// catalogDir resolves the vendored S-101 PortrayalCatalog, or skips the test.
// Override with S101_CATALOG=/path/to/PortrayalCatalog.
func catalogDir(t *testing.T) string {
	t.Helper()
	dir := os.Getenv("S101_CATALOG")
	if dir == "" {
		dir = "/home/jcollins/Projects/s101-portrayal-catalogue/PortrayalCatalog"
	}
	if _, err := os.Stat(filepath.Join(dir, "LineStyles")); err != nil {
		t.Skipf("S-101 catalogue not present (%s); set S101_CATALOG to run", dir)
	}
	return dir
}

func TestLoadLineStyleACHARE51(t *testing.T) {
	dir := catalogDir(t)
	ls, err := LoadLineStyle(filepath.Join(dir, "LineStyles", "ACHARE51.xml"))
	if err != nil {
		t.Fatal(err)
	}
	if ls.ID != "ACHARE51" {
		t.Errorf("ID = %q", ls.ID)
	}
	if ls.IntervalLength != 32.3 || ls.PenWidth != 0.32 || ls.PenColor != "CHMGD" {
		t.Errorf("pen/interval wrong: %+v", ls)
	}
	if len(ls.Dashes) != 3 || ls.Dashes[0] != (Dash{Start: 2, Length: 6}) {
		t.Errorf("dashes wrong: %+v", ls.Dashes)
	}
	if len(ls.Symbols) != 4 || ls.Symbols[0].Reference != "EMAREMG1" || ls.Symbols[0].Position != 5 {
		t.Errorf("symbols wrong: %+v", ls.Symbols)
	}
}

func TestLoadAreaFillDIAMOND1(t *testing.T) {
	dir := catalogDir(t)
	af, err := LoadAreaFill(filepath.Join(dir, "AreaFills", "DIAMOND1.xml"))
	if err != nil {
		t.Fatal(err)
	}
	if af.ID != "DIAMOND1" || af.SymbolRef != "DIAMOND1P" || af.CRS != "GlobalGeometry" {
		t.Errorf("areafill wrong: %+v", af)
	}
	if af.V1 != (Vec{X: 22.5, Y: 0}) || af.V2 != (Vec{X: 0, Y: 43.13}) {
		t.Errorf("basis vectors wrong: %+v %+v", af.V1, af.V2)
	}
}

func TestLoadCompositeLineStyle(t *testing.T) {
	dir := catalogDir(t)
	ls, err := LoadLineStyle(filepath.Join(dir, "LineStyles", "SCLBDY51.xml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(ls.Components) != 3 {
		t.Fatalf("want 3 components, got %d", len(ls.Components))
	}
	c0 := ls.Components[0]
	if c0.Offset != -1.0 || c0.PenWidth != 0.96 || c0.PenColor != "CHGRF" || len(c0.Dashes) != 1 {
		t.Errorf("component 0 wrong: %+v", c0)
	}
}

func TestLoadColorProfileAndCatalog(t *testing.T) {
	dir := catalogDir(t)
	cat, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cat.LineStyles) == 0 || len(cat.AreaFills) == 0 {
		t.Fatalf("catalog empty: %d lines, %d fills", len(cat.LineStyles), len(cat.AreaFills))
	}
	// NODTA day sRGB is 147,174,187 (see cmd/s101-color-diff).
	if got := cat.Colors.Day["NODTA"]; got != (RGB{147, 174, 187}) {
		t.Errorf("NODTA day = %+v, want {147 174 187}", got)
	}
	if got := cat.Colors.For("night")["CHBLK"]; got == (RGB{}) && len(cat.Colors.Night) == 0 {
		t.Errorf("night palette empty")
	}
	if len(cat.Colors.Day) != len(cat.Colors.Night) || len(cat.Colors.Day) == 0 {
		t.Errorf("palette sizes off: day=%d night=%d", len(cat.Colors.Day), len(cat.Colors.Night))
	}
}

// TestLoadAllParse loads the whole catalogue and reports any definitions that
// came out empty — those are gaps (e.g. a non-symbolFill area-fill variant) to
// triage, matching the project's surface-the-gaps approach.
func TestLoadAllParse(t *testing.T) {
	dir := catalogDir(t)

	lines, err := LoadLineStyles(filepath.Join(dir, "LineStyles"))
	if err != nil {
		t.Fatal(err)
	}
	fills, err := LoadAreaFills(filepath.Join(dir, "AreaFills"))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("loaded %d line styles, %d area fills", len(lines), len(fills))

	for id, ls := range lines {
		if ls.IntervalLength == 0 && len(ls.Dashes) == 0 && len(ls.Symbols) == 0 && ls.PenColor == "" && len(ls.Components) == 0 {
			t.Errorf("line style %s parsed empty", id)
		}
	}
	for id, af := range fills {
		if af.SymbolRef == "" {
			t.Logf("area fill %s has no symbol reference (non-symbolFill variant?) — triage", id)
		}
	}
}
