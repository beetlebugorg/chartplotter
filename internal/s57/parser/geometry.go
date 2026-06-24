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

	// BoundaryLines holds the polylines of a polygon's DRAWABLE boundary edges:
	// edges that are NOT masked (FSPT MASK={1}) and NOT cell-boundary/data-limit
	// edges (USAG={3}). Per S-52 PresLib §8.6.2 those edges "must not be drawn",
	// while the area fill must still include them (§8.6.3) — so the fill uses
	// Rings (complete) and the border stroke uses BoundaryLines (edges dropped).
	// One polyline per drawable edge; empty/nil ⇒ fall back to stroking Rings.
	BoundaryLines [][][]float64

	// Lines holds the DRAWABLE polylines of a LINE feature: the same edge chain as
	// Coordinates but with masked (FSPT MASK={1}) and data-limit/cell-boundary
	// (USAG={3}) edges removed (S-52 PresLib §8.6.2 — those edges must not be
	// drawn). Because dropping a mid-line edge splits the line, this is MULTI-PART:
	// each element is one contiguous drawn polyline of [lon,lat] points. The flat
	// Coordinates field still carries the FULL concatenation (all edges) for
	// backward compatibility. Empty/nil ⇒ no masking applied (or topology didn't
	// resolve) → fall back to stroking Coordinates.
	Lines [][][]float64
}

// constructGeometry builds a Geometry from feature and spatial records.
// S-57 §2.1 (31Main.pdf p23): Features reference spatial records to build geometry.
//
// coalneEdges and maskCoast drive DERIVED coastline-coincident boundary masking
// (see ParseOptions.MaskCoastlineCoincidentBoundaries). They are consulted only for
// polygon features; point and line constructors ignore them. When maskCoast is true,
// any boundary edge whose RCID is in coalneEdges is dropped from the polygon's drawn
// BoundaryLines (the fill Rings / flat Coordinates are unaffected).
func constructGeometry(featureRec *featureRecord, spatialRecords map[spatialKey]*spatialRecord, coalneEdges map[int64]bool, maskCoast bool) (Geometry, error) {
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
		return constructPolygonGeometry(featureRec, spatialRecords, coalneEdges, maskCoast)
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

	// Drawable multi-part line geometry (S-52 PresLib §8.6.2): masked / data-limit
	// edges are excluded from what is drawn, but still go into the flat allCoords
	// (backward compat). lineParts accumulates contiguous drawn polylines: a new
	// part is started whenever the chain is broken — either by a skipped (masked)
	// edge, or by a drawable edge whose first point does not continue the previous
	// part's last point. sawMask records whether ANY edge carried masking info, so
	// that an unmasked line leaves Lines nil (callers then stroke allCoords).
	var lineParts [][][]float64
	var curPart [][]float64
	chainBroken := true // force a new part on the first drawable edge
	sawMask := false
	flushPart := func() {
		if len(curPart) >= 2 {
			lineParts = append(lineParts, curPart)
		}
		curPart = nil
	}

	for _, spatialRef := range featureRec.SpatialRefs {
		// Resolve the EXACT record the FSPT pointer names (RCNM + RCID). RCID is
		// unique only WITHIN an RCNM, so probing by RCID across record types can
		// grab an unrelated record that reused the id. This bites hardest after an
		// update deletes an edge: e.g. a COALNE references edge 130/78, an update
		// (.011) deletes edge 130/78, but isolated node 110/78 still exists — the
		// old RCID-only search then resolved the dangling edge-ref to that node and
		// spliced its far-off coordinate into the coastline (visible as a long line
		// slashing across the chart). Trust the pointer's RCNM; if that exact record
		// is gone, drop the ref (the line is simply shorter) rather than guess.
		var spatial *spatialRecord
		if spatialRef.RCNM != 0 {
			if sp, ok := spatialRecords[spatialKey{RCNM: spatialRef.RCNM, RCID: spatialRef.RCID}]; ok {
				spatial = sp
			}
		} else {
			// Unknown RCNM in the pointer: fall back to searching by RCID.
			for _, rcnm := range []int{int(spatialTypeEdge), int(spatialTypeConnectedNode), int(spatialTypeIsolatedNode), int(spatialTypeFace)} {
				key := spatialKey{RCNM: rcnm, RCID: spatialRef.RCID}
				if sp, ok := spatialRecords[key]; ok {
					spatial = sp
					break
				}
			}
		}

		if spatial == nil {
			// Missing spatial record (or deleted by an update) - skip gracefully
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

			// Any edge carrying explicit MASK/USAG info means the producer encoded
			// masking for this feature; expose the drawable Lines so the renderer
			// honours §8.6.2. (Mask==0 / Usage==0 on every edge ⇒ no info ⇒ leave
			// Lines nil and fall back to the flat Coordinates, unchanged behaviour.)
			if spatialRef.Mask != 0 || spatialRef.Usage != 0 {
				sawMask = true
			}

			// Drawable-part accounting (S-52 §8.6.2). A masked / data-limit edge is
			// dropped from the drawn geometry: end the current part and mark the chain
			// broken so the next drawable edge starts a fresh part.
			if spatialRef.Mask == 1 || spatialRef.Usage == 3 {
				flushPart()
				chainBroken = true
				continue
			}
			if len(edgeCoords) < 2 {
				// Degenerate edge: nothing to draw, but it still interrupts continuity.
				flushPart()
				chainBroken = true
				continue
			}
			first := []float64{edgeCoords[0][0], edgeCoords[0][1]}
			// Start a new part if the chain was broken (masked gap) or this edge does
			// not continue the previous part's last point.
			if chainBroken || len(curPart) == 0 ||
				curPart[len(curPart)-1][0] != first[0] || curPart[len(curPart)-1][1] != first[1] {
				flushPart()
				curPart = make([][]float64, 0, len(edgeCoords))
				curPart = append(curPart, first)
				for _, coord := range edgeCoords[1:] {
					curPart = append(curPart, []float64{coord[0], coord[1]})
				}
			} else {
				// Continues the current part: skip the duplicated shared node.
				for _, coord := range edgeCoords[1:] {
					curPart = append(curPart, []float64{coord[0], coord[1]})
				}
			}
			chainBroken = false
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

	flushPart()

	result := make([][]float64, len(allCoords))
	copy(result, allCoords)
	geom := Geometry{
		Type:        GeometryTypeLineString,
		Coordinates: result,
	}
	// Only expose drawable parts when masking actually applied. Without masking,
	// leaving Lines nil keeps existing renderers stroking the flat Coordinates
	// exactly as before (no behaviour change for the common, unmasked case).
	if sawMask {
		geom.Lines = lineParts
	}
	return geom, nil
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

// collectRefCoords appends, to out, the coordinates of the spatial records named
// by the feature's FSPT pointers. It honours each pointer's RCNM — RCID is unique
// only WITHIN an RCNM — so a dangling reference (e.g. to an edge an update has
// deleted) does NOT scavenge an unrelated node/edge that reused the same RCID.
// That collision turned a bridge whose edges were all deleted by update .007 into
// a scattered stray ring (5 points pulled from connected/isolated nodes 120/110
// that happened to share the deleted edges' RCIDs). With no valid records the
// caller sees too few coords and drops the feature, which is correct.
func collectRefCoords(refs []spatialRef, spatialRecords map[spatialKey]*spatialRecord, out [][]float64) [][]float64 {
	for _, ref := range refs {
		if ref.RCNM != 0 {
			if sp, ok := spatialRecords[spatialKey{RCNM: ref.RCNM, RCID: ref.RCID}]; ok {
				for _, coord := range sp.Coordinates {
					out = append(out, []float64{coord[0], coord[1]})
				}
			}
			continue
		}
		// Unknown RCNM in the pointer: fall back to an RCID search across types.
		for key, spatial := range spatialRecords {
			if key.RCID == ref.RCID && len(spatial.Coordinates) > 0 {
				for _, coord := range spatial.Coordinates {
					out = append(out, []float64{coord[0], coord[1]})
				}
			}
		}
	}
	return out
}

// constructPolygonGeometry builds polygon geometry using VRPT topology resolution
// S-57 §7.3 (31Main.pdf p64): Area features use VRPT to reference edge topology.
//
// coalneEdges/maskCoast drive derived coastline-coincident boundary masking: when
// maskCoast is true, boundary edges whose RCID is in coalneEdges are excluded from
// the drawn BoundaryLines (fill Rings / flat Coordinates remain complete).
func constructPolygonGeometry(featureRec *featureRecord, spatialRecords map[spatialKey]*spatialRecord, coalneEdges map[int64]bool, maskCoast bool) (Geometry, error) {
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
		// Resolve the EXACT record the FSPT pointer names (RCNM + RCID) — RCID is
		// unique only within an RCNM. Probing by RCID across types can resolve a
		// dangling/deleted reference to an unrelated record that reused the id (see
		// constructLineStringGeometry). Trust the pointer's RCNM; fall back to the
		// RCID search only when the pointer carries no/unknown RCNM.
		var spatial *spatialRecord
		if fsptRef.RCNM != 0 {
			if sp, ok := spatialRecords[spatialKey{RCNM: fsptRef.RCNM, RCID: fsptRef.RCID}]; ok {
				spatial = sp
			}
		} else {
			for _, rcnm := range []int{int(spatialTypeFace), int(spatialTypeEdge), int(spatialTypeConnectedNode), int(spatialTypeIsolatedNode)} {
				key := spatialKey{RCNM: rcnm, RCID: fsptRef.RCID}
				if sp, ok := spatialRecords[key]; ok {
					spatial = sp
					break
				}
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

			// If we still can't get coordinates from edges, collect directly from the
			// records the pointers name (respecting RCNM — see collectRefCoords).
			allCoords = collectRefCoords(featureRec.SpatialRefs, spatialRecords, allCoords)

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
			Type:          GeometryTypePolygon,
			Coordinates:   allCoords, // Flattened for backward compatibility
			Rings:         rings,     // Structured rings with usage indicators
			BoundaryLines: resolver.drawableBoundaryLines(edgeRefs, coalneEdges, maskCoast),
		}, nil
	}

	// Fallback: No VRPT topology, collect direct coordinates (respecting RCNM so a
	// reference to a deleted edge doesn't scavenge an unrelated record — see
	// collectRefCoords).
	allCoordsPtr2 := coordSlicePool.Get().(*[][]float64)
	allCoords := (*allCoordsPtr2)[:0]
	defer coordSlicePool.Put(allCoordsPtr2)
	allCoords = collectRefCoords(featureRec.SpatialRefs, spatialRecords, allCoords)

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
