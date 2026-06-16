// Package dai implements HP-GL/DAI command parsing for S-52 symbol vector commands.
//
// References:
// - specs/DAI_TO_SVG_RENDERING_SPEC.md - Complete HP-GL/DAI command specification
// - specs/s52-dai-format.md section "Drawing Command Language"
// - HP-GL specification for pen plotter commands (PU, PD, CI, PM, etc.)
//
// The HP-GL parser converts semicolon-separated DAI vector commands into structured
// vector graphics primitives for SVG rendering.
package s52

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// daiVectorParser implements a robust HP-GL/DAI command parser based on the HP-GL specification.
// Supports all S-52 DAI vector commands as defined in specs/DAI_TO_SVG_RENDERING_SPEC.md
type daiVectorParser struct {
	// Current plotter state
	penDown        bool
	position       Point
	coordinateMode CoordinateMode
	polygonMode    bool
	polygonActive  bool
	strokeWidth    int  // 1, 2, or 3 (maps to 0.3, 0.6, 0.9mm)
	strokeType     int  // -1 (default/rounded), 0 (butt/miter), 1 (round), 2 (square/bevel)
	currentRole    rune // 'A' through 'Z' for DAI color roles
	fillType       int
	strokeWidthSet bool // Track if SW was explicitly set in current sequence

	// Transparency support (Phase 3)
	transparency      int    // 0=opaque (ST0), 1=25% (ST1), 2=50% (ST2), 3=75% (ST3)
	transparencyToken string // Current color token for transparency application

	// Polygon building
	currentPolygon *VectorCommand
	polygonRings   [][]Point
	polygonCircles []int // Indices of circles created during polygon mode

	// Pattern spacing for line complex rendering
	patternSpacing string // Commands like S501375W1
	currentRing    []Point

	// Output commands
	commands []VectorCommand

	// For multi-line polygons
	lastCompletedPolygon *VectorCommand

	// Phase 1 enhancements - Extended HPGL state
	rotation       float64            // Current rotation angle (RO command)
	clipWindow     *Rectangle         // Active clipping window (IW command)
	penMovement    []Point            // Track pen movement for SC orientation
	symbolDatabase map[string]*Symbol // For SC commands - symbol lookup
}

// CoordinateMode represents absolute or relative coordinate interpretation
type CoordinateMode int

const (
	ModeAbsolute CoordinateMode = iota
	ModeRelative
)

// newDaiVectorParser creates a new HP-GL/DAI parser instance
func newDaiVectorParser() *daiVectorParser {
	return &daiVectorParser{
		penDown:        false,
		position:       Point{X: 0, Y: 0},
		coordinateMode: ModeAbsolute,
		strokeWidth:    1,     // Default to thin width (S-52 standard)
		strokeType:     -1,    // Default to rounded
		currentRole:    'C',   // Default role
		strokeWidthSet: false, // Track if SW was explicitly set
		commands:       make([]VectorCommand, 0),

		// Transparency support (Phase 3)
		transparency:      0,  // Default to opaque
		transparencyToken: "", // No token by default

		// Phase 1 enhancements - Initialize new fields
		rotation:       0.0,
		clipWindow:     nil,
		penMovement:    make([]Point, 0),
		symbolDatabase: make(map[string]*Symbol),
	}
}

// ParseCommands parses a semicolon-delimited string of HP-GL/DAI commands.
// Processes SVCT command strings from DAI symbol definitions.
// Reference: specs/DAI_TO_SVG_RENDERING_SPEC.md section "Vector Command Semantics"
func (p *daiVectorParser) ParseCommands(commandString string) error {
	// Split by semicolon and process each command
	commands := strings.Split(commandString, ";")

	for _, cmd := range commands {
		cmd = strings.TrimSpace(cmd)
		if cmd == "" {
			continue
		}

		if err := p.parseCommand(cmd); err != nil {
			return fmt.Errorf("error parsing command '%s': %v", cmd, err)
		}
	}

	return nil
}

// parseCommand parses and executes a single HP-GL/DAI command
func (p *daiVectorParser) parseCommand(cmd string) error {
	// Extract command mnemonic (first 2-3 characters)
	if len(cmd) < 2 {
		return fmt.Errorf("command too short: %s", cmd)
	}

	// Handle different command types based on prefix
	switch {
	// Pen control
	case strings.HasPrefix(cmd, "PU"):
		return p.handlePenUp(cmd)
	case strings.HasPrefix(cmd, "PD"):
		return p.handlePenDown(cmd)

	// Coordinate mode
	case strings.HasPrefix(cmd, "PA"):
		return p.handlePlotAbsolute(cmd)
	case strings.HasPrefix(cmd, "PR"):
		return p.handlePlotRelative(cmd)

	// Polygon commands
	case cmd == "PM0":
		return p.handlePolygonStart()
	case cmd == "PM1":
		return p.handlePolygonContinue()
	case cmd == "PM2":
		return p.handlePolygonEnd()
	case cmd == "FP":
		return p.handleFillPolygon()
	case cmd == "EP":
		return p.handleEdgePolygon()

	// Drawing commands
	case strings.HasPrefix(cmd, "CI"):
		return p.handleCircle(cmd)

	// Phase 1 - Arc commands
	case strings.HasPrefix(cmd, "AA"):
		return p.handleAbsoluteArc(cmd)
	case strings.HasPrefix(cmd, "AR"):
		return p.handleRelativeArc(cmd)

	// Phase 1 - Rectangle commands
	case strings.HasPrefix(cmd, "EA"), strings.HasPrefix(cmd, "ER"),
		strings.HasPrefix(cmd, "RA"), strings.HasPrefix(cmd, "RR"):
		return p.handleRectangle(cmd)

	// Phase 1 - Symbol call
	case strings.HasPrefix(cmd, "SC"):
		return p.handleSymbolCall(cmd)

	// Phase 1 - System commands
	case cmd == "IN":
		return p.handleInitialize(cmd)
	case cmd == "DF":
		return p.handleDefault(cmd)
	case strings.HasPrefix(cmd, "RO"):
		return p.handleRotate(cmd)
	case strings.HasPrefix(cmd, "IW"):
		return p.handleInputWindow(cmd)

	// DAI stroke commands
	case strings.HasPrefix(cmd, "SP"):
		return p.handleSelectPen(cmd)
	case strings.HasPrefix(cmd, "SW"):
		return p.handleStrokeWidth(cmd)
	case strings.HasPrefix(cmd, "ST"):
		return p.handleStrokeType(cmd)

	// Fill commands
	case strings.HasPrefix(cmd, "FT"):
		return p.handleFillType(cmd)

	// Pattern spacing commands like S501375W1
	case strings.HasPrefix(cmd, "S") && len(cmd) > 1:
		return p.handlePatternSpacing(cmd)

	default:
		// Unknown command - log but don't fail
		fmt.Printf("Unknown command: %s\n", cmd)
	}

	return nil
}

// handlePenUp processes PU (Pen Up) command - move without drawing.
// Reference: specs/DAI_TO_SVG_RENDERING_SPEC.md section "Geometry Builders"
func (p *daiVectorParser) handlePenUp(cmd string) error {
	p.penDown = false

	// PU resets stroke type to default (rounded)
	p.strokeType = -1

	// PU resets stroke width to default only if SW wasn't explicitly set
	if !p.strokeWidthSet {
		p.strokeWidth = 1 // Default to thin width
	}

	// Parse optional coordinates
	coords := ""
	if len(cmd) > 2 {
		coords = cmd[2:]
	}

	if coords != "" {
		points, err := parseMultipleCoordinates(coords)
		if err != nil {
			return err
		}

		if len(points) > 0 {
			// Move to last point
			lastPoint := points[len(points)-1]
			p.position = p.transformPoint(lastPoint)

			// In polygon mode, PU starts a new ring (hole)
			if p.polygonMode && p.currentPolygon != nil {
				// Save current ring if it has points
				if len(p.currentRing) > 0 {
					p.polygonRings = append(p.polygonRings, p.currentRing)
				}
				// Start new ring
				p.currentRing = make([]Point, 0)
			}
		}
	}

	return nil
}

// handlePenDown processes PD (Pen Down) command - draw line segments.
// PD with no coordinates creates a dot at current position.
// Reference: specs/DAI_TO_SVG_RENDERING_SPEC.md section "Geometry Builders"
func (p *daiVectorParser) handlePenDown(cmd string) error {
	startPos := p.position
	wasPenUp := !p.penDown
	p.penDown = true

	// Parse optional coordinates
	coords := ""
	if len(cmd) > 2 {
		coords = cmd[2:]
	}

	// No coordinates - plot a dot
	if coords == "" {
		if !p.polygonMode {
			dot := VectorCommand{
				Type:        "DOT",
				Points:      []Point{p.position},
				StrokeWidth: p.strokeWidth,
				StrokeType:  p.strokeType,
				Role:        p.currentRole,
			}
			p.commands = append(p.commands, dot)
		}
		return nil
	}

	// Parse coordinate points
	points, err := parseMultipleCoordinates(coords)
	if err != nil {
		return err
	}

	if len(points) == 0 {
		return nil
	}

	// Transform points based on coordinate mode
	transformedPoints := make([]Point, 0, len(points)+1)

	// If pen was up, include starting position
	if wasPenUp {
		transformedPoints = append(transformedPoints, startPos)
	}

	// Transform all points
	for _, pt := range points {
		transformedPoints = append(transformedPoints, p.transformPoint(pt))
		p.position = p.transformPoint(pt)
	}

	// Handle polygon mode
	if p.polygonMode && p.currentPolygon != nil {
		// Add points to current ring (skip starting position if pen was up)
		if wasPenUp && len(transformedPoints) > 1 {
			p.currentRing = append(p.currentRing, transformedPoints[1:]...)
			p.currentPolygon.Points = append(p.currentPolygon.Points, transformedPoints[1:]...)
		} else {
			p.currentRing = append(p.currentRing, transformedPoints...)
			p.currentPolygon.Points = append(p.currentPolygon.Points, transformedPoints...)
		}
		return nil
	}

	// Check if we should append to the last command (continuous pen-down drawing)
	// If pen was already down and the last command is a PD with matching attributes,
	// append to it instead of creating a new command
	if !wasPenUp && len(p.commands) > 0 {
		lastCmd := &p.commands[len(p.commands)-1]
		if lastCmd.Type == "PD" &&
			lastCmd.StrokeWidth == p.strokeWidth &&
			lastCmd.StrokeType == p.strokeType &&
			lastCmd.Role == p.currentRole {
			// Append points to existing path
			// When pen was already down, transformedPoints doesn't include starting position
			// so we can append all points
			lastCmd.Points = append(lastCmd.Points, transformedPoints...)
			return nil
		}
	}

	// Normal line drawing - create a new command with all points
	// If pen was already down but we're creating a new command due to attribute changes,
	// we need to include the starting position to maintain continuity
	pathPoints := transformedPoints
	if !wasPenUp && len(p.commands) > 0 {
		// Include starting position when pen was down but attributes changed
		pathPoints = append([]Point{startPos}, transformedPoints...)
	}

	line := VectorCommand{
		Type:        "PD",
		Points:      pathPoints,
		StrokeWidth: p.strokeWidth,
		StrokeType:  p.strokeType,
		Role:        p.currentRole,
	}
	p.commands = append(p.commands, line)

	return nil
}

// handlePlotAbsolute processes PA command
func (p *daiVectorParser) handlePlotAbsolute(cmd string) error {
	p.coordinateMode = ModeAbsolute

	// Parse optional coordinates
	if len(cmd) > 2 {
		coords := cmd[2:]
		points, err := parseMultipleCoordinates(coords)
		if err != nil {
			return err
		}

		// Move/draw to points
		for _, pt := range points {
			if p.penDown {
				// Draw line
				line := VectorCommand{
					Type:        "PD",
					Points:      []Point{p.position, pt},
					StrokeWidth: p.strokeWidth,
					StrokeType:  p.strokeType,
					Role:        p.currentRole,
				}
				p.commands = append(p.commands, line)
			}
			p.position = pt
		}
	}

	return nil
}

// handlePlotRelative processes PR command
func (p *daiVectorParser) handlePlotRelative(cmd string) error {
	p.coordinateMode = ModeRelative

	// Parse optional coordinates
	if len(cmd) > 2 {
		coords := cmd[2:]
		points, err := parseMultipleCoordinates(coords)
		if err != nil {
			return err
		}

		// Move/draw relative to current position
		for _, pt := range points {
			newPos := Point{
				X: p.position.X + pt.X,
				Y: p.position.Y + pt.Y,
			}

			if p.penDown {
				// Draw line
				line := VectorCommand{
					Type:        "PD",
					Points:      []Point{p.position, newPos},
					StrokeWidth: p.strokeWidth,
					StrokeType:  p.strokeType,
					Role:        p.currentRole,
				}
				p.commands = append(p.commands, line)
			}
			p.position = newPos
		}
	}

	return nil
}

// handlePolygonStart processes PM0 (Polygon Mode Start) command.
// Begins polygon ring collection for filled areas.
// Reference: specs/DAI_TO_SVG_RENDERING_SPEC.md section "Polygon Mode (Areas)"
func (p *daiVectorParser) handlePolygonStart() error {
	p.polygonMode = true
	p.polygonActive = true
	p.polygonRings = make([][]Point, 0)
	p.polygonCircles = make([]int, 0) // Initialize for tracking circle indices
	p.currentRing = make([]Point, 0)

	p.currentPolygon = &VectorCommand{
		Type:         "POLYGON",
		Points:       make([]Point, 0),
		Rings:        make([][]Point, 0),
		StrokeWidth:  p.strokeWidth,
		StrokeType:   p.strokeType,
		Transparency: p.transparency,
		Role:         p.currentRole,
	}

	return nil
}

// handlePolygonContinue processes PM1 command
func (p *daiVectorParser) handlePolygonContinue() error {
	// Close current polygon and start new one
	if p.currentPolygon != nil {
		// Save current ring
		if len(p.currentRing) > 0 {
			p.polygonRings = append(p.polygonRings, p.currentRing)
		}

		// Save polygon
		p.currentPolygon.Rings = p.polygonRings
		p.commands = append(p.commands, *p.currentPolygon)
		p.lastCompletedPolygon = &p.commands[len(p.commands)-1]
	}

	// Start new polygon
	return p.handlePolygonStart()
}

// handlePolygonEnd processes PM2 (Polygon Mode End) command.
// Completes polygon ring definition.
// Reference: specs/DAI_TO_SVG_RENDERING_SPEC.md section "Polygon Mode (Areas)"
func (p *daiVectorParser) handlePolygonEnd() error {
	if p.currentPolygon != nil {
		// Save current ring
		if len(p.currentRing) > 0 {
			p.polygonRings = append(p.polygonRings, p.currentRing)
		}

		// Save polygon
		p.currentPolygon.Rings = p.polygonRings
		p.commands = append(p.commands, *p.currentPolygon)
		p.lastCompletedPolygon = &p.commands[len(p.commands)-1]

		p.currentPolygon = nil
	}

	p.polygonMode = false
	p.polygonActive = false
	p.currentRing = nil
	p.polygonRings = nil
	// Don't clear polygonCircles here - leave them for FP processing

	return nil
}

// handleFillPolygon processes FP (Fill Polygon) command.
// Upgrades the current/last polygon to filled status and applies transparency.
// Reference: specs/DAI_TO_SVG_RENDERING_SPEC.md section "Polygon Mode (Areas)"
func (p *daiVectorParser) handleFillPolygon() error {
	// Mark the appropriate polygon as filled and apply transparency
	if p.currentPolygon != nil {
		// FP during polygon definition
		p.currentPolygon.Type = "POLYGON_FILLED"
		p.currentPolygon.RawCommand += ";FP"
		p.currentPolygon.Transparency = p.transparency
	} else if p.lastCompletedPolygon != nil {
		// FP after PM2
		p.lastCompletedPolygon.Type = "POLYGON_FILLED"
		p.lastCompletedPolygon.RawCommand += ";FP"
		p.lastCompletedPolygon.Transparency = p.transparency
	}

	// Also mark any circles created during polygon mode as filled with transparency
	for _, idx := range p.polygonCircles {
		if idx >= 0 && idx < len(p.commands) {
			p.commands[idx].RawCommand += ";FP"
			p.commands[idx].Transparency = p.transparency
			// fmt.Printf("DEBUG: Added FP to circle at index %d: %s\n", idx, p.commands[idx].RawCommand)
		}
	}
	// fmt.Printf("DEBUG: FP processed %d circles\n", len(p.polygonCircles))

	// Clear the polygonCircles after FP processing
	p.polygonCircles = nil

	return nil
}

// handleEdgePolygon processes EP (Edge Polygon) command.
// Draws the outline of the most recently defined polygon.
// Reference: specs/DAI_TO_SVG_RENDERING_SPEC.md section "Polygon Mode (Areas)"
func (p *daiVectorParser) handleEdgePolygon() error {
	var targetPolygon *VectorCommand

	if p.currentPolygon != nil {
		// EP during polygon definition
		targetPolygon = p.currentPolygon
	} else if p.lastCompletedPolygon != nil {
		// EP after PM2
		targetPolygon = p.lastCompletedPolygon
	} else {
		return fmt.Errorf("EP command requires a defined polygon")
	}

	// Create edge command that references the polygon
	epCmd := VectorCommand{
		Type:        "EP",
		Points:      targetPolygon.Points,
		Rings:       targetPolygon.Rings,
		StrokeWidth: p.strokeWidth,
		StrokeType:  p.strokeType,
		Role:        p.currentRole,
		RawCommand:  "EP",
	}

	p.commands = append(p.commands, epCmd)
	return nil
}

// handleCircle processes CI (Circle) command with specified radius.
// Circle center is at last PU position.
// Reference: specs/DAI_TO_SVG_RENDERING_SPEC.md section "Geometry Builders"
func (p *daiVectorParser) handleCircle(cmd string) error {
	// Parse radius
	radiusStr := ""
	if len(cmd) > 2 {
		radiusStr = cmd[2:]
	}

	// Parse optional chord angle (ignore for now, default to 5)
	parts := strings.Split(radiusStr, ",")
	if len(parts) > 0 && parts[0] != "" {
		radius, err := strconv.ParseFloat(parts[0], 64)
		if err != nil {
			return err
		}

		// Make a copy of the current position to avoid mutation when position changes
		centerPos := p.position
		circle := VectorCommand{
			Type:        "CI",
			Center:      &centerPos,
			Points:      []Point{{X: radius, Y: 0}}, // Store radius as X coordinate
			StrokeWidth: p.strokeWidth,
			StrokeType:  p.strokeType,
			Role:        p.currentRole,
			RawCommand:  cmd,
		}
		p.commands = append(p.commands, circle)

		// If in polygon mode, also track this circle's index for potential FP command
		if p.polygonMode {
			idx := len(p.commands) - 1
			p.polygonCircles = append(p.polygonCircles, idx)
			// fmt.Printf("DEBUG: Tracking circle at index %d for polygon mode (role=%c)\n", idx, p.currentRole)
		}
	}

	return nil
}

// handleSelectPen processes SP commands including DAI role extensions (SPA-SPZ).
// SPx commands select stroke role for color mapping (A-Z roles).
// Reference: specs/DAI_TO_SVG_RENDERING_SPEC.md section "Stroke Context and Modifiers"
func (p *daiVectorParser) handleSelectPen(cmd string) error {
	if len(cmd) >= 3 {
		// DAI extension: SPA, SPB, SPC, etc., including lowercase (SPm, etc.)
		roleChar := cmd[2]
		if (roleChar >= 'A' && roleChar <= 'Z') || (roleChar >= 'a' && roleChar <= 'z') {
			// Preserve case for role characters - lowercase and uppercase have different meanings
			p.currentRole = rune(roleChar)

			// Create a command for role-based strokes
			roleCmd := VectorCommand{
				Type:        cmd[:3],
				Role:        p.currentRole,
				StrokeWidth: p.strokeWidth,
				StrokeType:  p.strokeType,
			}
			p.commands = append(p.commands, roleCmd)
		}
	} else if len(cmd) > 2 {
		// Standard SP with pen number (ignore for DAI)
	}

	return nil
}

// handleStrokeWidth processes SW (Stroke Width) command.
// Stores S-52 stroke width category (1=thin/0.3mm, 2=medium/0.6mm, 3=thick/0.9mm).
// Reference: specs/DAI_TO_SVG_RENDERING_SPEC.md section "Stroke Context and Modifiers"
func (p *daiVectorParser) handleStrokeWidth(cmd string) error {
	if len(cmd) > 2 {
		widthStr := cmd[2:]
		if width, err := strconv.Atoi(widthStr); err == nil {
			p.strokeWidth = width
			if p.strokeWidth < 1 {
				p.strokeWidth = 1
			}
			p.strokeWidthSet = true
		}
	}
	return nil
}

// handleStrokeType processes ST (Set Transparency) command per S-52 specification.
// ST sets transparency level: ST0→opaque, ST1→25%, ST2→50%, ST3→75%
// Transparency only affects polygon fill instruction (FP) while other instructions
// (AA, CI, EP, PD) produce opaque drawings.
// Reference: specs/reference/preslib/hpgl-command-reference.md (takes precedence)
func (p *daiVectorParser) handleStrokeType(cmd string) error {
	if len(cmd) > 2 {
		typeStr := cmd[2:]
		if value, err := strconv.Atoi(typeStr); err == nil {
			// ST sets transparency level for polygon fills
			if value >= 0 && value <= 3 {
				p.transparency = value
				// Note: strokeType is kept for backward compatibility but
				// the primary purpose is transparency per S-52 spec
				p.strokeType = value
			}
		}
	}
	return nil
}

// handleFillType processes FT command
func (p *daiVectorParser) handleFillType(cmd string) error {
	// Parse fill type parameters (simplified for now)
	if len(cmd) > 2 {
		params := cmd[2:]
		parts := strings.Split(params, ",")
		if len(parts) > 0 && parts[0] != "" {
			if ft, err := strconv.Atoi(parts[0]); err == nil {
				p.fillType = ft
			}
		}
	}
	return nil
}

// transformPoint transforms a point based on current coordinate mode
func (p *daiVectorParser) transformPoint(pt Point) Point {
	if p.coordinateMode == ModeAbsolute {
		return pt
	}
	// Relative mode
	return Point{
		X: p.position.X + pt.X,
		Y: p.position.Y + pt.Y,
	}
}

// GetCommands returns the parsed vector commands
func (p *daiVectorParser) GetCommands() []VectorCommand {
	return p.commands
}

// GetState returns current parser state for persistence
func (p *daiVectorParser) GetState() HPGLState {
	return HPGLState{
		PolygonMode:          p.polygonMode,
		CurrentPolygon:       p.currentPolygon,
		LastCompletedPolygon: p.lastCompletedPolygon,
		Position:             p.position,
		StrokeWidth:          p.strokeWidth,
		StrokeType:           p.strokeType,
		CurrentRole:          p.currentRole,
	}
}

// SetState restores parser state from previous parsing
func (p *daiVectorParser) SetState(state HPGLState) {
	p.polygonMode = state.PolygonMode
	p.currentPolygon = state.CurrentPolygon
	p.lastCompletedPolygon = state.LastCompletedPolygon
	p.position = state.Position
	p.strokeWidth = state.StrokeWidth
	p.strokeType = state.StrokeType
	p.currentRole = state.CurrentRole
}

// HPGLState holds persistent parser state
type HPGLState struct {
	PolygonMode          bool
	CurrentPolygon       *VectorCommand
	LastCompletedPolygon *VectorCommand
	Position             Point
	StrokeWidth          int
	StrokeType           int
	CurrentRole          rune
}

// Phase 1 Implementation - New HPGL command handlers

// calculateDistance calculates the Euclidean distance between two points
func (p *daiVectorParser) calculateDistance(p1, p2 Point) float64 {
	dx := p2.X - p1.X
	dy := p2.Y - p1.Y
	return math.Sqrt(dx*dx + dy*dy)
}

// calculateAngle calculates the angle from center to point in degrees
func (p *daiVectorParser) calculateAngle(center, point Point) float64 {
	dx := point.X - center.X
	dy := point.Y - center.Y
	return math.Atan2(dy, dx) * 180.0 / math.Pi
}

// handleAbsoluteArc processes AA (Absolute Arc) command
// Format: AA{center_x},{center_y},{sweep_angle}[,{chord_tolerance}]
func (p *daiVectorParser) handleAbsoluteArc(cmd string) error {
	params := strings.TrimPrefix(cmd, "AA")
	if params == "" {
		return fmt.Errorf("AA command requires parameters")
	}

	parts := strings.Split(params, ",")
	if len(parts) < 3 {
		return fmt.Errorf("AA command requires at least 3 parameters")
	}

	centerX, err := strconv.ParseFloat(parts[0], 64)
	if err != nil {
		return fmt.Errorf("invalid center X coordinate: %v", err)
	}

	centerY, err := strconv.ParseFloat(parts[1], 64)
	if err != nil {
		return fmt.Errorf("invalid center Y coordinate: %v", err)
	}

	sweepAngle, err := strconv.ParseFloat(parts[2], 64)
	if err != nil {
		return fmt.Errorf("invalid sweep angle: %v", err)
	}

	chordTolerance := 5.0 // Default chord tolerance
	if len(parts) > 3 {
		if ct, err := strconv.ParseFloat(parts[3], 64); err == nil {
			chordTolerance = ct
		}
	}

	center := Point{X: centerX, Y: centerY}
	return p.processArc(center, sweepAngle, chordTolerance, "AA", cmd)
}

// handleRelativeArc processes AR (Relative Arc) command
// Format: AR{delta_x},{delta_y},{sweep_angle}[,{chord_tolerance}]
func (p *daiVectorParser) handleRelativeArc(cmd string) error {
	params := strings.TrimPrefix(cmd, "AR")
	if params == "" {
		return fmt.Errorf("AR command requires parameters")
	}

	parts := strings.Split(params, ",")
	if len(parts) < 3 {
		return fmt.Errorf("AR command requires at least 3 parameters")
	}

	deltaX, err := strconv.ParseFloat(parts[0], 64)
	if err != nil {
		return fmt.Errorf("invalid delta X: %v", err)
	}

	deltaY, err := strconv.ParseFloat(parts[1], 64)
	if err != nil {
		return fmt.Errorf("invalid delta Y: %v", err)
	}

	sweepAngle, err := strconv.ParseFloat(parts[2], 64)
	if err != nil {
		return fmt.Errorf("invalid sweep angle: %v", err)
	}

	chordTolerance := 5.0
	if len(parts) > 3 {
		if ct, err := strconv.ParseFloat(parts[3], 64); err == nil {
			chordTolerance = ct
		}
	}

	// Calculate absolute center position
	center := Point{
		X: p.position.X + deltaX,
		Y: p.position.Y + deltaY,
	}

	return p.processArc(center, sweepAngle, chordTolerance, "AR", cmd)
}

// processArc creates an arc command and updates the position
func (p *daiVectorParser) processArc(center Point, sweepAngle, chordTolerance float64, cmdType, rawCmd string) error {
	radius := p.calculateDistance(p.position, center)
	startAngle := p.calculateAngle(center, p.position)

	arc := VectorCommand{
		Type:           cmdType,
		Center:         &center,
		Points:         []Point{{X: radius, Y: 0}}, // Store radius as X coordinate
		StartAngle:     startAngle,
		SweepAngle:     sweepAngle,
		Radius:         radius,
		ChordTolerance: chordTolerance,
		StrokeWidth:    p.strokeWidth,
		StrokeType:     p.strokeType,
		Role:           p.currentRole,
		RawCommand:     rawCmd,
	}

	p.commands = append(p.commands, arc)

	// Update position to arc end point
	endAngle := startAngle + sweepAngle
	p.position = Point{
		X: center.X + radius*math.Cos(endAngle*math.Pi/180.0),
		Y: center.Y + radius*math.Sin(endAngle*math.Pi/180.0),
	}

	// Track pen movement for SC orientation
	p.penMovement = append(p.penMovement, p.position)

	return nil
}

// handleRectangle processes rectangle commands (EA, ER, RA, RR)
func (p *daiVectorParser) handleRectangle(cmd string) error {
	var rectangleType string
	var params string
	var filled bool

	switch {
	case strings.HasPrefix(cmd, "EA"):
		rectangleType = "EA"
		params = strings.TrimPrefix(cmd, "EA")
		filled = false
	case strings.HasPrefix(cmd, "ER"):
		rectangleType = "ER"
		params = strings.TrimPrefix(cmd, "ER")
		filled = false
	case strings.HasPrefix(cmd, "RA"):
		rectangleType = "RA"
		params = strings.TrimPrefix(cmd, "RA")
		filled = true
	case strings.HasPrefix(cmd, "RR"):
		rectangleType = "RR"
		params = strings.TrimPrefix(cmd, "RR")
		filled = true
	default:
		return fmt.Errorf("unknown rectangle command: %s", cmd)
	}

	if params == "" {
		return fmt.Errorf("rectangle command requires parameters")
	}

	parts := strings.Split(params, ",")
	if len(parts) < 2 {
		return fmt.Errorf("rectangle command requires 2 parameters")
	}

	x, err := strconv.ParseFloat(parts[0], 64)
	if err != nil {
		return fmt.Errorf("invalid X coordinate: %v", err)
	}

	y, err := strconv.ParseFloat(parts[1], 64)
	if err != nil {
		return fmt.Errorf("invalid Y coordinate: %v", err)
	}

	var rect Rectangle
	if rectangleType == "ER" || rectangleType == "RR" {
		// Relative rectangle
		rect = Rectangle{
			X:      math.Min(p.position.X, p.position.X+x),
			Y:      math.Min(p.position.Y, p.position.Y+y),
			Width:  math.Abs(x),
			Height: math.Abs(y),
		}
	} else {
		// Absolute rectangle
		rect = Rectangle{
			X:      math.Min(p.position.X, x),
			Y:      math.Min(p.position.Y, y),
			Width:  math.Abs(x - p.position.X),
			Height: math.Abs(y - p.position.Y),
		}
	}

	rectCmd := VectorCommand{
		Type:        rectangleType,
		Rectangle:   &rect,
		Filled:      filled,
		StrokeWidth: p.strokeWidth,
		StrokeType:  p.strokeType,
		Role:        p.currentRole,
		RawCommand:  cmd,
	}

	p.commands = append(p.commands, rectCmd)

	// Update position to opposite corner
	if rectangleType == "ER" || rectangleType == "RR" {
		p.position.X += x
		p.position.Y += y
	} else {
		p.position.X = x
		p.position.Y = y
	}

	return nil
}

// handleSymbolCall processes SC (Symbol Call) command
// Format: SC{symbol_name},{orientation}[,{scale}]
func (p *daiVectorParser) handleSymbolCall(cmd string) error {
	params := strings.TrimPrefix(cmd, "SC")
	if params == "" {
		return fmt.Errorf("SC command requires parameters")
	}

	parts := strings.Split(params, ",")
	if len(parts) < 2 {
		return fmt.Errorf("SC command requires at least 2 parameters")
	}

	symbolName := strings.TrimSpace(parts[0])
	if len(symbolName) != 8 {
		return fmt.Errorf("symbol name must be 8 characters: %s", symbolName)
	}

	orientation, err := strconv.Atoi(parts[1])
	if err != nil {
		return fmt.Errorf("invalid orientation: %v", err)
	}

	scale := 1.0
	if len(parts) > 2 {
		if s, err := strconv.ParseFloat(parts[2], 64); err == nil {
			scale = s
		}
	}

	symbolCall := SymbolCall{
		SymbolName:   symbolName,
		Orientation:  orientation,
		Scale:        scale,
		CallPosition: p.position,
	}

	scCmd := VectorCommand{
		Type:        "SC",
		SymbolCall:  &symbolCall,
		StrokeWidth: p.strokeWidth,
		StrokeType:  p.strokeType,
		Role:        p.currentRole,
		RawCommand:  cmd,
	}

	p.commands = append(p.commands, scCmd)
	return nil
}

// handleInitialize processes IN (Initialize) command
func (p *daiVectorParser) handleInitialize(cmd string) error {
	// Reset all parser state to initial values
	p.penDown = false
	p.position = Point{X: 0, Y: 0}
	p.coordinateMode = ModeAbsolute
	p.polygonMode = false
	p.polygonActive = false
	p.strokeWidth = 1
	p.strokeType = -1
	p.currentRole = 'C'
	p.strokeWidthSet = false
	p.rotation = 0.0
	p.clipWindow = nil

	// Clear any active polygon
	p.currentPolygon = nil
	p.polygonRings = nil
	p.currentRing = nil
	p.polygonCircles = nil

	// Reset pen movement tracking
	p.penMovement = make([]Point, 0)

	// Add initialize command to output
	initCmd := VectorCommand{
		Type:       "IN",
		RawCommand: cmd,
	}
	p.commands = append(p.commands, initCmd)

	return nil
}

// handleDefault processes DF (Default) command
func (p *daiVectorParser) handleDefault(cmd string) error {
	// Restore default pen settings but preserve position
	p.strokeWidth = 1
	p.strokeType = -1
	p.currentRole = 'C'
	p.strokeWidthSet = false

	// Add default command to output
	dfCmd := VectorCommand{
		Type:       "DF",
		RawCommand: cmd,
	}
	p.commands = append(p.commands, dfCmd)

	return nil
}

// handleRotate processes RO (Rotate) command
func (p *daiVectorParser) handleRotate(cmd string) error {
	params := strings.TrimPrefix(cmd, "RO")

	angle := 0.0
	if params != "" {
		if a, err := strconv.ParseFloat(params, 64); err == nil {
			angle = a
		}
	}

	p.rotation = angle

	rotateCmd := VectorCommand{
		Type:       "RO",
		Rotation:   angle,
		RawCommand: cmd,
	}
	p.commands = append(p.commands, rotateCmd)

	return nil
}

// handleInputWindow processes IW (Input Window) command
func (p *daiVectorParser) handleInputWindow(cmd string) error {
	params := strings.TrimPrefix(cmd, "IW")

	if params == "" {
		// IW with no parameters disables clipping
		p.clipWindow = nil
	} else {
		parts := strings.Split(params, ",")
		if len(parts) < 4 {
			return fmt.Errorf("IW command requires 4 parameters or none")
		}

		x1, err := strconv.ParseFloat(parts[0], 64)
		if err != nil {
			return fmt.Errorf("invalid X1 coordinate: %v", err)
		}

		y1, err := strconv.ParseFloat(parts[1], 64)
		if err != nil {
			return fmt.Errorf("invalid Y1 coordinate: %v", err)
		}

		x2, err := strconv.ParseFloat(parts[2], 64)
		if err != nil {
			return fmt.Errorf("invalid X2 coordinate: %v", err)
		}

		y2, err := strconv.ParseFloat(parts[3], 64)
		if err != nil {
			return fmt.Errorf("invalid Y2 coordinate: %v", err)
		}

		clipWindow := &Rectangle{
			X:      math.Min(x1, x2),
			Y:      math.Min(y1, y2),
			Width:  math.Abs(x2 - x1),
			Height: math.Abs(y2 - y1),
		}

		p.clipWindow = clipWindow
	}

	iwCmd := VectorCommand{
		Type:       "IW",
		ClipWindow: p.clipWindow,
		RawCommand: cmd,
	}
	p.commands = append(p.commands, iwCmd)

	return nil
}

// SetSymbolDatabase sets the symbol database for SC command resolution
func (p *daiVectorParser) SetSymbolDatabase(symbols map[string]*Symbol) {
	p.symbolDatabase = symbols
}

// handlePatternSpacing parses pattern spacing commands like S501375W1
func (p *daiVectorParser) handlePatternSpacing(cmd string) error {
	// Pattern: S + code + spacing + W + width
	// Example: S501375W1 = S50 + 1375 (13.75mm spacing) + W1 (width 1)

	// For now, we'll store this info but the parsing logic may need refinement
	// The exact format might be different than assumed

	if len(cmd) < 4 {
		return nil // Too short to be valid
	}

	// Store the spacing command for use by subsequent PD commands
	p.patternSpacing = cmd

	return nil
}
