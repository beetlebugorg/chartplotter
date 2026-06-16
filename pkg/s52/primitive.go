package s52

// RenderPrimitiveType defines the type of scene graph rendering primitive.
// This is different from PrimitiveType (used for DAI vector commands).
type RenderPrimitiveType int

const (
	// RenderPrimitiveAreaFill represents a filled polygon
	RenderPrimitiveAreaFill RenderPrimitiveType = iota
	// RenderPrimitiveLineStroke represents a stroked path
	RenderPrimitiveLineStroke
	// RenderPrimitiveSymbol represents a symbol instance
	RenderPrimitiveSymbol
	// RenderPrimitiveText represents a text label
	RenderPrimitiveText
	// RenderPrimitivePattern represents a tiled pattern fill
	RenderPrimitivePattern
)

// RenderPrimitive represents a backend-agnostic drawing command.
// This is the fundamental building block of the scene graph rendering system.
// Each primitive contains all the data needed to render it without any
// S-52 lookup or computation.
type RenderPrimitive struct {
	// Type of primitive (area, line, symbol, text, pattern)
	Type RenderPrimitiveType

	// Display priority (0-9, from S-52 spec)
	// Determines rendering order - lower numbers render first (bottom)
	// Inherits from feature by default, but can be overridden
	Priority DisplayPriority

	// Geometry data (coordinates in surface units, typically mm)
	Geometry []Point   // For areas and lines
	Rings    [][]Point // For complex polygons with holes

	// Style information (resolved from S-52 lookup)
	FillColor   *Color  // For area fills
	StrokeColor *Color  // For line strokes
	StrokeWidth float64 // Line width in mm

	// Symbol data
	Symbol   *Symbol // Symbol definition
	Rotation float64 // Rotation in radians
	Scale    float64 // Scale factor

	// Pattern data
	Pattern *Pattern // Pattern definition

	// Text data
	Text      string     // Text content
	TextStyle *TextStyle // Text rendering style
	Location  Point      // Text anchor point

	// Line style
	LineStyle        *LineStyleDef // Simple line style (solid, dashed, etc.)
	ComplexLineStyle *Linestyle    // Complex line style with symbols and patterns (LC instruction)
}

// LineStyleDef defines line rendering properties
type LineStyleDef struct {
	Style       string    // "SOLID", "DASHED", "DOTTED"
	DashPattern []float64 // For dashed lines
	Cap         int       // Line cap style (0=butt, 1=round, 2=square)
	Join        int       // Line join style (0=miter, 1=round, 2=bevel)
}

// TextStyle defines text rendering properties for primitives
type TextStyle struct {
	FontFamily   string
	FontSize     float64 // in mm
	Bold         bool
	Italic       bool
	HAlign       int     // 0=left, 1=center, 2=right
	VAlign       int     // 0=top, 1=middle, 2=bottom
	CharSpacing  float64 // em units
	OutlineColor *Color  // Optional outline
	OutlineWidth float64 // Outline width in mm
}

// IsScalable returns true if this primitive can be smoothly scaled by the client.
// Scalable primitives (area fills, lines, patterns) go in the base layer.
// Fixed-size primitives (symbols, text) go in the overlay layer.
func (p *RenderPrimitive) IsScalable() bool {
	switch p.Type {
	case RenderPrimitiveAreaFill:
		// Solid area fills are scalable
		return true

	case RenderPrimitiveLineStroke:
		// Line strokes are scalable
		return true

	case RenderPrimitivePattern:
		// Patterns are generally scalable (tiled area patterns)
		// Symbol-based patterns would need special handling, but for now
		// we treat all patterns as scalable
		return true

	case RenderPrimitiveSymbol:
		// Symbols must stay constant pixel size
		return false

	case RenderPrimitiveText:
		// Text must stay readable and properly sized
		return false

	default:
		return false
	}
}
