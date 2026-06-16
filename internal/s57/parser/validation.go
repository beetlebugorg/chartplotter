package parser

import (
	"fmt"
)

// ValidateCoordinate validates a single coordinate pair
// S-57 coordinates must be within valid geographic bounds
func ValidateCoordinate(lat, lon float64) error {
	if lat < -90.0 || lat > 90.0 {
		return &ErrInvalidCoordinate{Lat: lat, Lon: lon}
	}
	if lon < -180.0 || lon > 180.0 {
		return &ErrInvalidCoordinate{Lat: lat, Lon: lon}
	}
	return nil
}

// ValidateGeometry validates geometry per S-57 spatial rules
// S-57 §7.3: Geometry validation rules
func ValidateGeometry(geometry *Geometry) error {
	if geometry == nil {
		return &ErrInvalidGeometry{Reason: "geometry is nil"}
	}

	// Allow empty coordinates for meta-features (PRIM=255) like C_AGGR, M_COVR
	// These features have no spatial representation
	if len(geometry.Coordinates) == 0 {
		return nil // Empty geometry is valid for meta-features
	}

	// DEBUG: Log geometry details
	// fmt.Fprintf(os.Stderr, "Validating %s with %d coords\n", geometry.Type, len(geometry.Coordinates))

	// Validate coordinate count based on geometry type
	switch geometry.Type {
	case GeometryTypePoint:
		// Point geometry can have 1 coordinate (simple point) or many (multipoint)
		// Multipoint features like SOUNDG can have hundreds of coordinates
		// Allow empty points - they will be skipped during rendering

	case GeometryTypeLineString:
		// Allow degenerate lines - they will be skipped during rendering

	case GeometryTypePolygon:
		// Allow degenerate polygons - they will be skipped during rendering
		// Validation of polygon closure is done during geometry construction
	}

	// Validate each coordinate (2D or 3D)
	// S-57 soundings (SOUNDG) use 3D coordinates [lon, lat, depth]
	for i, coord := range geometry.Coordinates {
		if len(coord) < 2 || len(coord) > 3 {
			return &ErrInvalidGeometry{
				Type:   geometry.Type,
				Reason: fmt.Sprintf("coordinate %d must have 2 or 3 values [lon, lat] or [lon, lat, depth], got %d", i, len(coord)),
			}
		}
		lon, lat := coord[0], coord[1]
		if err := ValidateCoordinate(lat, lon); err != nil {
			return &ErrInvalidGeometry{
				Type:   geometry.Type,
				Reason: fmt.Sprintf("coordinate %d invalid: %v", i, err),
			}
		}
		// Note: depth (Z) coordinate is not validated - can be any value including negative
	}

	return nil
}

// ValidateFeature validates a feature per S-57 rules
func ValidateFeature(feature *Feature) error {
	if feature == nil {
		return fmt.Errorf("feature is nil")
	}

	if feature.ObjectClass == "" {
		return fmt.Errorf("feature has empty object class")
	}

	if err := ValidateGeometry(&feature.Geometry); err != nil {
		return fmt.Errorf("feature %d: %w", feature.ID, err)
	}

	return nil
}
