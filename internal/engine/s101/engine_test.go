package s101

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/beetlebugorg/chartplotter/pkg/s100/fc"
)

// testEnv resolves the vendored catalogue + feature catalogue, or skips.
func testEnv(t *testing.T) (rulesDir string, cat *fc.Catalogue) {
	t.Helper()
	pc := os.Getenv("S101_CATALOG")
	if pc == "" {
		pc = "/home/jcollins/Projects/s101-portrayal-catalogue/PortrayalCatalog"
	}
	fcPath := os.Getenv("S101_FC")
	if fcPath == "" {
		fcPath = "/home/jcollins/Projects/s101-feature-catalogue/S-101FC/FeatureCatalogue.xml"
	}
	rulesDir = filepath.Join(pc, "Rules")
	if _, err := os.Stat(filepath.Join(rulesDir, "main.lua")); err != nil {
		t.Skipf("S-101 rules not present (%s); set S101_CATALOG/S101_FC to run", rulesDir)
	}
	if _, err := os.Stat(fcPath); err != nil {
		t.Skipf("S-101 feature catalogue not present (%s)", fcPath)
	}
	c, err := fc.Load(fcPath)
	if err != nil {
		t.Fatal(err)
	}
	return rulesDir, c
}

func portrayOne(t *testing.T, e *Engine, f Feature) string {
	t.Helper()
	res, err := e.Portray([]Feature{f})
	if err != nil {
		t.Fatal(err)
	}
	out := res[f.ID]
	if strings.HasPrefix(out, "ERROR:") {
		t.Fatalf("rule error: %s", out)
	}
	return out
}

// TestSiloTankAttributeDrivenSymbology proves the whole bridge + host path:
// an S-57 feature (acronyms) is adapted to S-101 and its rule branches on the
// translated attribute values to pick the right point symbol.
func TestSiloTankAttributeDrivenSymbology(t *testing.T) {
	rulesDir, cat := testEnv(t)
	e, err := NewEngine(rulesDir, cat)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()

	// CATSIL=3 (categoryOfSiloTank=tower), CONVIS=1 (visualProminence=prominent)
	// -> SiloTank rule emits PointInstruction:TOWERS03.
	out := portrayOne(t, e, Feature{
		ID: "f1", ObjectClass: "SILTNK", Primitive: "Point",
		Attributes: map[string]string{"CATSIL": "3", "CONVIS": "1"},
	})
	if !strings.Contains(out, "PointInstruction:TOWERS03") {
		t.Errorf("CATSIL=3,CONVIS=1: want TOWERS03, got %q", out)
	}

	// CATSIL=2, no CONVIS -> categoryOfSiloTank==2 branch -> TNKCON02.
	out = portrayOne(t, e, Feature{
		ID: "f2", ObjectClass: "SILTNK", Primitive: "Point",
		Attributes: map[string]string{"CATSIL": "2"},
	})
	if !strings.Contains(out, "PointInstruction:TNKCON02") {
		t.Errorf("CATSIL=2: want TNKCON02, got %q", out)
	}

	// Surface primitive -> ColorFill:CHBRN + a boundary line.
	out = portrayOne(t, e, Feature{
		ID: "f3", ObjectClass: "SILTNK", Primitive: "Surface",
		Attributes: map[string]string{"CATSIL": "1"},
	})
	if !strings.Contains(out, "ColorFill:CHBRN") {
		t.Errorf("surface: want ColorFill:CHBRN, got %q", out)
	}
}

func TestUnmappedObjectClassMarked(t *testing.T) {
	rulesDir, cat := testEnv(t)
	e, err := NewEngine(rulesDir, cat)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()

	res, err := e.Portray([]Feature{{ID: "x", ObjectClass: "ZZZZZZ", Primitive: "Point"}})
	if err != nil {
		t.Fatal(err)
	}
	if res["x"] != "UNMAPPED:ZZZZZZ" {
		t.Errorf("want UNMAPPED marker, got %q", res["x"])
	}
}
