package s101

import (
	"strings"
	"testing"
)

// TestCoLocatedLightsStack proves HostSpatialGetAssociatedFeatureIDs: two LIGHTS
// features sharing a node are reported as co-located, so the catalogue's
// LightFlareAndDescription rule stacks the second description via
// GetColocatedTextCount -> TextVerticalOffset. A control pair at distinct points
// must NOT stack — confirming the offset comes from real co-location, not every
// light.
func TestCoLocatedLightsStack(t *testing.T) {
	rulesDir, cat := testEnv(t)

	light := func(id string, pt [3]float64, colour string) Feature {
		return Feature{
			ID: id, ObjectClass: "LIGHTS", Primitive: "Point",
			Points:     [][3]float64{pt},
			Attributes: map[string]string{"COLOUR": colour, "LITCHR": "2", "VALNMR": "4", "SIGPER": "4"},
		}
	}
	countOffsets := func(t *testing.T, feats []Feature) int {
		e, err := NewEngine(rulesDir, cat)
		if err != nil {
			t.Fatal(err)
		}
		defer e.Close()
		res, err := e.Portray(feats)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, f := range feats {
			if strings.HasPrefix(res[f.ID], "ERROR:") {
				t.Fatalf("rule error for %s: %s", f.ID, res[f.ID])
			}
			if strings.Contains(res[f.ID], "TextVerticalOffset") {
				n++
			}
		}
		return n
	}

	same := [3]float64{-122.4, 45.5, 0}
	if n := countOffsets(t, []Feature{light("a", same, "3"), light("b", same, "4")}); n != 1 {
		t.Errorf("co-located pair: want exactly one stacked (offset) description, got %d", n)
	}

	apart := [3]float64{-122.3, 45.6, 0}
	if n := countOffsets(t, []Feature{light("a", same, "3"), light("b", apart, "4")}); n != 0 {
		t.Errorf("lights at distinct points must not stack, got %d offset(s)", n)
	}
}
