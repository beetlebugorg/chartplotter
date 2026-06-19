package s52

import "fmt"

// SOUNDG03 represents the Sounding symbology procedure.
// Symbolizes depth soundings by calling SNDFRM04 to format depth values.
//
// S-52 Section 13.2.17: SOUNDG03
//
// For multipoint SOUNDG objects, the renderer calls this once per point with DEPTH set.
type SOUNDG03 struct {
	ctx      *CSContext
	lib      *Library
	depth    float64 // Depth value
	hasDepth bool    // Whether depth was found
}

// NewSOUNDG03 creates a new SOUNDG03 procedure instance by parsing the execution context.
func NewSOUNDG03(csctx *CSContext, lib *Library) *SOUNDG03 {
	s := &SOUNDG03{
		ctx: csctx,
		lib: lib,
	}

	// Extract depth value from various possible attributes
	s.depth, s.hasDepth = s.extractDepth()

	return s
}

// Execute runs the SOUNDG03 symbology procedure and returns rendering instructions.
func (s *SOUNDG03) Execute() ([]Instruction, error) {
	if !s.hasDepth {
		return []Instruction{}, nil
	}

	// Call SNDFRM04 to get list of sounding symbols
	return s.lib.csSONDFRM04(s.depth, s.ctx.Attributes, s.ctx.Mariner)
}

// extractDepth retrieves the depth value from attributes.
// Priority order: DEPTH (renderer sets this for multipoint), VALSOU, depth, value, DEPTHS array.
func (s *SOUNDG03) extractDepth() (float64, bool) {
	// Try single depth attributes first
	for _, attr := range []string{"DEPTH", "VALSOU", "depth", "value"} {
		if val, ok := s.ctx.Attributes[attr]; ok {
			if depth, found := parseDepthValue(val); found {
				return depth, true
			}
		}
	}

	// If no single depth found, try DEPTHS array (legacy/fallback)
	if depths, ok := s.ctx.Attributes["DEPTHS"]; ok {
		if depth, found := parseDepthArray(depths); found {
			return depth, true
		}
	}

	return 0, false
}

// parseDepthValue extracts a float64 depth from various value types.
func parseDepthValue(val interface{}) (float64, bool) {
	switch v := val.(type) {
	case float64:
		return v, true
	case int:
		return float64(v), true
	case string:
		var depth float64
		_, err := fmt.Sscanf(v, "%f", &depth)
		if err == nil {
			return depth, true
		}
	}
	return 0, false
}

// parseDepthArray extracts the first depth value from a DEPTHS array.
func parseDepthArray(val interface{}) (float64, bool) {
	switch v := val.(type) {
	case []float64:
		if len(v) > 0 {
			return v[0], true
		}
	case []interface{}:
		if len(v) > 0 {
			if f, ok := v[0].(float64); ok {
				return f, true
			}
		}
	}
	return 0, false
}

// csSONDFRM04 implements the SNDFRM04 sub-procedure per S-52 Section 13.2.16.
// Converts a depth value into a list of sounding symbol IDs.
//
// Returns list of SY instructions with symbol IDs like SOUNDG13, SOUNDS56, etc.
//
// Symbol naming: {PREFIX}{TYPE}{DIGIT}
//   - Prefix: SOUNDS (shallow, bold) or SOUNDG (deep, faint)
//   - Type: 10-19 (units), 20-29 (tens), 30-39 (ten-thousands), 40-49 (small units),
//     50-59 (fractions), A1 (drying height), B1 (swept/diver), C2 (unreliable)
//   - Digit: 0-9
func (l *Library) csSONDFRM04(depthValue float64, attributes map[string]interface{}, mariner *MarinerSettings) ([]Instruction, error) {
	symbols := []string{}

	// Use mariner's depth unit preference
	// NOTE: Do NOT check if depthUnit == 0, because 0 is DepthUnitMeters (valid value)
	// Mariner settings are always provided, so just use them directly
	depthUnit := mariner.DepthUnits

	// Convert depth from meters (S-57 standard) to display unit
	displayDepth := ConvertDepth(depthValue, DepthUnitMeters, depthUnit)

	// Convert safety depth to same unit for comparison
	safetyDepthDisplay := ConvertDepth(mariner.SafetyDepth, DepthUnitMeters, depthUnit)

	// Determine symbol prefix based on safety depth
	prefix := "SOUNDG" // General (faint color) for depths > safety depth
	if displayDepth <= safetyDepthDisplay {
		prefix = "SOUNDS" // Shallow (dominant color) for depths <= safety depth
	}

	// Check for swept depth or diver sounding (TECSOU attribute)
	if tecsou, ok := attributes["TECSOU"]; ok {
		tecsouInt := getIntValue(tecsou)
		if tecsouInt == 4 || tecsouInt == 6 {
			// 4 = found by diver, 6 = swept by wire drag
			symbols = append(symbols, prefix+"B1")
		}
	}

	// Unreliable sounding: QUASOU (3,4,5,8,9) OR STATUS (18). S-52 SNDFRM04 ORs
	// these into a SINGLE "C2" decoration (the earlier code appended one per
	// attribute, stacking up to 3×).
	unreliable := false
	if quasou, ok := attributes["QUASOU"]; ok {
		q := getIntValue(quasou)
		if q == 3 || q == 4 || q == 5 || q == 8 || q == 9 {
			unreliable = true
		}
	}
	if status, ok := attributes["STATUS"]; ok {
		if getIntValue(status) == 18 { // uncertain sounding
			unreliable = true
		}
	}
	if unreliable {
		symbols = append(symbols, prefix+"C2")
	}

	// Position quality (QUAPOS not 1/10/11) is a SEPARATE C2 (so at most two).
	if quapos, ok := attributes["QUAPOS"]; ok {
		quaposInt := getIntValue(quapos)
		if quaposInt != 0 && quaposInt != 1 && quaposInt != 10 && quaposInt != 11 {
			symbols = append(symbols, prefix+"C2")
		}
	}

	// Check for drying height (negative depth)
	if displayDepth < 0 {
		symbols = append(symbols, prefix+"A1")
		displayDepth = -displayDepth // Make positive for digit extraction
	}

	// Apply depth formatting algorithm based on depth range.
	// Spec: "Truncate all digits after the decimal. Do not round up." Fractions
	// are shown for depths < 31 m (S-52 SNDFRM04) regardless of display unit.
	if displayDepth < 10.0 {
		// Algorithm 1: Depths < 10 with fraction (e.g., 3.6)
		leadingDigit := int(displayDepth)
		symbols = append(symbols, fmt.Sprintf("%s1%d", prefix, leadingDigit))

		// First decimal digit, TRUNCATED (SNDFRM04 algs 1/2: "truncate all
		// digits after the decimal; do not round up"). The +1e-6 only absorbs FP
		// error (0.7*10 = 6.9999…→7); it never reaches 10, so the glyph index
		// stays 0–9 (rounding here produced an invalid "SOUNDS510" for X.95+).
		fraction := int((displayDepth-float64(leadingDigit))*10.0 + 1e-6)
		if fraction > 0 {
			symbols = append(symbols, fmt.Sprintf("%s5%d", prefix, fraction))
		}

	} else if displayDepth < 31.0 && hasFraction(displayDepth) {
		// Algorithm 2: Depths 10-30 with fraction (e.g., 26.7)
		depthInt := int(displayDepth)
		tensDigit := depthInt / 10
		onesDigit := depthInt % 10

		// First decimal digit, TRUNCATED (see Algorithm 1 note) — never 10.
		fraction := int((displayDepth-float64(depthInt))*10.0 + 1e-6)

		symbols = append(symbols, fmt.Sprintf("%s2%d", prefix, tensDigit))
		symbols = append(symbols, fmt.Sprintf("%s1%d", prefix, onesDigit))
		if fraction > 0 {
			symbols = append(symbols, fmt.Sprintf("%s5%d", prefix, fraction))
		}

	} else if displayDepth < 100.0 {
		// Algorithm 3: Depths 31-99, no fraction (e.g., 47)
		depthInt := int(displayDepth) // Truncate, don't round
		tensDigit := depthInt / 10
		onesDigit := depthInt % 10

		symbols = append(symbols, fmt.Sprintf("%s1%d", prefix, tensDigit))
		symbols = append(symbols, fmt.Sprintf("%s0%d", prefix, onesDigit))

	} else if displayDepth < 1000.0 {
		// Algorithm 4: Depths 100-999 (e.g., 234)
		depthInt := int(displayDepth)
		hundredsDigit := depthInt / 100
		tensDigit := (depthInt / 10) % 10
		onesDigit := depthInt % 10

		symbols = append(symbols, fmt.Sprintf("%s2%d", prefix, hundredsDigit))
		symbols = append(symbols, fmt.Sprintf("%s1%d", prefix, tensDigit))
		symbols = append(symbols, fmt.Sprintf("%s0%d", prefix, onesDigit))

	} else if displayDepth < 10000.0 {
		// Algorithm 5: Depths 1000-9999 with small last digit (e.g., 2345)
		depthInt := int(displayDepth)
		thousandsDigit := depthInt / 1000
		hundredsDigit := (depthInt / 100) % 10
		tensDigit := (depthInt / 10) % 10
		onesDigit := depthInt % 10

		symbols = append(symbols, fmt.Sprintf("%s2%d", prefix, thousandsDigit))
		symbols = append(symbols, fmt.Sprintf("%s1%d", prefix, hundredsDigit))
		symbols = append(symbols, fmt.Sprintf("%s0%d", prefix, tensDigit))
		symbols = append(symbols, fmt.Sprintf("%s4%d", prefix, onesDigit)) // Small digit

	} else {
		// Algorithm 6: Depths >= 10000 with small last digit (e.g., 12345)
		depthInt := int(displayDepth)
		tenThousandsDigit := depthInt / 10000
		thousandsDigit := (depthInt / 1000) % 10
		hundredsDigit := (depthInt / 100) % 10
		tensDigit := (depthInt / 10) % 10
		onesDigit := depthInt % 10

		symbols = append(symbols, fmt.Sprintf("%s3%d", prefix, tenThousandsDigit))
		symbols = append(symbols, fmt.Sprintf("%s2%d", prefix, thousandsDigit))
		symbols = append(symbols, fmt.Sprintf("%s1%d", prefix, hundredsDigit))
		symbols = append(symbols, fmt.Sprintf("%s0%d", prefix, tensDigit))
		symbols = append(symbols, fmt.Sprintf("%s4%d", prefix, onesDigit)) // Small digit
	}

	// Convert symbol names to SY instructions
	instructions := make([]Instruction, len(symbols))
	for i, symbolID := range symbols {
		instructions[i] = &SYInstruction{SymbolID: symbolID}
	}

	return instructions, nil
}
