package portrayal

import (
	"testing"

	"github.com/beetlebugorg/chartplotter/pkg/s57"
)

// TestInformSymbolAdditional: an object carrying INFORM gets SY(INFORM01) appended
// (S-52 §10.6.1.1); one without does not.
func TestInformSymbolAdditional(t *testing.T) {
	pt := s57.Geometry{Type: s57.GeometryTypePoint, Coordinates: [][]float64{{-5.1, 15.1}}}
	countInform := func(fb FeatureBuild) int {
		n := 0
		for _, p := range fb.Primitives {
			if sc, ok := p.(SymbolCall); ok && sc.SymbolName == "INFORM01" {
				n++
			}
		}
		return n
	}

	withInfo := s57.NewFeature(1, "BOYLAT", pt, map[string]any{"INFORM": "lit by night"})
	if got := countInform(addInformSymbol(FeatureBuild{}, &withInfo)); got != 1 {
		t.Errorf("INFORM-bearing feature: INFORM01 count = %d, want 1", got)
	}

	noInfo := s57.NewFeature(2, "BOYLAT", pt, map[string]any{"COLOUR": "3"})
	if got := countInform(addInformSymbol(FeatureBuild{}, &noInfo)); got != 0 {
		t.Errorf("plain feature: INFORM01 count = %d, want 0", got)
	}

	// TXTDSC also qualifies (case 2 of §10.6.1.1).
	txt := s57.NewFeature(3, "WRECKS", pt, map[string]any{"TXTDSC": "wreck.txt"})
	if got := countInform(addInformSymbol(FeatureBuild{}, &txt)); got != 1 {
		t.Errorf("TXTDSC-bearing feature: INFORM01 count = %d, want 1", got)
	}
}
