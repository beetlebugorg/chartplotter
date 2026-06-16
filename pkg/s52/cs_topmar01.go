package s52

// TOPMAR01 represents the Top Mark symbology procedure.
// Symbolizes top marks on buoys, beacons, and other navigation aids.
//
// S-52 Section 13.2.19: TOPMAR01 (pages 100-104)
//
// When spatial context is provided with adjacent objects, uses co-located object info to
// distinguish floating vs rigid platforms. When spatial is nil, uses attribute heuristics.
type TOPMAR01 struct {
	ctx          *CSContext
	lib          *Library
	topshp       int  // Top mark shape
	topshpExists bool // Whether TOPSHP attribute is present
	isFloating   bool // Whether platform is floating (vs rigid)
}

// NewTOPMAR01 creates a new TOPMAR01 procedure instance by parsing the execution context.
func NewTOPMAR01(csctx *CSContext, lib *Library) *TOPMAR01 {
	topshp := csctx.GetInt("TOPSHP", 0)
	topshpExists := csctx.Has("TOPSHP")

	return &TOPMAR01{
		ctx:          csctx,
		lib:          lib,
		topshp:       topshp,
		topshpExists: topshpExists,
		isFloating:   determinePlatformType(csctx),
	}
}

// Execute runs the TOPMAR01 symbology procedure and returns rendering instructions.
func (t *TOPMAR01) Execute() ([]Instruction, error) {
	// If TOPSHP not given, return question mark
	if !t.topshpExists || t.topshp == 0 {
		return []Instruction{
			&SYInstruction{SymbolID: "QUESMRK1"},
		}, nil
	}

	symbolID := t.selectSymbol()
	return []Instruction{
		&SYInstruction{SymbolID: symbolID},
	}, nil
}

// selectSymbol chooses the appropriate symbol based on TOPSHP value and platform type.
func (t *TOPMAR01) selectSymbol() string {
	if t.isFloating {
		return topmarFloatingSymbol(t.topshp)
	}
	return topmarRigidSymbol(t.topshp)
}

// determinePlatformType checks if the platform is floating or rigid.
// Uses spatial.AdjacentObjects when available, otherwise uses attribute heuristics.
// Default to floating (most common case).
func determinePlatformType(csctx *CSContext) bool {
	// TODO: Use csctx.HasAdjacentObjects() to check co-located objects
	// Floating platforms: LITFLT, LITVES, BOY*, or MORFAC with CATMOR=7
	// Rigid platforms: BCN*, BRIDGE, BUISGL, DAYMAR, LNDMRK, MORFAC (not CATMOR=7),
	//                  OFSPLF, PILPNT, SLCONS, CRANES, FLODOC, FORSTC, FSHFAC,
	//                  HULKES, PONTON, OBSTRN, PYLONS, SILTNK, WRECKS

	// Simplified heuristic: check for beacon attributes
	if csctx.Has("BCNSHP") {
		return false // Rigid
	}

	return true // Default to floating
}

// Topmark symbol lookup maps (package level for efficiency)
var (
	// Per S-52 TOPMAR01 table, pages 102-103
	topmarFloatingMap = map[int]string{
		1:  "TOPMAR02", // Cone, point up
		2:  "TOPMAR04", // Cone, point down
		3:  "TOPMAR10", // Sphere
		4:  "TOPMAR12", // 2 spheres
		5:  "TOPMAR13", // Cylinder (can)
		6:  "TOPMAR14", // Board
		7:  "TOPMAR65", // X-shape (St Andrews cross)
		8:  "TOPMAR17", // Upright cross
		9:  "TOPMAR16", // Cube, point up
		10: "TOPMAR08", // 2 cones, point to point
		11: "TOPMAR07", // 2 cones, base to base
		12: "TOPMAR14", // Rhombus (diamond)
		13: "TOPMAR05", // 2 cones, points up
		14: "TOPMAR06", // 2 cones, points down
		15: "TMARDEF2", // Other shape
		16: "TMARDEF2", // Other shape
		17: "TMARDEF2", // Other shape
		18: "TOPMAR10", // Sphere
		19: "TOPMAR13", // Cylinder
		20: "TOPMAR14", // Board
		21: "TOPMAR13", // Cylinder
		22: "TOPMAR14", // Board
		23: "TOPMAR14", // Board
		24: "TOPMAR02", // Cone, point up
		25: "TOPMAR04", // Cone, point down
		26: "TOPMAR10", // Sphere
		27: "TOPMAR17", // Upright cross
		28: "TOPMAR18", // T-shape
		29: "TOPMAR02", // Cone, point up
		30: "TOPMAR17", // Upright cross
		31: "TOPMAR14", // Rhombus
		32: "TOPMAR08", // 2 cones, point to point
		33: "TMARDEF2", // Other shape
	}

	topmarRigidMap = map[int]string{
		1:  "TOPMAR22", // Cone, point up
		2:  "TOPMAR24", // Cone, point down
		3:  "TOPMAR30", // Sphere
		4:  "TOPMAR32", // 2 spheres
		5:  "TOPMAR33", // Cylinder (can)
		6:  "TOPMAR34", // Board
		7:  "TOPMAR85", // X-shape (St Andrews cross)
		8:  "TOPMAR86", // Upright cross
		9:  "TOPMAR36", // Cube, point up
		10: "TOPMAR28", // 2 cones, point to point
		11: "TOPMAR27", // 2 cones, base to base
		12: "TOPMAR14", // Rhombus (diamond)
		13: "TOPMAR25", // 2 cones, points up
		14: "TOPMAR26", // 2 cones, points down
		15: "TOPMAR88", // Other shape
		16: "TOPMAR87", // Board
		17: "TMARDEF1", // Default
		18: "TOPMAR30", // Sphere
		19: "TOPMAR33", // Cylinder
		20: "TOPMAR34", // Board
		21: "TOPMAR33", // Cylinder
		22: "TOPMAR34", // Board
		23: "TOPMAR34", // Board
		24: "TOPMAR22", // Cone, point up
		25: "TOPMAR24", // Cone, point down
		26: "TOPMAR30", // Sphere
		27: "TOPMAR86", // Upright cross
		28: "TOPMAR89", // T-shape
		29: "TOPMAR22", // Cone, point up
		30: "TOPMAR86", // Upright cross
		31: "TOPMAR14", // Rhombus
		32: "TOPMAR28", // 2 cones, point to point
		33: "TMARDEF1", // Default
	}
)

// topmarFloatingSymbol returns the symbol ID for floating platforms (buoys).
// Per S-52 TOPMAR01 table, pages 102-103.
func topmarFloatingSymbol(topshp int) string {
	if symbol, ok := topmarFloatingMap[topshp]; ok {
		return symbol
	}
	return "TMARDEF2" // Default for unknown shapes
}

// topmarRigidSymbol returns the symbol ID for rigid platforms (beacons).
// Per S-52 TOPMAR01 table, pages 102-103.
func topmarRigidSymbol(topshp int) string {
	if symbol, ok := topmarRigidMap[topshp]; ok {
		return symbol
	}
	return "TMARDEF1" // Default for unknown shapes
}
