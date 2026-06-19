package s52

import (
	"fmt"
	"strconv"
)

// LIGHTS06 represents the Navigation Light symbology procedure.
// Implements S-52 Part I Section 13.2.4 LIGHTS06 Conditional Symbology Procedure.
//
// Per specification flowchart in pslb04_0_part1.pdf pages 130-134.
type LIGHTS06 struct {
	ctx        *CSContext
	lib        *Library
	valnmr     float64 // Nominal range in nautical miles
	catlit     int     // Category of light
	litchr     int     // Light characteristic
	colour     int     // Light color
	hasColour  bool    // Whether COLOUR is present
	sector1    float64 // Sector start bearing
	sector2    float64 // Sector end bearing
	hasSectors bool    // Whether SECTR1/SECTR2 are present
	orient     float64 // Orientation for directional lights
	hasOrient  bool    // Whether ORIENT is present
}

// NewLIGHTS06 creates a new LIGHTS06 procedure instance by parsing the execution context.
func NewLIGHTS06(csctx *CSContext, lib *Library) *LIGHTS06 {
	colour := csctx.GetInt("COLOUR", 12) // Default: magenta per spec
	hasColour := csctx.Has("COLOUR")

	sector1 := csctx.GetFloat("SECTR1", 0.0)
	sector2 := csctx.GetFloat("SECTR2", 0.0)
	hasSectors := csctx.Has("SECTR1") && csctx.Has("SECTR2")

	orient := csctx.GetFloat("ORIENT", 0.0)
	hasOrient := csctx.Has("ORIENT")

	return &LIGHTS06{
		ctx:        csctx,
		lib:        lib,
		valnmr:     csctx.GetFloat("VALNMR", 9.0), // Default per spec
		catlit:     csctx.GetInt("CATLIT", 0),
		litchr:     csctx.GetInt("LITCHR", 0),
		colour:     colour,
		hasColour:  hasColour,
		sector1:    sector1,
		sector2:    sector2,
		hasSectors: hasSectors,
		orient:     orient,
		hasOrient:  hasOrient,
	}
}

// Execute runs the LIGHTS06 symbology procedure and returns rendering instructions.
func (lt *LIGHTS06) Execute() ([]Instruction, error) {
	var instructions []Instruction

	// Step 1: Check for floodlight or spotlight (CATLIT 8 or 11)
	if lt.catlit == 8 || lt.catlit == 11 {
		return []Instruction{&SYInstruction{SymbolID: "LIGHTS82", Rotation: 0.0}}, nil
	}

	// Step 2: Check for strip light (CATLIT 9)
	if lt.catlit == 9 {
		return []Instruction{&SYInstruction{SymbolID: "LIGHTS81", Rotation: 0.0}}, nil
	}

	// Step 3: Handle directional lights
	if lt.isDirectional() {
		instructions = append(instructions, lt.directionalLineInstruction()...)
	}

	// Step 4: Add light symbol with rotation
	symbol, rotation := lt.selectSymbolAndRotation()
	instructions = append(instructions, &SYInstruction{
		SymbolID: symbol,
		Rotation: lt.normalizeRotation(rotation),
	})

	// Step 5: Add sector arc/legs if light has sectors. Sector colour is keyed on
	// the COLOUR *set* (S-52 combination table), not a single value.
	if lt.hasSectors && !lt.isNoSector() {
		instructions = append(instructions, &SectorInstruction{
			StartAngle:   lt.sector1,
			EndAngle:     lt.sector2,
			Radius:       lt.valnmr,
			Color:        sectorColorToken(lightColours(lt.ctx.Attributes)),
			Transparency: 0, // Opaque
			ShowLegs:     true,
		})
	}

	// Add light characteristic text
	if lightChar := buildLightCharacteristic(lt.ctx.Attributes); lightChar != "" {
		instructions = append(instructions, lt.characteristicTextInstruction(lightChar))
	}

	return instructions, nil
}

// isDirectional returns true if the light is directional or has moiré effect.
func (lt *LIGHTS06) isDirectional() bool {
	return (lt.catlit == 1 || lt.catlit == 16) && lt.hasOrient
}

// directionalLineInstruction creates the dashed bearing line for directional lights.
func (lt *LIGHTS06) directionalLineInstruction() []Instruction {
	return []Instruction{
		&LSInstruction{
			Style: "DASH",
			Width: 1,
			Color: "CHBLK",
		},
	}
}

// selectSymbolAndRotation determines the appropriate symbol and rotation angle.
func (lt *LIGHTS06) selectSymbolAndRotation() (string, float64) {
	symbol := selectSymbolByColour(lt.colour)
	// All-round lights with no direction/sector get the S-52 default flare
	// orientation: the LIGHTS11/12/13 flare is drawn natively pointing up, and
	// the presentation library rotates it 135° clockwise so it points down-right
	// (clear of the upper-right label position). Directional/sectored lights
	// override this to point at their bearing.
	rotation := 135.0

	if lt.isDirectional() {
		// Directional light: ORIENT is bearing FROM seaward, flare points TOWARD seaward
		rotation = lt.orient + 180.0
	} else if !lt.isNoSector() {
		// Sectored light: rotate flare to point toward sector midpoint
		rotation = lt.calculateSectorMidpoint() + 180.0
	}

	return symbol, rotation
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
		cleaned := v
		if len(v) > 0 && v[0] == '(' {
			cleaned = trimString(v, "()")
		}
		cleaned = replaceAll(cleaned, ":", ",")
		var out []int
		for _, p := range splitString(cleaned, ",") {
			out = append(out, stringToInt(trimSpace(p)))
		}
		return out
	case []int:
		return v
	case []interface{}:
		out := make([]int, 0, len(v))
		for _, item := range v {
			out = append(out, getIntValue(item))
		}
		return out
	}
	return nil
}

// sectorColorToken maps a light's COLOUR set to the sector arc colour token per
// the S-52 LIGHTS06 combination table (p.31): red (incl. white+red) → LITRD;
// green (incl. white+green) → LITGN; white / yellow / orange (incl. blue+yellow)
// → LITYW; anything else → CHMGD. Keyed on the SET, so multi-colour sectors no
// longer collapse to magenta.
func sectorColorToken(colours []int) string {
	set := map[int]bool{}
	for _, c := range colours {
		set[c] = true
	}
	switch {
	case set[3]:
		return "LITRD"
	case set[4]:
		return "LITGN"
	case set[1] || set[6] || set[11]:
		return "LITYW"
	default:
		return "CHMGD"
	}
}

// isNoSector returns true if the light has no sector or the sector covers all directions.
func (lt *LIGHTS06) isNoSector() bool {
	if !lt.hasSectors {
		return true
	}
	return (lt.sector1 == lt.sector2) ||
		(lt.sector1 == 0.0 && lt.sector2 == 360.0) ||
		(lt.sector1 == 360.0 && lt.sector2 == 0.0)
}

// calculateSectorMidpoint calculates the midpoint angle of a sector.
func (lt *LIGHTS06) calculateSectorMidpoint() float64 {
	if lt.sector2 >= lt.sector1 {
		return (lt.sector1 + lt.sector2) / 2.0
	}
	// Wraps around 0/360
	midAngle := (lt.sector1 + lt.sector2 + 360) / 2.0
	if midAngle >= 360 {
		midAngle -= 360
	}
	return midAngle
}

// normalizeRotation normalizes a rotation angle to 0-360 degrees.
func (lt *LIGHTS06) normalizeRotation(rotation float64) float64 {
	for rotation >= 360.0 {
		rotation -= 360.0
	}
	for rotation < 0.0 {
		rotation += 360.0
	}
	return rotation
}

// characteristicTextInstruction creates the text instruction for light characteristic.
func (lt *LIGHTS06) characteristicTextInstruction(lightChar string) *TXInstruction {
	return &TXInstruction{
		TextInstruction: &TextInstruction{
			Text:    lightChar,
			HJust:   2, // Center
			VJust:   3, // Top (text below symbol)
			Space:   2,
			Font:    FontSpec{Style: 1, Weight: 5, Slant: 1, BodySize: 10},
			XOffset: 0,
			YOffset: 1, // Below the light symbol
			Color:   "CHBLK",
			Display: 28, // Display group for lights
		},
	}
}

// Lookup maps for light symbology (package level for efficiency)
var (
	// Per S-52 LIGHTS06 flowchart symbol selection table (spec figure 7)
	colourToSymbolMap = map[int]string{
		1:  "LIGHTS13", // White
		3:  "LIGHTS11", // Red
		4:  "LIGHTS12", // Green
		6:  "LIGHTS13", // Yellow
		11: "LIGHTS13", // Orange
	}

	// LITCHR code to characteristic abbreviation mapping
	litchrMap = map[int]string{
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

	// COLOUR code to color abbreviation mapping (S-57 Appendix A)
	colourCodeMap = map[int]string{
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
)

// selectSymbolByColour selects LIGHTS11/12/13 based on COLOUR attribute.
// Per S-52 LIGHTS06 flowchart symbol selection table.
func selectSymbolByColour(colour int) string {
	if symbol, ok := colourToSymbolMap[colour]; ok {
		return symbol
	}
	return "LIGHTS11" // Default for other colors
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
		litchr = getIntValue(val)
	} else if val, ok := attributes["ATTR_107"]; ok {
		litchr = getIntValue(val)
	}
	if litchr == 0 {
		return "" // No characteristic specified
	}

	// Get COLOUR (light color) — single or multiple values.
	colours := lightColours(attributes)

	// Get SIGPER (signal period, s), SIGGRP (flash group), HEIGHT (m), VALNMR (range, M)
	sigper := 0.0
	if val, ok := attributes["SIGPER"]; ok {
		sigper = getFloatValue(val)
	}
	siggrp := lightGroup(attributes)
	height := 0.0
	if val, ok := attributes["HEIGHT"]; ok {
		height = getFloatValue(val)
	}
	valnmr := 0.0
	if val, ok := attributes["VALNMR"]; ok {
		valnmr = getFloatValue(val)
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

// lightGroup returns the SIGGRP flash-group string (e.g. "1", "2+1"), or "".
func lightGroup(attributes map[string]interface{}) string {
	v, ok := attributes["SIGGRP"]
	if !ok {
		return ""
	}
	switch t := v.(type) {
	case string:
		s := trimSpace(t)
		// S-57 encodes groups like "(1)" or "(2+1)"; strip the wrapping parens.
		s = trimString(s, "()")
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
