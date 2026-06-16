package s52

// RESTRN01 represents the Restricted Area symbology procedure.
// Adds restriction patterns and dashed boundaries based on restriction type.
//
// S-52 Section 13.2.11: RESTRN01
//
// RESTRN patterns:
//   - 1,2,7-12 = General restrictions → AP(RESTRN01)
//   - 3,4,5,6 = Fishing/trawling → AP(RESTRN03)
//   - 13 = No wake → AP(RESTRN11)
//   - 14 = Area to avoid → AP(RESTRN81)
//
// Always adds dashed boundary: LS(DASH,2,CHGRD)
type RESTRN01 struct {
	ctx *CSContext
	lib *Library
}

// NewRESTRN01 creates a new RESTRN01 procedure instance.
func NewRESTRN01(csctx *CSContext, lib *Library) *RESTRN01 {
	return &RESTRN01{
		ctx: csctx,
		lib: lib,
	}
}

// Execute runs the RESTRN01 symbology procedure and returns rendering instructions.
func (r *RESTRN01) Execute() ([]Instruction, error) {
	if !r.hasRestriction() {
		return []Instruction{}, nil
	}

	var instructions []Instruction

	// Add restriction symbols from RESCSP02
	if restrictionInst := r.getRestrictionSymbols(); len(restrictionInst) > 0 {
		instructions = append(instructions, restrictionInst...)
	}

	// Always add dashed boundary per S-52 spec
	instructions = append(instructions, r.boundaryInstruction())

	return instructions, nil
}

// hasRestriction returns true if the RESTRN attribute exists.
func (r *RESTRN01) hasRestriction() bool {
	return r.ctx.Has("RESTRN")
}

// getRestrictionSymbols calls sub-procedure RESCSP02 to get restriction symbols.
func (r *RESTRN01) getRestrictionSymbols() []Instruction {
	restrictionInst, err := r.lib.csRESCSP02(r.ctx.Attributes, r.ctx.Mariner)
	if err == nil && len(restrictionInst) > 0 {
		return restrictionInst
	}
	return nil
}

// boundaryInstruction creates the dashed boundary line.
func (r *RESTRN01) boundaryInstruction() *LSInstruction {
	return &LSInstruction{
		Style: "DASH",
		Width: 2,
		Color: "CHGRD",
	}
}
