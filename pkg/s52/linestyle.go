// Package dai implements linestyle parsing for IHO S-52 DAI linestyle definitions.
//
// References:
// - IHO S-52 Presentation Library Edition 4.0.0, Section 11.7: Complex Linestyle Module
// - IHO S-52 Section 8.2: Complex Line Style Rendering
// - IHO S-52 Section 9.3: SHOWLINE Command (LC)
//
// Linestyles define repeating patterns along lines, including symbol calls
// and vector drawing commands for complex line presentations.
package s52

import (
	"fmt"
	"strconv"
	"strings"
)

// ParseLIND parses the LIND field containing linestyle definition data.
// S-52 Section 11.7.2: LIND - Linestyle Definition
// Format: LIND<length><linm><licl><lirw><lihl><livl><lbxc><lbxr>
// Example: LIND38CBLLNE01007500075000200001000075000700
//
//	linm: CBLLNE01 (8 chars) - Linestyle name
//	licl: 00750 (5 digits) - Pivot column
//	lirw: 00750 (5 digits) - Pivot row
//	lihl: 00200 (5 digits) - Bbox width (200 = 2.0mm)
//	livl: 00100 (5 digits) - Bbox height (100 = 1.0mm)
//	lbxc: 00750 (5 digits) - Bbox upper-left column
//	lbxr: 00700 (5 digits) - Bbox upper-left row
func (ls *Linestyle) ParseLIND(lind string) error {
	// LIND format after record type and length is removed:
	// <linm><licl><lirw><lihl><livl><lbxc><lbxr>
	// Total: 8 + 5 + 5 + 5 + 5 + 5 + 5 = 38 characters minimum

	if len(lind) < 38 {
		return fmt.Errorf("LIND too short: expected at least 38 chars, got %d", len(lind))
	}

	// Parse linestyle name (8 characters)
	ls.ID = strings.TrimSpace(lind[0:8])

	// Parse pivot column (5 digits)
	pivotX, err := strconv.Atoi(lind[8:13])
	if err != nil {
		return fmt.Errorf("invalid pivot column in LIND: %v", err)
	}
	ls.PivotX = pivotX

	// Parse pivot row (5 digits)
	pivotY, err := strconv.Atoi(lind[13:18])
	if err != nil {
		return fmt.Errorf("invalid pivot row in LIND: %v", err)
	}
	ls.PivotY = pivotY

	// Parse bbox width (5 digits) - in 0.01mm units
	bboxWidth, err := strconv.Atoi(lind[18:23])
	if err != nil {
		return fmt.Errorf("invalid bbox width in LIND: %v", err)
	}
	ls.BBoxWidth = bboxWidth

	// Parse bbox height (5 digits) - in 0.01mm units
	bboxHeight, err := strconv.Atoi(lind[23:28])
	if err != nil {
		return fmt.Errorf("invalid bbox height in LIND: %v", err)
	}
	ls.BBoxHeight = bboxHeight

	// Parse bbox upper-left column (5 digits)
	bboxX, err := strconv.Atoi(lind[28:33])
	if err != nil {
		return fmt.Errorf("invalid bbox X in LIND: %v", err)
	}
	ls.BBoxX = bboxX

	// Parse bbox upper-left row (5 digits)
	bboxY, err := strconv.Atoi(lind[33:38])
	if err != nil {
		return fmt.Errorf("invalid bbox Y in LIND: %v", err)
	}
	ls.BBoxY = bboxY

	// Initialize metadata
	if ls.Metadata == nil {
		ls.Metadata = make(map[string]string)
	}
	ls.Metadata["raw_lind"] = lind

	return nil
}

// ParseLCRF parses the LCRF field containing color reference data.
// S-52 Section 11.7.4: LCRF - Linestyle Color Reference
// Format: LCRF<length>*<cidx><ctok>
// Example: LCRF6ICHMGD
//
//	cidx: I (color index letter, ASCII >= 64)
//	ctok: CHMGD (5-char color token)
//
// Multiple colors: LCRF11ACHMGDBCHBLK
//
//	A->CHMGD, B->CHBLK (each is 1+5=6 chars)
func (ls *Linestyle) ParseLCRF(lcrf string) error {
	// Store raw color reference
	ls.ColorRef = lcrf

	// Parse color mappings: each is 1 char (role) + 5 chars (token)
	colors := ParsedColors{
		Roles: make(map[rune]string),
	}

	for i := 0; i < len(lcrf); i += 6 {
		if i+6 > len(lcrf) {
			break
		}
		role := rune(lcrf[i])
		token := lcrf[i+1 : i+6]
		colors.Roles[role] = token
	}

	ls.Colors = colors
	return nil
}

// ParseLVCT parses linestyle vector commands.
// S-52 Section 11.7.5: LVCT - Linestyle Vector Commands
// Format: LVCT<length><vector>
// Example: LVCT57SPI;PU850,750;SW1;AA900,750,180;
//
// Vector commands use HP-GL/DAI format (same as SVCT for symbols):
//
//	SPI/SPA - Set pen color by index
//	PU - Pen up (move)
//	PD - Pen down (draw)
//	SW - Set width
//	CI - Circle
//	AA - Arc absolute
//	PM - Polygon mode
//	SC - Symbol call (e.g., SCVLINEMAG,2;)
//
// Multiple LVCT records can define one linestyle pattern.
func (ls *Linestyle) ParseLVCT(lvct string) error {
	if strings.TrimSpace(lvct) == "" {
		return nil
	}

	// Create or get the persistent HP-GL parser
	var parser *daiVectorParser
	if ls.hpglParser == nil {
		parser = newDaiVectorParser()
		ls.hpglParser = parser
	} else {
		parser = ls.hpglParser.(*daiVectorParser)
	}

	// Parse the HP-GL commands
	err := parser.ParseCommands(lvct)
	if err != nil {
		return err
	}

	// Update linestyle's vector commands (parser accumulates them internally)
	ls.VectorCommands = parser.GetCommands()

	return nil
}

// HasSymbolCalls returns true if the linestyle contains symbol call commands.
func (ls *Linestyle) HasSymbolCalls() bool {
	for _, cmd := range ls.VectorCommands {
		if strings.Contains(cmd.RawCommand, "SC") {
			return true
		}
	}
	return false
}

// Validate performs basic validation on the linestyle definition.
// S-52 requires:
//   - Valid ID (non-empty, 8 chars max)
//   - Pivot point within reasonable bounds
//   - Non-zero bounding box dimensions
//   - At least one vector command
func (ls *Linestyle) Validate() []string {
	var warnings []string

	if ls.ID == "" {
		warnings = append(warnings, "Linestyle missing ID")
	}

	if ls.BBoxWidth == 0 || ls.BBoxHeight == 0 {
		warnings = append(warnings, fmt.Sprintf("Linestyle %s has zero bbox dimensions", ls.ID))
	}

	if len(ls.VectorCommands) == 0 {
		warnings = append(warnings, fmt.Sprintf("Linestyle %s has no vector commands", ls.ID))
	}

	return warnings
}
