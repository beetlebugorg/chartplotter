// Package dai defines S-52 symbol structures and parsing for DAI symbol definitions.
//
// References:
// - specs/s52-dai-format.md section "Symbol Definitions (SYMB/SYMD/SVCT)"
// - specs/DAI_TO_SVG_RENDERING_SPEC.md section "Symbol Core Records"
// - IHO S-52 Presentation Library symbol specification
//
// Implements complete symbol parsing including geometry metadata (SYMD),
// vector command parsing (SVCT), and color reference mapping (SCRF).
package s52

import (
	"fmt"
	"strconv"
	"strings"
)

// Use shared geometry types

// GetPolygonMode returns whether the symbol is currently in polygon mode
func (s *Symbol) GetPolygonMode() bool {
	return s.polygonMode
}

// BoundingBox is now defined in interfaces.go to avoid duplication

// Note: Point and Rectangle are imported via geometry package and aliased in interfaces.go

// isDigit checks if a byte is a digit
func isDigit(b byte) bool {
	return b >= '0' && b <= '9'
}

// ParseSYMD parses the SYMD field which contains symbol metadata and geometry.
// Format: SYMBOLIDV[PIVX][PIVY][BBW][BBH][MINX][MINY]
// Example: BCNGEN01V007500075000300005150060000300
// Where each bracketed value is a 5-digit number in DAI units (1/100 mm)
// Reference: specs/s52-dai-format.md section "SYMD Record"
func (s *Symbol) ParseSYMD(symd string) error {
	if len(symd) < 39 { // 9 chars ID + 30 chars data minimum
		return fmt.Errorf("SYMD too short: %s", symd)
	}

	// Symbol ID format: Variable length + 'V' + optional 2-digit version
	// Look for 'V' that leaves exactly 30 chars for geometry data
	vIndex := -1
	hasVersion := false

	// Try to find 'V' with version (Vnn format)
	for i := 6; i < len(symd)-30 && i < 12; i++ {
		if symd[i] == 'V' && i+2 < len(symd) && isDigit(symd[i+1]) && isDigit(symd[i+2]) {
			// Check if we have exactly 30 chars after V+version
			if len(symd[i+3:]) == 30 {
				vIndex = i
				hasVersion = true
				break
			}
		}
	}

	// If not found, try to find 'V' without version
	if vIndex == -1 {
		for i := 6; i < len(symd)-30 && i < 12; i++ {
			if symd[i] == 'V' {
				// Check if we have exactly 30 chars after V
				if len(symd[i+1:]) == 30 {
					vIndex = i
					hasVersion = false
					break
				}
			}
		}
	}

	if vIndex == -1 {
		return fmt.Errorf("no valid vector type marker 'V' found in SYMD: %s", symd)
	}

	// Extract symbol ID (without 'V' and version for compatibility)
	s.ID = symd[:vIndex]
	s.Type = "V"

	// Store version info in metadata if present
	var vectorData string
	if hasVersion {
		s.Metadata["full_id"] = symd[:vIndex+3]
		s.Metadata["version"] = symd[vIndex+1 : vIndex+3]
		vectorData = symd[vIndex+3:]
	} else {
		s.Metadata["full_id"] = symd[:vIndex+1]
		s.Metadata["version"] = "00" // Default version
		vectorData = symd[vIndex+1:]
	}
	if len(vectorData) < 30 { // Need at least 30 characters for 6 5-digit numbers
		return fmt.Errorf("SYMD vector data too short: %s", vectorData)
	}

	// Parse 5-digit values: pivot_x, pivot_y, bb_width, bb_height, min_x, min_y
	pivotX, err := strconv.ParseFloat(vectorData[0:5], 64)
	if err != nil {
		return fmt.Errorf("failed to parse pivot X from SYMD: %v", err)
	}

	pivotY, err := strconv.ParseFloat(vectorData[5:10], 64)
	if err != nil {
		return fmt.Errorf("failed to parse pivot Y from SYMD: %v", err)
	}

	bbWidth, err := strconv.ParseFloat(vectorData[10:15], 64)
	if err != nil {
		return fmt.Errorf("failed to parse bounding box width from SYMD: %v", err)
	}

	bbHeight, err := strconv.ParseFloat(vectorData[15:20], 64)
	if err != nil {
		return fmt.Errorf("failed to parse bounding box height from SYMD: %v", err)
	}

	minX, err := strconv.ParseFloat(vectorData[20:25], 64)
	if err != nil {
		return fmt.Errorf("failed to parse min X from SYMD: %v", err)
	}

	minY, err := strconv.ParseFloat(vectorData[25:30], 64)
	if err != nil {
		return fmt.Errorf("failed to parse min Y from SYMD: %v", err)
	}

	// Store the parsed geometry data (all in DAI units)
	s.PivotPoint = Point{X: pivotX, Y: pivotY}
	s.BoundingBox = BoundingBox{
		MinX: minX,
		MinY: minY,
		MaxX: minX + bbWidth,
		MaxY: minY + bbHeight,
	}

	s.Metadata = map[string]string{
		"vector_data": vectorData,
		"raw_symd":    symd,
	}

	return nil
}

// ParseSVCT parses the SVCT (Symbol Vector Command Table) field.
// Handles complex HP-GL/DAI commands like SPA, SW1, PM0/PM2, FP, CI, etc.
// Reference: specs/DAI_TO_SVG_RENDERING_SPEC.md section "Vector Command Semantics"
func (s *Symbol) ParseSVCT(svct string) error {
	// Use the HP-GL parser for better compliance with the specification
	// Reference: specs/DAI_TO_SVG_RENDERING_SPEC.md
	return s.ParseSVCTWithDAIVector(svct)
}

// parseMultipleCoordinates parses multiple coordinate pairs like "400,550,1050,750,800,750".
// Used by HP-GL PD commands for multi-point line segments.
// Special case: If there are exactly 3 values (x,y,angle), it's a positioned symbol call.
// Reference: specs/DAI_TO_SVG_RENDERING_SPEC.md section "Geometry Builders"
func parseMultipleCoordinates(coordStr string) ([]Point, error) {
	if coordStr == "" {
		return nil, nil
	}

	parts := strings.Split(coordStr, ",")

	// Special case: 3 values indicate x,y,rotation (for symbol calls)
	if len(parts) == 3 {
		x, err := strconv.ParseFloat(parts[0], 64)
		if err != nil {
			return nil, fmt.Errorf("invalid X coordinate: %s", parts[0])
		}

		y, err := strconv.ParseFloat(parts[1], 64)
		if err != nil {
			return nil, fmt.Errorf("invalid Y coordinate: %s", parts[1])
		}

		// The third value is rotation angle - we store it in metadata for now
		// but return just the x,y point
		// TODO: Handle rotation in symbol calls properly
		return []Point{{X: x, Y: y}}, nil
	}

	// Standard case: pairs of coordinates
	if len(parts)%2 != 0 {
		return nil, fmt.Errorf("invalid coordinate format, odd number of values: %s", coordStr)
	}

	var points []Point
	for i := 0; i < len(parts); i += 2 {
		x, err := strconv.ParseFloat(parts[i], 64)
		if err != nil {
			return nil, fmt.Errorf("invalid X coordinate: %s", parts[i])
		}

		y, err := strconv.ParseFloat(parts[i+1], 64)
		if err != nil {
			return nil, fmt.Errorf("invalid Y coordinate: %s", parts[i+1])
		}

		points = append(points, Point{X: x, Y: y})
	}

	return points, nil
}

// GetPivotPoint returns the actual pivot point from SYMD data.
// Pivot point defines the symbol's registration point for positioning.
// Reference: specs/s52-dai-format.md section "SYMD Record"
func (s *Symbol) GetPivotPoint() Point {
	// If we have a parsed pivot point from SYMD, use it
	if s.PivotPoint.X != 0 || s.PivotPoint.Y != 0 {
		return s.PivotPoint
	}
	// Fall back to center of bounding box for legacy symbols
	return Point{
		X: s.BoundingBox.MinX + s.BoundingBox.Width()/2,
		Y: s.BoundingBox.MinY + s.BoundingBox.Height()/2,
	}
}

// GetScaledBoundingBox returns the bounding box scaled by the given factor
func (s *Symbol) GetScaledBoundingBox(scaleFactor float64) BoundingBox {
	return BoundingBox{
		MinX: s.BoundingBox.MinX * scaleFactor,
		MinY: s.BoundingBox.MinY * scaleFactor,
		MaxX: s.BoundingBox.MaxX * scaleFactor,
		MaxY: s.BoundingBox.MaxY * scaleFactor,
	}
}

// ParseSVCTWithDAIVector parses SVCT using the HP-GL specification-based parser.
// Maintains parser state across multiple SVCT lines for proper polygon handling.
// Reference: specs/DAI_TO_SVG_RENDERING_SPEC.md section "Vector Command Semantics"
func (s *Symbol) ParseSVCTWithDAIVector(svct string) error {
	// Create or get the persistent parser
	var parser *daiVectorParser
	if s.hpglParser == nil {
		parser = newDaiVectorParser()
		s.hpglParser = parser
	} else {
		parser = s.hpglParser.(*daiVectorParser)
	}

	// Parse the commands
	err := parser.ParseCommands(svct)
	if err != nil {
		return err
	}

	// Update symbol's vector commands (parser accumulates them internally)
	s.VectorCommands = parser.GetCommands()

	return nil
}
