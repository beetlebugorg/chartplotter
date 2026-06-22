package parser

import "testing"

// edgeRecord builds an edge spatial record with the given RCID, SG2D shape points
// and (optional) start/end connected-node pointers. Mirrors how the existing
// topology tests construct edge spatialRecords.
func edgeRecord(rcid int64, points [][]float64, startNode, endNode int64) *spatialRecord {
	rec := &spatialRecord{
		ID:          rcid,
		RecordType:  spatialTypeEdge,
		Coordinates: points,
	}
	if startNode != 0 {
		rec.VectorPointers = append(rec.VectorPointers, vectorPointer{TargetRCNM: int(spatialTypeConnectedNode), TargetRCID: startNode})
	}
	if endNode != 0 {
		rec.VectorPointers = append(rec.VectorPointers, vectorPointer{TargetRCNM: int(spatialTypeConnectedNode), TargetRCID: endNode})
	}
	return rec
}

// hasPoint reports whether any drawable line part contains the [lon,lat] point.
func hasPoint(parts [][][]float64, lon, lat float64) bool {
	for _, part := range parts {
		for _, p := range part {
			if len(p) >= 2 && p[0] == lon && p[1] == lat {
				return true
			}
		}
	}
	return false
}

// flatHasPoint reports whether the flat Coordinates contain the [lon,lat] point.
func flatHasPoint(coords [][]float64, lon, lat float64) bool {
	for _, p := range coords {
		if len(p) >= 2 && p[0] == lon && p[1] == lat {
			return true
		}
	}
	return false
}

// TestLineMaskingSplitsAtMaskedEdge verifies S-52 PresLib §8.6.2: a LINE feature
// whose MIDDLE edge carries FSPT MASK={1} must NOT have that edge drawn, and the
// resulting drawable geometry splits into two disjoint parts. The flat Coordinates
// must still contain every edge (backward compatibility).
func TestLineMaskingSplitsAtMaskedEdge(t *testing.T) {
	// Three contiguous edges forming a single chain:
	//   e1: (0,0)->(1,0)   e2 (MASKED): (1,0)->(2,0)   e3: (2,0)->(3,0)
	spatialRecords := map[spatialKey]*spatialRecord{
		{RCNM: int(spatialTypeEdge), RCID: 1}: edgeRecord(1, [][]float64{{0, 0}, {1, 0}}, 0, 0),
		{RCNM: int(spatialTypeEdge), RCID: 2}: edgeRecord(2, [][]float64{{1, 0}, {2, 0}}, 0, 0),
		{RCNM: int(spatialTypeEdge), RCID: 3}: edgeRecord(3, [][]float64{{2, 0}, {3, 0}}, 0, 0),
	}
	feat := &featureRecord{
		GeomPrim: 2, // line
		SpatialRefs: []spatialRef{
			{RCNM: int(spatialTypeEdge), RCID: 1, Orientation: 1, Mask: 2},
			{RCNM: int(spatialTypeEdge), RCID: 2, Orientation: 1, Mask: 1}, // masked middle edge
			{RCNM: int(spatialTypeEdge), RCID: 3, Orientation: 1, Mask: 2},
		},
	}

	g, err := constructLineStringGeometry(feat, spatialRecords)
	if err != nil {
		t.Fatal(err)
	}

	// Masking applied → exactly two drawable parts.
	if len(g.Lines) != 2 {
		t.Fatalf("expected 2 drawable parts, got %d: %v", len(g.Lines), g.Lines)
	}

	// The masked edge's interior coordinates (1,0)->(2,0) must NOT appear as a drawn
	// segment. The shared endpoints (1,0) and (2,0) are also the endpoints of the
	// neighbouring drawable edges, so we assert the masked edge is absent by checking
	// the part split is clean: part 0 ends at (1,0), part 1 starts at (2,0), and no
	// part bridges (1,0)->(2,0).
	if got := g.Lines[0]; len(got) != 2 || got[0][0] != 0 || got[1][0] != 1 {
		t.Fatalf("part 0 = %v, want [[0,0],[1,0]]", got)
	}
	if got := g.Lines[1]; len(got) != 2 || got[0][0] != 2 || got[1][0] != 3 {
		t.Fatalf("part 1 = %v, want [[2,0],[3,0]]", got)
	}
	// No single drawable part may contain both (1,0) and (2,0) (that would mean the
	// masked edge was drawn as a bridge).
	for i, part := range g.Lines {
		var has1, has2 bool
		for _, p := range part {
			if p[0] == 1 && p[1] == 0 {
				has1 = true
			}
			if p[0] == 2 && p[1] == 0 {
				has2 = true
			}
		}
		if has1 && has2 {
			t.Fatalf("part %d bridges the masked edge (1,0)->(2,0): %v", i, part)
		}
	}

	// Backward compat: the flat Coordinates must still contain ALL edges, including
	// the masked edge's points.
	if !flatHasPoint(g.Coordinates, 1, 0) || !flatHasPoint(g.Coordinates, 2, 0) {
		t.Fatalf("flat Coordinates missing masked edge endpoints: %v", g.Coordinates)
	}
	if !flatHasPoint(g.Coordinates, 0, 0) || !flatHasPoint(g.Coordinates, 3, 0) {
		t.Fatalf("flat Coordinates missing outer endpoints: %v", g.Coordinates)
	}
}

// TestLineMaskingUnmaskedSinglePart verifies a fully-unmasked line yields a single
// contiguous part, and that an unmasked line (no masking info at all) leaves Lines
// nil so existing renderers fall back to the flat Coordinates (no behaviour change).
func TestLineMaskingUnmaskedSinglePart(t *testing.T) {
	spatialRecords := map[spatialKey]*spatialRecord{
		{RCNM: int(spatialTypeEdge), RCID: 1}: edgeRecord(1, [][]float64{{0, 0}, {1, 0}}, 0, 0),
		{RCNM: int(spatialTypeEdge), RCID: 2}: edgeRecord(2, [][]float64{{1, 0}, {2, 0}}, 0, 0),
		{RCNM: int(spatialTypeEdge), RCID: 3}: edgeRecord(3, [][]float64{{2, 0}, {3, 0}}, 0, 0),
	}

	// Case A: edges all explicitly Mask=2 (show) → masking info present, single part.
	featShown := &featureRecord{
		GeomPrim: 2,
		SpatialRefs: []spatialRef{
			{RCNM: int(spatialTypeEdge), RCID: 1, Orientation: 1, Mask: 2},
			{RCNM: int(spatialTypeEdge), RCID: 2, Orientation: 1, Mask: 2},
			{RCNM: int(spatialTypeEdge), RCID: 3, Orientation: 1, Mask: 2},
		},
	}
	g, err := constructLineStringGeometry(featShown, spatialRecords)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Lines) != 1 {
		t.Fatalf("expected 1 drawable part for fully-shown line, got %d: %v", len(g.Lines), g.Lines)
	}
	want := [][]float64{{0, 0}, {1, 0}, {2, 0}, {3, 0}}
	if len(g.Lines[0]) != len(want) {
		t.Fatalf("single part = %v, want %v", g.Lines[0], want)
	}
	for i, p := range g.Lines[0] {
		if p[0] != want[i][0] || p[1] != want[i][1] {
			t.Fatalf("single part point %d = %v, want %v", i, p, want[i])
		}
	}

	// Case B: no MASK/USAG info anywhere (Mask==0, Usage==0 means null/show). The
	// masking path must NOT engage → Lines nil, flat Coordinates intact.
	featNoMask := &featureRecord{
		GeomPrim: 2,
		SpatialRefs: []spatialRef{
			{RCNM: int(spatialTypeEdge), RCID: 1, Orientation: 1},
			{RCNM: int(spatialTypeEdge), RCID: 2, Orientation: 1},
			{RCNM: int(spatialTypeEdge), RCID: 3, Orientation: 1},
		},
	}
	gNo, err := constructLineStringGeometry(featNoMask, spatialRecords)
	if err != nil {
		t.Fatal(err)
	}
	if gNo.Lines != nil {
		t.Fatalf("expected Lines nil for an unmasked line (fall back to Coordinates), got %v", gNo.Lines)
	}
	if len(gNo.Coordinates) == 0 {
		t.Fatalf("expected flat Coordinates populated for unmasked line")
	}
}

// TestLineMaskingDataLimitEdge verifies USAG={3} (data-limit / truncated edge) is
// treated like a masked edge: it must not be drawn and breaks the chain.
func TestLineMaskingDataLimitEdge(t *testing.T) {
	spatialRecords := map[spatialKey]*spatialRecord{
		{RCNM: int(spatialTypeEdge), RCID: 1}: edgeRecord(1, [][]float64{{0, 0}, {1, 0}}, 0, 0),
		{RCNM: int(spatialTypeEdge), RCID: 2}: edgeRecord(2, [][]float64{{1, 0}, {2, 0}}, 0, 0),
		{RCNM: int(spatialTypeEdge), RCID: 3}: edgeRecord(3, [][]float64{{2, 0}, {3, 0}}, 0, 0),
	}
	feat := &featureRecord{
		GeomPrim: 2,
		SpatialRefs: []spatialRef{
			{RCNM: int(spatialTypeEdge), RCID: 1, Orientation: 1, Mask: 2},
			{RCNM: int(spatialTypeEdge), RCID: 2, Orientation: 1, Usage: 3}, // data-limit edge
			{RCNM: int(spatialTypeEdge), RCID: 3, Orientation: 1, Mask: 2},
		},
	}
	g, err := constructLineStringGeometry(feat, spatialRecords)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Lines) != 2 {
		t.Fatalf("expected 2 drawable parts around data-limit edge, got %d: %v", len(g.Lines), g.Lines)
	}
	// Backward compat: flat Coordinates still carry the data-limit edge.
	if !flatHasPoint(g.Coordinates, 1, 0) || !flatHasPoint(g.Coordinates, 2, 0) {
		t.Fatalf("flat Coordinates missing data-limit edge endpoints: %v", g.Coordinates)
	}
}

// TestLineMaskingTrailingMaskedEdge verifies a masked edge at the END of the chain
// yields a single drawable part (no empty trailing part) but keeps all edges in the
// flat Coordinates.
func TestLineMaskingTrailingMaskedEdge(t *testing.T) {
	spatialRecords := map[spatialKey]*spatialRecord{
		{RCNM: int(spatialTypeEdge), RCID: 1}: edgeRecord(1, [][]float64{{0, 0}, {1, 0}}, 0, 0),
		{RCNM: int(spatialTypeEdge), RCID: 2}: edgeRecord(2, [][]float64{{1, 0}, {2, 0}}, 0, 0),
		{RCNM: int(spatialTypeEdge), RCID: 3}: edgeRecord(3, [][]float64{{2, 0}, {3, 0}}, 0, 0),
	}
	feat := &featureRecord{
		GeomPrim: 2,
		SpatialRefs: []spatialRef{
			{RCNM: int(spatialTypeEdge), RCID: 1, Orientation: 1, Mask: 2},
			{RCNM: int(spatialTypeEdge), RCID: 2, Orientation: 1, Mask: 2},
			{RCNM: int(spatialTypeEdge), RCID: 3, Orientation: 1, Mask: 1}, // masked tail
		},
	}
	g, err := constructLineStringGeometry(feat, spatialRecords)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Lines) != 1 {
		t.Fatalf("expected 1 drawable part for trailing-masked line, got %d: %v", len(g.Lines), g.Lines)
	}
	// The masked tail (3,0) must not be in the drawable part...
	if hasPoint(g.Lines, 3, 0) {
		t.Fatalf("masked tail (3,0) appears in drawable parts: %v", g.Lines)
	}
	// ...but must remain in the flat Coordinates.
	if !flatHasPoint(g.Coordinates, 3, 0) {
		t.Fatalf("flat Coordinates missing masked tail (3,0): %v", g.Coordinates)
	}
}
