package s52

// OBSTRN07 represents the Obstruction symbology procedure.
// Symbolizes underwater obstructions based on depth and type.
//
// S-52 Section 13.2.12 (pages 65-68)
//
// When spatial context is provided with underlying objects, can call DEPVAL02 for depth.
// When geometryType is "Area", can add area fill patterns (future enhancement).
type OBSTRN07 struct {
	ctx          *CSContext
	lib          *Library
	valsou       float64 // Sounding value - depth over obstruction
	valsouExists bool    // Whether VALSOU is present
	watlev       int     // Water level
	catobs       int     // Category of obstruction
}

// NewOBSTRN07 creates a new OBSTRN07 procedure instance by parsing the execution context.
func NewOBSTRN07(csctx *CSContext, lib *Library) *OBSTRN07 {
	valsou := csctx.GetFloat("VALSOU", -1.0)
	valsouExists := csctx.Has("VALSOU")

	o := &OBSTRN07{
		ctx:          csctx,
		lib:          lib,
		valsou:       valsou,
		valsouExists: valsouExists,
		watlev:       csctx.GetInt("WATLEV", 0),
		catobs:       csctx.GetInt("CATOBS", 0),
	}

	// If VALSOU not provided, call DEPVAL02 sub-procedure
	if !valsouExists {
		o.fetchDepthFromUnderlying()
	}

	return o
}

// Execute runs the OBSTRN07 symbology procedure and returns rendering instructions.
func (o *OBSTRN07) Execute() ([]Instruction, error) {
	var instructions []Instruction

	// Add symbol
	instructions = append(instructions, &SYInstruction{SymbolID: o.selectSymbol()})

	// Add sounding text if dangerous and a POSITIVE depth is known. A rock/
	// obstruction at or above sounding datum (VALSOU <= 0, i.e. awash/drying) is
	// conveyed by its symbol (OBSTRN11 etc.) — S-52 shows no plain sounding
	// there, so a "0" label just obscures the symbol.
	if o.isDangerous() && o.valsouExists && o.valsou > 0 {
		instructions = append(instructions, o.depthLabelInstruction())
	}

	return instructions, nil
}

// fetchDepthFromUnderlying calls DEPVAL02 to get depth from underlying depth areas.
func (o *OBSTRN07) fetchDepthFromUnderlying() {
	leastDepth, _ := o.lib.csDEPVAL02(o.ctx.Attributes, o.ctx.Spatial, o.ctx.Mariner)
	if leastDepth >= 0 {
		o.valsou = leastDepth
		o.valsouExists = true
	}
}

// selectSymbol chooses the appropriate obstruction symbol based on category, water level, and depth.
func (o *OBSTRN07) selectSymbol() string {
	// UWTROC (rocks) use the rock-specific symbols, NOT the generic obstruction
	// glyphs (S-52 OBSTRN07 Continuation A): a rock at/above sounding datum
	// (VALSOU <= 0, i.e. awash) -> UWTROC04 "rock awash"; an underwater rock
	// (VALSOU > 0) -> UWTROC03. With no VALSOU, fall back to WATLEV.
	if o.ctx.ObjectClass == "UWTROC" {
		if o.valsouExists {
			if o.valsou <= 0 {
				return "UWTROC04"
			}
			return "UWTROC03"
		}
		if o.isAwash() {
			return "UWTROC04"
		}
		return "UWTROC03"
	}

	// Special category handling (overrides depth-based logic)
	switch o.catobs {
	case 6, 7: // Foul ground, foul area
		return "FOULGND1"
	case 9: // Boom
		return "OBSTRN08"
	default:
		// Standard obstruction symbols
		if o.isAwash() {
			return "OBSTRN11" // Awash rock
		} else if o.isDangerous() {
			return "OBSTRN03" // Dangerous underwater obstruction
		}
		return "OBSTRN01" // Safe or unknown obstruction
	}
}

// isAwash returns true if the obstruction covers and uncovers.
func (o *OBSTRN07) isAwash() bool {
	return o.watlev == 4 || o.watlev == 5
}

// isDangerous returns true if the obstruction is at or shallower than the safety
// depth (S-52 OBSTRN07: VALSOU <= SAFETY DEPTH).
func (o *OBSTRN07) isDangerous() bool {
	return o.valsouExists && o.valsou <= o.ctx.Mariner.SafetyDepth
}

// depthLabelInstruction creates a text instruction showing the depth value.
func (o *OBSTRN07) depthLabelInstruction() *TXInstruction {
	displayDepth := ConvertDepth(o.valsou, DepthUnitMeters, o.lib.depthUnit)
	depthStr := formatDepthValue(displayDepth)

	return &TXInstruction{
		TextInstruction: &TextInstruction{
			Text: depthStr,
			// S-52 SHOWTEXT justification codes (HJUST 1=centre/2=right/3=left,
			// VJUST 1=bottom/2=centre/3=top). Centre the depth over the danger
			// symbol and bottom-justify so it sits ABOVE it — was 3/2 (left/centre),
			// which anchored the number at the symbol centre and overlapped it.
			HJust:   1, // Centre
			VJust:   1, // Bottom (text sits above the anchor point)
			Space:   2,
			Font:    FontSpec{Style: 1, Weight: 5, Slant: 1, BodySize: 10},
			XOffset: 0,
			YOffset: 0,
			Color:   "CHBLK",
			Display: 34,
		},
	}
}
