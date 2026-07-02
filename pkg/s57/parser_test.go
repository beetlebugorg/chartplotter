package s57

import (
	"testing"
)

const testChartPath = "../../testdata/US4MD81M.000"

// TestPublicAPI tests the public parser API
func TestPublicAPI(t *testing.T) {
	// Test default options
	opts := DefaultParseOptions()
	if opts.ValidateGeometry != true {
		t.Error("Default ValidateGeometry should be true")
	}
	if opts.SkipUnknownFeatures != false {
		t.Error("Default SkipUnknownFeatures should be false")
	}
}

// TestParseRealChart tests parsing a real NOAA ENC chart
// S-57 §7.2: Dataset General Information Record (DSID)
func TestParseRealChart(t *testing.T) {
	chart, err := ParseWithOptions(testChartPath, DefaultParseOptions())
	if err != nil {
		t.Fatalf("Failed to parse chart: %v", err)
	}

	// Verify DSID metadata - S-57 §7.2.1
	if chart.DatasetName() == "" {
		t.Error("Dataset name should not be empty")
	}
	if chart.Edition() == "" {
		t.Error("Edition should not be empty")
	}
	if chart.ProducingAgency() == 0 {
		t.Error("Producing agency should not be zero")
	}

	// Verify features were parsed - S-57 §7.3
	if len(chart.Features()) == 0 {
		t.Error("Chart should contain features")
	}

	// Verify spatial bounds calculated
	bounds := chart.Bounds()
	if bounds.MinLon >= bounds.MaxLon {
		t.Errorf("Invalid longitude bounds: %f to %f", bounds.MinLon, bounds.MaxLon)
	}
	if bounds.MinLat >= bounds.MaxLat {
		t.Errorf("Invalid latitude bounds: %f to %f", bounds.MinLat, bounds.MaxLat)
	}

	t.Logf("Parsed %s: %d features, bounds [%.6f,%.6f] to [%.6f,%.6f]",
		chart.DatasetName(), len(chart.Features()),
		bounds.MinLon, bounds.MinLat, bounds.MaxLon, bounds.MaxLat)
}

// TestUpdateFileHandling tests automatic update file application
// S-57 §3.1: Exchange Set Structure
func TestUpdateFileHandling(t *testing.T) {
	// Parse with updates (default behavior)
	chart, err := ParseWithOptions(testChartPath, DefaultParseOptions())
	if err != nil {
		t.Fatalf("Failed to parse with updates: %v", err)
	}

	// Should have applied updates .001, .002, .003
	updateNum := chart.UpdateNumber()
	if updateNum == "0" {
		t.Error("Expected updates to be applied, got update number 0")
	}

	t.Logf("Chart update number: %s", updateNum)

	// Parse without updates
	baseChart, err := ParseWithOptions(testChartPath, ParseOptions{ApplyUpdates: false})
	if err != nil {
		t.Fatalf("Failed to parse base cell: %v", err)
	}

	if baseChart.UpdateNumber() != "0" {
		t.Errorf("Base cell should have update number 0, got %s", baseChart.UpdateNumber())
	}

	// Chart with updates should have different feature count than base
	// (updates modify the dataset)
	t.Logf("Base features: %d, Updated features: %d",
		len(baseChart.Features()), len(chart.Features()))
}

// TestFeatureObjects tests S-57 feature objects
// S-57 §7.3: Feature Object Records
func TestFeatureObjects(t *testing.T) {
	chart, err := ParseWithOptions(testChartPath, DefaultParseOptions())
	if err != nil {
		t.Fatalf("Failed to parse chart: %v", err)
	}

	// Count feature types
	counts := make(map[string]int)
	for _, f := range chart.Features() {
		counts[f.ObjectClass()]++
	}

	// Verify common S-57 object classes exist
	expectedClasses := []string{"DEPCNT", "LIGHTS", "BUAARE"}
	for _, class := range expectedClasses {
		if count, ok := counts[class]; !ok || count == 0 {
			t.Errorf("Expected to find %s features", class)
		}
	}

	// Test feature accessor methods
	features := chart.Features()
	if len(features) == 0 {
		t.Fatal("No features to test")
	}

	f := features[0]
	if f.ID() == 0 {
		t.Error("Feature ID should not be zero")
	}
	if f.ObjectClass() == "" {
		t.Error("Feature ObjectClass should not be empty")
	}

	// Geometry should be valid
	geom := f.Geometry()
	if geom.Type != GeometryTypePoint && geom.Type != GeometryTypeLineString && geom.Type != GeometryTypePolygon {
		t.Errorf("Unexpected geometry type: %s", geom.Type)
	}

	t.Logf("Sample feature: ID=%d, Class=%s, Type=%s, Coords=%d",
		f.ID(), f.ObjectClass(), geom.Type, len(geom.Coordinates))
}

// TestGeometryTypes tests S-57 geometry types
// S-57 §7.3.3: Spatial Primitives (Point, Line, Area)
func TestGeometryTypes(t *testing.T) {
	chart, err := ParseWithOptions(testChartPath, DefaultParseOptions())
	if err != nil {
		t.Fatalf("Failed to parse chart: %v", err)
	}

	// Count geometry types
	typeCounts := make(map[GeometryType]int)
	for _, f := range chart.Features() {
		geom := f.Geometry()
		typeCounts[geom.Type]++
	}

	// S-57 uses Point, Line (LineString), and Area (Polygon)
	if typeCounts[GeometryTypePoint] == 0 {
		t.Error("Expected some point geometries")
	}
	if typeCounts[GeometryTypeLineString] == 0 {
		t.Error("Expected some line geometries")
	}
	if typeCounts[GeometryTypePolygon] == 0 {
		t.Error("Expected some polygon geometries")
	}

	t.Logf("Geometry types: Point=%d, Line=%d, Polygon=%d",
		typeCounts[GeometryTypePoint],
		typeCounts[GeometryTypeLineString],
		typeCounts[GeometryTypePolygon])
}

// TestFeatureAttributes tests S-57 feature attributes
// S-57 §7.3.1: Feature Attributes
func TestFeatureAttributes(t *testing.T) {
	chart, err := ParseWithOptions(testChartPath, DefaultParseOptions())
	if err != nil {
		t.Fatalf("Failed to parse chart: %v", err)
	}

	// Find a LIGHTS feature (should have attributes)
	var light Feature
	found := false
	for _, f := range chart.Features() {
		if f.ObjectClass() == "LIGHTS" {
			light = f
			found = true
			break
		}
	}

	if !found {
		t.Skip("No LIGHTS feature found in test chart")
	}

	// Test attribute access
	attrs := light.Attributes()
	if len(attrs) == 0 {
		t.Error("LIGHTS feature should have attributes")
	}

	// Test individual attribute access
	if val, ok := light.Attribute("OBJNAM"); ok {
		t.Logf("Light name: %v", val)
	}

	t.Logf("LIGHTS feature has %d attributes", len(attrs))
}

// TestObjectClassFiltering tests filtering by object class
// S-57 §7.3: Feature Object Class codes
func TestObjectClassFiltering(t *testing.T) {
	// Parse only depth contours
	opts := ParseOptions{
		ObjectClassFilter: []string{"DEPCNT"},
	}

	chart, err := ParseWithOptions(testChartPath, opts)
	if err != nil {
		t.Fatalf("Failed to parse with filter: %v", err)
	}

	// All features should be DEPCNT
	for _, f := range chart.Features() {
		if f.ObjectClass() != "DEPCNT" {
			t.Errorf("Expected only DEPCNT features, got %s", f.ObjectClass())
		}
	}

	t.Logf("Filtered to %d DEPCNT features", len(chart.Features()))
}

// TestGeometryTypeString tests geometry type string conversion
func TestGeometryTypeString(t *testing.T) {
	tests := []struct {
		gtype    GeometryType
		expected string
	}{
		{GeometryTypePoint, "Point"},
		{GeometryTypeLineString, "LineString"},
		{GeometryTypePolygon, "Polygon"},
	}

	for _, tt := range tests {
		if tt.gtype.String() != tt.expected {
			t.Errorf("GeometryType %d: expected %s, got %s",
				tt.gtype, tt.expected, tt.gtype.String())
		}
	}
}
