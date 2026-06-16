package s52

// QUAPOS01 represents the Position Quality indicator procedure.
// Modifies line styles to dashed when position is uncertain.
//
// S-52 Section 13.2.7: QUAPOS01
//
// QUAPOS values:
//   - 1, 10, 11 = Good quality (surveyed, precise, GPS) → solid line
//   - 2-9 = Uncertain quality → dashed line
//   - missing = Assume good quality → solid line
type QUAPOS01 struct {
	ctx    *CSContext
	quapos int  // Position quality value
	exists bool // Whether QUAPOS attribute exists
}

// NewQUAPOS01 creates a new QUAPOS01 procedure instance by parsing the execution context.
func NewQUAPOS01(csctx *CSContext) *QUAPOS01 {
	return &QUAPOS01{
		ctx:    csctx,
		quapos: csctx.GetInt("QUAPOS", 0),
		exists: csctx.Has("QUAPOS"),
	}
}

// Execute runs the QUAPOS01 symbology procedure and returns rendering instructions.
func (q *QUAPOS01) Execute() ([]Instruction, error) {
	return []Instruction{q.lineInstruction()}, nil
}

// lineInstruction creates the line instruction based on position quality.
func (q *QUAPOS01) lineInstruction() *LSInstruction {
	return &LSInstruction{
		Style: q.getLineStyle(),
		Width: 1,
		Color: "CHBLK",
	}
}

// getLineStyle returns the appropriate line style based on position quality.
func (q *QUAPOS01) getLineStyle() string {
	if q.isGoodQuality() {
		return "SOLD"
	}
	return "DASH"
}

// isGoodQuality returns true if position quality is good (1, 10, 11) or missing.
func (q *QUAPOS01) isGoodQuality() bool {
	if !q.exists {
		// Missing QUAPOS - assume good quality
		return true
	}
	// 0 = Default/unknown, 1 = Surveyed, 10 = Precisely known, 11 = Calculated (GPS)
	return q.quapos == 0 || q.quapos == 1 || q.quapos == 10 || q.quapos == 11
}
