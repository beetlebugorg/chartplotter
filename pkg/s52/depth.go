package s52

// Depth unit conversion utilities for soundings and depth values.
//
// Standard conversions:
//   - 1 meter = 3.28084 feet
//   - 1 fathom = 6 feet = 1.8288 meters
//
// S-57 ENC data typically stores depths in meters.

const (
	metersToFeet    = 3.28084
	feetToMeters    = 1.0 / metersToFeet
	metersToFathoms = 1.0 / 1.8288
	fathomsToMeters = 1.8288
	feetToFathoms   = 1.0 / 6.0
	fathomsToFeet   = 6.0
)

// ConvertDepth converts a depth value from one unit to another
func ConvertDepth(value float64, from, to DepthUnit) float64 {
	if from == to {
		return value
	}

	// Convert to meters first (canonical unit)
	var meters float64
	switch from {
	case DepthUnitMeters:
		meters = value
	case DepthUnitFeet:
		meters = value * feetToMeters
	case DepthUnitFathoms:
		meters = value * fathomsToMeters
	default:
		return value // Unknown unit, return as-is
	}

	// Convert from meters to target unit
	switch to {
	case DepthUnitMeters:
		return meters
	case DepthUnitFeet:
		return meters * metersToFeet
	case DepthUnitFathoms:
		return meters * metersToFathoms
	default:
		return meters // Unknown unit, return meters
	}
}

// MetersToFeet converts meters to feet
func MetersToFeet(meters float64) float64 {
	return meters * metersToFeet
}

// FeetToMeters converts feet to meters
func FeetToMeters(feet float64) float64 {
	return feet * feetToMeters
}

// MetersToFathoms converts meters to fathoms
func MetersToFathoms(meters float64) float64 {
	return meters * metersToFathoms
}

// FathomsToMeters converts fathoms to meters
func FathomsToMeters(fathoms float64) float64 {
	return fathoms * fathomsToMeters
}

// FeetToFathoms converts feet to fathoms
func FeetToFathoms(feet float64) float64 {
	return feet * feetToFathoms
}

// FathomsToFeet converts fathoms to feet
func FathomsToFeet(fathoms float64) float64 {
	return fathoms * fathomsToFeet
}
