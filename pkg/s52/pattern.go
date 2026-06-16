// Package dai implements pattern parsing for IHO S-52 DAI pattern definitions.
//
// References:
// - specs/s52-dai-format.md - Complete S-52 DAI format specification
// - IHO S-52 Presentation Library pattern specification
//
// Patterns define area fill patterns using vector commands and tiling parameters
// for complex area presentations in maritime charts.
package s52

import (
	"fmt"
	"strconv"
)

// ParsePATD parses the PATD field containing pattern definition data.
//
// S-52 PresLib e4.0.0, Part I, Section 11.5.3: Pattern Definition-Field (PATD)
//
// Format (55 characters total):
//
//	PANM (8):  Pattern name
//	PADF (1):  Definition type: V=vector, R=raster
//	PATP (3):  Pattern type: STG=staggered, LIN=linear
//	PASP (3):  Spacing type: CON=constant, SCL=scale-dependent
//	PAMI (5):  Minimum distance between pattern symbols (0.01mm units)
//	PAMA (5):  Maximum distance (scale-dependent only, else 0)
//	PACL (5):  Pivot point column number (DAI coordinate space)
//	PARW (5):  Pivot point row number (DAI coordinate space)
//	PAHL (5):  Bounding box width (DAI units)
//	PAVL (5):  Bounding box height (DAI units)
//	PBXC (5):  Bounding box upper-left column
//	PBXR (5):  Bounding box upper-left row
//
// Example: AIRARE02VSTGCON0200010000022590225600618005280043500452
//
//	AIRARE02 = pattern name
//	V = vector
//	STG = staggered
//	CON = constant spacing
//	02000 = min dist 2000 units = 20mm
//	10000 = max dist (not used for CON)
//	02259 = pivot column
//	...
func (p *Pattern) ParsePATD(patd string) error {
	if len(patd) != 55 {
		return fmt.Errorf("PATD field must be 55 characters, got %d: %s", len(patd), patd)
	}

	// Parse fixed-position fields
	p.ID = patd[0:8]    // PANM
	p.Type = patd[8:9]  // PADF
	patp := patd[9:12]  // PATP (pattern type)
	pasp := patd[12:15] // PASP (spacing type)

	// Validate type
	if p.Type != "V" && p.Type != "R" {
		return fmt.Errorf("invalid pattern definition type '%s', expected V or R", p.Type)
	}

	// Build combined pattern type string for compatibility
	p.PatternType = patp + pasp

	// Parse numeric fields (5 digits each, in 0.01mm units)
	var err error
	pami, err := strconv.Atoi(patd[15:20]) // Minimum distance
	if err != nil {
		return fmt.Errorf("parse PAMI: %w", err)
	}
	pama, err := strconv.Atoi(patd[20:25]) // Maximum distance
	if err != nil {
		return fmt.Errorf("parse PAMA: %w", err)
	}
	pacl, err := strconv.Atoi(patd[25:30]) // Pivot column
	if err != nil {
		return fmt.Errorf("parse PACL: %w", err)
	}
	parw, err := strconv.Atoi(patd[30:35]) // Pivot row
	if err != nil {
		return fmt.Errorf("parse PARW: %w", err)
	}
	pahl, err := strconv.Atoi(patd[35:40]) // Width
	if err != nil {
		return fmt.Errorf("parse PAHL: %w", err)
	}
	pavl, err := strconv.Atoi(patd[40:45]) // Height
	if err != nil {
		return fmt.Errorf("parse PAVL: %w", err)
	}
	pbxc, err := strconv.Atoi(patd[45:50]) // BBox column
	if err != nil {
		return fmt.Errorf("parse PBXC: %w", err)
	}
	pbxr, err := strconv.Atoi(patd[50:55]) // BBox row
	if err != nil {
		return fmt.Errorf("parse PBXR: %w", err)
	}

	// S-52 spec: PAMI is the minimum distance between pattern symbol COVERS (bounding box + pivot)
	// This is the spacing we need for tiling
	p.SpacingX = pami
	p.SpacingY = pami // Use same spacing for both X and Y

	// Store tile dimensions
	p.TileWidth = pahl
	p.TileHeight = pavl

	// Store bounding box position (upper-left corner in pattern coordinate space)
	p.BBoxX = pbxc
	p.BBoxY = pbxr

	// Store pivot point
	p.PivotX = pacl
	p.PivotY = parw

	// Store metadata for debugging
	p.Metadata = map[string]string{
		"raw_patd": patd,
		"patp":     patp,
		"pasp":     pasp,
		"pami":     fmt.Sprintf("%d", pami),
		"pama":     fmt.Sprintf("%d", pama),
		"pacl":     fmt.Sprintf("%d", pacl),
		"parw":     fmt.Sprintf("%d", parw),
		"pahl":     fmt.Sprintf("%d", pahl),
		"pavl":     fmt.Sprintf("%d", pavl),
		"pbxc":     fmt.Sprintf("%d", pbxc),
		"pbxr":     fmt.Sprintf("%d", pbxr),
	}

	return nil
}

// ParsePVCT parses pattern vector commands.
// Multiple PVCT records can define the complete pattern geometry.
// IMPORTANT: State is preserved across multiple PVCT calls to handle patterns
// where vector commands span multiple records (e.g., ICEARE04).
func (p *Pattern) ParsePVCT(pvct string) error {
	// Initialize parser on first call, reuse for subsequent calls
	if p.hpglParser == nil {
		p.hpglParser = newDaiVectorParser()
	}

	// Parse the vector commands - state is preserved across calls
	if err := p.hpglParser.ParseCommands(pvct); err != nil {
		return fmt.Errorf("failed to parse PVCT commands: %v", err)
	}

	// Get all commands parsed so far (including from previous PVCT records)
	p.VectorCommands = p.hpglParser.GetCommands()

	return nil
}

// GetTileDimensions returns the pattern tile dimensions in DAI units.
func (p *Pattern) GetTileDimensions() (width, height int) {
	return p.TileWidth, p.TileHeight
}

// GetSpacing returns the pattern spacing in DAI units.
func (p *Pattern) GetSpacing() (spacingX, spacingY int) {
	return p.SpacingX, p.SpacingY
}

// HasSymbolCalls returns true if the pattern contains symbol call commands.
func (p *Pattern) HasSymbolCalls() bool {
	for _, cmd := range p.VectorCommands {
		if cmd.Type == "SC" || cmd.SymbolCall != nil {
			return true
		}
	}
	return false
}

// GetSymbolCalls returns all symbol call commands in this pattern.
func (p *Pattern) GetSymbolCalls() []VectorCommand {
	var symbolCalls []VectorCommand
	for _, cmd := range p.VectorCommands {
		if cmd.Type == "SC" || cmd.SymbolCall != nil {
			symbolCalls = append(symbolCalls, cmd)
		}
	}
	return symbolCalls
}

// NormalizeCoordinates applies the BBoxOffset to all vector commands,
// normalizing pattern coordinates into the (0, 0, TileWidth, TileHeight) space.
//
// S-52 Spec: Pattern primitives are defined in DAI coordinate space. The PBXC/PBXR
// (BBoxX/BBoxY) values from the PATD record define the upper-left corner of the
// bounding box in that space. To render the pattern in its own tile coordinate system,
// we offset all coordinates by (-BBoxX, -BBoxY) so the bounding box starts at (0, 0).
//
// This must be called after all PVCT records have been parsed and PATD has been processed.
func (p *Pattern) NormalizeCoordinates() {
	if len(p.VectorCommands) == 0 {
		return
	}

	// S-52: Use PBXC/PBXR (BBoxX/BBoxY) as the offset, not calculated min coordinates
	offsetX := float64(-p.BBoxX)
	offsetY := float64(-p.BBoxY)

	// Apply offset to all points in all vector commands
	for i := range p.VectorCommands {
		cmd := &p.VectorCommands[i]

		// Offset all points
		for j := range cmd.Points {
			cmd.Points[j].X += offsetX
			cmd.Points[j].Y += offsetY
		}

		// Offset center point (for circles, arcs)
		if cmd.Center != nil {
			cmd.Center.X += offsetX
			cmd.Center.Y += offsetY
		}

		// Offset all rings (for polygons)
		for ringIdx := range cmd.Rings {
			for ptIdx := range cmd.Rings[ringIdx] {
				cmd.Rings[ringIdx][ptIdx].X += offsetX
				cmd.Rings[ringIdx][ptIdx].Y += offsetY
			}
		}

		// Offset rectangle (if present)
		if cmd.Rectangle != nil {
			cmd.Rectangle.X += offsetX
			cmd.Rectangle.Y += offsetY
		}

		// Offset symbol call position (if present)
		if cmd.SymbolCall != nil {
			cmd.SymbolCall.CallPosition.X += offsetX
			cmd.SymbolCall.CallPosition.Y += offsetY
		}
	}

	// Add metadata about the transformation
	if p.Metadata == nil {
		p.Metadata = make(map[string]string)
	}
	p.Metadata["normalized"] = "true"
	p.Metadata["bbox_offset"] = fmt.Sprintf("%.0f,%.0f", offsetX, offsetY)
}

// CalculateVectorBounds calculates the bounding box of all vector commands in the pattern.
//
// S-52 PresLib e4.0.0 Section 8 (page 31): Vector coordinates are within 0-32767 units,
// where each unit = 0.01mm. Pattern vectors use absolute coordinates in this global
// coordinate space, not coordinates relative to the pattern tile.
//
// S-52 PresLib e4.0.0 Section 8.5.4 (page 42): "The position where an area fill with
// a pattern symbol is started must be based on a geographical position and not on an
// edge of the screen." This explains why pattern coordinates may exceed tile bounds -
// they reference positions on an infinite pattern sheet anchored to chart geography.
//
// For example, DQUALA21 has coordinates like 2779,1066 which exceed its 1400-unit
// tile width because they define positions on the global pattern coordinate system.
func (p *Pattern) CalculateVectorBounds() (minX, minY, maxX, maxY float64, hasCoordinates bool) {
	minX, minY = 999999, 999999
	maxX, maxY = -999999, -999999
	hasCoordinates = false

	for _, cmd := range p.VectorCommands {
		for _, point := range cmd.Points {
			hasCoordinates = true
			x, y := point.X, point.Y
			if x < minX {
				minX = x
			}
			if x > maxX {
				maxX = x
			}
			if y < minY {
				minY = y
			}
			if y > maxY {
				maxY = y
			}
		}

		// Also check center points for circles
		if cmd.Center != nil {
			hasCoordinates = true
			x, y := cmd.Center.X, cmd.Center.Y
			if x < minX {
				minX = x
			}
			if x > maxX {
				maxX = x
			}
			if y < minY {
				minY = y
			}
			if y > maxY {
				maxY = y
			}
		}
	}

	return minX, minY, maxX, maxY, hasCoordinates
}

// Validate performs basic validation on the pattern definition.
func (p *Pattern) Validate() []string {
	var warnings []string

	if p.ID == "" {
		warnings = append(warnings, "Pattern missing ID")
	}

	if p.PatternType == "" {
		warnings = append(warnings, "Pattern missing pattern type")
	}

	if p.TileWidth <= 0 || p.TileHeight <= 0 {
		warnings = append(warnings, "Pattern has invalid tile dimensions")
	}

	if len(p.VectorCommands) == 0 {
		warnings = append(warnings, "Pattern has no vector commands")
	}

	return warnings
}

// GetTileInfo calculates pattern tiling information
//
// S-52 PresLib e4.0.0, Part I, Section 8.5.4: Pattern Spacing
//
// "The vertical and horizontal distance between pattern symbols is given in the
// pattern definition. This distance is the space between symbol covers."
//
// PAMI is the GAP between symbols (edge-to-edge), NOT center-to-center distance.
// For tile positioning, we need center-to-center spacing = gap + bbox size.
// For LINEAR patterns with spacing=0, tiles are placed edge-to-edge using bounding box.
//
// All coordinates in S-52 DAI format are in units of 0.01mm. This function converts
// to mm for rendering.
func (p *Pattern) GetTileInfo() PatternTileInfo {
	const daiToMM = 0.01

	// Convert DAI units to mm
	pamiX := float64(p.SpacingX) * daiToMM // Horizontal gap between symbols (mm)
	pamiY := float64(p.SpacingY) * daiToMM // Vertical gap between symbols (mm)
	bboxWidth := p.BoundingBox.Width() * daiToMM
	bboxHeight := p.BoundingBox.Height() * daiToMM

	// Determine if this is a linear pattern
	isLinear := p.PatternType == "LINCON" || p.PatternType == "LIN"

	var spacingX, spacingY float64

	if isLinear && pamiX == 0.0 && pamiY == 0.0 {
		// Linear patterns with 0 spacing: tile edge-to-edge using bbox
		spacingX = bboxWidth
		spacingY = bboxHeight
	} else {
		// PAMI is the gap between symbols, so center-to-center = gap + bbox
		// "This distance is the space between symbol covers"
		if pamiX > 0.0 {
			spacingX = bboxWidth + pamiX
		} else {
			spacingX = bboxWidth
		}
		if pamiY > 0.0 {
			spacingY = bboxHeight + pamiY
		} else {
			spacingY = bboxHeight
		}
	}

	// Sanity check - ensure minimum spacing per S-52 spec
	if spacingX < 1.0 {
		spacingX = 5.0
	}
	if spacingY < 1.0 {
		spacingY = 5.0
	}

	return PatternTileInfo{
		SpacingX:   spacingX,
		SpacingY:   spacingY,
		IsLinear:   isLinear,
		TileWidth:  bboxWidth,
		TileHeight: bboxHeight,
	}
}
