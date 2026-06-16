package parser

import (
	"testing"
)

// TestGeometryTypes tests geometry type enumeration
func TestGeometryTypes(t *testing.T) {
	tests := []struct {
		geomType GeometryType
		expected string
	}{
		{GeometryTypePoint, "Point"},
		{GeometryTypeLineString, "LineString"},
		{GeometryTypePolygon, "Polygon"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if tt.geomType.String() != tt.expected {
				t.Errorf("Expected %s, got %s", tt.expected, tt.geomType.String())
			}
		})
	}
}

// TestGeometryCreation tests basic geometry creation
func TestGeometryCreation(t *testing.T) {
	tests := []struct {
		name        string
		geomType    GeometryType
		coordinates [][]float64
	}{
		{
			name:     "point",
			geomType: GeometryTypePoint,
			coordinates: [][]float64{
				{-71.05, 42.35},
			},
		},
		{
			name:     "linestring",
			geomType: GeometryTypeLineString,
			coordinates: [][]float64{
				{-71.05, 42.35},
				{-71.04, 42.36},
			},
		},
		{
			name:     "polygon",
			geomType: GeometryTypePolygon,
			coordinates: [][]float64{
				{-71.05, 42.35},
				{-71.04, 42.35},
				{-71.04, 42.36},
				{-71.05, 42.36},
				{-71.05, 42.35}, // Closed
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			geom := Geometry{
				Type:        tt.geomType,
				Coordinates: tt.coordinates,
			}

			if geom.Type != tt.geomType {
				t.Errorf("Expected Type=%v, got %v", tt.geomType, geom.Type)
			}

			if len(geom.Coordinates) != len(tt.coordinates) {
				t.Errorf("Expected %d coordinates, got %d", len(tt.coordinates), len(geom.Coordinates))
			}
		})
	}
}
