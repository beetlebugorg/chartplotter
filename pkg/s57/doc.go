// Package s57 provides a parser for IHO S-57 Electronic Navigational Charts.
//
// This package is designed for chart rendering applications. It provides fast spatial
// queries, feature grouping, and a clean API optimized for viewport-based rendering.
//
// # Basic Usage
//
//	parser := s57.NewParser()
//	chart, err := parser.Parse("US5MA22M.000")
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	fmt.Printf("Chart: %s covers %+v\n", chart.DatasetName(), chart.Bounds())
//
// # Rendering Workflow
//
// The typical rendering workflow uses spatial queries to efficiently render only
// visible features:
//
//	// 1. Query features in viewport
//	viewport := s57.Bounds{
//	    MinLon: -71.5, MaxLon: -71.0,
//	    MinLat: 42.0, MaxLat: 42.5,
//	}
//	visibleFeatures := chart.FeaturesInBounds(viewport)
//
//	// 2. Pass to S-52 presentation library for rendering
//	// S-52 handles all grouping, ordering, and symbology based on its lookup tables
//	s52.Render(visibleFeatures, displaySettings)
//
// # Spatial Queries
//
// The chart automatically builds a spatial index for fast viewport queries:
//
//	// Get chart coverage
//	bounds := chart.Bounds()
//
//	// Query visible features
//	visible := chart.FeaturesInBounds(viewport)
//
//	// Features are returned as a slice - no allocation overhead for iteration
//
// # Feature Access
//
// Access all features or query by object class:
//
//	// Get all features in the chart
//	allFeatures := chart.Features()
//
//	// Each feature contains everything needed for S-52 symbology lookup:
//	for _, feature := range allFeatures {
//	    class := feature.ObjectClass()        // "ACHARE", "DEPARE", "LNDARE"
//	    attrs := feature.Attributes()         // All feature attributes
//	    geom := feature.Geometry()            // Geometry with type and coordinates
//	    // Pass to S-52 for symbology lookup and rendering
//	}
//
// # Accessing Feature Data
//
//	for _, feature := range visibleFeatures {
//	    id := feature.ID()
//	    class := feature.ObjectClass()    // "DEPCNT", "LIGHTS", etc.
//	    geom := feature.Geometry()
//
//	    // Access coordinates
//	    for _, coord := range geom.Coordinates {
//	        lon, lat := coord[0], coord[1]
//	        // ... project and render
//	    }
//
//	    // Access attributes for styling
//	    if depth, ok := feature.Attribute("DRVAL1"); ok {
//	        // Apply depth-based color
//	    }
//	}
//
// # Integration with S-52 Presentation Library
//
// This library handles S-57 parsing only. Features are designed to work directly
// with S-52 presentation libraries for symbology lookup and rendering.
//
//	// Parse S-57 chart
//	chart, _ := s57Parser.Parse("chart.000")
//
//	// S-52 uses ObjectClass + Attributes + Geometry for lookup
//	for _, feature := range chart.Features() {
//	    // S-52 looks up: ObjectClass + GeometryType + Attributes → Symbology
//	    symbology := s52.Lookup(feature.ObjectClass(), feature.GeometryType(), feature.Attributes())
//	    render(feature.Geometry(), symbology)
//	}
//
// # Performance
//
// - Spatial index built automatically during parsing
// - Viewport queries are O(n) with low constant factor (simple bounding box checks)
// - No allocations during iteration
// - Features parsed eagerly (charts fit in memory)
package s57
