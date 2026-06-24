package s52

import (
	"fmt"
	"strconv"
	"strings"
)

// LITCHR code to characteristic abbreviation mapping.
var litchrMap = map[int]string{
	1:  "F",     // Fixed
	2:  "Fl",    // Flashing
	3:  "LFl",   // Long Flashing
	4:  "Q",     // Quick
	5:  "VQ",    // Very Quick
	6:  "UQ",    // Ultra Quick
	7:  "Iso",   // Isophase
	8:  "Oc",    // Occulting
	9:  "IQ",    // Interrupted Quick
	10: "IVQ",   // Interrupted Very Quick
	11: "IUQ",   // Interrupted Ultra Quick
	12: "Mo",    // Morse
	13: "FFl",   // Fixed and Flashing
	14: "Al",    // Alternating
	15: "LAlO",  // Long Alternating Occulting
	16: "LAlFl", // Long Alternating Flashing
	17: "OcFl",  // Occulting + Flashing
	18: "FFlA",  // Fixed, Flash, Alternating
	19: "AlOc",  // Alternating Occulting
}

// COLOUR code to color abbreviation mapping (S-57 Appendix A).
var colourCodeMap = map[int]string{
	1:  "W",  // White
	2:  "B",  // Black
	3:  "R",  // Red
	4:  "G",  // Green
	5:  "Bu", // Blue
	6:  "Y",  // Yellow
	7:  "Gr", // Grey
	8:  "Br", // Brown
	9:  "Am", // Amber
	10: "Vi", // Violet
	11: "O",  // Orange
	12: "Mg", // Magenta
	13: "Pk", // Pink
}

// BuildLightCharacteristic is the exported form of buildLightCharacteristic — an
// S-52 light characteristic string (e.g. "Fl.R.4s") for a LIGHTS feature's
// attributes, or "" if none. Used by the baker to carry light data for display.
func BuildLightCharacteristic(attributes map[string]interface{}) string {
	return buildLightCharacteristic(attributes)
}

// buildLightCharacteristic builds a light characteristic string from attributes
// Returns strings like "Fl.W.5s", "Q.R", "Oc.G.10s", etc.
// Per S-57 Appendix A and S-52 LIGHTS06 procedure
func buildLightCharacteristic(attributes map[string]interface{}) string {
	// Get LITCHR (light characteristic code)
	// Check both friendly name and attribute code (ATTR_107)
	litchr := 0
	if val, ok := attributes["LITCHR"]; ok {
		litchr = lightInt(val)
	} else if val, ok := attributes["ATTR_107"]; ok {
		litchr = lightInt(val)
	}
	if litchr == 0 {
		return "" // No characteristic specified
	}

	// Get COLOUR (light color) — single or multiple values.
	colours := lightColours(attributes)

	// Get SIGPER (signal period, s), SIGGRP (flash group), HEIGHT (m), VALNMR (range, M)
	sigper := 0.0
	if val, ok := attributes["SIGPER"]; ok {
		sigper = lightFloat(val)
	}
	siggrp := lightGroup(attributes)
	height := 0.0
	if val, ok := attributes["HEIGHT"]; ok {
		height = lightFloat(val)
	}
	valnmr := 0.0
	if val, ok := attributes["VALNMR"]; ok {
		valnmr = lightFloat(val)
	}

	// Map LITCHR code to abbreviation
	charStr := litchrMap[litchr]
	if charStr == "" {
		// Unknown characteristic code - return numeric representation
		charStr = fmt.Sprintf("LITCHR(%d)", litchr)
	}

	// Build color string (concatenated, e.g. "WR" for alternating white/red)
	var colorStr string
	for _, c := range colours {
		colorStr += mapColorCode(c)
	}

	// Assemble in S-52 / NOAA form, e.g. "Fl(1)R 3s 4.3m 5M":
	//   <char>(<group>)<colour> <period>s <height>m <range>M  (parts omitted if absent)
	result := charStr
	if siggrp != "" {
		result += "(" + siggrp + ")" + colorStr // group parens separate the colour
	} else if colorStr != "" {
		result += " " + colorStr // no group → space before colour ("Fl R")
	}
	if sigper > 0 {
		result += " " + trimFloat(sigper) + "s"
	}
	if height > 0 {
		result += " " + trimFloat(height) + "m"
	}
	if valnmr > 0 {
		result += " " + trimFloat(valnmr) + "M"
	}
	return result
}

// lightColours parses the COLOUR (or ATTR_75) attribute into the set of S-52
// colour codes present. COLOUR may be a single value, a comma/colon-separated
// string ("3,1" or "(1:1)"), or a list.
func lightColours(attributes map[string]interface{}) []int {
	val, ok := attributes["COLOUR"]
	if !ok {
		val, ok = attributes["ATTR_75"]
	}
	if !ok {
		return nil
	}
	switch v := val.(type) {
	case int:
		return []int{v}
	case float64:
		return []int{int(v)}
	case string:
		cleaned := strings.Trim(v, "()")
		cleaned = strings.ReplaceAll(cleaned, ":", ",")
		var out []int
		for _, p := range strings.Split(cleaned, ",") {
			out = append(out, lightStringToInt(strings.TrimSpace(p)))
		}
		return out
	case []int:
		return v
	case []interface{}:
		out := make([]int, 0, len(v))
		for _, item := range v {
			out = append(out, lightInt(item))
		}
		return out
	}
	return nil
}

// lightGroup returns the SIGGRP flash-group string (e.g. "1", "2+1"), or "".
func lightGroup(attributes map[string]interface{}) string {
	v, ok := attributes["SIGGRP"]
	if !ok {
		return ""
	}
	switch t := v.(type) {
	case string:
		s := strings.TrimSpace(t)
		// S-57 encodes groups like "(1)" or "(2+1)"; strip the wrapping parens.
		s = strings.Trim(s, "()")
		if s == "" || s == "0" {
			return ""
		}
		return s
	case int:
		if t <= 0 {
			return ""
		}
		return fmt.Sprintf("%d", t)
	case float64:
		if t <= 0 {
			return ""
		}
		return fmt.Sprintf("%d", int(t))
	}
	return ""
}

// trimFloat formats a float without a trailing ".0" (3.0 → "3", 4.3 → "4.3").
func trimFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// mapColorCode maps S-57 COLOUR attribute values to abbreviations.
// Returns empty string for unknown codes.
func mapColorCode(code int) string {
	return colourCodeMap[code]
}

// lightInt coerces an attribute value (int / float64 / string) to int.
func lightInt(val interface{}) int {
	switch v := val.(type) {
	case int:
		return v
	case float64:
		return int(v)
	case string:
		return lightStringToInt(v)
	default:
		return 0
	}
}

// lightFloat coerces an attribute value (float64 / int / string) to float64.
func lightFloat(val interface{}) float64 {
	switch v := val.(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case string:
		if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
			return f
		}
		return 0.0
	default:
		return 0.0
	}
}

// lightStringToInt parses a leading integer out of s, returning 0 on failure.
func lightStringToInt(s string) int {
	var result int
	fmt.Sscanf(s, "%d", &result)
	return result
}
