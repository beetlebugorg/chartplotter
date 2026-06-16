package s52

// SLCONS04 represents the Shoreline Construction symbology procedure.
// Symbolizes shoreline construction based on type and water level.
//
// S-52 PresLib e4.0.0, Part I, Section 13.2.14, Figure 31 (page 84)
//
// When spatial context is provided with component data, can apply per-segment QUAPOS styling.
// When spatial is nil, uses object-level attributes for the entire line.
type SLCONS04 struct {
	ctx    *CSContext
	lib    *Library
	catslc int // Category of shoreline construction
	watlev int // Water level
	condtn int // Condition
}

// NewSLCONS04 creates a new SLCONS04 procedure instance by parsing the execution context.
func NewSLCONS04(csctx *CSContext, lib *Library) *SLCONS04 {
	return &SLCONS04{
		ctx:    csctx,
		lib:    lib,
		catslc: csctx.GetInt("CATSLC", 0),
		watlev: csctx.GetInt("WATLEV", 0),
		condtn: csctx.GetInt("CONDTN", 0),
	}
}

// Execute runs the SLCONS04 symbology procedure and returns rendering instructions.
func (s *SLCONS04) Execute() ([]Instruction, error) {
	return []Instruction{s.lineInstruction()}, nil
}

// lineInstruction creates the appropriate line instruction based on attributes.
// Applies strict lookup table matching per spec (Figure 31, page 84).
// Order matters - first match wins.
func (s *SLCONS04) lineInstruction() *LSInstruction {
	style, width := s.getLineStyle()
	return &LSInstruction{
		Style: style,
		Width: width,
		Color: "CSTLN",
	}
}

// getLineStyle determines the line style and width based on condition, category, and water level.
func (s *SLCONS04) getLineStyle() (string, int) {
	switch {
	case s.condtn == 1: // Under construction
		return "DASH", 1
	case s.condtn == 2: // Ruined
		return "DASH", 1
	case s.catslc == 6: // Fence/wall
		return "SOLD", 4
	case s.catslc == 15: // Solid face wharf
		return "SOLD", 4
	case s.catslc == 16: // Open face wharf
		return "SOLD", 4
	case s.watlev == 2: // Partially submerged at high water
		return "SOLD", 2
	case s.watlev == 3: // Covers and uncovers
		return "DASH", 2
	case s.watlev == 4: // Awash
		return "DASH", 2
	default: // No attributes or not matched
		return "SOLD", 2
	}
}
