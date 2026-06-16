package s52

import "time"

// Core S-52 data structure types

// Point represents a 2D coordinate (in DAI units: 0.01mm)
type Point struct {
	X, Y float64
}

// Rectangle represents a rectangle
type Rectangle struct {
	X      float64
	Y      float64
	Width  float64
	Height float64
}

// ParsedColors represents the resolved color roles from SCRF parsing
type ParsedColors struct {
	Fill   string          `json:"fill"`   // Primary fill color token (role A)
	Stroke string          `json:"stroke"` // Primary stroke color token (role B)
	Roles  map[rune]string `json:"roles"`  // All role assignments (A, B, C, etc.)
}

// Symbol represents an S-52 point symbol
type Symbol struct {
	ID             string            `json:"id"`
	ReferenceID    string            `json:"reference_id"`
	Description    string            `json:"description"`
	ColorRef       string            `json:"color_ref"`
	Colors         ParsedColors      `json:"colors"`
	VectorCommands []VectorCommand   `json:"vector_commands"`
	Type           string            `json:"type"`
	BoundingBox    BoundingBox       `json:"bounding_box"`
	PivotPoint     Point             `json:"pivot_point"`
	Metadata       map[string]string `json:"metadata"`
	Primitives     []Primitive       `json:"-"` // Parsed vector primitives (not in DAI)

	// Internal parser state (unexported)
	polygonMode bool        `json:"-"`
	hpglParser  interface{} `json:"-"`
}

// Pattern represents an S-52 area fill pattern
type Pattern struct {
	ID             string            `json:"id"`
	ReferenceID    string            `json:"reference_id"`
	Description    string            `json:"description"`
	ColorRef       string            `json:"color_ref"`
	Colors         ParsedColors      `json:"colors"`
	VectorCommands []VectorCommand   `json:"vector_commands"`
	Type           string            `json:"type"`
	PatternType    string            `json:"pattern_type"`
	TileWidth      int               `json:"tile_width"`
	TileHeight     int               `json:"tile_height"`
	SpacingX       int               `json:"spacing_x"`
	SpacingY       int               `json:"spacing_y"`
	BBoxX          int               `json:"bbox_x"`
	BBoxY          int               `json:"bbox_y"`
	PivotX         int               `json:"pivot_x"`
	PivotY         int               `json:"pivot_y"`
	Metadata       map[string]string `json:"metadata"`
	BoundingBox    BoundingBox       `json:"-"` // Computed
	Primitives     []Primitive       `json:"-"` // Parsed vector primitives (not in DAI)

	// Internal parser state (unexported)
	hpglParser *daiVectorParser `json:"-"`
}

// PatternTileInfo contains calculated pattern tiling information
//
// S-52 PresLib e4.0.0, Part I, Section 8.5.4: Pattern Spacing
type PatternTileInfo struct {
	// Spacing between tiles (mm)
	SpacingX float64
	SpacingY float64

	// Pattern type
	IsLinear bool

	// Bounding box dimensions (mm)
	TileWidth  float64
	TileHeight float64
}

// Linestyle represents a complete linestyle definition from DAI file
type Linestyle struct {
	ID             string            `json:"id"`
	ReferenceID    string            `json:"reference_id"`
	Description    string            `json:"description"`
	ColorRef       string            `json:"color_ref"`
	Colors         ParsedColors      `json:"colors"`
	VectorCommands []VectorCommand   `json:"vector_commands"`
	PivotX         int               `json:"pivot_x"`
	PivotY         int               `json:"pivot_y"`
	BBoxWidth      int               `json:"bbox_width"`
	BBoxHeight     int               `json:"bbox_height"`
	BBoxX          int               `json:"bbox_x"`
	BBoxY          int               `json:"bbox_y"`
	Metadata       map[string]string `json:"metadata"`
	BoundingBox    BoundingBox       `json:"-"` // Computed
	Pivot          Point             `json:"-"` // Computed
	Primitives     []Primitive       `json:"-"` // Parsed vector primitives (not in DAI)

	// Internal parser state (unexported)
	hpglParser interface{} `json:"-"`
}

// PrimitiveType represents the type of primitive
type PrimitiveType string

const (
	PrimitiveLine       PrimitiveType = "LINE"
	PrimitiveCircle     PrimitiveType = "CIRCLE"
	PrimitiveArc        PrimitiveType = "ARC"
	PrimitivePolygon    PrimitiveType = "POLYGON"
	PrimitivePoint      PrimitiveType = "POINT"
	PrimitiveFill       PrimitiveType = "FILL"
	PrimitiveSymbolCall PrimitiveType = "SYMBOL"
)

// Primitive represents a linestyle drawing primitive
type Primitive struct {
	Type              PrimitiveType
	ColorRole         rune // Color role from SCRF
	Symbol            *Symbol
	Bounds            BoundingBox
	Pivot             Point
	Dash              []int
	Path              []Point   // For lines
	Rings             [][]Point // For polygons
	Center            *Point    // For circles
	Radius            float64   // For circles
	StrokeWidth       int       // Line width
	Transparency      int       // Transparency: 0=opaque, 1=25%, 2=50%, 3=75%
	SymbolName        string    // For symbol calls
	SymbolPosition    Point     // For symbol calls
	SymbolOrientation int       // For symbol calls: 0=upright, 1=rotate to pen, 2=rotate 90° to edge
	SymbolScale       float64   // For symbol calls
}

// SymbolCall represents a symbol call command (SC) with positioning and orientation
type SymbolCall struct {
	SymbolName   string  `json:"symbol_name"`
	Orientation  int     `json:"orientation"`
	Scale        float64 `json:"scale"`
	CallPosition Point   `json:"call_position"`
}

// VectorCommand represents a single DAI vector drawing command
type VectorCommand struct {
	Type         string    `json:"type"`
	Role         rune      `json:"role"`
	StrokeWidth  int       `json:"stroke_width"`
	StrokeType   int       `json:"stroke_type"`
	Transparency int       `json:"transparency"`
	Points       []Point   `json:"points"`
	Rings        [][]Point `json:"rings"`
	Center       *Point    `json:"center"`
	RawCommand   string    `json:"raw_command"`

	// Phase 1 HPGL extensions
	SweepAngle     float64     `json:"sweep_angle,omitempty"`
	StartAngle     float64     `json:"start_angle,omitempty"`
	Radius         float64     `json:"radius,omitempty"`
	ChordTolerance float64     `json:"chord_tolerance,omitempty"`
	SymbolCall     *SymbolCall `json:"symbol_call,omitempty"`
	ClipWindow     *Rectangle  `json:"clip_window,omitempty"`
	Rotation       float64     `json:"rotation,omitempty"`
	Rectangle      *Rectangle  `json:"rectangle,omitempty"`
	Filled         bool        `json:"filled,omitempty"`
}

// BoundingBox defines a rectangular extent
type BoundingBox struct {
	MinX, MinY, MaxX, MaxY float64
}

// Width returns the width of the bounding box
func (b BoundingBox) Width() float64 {
	return b.MaxX - b.MinX
}

// Height returns the height of the bounding box
func (b BoundingBox) Height() float64 {
	return b.MaxY - b.MinY
}

// LookupEntry represents an entry in the S-52 lookup table
type LookupEntry struct {
	ObjectClass     string
	Geometry        string
	Attributes      []AttributeCondition
	Instruction     string
	DisplayPriority int
	DisplayCategory string
	RadarPriority   string
	ViewingGroup    string
	Comment         string
}

// AttributeCondition represents an attribute matching condition
type AttributeCondition struct {
	Attribute string `json:"attribute"`
	Value     string `json:"value"`
}

// RGB represents RGB color values
type RGB struct {
	R uint8 `json:"r"`
	G uint8 `json:"g"`
	B uint8 `json:"b"`
}

// ColorDefinition represents a CCIE color entry with CIE xyY coordinates
type ColorDefinition struct {
	Token         string  `json:"token"`
	CIE_X         float64 `json:"cie_x"`
	CIE_Y         float64 `json:"cie_y"`
	CIE_Luminance float64 `json:"cie_luminance"`
	Name          string  `json:"name"`
	RGB           RGB     `json:"rgb"`
}

// LookupTable represents a LUPT record for conditional symbol display
type LookupTable struct {
	ID              string               `json:"id"`
	ObjectClass     string               `json:"object_class"`
	GeometryType    string               `json:"geometry_type"`
	TableName       string               `json:"table_name"`
	AttributeName   string               `json:"attribute_name"`
	AttributeValue  string               `json:"attribute_value"`
	DisplayPriority int                  `json:"display_priority"`
	RadarOverlay    string               `json:"radar_overlay"`
	DisplayCategory string               `json:"display_category"`
	ViewingGroup    string               `json:"viewing_group"`
	Attributes      []AttributeCondition `json:"attributes"`
	Instructions    []RawInstruction     `json:"instructions"`
}

// RawInstruction represents an INST record from DAI parsing
type RawInstruction struct {
	Command    string            `json:"command"`
	Parameters []string          `json:"parameters"`
	RawCommand string            `json:"raw_command"`
	Metadata   map[string]string `json:"metadata"`
}

// DAIHeader represents the file header information from LBID records
type DAIHeader struct {
	FileID           string    `json:"file_id"`
	Version          string    `json:"version"`
	Edition          string    `json:"edition"`
	Revision         string    `json:"revision"`
	Date             time.Time `json:"date"`
	Title            string    `json:"title"`
	Compliant        bool      `json:"compliant"`
	ValidationErrors []string  `json:"validation_errors"`
}

// ValidationResult represents S-52 compliance validation results
type ValidationResult struct {
	Compliant      bool     `json:"compliant"`
	Version        string   `json:"version"`
	SOLASCompliant bool     `json:"solas_compliant"`
	Errors         []string `json:"errors"`
	Warnings       []string `json:"warnings"`
}

// InstructionSet contains categorized S-52 instructions
type InstructionSet struct {
	Instructions    []Instruction // All instructions combined
	AreaFill        string
	Lines           string
	Symbols         string
	Text            string
	DisplayPriority int
	DisplayCategory int // Display category: DisplayBase(6), DisplayStandard(7), DisplayOther(8)
	RadarPriority   string
}

// HasAreaFill checks if the instruction set contains an AC (area color fill) instruction
func (is *InstructionSet) HasAreaFill() bool {
	if is == nil {
		return false
	}
	for _, instr := range is.Instructions {
		if _, ok := instr.(*ACInstruction); ok {
			return true
		}
	}
	return false
}

// HasAreaPattern checks if the instruction set contains an AP (area pattern) instruction
func (is *InstructionSet) HasAreaPattern() bool {
	if is == nil {
		return false
	}
	for _, instr := range is.Instructions {
		if _, ok := instr.(*APInstruction); ok {
			return true
		}
	}
	return false
}

// HasComplexLine checks if the instruction set contains an LC (complex line) instruction
func (is *InstructionSet) HasComplexLine() bool {
	if is == nil {
		return false
	}
	for _, instr := range is.Instructions {
		if _, ok := instr.(*LCInstruction); ok {
			return true
		}
	}
	return false
}

// HasSimpleLine checks if the instruction set contains an LS (simple line) instruction
func (is *InstructionSet) HasSimpleLine() bool {
	if is == nil {
		return false
	}
	for _, instr := range is.Instructions {
		if _, ok := instr.(*LSInstruction); ok {
			return true
		}
	}
	return false
}

// HasSymbol checks if the instruction set contains a SY (symbol) instruction
func (is *InstructionSet) HasSymbol() bool {
	if is == nil {
		return false
	}
	for _, instr := range is.Instructions {
		if _, ok := instr.(*SYInstruction); ok {
			return true
		}
	}
	return false
}

// HasText checks if the instruction set contains a TX (text) instruction
func (is *InstructionSet) HasText() bool {
	if is == nil {
		return false
	}
	for _, instr := range is.Instructions {
		if _, ok := instr.(*TXInstruction); ok {
			return true
		}
	}
	return false
}

// HasConditionalSymbology checks if the instruction set contains a CS (conditional symbology) instruction
func (is *InstructionSet) HasConditionalSymbology() bool {
	if is == nil {
		return false
	}
	for _, instr := range is.Instructions {
		if _, ok := instr.(*CSInstruction); ok {
			return true
		}
	}
	return false
}

// GetSymbols returns all SY (symbol) instructions
func (is *InstructionSet) GetSymbols() []*SYInstruction {
	if is == nil {
		return nil
	}
	var symbols []*SYInstruction
	for _, instr := range is.Instructions {
		if sy, ok := instr.(*SYInstruction); ok {
			symbols = append(symbols, sy)
		}
	}
	return symbols
}

// GetAreaPatterns returns all AP (area pattern) instructions
func (is *InstructionSet) GetAreaPatterns() []*APInstruction {
	if is == nil {
		return nil
	}
	var patterns []*APInstruction
	for _, instr := range is.Instructions {
		if ap, ok := instr.(*APInstruction); ok {
			patterns = append(patterns, ap)
		}
	}
	return patterns
}

// GetAreaColors returns all AC (area color) instructions
func (is *InstructionSet) GetAreaColors() []*ACInstruction {
	if is == nil {
		return nil
	}
	var colors []*ACInstruction
	for _, instr := range is.Instructions {
		if ac, ok := instr.(*ACInstruction); ok {
			colors = append(colors, ac)
		}
	}
	return colors
}

// GetLines returns all line instructions (LC and LS)
func (is *InstructionSet) GetLines() []Instruction {
	if is == nil {
		return nil
	}
	var lines []Instruction
	for _, instr := range is.Instructions {
		switch instr.(type) {
		case *LCInstruction, *LSInstruction:
			lines = append(lines, instr)
		}
	}
	return lines
}

// GetComplexLines returns all LC (complex line) instructions
func (is *InstructionSet) GetComplexLines() []*LCInstruction {
	if is == nil {
		return nil
	}
	var lines []*LCInstruction
	for _, instr := range is.Instructions {
		if lc, ok := instr.(*LCInstruction); ok {
			lines = append(lines, lc)
		}
	}
	return lines
}

// GetSimpleLines returns all LS (simple line) instructions
func (is *InstructionSet) GetSimpleLines() []*LSInstruction {
	if is == nil {
		return nil
	}
	var lines []*LSInstruction
	for _, instr := range is.Instructions {
		if ls, ok := instr.(*LSInstruction); ok {
			lines = append(lines, ls)
		}
	}
	return lines
}

// GetTextInstructions returns all TX (text) instructions
func (is *InstructionSet) GetTextInstructions() []*TXInstruction {
	if is == nil {
		return nil
	}
	var texts []*TXInstruction
	for _, instr := range is.Instructions {
		if tx, ok := instr.(*TXInstruction); ok {
			texts = append(texts, tx)
		}
	}
	return texts
}

// GetSectors returns all SECTOR instructions (from light sectors)
func (is *InstructionSet) GetSectors() []*SectorInstruction {
	if is == nil {
		return nil
	}
	var sectors []*SectorInstruction
	for _, instr := range is.Instructions {
		if sec, ok := instr.(*SectorInstruction); ok {
			sectors = append(sectors, sec)
		}
	}
	return sectors
}

// DepthUnit represents the unit of measurement for depth/sounding values
type DepthUnit int

const (
	// DepthUnitMeters represents depth in meters (S-57 standard)
	DepthUnitMeters DepthUnit = iota
	// DepthUnitFeet represents depth in feet (common in North America)
	DepthUnitFeet
	// DepthUnitFathoms represents depth in fathoms (1 fathom = 6 feet)
	DepthUnitFathoms
)

// String returns the string representation of the depth unit
func (d DepthUnit) String() string {
	switch d {
	case DepthUnitMeters:
		return "meters"
	case DepthUnitFeet:
		return "feet"
	case DepthUnitFathoms:
		return "fathoms"
	default:
		return "unknown"
	}
}

// Abbreviation returns the abbreviated form for display (e.g., "ft", "m", "fms")
func (d DepthUnit) Abbreviation() string {
	switch d {
	case DepthUnitMeters:
		return "m"
	case DepthUnitFeet:
		return "ft"
	case DepthUnitFathoms:
		return "fms"
	default:
		return ""
	}
}

// LibraryStats contains statistics about a loaded library

// TextInstruction represents a parsed TX command from S-52
type TextInstruction struct {
	Text                 string
	Font                 FontSpec
	HJust                int
	VJust                int
	Space                int
	XOffset              int
	YOffset              int
	Color                string
	Display              int
	IsAttributeReference bool

	// TE (formatted text) only: the C-printf format string (e.g. "clr op %4.1lf")
	// and the attribute acronyms substituted into it, in order. Empty for TX.
	// Preserved so the portrayal layer can run S-52 §8.3.3.3 substitution instead
	// of discarding the format. See ParseTextInstruction / parseTE.
	Format      string
	FormatAttrs []string
}

// FontSpec represents font specifications
type FontSpec struct {
	Style    int
	Weight   int
	Slant    int
	BodySize int
}

// IsSerif returns true if the font is serif
func (f FontSpec) IsSerif() bool {
	return f.Style == 1 // 1 = serif, 0 = sans-serif
}

// IsBold returns true if the font is bold
func (f FontSpec) IsBold() bool {
	return f.Weight >= 6 // Weight >= 6 is bold
}

// IsItalic returns true if the font is italic
func (f FontSpec) IsItalic() bool {
	return f.Slant == 2 // Slant 2 = italic, 1 = upright
}

// BodySizeMM returns the font body size in millimeters
func (f FontSpec) BodySizeMM() float64 {
	return float64(f.BodySize) * 0.351 // BodySize is in points (1pt = 0.351mm)
}
