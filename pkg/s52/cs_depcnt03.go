package s52

// DEPCNT03 represents the Depth Contour symbology procedure.
// Symbolizes depth contour lines with safety contour highlighting.
//
// When spatial context is provided with component data, can apply per-segment QUAPOS styling.
// When spatial is nil, uses object-level QUAPOS for the entire contour.
type DEPCNT03 struct {
	ctx             *CSContext
	lib             *Library
	valdco          float64 // Contour depth value
	quapos          int     // Position quality
	isSafetyContour bool    // Is this the safety contour?
}

// NewDEPCNT03 creates a new DEPCNT03 procedure instance by parsing the execution context.
func NewDEPCNT03(csctx *CSContext, lib *Library) *DEPCNT03 {
	valdco := csctx.GetFloat("VALDCO", 0.0)
	return &DEPCNT03{
		ctx:             csctx,
		lib:             lib,
		valdco:          valdco,
		quapos:          csctx.GetInt("QUAPOS", 0),
		isSafetyContour: valdco == csctx.Mariner.SafetyContour,
	}
}

// Execute runs the DEPCNT03 symbology procedure and returns rendering instructions.
func (d *DEPCNT03) Execute() ([]Instruction, error) {
	var instructions []Instruction

	// Add contour line
	instructions = append(instructions, d.contourLineInstruction())

	// Add depth label if enabled
	if d.shouldShowLabel() {
		if labelInst := d.getLabelInstructions(); labelInst != nil {
			instructions = append(instructions, labelInst...)
		}
	}

	return instructions, nil
}

// contourLineInstruction creates the appropriate line instruction based on contour type.
func (d *DEPCNT03) contourLineInstruction() *LSInstruction {
	if d.isSafetyContour {
		// Safety contour: 2 units wide, DEPSC color
		return &LSInstruction{
			Style: d.getPositionQualityStyle(),
			Width: 2,
			Color: "DEPSC",
		}
	}
	// Normal contour: 1 unit wide, DEPCN color
	return &LSInstruction{
		Style: d.getPositionQualityStyle(),
		Width: 1,
		Color: "DEPCN",
	}
}

// getPositionQualityStyle returns the line style based on position quality.
// Good quality (1, 10, 11) uses solid lines; uncertain quality uses dashed lines.
// TODO: Use d.ctx.HasComponents() for per-segment QUAPOS styling
func (d *DEPCNT03) getPositionQualityStyle() string {
	if d.quapos == 0 || d.quapos == 1 || d.quapos == 10 || d.quapos == 11 {
		return "SOLD"
	}
	return "DASH"
}

// shouldShowLabel returns true if contour labels should be displayed.
func (d *DEPCNT03) shouldShowLabel() bool {
	return d.ctx.Mariner.SafetyContourLabels && d.valdco > 0
}

// getLabelInstructions generates contour label instructions using SAFCON01.
func (d *DEPCNT03) getLabelInstructions() []Instruction {
	labelInst, err := d.lib.csSAFCON01(d.valdco, d.ctx.Mariner)
	if err == nil && labelInst != nil {
		return labelInst
	}
	return nil
}
