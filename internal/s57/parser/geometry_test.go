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

// TestPointGeometryResolvesByRCNM is a regression test for the FSPT pointer bug:
// RCID is unique only WITHIN a record type (RCNM), so a point feature pointing at
// a connected node (120) must not be mis-resolved to an isolated node (110) that
// happens to share that RCID. Real-world symptom: range rear lights (which point
// at connected nodes) placed kilometres from their true position.
func TestPointGeometryResolvesByRCNM(t *testing.T) {
	const rcid int64 = 5
	spatialRecords := map[spatialKey]*spatialRecord{
		{RCNM: int(spatialTypeIsolatedNode), RCID: rcid}:  {ID: rcid, RecordType: spatialTypeIsolatedNode, Coordinates: [][]float64{{-76.0, 38.0}}},
		{RCNM: int(spatialTypeConnectedNode), RCID: rcid}: {ID: rcid, RecordType: spatialTypeConnectedNode, Coordinates: [][]float64{{-76.46, 39.22}}},
	}
	feat := &featureRecord{
		GeomPrim:    1, // point
		SpatialRefs: []spatialRef{{RCNM: int(spatialTypeConnectedNode), RCID: rcid}},
	}
	g, err := constructPointGeometry(feat, spatialRecords)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Coordinates) != 1 || g.Coordinates[0][0] != -76.46 || g.Coordinates[0][1] != 39.22 {
		t.Fatalf("point resolved to wrong spatial record: got %v, want connected node [-76.46, 39.22]", g.Coordinates)
	}
}
