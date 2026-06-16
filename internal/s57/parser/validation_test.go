package parser

import (
	"fmt"
	"strings"
	"testing"
)

// TestValidateCoordinate tests coordinate validation
func TestValidateCoordinate(t *testing.T) {
	tests := []struct {
		name    string
		lat     float64
		lon     float64
		wantErr bool
	}{
		{"valid", 42.35, -71.05, false},
		{"lat max boundary", 90.0, 0.0, false},
		{"lat min boundary", -90.0, 0.0, false},
		{"lon max boundary", 0.0, 180.0, false},
		{"lon min boundary", 0.0, -180.0, false},
		{"lat too high", 90.1, 0.0, true},
		{"lat too low", -90.1, 0.0, true},
		{"lon too high", 0.0, 180.1, true},
		{"lon too low", 0.0, -180.1, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateCoordinate(tt.lat, tt.lon)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateCoordinate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestValidateGeometry tests geometry validation
func TestValidateGeometry(t *testing.T) {
	tests := []struct {
		name     string
		geometry *Geometry
		wantErr  bool
	}{
		{
			name: "valid point",
			geometry: &Geometry{
				Type:        GeometryTypePoint,
				Coordinates: [][]float64{{-71.0, 42.0}},
			},
			wantErr: false,
		},
		{
			name: "valid linestring",
			geometry: &Geometry{
				Type: GeometryTypeLineString,
				Coordinates: [][]float64{
					{-71.0, 42.0},
					{-70.0, 43.0},
				},
			},
			wantErr: false,
		},
		{
			name: "valid polygon",
			geometry: &Geometry{
				Type: GeometryTypePolygon,
				Coordinates: [][]float64{
					{-71.0, 42.0},
					{-70.0, 42.0},
					{-70.0, 43.0},
					{-71.0, 42.0}, // Closed
				},
			},
			wantErr: false,
		},
		{
			name: "multipoint (SOUNDG) with multiple coordinates",
			geometry: &Geometry{
				Type: GeometryTypePoint,
				Coordinates: [][]float64{
					{-71.0, 42.0},
					{-70.0, 43.0},
				},
			},
			wantErr: false, // S-57 allows multipoint features like SOUNDG
		},
		{
			name: "degenerate linestring with one coordinate",
			geometry: &Geometry{
				Type:        GeometryTypeLineString,
				Coordinates: [][]float64{{-71.0, 42.0}},
			},
			wantErr: false, // Degenerate geometries allowed, skipped during rendering
		},
		{
			name: "degenerate polygon not closed",
			geometry: &Geometry{
				Type: GeometryTypePolygon,
				Coordinates: [][]float64{
					{-71.0, 42.0},
					{-70.0, 42.0},
					{-70.0, 43.0},
					// Missing closing coordinate
				},
			},
			wantErr: false, // Degenerate geometries allowed, skipped during rendering
		},
		{
			name: "invalid latitude",
			geometry: &Geometry{
				Type:        GeometryTypePoint,
				Coordinates: [][]float64{{-71.0, 95.0}}, // lat > 90
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateGeometry(tt.geometry)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateGeometry() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestValidateFeature tests feature validation
func TestValidateFeature(t *testing.T) {
	tests := []struct {
		name    string
		feature *Feature
		wantErr bool
	}{
		{
			name: "valid feature",
			feature: &Feature{
				ID:          1,
				ObjectClass: "DEPCNT",
				Geometry: Geometry{
					Type:        GeometryTypeLineString,
					Coordinates: [][]float64{{-71.0, 42.0}, {-70.0, 43.0}},
				},
				Attributes: map[string]interface{}{"DRVAL1": 10.0},
			},
			wantErr: false,
		},
		{
			name:    "nil feature",
			feature: nil,
			wantErr: true,
		},
		{
			name: "empty object class",
			feature: &Feature{
				ID:          1,
				ObjectClass: "",
				Geometry: Geometry{
					Type:        GeometryTypePoint,
					Coordinates: [][]float64{{-71.0, 42.0}},
				},
			},
			wantErr: true,
		},
		{
			name: "invalid geometry",
			feature: &Feature{
				ID:          1,
				ObjectClass: "DEPCNT",
				Geometry: Geometry{
					Type:        GeometryTypePoint,
					Coordinates: [][]float64{{-71.0, 95.0}}, // Invalid lat
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateFeature(tt.feature)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateFeature() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestValidationBreakdown shows exactly which validation checks are failing
func TestValidationBreakdown(t *testing.T) {
	parser, err := DefaultParser()
	if err != nil {
		t.Fatalf("failed to create parser: %v", err)
	}

	// Parse without validation
	opts := ParseOptions{
		SkipUnknownFeatures: true,
		ValidateGeometry:    false,
	}
	chart, err := parser.ParseWithOptions("../../../testdata/US4MD81M.000", opts)
	if err != nil {
		t.Fatalf("failed to parse: %v", err)
	}

	// Now validate each feature manually and categorize failures
	validationErrors := make(map[string]int)

	for _, feature := range chart.Features {
		if err := ValidateGeometry(&feature.Geometry); err != nil {
			// Extract the specific error reason
			errMsg := err.Error()
			// Simplify error message to group similar errors
			if strings.Contains(errMsg, "no coordinates") {
				validationErrors["no_coordinates"]++
			} else if strings.Contains(errMsg, "exactly 1 coordinate") {
				validationErrors["point_wrong_count"]++
			} else if strings.Contains(errMsg, "at least 2 coordinates") {
				validationErrors["linestring_too_short"]++
			} else if strings.Contains(errMsg, "at least 3 coordinates") {
				validationErrors["polygon_too_short"]++
			} else if strings.Contains(errMsg, "not closed") {
				validationErrors["polygon_not_closed"]++
			} else if strings.Contains(errMsg, "exactly 2 values") {
				validationErrors["coord_wrong_dimension"]++
			} else if strings.Contains(errMsg, "invalid") {
				validationErrors["coord_out_of_bounds"]++
			} else {
				validationErrors["other: "+errMsg]++
			}
		}
	}

	fmt.Printf("\n=== VALIDATION FAILURE BREAKDOWN ===\n")
	fmt.Printf("Total features parsed: %d\n", len(chart.Features))

	totalErrors := 0
	for _, count := range validationErrors {
		totalErrors += count
	}
	fmt.Printf("Features failing validation: %d (%.1f%%)\n\n", totalErrors, float64(totalErrors)/float64(len(chart.Features))*100.0)

	fmt.Println("Failure reasons:")
	for reason, count := range validationErrors {
		fmt.Printf("  %-30s: %d\n", reason, count)
	}
}
