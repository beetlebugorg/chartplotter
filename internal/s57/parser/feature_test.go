package parser

import (
	"testing"
)

// TestFeatureCreation tests basic feature creation
func TestFeatureCreation(t *testing.T) {
	feature := Feature{
		ID:          12345,
		ObjectClass: "DEPCNT",
		Geometry: Geometry{
			Type: GeometryTypeLineString,
			Coordinates: [][]float64{
				{-71.05, 42.35},
				{-71.04, 42.36},
			},
		},
		Attributes: map[string]interface{}{
			"DRVAL1": 10.0,
		},
	}

	if feature.ID != 12345 {
		t.Errorf("Expected ID=12345, got %d", feature.ID)
	}

	if feature.ObjectClass != "DEPCNT" {
		t.Errorf("Expected ObjectClass=DEPCNT, got %s", feature.ObjectClass)
	}

	if feature.Geometry.Type != GeometryTypeLineString {
		t.Errorf("Expected GeometryTypeLineString, got %v", feature.Geometry.Type)
	}

	if len(feature.Attributes) != 1 {
		t.Errorf("Expected 1 attribute, got %d", len(feature.Attributes))
	}
}

// TestChart tests chart creation and metadata access
func TestChart(t *testing.T) {
	features := []Feature{
		{ID: 1, ObjectClass: "DEPCNT"},
		{ID: 2, ObjectClass: "DEPARE"},
	}

	metadata := &datasetMetadata{
		dsnm: "US5MA22M",
		edtn: "2",
		updn: "0",
	}

	chart := Chart{
		Features: features,
		metadata: metadata,
	}

	// Test metadata access via public methods
	if chart.DatasetName() != "US5MA22M" {
		t.Errorf("Expected DSNM=US5MA22M, got %s", chart.DatasetName())
	}

	if chart.Edition() != "2" {
		t.Errorf("Expected Edition=2, got %s", chart.Edition())
	}

	if chart.UpdateNumber() != "0" {
		t.Errorf("Expected UpdateNumber=0, got %s", chart.UpdateNumber())
	}

	if len(chart.Features) != 2 {
		t.Errorf("Expected 2 features, got %d", len(chart.Features))
	}
}
