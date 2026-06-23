package parser

import "testing"

// Derived coastline-coincident boundary masking
// (S-57 Appendix B.1 Annex A §17 scenario 2).
//
// These tests build synthetic spatial + feature records where a COALNE line and an
// area (DEPARE / LNDARE) are built from the SAME edge RCID — exactly the situation
// in real NOAA cells where the producer never sets FSPT MASK=1. The area's drawn
// boundary edge that shares a COALNE edge RCID must be dropped from BoundaryLines
// when masking is on, while the fill (Rings) and flat Coordinates keep it.

// squareAreaRecords returns the spatial records for a unit square boundary made of
// four direct edges (RCIDs 10..13). Edge 10 is the SHARED coastline edge
// (bottom: (0,0)->(1,0)); the other three close the ring. Connected nodes carry
// the corner coordinates so getFullEdgeCoordinates yields full edge geometry.
func squareAreaRecords() map[spatialKey]*spatialRecord {
	node := func(id int64, lon, lat float64) *spatialRecord {
		return &spatialRecord{ID: id, RecordType: spatialTypeConnectedNode, Coordinates: [][]float64{{lon, lat}}}
	}
	return map[spatialKey]*spatialRecord{
		// Corner nodes of the unit square.
		{RCNM: int(spatialTypeConnectedNode), RCID: 100}: node(100, 0, 0),
		{RCNM: int(spatialTypeConnectedNode), RCID: 101}: node(101, 1, 0),
		{RCNM: int(spatialTypeConnectedNode), RCID: 102}: node(102, 1, 1),
		{RCNM: int(spatialTypeConnectedNode), RCID: 103}: node(103, 0, 1),
		// Four boundary edges. Edge 10 is the shared coastline edge (bottom).
		{RCNM: int(spatialTypeEdge), RCID: 10}: edgeRecord(10, nil, 100, 101), // (0,0)->(1,0) SHARED
		{RCNM: int(spatialTypeEdge), RCID: 11}: edgeRecord(11, nil, 101, 102), // (1,0)->(1,1)
		{RCNM: int(spatialTypeEdge), RCID: 12}: edgeRecord(12, nil, 102, 103), // (1,1)->(0,1)
		{RCNM: int(spatialTypeEdge), RCID: 13}: edgeRecord(13, nil, 103, 100), // (0,1)->(0,0)
	}
}

// squareAreaFeature builds an area (PRIM=3) feature whose boundary is the four
// direct edges of the unit square. The object-class code is cosmetic here:
// constructPolygonGeometry decides masking from its maskCoast argument, not the
// object class (buildChart applies the LNDARE exemption before calling it).
func squareAreaFeature(objClass int) *featureRecord {
	return &featureRecord{
		ObjectClass: objClass,
		GeomPrim:    3, // area
		SpatialRefs: []spatialRef{
			{RCNM: int(spatialTypeEdge), RCID: 10, Orientation: 1, Usage: 1},
			{RCNM: int(spatialTypeEdge), RCID: 11, Orientation: 1, Usage: 1},
			{RCNM: int(spatialTypeEdge), RCID: 12, Orientation: 1, Usage: 1},
			{RCNM: int(spatialTypeEdge), RCID: 13, Orientation: 1, Usage: 1},
		},
	}
}

// ringsHavePoint reports whether any ring's coordinates contain [lon,lat].
func ringsHavePoint(rings []Ring, lon, lat float64) bool {
	for _, ring := range rings {
		if flatHasPoint(ring.Coordinates, lon, lat) {
			return true
		}
	}
	return false
}

// TestCoastlineMaskingDropsSharedEdgeFromBoundaryOnly verifies that, with derived
// masking ON, a DEPARE area sharing edge 10 with COALNE drops that edge from its
// drawn BoundaryLines, but keeps the shared edge in its fill Rings (and flat
// Coordinates). The three non-shared edges remain drawn.
func TestCoastlineMaskingDropsSharedEdgeFromBoundaryOnly(t *testing.T) {
	spatialRecords := squareAreaRecords()
	feat := squareAreaFeature(42)

	coalneEdges := map[int64]bool{10: true} // edge 10 is referenced by COALNE

	g, err := constructPolygonGeometry(feat, spatialRecords, coalneEdges, true)
	if err != nil {
		t.Fatal(err)
	}

	// The shared edge 10 spans (0,0)->(1,0); its UNIQUE midpoint-distinguishing
	// endpoint relative to the other edges is (1,0)... but (1,0) is shared with
	// edge 11's start. The unambiguous test is: the bottom segment (0,0)->(1,0)
	// must not appear as a drawn boundary part. Check that no boundary part
	// contains BOTH (0,0) and (1,0).
	for i, part := range g.BoundaryLines {
		var has00, has10 bool
		for _, p := range part {
			if p[0] == 0 && p[1] == 0 {
				has00 = true
			}
			if p[0] == 1 && p[1] == 0 {
				has10 = true
			}
		}
		if has00 && has10 {
			t.Fatalf("boundary part %d still draws the shared coastline edge (0,0)->(1,0): %v", i, part)
		}
	}

	// Exactly the three non-shared edges remain drawn.
	if len(g.BoundaryLines) != 3 {
		t.Fatalf("expected 3 drawable boundary edges (shared one dropped), got %d: %v", len(g.BoundaryLines), g.BoundaryLines)
	}

	// The three non-shared edges' distinctive coordinates are still drawn.
	if !hasPoint(g.BoundaryLines, 1, 1) {
		t.Errorf("non-shared edge corner (1,1) missing from BoundaryLines")
	}
	if !hasPoint(g.BoundaryLines, 0, 1) {
		t.Errorf("non-shared edge corner (0,1) missing from BoundaryLines")
	}

	// The fill (Rings) MUST still include the shared edge's geometry — both its
	// endpoints (0,0) and (1,0) appear in the ring.
	if !ringsHavePoint(g.Rings, 0, 0) || !ringsHavePoint(g.Rings, 1, 0) {
		t.Errorf("fill Rings dropped the shared edge geometry; Rings=%v", g.Rings)
	}
	// Flat Coordinates (backward-compat fill) likewise keep the shared edge.
	if !flatHasPoint(g.Coordinates, 0, 0) || !flatHasPoint(g.Coordinates, 1, 0) {
		t.Errorf("flat Coordinates dropped the shared edge geometry; Coordinates=%v", g.Coordinates)
	}
}

// TestCoastlineMaskingOffKeepsSharedEdge verifies that with the option OFF the
// DEPARE keeps the shared edge in BoundaryLines (all four edges drawn).
func TestCoastlineMaskingOffKeepsSharedEdge(t *testing.T) {
	spatialRecords := squareAreaRecords()
	feat := squareAreaFeature(42)
	coalneEdges := map[int64]bool{10: true}

	g, err := constructPolygonGeometry(feat, spatialRecords, coalneEdges, false)
	if err != nil {
		t.Fatal(err)
	}

	if len(g.BoundaryLines) != 4 {
		t.Fatalf("masking OFF: expected 4 drawable boundary edges, got %d: %v", len(g.BoundaryLines), g.BoundaryLines)
	}
	// The shared bottom edge (0,0)->(1,0) must still be drawn as one part.
	var found bool
	for _, part := range g.BoundaryLines {
		var has00, has10 bool
		for _, p := range part {
			if p[0] == 0 && p[1] == 0 {
				has00 = true
			}
			if p[0] == 1 && p[1] == 0 {
				has10 = true
			}
		}
		if has00 && has10 {
			found = true
		}
	}
	if !found {
		t.Errorf("masking OFF: shared coastline edge (0,0)->(1,0) should still be drawn; BoundaryLines=%v", g.BoundaryLines)
	}
}

// TestCoastDefinerSet locks in which classes define the coast. They are BOTH the
// edge-set contributors (buildChart) AND exempt from masking. COALNE alone left
// stray boundary "chevrons" where the NOAA land/water line is encoded only as an
// LNDARE or SLCONS edge, so the set was widened to all three.
func TestCoastDefinerSet(t *testing.T) {
	for _, c := range []string{"COALNE", "LNDARE", "SLCONS"} {
		if !isCoastlineMaskExempt(c) {
			t.Errorf("%s must be a coast-definer (exempt + edge-set contributor)", c)
		}
	}
	for _, c := range []string{"DEPARE", "SEAARE", "RESARE", "CTNARE"} {
		if isCoastlineMaskExempt(c) {
			t.Errorf("%s must NOT be a coast-definer", c)
		}
	}
}

// TestCoastlineMaskingExemptsLNDARE verifies the coast-definer exemption: an LNDARE
// area sharing the same edge KEEPS it in BoundaryLines even when masking is on. The
// exemption is enforced by buildChart (which sets maskCoast=false for LNDARE), so we
// call constructPolygonGeometry with maskCoast=false to mirror that path AND assert
// isCoastlineMaskExempt classifies LNDARE.
func TestCoastlineMaskingExemptsLNDARE(t *testing.T) {
	if !isCoastlineMaskExempt("LNDARE") {
		t.Fatalf("LNDARE must be exempt from coastline masking")
	}
	if isCoastlineMaskExempt("DEPARE") {
		t.Fatalf("DEPARE must NOT be exempt from coastline masking")
	}

	spatialRecords := squareAreaRecords()
	feat := squareAreaFeature(71)
	coalneEdges := map[int64]bool{10: true}

	// buildChart computes maskCoast=false for exempt classes; mirror that here.
	maskCoast := !isCoastlineMaskExempt("LNDARE") // = false
	g, err := constructPolygonGeometry(feat, spatialRecords, coalneEdges, maskCoast)
	if err != nil {
		t.Fatal(err)
	}

	if len(g.BoundaryLines) != 4 {
		t.Fatalf("LNDARE (exempt): expected 4 drawable boundary edges, got %d: %v", len(g.BoundaryLines), g.BoundaryLines)
	}
	var found bool
	for _, part := range g.BoundaryLines {
		var has00, has10 bool
		for _, p := range part {
			if p[0] == 0 && p[1] == 0 {
				has00 = true
			}
			if p[0] == 1 && p[1] == 0 {
				has10 = true
			}
		}
		if has00 && has10 {
			found = true
		}
	}
	if !found {
		t.Errorf("LNDARE (exempt): shared coastline edge (0,0)->(1,0) should still be drawn; BoundaryLines=%v", g.BoundaryLines)
	}
}
