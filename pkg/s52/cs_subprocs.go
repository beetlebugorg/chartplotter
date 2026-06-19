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

// csRESCSP02 - Restriction Sub-Procedure (S-52 PresLib 4.0, Figure 28, p.71-76).
// Selects ONE centred restriction symbol from the RESTRN value set, by family:
// entry (ENTRES) / anchoring (ACHRES) / fishing (FSHRES) / other (CTYARE) /
// information-only (INFARE) / unknown (RSRDEF), each with a base (51), "additional
// restriction !" (61), or "information i" (71) variant. Used by RESTRN01 and
// DEPARE03 (DRGARE). No boundary — that's the calling procedure's job.
func (l *Library) csRESCSP02(attributes map[string]interface{}, mariner *MarinerSettings) ([]Instruction, error) {
	vals := restrnValues(attributes)
	if len(vals) == 0 {
		return nil, nil
	}
	has := func(set ...int) bool {
		for _, s := range set {
			if vals[s] {
				return true
			}
		}
		return false
	}
	// "Information" secondary set (own-ship restrictions) → the 71 "i" variant,
	// shared across families.
	info := []int{9, 10, 11, 12, 15, 18, 19, 20, 21, 22}

	var symbolID string
	switch {
	case has(7, 8, 14): // entry prohibited / restricted / area to be avoided
		switch {
		case has(1, 2, 3, 4, 5, 6, 13, 16, 17, 23, 24, 25, 26, 27):
			symbolID = "ENTRES61"
		case has(info...):
			symbolID = "ENTRES71"
		default:
			symbolID = "ENTRES51"
		}
	case has(1, 2): // anchoring prohibited / restricted
		switch {
		case has(3, 4, 5, 6, 13, 16, 17, 23, 24, 25, 26, 27):
			symbolID = "ACHRES61"
		case has(info...):
			symbolID = "ACHRES71"
		default:
			symbolID = "ACHRES51"
		}
	case has(3, 4, 5, 6, 24): // fishing / trawling prohibited or restricted
		switch {
		case has(13, 16, 17, 23, 25, 26, 27):
			symbolID = "FSHRES61"
		case has(info...):
			symbolID = "FSHRES71"
		default:
			symbolID = "FSHRES51"
		}
	case has(13, 16, 17, 23, 25, 26, 27): // other restrictions
		if has(info...) {
			symbolID = "CTYARE71"
		} else {
			symbolID = "CTYARE51"
		}
	case has(info...): // information / own-ship restrictions only
		symbolID = "INFARE51"
	default:
		symbolID = "RSRDEF51" // restriction of an unknown nature
	}

	return []Instruction{&SYInstruction{SymbolID: symbolID}}, nil
}

// restrnValues parses the RESTRN attribute (string "1,3", int, float, or a list)
// into the set of restriction codes present. Codes <= 0 are ignored.
func restrnValues(attributes map[string]interface{}) map[int]bool {
	out := map[int]bool{}
	v, ok := attributes["RESTRN"]
	if !ok || v == nil {
		return out
	}
	addStr := func(s string) {
		for _, p := range splitAndTrim(s, ",") {
			if n := stringToInt(p); n > 0 {
				out[n] = true
			}
		}
	}
	switch t := v.(type) {
	case string:
		addStr(t)
	case int:
		if t > 0 {
			out[t] = true
		}
	case int64:
		if t > 0 {
			out[int(t)] = true
		}
	case float64:
		if t > 0 {
			out[int(t)] = true
		}
	case []int:
		for _, n := range t {
			if n > 0 {
				out[n] = true
			}
		}
	case []interface{}:
		for _, e := range t {
			switch x := e.(type) {
			case int:
				if x > 0 {
					out[x] = true
				}
			case float64:
				if x > 0 {
					out[int(x)] = true
				}
			case string:
				addStr(x)
			}
		}
	case []string:
		for _, s := range t {
			addStr(s)
		}
	}
	return out
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
// When spatial context is available, LEAST_DEPTH/SEABED_DEPTH are derived from the
// shoalest underlying depth area's DRVAL1; an underlying UNSARE (unsurveyed)
// leaves them unknown. Without spatial context, returns (-1, -1) and the caller
// falls back to its CATWRK/WATLEV defaults.
func (l *Library) csDEPVAL02(attributes map[string]interface{}, spatial *SpatialContext, mariner *MarinerSettings) (leastDepth float64, seabedDepth float64) {
	leastDepth, seabedDepth = -1.0, -1.0
	if spatial == nil {
		return leastDepth, seabedDepth
	}

	// Shoalest underlying DEPARE/DRGARE DRVAL1. An UNSARE underneath means the
	// depth is unsurveyed → leave unknown and exit (S-52 DEPVAL02).
	found := false
	var least float64
	for _, u := range spatial.UnderlyingObjects {
		if u.ObjectClass == "UNSARE" {
			return -1.0, -1.0
		}
		if u.ObjectClass != "DEPARE" && u.ObjectClass != "DRGARE" {
			continue
		}
		if v, ok := u.Attributes["DRVAL1"]; ok {
			d := getFloatValue(v)
			if !found || d < least {
				least, found = d, true
			}
		}
	}
	if !found {
		return -1.0, -1.0
	}

	// SEABED_DEPTH is the underlying least depth. LEAST_DEPTH stays known only for
	// an always-underwater object whose sounding is within/deeper than its range
	// (WATLEV==3 && EXPSOU in {1,3}); otherwise it is cleared (S-52 DEPVAL02).
	seabedDepth = least
	watlev := getIntValue(attributes["WATLEV"])
	expsou := getIntValue(attributes["EXPSOU"])
	if watlev == 3 && (expsou == 1 || expsou == 3) {
		leastDepth = least
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
// When spatial context is available, the hazard is an ISOLATED danger only if it
// lies in otherwise-safe water — i.e. an underlying/surrounding depth area is
// DEEPER than the safety contour (DRVAL1 >= SAFETY_CONTOUR). Without spatial
// context we cannot tell, so fall back to the conservative "show it" (safety
// first), which is the old behaviour.
func (l *Library) csUDWHAZ05(depthValue float64, attributes map[string]interface{}, spatial *SpatialContext, mariner *MarinerSettings) (showIsolatedDanger bool, displayPriority int, viewingGroup int) {
	showIsolatedDanger = false
	displayPriority = 8
	viewingGroup = 34050 // Default viewing group

	// Step 1: deeper than the safety contour → not a danger.
	if depthValue > mariner.SafetyContour {
		return false, displayPriority, viewingGroup
	}

	// Step 2: above water (WATLEV 1/2) → not an isolated underwater danger.
	if watlev := getIntValue(attributes["WATLEV"]); watlev == 1 || watlev == 2 {
		return false, displayPriority, 14050
	}

	// Step 3: isolated-danger test. With underlying depth areas, the hazard is
	// isolated only if it sits in safe water (an underlying DEPARE/DRGARE with
	// DRVAL1 >= SAFETY_CONTOUR). Without that context, assume it is (conservative).
	if !inSafeWater(spatial, mariner.SafetyContour) {
		return false, displayPriority, viewingGroup
	}

	showIsolatedDanger = true
	viewingGroup = 14010 // DISPLAYBASE with isolated danger
	if mariner.ShowIsolatedDangersInShallowWater {
		viewingGroup = 24020 // STANDARD
	}
	return showIsolatedDanger, displayPriority, viewingGroup
}

// inSafeWater reports whether any underlying depth area is deeper than the safety
// contour (DRVAL1 >= safetyContour). Returns true (conservative) when no spatial
// context / no underlying depth areas are available, so a hazard is never
// silently dropped for lack of topology.
func inSafeWater(spatial *SpatialContext, safetyContour float64) bool {
	if spatial == nil || len(spatial.UnderlyingObjects) == 0 {
		return true
	}
	sawDepthArea := false
	for _, u := range spatial.UnderlyingObjects {
		if u.ObjectClass != "DEPARE" && u.ObjectClass != "DRGARE" {
			continue
		}
		sawDepthArea = true
		if v, ok := u.Attributes["DRVAL1"]; ok && getFloatValue(v) >= safetyContour {
			return true
		}
	}
	// Underlying depth areas exist but all are shallow → not isolated. If none
	// were depth areas at all, we still can't tell → conservative true.
	return !sawDepthArea
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
