package parser

import "sync"

var (
	coordSlicePool = sync.Pool{
		New: func() interface{} {
			slice := make([][]float64, 0, 64)
			return &slice
		},
	}

	visitedMapPool = sync.Pool{
		New: func() interface{} {
			return make(map[int64]bool, 16)
		},
	}

	spatialRefSlicePool = sync.Pool{
		New: func() interface{} {
			slice := make([]spatialRef, 0, 16)
			return &slice
		},
	}

	coord2DPool = sync.Pool{
		New: func() interface{} {
			return &[2]float64{}
		},
	}
)

// GeometryType represents the type of geometry for a feature
type GeometryType int

const (
	// GeometryTypePoint represents a single point location
	GeometryTypePoint GeometryType = iota
	// GeometryTypeLineString represents a line composed of connected points
	GeometryTypeLineString
	// GeometryTypePolygon represents a closed polygon area
	GeometryTypePolygon
)

// String returns the string representation of the geometry type
func (g GeometryType) String() string {
	switch g {
	case GeometryTypePoint:
		return "Point"
	case GeometryTypeLineString:
		return "LineString"
	case GeometryTypePolygon:
		return "Polygon"
	default:
		return "Unknown"
	}
}

// Ring represents a polygon ring (exterior or interior) with usage indicator
// S-57 §2.2.8: USAG subfield indicates ring type
type Ring struct {
	// Usage indicates the ring type:
	//   1 = Exterior boundary
	//   2 = Interior boundary (hole)
	//   3 = Exterior boundary truncated at data limit
	Usage int
	// Coordinates is an array of [longitude, latitude] pairs forming the ring
	Coordinates [][]float64
}

// Geometry represents the spatial representation of a feature
// S-57 §7.3 (31Main.pdf p64): Spatial record structure
type Geometry struct {
	// Type is the geometry type (Point, LineString, or Polygon)
	Type GeometryType
	// Coordinates is an array of [longitude, latitude] pairs
	// Per GeoJSON convention: [lon, lat]
	// DEPRECATED for polygons: Use Rings instead to preserve ring structure
	Coordinates [][]float64
	// Rings contains polygon ring data with usage indicators
	// Only populated for Polygon geometry type
	// First ring with Usage=1 is exterior, Usage=2 are holes, Usage=3 are truncated exterior
	Rings []Ring
}

// constructGeometry builds a Geometry from feature and spatial records
// S-57 §2.1 (31Main.pdf p23): Features reference spatial records to build geometry
func constructGeometry(featureRec *featureRecord, spatialRecords map[spatialKey]*spatialRecord) (Geometry, error) {
	// PRIM=255 means N/A (no geometry) - these are meta-features like C_AGGR, M_COVR, etc.
	// Return empty point geometry for these
	if featureRec.GeomPrim == 255 {
		return Geometry{
			Type:        GeometryTypePoint,
			Coordinates: [][]float64{},
		}, nil
	}

	// If no spatial references, cannot construct geometry
	if len(featureRec.SpatialRefs) == 0 {
		return Geometry{}, &ErrMissingSpatialRecord{
			FeatureID: featureRec.ID,
			SpatialID: 0,
		}
	}

	// Determine geometry type from PRIM field (IHO S-57 §7.6.1, 31Main.pdf p74)
	// PRIM: 1=Point, 2=Line, 3=Area, 255=N/A
	geomType := geomTypeFromPrim(featureRec.GeomPrim)

	// For polygon features (PRIM=3), use VRPT topology resolver
	if geomType == GeometryTypePolygon {
		return constructPolygonGeometry(featureRec, spatialRecords)
	}

	// For Point features (PRIM=1), use only the FIRST spatial ref
	// S-57 §7.6 (31Main.pdf p74): Point features reference a single isolated node
	if geomType == GeometryTypePoint {
		return constructPointGeometry(featureRec, spatialRecords)
	}

	// For LineString features (PRIM=2), collect coordinates from all spatial refs
	// S-57 §7.6 (31Main.pdf p74): Line features may reference edges (RCNM=130) which require topology resolution
	return constructLineStringGeometry(featureRec, spatialRecords)
}

// constructLineStringGeometry builds linestring geometry from spatial references
// S-57 §7.6 (31Main.pdf p74): Line features reference edges (RCNM=130) or connected nodes
func constructLineStringGeometry(featureRec *featureRecord, spatialRecords map[spatialKey]*spatialRecord) (Geometry, error) {
	allCoordsPtr := coordSlicePool.Get().(*[][]float64)
	allCoords := (*allCoordsPtr)[:0]
	defer coordSlicePool.Put(allCoordsPtr)
	resolver := newPolygonBuilder(spatialRecords)

	for _, spatialRef := range featureRec.SpatialRefs {
		// Find the spatial record - try all possible RCNMs since FSPT only gives RCID
		// S-57 spatial records can be: 110=isolated node, 120=connected node, 130=edge, 140=face
		var spatial *spatialRecord
		for _, rcnm := range []int{int(spatialTypeEdge), int(spatialTypeConnectedNode), int(spatialTypeIsolatedNode), int(spatialTypeFace)} {
			key := spatialKey{RCNM: rcnm, RCID: spatialRef.RCID}
			if sp, ok := spatialRecords[key]; ok {
				spatial = sp
				break
			}
		}

		if spatial == nil {
			// Missing spatial record - skip gracefully
			continue
		}

		// If this is an edge (RCNM=130), use full edge resolution including nodes
		if spatial.RecordType == spatialTypeEdge {
			edge, err := resolver.loadEdge(spatial.ID)
			if err != nil {
				continue // Skip edges that can't be loaded
			}
			// Get full edge coordinates with nodes (use orientation from FSPT)
			edgeCoords := resolver.getFullEdgeCoordinates(edge, spatialRef.Orientation)
			for _, coord := range edgeCoords {
				allCoords = append(allCoords, []float64{coord[0], coord[1]})
			}
		} else if len(spatial.Coordinates) > 0 {
			// Direct coordinates from node
			for _, coord := range spatial.Coordinates {
				allCoords = append(allCoords, []float64{coord[0], coord[1]})
			}
		} else if len(spatial.VectorPointers) > 0 {
			// Follow VRPT pointers
			coordsFromPointers := resolveVectorPointers(spatial, spatialRecords)
			allCoords = append(allCoords, coordsFromPointers...)
		}
	}

	if len(allCoords) < 2 {
		// Not enough coordinates for a valid line
		// Return empty geometry (feature will be skipped by caller)
		return Geometry{
			Type:        GeometryTypeLineString,
			Coordinates: [][]float64{},
		}, nil
	}

	result := make([][]float64, len(allCoords))
	copy(result, allCoords)
	return Geometry{
		Type:        GeometryTypeLineString,
		Coordinates: result,
	}, nil
}

// constructPointGeometry builds point geometry from spatial references
// S-57 §7.6 (31Main.pdf p74): Point features can reference:
//   - Single isolated node (RCNM=110) for simple point features
//   - Multiple isolated nodes for multipoint features (e.g., SOUNDG with many soundings)
func constructPointGeometry(featureRec *featureRecord, spatialRecords map[spatialKey]*spatialRecord) (Geometry, error) {
	// Collect coordinates from ALL spatial references
	// For multipoint features like SOUNDG, there can be hundreds of refs
	allCoordsPtr := coordSlicePool.Get().(*[][]float64)
	allCoords := (*allCoordsPtr)[:0]
	defer coordSlicePool.Put(allCoordsPtr)

	for _, spatialRef := range featureRec.SpatialRefs {
		// Resolve the EXACT record the FSPT pointer names (RCNM + RCID). RCID is
		// unique only within an RCNM, so probing by RCID alone can grab an unrelated
		// record of a different type that happens to share the id — e.g. a point
		// feature pointing at connected node 120/X mis-resolving to isolated node
		// 110/X, which puts the feature at a completely different location.
		var spatial *spatialRecord
		if spatialRef.RCNM != 0 {
			if sp, ok := spatialRecords[spatialKey{RCNM: spatialRef.RCNM, RCID: spatialRef.RCID}]; ok {
				spatial = sp
			}
		}
		// Fallback for records with no/unknown RCNM in the pointer: check isolated
		// node first, then connected node (isolated holds SG3D for multipoint SOUNDG).
		if spatial == nil {
			for _, rcnm := range []int{int(spatialTypeIsolatedNode), int(spatialTypeConnectedNode)} {
				key := spatialKey{RCNM: rcnm, RCID: spatialRef.RCID}
				if sp, ok := spatialRecords[key]; ok {
					spatial = sp
					break
				}
			}
		}

		if spatial == nil {
			// Skip missing spatial records (don't fail entire feature)
			continue
		}

		// Get coordinates from this spatial record
		if len(spatial.Coordinates) > 0 {
			// Extract ALL coordinates from spatial record
			// Preserve all dimensions (2D or 3D) - don't strip Z coordinates
			allCoords = append(allCoords, spatial.Coordinates...)
		}
	}

	if len(allCoords) == 0 {
		// All spatial refs were missing or had no coordinates
		// Return empty geometry (feature will be skipped by caller)
		return Geometry{
			Type:        GeometryTypePoint,
			Coordinates: [][]float64{},
		}, nil
	}

	result := make([][]float64, len(allCoords))
	copy(result, allCoords)
	return Geometry{
		Type:        GeometryTypePoint,
		Coordinates: result,
	}, nil
}

// constructPolygonGeometry builds polygon geometry using VRPT topology resolution
// S-57 §7.3 (31Main.pdf p64): Area features use VRPT to reference edge topology
func constructPolygonGeometry(featureRec *featureRecord, spatialRecords map[spatialKey]*spatialRecord) (Geometry, error) {
	// Create polygon builder
	resolver := newPolygonBuilder(spatialRecords)

	// Check if feature references face records (spatial primitives with VRPT)
	// Collect edge references WITH orientation from FSPT
	edgeRefsPtr := spatialRefSlicePool.Get().(*[]spatialRef)
	edgeRefs := (*edgeRefsPtr)[:0]
	defer func() {
		*edgeRefsPtr = (*edgeRefsPtr)[:0]
		spatialRefSlicePool.Put(edgeRefsPtr)
	}()
	for _, fsptRef := range featureRec.SpatialRefs {
		// FSPT references can be to any spatial type - try all RCNMs to find by RCID
		var spatial *spatialRecord
		for _, rcnm := range []int{int(spatialTypeFace), int(spatialTypeEdge), int(spatialTypeConnectedNode), int(spatialTypeIsolatedNode)} {
			key := spatialKey{RCNM: rcnm, RCID: fsptRef.RCID}
			if sp, ok := spatialRecords[key]; ok {
				spatial = sp
				break
			}
		}

		if spatial == nil {
			continue
		}

		// If this is a face record (RCNM=140), collect edge references from VRPT
		if spatial.RecordType == spatialTypeFace {
			for _, ptr := range spatial.VectorPointers {
				// Edge records have RCNM=130
				if ptr.TargetRCNM == int(spatialTypeEdge) {
					// Face VRPT has orientation - use it
					edgeRefs = append(edgeRefs, spatialRef{
						RCID:        ptr.TargetRCID,
						Orientation: ptr.Orientation,
						Usage:       ptr.Usage,
						Mask:        ptr.Mask,
					})
				}
			}
		} else if spatial.RecordType == spatialTypeEdge {
			// Direct edge reference - use FSPT orientation
			edgeRefs = append(edgeRefs, fsptRef)
		}
	}

	// If we have edge references, resolve topology
	if len(edgeRefs) > 0 {
		ringsWithUsage, err := resolver.resolvePolygonWithUsage(edgeRefs)
		if err != nil {
			// VRPT resolution failed - fall back to direct coordinate collection
			// This can happen if topology is incomplete or malformed (e.g., M_COVR meta features)
			// Try to collect coordinates directly from edges
			allCoordsPtr := coordSlicePool.Get().(*[][]float64)
			allCoords := (*allCoordsPtr)[:0]
			defer coordSlicePool.Put(allCoordsPtr)
			for _, edgeRef := range edgeRefs {
				edgeKey := spatialKey{RCNM: int(spatialTypeEdge), RCID: edgeRef.RCID}
				if edge, ok := spatialRecords[edgeKey]; ok && len(edge.Coordinates) > 0 {
					for _, coord := range edge.Coordinates {
						allCoords = append(allCoords, []float64{coord[0], coord[1]})
					}
				}
			}

			if len(allCoords) > 0 {
				allCoords = ensurePolygonClosure(allCoords)
				result := make([][]float64, len(allCoords))
				copy(result, allCoords)
				return Geometry{
					Type:        GeometryTypePolygon,
					Coordinates: result,
					Rings: []Ring{{
						Usage:       1, // Default to exterior
						Coordinates: result,
					}},
				}, nil
			}

			// If we still can't get coordinates from edges, try collecting from ANY spatial record
			// This handles cases where the feature references spatial records that aren't properly linked
			for _, spatialRef := range featureRec.SpatialRefs {
				for key, spatial := range spatialRecords {
					if key.RCID == spatialRef.RCID && len(spatial.Coordinates) > 0 {
						for _, coord := range spatial.Coordinates {
							allCoords = append(allCoords, []float64{coord[0], coord[1]})
						}
					}
				}
			}

			if len(allCoords) > 0 {
				allCoords = ensurePolygonClosure(allCoords)
				result := make([][]float64, len(allCoords))
				copy(result, allCoords)
				return Geometry{
					Type:        GeometryTypePolygon,
					Coordinates: result,
					Rings: []Ring{{
						Usage:       1,
						Coordinates: result,
					}},
				}, nil
			}

			// Last resort: return the error
			return Geometry{}, err
		}

		// Build Rings array with usage indicators
		rings := make([]Ring, len(ringsWithUsage))
		allCoords := make([][]float64, 0)

		for i, ringData := range ringsWithUsage {
			// Convert [][2]float64 to [][]float64
			coords := make([][]float64, len(ringData.coords))
			for j, point := range ringData.coords {
				coords[j] = []float64{point[0], point[1]}
				allCoords = append(allCoords, coords[j]) // Also add to flat list for backward compat
			}

			rings[i] = Ring{
				Usage:       ringData.usage,
				Coordinates: coords,
			}
		}

		// Check if we have enough coordinates for a valid polygon
		if len(allCoords) < 3 {
			// Degenerate polygon - return empty geometry
			return Geometry{
				Type:        GeometryTypePolygon,
				Coordinates: [][]float64{},
				Rings:       []Ring{},
			}, nil
		}

		return Geometry{
			Type:        GeometryTypePolygon,
			Coordinates: allCoords, // Flattened for backward compatibility
			Rings:       rings,     // Structured rings with usage indicators
		}, nil
	}

	// Fallback: No VRPT topology, collect direct coordinates
	allCoordsPtr2 := coordSlicePool.Get().(*[][]float64)
	allCoords := (*allCoordsPtr2)[:0]
	defer coordSlicePool.Put(allCoordsPtr2)
	for _, spatialRef := range featureRec.SpatialRefs {
		// Search by RCID
		for key, spatial := range spatialRecords {
			if key.RCID == spatialRef.RCID && len(spatial.Coordinates) > 0 {
				for _, coord := range spatial.Coordinates {
					allCoords = append(allCoords, []float64{coord[0], coord[1]})
				}
			}
		}
	}

	// Check if we have enough coordinates for a valid polygon
	if len(allCoords) < 3 {
		// Degenerate polygon - return empty geometry
		return Geometry{
			Type:        GeometryTypePolygon,
			Coordinates: [][]float64{},
			Rings:       []Ring{},
		}, nil
	}

	// Ensure polygon closure
	allCoords = ensurePolygonClosure(allCoords)

	result := make([][]float64, len(allCoords))
	copy(result, allCoords)
	return Geometry{
		Type:        GeometryTypePolygon,
		Coordinates: result,
		Rings: []Ring{{
			Usage:       1, // Default to exterior for fallback path
			Coordinates: result,
		}},
	}, nil
}

// geomTypeFromPrim converts PRIM value to GeometryType
// Per IHO S-57 §7.6.1 (31Main.pdf p74): PRIM values are 1=Point, 2=Line, 3=Area, 255=N/A
func geomTypeFromPrim(prim int) GeometryType {
	switch prim {
	case 1: // Point
		return GeometryTypePoint
	case 2: // Line
		return GeometryTypeLineString
	case 3: // Area
		return GeometryTypePolygon
	default: // 255 = N/A or unknown
		return GeometryTypePoint // Default to point if unknown
	}
}

// ensurePolygonClosure ensures a polygon is closed (first coordinate == last)
func ensurePolygonClosure(coords [][]float64) [][]float64 {
	if len(coords) < 3 {
		return coords // Not enough points for polygon
	}

	// Check if already closed
	first := coords[0]
	last := coords[len(coords)-1]

	if len(first) == 2 && len(last) == 2 {
		if first[0] == last[0] && first[1] == last[1] {
			return coords // Already closed
		}
	}

	// Add closing point
	closed := make([][]float64, len(coords)+1)
	copy(closed, coords)
	closed[len(coords)] = []float64{first[0], first[1]}

	return closed
}

// resolveVectorPointers recursively resolves VRPT pointers to collect coordinates
func resolveVectorPointers(spatial *spatialRecord, spatialRecords map[spatialKey]*spatialRecord) [][]float64 {
	visited := visitedMapPool.Get().(map[int64]bool)
	for k := range visited {
		delete(visited, k)
	}
	defer visitedMapPool.Put(visited)
	return resolveVectorPointersRecursive(spatial, spatialRecords, visited)
}

// resolveVectorPointersRecursive implements recursive VRPT resolution with cycle detection
func resolveVectorPointersRecursive(spatial *spatialRecord, spatialRecords map[spatialKey]*spatialRecord, visited map[int64]bool) [][]float64 {
	coordsPtr := coordSlicePool.Get().(*[][]float64)
	coords := (*coordsPtr)[:0]
	defer coordSlicePool.Put(coordsPtr)

	for _, ptr := range spatial.VectorPointers {
		// Check for circular reference
		if visited[ptr.TargetRCID] {
			continue // Skip to prevent infinite loop
		}
		visited[ptr.TargetRCID] = true

		// Lookup using composite key (RCNM, RCID)
		targetKey := spatialKey{RCNM: ptr.TargetRCNM, RCID: ptr.TargetRCID}
		target, ok := spatialRecords[targetKey]
		if !ok {
			continue // Target not found, skip
		}

		// Collect coordinates from target
		if len(target.Coordinates) > 0 {
			// Target has direct coordinates - apply orientation inline
			if ptr.Orientation == 2 { // Reverse
				for i := len(target.Coordinates) - 1; i >= 0; i-- {
					coords = append(coords, []float64{target.Coordinates[i][0], target.Coordinates[i][1]})
				}
			} else { // Forward or null
				for _, coord := range target.Coordinates {
					coords = append(coords, []float64{coord[0], coord[1]})
				}
			}
		} else if len(target.VectorPointers) > 0 {
			// Target has no direct coords, recurse
			targetCoords := resolveVectorPointersRecursive(target, spatialRecords, visited)
			// Apply orientation (reverse if needed)
			if ptr.Orientation == 2 { // Reverse
				for i := len(targetCoords) - 1; i >= 0; i-- {
					coords = append(coords, targetCoords[i])
				}
			} else { // Forward or null
				coords = append(coords, targetCoords...)
			}
		}
	}

	result := make([][]float64, len(coords))
	copy(result, coords)
	return result
}
