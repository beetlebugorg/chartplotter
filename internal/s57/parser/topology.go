package parser

// topology.go - VRPT (Vector Record Pointer Table) topology resolution
// Implements S-57 Edition 3.1 polygon construction from edge references

// spatialKey uniquely identifies a spatial record by (RCNM, RCID) pair
// S-57 §2.2.2 (31Main.pdf p27): RCID is unique within a record type, not globally
type spatialKey struct {
	RCNM int   // Record name (110=node, 120=connected node, 130=edge, 140=face)
	RCID int64 // Record ID (unique within RCNM type)
}

// edge represents a spatial edge record with connectivity information
// S-57 §5.1.3.2 (31Main.pdf p54): Edges connect nodes to form polygon boundaries
type edge struct {
	ID          int64        // Edge record ID (RCID)
	Points      [][2]float64 // Coordinate points along the edge [lon, lat]
	StartNodeID int64        // ID of starting node
	EndNodeID   int64        // ID of ending node
}

// polygonBuilder constructs polygon geometries from topological primitives (edges/nodes)
// Caches edges to avoid redundant lookups during ring construction
type polygonBuilder struct {
	spatialRecords map[spatialKey]*spatialRecord // Spatial records indexed by (RCNM, RCID)
	edgeCache      map[int64]*edge               // Cached edges for reuse
}

// newPolygonBuilder creates a new polygon builder with given spatial records
func newPolygonBuilder(spatialRecords map[spatialKey]*spatialRecord) *polygonBuilder {
	return &polygonBuilder{
		spatialRecords: spatialRecords,
		edgeCache:      make(map[int64]*edge),
	}
}

// getNode retrieves a node's coordinates from spatial records
// Tries connected node first, then isolated node
func (r *polygonBuilder) getNode(nodeID int64) *spatialRecord {
	// Try connected node
	nodeKey := spatialKey{RCNM: int(spatialTypeConnectedNode), RCID: nodeID}
	if node, ok := r.spatialRecords[nodeKey]; ok && len(node.Coordinates) > 0 {
		return node
	}
	// Try isolated node
	nodeKey = spatialKey{RCNM: int(spatialTypeIsolatedNode), RCID: nodeID}
	if node, ok := r.spatialRecords[nodeKey]; ok && len(node.Coordinates) > 0 {
		return node
	}
	return nil
}

// getFullEdgeCoordinates builds full edge coordinates: start node + SG2D + end node
// Reverses the entire array if orientation==2 (like marinejet does)
func (r *polygonBuilder) getFullEdgeCoordinates(edge *edge, orientation int) [][2]float64 {
	coords := make([][2]float64, 0)

	// Add start node
	if edge.StartNodeID != 0 {
		if node := r.getNode(edge.StartNodeID); node != nil && len(node.Coordinates) > 0 {
			// Extract 2D coordinate (first 2 values) from variable-length coordinate
			coord := node.Coordinates[0]
			if len(coord) >= 2 {
				coords = append(coords, [2]float64{coord[0], coord[1]})
			}
		}
	}

	// Add SG2D intermediate points
	coords = append(coords, edge.Points...)

	// Add end node
	if edge.EndNodeID != 0 {
		if node := r.getNode(edge.EndNodeID); node != nil && len(node.Coordinates) > 0 {
			// Extract 2D coordinate (first 2 values) from variable-length coordinate
			coord := node.Coordinates[0]
			if len(coord) >= 2 {
				coords = append(coords, [2]float64{coord[0], coord[1]})
			}
		}
	}

	// Reverse if orientation is 2
	if orientation == 2 {
		reversed := make([][2]float64, len(coords))
		for i, coord := range coords {
			reversed[len(coords)-1-i] = coord
		}
		return reversed
	}

	return coords
}

// drawableBoundaryLines builds the polylines of a polygon's DRAWABLE boundary
// edges. Per S-52 PresLib §8.6.2, edges with the FSPT MASK subfield = {1} or the
// USAG subfield = {3} (cell-boundary / data-limit edges) must not be drawn — so
// they are excluded here. They remain in the fill rings (§8.6.3). One polyline
// per drawable edge (oriented per its FSPT orientation).
//
// DERIVED coastline-coincident masking (S-57 App. B.1 Annex A §17 scn 2): when
// maskCoast is true, an edge whose RCID is in coalneEdges (i.e. the same edge
// record a COALNE feature is built from) is ALSO suppressed. NOAA producers never
// set FSPT MASK=1 for these, so we derive the suppression from the shared edge
// RCID. This affects only the drawn boundary; the fill rings are untouched.
func (r *polygonBuilder) drawableBoundaryLines(edgeRefs []spatialRef, coalneEdges map[int64]bool, maskCoast bool) [][][]float64 {
	// Non-nil even when every edge is masked, so the renderer can tell "computed,
	// all edges suppressed" (draw nothing) from "not computed" (stroke the rings).
	lines := make([][][]float64, 0, len(edgeRefs))
	for _, er := range edgeRefs {
		if er.Mask == 1 || er.Usage == 3 {
			continue // masked or data-limit edge — must not be drawn
		}
		if maskCoast && coalneEdges[er.RCID] {
			continue // edge coincident with the coastline (derived) — must not be drawn
		}
		edge, err := r.loadEdge(er.RCID)
		if err != nil || edge == nil {
			continue
		}
		coords := r.getFullEdgeCoordinates(edge, er.Orientation)
		if len(coords) < 2 {
			continue
		}
		line := make([][]float64, len(coords))
		for i, c := range coords {
			line[i] = []float64{c[0], c[1]}
		}
		lines = append(lines, line)
	}
	return lines
}

// boundaryQuapos returns the feature's effective QUAPOS over its DRAWN boundary
// edges — the same edge selection as drawableBoundaryLines (S-52 §8.6.2). Returns
// the low-accuracy value when the majority of drawn edges are low accuracy, else
// 0. (QUAPOS is a spatial-level attribute on the edge records.)
func (r *polygonBuilder) boundaryQuapos(edgeRefs []spatialRef, coalneEdges map[int64]bool, maskCoast bool) int {
	var total, low, lowVal int
	for _, er := range edgeRefs {
		if er.Mask == 1 || er.Usage == 3 {
			continue
		}
		if maskCoast && coalneEdges[er.RCID] {
			continue
		}
		sp := r.spatialRecords[spatialKey{RCNM: int(spatialTypeEdge), RCID: er.RCID}]
		if sp == nil {
			continue
		}
		total++
		if isLowAccuracyQuapos(sp.Quapos) {
			low++
			lowVal = sp.Quapos
		}
	}
	if total > 0 && low*2 > total {
		return lowVal
	}
	return 0
}

// loadEdge loads an edge from spatial records, with caching
// Returns cached edge if already loaded, otherwise loads from spatial record
func (r *polygonBuilder) loadEdge(edgeID int64) (*edge, error) {
	// Check cache first
	if edge, ok := r.edgeCache[edgeID]; ok {
		return edge, nil
	}

	// Load from spatial records using composite key (RCNM=130 for edges)
	edgeKey := spatialKey{RCNM: int(spatialTypeEdge), RCID: edgeID}
	spatial, ok := r.spatialRecords[edgeKey]
	if !ok {
		return nil, &ErrMissingSpatialRecord{
			FeatureID: 0, // Feature ID not known at this level
			SpatialID: edgeID,
		}
	}

	// Verify this is an edge record (RCNM = 130)
	if spatial.RecordType != spatialTypeEdge {
		return nil, &ErrInvalidSpatialRecord{
			SpatialID: edgeID,
			Reason:    "expected edge record (RCNM=130)",
		}
	}

	// Extract node connectivity from vector pointers
	// S-57 §5.1.3.2 (31Main.pdf p54): Edges must reference nodes via VRPT with topology indicators:
	//   B{1} = Beginning node (required)
	//   E{2} = End node (required)
	//   S{3} = Left face (required in full topology)
	//   D{4} = Right face (required in full topology)
	// References must appear in sequence: B, E, S, D
	var startNodeID, endNodeID int64
	for _, ptr := range spatial.VectorPointers {
		// Node records have RCNM = 110 (isolated) or 120 (connected)
		if ptr.TargetRCNM == int(spatialTypeIsolatedNode) || ptr.TargetRCNM == int(spatialTypeConnectedNode) {
			if startNodeID == 0 {
				startNodeID = ptr.TargetRCID
			} else if endNodeID == 0 {
				endNodeID = ptr.TargetRCID
			}
		}
	}

	// Extract edge geometry per S-57 §5.1.4.4 (31Main.pdf p56):
	// "The geometry of the connected node is not part of the edge"
	// This means edge.Points contains ONLY the SG2D intermediate shape points
	// Nodes are stored separately and referenced via VRPT

	// Edge.Points = SG2D coordinates only (may be empty for straight-line edges)
	// Convert variable-length coordinates to fixed 2D coordinates
	points := make([][2]float64, 0, len(spatial.Coordinates))
	for _, coord := range spatial.Coordinates {
		if len(coord) >= 2 {
			points = append(points, [2]float64{coord[0], coord[1]})
		}
	}

	// Create edge
	newEdge := &edge{
		ID:          edgeID,
		Points:      points,
		StartNodeID: startNodeID,
		EndNodeID:   endNodeID,
	}

	// Cache for reuse
	r.edgeCache[edgeID] = newEdge

	return newEdge, nil
}

// resolvePolygon constructs polygon rings from edge references via VRPT topology
// IMPORTANT: Despite S-57 §4.7.3 (31Main.pdf p50) saying edges "must be referenced sequentially",
// real-world ENC files do NOT provide edges in sequential order. We must follow
// topology graph by matching node connectivity.
func (r *polygonBuilder) resolvePolygon(edgeRefs []spatialRef) ([][][2]float64, error) {
	rings, err := r.resolvePolygonWithUsage(edgeRefs)
	if err != nil {
		return nil, err
	}

	// Extract coordinates only for backward compatibility
	coords := make([][][2]float64, len(rings))
	for i, ring := range rings {
		coords[i] = ring.coords
	}
	return coords, nil
}

// resolvePolygonWithUsage constructs polygon rings with usage indicators
func (r *polygonBuilder) resolvePolygonWithUsage(edgeRefs []spatialRef) ([]ringWithUsage, error) {
	if len(edgeRefs) == 0 {
		return nil, &ErrInvalidGeometry{
			Reason: "no edge references provided",
		}
	}

	// Pre-load all edges and store with their orientations
	edgeOrientations := make(map[int64]int) // edgeID -> orientation
	for _, edgeRef := range edgeRefs {
		if _, err := r.loadEdge(edgeRef.RCID); err != nil {
			// Skip edges that fail to load
			continue
		}
		edgeOrientations[edgeRef.RCID] = edgeRef.Orientation
	}

	// Build rings by following topology graph
	return r.buildRingsWithUsage(edgeRefs, edgeOrientations)
}

// ringWithUsage holds a ring and its usage indicator
type ringWithUsage struct {
	coords [][2]float64
	usage  int
}

// buildRingsWithUsage constructs polygon rings from an area's edges.
// S-57 §2.2.8: USAG indicates ring type (1=Exterior, 2=Interior, 3=Truncated).
//
// Edges are assembled into rings by TOPOLOGY — matching shared endpoints — NOT by
// raw FSPT order. Although §4.7.3 says boundary edges "must be referenced
// sequentially", real ENCs routinely list them out of order, and an area commonly
// comprises several disjoint loops (an exterior plus island holes). The old code
// concatenated edges in FSPT order and only ended a ring on an exact match back to
// the start coord; when a loop's closure went undetected it appended the next,
// unrelated edge anyway, drawing a straight connector segment between them — the
// long diagonal lines that slashed across large coastal DEPAREs. Here each edge's
// endpoints (the connected-node coordinates, bit-identical across the two edges
// that meet at a node) are indexed, and rings are grown by following those shared
// endpoints, reversing an edge when it connects at its far end, and starting a new
// ring whenever the current one closes or dead-ends.
func (r *polygonBuilder) buildRingsWithUsage(edgeRefs []spatialRef, orientations map[int64]int) ([]ringWithUsage, error) {
	_ = orientations // FSPT orientation is read per-edge below via getFullEdgeCoordinates
	if len(edgeRefs) == 0 {
		return nil, &ErrInvalidGeometry{Reason: "no edge references provided"}
	}

	// Resolve each FSPT edge to its full coordinate polyline (node + SG2D + node),
	// in FSPT orientation, tagged with its ring usage.
	type seg struct {
		coords [][2]float64
		usage  int
		used   bool
	}
	segs := make([]seg, 0, len(edgeRefs))
	for _, edgeRef := range edgeRefs {
		edge, err := r.loadEdge(edgeRef.RCID)
		if err != nil || edge == nil {
			continue // skip failed / deleted edges
		}
		coords := r.getFullEdgeCoordinates(edge, edgeRef.Orientation)
		if len(coords) < 2 {
			continue
		}
		usage := edgeRef.Usage
		if usage == 0 {
			usage = 1 // default to exterior
		}
		segs = append(segs, seg{coords: coords, usage: usage})
	}
	if len(segs) == 0 {
		return nil, &ErrInvalidGeometry{Reason: "no valid rings could be constructed"}
	}

	// Index both endpoints of every segment so a continuation can be found in O(1).
	endpoints := make(map[[2]float64][]int, len(segs)*2)
	for i, s := range segs {
		a, b := s.coords[0], s.coords[len(s.coords)-1]
		endpoints[a] = append(endpoints[a], i)
		if b != a {
			endpoints[b] = append(endpoints[b], i)
		}
	}
	// nextAt returns an unused segment touching p, or -1.
	nextAt := func(p [2]float64) int {
		for _, j := range endpoints[p] {
			if !segs[j].used {
				return j
			}
		}
		return -1
	}
	// appendSeg joins src to ring, dropping src's leading point when it duplicates
	// the ring's tail (the shared connecting node).
	appendSeg := func(ring, src [][2]float64) [][2]float64 {
		if len(ring) > 0 && len(src) > 0 && ring[len(ring)-1] == src[0] {
			src = src[1:]
		}
		return append(ring, src...)
	}

	rings := make([]ringWithUsage, 0)
	for i := range segs {
		if segs[i].used {
			continue
		}
		segs[i].used = true
		ringUsage := segs[i].usage
		ring := append([][2]float64(nil), segs[i].coords...)
		start := ring[0]

		for {
			tail := ring[len(ring)-1]
			if tail == start {
				break // ring closed
			}
			j := nextAt(tail)
			if j < 0 {
				break // dead end — no edge continues this ring
			}
			next := segs[j].coords
			if next[0] != tail { // connects at its far end → traverse it reversed
				next = reverseCoords(next)
			}
			ring = appendSeg(ring, next)
			segs[j].used = true
		}

		if len(ring) >= 2 {
			if ring[len(ring)-1] != ring[0] {
				ring = append(ring, ring[0]) // close geometrically if it dead-ended
			}
			if len(ring) >= 3 {
				rings = append(rings, ringWithUsage{coords: ring, usage: ringUsage})
			}
		}
	}

	if len(rings) == 0 {
		return nil, &ErrInvalidGeometry{Reason: "no valid rings could be constructed"}
	}

	// GeoJSON-style order: exterior (1), truncated (3), then interior holes (2).
	sortedRings := make([]ringWithUsage, 0, len(rings))
	for _, usage := range []int{1, 3, 2} {
		for _, ring := range rings {
			if ring.usage == usage {
				sortedRings = append(sortedRings, ring)
			}
		}
	}
	return sortedRings, nil
}

// reverseCoords returns a new slice with the points in reverse order.
func reverseCoords(c [][2]float64) [][2]float64 {
	out := make([][2]float64, len(c))
	for i, p := range c {
		out[len(c)-1-i] = p
	}
	return out
}

// isRingClosed checks if a ring is properly closed
func isRingClosed(ring [][2]float64) bool {
	if len(ring) < 3 {
		return false
	}
	first := ring[0]
	last := ring[len(ring)-1]
	return first[0] == last[0] && first[1] == last[1]
}
