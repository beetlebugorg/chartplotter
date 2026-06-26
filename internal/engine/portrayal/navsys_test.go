package portrayal

import (
	"testing"

	"github.com/beetlebugorg/chartplotter/pkg/geo"
	"github.com/beetlebugorg/chartplotter/pkg/s57"
)

// TestNavSystemMarsysTransition: an M_NSYS IALA-A region and an IALA-B region
// that share a boundary edge draw that shared edge with the MARSYS51 "A-B" line
// (S-52 "boundary between IALA-A and IALA-B systems"), while their other edges
// use the generic NAVARE51 triangle boundary.
func TestNavSystemMarsysTransition(t *testing.T) {
	poly := func(coords [][]float64, marsys string) *s57.Feature {
		f := s57.NewFeature(0, "M_NSYS", s57.Geometry{
			Type:  s57.GeometryTypePolygon,
			Rings: []s57.Ring{{Usage: 1, Coordinates: coords}},
		}, map[string]any{"MARSYS": marsys})
		return &f
	}
	// Two unit squares sharing the lon=1 edge: A on the left, B on the right.
	a := poly([][]float64{{0, 0}, {1, 0}, {1, 1}, {0, 1}, {0, 0}}, "1") // IALA-A
	b := poly([][]float64{{1, 0}, {2, 0}, {2, 1}, {1, 1}, {1, 0}}, "2") // IALA-B

	idx := buildNsysIndex([]*s57.Feature{a, b})
	if !idx.isTransition(geo.LatLon{Lat: 0, Lon: 1}, geo.LatLon{Lat: 1, Lon: 1}) {
		t.Fatal("the shared lon=1 edge should be an A-B transition")
	}
	if idx.isTransition(geo.LatLon{Lat: 0, Lon: 0}, geo.LatLon{Lat: 0, Lon: 1}) {
		t.Fatal("a non-shared edge must not be a transition")
	}

	var marsys, navare int
	for _, p := range navSystemBuild(a, idx).Primitives {
		if lp, ok := p.(LinePattern); ok {
			switch lp.LinestyleName {
			case "MARSYS51":
				marsys++
			case "NAVARE51":
				navare++
			}
		}
	}
	if marsys == 0 {
		t.Error("shared IALA-A/B edge should draw MARSYS51; got none")
	}
	if navare == 0 {
		t.Error("non-shared edges should draw NAVARE51; got none")
	}
}
