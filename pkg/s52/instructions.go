package s52

import (
	"fmt"
	"strconv"
	"strings"
)

// ParseTextInstruction parses a TX instruction string into a structured TextInstruction
// S-52 PresLib e4.0.0 Part I, Section 9.1: SHOWTEXT
// Format: TX(STRING, HJUST, VJUST, SPACE, 'CHARS', XOFFS, YOFFS, COLOUR, DISPLAY)
// Example: TX('DR',2,3,2,'15110',-1,1,CHBLK,50)
func ParseTextInstruction(instruction string) (*TextInstruction, error) {
	// Remove "TX(" prefix and ")" suffix
	if !strings.HasPrefix(instruction, "TX(") || !strings.HasSuffix(instruction, ")") {
		return nil, fmt.Errorf("invalid TX instruction format: %s", instruction)
	}

	args := instruction[3 : len(instruction)-1]

	// Split arguments, respecting quoted strings
	parts := splitTextArgs(args)
	if len(parts) < 9 {
		return nil, fmt.Errorf("TX instruction requires 9 arguments, got %d", len(parts))
	}

	// Parse integer parameters
	hjust, err := strconv.Atoi(parts[1])
	if err != nil {
		return nil, fmt.Errorf("invalid HJUST: %w", err)
	}

	vjust, err := strconv.Atoi(parts[2])
	if err != nil {
		return nil, fmt.Errorf("invalid VJUST: %w", err)
	}

	space, err := strconv.Atoi(parts[3])
	if err != nil {
		return nil, fmt.Errorf("invalid SPACE: %w", err)
	}

	xoffs, err := strconv.Atoi(parts[5])
	if err != nil {
		return nil, fmt.Errorf("invalid XOFFS: %w", err)
	}

	yoffs, err := strconv.Atoi(parts[6])
	if err != nil {
		return nil, fmt.Errorf("invalid YOFFS: %w", err)
	}

	display, err := strconv.Atoi(parts[8])
	if err != nil {
		return nil, fmt.Errorf("invalid DISPLAY: %w", err)
	}

	// Parse CHARS parameter
	chars := strings.Trim(parts[4], "'\"")
	font, err := parseCharsParameter(chars)
	if err != nil {
		return nil, fmt.Errorf("invalid CHARS: %w", err)
	}

	// Check if STRING parameter (parts[0]) was quoted
	// Quoted → literal text: TX('literal', ...)
	// Unquoted → attribute reference: TX(OBJNAM, ...)
	rawString := parts[0]
	isQuoted := strings.HasPrefix(rawString, "'") || strings.HasPrefix(rawString, "\"")
	text := strings.Trim(rawString, "'\"")

	return &TextInstruction{
		Text:                 text,
		IsAttributeReference: !isQuoted,
		HJust:                hjust,
		VJust:                vjust,
		Space:                space,
		Font:                 font,
		XOffset:              xoffs,
		YOffset:              yoffs,
		Color:                parts[7],
		Display:              display,
	}, nil
}

// parseCharsParameter parses the CHARS parameter
// Format: 'abcdd' where:
//
//	a = font style (1=serif)
//	b = weight (4=light, 5=medium, 6=bold)
//	c = slant (1=upright, 2=italic)
//	dd = body size in pica points
func parseCharsParameter(chars string) (FontSpec, error) {
	// Default: '15110' = serif, medium, upright, 10pt
	if len(chars) < 5 {
		return FontSpec{}, fmt.Errorf("CHARS must be 5 characters, got %d", len(chars))
	}

	style := int(chars[0] - '0')
	weight := int(chars[1] - '0')
	slant := int(chars[2] - '0')

	// Parse two-digit body size
	bodySize, err := strconv.Atoi(chars[3:5])
	if err != nil {
		return FontSpec{}, fmt.Errorf("invalid body size in CHARS: %w", err)
	}

	// Validate ranges
	if style < 1 || style > 9 {
		return FontSpec{}, fmt.Errorf("invalid font style: %d (must be 1-9)", style)
	}
	if weight < 4 || weight > 6 {
		return FontSpec{}, fmt.Errorf("invalid font weight: %d (must be 4-6)", weight)
	}
	if slant < 1 || slant > 2 {
		return FontSpec{}, fmt.Errorf("invalid font slant: %d (must be 1-2)", slant)
	}
	// S-52 spec says 6-99, but some test datasets use smaller values
	// Accept 1-99 to handle edge cases
	if bodySize < 1 || bodySize > 99 {
		return FontSpec{}, fmt.Errorf("invalid body size: %d (must be 1-99)", bodySize)
	}

	return FontSpec{
		Style:    style,
		Weight:   weight,
		Slant:    slant,
		BodySize: bodySize,
	}, nil
}

// splitTextArgs splits TX command arguments, respecting quoted strings
// Handles: TX('text',1,2,3,'15110',0,0,CHBLK,26)
func splitTextArgs(args string) []string {
	var result []string
	var current strings.Builder
	inQuote := false

	for i := 0; i < len(args); i++ {
		ch := args[i]

		switch ch {
		case '\'', '"':
			// Toggle quote state
			inQuote = !inQuote
			current.WriteByte(ch)
		case ',':
			if inQuote {
				// Inside quoted string, keep comma
				current.WriteByte(ch)
			} else {
				// End of argument
				result = append(result, strings.TrimSpace(current.String()))
				current.Reset()
			}
		default:
			current.WriteByte(ch)
		}
	}

	// Add last argument
	if current.Len() > 0 {
		result = append(result, strings.TrimSpace(current.String()))
	}

	return result
}

// InstructionType represents the type of S-52 instruction
type InstructionType string

const (
	InstructionSY     InstructionType = "SY"     // Point symbol
	InstructionLC     InstructionType = "LC"     // Complex line
	InstructionLS     InstructionType = "LS"     // Simple line
	InstructionAC     InstructionType = "AC"     // Area color
	InstructionAP     InstructionType = "AP"     // Area pattern
	InstructionTX     InstructionType = "TX"     // Text
	InstructionCS     InstructionType = "CS"     // Conditional symbology
	InstructionSector InstructionType = "SECTOR" // Light sector (generated by CS procedures)
)

// Instruction represents a parsed S-52 instruction
type Instruction interface {
	Type() InstructionType
	String() string // Returns S-52 instruction string representation
}

// SYInstruction - Point symbol command
// S-52 PresLib e4.0.0 Part I, Section 8.3.1: Point Symbols
// Format: SY(symbol_id[,rotation])
// Example: SY(ACHARE51) or SY(LIGHTDEF,135)
// Rotation is optional and specified in degrees clockwise from north
type SYInstruction struct {
	SymbolID string
	Rotation float64 // degrees clockwise, 0 if not specified
}

func (s *SYInstruction) Type() InstructionType { return InstructionSY }

func (s *SYInstruction) String() string {
	if s.Rotation != 0 {
		return fmt.Sprintf("SY(%s,%.0f)", s.SymbolID, s.Rotation)
	}
	return fmt.Sprintf("SY(%s)", s.SymbolID)
}

// LCInstruction - Complex line command
// S-52 PresLib e4.0.0 Part I, Section 8.3.2: Complex Line Styles
// Format: LC(linestyle_id)
// Example: LC(NAVARE51)
type LCInstruction struct {
	LineStyleID string
}

func (l *LCInstruction) Type() InstructionType { return InstructionLC }

func (l *LCInstruction) String() string {
	return fmt.Sprintf("LC(%s)", l.LineStyleID)
}

// LSInstruction - Simple line command
// S-52 PresLib e4.0.0 Part I, Section 8.3.3: Simple Line Styles
// Format: LS(style, width, color)
// Example: LS(SOLD,1,CHBLK)
type LSInstruction struct {
	Style string // "SOLD", "DASH", "DOTT"
	Width int    // 1-4 (thin, medium, thick, extra-thick)
	Color string // Color token (e.g., "CHBLK")
}

func (l *LSInstruction) Type() InstructionType { return InstructionLS }

func (l *LSInstruction) String() string {
	return fmt.Sprintf("LS(%s,%d,%s)", l.Style, l.Width, l.Color)
}

// ACInstruction - Area color command
// S-52 PresLib e4.0.0 Part I, Section 8.4: Area Fills
// Format: AC(color) or AC(color, transparency)
// Example: AC(DEPMD) or AC(DEPMD,2)
type ACInstruction struct {
	Color  string // Color token (e.g., "DEPMD", "CHBRN")
	Transp int    // Transparency: 0=opaque, 1=25%, 2=50%, 3=75%
}

func (a *ACInstruction) Type() InstructionType { return InstructionAC }

func (a *ACInstruction) String() string {
	if a.Transp > 0 {
		return fmt.Sprintf("AC(%s,%d)", a.Color, a.Transp)
	}
	return fmt.Sprintf("AC(%s)", a.Color)
}

// APInstruction - Area pattern command
// S-52 PresLib e4.0.0 Part I, Section 8.5: Area Patterns
// Format: AP(pattern_id)
// Example: AP(DIAMOND1)
type APInstruction struct {
	PatternID string
}

func (a *APInstruction) Type() InstructionType { return InstructionAP }

func (a *APInstruction) String() string {
	return fmt.Sprintf("AP(%s)", a.PatternID)
}

// TXInstruction wraps TextInstruction to implement Instruction interface
type TXInstruction struct {
	*TextInstruction
}

func (t *TXInstruction) Type() InstructionType { return InstructionTX }

func (t *TXInstruction) String() string {
	return fmt.Sprintf("TX('%s',%d,%d,%d,'%d%d%d%d%d',%d,%d,%s,%d)",
		t.Text, t.HJust, t.VJust, t.Space,
		t.Font.Style, t.Font.Weight, t.Font.Slant, t.Font.BodySize, 0,
		t.XOffset, t.YOffset, t.Color, t.Display)
}

// CSInstruction - Conditional symbology procedure
// S-52 PresLib e4.0.0 Part I, Section 13: Conditional Symbology
// Format: CS(procedure_name)
// Example: CS(DEPARE03)
type CSInstruction struct {
	ProcedureName string
}

func (c *CSInstruction) Type() InstructionType { return InstructionCS }

func (c *CSInstruction) String() string {
	return fmt.Sprintf("CS(%s)", c.ProcedureName)
}

// SectorInstruction - Light Sector command (generated by LIGHTS06 CS procedure)
// Not a standard S-52 instruction - generated by conditional symbology to represent
// light sectors with radial legs and arc fills.
// Used for navigation lights with sector attributes (SECTR1, SECTR2)
type SectorInstruction struct {
	StartAngle   float64 // SECTR1 in degrees (0-360, 0=North, clockwise)
	EndAngle     float64 // SECTR2 in degrees (0-360, 0=North, clockwise)
	Radius       float64 // VALNMR in nautical miles (nominal range)
	Color        string  // S-52 color token (LITRD, LITGN, LITYW, CHMGD, etc.)
	Transparency int     // 0=opaque, 1=25%, 2=50%, 3=75%
	ShowLegs     bool    // Draw radial leg lines at sector boundaries
}

func (s *SectorInstruction) Type() InstructionType { return InstructionSector }

func (s *SectorInstruction) String() string {
	return fmt.Sprintf("SECTOR(%.1f-%.1f,%.1fnm,%s,%d)", s.StartAngle, s.EndAngle, s.Radius, s.Color, s.Transparency)
}

// ParseInstructions parses a complete S-52 instruction string
// Handles compound instructions like "SY(ACHARE51);AC(CHBRN);TX('foo',1,2,3,'15110',0,0,CHBLK,21)"
// Returns a slice of instruction objects in order
func ParseInstructions(instruction string) ([]Instruction, error) {
	if instruction == "" {
		return []Instruction{}, nil
	}

	// Split by semicolon (respecting quotes and parens)
	parts := splitInstructions(instruction)

	var result []Instruction
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		parsed, err := parseSingleInstruction(part)
		if err != nil {
			return nil, fmt.Errorf("parse instruction %q: %w", part, err)
		}
		result = append(result, parsed)
	}

	return result, nil
}

// parseSingleInstruction parses a single S-52 instruction command
func parseSingleInstruction(instruction string) (Instruction, error) {
	if !strings.Contains(instruction, "(") || !strings.HasSuffix(instruction, ")") {
		return nil, fmt.Errorf("invalid instruction format: %s", instruction)
	}

	parenIdx := strings.Index(instruction, "(")
	cmdType := instruction[:parenIdx]
	args := instruction[parenIdx+1 : len(instruction)-1]

	switch cmdType {
	case "SY":
		return parseSY(args)
	case "LC":
		return parseLC(args)
	case "LS":
		return parseLS(args)
	case "AC":
		return parseAC(args)
	case "AP":
		return parseAP(args)
	case "TX":
		return parseTX(args)
	case "TE":
		// TE (Text with format) - has 10 params vs TX's 9
		// Format: TE('format %s','ATTR',hjust,vjust,space,'chars',xoffs,yoffs,color,display)
		return parseTE(args)
	case "CS":
		return parseCS(args)
	default:
		return nil, fmt.Errorf("unknown instruction type: %s", cmdType)
	}
}

func parseSY(args string) (*SYInstruction, error) {
	if args == "" {
		return nil, fmt.Errorf("SY instruction requires symbol ID")
	}

	// Parse symbol ID and optional rotation
	// Format: SY(symbol_id) or SY(symbol_id,rotation)
	parts := strings.Split(args, ",")
	symbolID := strings.TrimSpace(parts[0])
	rotation := 0.0

	if len(parts) >= 2 {
		// Parse rotation parameter (degrees)
		rotStr := strings.TrimSpace(parts[1])
		if rotStr != "" {
			var err error
			rotation, err = strconv.ParseFloat(rotStr, 64)
			if err != nil {
				return nil, fmt.Errorf("invalid rotation value: %s", rotStr)
			}
		}
	}

	return &SYInstruction{
		SymbolID: symbolID,
		Rotation: rotation,
	}, nil
}

func parseLC(args string) (*LCInstruction, error) {
	if args == "" {
		return nil, fmt.Errorf("LC instruction requires linestyle ID")
	}
	return &LCInstruction{LineStyleID: args}, nil
}

func parseLS(args string) (*LSInstruction, error) {
	// Format: LS(SOLD,1,CHBLK)
	parts := strings.Split(args, ",")
	if len(parts) != 3 {
		return nil, fmt.Errorf("LS instruction requires 3 arguments (style,width,color), got %d", len(parts))
	}

	width, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil {
		return nil, fmt.Errorf("invalid LS width: %w", err)
	}

	// Note: S-52 spec mentions 1-4 but actual DAI files use wider range
	// Accept any positive integer width
	if width < 1 {
		return nil, fmt.Errorf("invalid LS width %d: must be positive", width)
	}

	return &LSInstruction{
		Style: strings.TrimSpace(parts[0]),
		Width: width,
		Color: strings.TrimSpace(parts[2]),
	}, nil
}

func parseAC(args string) (*ACInstruction, error) {
	// Format: AC(COLOUR) or AC(COLOUR,TRANSP)
	parts := strings.Split(args, ",")

	if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" {
		return nil, fmt.Errorf("AC instruction requires color token")
	}

	color := strings.TrimSpace(parts[0])
	transp := 0

	if len(parts) > 1 {
		var err error
		transp, err = strconv.Atoi(strings.TrimSpace(parts[1]))
		if err != nil {
			return nil, fmt.Errorf("invalid AC transparency: %w", err)
		}
		if transp < 0 || transp > 3 {
			return nil, fmt.Errorf("invalid AC transparency %d: must be 0-3", transp)
		}
	}

	return &ACInstruction{
		Color:  color,
		Transp: transp,
	}, nil
}

func parseAP(args string) (*APInstruction, error) {
	if args == "" {
		return nil, fmt.Errorf("AP instruction requires pattern ID")
	}
	return &APInstruction{PatternID: args}, nil
}

func parseTX(args string) (*TXInstruction, error) {
	// Reuse existing ParseTextInstruction logic
	fullInstruction := "TX(" + args + ")"
	textInstr, err := ParseTextInstruction(fullInstruction)
	if err != nil {
		return nil, err
	}
	return &TXInstruction{TextInstruction: textInstr}, nil
}

func parseTE(args string) (*TXInstruction, error) {
	// TE (Text with format) has 10 params: format, attr, hjust, vjust, space, chars, xoffs, yoffs, color, display
	// TX has 9 params: string, hjust, vjust, space, chars, xoffs, yoffs, color, display
	// Convert TE to TX by dropping the format string and using just the attribute name

	parts := splitTextArgs(args)
	if len(parts) < 10 {
		return nil, fmt.Errorf("TE instruction requires 10 arguments, got %d", len(parts))
	}

	// Build the base TX from the remaining params, using the attribute name as a
	// fallback text. Then preserve the format string + attribute list so the
	// portrayal layer can do S-52 §8.3.3.3 printf substitution rather than
	// dropping the format (which the Zig reference does, e.g. "clr op %4.1lf").
	txArgs := parts[1] + "," + strings.Join(parts[2:], ",")
	tx, err := parseTX(txArgs)
	if err != nil {
		return nil, err
	}
	tx.Format = strings.Trim(parts[0], "'\"")
	for _, a := range strings.Split(strings.Trim(parts[1], "'\""), ",") {
		if a = strings.TrimSpace(a); a != "" {
			tx.FormatAttrs = append(tx.FormatAttrs, a)
		}
	}
	return tx, nil
}

func parseCS(args string) (*CSInstruction, error) {
	if args == "" {
		return nil, fmt.Errorf("CS instruction requires procedure name")
	}
	return &CSInstruction{ProcedureName: args}, nil
}

// splitInstructions splits compound instructions like "SY(ACHARE51);AC(CHBRN)"
// Respects quotes and parentheses
func splitInstructions(instruction string) []string {
	var result []string
	var current strings.Builder
	parenDepth := 0
	inQuote := false

	for i := 0; i < len(instruction); i++ {
		ch := instruction[i]

		switch ch {
		case '\'', '"':
			inQuote = !inQuote
			current.WriteByte(ch)
		case '(':
			if !inQuote {
				parenDepth++
			}
			current.WriteByte(ch)
		case ')':
			if !inQuote {
				parenDepth--
			}
			current.WriteByte(ch)
		case ';':
			if !inQuote && parenDepth == 0 {
				// End of instruction
				result = append(result, current.String())
				current.Reset()
			} else {
				current.WriteByte(ch)
			}
		default:
			current.WriteByte(ch)
		}
	}

	// Add last instruction
	if current.Len() > 0 {
		result = append(result, current.String())
	}

	return result
}

// ParseInstruction parses any S-52 instruction string and returns the command type
// DEPRECATED: Use ParseInstructions instead for better type safety
// For TX commands, returns *TextInstruction
// For other commands, returns the raw string for now (TODO: add more types)
func ParseInstruction(instruction string) (interface{}, error) {
	if strings.HasPrefix(instruction, "TX(") {
		return ParseTextInstruction(instruction)
	}
	// TODO: Add parsers for SY, LC, LS, AC, AP, CS
	return instruction, nil
}
