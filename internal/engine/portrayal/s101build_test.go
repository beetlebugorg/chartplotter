package portrayal

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/beetlebugorg/chartplotter/pkg/s57"
)

func s101Builder(t *testing.T) *S101Builder {
	t.Helper()
	pc := os.Getenv("S101_CATALOG")
	if pc == "" {
		pc = "/home/jcollins/Projects/s101-portrayal-catalogue/PortrayalCatalog"
	}
	fcPath := os.Getenv("S101_FC")
	if fcPath == "" {
		fcPath = "/home/jcollins/Projects/s101-feature-catalogue/S-101FC/FeatureCatalogue.xml"
	}
	if _, err := os.Stat(filepath.Join(pc, "Rules", "main.lua")); err != nil {
		t.Skipf("S-101 catalogue not present; set S101_CATALOG/S101_FC")
	}
	if _, err := os.Stat(fcPath); err != nil {
		t.Skipf("S-101 feature catalogue not present")
	}
	b, err := NewS101Builder(pc, fcPath)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestS101BuildPointSymbol drives a real S-57 feature through the full cutover
// seam: S-57 acronyms → S-101 rule → instructions → geometry-placed Primitive.
func TestS101BuildPointSymbol(t *testing.T) {
	b := s101Builder(t)

	pt := s57.NewFeature(1, "SILTNK",
		s57.Geometry{Type: s57.GeometryTypePoint, Coordinates: [][]float64{{12.5, 55.7}}},
		map[string]interface{}{"CATSIL": 3, "CONVIS": 1},
	)
	build, ok := b.Build(&pt)
	if !ok {
		t.Fatal("build failed")
	}
	var sym *SymbolCall
	for i := range build.Primitives {
		if sc, ok := build.Primitives[i].(SymbolCall); ok {
			sym = &sc
			break
		}
	}
	if sym == nil {
		t.Fatalf("no SymbolCall emitted; got %#v", build.Primitives)
	}
	if sym.SymbolName != "TOWERS03" {
		t.Errorf("symbol = %q, want TOWERS03", sym.SymbolName)
	}
	if sym.Anchor.Lat != 55.7 || sym.Anchor.Lon != 12.5 {
		t.Errorf("anchor = %+v, want {55.7,12.5}", sym.Anchor)
	}
}

// TestS101BuildAreaFillAndLine drives a polygon feature; the SiloTank surface
// branch emits ColorFill:CHBRN + a boundary line, lowered onto the rings.
func TestS101BuildAreaFillAndLine(t *testing.T) {
	b := s101Builder(t)
	ring := [][]float64{{0, 0}, {0, 1}, {1, 1}, {1, 0}, {0, 0}}
	poly := s57.NewFeature(2, "SILTNK",
		s57.Geometry{Type: s57.GeometryTypePolygon, Coordinates: ring},
		map[string]interface{}{"CATSIL": 1},
	)
	build, ok := b.Build(&poly)
	if !ok {
		t.Fatal("build failed")
	}
	var fill *FillPolygon
	for i := range build.Primitives {
		if fp, ok := build.Primitives[i].(FillPolygon); ok {
			fill = &fp
			break
		}
	}
	if fill == nil || fill.ColorToken != "CHBRN" {
		t.Fatalf("want FillPolygon CHBRN, got %#v", build.Primitives)
	}
	if len(fill.Rings) == 0 || len(fill.Rings[0]) == 0 {
		t.Errorf("fill not lowered onto geometry: %+v", fill.Rings)
	}
}

// TestS101BuildUnknownClassPlaceholder: an object class with no S-101 alias
// renders the QUESMRK1 placeholder rather than vanishing.
func TestS101BuildUnknownClassPlaceholder(t *testing.T) {
	b := s101Builder(t)
	f := s57.NewFeature(3, "ZZZZZZ",
		s57.Geometry{Type: s57.GeometryTypePoint, Coordinates: [][]float64{{1, 2}}}, nil)
	build, ok := b.Build(&f)
	if !ok {
		t.Fatal("build should succeed with placeholder")
	}
	if len(build.Primitives) != 1 {
		t.Fatalf("want 1 placeholder primitive, got %d", len(build.Primitives))
	}
	if sc, ok := build.Primitives[0].(SymbolCall); !ok || sc.SymbolName != "QUESMRK1" {
		t.Errorf("want QUESMRK1 placeholder, got %#v", build.Primitives[0])
	}
}
