package s52

import (
	"math"
	"testing"
)

const epsilon = 0.0001 // Tolerance for floating point comparisons

func TestDepthUnitString(t *testing.T) {
	tests := []struct {
		unit     DepthUnit
		expected string
	}{
		{DepthUnitMeters, "meters"},
		{DepthUnitFeet, "feet"},
		{DepthUnitFathoms, "fathoms"},
		{DepthUnit(999), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if got := tt.unit.String(); got != tt.expected {
				t.Errorf("DepthUnit.String() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestDepthUnitAbbreviation(t *testing.T) {
	tests := []struct {
		unit     DepthUnit
		expected string
	}{
		{DepthUnitMeters, "m"},
		{DepthUnitFeet, "ft"},
		{DepthUnitFathoms, "fms"},
		{DepthUnit(999), ""},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if got := tt.unit.Abbreviation(); got != tt.expected {
				t.Errorf("DepthUnit.Abbreviation() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestConvertDepth(t *testing.T) {
	tests := []struct {
		name     string
		value    float64
		from     DepthUnit
		to       DepthUnit
		expected float64
	}{
		// Same unit (no conversion)
		{"meters to meters", 10.0, DepthUnitMeters, DepthUnitMeters, 10.0},
		{"feet to feet", 100.0, DepthUnitFeet, DepthUnitFeet, 100.0},
		{"fathoms to fathoms", 5.0, DepthUnitFathoms, DepthUnitFathoms, 5.0},

		// Meters to other units
		{"meters to feet", 1.0, DepthUnitMeters, DepthUnitFeet, 3.28084},
		{"meters to fathoms", 1.8288, DepthUnitMeters, DepthUnitFathoms, 1.0},
		{"10 meters to feet", 10.0, DepthUnitMeters, DepthUnitFeet, 32.8084},

		// Feet to other units
		{"feet to meters", 3.28084, DepthUnitFeet, DepthUnitMeters, 1.0},
		{"feet to fathoms", 6.0, DepthUnitFeet, DepthUnitFathoms, 1.0},
		{"100 feet to meters", 100.0, DepthUnitFeet, DepthUnitMeters, 30.48},

		// Fathoms to other units
		{"fathoms to meters", 1.0, DepthUnitFathoms, DepthUnitMeters, 1.8288},
		{"fathoms to feet", 1.0, DepthUnitFathoms, DepthUnitFeet, 6.0},
		{"10 fathoms to meters", 10.0, DepthUnitFathoms, DepthUnitMeters, 18.288},

		// Zero depth
		{"zero meters", 0.0, DepthUnitMeters, DepthUnitFeet, 0.0},
		{"zero feet", 0.0, DepthUnitFeet, DepthUnitMeters, 0.0},

		// Negative depth (e.g., for drying heights)
		{"negative meters to feet", -5.0, DepthUnitMeters, DepthUnitFeet, -16.4042},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ConvertDepth(tt.value, tt.from, tt.to)
			if math.Abs(got-tt.expected) > epsilon {
				t.Errorf("ConvertDepth(%v, %v, %v) = %v, want %v",
					tt.value, tt.from, tt.to, got, tt.expected)
			}
		})
	}
}

func TestMetersToFeet(t *testing.T) {
	tests := []struct {
		meters   float64
		expected float64
	}{
		{0.0, 0.0},
		{1.0, 3.28084},
		{10.0, 32.8084},
		{100.0, 328.084},
		{-5.0, -16.4042},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			got := MetersToFeet(tt.meters)
			if math.Abs(got-tt.expected) > epsilon {
				t.Errorf("MetersToFeet(%v) = %v, want %v", tt.meters, got, tt.expected)
			}
		})
	}
}

func TestFeetToMeters(t *testing.T) {
	tests := []struct {
		feet     float64
		expected float64
	}{
		{0.0, 0.0},
		{3.28084, 1.0},
		{32.8084, 10.0},
		{328.084, 100.0},
		{100.0, 30.48},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			got := FeetToMeters(tt.feet)
			if math.Abs(got-tt.expected) > epsilon {
				t.Errorf("FeetToMeters(%v) = %v, want %v", tt.feet, got, tt.expected)
			}
		})
	}
}

func TestMetersToFathoms(t *testing.T) {
	tests := []struct {
		meters   float64
		expected float64
	}{
		{0.0, 0.0},
		{1.8288, 1.0},
		{18.288, 10.0},
		{100.0, 54.6807},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			got := MetersToFathoms(tt.meters)
			if math.Abs(got-tt.expected) > epsilon {
				t.Errorf("MetersToFathoms(%v) = %v, want %v", tt.meters, got, tt.expected)
			}
		})
	}
}

func TestFathomsToMeters(t *testing.T) {
	tests := []struct {
		fathoms  float64
		expected float64
	}{
		{0.0, 0.0},
		{1.0, 1.8288},
		{10.0, 18.288},
		{100.0, 182.88},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			got := FathomsToMeters(tt.fathoms)
			if math.Abs(got-tt.expected) > epsilon {
				t.Errorf("FathomsToMeters(%v) = %v, want %v", tt.fathoms, got, tt.expected)
			}
		})
	}
}

func TestFeetToFathoms(t *testing.T) {
	tests := []struct {
		feet     float64
		expected float64
	}{
		{0.0, 0.0},
		{6.0, 1.0},
		{60.0, 10.0},
		{100.0, 16.6667},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			got := FeetToFathoms(tt.feet)
			if math.Abs(got-tt.expected) > epsilon {
				t.Errorf("FeetToFathoms(%v) = %v, want %v", tt.feet, got, tt.expected)
			}
		})
	}
}

func TestFathomsToFeet(t *testing.T) {
	tests := []struct {
		fathoms  float64
		expected float64
	}{
		{0.0, 0.0},
		{1.0, 6.0},
		{10.0, 60.0},
		{100.0, 600.0},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			got := FathomsToFeet(tt.fathoms)
			if math.Abs(got-tt.expected) > epsilon {
				t.Errorf("FathomsToFeet(%v) = %v, want %v", tt.fathoms, got, tt.expected)
			}
		})
	}
}

// Test round-trip conversions to ensure accuracy
func TestRoundTripConversions(t *testing.T) {
	original := 42.5

	// Meters → Feet → Meters
	feet := MetersToFeet(original)
	backToMeters := FeetToMeters(feet)
	if math.Abs(backToMeters-original) > epsilon {
		t.Errorf("Round trip meters→feet→meters: got %v, want %v", backToMeters, original)
	}

	// Meters → Fathoms → Meters
	fathoms := MetersToFathoms(original)
	backToMeters = FathomsToMeters(fathoms)
	if math.Abs(backToMeters-original) > epsilon {
		t.Errorf("Round trip meters→fathoms→meters: got %v, want %v", backToMeters, original)
	}

	// Feet → Fathoms → Feet
	originalFeet := 120.0
	fathoms = FeetToFathoms(originalFeet)
	backToFeet := FathomsToFeet(fathoms)
	if math.Abs(backToFeet-originalFeet) > epsilon {
		t.Errorf("Round trip feet→fathoms→feet: got %v, want %v", backToFeet, originalFeet)
	}
}

// Test ConvertDepth with all unit combinations
func TestConvertDepthAllCombinations(t *testing.T) {
	value := 10.0
	units := []DepthUnit{DepthUnitMeters, DepthUnitFeet, DepthUnitFathoms}

	for _, from := range units {
		for _, to := range units {
			// Convert and back
			converted := ConvertDepth(value, from, to)
			backConverted := ConvertDepth(converted, to, from)

			if math.Abs(backConverted-value) > epsilon {
				t.Errorf("Round trip %v→%v→%v: got %v, want %v",
					from, to, from, backConverted, value)
			}
		}
	}
}
