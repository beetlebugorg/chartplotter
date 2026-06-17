package s52

import (
	"testing"

	s57 "github.com/beetlebugorg/chartplotter/pkg/s57"
)

// mockProjection is a simple projection for testing
type mockProjection struct{}

func (m *mockProjection) Project(lat, lon float64) (x, y float64) {
	// Simple equirectangular projection for testing
	return lon * 111320.0, lat * 110540.0 // meters
}

func TestNewChartScene(t *testing.T) {
	library := &Library{}
	settings := DefaultMarinerSettings()

	scene := NewChartScene(library, settings)

	if scene == nil {
		t.Fatal("NewChartScene returned nil")
	}
	if scene.Library != library {
		t.Error("scene library not set correctly")
	}
	if scene.Settings != settings {
		t.Error("scene settings not set correctly")
	}
	if scene.Nodes == nil {
		t.Error("scene nodes not initialized")
	}
	if scene.SpatialIndex == nil {
		t.Error("spatial index not initialized")
	}
}

func TestAddNode(t *testing.T) {
	library := &Library{}
	settings := DefaultMarinerSettings()
	scene := NewChartScene(library, settings)

	// Create nodes directly (testing AddNode, not BuildFromFeatures)
	node1 := &FeatureNode{
		ID:          "DEPARE-1",
		ObjectClass: "DEPARE",
		GeoBounds:   GeoBounds{MinLon: -71.0, MaxLon: -70.5, MinLat: 42.0, MaxLat: 42.5},
		Attributes:  map[string]interface{}{"DRVAL1": 10.0},
		Dirty:       GeometryDirty | StyleDirty | PrimitivesDirty | VisibilityDirty,
	}

	node2 := &FeatureNode{
		ID:          "BOYLAT-2",
		ObjectClass: "BOYLAT",
		GeoBounds:   GeoBounds{MinLon: -70.8, MaxLon: -70.79, MinLat: 42.3, MaxLat: 42.31}, // Small area, not point
		Attributes:  map[string]interface{}{},
		Dirty:       GeometryDirty | StyleDirty | PrimitivesDirty | VisibilityDirty,
	}

	scene.AddNode(node1)
	scene.AddNode(node2)

	if len(scene.Nodes) != 2 {
		t.Errorf("expected 2 nodes, got %d", len(scene.Nodes))
	}

	// Verify spatial index was updated
	if scene.SpatialIndex.Size() != 2 {
		t.Errorf("expected 2 nodes in spatial index, got %d", scene.SpatialIndex.Size())
	}

	// Verify first node
	if scene.Nodes[0].ObjectClass != "DEPARE" {
		t.Errorf("expected DEPARE, got %s", scene.Nodes[0].ObjectClass)
	}
	if scene.Nodes[0].GeoBounds.MinLon != -71.0 || scene.Nodes[0].GeoBounds.MaxLon != -70.5 {
		t.Errorf("incorrect bounds for DEPARE: %+v", scene.Nodes[0].GeoBounds)
	}

	// Verify second node
	if scene.Nodes[1].ObjectClass != "BOYLAT" {
		t.Errorf("expected BOYLAT, got %s", scene.Nodes[1].ObjectClass)
	}
	if !scene.Nodes[1].Dirty.Has(GeometryDirty | StyleDirty | PrimitivesDirty | VisibilityDirty) {
		t.Error("new node should be marked fully dirty")
	}
}

func TestDirtyTracking(t *testing.T) {
	library := &Library{}
	settings := DefaultMarinerSettings()
	scene := NewChartScene(library, settings)

	node := &FeatureNode{
		ID:          "TEST-1",
		ObjectClass: "DEPARE",
		GeoBounds:   GeoBounds{MinLon: -71, MaxLon: -70, MinLat: 42, MaxLat: 43},
		Dirty:       CleanNode,
	}
	scene.AddNode(node)

	// Test UpdateViewport marks nodes dirty
	viewport := &Viewport{
		GeoBounds:  GeoBounds{MinLon: -71, MaxLon: -70, MinLat: 42, MaxLat: 43},
		Scale:      25000,
		Projection: &mockProjection{},
	}
	scene.UpdateViewport(viewport)

	if !node.Dirty.Has(VisibilityDirty | GeometryDirty | PrimitivesDirty) {
		t.Error("UpdateViewport should mark nodes dirty")
	}

	// Clear dirty flags
	node.Dirty.ClearAll()

	// Test UpdateSettings marks nodes dirty
	newSettings := DefaultMarinerSettings()
	newSettings.DisplayCategory = DisplayOther
	scene.UpdateSettings(newSettings)

	if !node.Dirty.Has(StyleDirty | PrimitivesDirty) {
		t.Error("UpdateSettings should mark style dirty")
	}
}

func TestGetVisibleNodes(t *testing.T) {
	library := &Library{}
	settings := DefaultMarinerSettings()
	scene := NewChartScene(library, settings)

	// Add nodes with different bounds
	nodes := []*FeatureNode{
		{
			ID:          "INSIDE-1",
			ObjectClass: "DEPARE",
			GeoBounds:   GeoBounds{MinLon: -71, MaxLon: -70.5, MinLat: 42, MaxLat: 42.5},
		},
		{
			ID:          "OUTSIDE-1",
			ObjectClass: "LNDARE",
			GeoBounds:   GeoBounds{MinLon: -80, MaxLon: -79, MinLat: 30, MaxLat: 31},
		},
		{
			ID:          "OVERLAPPING-1",
			ObjectClass: "BOYLAT",
			GeoBounds:   GeoBounds{MinLon: -70.6, MaxLon: -70.4, MinLat: 42.4, MaxLat: 42.6},
		},
	}

	for _, node := range nodes {
		scene.AddNode(node)
	}

	// Set viewport that includes first and third node
	scene.Viewport = &Viewport{
		GeoBounds:  GeoBounds{MinLon: -71, MaxLon: -70, MinLat: 42, MaxLat: 42.5},
		Scale:      25000,
		Projection: &mockProjection{},
	}

	visible := scene.GetVisibleNodes()

	if len(visible) != 2 {
		t.Errorf("expected 2 visible nodes, got %d", len(visible))
	}

	// Verify correct nodes are returned
	ids := make(map[string]bool)
	for _, node := range visible {
		ids[node.ID] = true
	}

	if !ids["INSIDE-1"] {
		t.Error("INSIDE-1 should be visible")
	}
	if !ids["OVERLAPPING-1"] {
		t.Error("OVERLAPPING-1 should be visible")
	}
	if ids["OUTSIDE-1"] {
		t.Error("OUTSIDE-1 should not be visible")
	}
}

func TestSpatialIndex(t *testing.T) {
	library := &Library{}
	settings := DefaultMarinerSettings()
	scene := NewChartScene(library, settings)

	// Add 100 nodes
	for i := 0; i < 100; i++ {
		lon := -71.0 + float64(i)*0.01
		lat := 42.0 + float64(i)*0.01
		node := &FeatureNode{
			ID:          "DEPARE-" + string(rune('0'+i)),
			ObjectClass: "DEPARE",
			GeoBounds:   GeoBounds{MinLon: lon, MaxLon: lon + 0.01, MinLat: lat, MaxLat: lat + 0.01},
		}
		scene.AddNode(node)
	}

	if scene.SpatialIndex.Size() != 100 {
		t.Errorf("expected 100 nodes in spatial index, got %d", scene.SpatialIndex.Size())
	}

	// Query small area (should only return subset)
	scene.Viewport = &Viewport{
		GeoBounds:  GeoBounds{MinLon: -71.0, MaxLon: -70.9, MinLat: 42.0, MaxLat: 42.1},
		Scale:      25000,
		Projection: &mockProjection{},
	}

	visible := scene.GetVisibleNodes()

	if len(visible) >= 100 {
		t.Error("spatial query should return subset of nodes")
	}
	if len(visible) == 0 {
		t.Error("spatial query should return some nodes")
	}
}

func TestUpdateNodeGeometry(t *testing.T) {
	library := &Library{}
	settings := DefaultMarinerSettings()
	scene := NewChartScene(library, settings)

	// Create a point feature
	geom := s57.Geometry{
		Type: s57.GeometryTypePoint,
		Coordinates: [][]float64{
			{-71.0, 42.0}, // lon, lat
		},
	}

	node := &FeatureNode{
		ID:          "TEST-1",
		ObjectClass: "BOYLAT",
		GeoGeometry: geom,
	}

	scene.Viewport = &Viewport{
		GeoBounds:  GeoBounds{MinLon: -72, MaxLon: -70, MinLat: 41, MaxLat: 43},
		Scale:      25000,
		Projection: &mockProjection{},
	}

	err := scene.updateNodeGeometry(node)
	if err != nil {
		t.Fatalf("updateNodeGeometry failed: %v", err)
	}

	if len(node.Geometry) != 1 {
		t.Errorf("expected 1 projected point, got %d", len(node.Geometry))
	}

	// Verify projection was applied (values should be in meters, not degrees)
	// For lon=-71, lat=42 the projected values should be around -7.9M and 4.6M meters
	if node.Geometry[0].X > -1e6 || node.Geometry[0].Y < 1e6 {
		t.Errorf("projection values seem incorrect (should be in meters): %+v", node.Geometry[0])
	}
}

// Note: BuildFromFeatures is tested in integration tests with real S-57 data
// Unit tests focus on scene graph operations that can be tested without mocking s57.Feature
