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

// ParseSYMD parses the SYMD field which contains symbol metadata and geometry.
// Format: SYMBOLIDV[PIVX][PIVY][BBW][BBH][MINX][MINY]
// Example: BCNGEN01V007500075000300005150060000300
// Where each bracketed value is a 5-digit number in DAI units (1/100 mm)
// Reference: specs/s52-dai-format.md section "SYMD Record"
func (s *Symbol) ParseSYMD(symd string) error {
	// S-52 PresLib §11.6.3 SYMD field — FIXED layout (no version field):
	//   [0:8)   SYNM symbol name
	//   [8]     SYDF graphic type 'V' (vector) / 'R' (raster — skipped)
	//   [9:14)  SYCL pivot column      [14:19) SYRW pivot row
	//   [19:24) SYHL bbox width        [24:29) SYVL bbox height
	//   [29:34) SBXC bbox upper-left col   [34:39) SBXR bbox upper-left row
	// Any trailing bytes beyond 39 are ignored (some records carry extras).
	// This fixed-offset layout is authoritative. The old
	// "optional 2-digit version" heuristic mis-fired on records whose data
	// section ran 32 chars, stripping the first two pivot digits.
	if len(symd) < 39 {
		return fmt.Errorf("SYMD too short: %s", symd)
	}
	if symd[8] != 'V' {
		return fmt.Errorf("SYMD not a vector symbol (graphic type %q): %s", symd[8:9], symd)
	}

	s.ID = strings.TrimSpace(symd[0:8])
	s.Type = "V"

	// parse5 reads a fixed 5-char field: it is parsed as an
	// unsigned integer; anything that fails (incl. a literal negative like the
	// "-2146" some records carry in the pivot field) becomes 0.
	parse5 := func(lo, hi int) float64 {
		v, err := strconv.ParseUint(strings.TrimSpace(symd[lo:hi]), 10, 16)
		if err != nil {
			return 0
		}
		return float64(v)
	}
	pivotX := parse5(9, 14)
	pivotY := parse5(14, 19)
	bbWidth := parse5(19, 24)
	bbHeight := parse5(24, 29)
	minX := parse5(29, 34)
	minY := parse5(34, 39)

	// Store the parsed geometry data (all in DAI units)
	s.PivotPoint = Point{X: pivotX, Y: pivotY}
	s.BoundingBox = BoundingBox{
		MinX: minX,
		MinY: minY,
		MaxX: minX + bbWidth,
		MaxY: minY + bbHeight,
	}

	if s.Metadata == nil {
		s.Metadata = make(map[string]string)
	}
	s.Metadata["full_id"] = symd[0:9]
	s.Metadata["version"] = "00"
	s.Metadata["raw_symd"] = symd

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
