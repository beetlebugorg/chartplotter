package bake

import "testing"

// pointInRings: even-odd across rings, so a point inside a hole reads as outside.
func TestPointInRings(t *testing.T) {
	// 0..10 square with a 4..6 square hole.
	outer := [][]float64{{0, 0}, {10, 0}, {10, 10}, {0, 10}, {0, 0}}
	hole := [][]float64{{4, 4}, {6, 4}, {6, 6}, {4, 6}, {4, 4}}
	rings := [][][]float64{outer, hole}

	cases := []struct {
		lon, lat float64
		want     bool
	}{
		{2, 2, true},   // inside, outside the hole
		{5, 5, false},  // inside the hole → outside the area
		{12, 5, false}, // outside entirely
	}
	for _, c := range cases {
		if got := pointInRings(c.lon, c.lat, rings); got != c.want {
			t.Errorf("pointInRings(%v,%v) = %v, want %v", c.lon, c.lat, got, c.want)
		}
	}
}

// underlyingAt returns the depth areas (with DRVAL1) containing a point.
func TestDepthIndexUnderlyingAt(t *testing.T) {
	idx := &depthIndex{areas: []depthArea{{
		class:  "DEPARE",
		attrs:  map[string]interface{}{"DRVAL1": 35.0},
		rings:  [][][]float64{{{0, 0}, {10, 0}, {10, 10}, {0, 10}, {0, 0}}},
		minLon: 0, minLat: 0, maxLon: 10, maxLat: 10,
	}}}

	in := idx.underlyingAt(5, 5)
	if len(in) != 1 || in[0].ObjectClass != "DEPARE" {
		t.Fatalf("expected one DEPARE underlying, got %+v", in)
	}
	if got := in[0].Attributes["DRVAL1"]; got != 35.0 {
		t.Errorf("DRVAL1 = %v, want 35", got)
	}
	if out := idx.underlyingAt(50, 50); out != nil {
		t.Errorf("expected nil outside all areas, got %+v", out)
	}
}
