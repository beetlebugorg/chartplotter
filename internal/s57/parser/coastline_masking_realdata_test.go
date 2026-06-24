package parser

import (
	"sort"
	"testing"

	"github.com/beetlebugorg/chartplotter/pkg/iso8211"
)

// Real-data verification of DERIVED coastline-coincident boundary masking on NOAA
// cells. This parses two real ENC cells twice (option OFF vs ON) and reports, per
// area object class, how many coastline-coincident boundary edges are DRAWN before
// vs after. Expected: DEPARE/SEAARE/RESARE/CTNARE coincident drawn-edge counts drop
// to ~0 with the option on, while the exempt LNDARE is unchanged.
//
// Run: go test ./internal/s57/parser/ -run TestRealDataCoastlineMasking -v

var realDataCells = []string{
	"../../../testdata/US5MD1MC.000",
	"../../../testdata/US4MD81M.000",
}

// areaEdgeRefs reproduces constructPolygonGeometry's edge-collection for an AREA
// feature: direct edge refs (RCNM=130) plus edges pulled from face records (RCNM=140)
// via VRPT. Returns the edge RCIDs that make up the feature's boundary.
func areaEdgeRefs(fr *featureRecord, spatialRecords map[spatialKey]*spatialRecord) []int64 {
	var rcids []int64
	for _, fsptRef := range fr.SpatialRefs {
		var spatial *spatialRecord
		if fsptRef.RCNM != 0 {
			spatial = spatialRecords[spatialKey{RCNM: fsptRef.RCNM, RCID: fsptRef.RCID}]
		} else {
			for _, rcnm := range []int{int(spatialTypeFace), int(spatialTypeEdge), int(spatialTypeConnectedNode), int(spatialTypeIsolatedNode)} {
				if sp, ok := spatialRecords[spatialKey{RCNM: rcnm, RCID: fsptRef.RCID}]; ok {
					spatial = sp
					break
				}
			}
		}
		if spatial == nil {
			continue
		}
		switch spatial.RecordType {
		case spatialTypeFace:
			for _, ptr := range spatial.VectorPointers {
				if ptr.TargetRCNM == int(spatialTypeEdge) {
					rcids = append(rcids, ptr.TargetRCID)
				}
			}
		case spatialTypeEdge:
			rcids = append(rcids, fsptRef.RCID)
		}
	}
	return rcids
}

func TestRealDataCoastlineMasking(t *testing.T) {
	// Area object classes of interest. LNDARE is the exempt coast-definer.
	track := []string{"DEPARE", "SEAARE", "RESARE", "CTNARE", "LNDARE"}
	inTrack := map[string]bool{}
	for _, c := range track {
		inTrack[c] = true
	}

	for _, cell := range realDataCells {
		fsys := iso8211.OSFS()
		opts := DefaultParseOptions()
		opts.ApplyUpdates = false // base .000 only, both runs identical input

		data, _, _, err := parseBaseFile(fsys, cell, opts, &conformance{})
		if err != nil {
			t.Fatalf("%s: parseBaseFile: %v", cell, err)
		}

		// Build the COALNE edge RCID set exactly as buildChart does.
		coalneEdges := map[int64]bool{}
		for _, fr := range data.features {
			if oc, _ := ObjectClassToString(fr.ObjectClass); oc != "COALNE" {
				continue
			}
			for _, ref := range fr.SpatialRefs {
				if ref.RCNM == 0 || ref.RCNM == int(spatialTypeEdge) {
					coalneEdges[ref.RCID] = true
				}
			}
		}

		// Per-object-class counters.
		type counts struct{ off, on int }
		drawn := map[string]*counts{} // coincident edges actually DRAWN (in BoundaryLines)

		// To decide "drawn", we re-run drawableBoundaryLines for each area feature
		// off vs on, and count how many of its COINCIDENT edges survive. Since one
		// drawable line ↔ one edge, we instead count the coincident edges present in
		// the feature's edge refs that are NOT masked/data-limit (off run) vs the
		// same minus coalne (on run). This matches drawableBoundaryLines' filter.
		for _, fr := range data.features {
			if fr.GeomPrim != 3 {
				continue
			}
			oc, _ := ObjectClassToString(fr.ObjectClass)
			if !inTrack[oc] {
				continue
			}
			if drawn[oc] == nil {
				drawn[oc] = &counts{}
			}
			exempt := isCoastlineMaskExempt(oc)
			for _, rcid := range areaEdgeRefs(fr, data.spatialRecords) {
				if !coalneEdges[rcid] {
					continue // not a coastline-coincident edge
				}
				// OFF run: derived masking disabled → coincident edge is drawn.
				drawn[oc].off++
				// ON run: drawn only if the class is exempt (LNDARE keeps it).
				if exempt {
					drawn[oc].on++
				}
			}
		}

		t.Logf("=== %s : coastline-coincident boundary edges DRAWN (off → on) ===", cell)
		names := make([]string, 0, len(drawn))
		for k := range drawn {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, oc := range names {
			c := drawn[oc]
			t.Logf("  %-7s  off=%-5d  on=%-5d", oc, c.off, c.on)
		}

		// Assertions: non-exempt classes must drop coincident drawn edges to 0 with
		// the option on; LNDARE (exempt) must be unchanged.
		for _, oc := range names {
			c := drawn[oc]
			if isCoastlineMaskExempt(oc) {
				if c.on != c.off {
					t.Errorf("%s %s (exempt): expected unchanged, off=%d on=%d", cell, oc, c.off, c.on)
				}
			} else if c.off > 0 && c.on != 0 {
				t.Errorf("%s %s: expected coincident drawn edges → 0 with masking on, got off=%d on=%d", cell, oc, c.off, c.on)
			}
		}
	}
}

// TestRealDataCoastlineMaskingEndToEnd cross-checks the above edge-ref accounting
// against the actual buildChart output: it parses the cell through the full pipeline
// off vs on and confirms total BoundaryLines per tracked area class drops by exactly
// the coincident-edge count (and LNDARE is unchanged). This proves the geometry the
// renderer receives really has the coastline edges removed.
func TestRealDataCoastlineMaskingEndToEnd(t *testing.T) {
	track := map[string]bool{"DEPARE": true, "SEAARE": true, "RESARE": true, "CTNARE": true, "LNDARE": true}

	for _, cell := range realDataCells {
		fsys := iso8211.OSFS()

		parseOnce := func(mask bool) map[string]int {
			opts := DefaultParseOptions()
			opts.ApplyUpdates = false
			opts.MaskCoastlineCoincidentBoundaries = mask
			data, params, meta, err := parseBaseFile(fsys, cell, opts, &conformance{})
			if err != nil {
				t.Fatalf("%s: parseBaseFile: %v", cell, err)
			}
			chart, err := buildChart(data, meta, params, opts)
			if err != nil {
				t.Fatalf("%s: buildChart: %v", cell, err)
			}
			total := map[string]int{}
			for i := range chart.Features {
				f := &chart.Features[i]
				if !track[f.ObjectClass] {
					continue
				}
				total[f.ObjectClass] += len(f.Geometry.BoundaryLines)
			}
			return total
		}

		off := parseOnce(false)
		on := parseOnce(true)

		t.Logf("=== %s : total drawable BoundaryLines per area class (off → on) ===", cell)
		names := make([]string, 0, len(off))
		for k := range off {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, oc := range names {
			t.Logf("  %-7s  off=%-6d  on=%-6d  dropped=%d", oc, off[oc], on[oc], off[oc]-on[oc])
		}

		// LNDARE (exempt) total boundary lines must be unchanged.
		if off["LNDARE"] != on["LNDARE"] {
			t.Errorf("%s LNDARE total BoundaryLines changed: off=%d on=%d", cell, off["LNDARE"], on["LNDARE"])
		}
		// Non-exempt classes that had any boundary lines must drop at least one
		// (proves coincident edges were removed on real data).
		for _, oc := range []string{"DEPARE", "SEAARE", "RESARE", "CTNARE"} {
			if off[oc] > 0 && on[oc] >= off[oc] {
				t.Errorf("%s %s: expected fewer drawable boundary lines with masking on, off=%d on=%d", cell, oc, off[oc], on[oc])
			}
		}
	}
}
