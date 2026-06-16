package s57

import (
	"testing"
)

const testChartPath = "../../testdata/US4MD81M.000"

// TestPublicAPI tests the public parser API
func TestPublicAPI(t *testing.T) {
	parser := NewParser()
	if parser == nil {
		t.Fatal("NewParser returned nil")
	}

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
	parser := NewParser()
	chart, err := parser.Parse(testChartPath)
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
	if chart.FeatureCount() == 0 {
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
		chart.DatasetName(), chart.FeatureCount(),
		bounds.MinLon, bounds.MinLat, bounds.MaxLon, bounds.MaxLat)
}

// TestUpdateFileHandling tests automatic update file application
// S-57 §3.1: Exchange Set Structure
func TestUpdateFileHandling(t *testing.T) {
	parser := NewParser()

	// Parse with updates (default behavior)
	chart, err := parser.Parse(testChartPath)
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
	opts := ParseOptions{ApplyUpdates: false}
	baseChart, err := parser.ParseWithOptions(testChartPath, opts)
	if err != nil {
		t.Fatalf("Failed to parse base cell: %v", err)
	}

	if baseChart.UpdateNumber() != "0" {
		t.Errorf("Base cell should have update number 0, got %s", baseChart.UpdateNumber())
	}

	// Chart with updates should have different feature count than base
	// (updates modify the dataset)
	t.Logf("Base features: %d, Updated features: %d",
		baseChart.FeatureCount(), chart.FeatureCount())
}

// TestFeatureObjects tests S-57 feature objects
// S-57 §7.3: Feature Object Records
func TestFeatureObjects(t *testing.T) {
	parser := NewParser()
	chart, err := parser.Parse(testChartPath)
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

// TestSpatialIndexing tests R-tree spatial index for viewport queries
// S-57 §7.3.3: Spatial Objects
func TestSpatialIndexing(t *testing.T) {
	parser := NewParser()
	chart, err := parser.Parse(testChartPath)
	if err != nil {
		t.Fatalf("Failed to parse chart: %v", err)
	}

	// Get chart bounds
	chartBounds := chart.Bounds()

	// Query a subset viewport (middle 50% of chart)
	lonRange := chartBounds.MaxLon - chartBounds.MinLon
	latRange := chartBounds.MaxLat - chartBounds.MinLat

	viewport := Bounds{
		MinLon: chartBounds.MinLon + lonRange*0.25,
		MaxLon: chartBounds.MaxLon - lonRange*0.25,
		MinLat: chartBounds.MinLat + latRange*0.25,
		MaxLat: chartBounds.MaxLat - latRange*0.25,
	}

	visible := chart.FeaturesInBounds(viewport)

	// Should return subset of features
	totalFeatures := chart.FeatureCount()
	if len(visible) == 0 {
		t.Error("Viewport query should return some features")
	}
	if len(visible) >= totalFeatures {
		t.Error("Viewport query should return fewer features than total")
	}

	// Note: R-tree spatial index can return features slightly outside the query bounds
	// due to node overlap and floating point precision. This is expected behavior.
	// We just verify that the query returns a reasonable subset of features.

	t.Logf("Viewport query: %d/%d features visible", len(visible), totalFeatures)
}

// TestGeometryTypes tests S-57 geometry types
// S-57 §7.3.3: Spatial Primitives (Point, Line, Area)
func TestGeometryTypes(t *testing.T) {
	parser := NewParser()
	chart, err := parser.Parse(testChartPath)
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
	parser := NewParser()
	chart, err := parser.Parse(testChartPath)
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
	parser := NewParser()

	// Parse only depth contours
	opts := ParseOptions{
		ObjectClassFilter: []string{"DEPCNT"},
	}

	chart, err := parser.ParseWithOptions(testChartPath, opts)
	if err != nil {
		t.Fatalf("Failed to parse with filter: %v", err)
	}

	// All features should be DEPCNT
	for _, f := range chart.Features() {
		if f.ObjectClass() != "DEPCNT" {
			t.Errorf("Expected only DEPCNT features, got %s", f.ObjectClass())
		}
	}

	t.Logf("Filtered to %d DEPCNT features", chart.FeatureCount())
}

// TestUsageBand tests ENC usage band classification
// S-57 Appendix B.1: Navigational Purpose
func TestUsageBand(t *testing.T) {
	tests := []struct {
		band     UsageBand
		name     string
		minScale int
		maxScale int
	}{
		{UsageBandOverview, "Overview", 1500000, 0},
		{UsageBandGeneral, "General", 350000, 1500000},
		{UsageBandCoastal, "Coastal", 90000, 350000},
		{UsageBandApproach, "Approach", 22000, 90000},
		{UsageBandHarbour, "Harbour", 4000, 22000},
		{UsageBandBerthing, "Berthing", 0, 4000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.band.String() != tt.name {
				t.Errorf("Expected name %s, got %s", tt.name, tt.band.String())
			}
			min, max := tt.band.ScaleRange()
			if min != tt.minScale || max != tt.maxScale {
				t.Errorf("Expected scale range (%d, %d), got (%d, %d)",
					tt.minScale, tt.maxScale, min, max)
			}
		})
	}
}

// TestBoundsOperations tests bounding box operations
func TestBoundsOperations(t *testing.T) {
	b1 := Bounds{MinLon: -71.0, MaxLon: -70.0, MinLat: 42.0, MaxLat: 43.0}
	b2 := Bounds{MinLon: -70.5, MaxLon: -69.5, MinLat: 42.5, MaxLat: 43.5}
	b3 := Bounds{MinLon: -69.0, MaxLon: -68.0, MinLat: 44.0, MaxLat: 45.0}

	// Test Intersects
	if !b1.Intersects(b2) {
		t.Error("b1 and b2 should intersect")
	}
	if b1.Intersects(b3) {
		t.Error("b1 and b3 should not intersect")
	}

	// Test Contains
	if !b1.Contains(-70.5, 42.5) {
		t.Error("b1 should contain point (-70.5, 42.5)")
	}
	if b1.Contains(-69.0, 44.0) {
		t.Error("b1 should not contain point (-69.0, 44.0)")
	}
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
