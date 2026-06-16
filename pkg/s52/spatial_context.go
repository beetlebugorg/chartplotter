package s52

// Coordinate represents a geographic coordinate point.
type Coordinate struct {
	Lat float64 // Latitude in degrees
	Lon float64 // Longitude in degrees
}

// SpatialComponent represents a spatial component (edge, ring, point) of a feature
// with its own geometry and attributes.
//
// In S-57, spatial components can have their own attributes. For example,
// a DEPCNT (depth contour) may have multiple edges, each with its own QUAPOS
// (position quality) attribute.
type SpatialComponent struct {
	// ID is a unique identifier for this component within the feature
	ID int

	// Type identifies what kind of component this is (e.g., "Edge", "Node", "Face")
	Type string

	// Geometry contains the coordinate points that make up this component
	Geometry []Coordinate

	// Attributes contains component-level S-57 attributes
	// For example: QUAPOS (quality of position) can be specified per edge
	Attributes map[string]interface{}
}

// AdjacentObject represents a neighboring feature that shares an edge or boundary
// with the current feature.
//
// This is used by procedures like DEPARE03 to detect when a depth area is adjacent
// to land (LNDARE) or unsurveyed areas (UNSARE), which affects safety contour rendering.
type AdjacentObject struct {
	// ObjectClass is the S-57 object class of the adjacent feature (e.g., "LNDARE", "UNSARE")
	ObjectClass string

	// SharedEdge identifies which component (by ID) is shared with this adjacent object
	// -1 if the adjacency is not edge-specific
	SharedEdge int

	// Attributes contains the S-57 attributes of the adjacent feature
	Attributes map[string]interface{}
}

// UnderlyingObject represents a feature that is spatially beneath or contains
// the current feature.
//
// This is used by procedures like DEPVAL02 (depth value determination) to find
// underlying DEPARE (depth area) objects when determining the depth at a point.
type UnderlyingObject struct {
	// ObjectClass is the S-57 object class of the underlying feature (e.g., "DEPARE")
	ObjectClass string

	// Attributes contains the S-57 attributes of the underlying feature
	Attributes map[string]interface{}
}

// SpatialContext provides spatial topology information to CS procedures.
//
// This structure is OPTIONAL and can be nil. When provided, it enables full
// S-52 compliance for procedures that require spatial analysis. When nil,
// procedures fall back to simplified logic that works without spatial topology.
//
// The library does not perform spatial analysis - this data must be provided
// by the caller (renderer, SENC builder, etc.) who has access to the full
// spatial dataset and can perform topology queries.
//
// Example use cases:
//   - DEPARE03: Needs adjacent objects to detect inland water vs ocean depths
//   - DEPARE03: Needs component data for edge-by-edge safety contour rendering
//   - DEPVAL02: Needs underlying objects to determine depth at a sounding point
//   - DEPCNT03: Needs component QUAPOS for per-segment line styling
//   - UDWHAZ05: Needs underlying safe water to detect isolated dangers
type SpatialContext struct {
	// GeometryType identifies the geometry type of the feature
	// Valid values: "Point", "Line", "Area", "MultiPoint"
	// This is already known from the LUT but included here for completeness
	GeometryType string

	// Components contains the individual spatial components (edges, rings, nodes)
	// that make up this feature, each with its own geometry and attributes.
	//
	// For example, a DEPARE (area) might have multiple edge components, each
	// with its own QUAPOS (quality) attribute indicating which edges are certain
	// vs approximate.
	Components []SpatialComponent

	// AdjacentObjects contains features that share an edge or boundary with
	// this feature.
	//
	// Used for detecting adjacency relationships, such as:
	//   - DEPARE adjacent to LNDARE (land) → inland water
	//   - DEPARE adjacent to UNSARE (unsurveyed) → use dashed safety contour
	AdjacentObjects []AdjacentObject

	// UnderlyingObjects contains features that are spatially beneath or contain
	// this feature.
	//
	// Used for spatial queries such as:
	//   - Finding DEPARE under a SOUNDG point to determine expected depth
	//   - Finding safe water under/around UWTROC to determine if isolated danger
	UnderlyingObjects []UnderlyingObject
}
