package s52

// RESTRN01 represents the Restricted Area symbology procedure.
//
// S-52 PresLib 4.0 (Figure 27): RESTRN01 is a "signpost" — it merely passes the
// RESTRN attribute to sub-procedure RESCSP02 and returns its centred restriction
// symbol(s). It draws NO boundary and NO area pattern; the calling object's own
// look-up table supplies the boundary line/fill (every CS(RESTRN01) LUPT already
// includes its own LS/LC/AP).
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

// Execute runs the RESTRN01 symbology procedure: it returns only the centred
// restriction symbol(s) from RESCSP02 (no boundary — see the type doc).
func (r *RESTRN01) Execute() ([]Instruction, error) {
	if !r.ctx.Has("RESTRN") {
		return []Instruction{}, nil
	}
	restrictionInst, err := r.lib.csRESCSP02(r.ctx.Attributes, r.ctx.Mariner)
	if err != nil {
		return []Instruction{}, nil
	}
	return restrictionInst, nil
}
