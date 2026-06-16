package s52

// UNSARE01 represents the Unsurveyed Area symbology procedure.
// Fills unsurveyed/inadequate survey areas with the NODTA (no data) color.
//
// UNSARE objects indicate areas where:
// - No survey has been conducted
// - Survey is inadequate or unreliable
// - Data quality is insufficient for safe navigation
type UNSARE01 struct {
	ctx *CSContext
	lib *Library
}

// NewUNSARE01 creates a new UNSARE01 procedure instance.
func NewUNSARE01(csctx *CSContext, lib *Library) *UNSARE01 {
	return &UNSARE01{
		ctx: csctx,
		lib: lib,
	}
}

// Execute runs the UNSARE01 symbology procedure and returns rendering instructions.
func (u *UNSARE01) Execute() ([]Instruction, error) {
	var instructions []Instruction

	// Fill with NODTA (no data) color
	instructions = append(instructions, &ACInstruction{Color: "NODTA"})

	// Add diagonal line pattern to indicate unsurveyed area
	// S-52: Use NODATA03 or similar pattern to indicate lack of survey data
	instructions = append(instructions, &APInstruction{PatternID: "NODATA03"})

	return instructions, nil
}
