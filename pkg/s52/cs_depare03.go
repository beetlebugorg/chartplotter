package s52

// DEPARE03 represents the Depth Area symbology procedure.
// Colors depth areas and handles DRGARE (dredged areas).
//
// When spatial context is provided, uses adjacency data for more accurate safety contour detection.
// When spatial is nil, falls back to simplified depth range check.
type DEPARE03 struct {
	ctx           *CSContext
	lib           *Library
	drval1        float64 // Minimum depth
	drval2        float64 // Maximum depth
	quapos        int     // Position quality
	isDredgedArea bool    // Is this a dredged area?
}

// NewDEPARE03 creates a new DEPARE03 procedure instance by parsing the execution context.
func NewDEPARE03(csctx *CSContext, lib *Library) *DEPARE03 {
	return &DEPARE03{
		ctx:           csctx,
		lib:           lib,
		drval1:        csctx.GetFloat("DRVAL1", -1.0),
		drval2:        csctx.GetFloat("DRVAL2", 0.0),
		quapos:        csctx.GetInt("QUAPOS", 0),
		isDredgedArea: csctx.ObjectClass == "DRGARE",
	}
}

// Execute runs the DEPARE03 symbology procedure and returns rendering instructions.
func (d *DEPARE03) Execute() ([]Instruction, error) {
	var instructions []Instruction

	// Add area color fill
	instructions = append(instructions, &ACInstruction{Color: d.getSeabedColor()})

	// Add shallow pattern if enabled and in shallow water
	// S-52: Apply diagonal line pattern (DIAMOND1 or similar) to areas shallower than shallow contour
	if d.ctx.Mariner.ShallowPattern && d.isShallowWater() {
		instructions = append(instructions, &APInstruction{PatternID: "DIAMOND1"})
	}

	// Add dredged area symbology if applicable
	if d.isDredgedArea {
		instructions = append(instructions, d.dredgedAreaInstructions()...)
	}

	// Add safety contour line if area crosses the safety contour
	if d.crossesSafetyContour() {
		instructions = append(instructions, d.safetyContourInstruction())
	}

	return instructions, nil
}

// isShallowWater returns true if this depth area is in shallow water (< shallow contour)
func (d *DEPARE03) isShallowWater() bool {
	// Use maximum depth (DRVAL2) to determine if area is shallow
	// Area is shallow if its deepest point is still less than shallow contour
	return d.drval2 < d.ctx.Mariner.ShallowContour
}

// getSeabedColor determines the area fill color based on depth and mariner settings.
// Implements S-52 SEABED01 color determination logic.
func (d *DEPARE03) getSeabedColor() string {
	return d.lib.csSEABED01(d.drval1, d.drval2, d.ctx.Mariner)
}

// crossesSafetyContour returns true if this depth area crosses the safety contour.
// TODO: Use d.ctx.HasAdjacentObjects() for edge-by-edge spatial detection
func (d *DEPARE03) crossesSafetyContour() bool {
	return d.drval1 < d.ctx.Mariner.SafetyContour &&
		d.drval2 >= d.ctx.Mariner.SafetyContour
}

// getPositionQualityStyle returns the line style based on position quality.
// Good quality (1, 10, 11) uses solid lines; uncertain quality uses dashed lines.
func (d *DEPARE03) getPositionQualityStyle() string {
	if d.quapos == 0 || d.quapos == 1 || d.quapos == 10 || d.quapos == 11 {
		return "SOLD"
	}
	return "DASH"
}

// safetyContourInstruction creates the safety contour line instruction.
func (d *DEPARE03) safetyContourInstruction() *LSInstruction {
	return &LSInstruction{
		Style: d.getPositionQualityStyle(),
		Width: 2,
		Color: "DEPSC",
	}
}

// dredgedAreaInstructions generates symbology for dredged areas (pattern + boundary + restrictions).
func (d *DEPARE03) dredgedAreaInstructions() []Instruction {
	instructions := []Instruction{
		// Dredged area pattern
		&APInstruction{PatternID: "DRGARE01"},
		// Dashed boundary
		&LSInstruction{
			Style: "DASH",
			Width: 1,
			Color: "CHGRF",
		},
	}

	// Add restriction symbols if present
	if restrictionInst, err := d.lib.csRESCSP02(d.ctx.Attributes, d.ctx.Mariner); err == nil && len(restrictionInst) > 0 {
		instructions = append(instructions, restrictionInst...)
	}

	return instructions
}
