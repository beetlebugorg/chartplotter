package s52

// Sub-procedure stubs (used by main procedures)

// csSEABED01 - Depth Color Determination
// Returns area color based on depth value and mariner contour settings
// Per S-52 spec: DEPVS (very shallow), DEPIT (shallow), DEPMD (medium), DEPDW (deep), NODTA (no data)
//
// Note: NODTA (no data) should primarily be used for UNSARE objects, not DEPARE.
// DEPARE objects with negative DRVAL1 typically represent intertidal zones.
func (l *Library) csSEABED01(drval1, drval2 float64, mariner *MarinerSettings) string {
	// If DRVAL1 not given, assume intertidal (-1)
	if drval1 < 0 {
		drval1 = -1
	}

	// If DRVAL2 not given, use DRVAL1 + 0.01
	if drval2 <= 0 {
		drval2 = drval1 + 0.01
	}
	_ = drval2 // Normalized but not used in current implementation

	// Use DRVAL1 (minimum depth) for color determination
	depth := drval1

	// Determine color based on depth vs contours
	var color string

	// Very shallow: depth < shallow contour
	if depth < mariner.ShallowContour {
		color = "DEPVS" // Very shallow (darkest blue)
	} else if depth < mariner.SafetyContour {
		// Shallow: depth < safety contour
		color = "DEPMS" // Shallow (light blue)
	} else if depth < mariner.DeepContour {
		// Medium: depth < deep contour
		color = "DEPMD" // Medium (lighter blue)
	} else {
		// Deep: depth >= deep contour
		color = "DEPDW" // Deep (white/lightest)
	}

	return color
}

// csSAFCON01 - Safety Contour Labels
// Returns TX() instruction with contour depth label
func (l *Library) csSAFCON01(depth float64, mariner *MarinerSettings) ([]Instruction, error) {
	// Format depth as string (whole number for contours)
	depthStr := formatDepthValue(depth)

	// Create text instruction for contour label
	// Font: 15110 = serif, medium weight, upright, 10pt
	// Position: below and to right (offset 1,1)
	// Color: CHBLK (black)
	// Display: 34 = display base
	return []Instruction{
		&TXInstruction{
			TextInstruction: &TextInstruction{
				Text:    depthStr,
				HJust:   3, // Center
				VJust:   2, // Bottom
				Space:   2, // Standard spacing
				Font:    FontSpec{Style: 1, Weight: 5, Slant: 1, BodySize: 10},
				XOffset: 1,
				YOffset: 1,
				Color:   "CHBLK",
				Display: 34,
			},
		},
	}, nil
}

// csRESCSP02 - Restriction Sub-Procedure
// Returns AP() instructions for restriction patterns (used by DEPARE03 for DRGARE)
func (l *Library) csRESCSP02(attributes map[string]interface{}, mariner *MarinerSettings) ([]Instruction, error) {
	// This is essentially the same logic as RESTRN01, but called as a sub-procedure
	// Get RESTRN attribute
	restrn, ok := attributes["RESTRN"]
	if !ok {
		// No restriction, return empty
		return nil, nil
	}

	// Convert to string
	var restrnStr string
	switch v := restrn.(type) {
	case string:
		restrnStr = v
	case int:
		restrnStr = intToString(v)
	case float64:
		restrnStr = intToString(int(v))
	default:
		return nil, nil
	}

	// Split multiple values
	values := splitAndTrim(restrnStr, ",")
	if len(values) == 0 {
		return nil, nil
	}

	// Determine symbol based on RESTRN value priority
	// S-52 Section 13.2.11 (Figure 27): RESCSP02 uses SYMBOLS, not patterns
	// Priority order (first match wins):
	// 1. Entry prohibited/restricted (7,8,14) -> SY(ENTRES61) or SY(ENTRES71)
	// 2. Other restrictions (9-12,15,18-22) -> SY(ENTRES51)
	// 3. Anchoring/fishing (1-6,13,16,17,23-27) -> Check more specific

	hasEntryRestricted := false  // 7,8,14
	hasOtherRestriction := false // 9-12,15,18-22
	hasAnchorFishing := false    // 1-6,13,16,17,23-27

	for _, val := range values {
		valInt := stringToInt(val)
		if valInt == 0 {
			continue
		}

		// Check for entry restrictions (highest priority)
		if valInt == 7 || valInt == 8 || valInt == 14 {
			hasEntryRestricted = true
			break
		}

		// Check for other restrictions
		if valInt == 9 || valInt == 10 || valInt == 11 || valInt == 12 ||
			valInt == 15 || (valInt >= 18 && valInt <= 22) {
			hasOtherRestriction = true
		}

		// Check for anchor/fishing restrictions
		if (valInt >= 1 && valInt <= 6) || valInt == 13 || valInt == 16 ||
			valInt == 17 || (valInt >= 23 && valInt <= 27) {
			hasAnchorFishing = true
		}
	}

	// Determine which symbol to use (per S-52 Figure 27)
	var symbolID string
	if hasEntryRestricted {
		// Entry restricted or prohibited
		// Use ENTRES61 for restricted, ENTRES71 for prohibited
		// For simplicity, use ENTRES61 (the spec doesn't clearly distinguish)
		symbolID = "ENTRES61"
	} else if hasOtherRestriction {
		symbolID = "ENTRES51"
	} else if hasAnchorFishing {
		// Continue checking for more specific symbols
		// For anchoring restrictions (1,2) - use ENTRES51
		symbolID = "ENTRES51"
	}

	if symbolID == "" {
		return nil, nil
	}

	// Return symbol instruction (no boundary - that's added by calling procedure)
	return []Instruction{
		&SYInstruction{SymbolID: symbolID},
	}, nil
}

// csDEPVAL02 - Depth Value Sub-Procedure
// Returns LEAST_DEPTH and SEABED_DEPTH values for objects with unknown VALSOU
//
// S-52 Section 13.2.3: DEPVAL02 (pages 18-22)
//
// This sub-procedure examines underlying DEPARE/DRGARE objects to establish
// default depth values when VALSOU is missing.
//
// Returns:
//
//	leastDepth - the shallowest DRVAL1 from underlying depth areas (or -1 if unknown)
//	seabedDepth - the DRVAL1 value for seabed depth calculation (or -1 if unknown)
//
// NOTE: This implementation cannot access underlying spatial objects, so it returns
// unknown values. Full implementation would require spatial queries.
func (l *Library) csDEPVAL02(attributes map[string]interface{}, mariner *MarinerSettings) (leastDepth float64, seabedDepth float64) {
	// Initialize to unknown
	leastDepth = -1.0
	seabedDepth = -1.0

	// Get WATLEV and EXPSOU attributes
	watlev := getIntValue(attributes["WATLEV"])
	expsou := getIntValue(attributes["EXPSOU"])

	// NOTE: In a full implementation, we would:
	// 1. Loop through all underlying DEPARE/DRGARE objects
	// 2. Get DRVAL1 from each one
	// 3. Find the minimum DRVAL1 (least depth)
	// 4. If UNSARE found, set leastDepth to unknown and exit
	//
	// Since we don't have access to spatial relationships in this library,
	// we return unknown values. The calling procedure will handle this.

	// Check validity conditions per spec
	// Valid only if WATLEV=3 (always underwater) AND
	// EXPSOU=1 (within range) OR EXPSOU=3 (deeper than range)
	if watlev == 3 && (expsou == 1 || expsou == 3) {
		// Would set seabedDepth = leastDepth here if we had underlying data
		// For now, both remain unknown (-1.0)
	} else if leastDepth > 0 {
		// If conditions not met but we have a least depth,
		// set seabedDepth but clear leastDepth
		seabedDepth = leastDepth
		leastDepth = -1.0
	}

	return leastDepth, seabedDepth
}

// csUDWHAZ05 - Underwater Hazard Sub-Procedure
// Determines if object should be marked with isolated danger symbol
//
// S-52 Section 13.2.20: UDWHAZ05 (pages 105-112)
//
// This sub-procedure checks if an underwater hazard (wreck, obstruction, rock)
// should be marked with the ISODGR01 (isolated danger) symbol.
//
// Algorithm:
// 1. If DEPTH_VALUE > SAFETY_CONTOUR, not an isolated danger
// 2. Check if object lies within deeper water (>= SAFETY_CONTOUR)
// 3. If WATLEV=1 or 2 (above water), not an isolated danger
// 4. If object is in safe deep water, mark with isolated danger symbol
// 5. Optionally check shallow water if mariner setting enabled
//
// Returns:
//
//	showIsolatedDanger - true if ISODGR01 symbol should be shown
//	displayPriority - 8 for isolated dangers
//	viewingGroup - 14010 (DISPLAYBASE) or 24020 (STANDARD)
//
// NOTE: This implementation cannot access underlying DEPARE objects, so it uses
// simplified logic based on depth value alone.
func (l *Library) csUDWHAZ05(depthValue float64, attributes map[string]interface{}, mariner *MarinerSettings) (showIsolatedDanger bool, displayPriority int, viewingGroup int) {
	// Initialize return values
	showIsolatedDanger = false
	displayPriority = 8
	viewingGroup = 34050 // Default viewing group

	// Step 1: Check if depth is less than or equal to safety contour
	if depthValue > mariner.SafetyContour {
		// Not shallow enough to be a danger
		return false, displayPriority, viewingGroup
	}

	// Get WATLEV attribute
	watlev := getIntValue(attributes["WATLEV"])

	// Step 2: Check if above water (not an isolated underwater danger)
	if watlev == 1 || watlev == 2 {
		// Above water danger - no isolated danger symbol
		// But still in DISPLAYBASE category
		return false, displayPriority, 14050
	}

	// NOTE: Full implementation would check underlying DEPARE objects here
	// to determine if object is within safe deep water.
	// For simplified implementation, we assume if depth <= safety contour
	// and underwater, it's an isolated danger.

	// Step 3: Object is underwater and depth <= safety contour
	// Mark as isolated danger in DISPLAYBASE
	showIsolatedDanger = true
	viewingGroup = 14010 // DISPLAYBASE with isolated danger

	// Step 4: Check if mariner wants to show isolated dangers in shallow water
	// (This would check if object is between 0m and safety contour)
	if mariner.ShowIsolatedDangersInShallowWater {
		// If showing shallow water dangers, use STANDARD category
		viewingGroup = 24020
	}

	return showIsolatedDanger, displayPriority, viewingGroup
}

// csQUAPNT02 - Quality of Point Sub-Procedure
// Determines if low accuracy symbol should be shown for point/area objects
//
// S-52 Section 13.2.9: QUAPNT02 (pages 53-56)
//
// Algorithm:
// 1. Check if mariner has enabled "Show Low Accuracy Symbols"
// 2. Loop through spatial components checking QUAPOS attribute
// 3. If any QUAPOS value is 2-9 (uncertain), return LOWACC01 symbol
//
// Returns:
//
//	showLowAccuracy - true if LOWACC01 symbol should be shown
//
// NOTE: This implementation checks attributes only, not spatial components,
// since we don't have access to spatial relationships.
func (l *Library) csQUAPNT02(attributes map[string]interface{}, mariner *MarinerSettings) bool {
	// Check if mariner wants to see low accuracy symbols
	// (This would be a mariner setting in a full implementation)
	// For now, assume enabled
	showLowAccuracySymbols := true
	if !showLowAccuracySymbols {
		return false
	}

	// Get QUAPOS attribute
	quapos := getIntValue(attributes["QUAPOS"])

	// Check if QUAPOS indicates uncertain position
	// Values 2-9 = uncertain (see spec page 55)
	// Values 0, 1, 10, 11 = good quality
	if quapos >= 2 && quapos <= 9 {
		return true
	}

	// NOTE: Full implementation would loop through all spatial components
	// checking each one's QUAPOS attribute. Since we don't have access to
	// spatial components, we only check the object-level attribute.

	return false
}
