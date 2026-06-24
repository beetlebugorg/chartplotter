package portrayal

import (
	"testing"

	"github.com/beetlebugorg/chartplotter/pkg/geo"
)

// An L-shaped (concave) polygon: the plain vertex average / area centroid lands in
// the missing corner — outside the polygon. areaSurfacePoint must return a point
// that is actually inside.
func TestAreaSurfacePointConcave(t *testing.T) {
	// L-shape occupying the region minus the top-right quadrant.
	ring := []geo.LatLon{
		{Lat: 0, Lon: 0},
		{Lat: 0, Lon: 10},
		{Lat: 5, Lon: 10},
		{Lat: 5, Lon: 5},
		{Lat: 10, Lon: 5},
		{Lat: 10, Lon: 0},
	}
	p, ok := areaSurfacePoint(ring)
	if !ok {
		t.Fatal("areaSurfacePoint returned ok=false")
	}
	if !pointInRing(p, ring) {
		t.Fatalf("anchor %+v is outside the polygon", p)
	}

	// Sanity: the naive vertex average IS outside this shape (the bug we fixed).
	var sLat, sLon float64
	for _, v := range ring {
		sLat += v.Lat
		sLon += v.Lon
	}
	mean := geo.LatLon{Lat: sLat / float64(len(ring)), Lon: sLon / float64(len(ring))}
	if pointInRing(mean, ring) {
		t.Skip("vertex average happens to be inside; concavity assumption changed")
	}
}

// For a convex polygon the centroid is inside, so it should be returned as-is.
func TestAreaSurfacePointConvex(t *testing.T) {
	ring := []geo.LatLon{{Lat: 0, Lon: 0}, {Lat: 0, Lon: 4}, {Lat: 4, Lon: 4}, {Lat: 4, Lon: 0}}
	p, ok := areaSurfacePoint(ring)
	if !ok || !pointInRing(p, ring) {
		t.Fatalf("convex anchor %+v ok=%v not inside", p, ok)
	}
}
