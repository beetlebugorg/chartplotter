package s52

import (
	"fmt"
)

// GetSymbol returns a symbol definition by ID
//
// S-52 PresLib e4.0.0, Part I, Section 8.2: Point Symbols
//
// Symbol IDs are like: "ACHARE51", "BOYLAT12", "LIGHTS11"
//
// Returns nil if symbol not found
func (l *Library) GetSymbol(symbolID string) (*Symbol, error) {
	internalSymbol, exists := l.symbols[symbolID]
	if !exists {
		return nil, fmt.Errorf("symbol %s not found", symbolID)
	}

	return convertSymbol(internalSymbol), nil
}

// ListSymbols returns all available symbol IDs
func (l *Library) ListSymbols() []string {
	symbols := make([]string, 0, len(l.symbols))
	for id := range l.symbols {
		symbols = append(symbols, id)
	}
	return symbols
}

// GetPattern returns an area pattern definition by ID
//
// S-52 PresLib e4.0.0, Part I, Section 8.5: Area Patterns
//
// Pattern IDs are like: "DIAMOND1", "CROSS1", "CHCRF"
//
// Returns nil if pattern not found
func (l *Library) GetPattern(patternID string) (*Pattern, error) {
	internalPattern, exists := l.patterns[patternID]
	if !exists {
		return nil, fmt.Errorf("pattern %s not found", patternID)
	}

	return convertPattern(internalPattern), nil
}

// ListPatterns returns all available pattern IDs
func (l *Library) ListPatterns() []string {
	patterns := make([]string, 0, len(l.patterns))
	for id := range l.patterns {
		patterns = append(patterns, id)
	}
	return patterns
}

// GetLineStyle returns a line style definition by ID
//
// S-52 PresLib e4.0.0, Part I, Section 8.3.2: Complex Line Styles
//
// Line style IDs are like: "SOLD", "DASH", "DOTT"
//
// Returns nil if line style not found
func (l *Library) GetLineStyle(lineStyleID string) (*Linestyle, error) {
	internalStyle, exists := l.linestyles[lineStyleID]
	if !exists {
		return nil, fmt.Errorf("line style %s not found", lineStyleID)
	}

	return convertLineStyle(internalStyle), nil
}

// ListLineStyles returns all available line style IDs
func (l *Library) ListLineStyles() []string {
	styles := make([]string, 0, len(l.linestyles))
	for id := range l.linestyles {
		styles = append(styles, id)
	}
	return styles
}

// parseHPGLtoPrimitives converts HPGL command stream to vector primitives
// Translates internal drawing commands (PD, CI, POLYGON_FILLED) into
// high-level LINE, CIRCLE, FILL primitives for rendering
func parseHPGLtoPrimitives(commands []VectorCommand) []Primitive {
	primitives := make([]Primitive, 0, len(commands))

	for _, cmd := range commands {
		switch cmd.Type {
		case "PD": // Pen down - line drawing
			if len(cmd.Points) >= 2 {
				primitives = append(primitives, Primitive{
					Type:         PrimitiveLine,
					ColorRole:    cmd.Role,
					Path:         convertPoints(cmd.Points),
					StrokeWidth:  cmd.StrokeWidth,
					Transparency: cmd.Transparency,
				})
			}

		case "DOT": // Single point/dot (PD with no coordinates)
			if len(cmd.Points) >= 1 {
				primitives = append(primitives, Primitive{
					Type:         PrimitivePoint,
					ColorRole:    cmd.Role,
					Path:         convertPoints(cmd.Points), // Single point in Path
					StrokeWidth:  cmd.StrokeWidth,
					Transparency: cmd.Transparency,
				})
			}

		case "CI": // Circle
			if cmd.Center != nil && len(cmd.Points) > 0 {
				primitives = append(primitives, Primitive{
					Type:         PrimitiveCircle,
					ColorRole:    cmd.Role,
					Center:       &Point{X: cmd.Center.X, Y: cmd.Center.Y},
					Radius:       cmd.Points[0].X, // Radius stored as X coordinate
					StrokeWidth:  cmd.StrokeWidth,
					Transparency: cmd.Transparency,
				})
			}

		case "POLYGON_FILLED": // Filled polygon
			if len(cmd.Rings) > 0 {
				primitives = append(primitives, Primitive{
					Type:         PrimitiveFill,
					ColorRole:    cmd.Role,
					Rings:        convertRings(cmd.Rings),
					StrokeWidth:  cmd.StrokeWidth,
					Transparency: cmd.Transparency,
				})
			}

		case "SC": // Symbol call (nested symbol in linestyles)
			if cmd.SymbolCall != nil {
				primitives = append(primitives, Primitive{
					Type:              PrimitiveSymbolCall,
					ColorRole:         cmd.Role,
					SymbolName:        cmd.SymbolCall.SymbolName,
					SymbolPosition:    Point{X: cmd.SymbolCall.CallPosition.X, Y: cmd.SymbolCall.CallPosition.Y},
					SymbolOrientation: cmd.SymbolCall.Orientation,
					SymbolScale:       cmd.SymbolCall.Scale,
				})
			}

			// Ignore state commands (PU, SP*, SW*, etc.) - already processed
		}
	}

	return primitives
}

// convertPoints converts internal points to public API points
func convertPoints(points []Point) []Point {
	result := make([]Point, len(points))
	for i, pt := range points {
		result[i] = Point{X: pt.X, Y: pt.Y}
	}
	return result
}

// convertRings converts internal polygon rings to public API rings
func convertRings(rings [][]Point) [][]Point {
	result := make([][]Point, len(rings))
	for i, ring := range rings {
		result[i] = convertPoints(ring)
	}
	return result
}

// convertSymbol converts internal Symbol to public API type
// Parses HPGL command stream into high-level vector primitives
func convertSymbol(internal *Symbol) *Symbol {
	if internal == nil {
		return nil
	}

	symbol := &Symbol{
		ID:          internal.ID,
		Description: internal.Description,
		ColorRef:    internal.ColorRef,
		BoundingBox: BoundingBox{
			MinX: internal.BoundingBox.MinX,
			MinY: internal.BoundingBox.MinY,
			MaxX: internal.BoundingBox.MaxX,
			MaxY: internal.BoundingBox.MaxY,
		},
		PivotPoint: Point{
			X: internal.PivotPoint.X,
			Y: internal.PivotPoint.Y,
		},
		Primitives: parseHPGLtoPrimitives(internal.VectorCommands),
	}

	return symbol
}

// convertPattern converts internal Pattern to public API type
func convertPattern(internal *Pattern) *Pattern {
	if internal == nil {
		return nil
	}

	// Parse HPGL commands to primitives if not already done
	if len(internal.Primitives) == 0 && len(internal.VectorCommands) > 0 {
		internal.Primitives = parseHPGLtoPrimitives(internal.VectorCommands)
	}

	// Set bounding box from tile dimensions if not set
	if internal.TileWidth > 0 && internal.TileHeight > 0 {
		internal.BoundingBox = BoundingBox{
			MinX: 0,
			MinY: 0,
			MaxX: float64(internal.TileWidth),
			MaxY: float64(internal.TileHeight),
		}
	}

	// Return the same pointer since internal and public types are now unified
	return internal
}

// convertLineStyle converts internal Linestyle to public API type
// S-52 Section 11.7: Linestyle definitions include pivot and bbox from LIND
func convertLineStyle(internal *Linestyle) *Linestyle {
	if internal == nil {
		return nil
	}

	// Convert vector commands to primitives if not already done
	if len(internal.Primitives) == 0 && len(internal.VectorCommands) > 0 {
		internal.Primitives = parseHPGLtoPrimitives(internal.VectorCommands)
	}

	// Set bounding box from LIND fields if not set
	if internal.BBoxWidth > 0 && internal.BBoxHeight > 0 {
		internal.BoundingBox = BoundingBox{
			MinX: float64(internal.BBoxX),
			MinY: float64(internal.BBoxY),
			MaxX: float64(internal.BBoxX + internal.BBoxWidth),
			MaxY: float64(internal.BBoxY + internal.BBoxHeight),
		}
	}

	// Set pivot point from LIND fields
	internal.Pivot = Point{
		X: float64(internal.PivotX),
		Y: float64(internal.PivotY),
	}

	return internal
}

// ParseSCRF parses Symbol/Pattern Color Reference Format string
//
// S-52 PresLib e4.0.0, Part I, Section 8.2.3: Symbol Color Reference Format
//
// SCRF maps color roles (single characters) to S-52 color tokens (5 characters).
// Format: Each entry is 1-char role + 5-char color token
//
// Examples:
//
//	"CCHBLK" = role 'C' uses color token "CHBLK"
//	"JCHMGF" = role 'J' uses color token "CHMGF"
//	"CCHBLKJCHMGF" = role 'C' uses "CHBLK", role 'J' uses "CHMGF"
//
// Returns a map from role character to color token string.
func ParseSCRF(scrf string) map[rune]string {
	result := make(map[rune]string)

	// Parse SCRF: each entry is 1 char role + 5 char color token
	for i := 0; i+5 < len(scrf); i += 6 {
		role := rune(scrf[i])
		colorToken := scrf[i+1 : i+6]
		result[role] = colorToken
	}

	return result
}
