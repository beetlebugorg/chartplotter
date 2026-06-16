package geo

import "testing"

func TestBoundingBoxExtendsAndIntersects(t *testing.T) {
	b := EmptyBox()
	b.ExtendPoint(LatLon{Lat: 42.0, Lon: -71.0})
	b.ExtendPoint(LatLon{Lat: 42.5, Lon: -70.5})
	if b.MinLat != 42.0 {
		t.Fatalf("MinLat = %v, want 42.0", b.MinLat)
	}
	if b.MaxLon != -70.5 {
		t.Fatalf("MaxLon = %v, want -70.5", b.MaxLon)
	}
	if !b.Contains(LatLon{Lat: 42.25, Lon: -70.75}) {
		t.Fatal("expected box to contain interior point")
	}
	if b.Contains(LatLon{Lat: 41.9, Lon: -71.5}) {
		t.Fatal("expected box not to contain exterior point")
	}
	other := BoundingBox{MinLat: 42.4, MinLon: -70.7, MaxLat: 43.0, MaxLon: -70.0}
	if !b.Intersects(other) {
		t.Fatal("expected boxes to intersect")
	}
}

func TestLatLonOrder(t *testing.T) {
	a := LatLon{Lat: 1.0, Lon: 2.0}
	b := LatLon{Lat: 1.0, Lon: 3.0}
	if got := a.Order(b); got != -1 {
		t.Fatalf("a.Order(b) = %d, want -1", got)
	}
	if got := a.Order(a); got != 0 {
		t.Fatalf("a.Order(a) = %d, want 0", got)
	}
}
