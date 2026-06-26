package instructions

import "testing"

// TestAugmentedRayLengthCRS: the AugmentedRay length's CRS decides its unit.
// LocalCRS ⇒ display millimetres (the 25 mm short sector leg); GeographicCRS ⇒
// a fixed ground distance in metres (a sectorLineLength / full-VALNMR leg).
// Conflating them rendered geographic legs at metres-as-mm — ~10× too long.
func TestAugmentedRayLengthCRS(t *testing.T) {
	cases := []struct {
		in               string
		wantMM, wantGndM float64
	}{
		{"AugmentedRay:GeographicCRS,123.4,GeographicCRS,185.2;LineInstruction:_simple_", 0, 185.2},
		{"AugmentedRay:GeographicCRS,123.4,LocalCRS,25;LineInstruction:_simple_", 25, 0},
	}
	for _, c := range cases {
		cmds, _ := Reduce(ParseStream(c.in))
		var got *AugmentedGeom
		for i := range cmds {
			if cmds[i].Augmented != nil {
				got = cmds[i].Augmented
			}
		}
		if got == nil {
			t.Fatalf("%s: no augmented geom", c.in)
		}
		if got.LengthMM != c.wantMM || got.LengthGroundM != c.wantGndM {
			t.Errorf("%s: LengthMM=%v LengthGroundM=%v, want %v / %v", c.in, got.LengthMM, got.LengthGroundM, c.wantMM, c.wantGndM)
		}
	}
}
