package s52

// WRECKS05 represents the Wreck symbology procedure.
// Symbolizes wrecks based on depth, water level, and category.
//
// S-52 Section 13.2.21: WRECKS05 (pages 113-122)
//
// When spatial context is provided with underlying objects, uses DEPVAL02 for depth.
// When geometryType is "Area", can add area fill and boundary (future enhancement).
type WRECKS05 struct {
	ctx                  *CSContext
	lib                  *Library
	valsou               float64       // Sounding value
	hasVALSOU            bool          // Whether VALSOU is present
	catwrk               int           // Category of wreck
	watlev               int           // Water level
	depthValue           float64       // Final depth value (from VALSOU or calculated)
	leastDepth           float64       // From DEPVAL02
	seabedDepth          float64       // From DEPVAL02
	showIsolated         bool          // Show isolated danger symbol
	showLowAccuracy      bool          // Show low accuracy symbol
	soundingInstructions []Instruction // Formatted sounding symbols
}

// NewWRECKS05 creates a new WRECKS05 procedure instance by parsing the execution context.
func NewWRECKS05(csctx *CSContext, lib *Library) *WRECKS05 {
	valsou := csctx.GetFloat("VALSOU", 0.0)
	hasVALSOU := csctx.Has("VALSOU")

	w := &WRECKS05{
		ctx:       csctx,
		lib:       lib,
		valsou:    valsou,
		hasVALSOU: hasVALSOU,
		catwrk:    csctx.GetInt("CATWRK", 0),
		watlev:    csctx.GetInt("WATLEV", 0),
	}

	// Calculate depth value
	w.calculateDepthValue()

	// Check for isolated danger and low accuracy
	w.showIsolated, _, _ = lib.csUDWHAZ05(w.depthValue, csctx.Attributes, csctx.Spatial, csctx.Mariner)
	w.showLowAccuracy = lib.csQUAPNT02(csctx.Attributes, csctx.Mariner)

	// Format sounding if applicable
	if hasVALSOU && w.depthValue <= csctx.Mariner.SafetyDepth {
		w.soundingInstructions, _ = lib.csSONDFRM04(w.depthValue, csctx.Attributes, csctx.Mariner)
	}

	return w
}

// Execute runs the WRECKS05 symbology procedure and returns rendering instructions.
func (w *WRECKS05) Execute() ([]Instruction, error) {
	var instructions []Instruction

	// If isolated danger, add symbol and return
	if w.showIsolated {
		instructions = append(instructions, &SYInstruction{SymbolID: "ISODGR01"})
		if w.showLowAccuracy {
			instructions = append(instructions, &SYInstruction{SymbolID: "LOWACC01"})
		}
		return instructions, nil
	}

	// Not isolated danger - select wreck symbol
	instructions = append(instructions, w.wreckSymbolInstruction())

	// Add sounding symbols if applicable
	if len(w.soundingInstructions) > 0 {
		instructions = append(instructions, w.soundingInstructions...)
	}

	// Add low accuracy symbol if needed
	if w.showLowAccuracy {
		instructions = append(instructions, &SYInstruction{SymbolID: "LOWACC01"})
	}

	return instructions, nil
}

// calculateDepthValue determines the depth value from VALSOU or DEPVAL02.
func (w *WRECKS05) calculateDepthValue() {
	if w.hasVALSOU {
		w.depthValue = w.valsou
		return
	}

	// Call DEPVAL02 to get depth from underlying area
	w.leastDepth, w.seabedDepth = w.lib.csDEPVAL02(w.ctx.Attributes, w.ctx.Spatial, w.ctx.Mariner)

	if w.leastDepth >= 0 {
		w.depthValue = w.leastDepth
		return
	}

	// No VALSOU and no underlying depth - use defaults per spec
	if w.catwrk == 1 {
		// Non-dangerous wreck - use 20.1m default
		w.depthValue = 20.1
	} else if w.watlev == 3 || w.watlev == 5 {
		// Always underwater or awash - use 0
		w.depthValue = 0
	} else {
		// Unknown depth - use -15m (drying height indicator)
		w.depthValue = -15
	}

	// Calculate safe clearance for non-dangerous wrecks
	if w.catwrk == 1 && w.seabedDepth >= 0 {
		safeDepth := w.seabedDepth - 20.1 // 66 feet
		if safeDepth < 20.1 {
			w.depthValue = safeDepth
		}
	}
}

// wreckSymbolInstruction creates the appropriate wreck symbol instruction.
func (w *WRECKS05) wreckSymbolInstruction() *SYInstruction {
	var symbolID string

	// S-52 WRECKS05 Continuation A: a sounded wreck (VALSOU present) is the
	// dangerous symbol DANGER01 when VALSOU <= SAFETY DEPTH, else DANGER02 — a
	// single threshold (the earlier SafetyDepth/2 split was not in the spec).
	// Wrecks without a sounding use the standard CATWRK/WATLEV lookup.
	if w.hasVALSOU {
		if w.depthValue <= w.ctx.Mariner.SafetyDepth {
			symbolID = "DANGER01"
		} else {
			symbolID = "DANGER02"
		}
	} else {
		symbolID = selectWreckSymbol(w.ctx.Attributes)
	}

	return &SYInstruction{SymbolID: symbolID}
}

// selectWreckSymbol selects the appropriate wreck symbol based on CATWRK and WATLEV
// Per S-52 WRECKS05 lookup table (spec pages 118-119)
func selectWreckSymbol(attributes map[string]interface{}) string {
	catwrk := getIntValue(attributes["CATWRK"])
	watlev := getIntValue(attributes["WATLEV"])

	// Lookup table from spec:
	// WRECKS + CATWRK=1 + WATLEV=3 -> WRECKS04 (non-dangerous, underwater)
	// WRECKS + CATWRK=2 + WATLEV=3 -> WRECKS05 (dangerous, underwater)
	// WRECKS + CATWRK=4 -> WRECKS01 (mast showing)
	// WRECKS + CATWRK=5 -> WRECKS01 (hull showing)
	// WRECKS + WATLEV=1 -> WRECKS01 (always dry)
	// WRECKS + WATLEV=2 -> WRECKS01 (awash)
	// WRECKS + WATLEV=3 -> WRECKS01 (always underwater)
	// WRECKS + WATLEV=4 -> WRECKS01 (covers and uncovers)
	// WRECKS (default) -> WRECKS05

	// Priority order (first match wins):
	switch {
	case catwrk == 1 && watlev == 3:
		return "WRECKS04" // Non-dangerous, always underwater
	case catwrk == 2 && watlev == 3:
		return "WRECKS05" // Dangerous, always underwater
	case catwrk == 4 || catwrk == 5:
		return "WRECKS01" // Mast or hull showing
	case watlev == 1 || watlev == 2 || watlev == 3 || watlev == 4:
		return "WRECKS01" // Any specific water level
	default:
		return "WRECKS05" // Default wreck symbol
	}
}
