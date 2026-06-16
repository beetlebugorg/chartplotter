package s57

// Bounds represents a geographic bounding box in WGS-84 coordinates.
//
// Coordinates are in decimal degrees.
type Bounds struct {
	MinLon float64 // Western edge
	MaxLon float64 // Eastern edge
	MinLat float64 // Southern edge
	MaxLat float64 // Northern edge
}

// Contains returns true if the point (lon, lat) is within the bounds.
func (b Bounds) Contains(lon, lat float64) bool {
	return lon >= b.MinLon && lon <= b.MaxLon &&
		lat >= b.MinLat && lat <= b.MaxLat
}

// Intersects returns true if the given bounds intersects with this bounds.
func (b Bounds) Intersects(other Bounds) bool {
	return !(other.MaxLon < b.MinLon ||
		other.MinLon > b.MaxLon ||
		other.MaxLat < b.MinLat ||
		other.MinLat > b.MaxLat)
}

// Expand returns a new Bounds expanded by the given margin in all directions.
//
// Margin is in decimal degrees.
func (b Bounds) Expand(margin float64) Bounds {
	return Bounds{
		MinLon: b.MinLon - margin,
		MaxLon: b.MaxLon + margin,
		MinLat: b.MinLat - margin,
		MaxLat: b.MaxLat + margin,
	}
}

// Union returns a new Bounds that encompasses both this bounds and the other.
//
// The resulting bounds will be the smallest bounding box that contains both inputs.
func (b Bounds) Union(other Bounds) Bounds {
	result := b

	if other.MinLon < result.MinLon {
		result.MinLon = other.MinLon
	}
	if other.MaxLon > result.MaxLon {
		result.MaxLon = other.MaxLon
	}
	if other.MinLat < result.MinLat {
		result.MinLat = other.MinLat
	}
	if other.MaxLat > result.MaxLat {
		result.MaxLat = other.MaxLat
	}

	return result
}

// featureBounds calculates the bounding box for a feature's geometry.
func featureBounds(f Feature) Bounds {
	if len(f.geometry.Coordinates) == 0 {
		return Bounds{}
	}

	// Initialize with first coordinate
	first := f.geometry.Coordinates[0]
	bounds := Bounds{
		MinLon: first[0],
		MaxLon: first[0],
		MinLat: first[1],
		MaxLat: first[1],
	}

	// Expand to include all coordinates
	for _, coord := range f.geometry.Coordinates {
		lon, lat := coord[0], coord[1]
		if lon < bounds.MinLon {
			bounds.MinLon = lon
		}
		if lon > bounds.MaxLon {
			bounds.MaxLon = lon
		}
		if lat < bounds.MinLat {
			bounds.MinLat = lat
		}
		if lat > bounds.MaxLat {
			bounds.MaxLat = lat
		}
	}

	return bounds
}
