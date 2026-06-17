// Package geo holds the shared geographic and screen primitives every other
// package speaks. Two point types are kept deliberately: LatLon is a WGS84
// world position (float64), Point is a projected screen position (float32).
// Collapsing them would silently conflate "where on Earth" with "where on the
// page" and change precision, so they stay distinct.
package geo

import "math"

// LatLon is a WGS84 geographic position in degrees.
type LatLon struct {
	Lat float64
	Lon float64
}

// Order compares two positions lat-major, then lon. Returns -1, 0, or 1.
func (a LatLon) Order(b LatLon) int {
	if a.Lat != b.Lat {
		return cmp(a.Lat, b.Lat)
	}
	return cmp(a.Lon, b.Lon)
}

func cmp(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

// Point is a projected screen position in page/pixel units.
type Point struct {
	X float32
	Y float32
}

// BoundingBox is a geographic bounding box in degrees.
type BoundingBox struct {
	MinLat float64
	MinLon float64
	MaxLat float64
	MaxLon float64
}

// EmptyBox returns a box that contains nothing; the first ExtendPoint snaps it
// to that point.
func EmptyBox() BoundingBox {
	return BoundingBox{
		MinLat: math.Inf(1),
		MinLon: math.Inf(1),
		MaxLat: math.Inf(-1),
		MaxLon: math.Inf(-1),
	}
}

// ExtendPoint grows the box to include p.
func (b *BoundingBox) ExtendPoint(p LatLon) {
	if p.Lat < b.MinLat {
		b.MinLat = p.Lat
	}
	if p.Lon < b.MinLon {
		b.MinLon = p.Lon
	}
	if p.Lat > b.MaxLat {
		b.MaxLat = p.Lat
	}
	if p.Lon > b.MaxLon {
		b.MaxLon = p.Lon
	}
}

// ExtendBox grows the box to include other.
func (b *BoundingBox) ExtendBox(other BoundingBox) {
	if other.MinLat < b.MinLat {
		b.MinLat = other.MinLat
	}
	if other.MinLon < b.MinLon {
		b.MinLon = other.MinLon
	}
	if other.MaxLat > b.MaxLat {
		b.MaxLat = other.MaxLat
	}
	if other.MaxLon > b.MaxLon {
		b.MaxLon = other.MaxLon
	}
}

// Contains reports whether p lies within the box (inclusive).
func (b BoundingBox) Contains(p LatLon) bool {
	return p.Lat >= b.MinLat && p.Lat <= b.MaxLat &&
		p.Lon >= b.MinLon && p.Lon <= b.MaxLon
}

// Intersects reports whether the two boxes overlap.
func (b BoundingBox) Intersects(other BoundingBox) bool {
	return b.MinLat <= other.MaxLat && b.MaxLat >= other.MinLat &&
		b.MinLon <= other.MaxLon && b.MaxLon >= other.MinLon
}
