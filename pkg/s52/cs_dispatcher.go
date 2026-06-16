package s52

import (
	"fmt"
)

// ExecuteCS executes a CS (Conditional Symbology) procedure with the given context.
//
// Parameters:
//   - procedureName: Name of the CS procedure to execute (e.g., "DEPARE03", "WRECKS05")
//   - ctx: Execution context containing attributes, geometry type, spatial data, and mariner settings
//
// The context-based API simplifies calls and provides type-safe attribute access.
// When ctx.Spatial is nil, procedures use simplified logic that doesn't require spatial topology.
//
// This is an internal method - use Engine.ExecuteCS for rendering, or ExecuteCSToString for inspection.
func (l *Library) ExecuteCS(procedureName string, ctx *CSContext) ([]Instruction, error) {
	return l.executeCS(procedureName, ctx)
}

// ExecuteCSToString executes a CS procedure and returns the formatted instruction string.
// This is for debugging, inspection, and non-rendering use cases (e.g., WASM API).
// For rendering, use Engine.ExecuteCS instead.
func (l *Library) ExecuteCSToString(procedureName string, ctx *CSContext) (string, error) {
	instructions, err := l.executeCS(procedureName, ctx)
	if err != nil {
		return "", err
	}

	// Combine all instructions into a single string (semicolon-separated)
	if len(instructions) == 0 {
		return "", nil
	}

	result := ""
	for i, instr := range instructions {
		if i > 0 {
			result += ";"
		}
		result += instr.String()
	}

	return result, nil
}

// CS procedure dispatcher function type
type csProcedureFunc func(*Library, *CSContext) ([]Instruction, error)

// CS procedure dispatch map
var csProcedures = map[string]csProcedureFunc{
	"DEPARE03": func(l *Library, ctx *CSContext) ([]Instruction, error) { return NewDEPARE03(ctx, l).Execute() },
	"DEPCNT03": func(l *Library, ctx *CSContext) ([]Instruction, error) { return NewDEPCNT03(ctx, l).Execute() },
	"LIGHTS06": func(l *Library, ctx *CSContext) ([]Instruction, error) { return NewLIGHTS06(ctx, l).Execute() },
	"OBSTRN07": func(l *Library, ctx *CSContext) ([]Instruction, error) { return NewOBSTRN07(ctx, l).Execute() },
	"SOUNDG03": func(l *Library, ctx *CSContext) ([]Instruction, error) { return NewSOUNDG03(ctx, l).Execute() },
	"QUAPOS01": func(l *Library, ctx *CSContext) ([]Instruction, error) { return NewQUAPOS01(ctx).Execute() },
	"RESTRN01": func(l *Library, ctx *CSContext) ([]Instruction, error) { return NewRESTRN01(ctx, l).Execute() },
	"SLCONS04": func(l *Library, ctx *CSContext) ([]Instruction, error) { return NewSLCONS04(ctx, l).Execute() },
	"TOPMAR01": func(l *Library, ctx *CSContext) ([]Instruction, error) { return NewTOPMAR01(ctx, l).Execute() },
	"WRECKS05": func(l *Library, ctx *CSContext) ([]Instruction, error) { return NewWRECKS05(ctx, l).Execute() },
	"RESARE04": func(l *Library, ctx *CSContext) ([]Instruction, error) { return NewRESARE04(ctx, l).Execute() },
	"SYMINS02": func(l *Library, ctx *CSContext) ([]Instruction, error) { return NewSYMINS02(ctx, l).Execute() },
	"UNSARE01": func(l *Library, ctx *CSContext) ([]Instruction, error) { return NewUNSARE01(ctx, l).Execute() },
}

// executeCS dispatches CS (Conditional Symbology) procedure execution based on
// procedure name. Returns expanded instructions or an error if the procedure
// is unknown or execution fails.
//
// S-52 Section 13: Conditional Symbology Procedures
//
// Supported procedures:
//   - DEPARE03 - Depth Areas
//   - DEPCNT03 - Depth Contours
//   - LIGHTS06 - Navigation Lights
//   - OBSTRN07 - Obstructions
//   - SOUNDG03 - Soundings
//   - QUAPOS01 - Position Quality
//   - RESTRN01 - Restrictions
//   - SLCONS04 - Shoreline Construction
//   - SYMINS02 - Symbol Installation (NEWOBJ)
//   - TOPMAR01 - Top Marks
//   - UNSARE01 - Unsurveyed Areas
//   - WRECKS05 - Wrecks
//   - RESARE04 - Restricted Areas
//
// Sub-procedures (called by main procedures):
//   - SEABED01 - Depth Color Determination
//   - SAFCON01 - Safety Contour Labels
//   - RESCSP02 - Restriction Sub-Procedure
//   - SNDFRM04 - Sounding Format
//   - DEPVAL02 - Depth Value Determination
//   - UDWHAZ05 - Underwater Hazard
//   - QUAPNT02 - Quality of Point
//   - QUALIN01 - Quality of Line
func (l *Library) executeCS(procedureName string, ctx *CSContext) ([]Instruction, error) {
	// Ensure context exists
	if ctx == nil {
		ctx = NewCSContext(nil, "", nil, nil)
	}
	// Ensure mariner settings exist
	if ctx.Mariner == nil {
		ctx.Mariner = DefaultMarinerSettings()
	}

	// Dispatch to appropriate CS procedure implementation
	procFunc, ok := csProcedures[procedureName]
	if !ok {
		return nil, fmt.Errorf("unknown CS procedure: %s", procedureName)
	}
	return procFunc(l, ctx)
}

// Helper functions for type conversion and string manipulation

func getFloatValue(val interface{}) float64 {
	switch v := val.(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case string:
		// Try to parse string to float
		if f, err := parseFloat(v); err == nil {
			return f
		}
		return 0.0
	default:
		return 0.0
	}
}

func getIntValue(val interface{}) int {
	switch v := val.(type) {
	case int:
		return v
	case float64:
		return int(v)
	case string:
		return stringToInt(v)
	default:
		return 0
	}
}

// parseFloat parses a string to float64
func parseFloat(s string) (float64, error) {
	// Simple float parsing - handle common cases
	s = trimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty string")
	}

	// Handle negative numbers
	negative := false
	if s[0] == '-' {
		negative = true
		s = s[1:]
	}

	// Split on decimal point
	parts := splitString(s, ".")
	if len(parts) > 2 {
		return 0, fmt.Errorf("invalid float: multiple decimal points")
	}

	// Parse integer part
	var result float64
	if len(parts[0]) > 0 {
		intPart := stringToInt(parts[0])
		result = float64(intPart)
	}

	// Parse decimal part if present
	if len(parts) == 2 && len(parts[1]) > 0 {
		decPart := stringToInt(parts[1])
		divisor := 1.0
		for i := 0; i < len(parts[1]); i++ {
			divisor *= 10.0
		}
		result += float64(decPart) / divisor
	}

	if negative {
		result = -result
	}

	return result, nil
}

// formatDepthValue formats a depth value for display
func formatDepthValue(depth float64) string {
	// Format as whole number for contours and deeper soundings
	return fmt.Sprintf("%.0f", depth)
}

// stringToInt converts a string to int
func stringToInt(s string) int {
	var result int
	fmt.Sscanf(s, "%d", &result)
	return result
}

// intToString converts an int to string
func intToString(i int) string {
	return fmt.Sprintf("%d", i)
}

// splitAndTrim splits a string by delimiter and trims whitespace
func splitAndTrim(s, delim string) []string {
	var result []string
	for _, part := range splitString(s, delim) {
		trimmed := trimSpace(part)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

// splitString splits a string by delimiter (simple implementation)
func splitString(s, delim string) []string {
	if s == "" {
		return []string{}
	}
	var result []string
	start := 0
	for i := 0; i < len(s); i++ {
		if i+len(delim) <= len(s) && s[i:i+len(delim)] == delim {
			result = append(result, s[start:i])
			start = i + len(delim)
			i += len(delim) - 1
		}
	}
	result = append(result, s[start:])
	return result
}

// trimSpace trims leading and trailing whitespace
func trimSpace(s string) string {
	start := 0
	end := len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t' || s[start] == '\n' || s[start] == '\r') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\n' || s[end-1] == '\r') {
		end--
	}
	return s[start:end]
}

// trimString trims specified characters from both ends of string
func trimString(s, cutset string) string {
	start := 0
	end := len(s)

	// Trim from start
	for start < end {
		found := false
		for i := 0; i < len(cutset); i++ {
			if s[start] == cutset[i] {
				found = true
				break
			}
		}
		if !found {
			break
		}
		start++
	}

	// Trim from end
	for end > start {
		found := false
		for i := 0; i < len(cutset); i++ {
			if s[end-1] == cutset[i] {
				found = true
				break
			}
		}
		if !found {
			break
		}
		end--
	}

	return s[start:end]
}

// replaceAll replaces all occurrences of old with new in string s
func replaceAll(s, old, new string) string {
	if old == "" {
		return s
	}
	result := ""
	for len(s) > 0 {
		idx := indexString(s, old)
		if idx < 0 {
			result += s
			break
		}
		result += s[:idx] + new
		s = s[idx+len(old):]
	}
	return result
}

// indexString returns the index of the first occurrence of substr in s, or -1
func indexString(s, substr string) int {
	if len(substr) == 0 {
		return 0
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		match := true
		for j := 0; j < len(substr); j++ {
			if s[i+j] != substr[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

// hasFraction returns true if the depth has a fractional part
func hasFraction(depth float64) bool {
	return depth != float64(int(depth))
}
