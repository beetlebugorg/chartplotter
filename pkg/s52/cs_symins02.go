package s52

import (
	"fmt"
	"strings"
)

// SYMINS02 represents the Symbol Installation procedure for NEWOBJ objects.
// Parses a SYMINS attribute containing symbology instructions.
//
// S-52 Section 13.2.18: SYMINS02 (pages 98-99)
//
// The NEWOBJ feature class is used for future IMO requirements that cannot be
// encoded by existing object classes. The SYMINS attribute contains a string
// of symbology instructions like: "SY(SYMBOL01);LS(DASH,2,CHMGD)"
//
// Valid instructions: AC(), AP(), LS(), LC(), SY(), TX(), TE()
type SYMINS02 struct {
	ctx       *CSContext
	lib       *Library
	symins    string // SYMINS attribute value
	hasSYMINS bool   // Whether SYMINS exists
	geometry  string // Geometry type
}

// NewSYMINS02 creates a new SYMINS02 procedure instance by parsing the execution context.
func NewSYMINS02(csctx *CSContext, lib *Library) *SYMINS02 {
	symins := csctx.GetString("SYMINS", "")
	geometry := csctx.GeometryType
	if geometry == "" {
		geometry = "Area" // Default to Area if unknown
	}

	return &SYMINS02{
		ctx:       csctx,
		lib:       lib,
		symins:    symins,
		hasSYMINS: csctx.Has("SYMINS") && symins != "",
		geometry:  geometry,
	}
}

// Execute runs the SYMINS02 symbology procedure and returns rendering instructions.
func (s *SYMINS02) Execute() ([]Instruction, error) {
	// If no SYMINS attribute, use default symbolization
	if !s.hasSYMINS {
		return s.defaultSymbology(), nil
	}

	// Parse and validate symbology instructions
	instructions, valid := s.parseInstructions()

	// If no valid instructions found, use default
	if !valid {
		return s.defaultSymbology(), nil
	}

	return instructions, nil
}

// parseInstructions parses the SYMINS attribute into instructions.
// Instructions can be: AC(), AP(), LS(), LC(), SY(), TX(), TE()
// They must be added in the order they appear. Invalid instructions are skipped.
func (s *SYMINS02) parseInstructions() ([]Instruction, bool) {
	var result []Instruction

	// Split on semicolon to get individual instructions
	parts := strings.Split(s.symins, ";")

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		// Parse single instruction
		if instr := s.parseSingleInstruction(part); instr != nil {
			result = append(result, instr)
		}
	}

	// Valid only if we got at least one instruction
	return result, len(result) > 0
}

// parseSingleInstruction parses one symbology instruction.
// Returns nil if invalid or incompatible with geometry type.
func (s *SYMINS02) parseSingleInstruction(instr string) Instruction {
	// Find opening and closing parentheses
	openIdx := strings.Index(instr, "(")
	closeIdx := strings.LastIndex(instr, ")")

	if openIdx == -1 || closeIdx == -1 || closeIdx < openIdx {
		return nil // Malformed
	}

	cmd := strings.TrimSpace(instr[:openIdx])
	params := strings.TrimSpace(instr[openIdx+1 : closeIdx])

	// Validate instruction type and geometry compatibility
	switch cmd {
	case "SY": // Point symbol - valid for all geometries
		return s.parseSY(params)
	case "LS": // Simple line - valid for Line and Area
		if s.geometry == "Point" {
			return nil
		}
		return s.parseLS(params)
	case "LC": // Complex line - valid for Line and Area
		if s.geometry == "Point" {
			return nil
		}
		return s.parseLC(params)
	case "AC": // Area color - valid for Area only
		if s.geometry != "Area" {
			return nil
		}
		return s.parseAC(params)
	case "AP": // Area pattern - valid for Area only
		if s.geometry != "Area" {
			return nil
		}
		return s.parseAP(params)
	case "TX", "TE": // Text - valid for all geometries
		return s.parseText(cmd, params)
	default:
		return nil // Unrecognized instruction
	}
}

// parseSY parses a SY(symbol) instruction.
func (s *SYMINS02) parseSY(params string) Instruction {
	symbolName := strings.TrimSpace(params)
	if symbolName == "" {
		return nil
	}
	return &SYInstruction{SymbolID: symbolName}
}

// parseLS parses a LS(style,width,color) instruction.
func (s *SYMINS02) parseLS(params string) Instruction {
	parts := strings.Split(params, ",")
	if len(parts) != 3 {
		return nil
	}

	style := strings.TrimSpace(parts[0])
	color := strings.TrimSpace(parts[2])

	var width int
	_, err := fmt.Sscanf(strings.TrimSpace(parts[1]), "%d", &width)
	if err != nil {
		return nil
	}

	// Validate style
	if style != "SOLD" && style != "DASH" && style != "DOTT" {
		return nil
	}

	return &LSInstruction{Style: style, Width: width, Color: color}
}

// parseLC parses a LC(pattern) instruction.
func (s *SYMINS02) parseLC(params string) Instruction {
	patternName := strings.TrimSpace(params)
	if patternName == "" {
		return nil
	}
	return &LCInstruction{LineStyleID: patternName}
}

// parseAC parses an AC(color) instruction.
func (s *SYMINS02) parseAC(params string) Instruction {
	color := strings.TrimSpace(params)
	if color == "" {
		return nil
	}
	return &ACInstruction{Color: color}
}

// parseAP parses an AP(pattern) instruction.
func (s *SYMINS02) parseAP(params string) Instruction {
	patternName := strings.TrimSpace(params)
	if patternName == "" {
		return nil
	}
	return &APInstruction{PatternID: patternName}
}

// parseText parses a TX(...) or TE(...) instruction using the same full
// parameter parser as the main LUPT path, so the text string/attribute, the
// justification/font/offset, and the text-group number (last param) are all
// extracted properly — instead of stuffing the raw parameter list in as the
// label (the old stub showed e.g. "'V-AIS',3,2,2,'15110',2,0,CHMGD,11").
// Malformed instructions are dropped (S-52 §9.1).
func (s *SYMINS02) parseText(cmd, params string) Instruction {
	var tx *TXInstruction
	var err error
	if cmd == "TE" {
		tx, err = parseTE(params)
	} else {
		tx, err = parseTX(params)
	}
	if err != nil || tx == nil {
		return nil
	}
	return tx
}

// defaultSymbology returns default symbology based on geometry type.
// Per spec pages 98-99:
//   - Point: SY(NEWOBJ01)
//   - Line: LC(NEWOBJ01)
//   - Area: SY(NEWOBJ01) + LS(DASH,2,CHMGD) boundary
func (s *SYMINS02) defaultSymbology() []Instruction {
	switch s.geometry {
	case "Point":
		return []Instruction{
			&SYInstruction{SymbolID: "NEWOBJ01"},
		}
	case "Line":
		return []Instruction{
			&LCInstruction{LineStyleID: "NEWOBJ01"},
		}
	case "Area":
		return []Instruction{
			&SYInstruction{SymbolID: "NEWOBJ01"},                    // Center symbol
			&LSInstruction{Style: "DASH", Width: 2, Color: "CHMGD"}, // Boundary
		}
	default:
		// Unknown geometry - use point default
		return []Instruction{
			&SYInstruction{SymbolID: "NEWOBJ01"},
		}
	}
}
