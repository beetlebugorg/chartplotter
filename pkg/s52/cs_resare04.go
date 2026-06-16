package s52

// RESARE04 represents the Restricted Area symbology procedure.
// Symbolizes restricted areas based on CATREA and RESTRN attributes.
//
// S-52 Section 13.2.10: RESARE04 (pages 56-69)
//
// The procedure follows a cascading decision tree where the FIRST matching
// restriction type is used, with subscripts added based on additional restrictions.
type RESARE04 struct {
	ctx          *CSContext
	lib          *Library
	restrnValues []int // RESTRN attribute values (can be multiple)
	catreaValues []int // CATREA attribute values (can be multiple)
}

// NewRESARE04 creates a new RESARE04 procedure instance by parsing the execution context.
func NewRESARE04(csctx *CSContext, lib *Library) *RESARE04 {
	return &RESARE04{
		ctx:          csctx,
		lib:          lib,
		restrnValues: getIntList(csctx.Attributes["RESTRN"]),
		catreaValues: getIntList(csctx.Attributes["CATREA"]),
	}
}

// Execute runs the RESARE04 symbology procedure and returns rendering instructions.
func (r *RESARE04) Execute() ([]Instruction, error) {
	var instructions []Instruction

	// Select symbol based on priority order (first match wins)
	symbol := r.selectSymbol()
	instructions = append(instructions, &SYInstruction{SymbolID: symbol})

	// Add area boundary
	instructions = append(instructions, r.areaBoundary()...)

	return instructions, nil
}

// selectSymbol determines the appropriate symbol based on priority order.
// Priority: Entry > Anchoring > Fishing > Other > Own ship > Category > Default
func (r *RESARE04) selectSymbol() string {
	// Priority 1: Entry prohibited/restricted (7, 8, 14)
	if hasAnyValue(r.restrnValues, []int{7, 8, 14}) {
		return r.entryRestrictionSymbol()
	}

	// Priority 2: Anchoring prohibited/restricted (1, 2)
	if hasAnyValue(r.restrnValues, []int{1, 2}) {
		return r.anchoringRestrictionSymbol()
	}

	// Priority 3: Fishing/trawling/dragging prohibited/restricted (3, 4, 5, 6, 24)
	if hasAnyValue(r.restrnValues, []int{3, 4, 5, 6, 24}) {
		return r.fishingRestrictionSymbol()
	}

	// Priority 4: Other restrictions (no wake, discharge, speed, etc)
	if hasAnyValue(r.restrnValues, []int{13, 16, 17, 23, 25, 26, 27}) {
		return r.otherRestrictionSymbol()
	}

	// Priority 5: Own ship restrictions (dredging, diving, development, etc)
	if hasAnyValue(r.restrnValues, []int{9, 10, 11, 12, 15, 18, 19, 20, 21, 22}) {
		return r.ownShipRestrictionSymbol()
	}

	// No RESTRN value - use CATREA only or default
	if len(r.catreaValues) > 0 {
		return r.categorySymbol()
	}

	// Default
	return "INFARE51"
}

// entryRestrictionSymbol determines which ENTRES symbol to use (pages 59-60, Continuation A).
func (r *RESARE04) entryRestrictionSymbol() string {
	if r.hasAdditionalRestrictions() {
		return "ENTRES61" // Entry restricted with additional restrictions (!)
	}
	if r.hasNatureEcologyRestrictions() {
		return "ENTRES71" // Entry restricted with nature/ecology info (i)
	}
	return "ENTRES51"
}

// anchoringRestrictionSymbol determines which ACHRES symbol to use (pages 61-62, Continuation B).
func (r *RESARE04) anchoringRestrictionSymbol() string {
	// Check for additional restrictions (excluding anchoring itself: 1,2)
	if hasAnyValue(r.restrnValues, []int{3, 4, 5, 6, 13, 16, 17, 23, 24, 25, 26, 27}) ||
		hasAnyValue(r.catreaValues, []int{1, 8, 9, 12, 14, 18, 19, 21, 24, 25, 26}) {
		return "ACHRES61" // Anchoring restricted with additional restrictions (!)
	}
	if r.hasNatureEcologyRestrictions() {
		return "ACHRES71" // Anchoring restricted with nature/ecology info (i)
	}
	return "ACHRES51"
}

// fishingRestrictionSymbol determines which FSHRES symbol to use (pages 63-64, Continuation C).
func (r *RESARE04) fishingRestrictionSymbol() string {
	// Check for additional restrictions (excluding fishing: 3,4,5,6,24)
	if hasAnyValue(r.restrnValues, []int{13, 16, 17, 23, 25, 26, 27}) ||
		hasAnyValue(r.catreaValues, []int{1, 8, 9, 12, 14, 18, 19, 21, 24, 25, 26}) {
		return "FSHRES61" // Fishing restricted with additional restrictions (!)
	}
	if r.hasNatureEcologyRestrictions() {
		return "FSHRES71" // Fishing restricted with nature/ecology info (i)
	}
	return "FSHRES51"
}

// otherRestrictionSymbol determines which CTYARE symbol to use (pages 65-66, Continuation D).
// For no wake, discharge, speed, etc.
func (r *RESARE04) otherRestrictionSymbol() string {
	if r.hasNatureEcologyRestrictions() {
		return "CTYARE71" // Other restrictions with nature/ecology info (i)
	}
	return "CTYARE51"
}

// ownShipRestrictionSymbol determines which CTYARE symbol to use (pages 65-66, Continuation D).
// For dredging, diving, development, etc.
func (r *RESARE04) ownShipRestrictionSymbol() string {
	if hasAnyValue(r.catreaValues, []int{4, 5, 6, 7, 10, 20, 22, 23}) {
		return "CTYARE71" // Own ship restrictions with nature/ecology info (i)
	}
	return "CTYARE51"
}

// categorySymbol determines symbol based on CATREA only when no RESTRN (page 68, Continuation E).
func (r *RESARE04) categorySymbol() string {
	// Check for inshore traffic zones and similar
	if hasAnyValue(r.catreaValues, []int{1, 8, 9, 12, 14, 18, 19, 21, 24, 25, 26}) {
		return "CTYARE51"
	}
	// Check for nature reserves and sanctuaries
	if hasAnyValue(r.catreaValues, []int{4, 5, 6, 7, 10, 20, 22, 23}) {
		return "CTYARE71"
	}
	return "INFARE51"
}

// hasAdditionalRestrictions returns true if there are additional general restrictions.
func (r *RESARE04) hasAdditionalRestrictions() bool {
	return hasAnyValue(r.restrnValues, []int{1, 2, 3, 4, 5, 6, 13, 16, 17, 23, 24, 25, 26, 27}) ||
		hasAnyValue(r.catreaValues, []int{1, 8, 9, 12, 14, 18, 19, 21, 24, 25, 26})
}

// hasNatureEcologyRestrictions returns true if there are nature/ecology restrictions.
func (r *RESARE04) hasNatureEcologyRestrictions() bool {
	return hasAnyValue(r.restrnValues, []int{9, 10, 11, 12, 15, 18, 19, 20, 21, 22}) ||
		hasAnyValue(r.catreaValues, []int{4, 5, 6, 7, 10, 20, 22, 23})
}

// areaBoundary returns the area boundary instructions.
// LC(pattern) if symbolized boundaries enabled, else LS(DASH,2,CHMGD).
func (r *RESARE04) areaBoundary() []Instruction {
	if r.ctx.Mariner.SymbolizedBoundaries {
		return []Instruction{
			&LCInstruction{LineStyleID: "CTYARE51"},
		}
	}
	return []Instruction{
		&LSInstruction{Style: "DASH", Width: 2, Color: "CHMGD"},
	}
}

// hasAnyValue checks if any value from checkValues exists in values.
func hasAnyValue(values []int, checkValues []int) bool {
	for _, v := range values {
		for _, cv := range checkValues {
			if v == cv {
				return true
			}
		}
	}
	return false
}

// getIntList extracts a list of integers from an attribute value.
// Handles: int, []int, string (comma-separated), []interface{}
func getIntList(val interface{}) []int {
	if val == nil {
		return []int{}
	}

	switch v := val.(type) {
	case int:
		return []int{v}
	case []int:
		return v
	case float64:
		return []int{int(v)}
	case string:
		// Parse comma-separated string
		parts := splitAndTrim(v, ",")
		result := make([]int, 0, len(parts))
		for _, p := range parts {
			if i := stringToInt(p); i > 0 {
				result = append(result, i)
			}
		}
		return result
	case []interface{}:
		result := make([]int, 0, len(v))
		for _, item := range v {
			if i := getIntValue(item); i > 0 {
				result = append(result, i)
			}
		}
		return result
	default:
		return []int{}
	}
}
