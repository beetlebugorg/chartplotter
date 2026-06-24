package parser

import (
	"strings"
	"testing"
)

// TestConformanceCollectorDedup verifies that repeated deviations of the same
// Code collapse to one entry with an occurrence count, while distinct codes are
// kept separately and in first-seen order.
func TestConformanceCollectorDedup(t *testing.T) {
	c := &conformance{}
	c.add("4.7.1", "A", "first")
	c.add("4.7.1", "A", "second") // same code → bumps count, keeps first message
	c.addf("7.7.1.4", "B", "val=%d", 9)

	ws := c.warnings()
	if len(ws) != 2 {
		t.Fatalf("want 2 unique warnings, got %d: %+v", len(ws), ws)
	}
	if ws[0].Code != "A" || ws[0].Count != 2 || ws[0].Message != "first" {
		t.Errorf("dedup wrong: %+v", ws[0])
	}
	if ws[1].Code != "B" || ws[1].Message != "val=9" {
		t.Errorf("addf wrong: %+v", ws[1])
	}
}

// TestConformanceNilSafe confirms the checks are no-ops on a nil collector, so
// callers that don't track conformance pay nothing and never panic.
func TestConformanceNilSafe(t *testing.T) {
	var c *conformance
	c.add("x", "y", "z") // must not panic
	if c.warnings() != nil {
		t.Error("nil collector should yield nil warnings")
	}
	if c.asError() != nil {
		t.Error("nil collector should yield nil error")
	}
}

// TestConformanceAsError aggregates warnings into a single strict-mode error.
func TestConformanceAsError(t *testing.T) {
	c := &conformance{}
	if c.asError() != nil {
		t.Fatal("empty collector should produce no error")
	}
	c.add("8.4.2.1", "RVER_SEQUENCE_FEATURE", "gap")
	err := c.asError()
	if err == nil || !strings.Contains(err.Error(), "RVER_SEQUENCE_FEATURE") {
		t.Fatalf("strict error should mention the code, got %v", err)
	}
}

// TestValidatePointFeaturePointerRule checks the §4.7.1 rule that a point
// feature's FSPT ORNT/USAG/MASK must all be null {255}.
func TestValidatePointFeaturePointerRule(t *testing.T) {
	c := &conformance{}
	// Conformant point feature: all pointer subfields null.
	validateFeatureConformance(&featureRecord{
		GeomPrim:    1,
		SpatialRefs: []spatialRef{{RCNM: 110, Orientation: 255, Usage: 255, Mask: 255}},
	}, c)
	if len(c.warnings()) != 0 {
		t.Fatalf("conformant point feature should not warn: %+v", c.warnings())
	}

	// Non-conformant: a point feature carrying USAG=1 (exterior) is wrong.
	c2 := &conformance{}
	validateFeatureConformance(&featureRecord{
		GeomPrim:    1,
		SpatialRefs: []spatialRef{{RCNM: 110, Orientation: 255, Usage: 1, Mask: 255}},
	}, c2)
	if !hasCode(c2, "FSPT_POINT_NOT_NULL") {
		t.Errorf("expected FSPT_POINT_NOT_NULL, got %+v", c2.warnings())
	}
}

// TestValidateDomainRanges checks out-of-domain ORNT/USAG/MASK/TOPI values.
func TestValidateDomainRanges(t *testing.T) {
	c := &conformance{}
	validateFeatureConformance(&featureRecord{
		GeomPrim:    3,
		SpatialRefs: []spatialRef{{RCNM: 130, Orientation: 9, Usage: 7, Mask: 4}},
	}, c)
	for _, code := range []string{"FSPT_ORNT_DOMAIN", "FSPT_USAG_DOMAIN", "FSPT_MASK_DOMAIN"} {
		if !hasCode(c, code) {
			t.Errorf("expected %s, got %+v", code, c.warnings())
		}
	}

	cs := &conformance{}
	validateSpatialConformance(&spatialRecord{
		VectorPointers: []vectorPointer{{TargetRCNM: 120, Orientation: 9, Usage: 7, Topology: 8, Mask: 4}},
	}, cs)
	for _, code := range []string{"VRPT_ORNT_DOMAIN", "VRPT_USAG_DOMAIN", "VRPT_TOPI_DOMAIN", "VRPT_MASK_DOMAIN"} {
		if !hasCode(cs, code) {
			t.Errorf("expected %s, got %+v", code, cs.warnings())
		}
	}
}

func hasCode(c *conformance, code string) bool {
	for _, w := range c.warnings() {
		if w.Code == code {
			return true
		}
	}
	return false
}

// TestRealCellConformance parses a real NOAA cell and exercises both the
// warn-and-render default and strict mode. A genuine NOAA ENC is expected to be
// largely conformant; this guards against the checks spuriously flagging valid
// data (which would flood every chart). It logs whatever is flagged for insight.
func TestRealCellConformance(t *testing.T) {
	const cell = "../../../testdata/US4MD81M.000"
	p := NewParser()

	chart, err := p.ParseWithOptions(cell, DefaultParseOptions())
	if err != nil {
		t.Fatalf("default parse must succeed (warn-and-render): %v", err)
	}
	ws := chart.Warnings()
	t.Logf("%s: %d conformance warning(s)", cell, len(ws))
	for _, w := range ws {
		t.Logf("  %s", w.String())
	}
	// A conformant NOAA base cell should not trip the leader checks at all.
	for _, w := range ws {
		if strings.HasPrefix(w.Code, "DDR_") || strings.HasPrefix(w.Code, "DR_") {
			t.Errorf("unexpected ISO-8211 leader deviation on a real NOAA cell: %s", w.String())
		}
	}

	// Strict mode: errors iff the cell had any deviation; otherwise parses clean.
	strict := DefaultParseOptions()
	strict.ValidateConformance = true
	_, serr := p.ParseWithOptions(cell, strict)
	if len(ws) == 0 && serr != nil {
		t.Errorf("strict mode should succeed on a clean cell, got %v", serr)
	}
	if len(ws) > 0 && serr == nil {
		t.Errorf("strict mode should fail when warnings exist (%d)", len(ws))
	}
}
