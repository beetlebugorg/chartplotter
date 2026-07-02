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
