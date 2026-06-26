package portrayal

import (
	"testing"

	"github.com/beetlebugorg/chartplotter/pkg/s57"
)

// TestNavSystemMarsysLine: an M_NSYS region with NO direction of buoyage (no
// ORIENT) draws its boundary with the MARSYS51 "A-B" line (DAI LU00344) and emits
// no buoyage arrow. A region WITH ORIENT draws the generic NAVARE51 triangle
// boundary plus a direction-of-buoyage arrow keyed to MARSYS (DAI LU00345-347).
func TestNavSystemMarsysLine(t *testing.T) {
	square := [][]float64{{0, 0}, {1, 0}, {1, 1}, {0, 1}, {0, 0}}
	nsys := func(attrs map[string]any) *s57.Feature {
		f := s57.NewFeature(0, "M_NSYS", s57.Geometry{
			Type:  s57.GeometryTypePolygon,
			Rings: []s57.Ring{{Usage: 1, Coordinates: square}},
		}, attrs)
		return &f
	}

	count := func(fb FeatureBuild) (marsys, navare, arrows int, arrow string) {
		for _, p := range fb.Primitives {
			switch v := p.(type) {
			case LinePattern:
				switch v.LinestyleName {
				case "MARSYS51":
					marsys++
				case "NAVARE51":
					navare++
				}
			case SymbolCall:
				arrows++
				arrow = v.SymbolName
			}
		}
		return
	}

	// No ORIENT → the A-B system boundary line, no arrow.
	marsys, navare, arrows, _ := count(navSystemBuild(nsys(map[string]any{"MARSYS": "1"})))
	if marsys == 0 {
		t.Error("a region without ORIENT should draw MARSYS51; got none")
	}
	if navare != 0 {
		t.Error("a region without ORIENT must not draw NAVARE51")
	}
	if arrows != 0 {
		t.Error("a region without ORIENT must not draw a buoyage arrow")
	}

	// ORIENT present → NAVARE51 boundary + a direction-of-buoyage arrow per MARSYS.
	for _, tc := range []struct {
		marsys string
		want   string
	}{
		{"1", "DIRBOYA1"}, // IALA-A
		{"2", "DIRBOYB1"}, // IALA-B
		{"10", "DIRBOY01"}, // other system
	} {
		marsys, navare, arrows, arrow := count(navSystemBuild(nsys(map[string]any{"MARSYS": tc.marsys, "ORIENT": 42.0})))
		if navare == 0 {
			t.Errorf("MARSYS %s with ORIENT should draw NAVARE51; got none", tc.marsys)
		}
		if marsys != 0 {
			t.Errorf("MARSYS %s with ORIENT must not draw MARSYS51", tc.marsys)
		}
		if arrows != 1 || arrow != tc.want {
			t.Errorf("MARSYS %s with ORIENT: want one %s arrow, got %d (%s)", tc.marsys, tc.want, arrows, arrow)
		}
	}
}
