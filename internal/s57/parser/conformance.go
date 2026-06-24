package parser

// conformance.go — non-fatal S-57 / ISO-8211 spec-conformance checks.
//
// A chart *reader* has two obligations that pull in opposite directions: it must
// interpret conformant data per the spec, and it must still render the real-world
// cells it is handed (which are not always conformant — S-58 exists precisely
// because producers deviate). So these checks DETECT and REPORT deviations (the
// S-58 spirit) without rejecting the cell: parsing continues and the chart still
// renders. The collected warnings are attached to the Chart (see Chart.Warnings);
// callers that want strict behaviour set ParseOptions.ValidateConformance, which
// promotes any warning to a parse error.
//
// Spec citations are to S-57 Edition 3.1 Part 3 (31Main.pdf) and its Annex A
// (ISO/IEC 8211), unless noted.

import (
	"fmt"
	"strings"

	"github.com/beetlebugorg/chartplotter/pkg/iso8211"
)

// ConformanceWarning is one detected spec deviation. It is non-fatal.
type ConformanceWarning struct {
	Clause  string // governing spec clause, e.g. "8.4.2.1" or "ISO8211 A.2.2"
	Code    string // short stable identifier, e.g. "RVER_SEQUENCE"
	Message string // human-readable detail (from the first occurrence)
	Count   int    // number of times this code was hit during the parse
}

func (w ConformanceWarning) String() string {
	if w.Count > 1 {
		return fmt.Sprintf("[%s §%s] %s (×%d)", w.Code, w.Clause, w.Message, w.Count)
	}
	return fmt.Sprintf("[%s §%s] %s", w.Code, w.Clause, w.Message)
}

// conformance accumulates warnings, deduplicated by Code so a deviation that
// recurs across thousands of records (e.g. a per-DR leader value) reports once
// with a count instead of flooding the list.
type conformance struct {
	seen map[string]int // Code -> index into list
	list []ConformanceWarning
}

// add records a deviation. Safe on a nil receiver (checks are no-ops when the
// collector was not created).
func (c *conformance) add(clause, code, msg string) {
	if c == nil {
		return
	}
	if c.seen == nil {
		c.seen = make(map[string]int)
	}
	if i, ok := c.seen[code]; ok {
		c.list[i].Count++
		return
	}
	c.seen[code] = len(c.list)
	c.list = append(c.list, ConformanceWarning{Clause: clause, Code: code, Message: msg, Count: 1})
}

func (c *conformance) addf(clause, code, format string, args ...any) {
	c.add(clause, code, fmt.Sprintf(format, args...))
}

// warnings returns the accumulated deviations (nil-safe).
func (c *conformance) warnings() []ConformanceWarning {
	if c == nil {
		return nil
	}
	return c.list
}

// asError aggregates all warnings into a single error for strict mode, or nil if
// none were collected.
func (c *conformance) asError() error {
	if c == nil || len(c.list) == 0 {
		return nil
	}
	parts := make([]string, len(c.list))
	for i, w := range c.list {
		parts[i] = w.String()
	}
	return fmt.Errorf("S-57 conformance: %d deviation(s): %s", len(c.list), strings.Join(parts, "; "))
}

// isNullPtrField reports whether v is the S-57 "null" value {255} used for the
// ORNT/USAG/MASK/TOPI subfields when not applicable.
func isNullPtrField(v int) bool { return v == 255 }

// validateLeaders checks the ISO/IEC 8211 leader fixed values mandated for S-57
// (Part 3 Annex A.2.2, Tables A.1 / A.3). The DDR (LeaderIdentifier 'L') and the
// data records ('D') carry different fixed values; deviations are reported but do
// not stop parsing (the leader was already structurally usable to get here).
func validateLeaders(f *iso8211.ISO8211File, c *conformance) {
	if c == nil || f == nil {
		return
	}
	if f.DDR != nil && f.DDR.Leader != nil {
		l := f.DDR.Leader
		// DDR fixed values (Table A.1).
		if l.InterchangeLevel != '3' {
			c.addf("ISO8211 A.2.2", "DDR_INTERCHANGE_LEVEL", "DDR interchange level must be '3', got %q", string(l.InterchangeLevel))
		}
		if l.VersionNumber != '1' {
			c.addf("ISO8211 A.2.2", "DDR_VERSION", "DDR version number must be '1', got %q", string(l.VersionNumber))
		}
		if l.ApplicationIndicator != ' ' {
			c.addf("ISO8211 A.2.2", "DDR_APP_INDICATOR", "DDR application indicator must be SPACE, got %q", string(l.ApplicationIndicator))
		}
		if l.ExtendedCharSet != [3]byte{' ', '!', ' '} {
			c.addf("ISO8211 A.2.2", "DDR_EXT_CHARSET", "DDR extended character set must be ' ! ', got %q", string(l.ExtendedCharSet[:]))
		}
		validateCommonLeader(l, c, "DDR")
	}
	// Data-record leaders (Table A.3): interchange level, version and application
	// indicator are SPACE; extended char set is three SPACEs.
	for _, r := range f.Records {
		if r == nil || r.Leader == nil {
			continue
		}
		l := r.Leader
		if l.InterchangeLevel != ' ' {
			c.addf("ISO8211 A.2.2", "DR_INTERCHANGE_LEVEL", "DR interchange level must be SPACE, got %q", string(l.InterchangeLevel))
		}
		if l.VersionNumber != ' ' {
			c.addf("ISO8211 A.2.2", "DR_VERSION", "DR version number must be SPACE, got %q", string(l.VersionNumber))
		}
		if l.ExtendedCharSet != [3]byte{' ', ' ', ' '} {
			c.addf("ISO8211 A.2.2", "DR_EXT_CHARSET", "DR extended character set must be 3 SPACEs, got %q", string(l.ExtendedCharSet[:]))
		}
		validateCommonLeader(l, c, "DR")
	}
}

// validateCommonLeader checks leader fields fixed across both DDR and DR.
func validateCommonLeader(l *iso8211.Leader, c *conformance, kind string) {
	if l.Reserved != '0' {
		c.addf("ISO8211 A.2.2", kind+"_RESERVED", "%s leader reserved byte must be '0', got %q", kind, string(l.Reserved))
	}
	if l.SizeOfFieldTag != 4 {
		c.addf("ISO8211 A.2.2", kind+"_TAG_SIZE", "%s size of field tag must be 4 for S-57, got %d", kind, l.SizeOfFieldTag)
	}
}

// validateFeatureConformance checks a feature record's FSPT pointer subfields
// against the geometric-primitive rules in Part 3 §4.7 (and winding.md). The
// values are interpreted regardless; this only flags out-of-domain values.
func validateFeatureConformance(fr *featureRecord, c *conformance) {
	if c == nil || fr == nil {
		return
	}
	for _, ref := range fr.SpatialRefs {
		// Domain checks for ORNT/USAG/MASK (Part 3 §4.7.3, table 7.31 domains).
		if ref.Orientation != 1 && ref.Orientation != 2 && !isNullPtrField(ref.Orientation) {
			c.addf("4.7.3", "FSPT_ORNT_DOMAIN", "FSPT ORNT must be 1, 2 or 255, got %d", ref.Orientation)
		}
		if ref.Usage != 1 && ref.Usage != 2 && ref.Usage != 3 && !isNullPtrField(ref.Usage) {
			c.addf("4.7.3", "FSPT_USAG_DOMAIN", "FSPT USAG must be 1, 2, 3 or 255, got %d", ref.Usage)
		}
		if ref.Mask != 1 && ref.Mask != 2 && !isNullPtrField(ref.Mask) {
			c.addf("4.7.3", "FSPT_MASK_DOMAIN", "FSPT MASK must be 1, 2 or 255, got %d", ref.Mask)
		}
		// Point features: ORNT/USAG/MASK must be null {255} (§4.7.1, winding.md §4.7.1).
		if fr.GeomPrim == 1 {
			if !isNullPtrField(ref.Orientation) || !isNullPtrField(ref.Usage) || !isNullPtrField(ref.Mask) {
				c.add("4.7.1", "FSPT_POINT_NOT_NULL", "point feature FSPT ORNT/USAG/MASK must all be null {255}")
			}
		}
	}
}

// validateSpatialConformance checks a vector record's VRPT pointer subfields
// against the domains in Part 3 §7.7.1.4 (table 7.31).
func validateSpatialConformance(sr *spatialRecord, c *conformance) {
	if c == nil || sr == nil {
		return
	}
	for _, ptr := range sr.VectorPointers {
		if ptr.Orientation != 1 && ptr.Orientation != 2 && !isNullPtrField(ptr.Orientation) {
			c.addf("7.7.1.4", "VRPT_ORNT_DOMAIN", "VRPT ORNT must be 1, 2 or 255, got %d", ptr.Orientation)
		}
		if ptr.Usage != 1 && ptr.Usage != 2 && ptr.Usage != 3 && !isNullPtrField(ptr.Usage) {
			c.addf("7.7.1.4", "VRPT_USAG_DOMAIN", "VRPT USAG must be 1, 2, 3 or 255, got %d", ptr.Usage)
		}
		if ptr.Topology < 1 && !isNullPtrField(ptr.Topology) || ptr.Topology > 4 && !isNullPtrField(ptr.Topology) {
			c.addf("7.7.1.4", "VRPT_TOPI_DOMAIN", "VRPT TOPI must be 1..4 or 255, got %d", ptr.Topology)
		}
		if ptr.Mask != 1 && ptr.Mask != 2 && !isNullPtrField(ptr.Mask) {
			c.addf("7.7.1.4", "VRPT_MASK_DOMAIN", "VRPT MASK must be 1, 2 or 255, got %d", ptr.Mask)
		}
	}
}
