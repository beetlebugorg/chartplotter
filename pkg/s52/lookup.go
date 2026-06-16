package s52

import (
	"fmt"
	"sort"
	"strings"
)

// LookupFeature finds the S-52 symbology instructions for a given S-57 feature
//
// geometryType must be "P" (point), "L" (line), or "A" (area)
func (l *Library) LookupFeature(objectClass string, geometryType string, attributes map[string]interface{}, mariner *MarinerSettings) *InstructionSet {
	// Ensure mariner settings exist
	if mariner == nil {
		mariner = DefaultMarinerSettings()
	}

	// Step 1: Gather all LUT entries with matching object class and table name
	var candidates []*LookupTable
	var failsafe *LookupTable

	for _, lupt := range l.lookupTables {
		if lupt.ObjectClass == objectClass {
			// Filter by geometry type
			if lupt.GeometryType != "" && lupt.GeometryType != geometryType {
				continue
			}
			// Filter by table name based on geometry type and mariner settings
			matches := l.matchesTableName(lupt, mariner)
			if !matches {
				continue
			}

			candidates = append(candidates, lupt)
			// Track failsafe entry (empty attributes and has instructions)
			// Prefer entries with valid instructions over malformed ones
			if len(lupt.Attributes) == 0 && len(lupt.Instructions) > 0 {
				if failsafe == nil || len(failsafe.Instructions) == 0 {
					failsafe = lupt
				}
			}
		}
	}

	if len(candidates) == 0 {
		return nil // No LUT entry for this object class
	}

	// Step 2: Find the right instruction string
	var instructionStr string

	if len(candidates) == 1 {
		if len(candidates[0].Instructions) > 0 {
			instructionStr = candidates[0].Instructions[0].RawCommand
		}
	} else {
		// Step 3: Search for FIRST entry where ALL attributes match
		bestMatch := l.findFirstAttributeMatch(candidates, attributes)
		if bestMatch != nil && len(bestMatch.Instructions) > 0 {
			instructionStr = bestMatch.Instructions[0].RawCommand
		} else if failsafe != nil && len(failsafe.Instructions) > 0 {
			// Step 4: Use failsafe entry
			instructionStr = failsafe.Instructions[0].RawCommand
		}
	}

	if instructionStr == "" {
		return nil
	}

	// Parse instructions
	instructions := parseInstructions(instructionStr)

	// If mariner settings provided, expand CS instructions
	if mariner != nil {
		instructions = l.expandCSInstructions(instructions, attributes, mariner)
	}

	// Get the selected entry for metadata
	var selectedEntry *LookupTable
	if len(candidates) == 1 {
		selectedEntry = candidates[0]
	} else {
		bestMatch := l.findFirstAttributeMatch(candidates, attributes)
		if bestMatch != nil {
			selectedEntry = bestMatch
		} else if failsafe != nil {
			selectedEntry = failsafe
		}
	}

	// Return structured instruction set with metadata
	result := &InstructionSet{
		Instructions: instructions,
	}
	if selectedEntry != nil {
		result.DisplayPriority = selectedEntry.DisplayPriority
		result.DisplayCategory = stringToDisplayCategory(selectedEntry.DisplayCategory)
		result.RadarPriority = selectedEntry.RadarOverlay

		// SOUNDG (soundings) are fundamental navigation data
		// Override DAI classification if it's marked as DisplayOther
		// Soundings should default to DisplayStandard like other navigation data
		if objectClass == "SOUNDG" && result.DisplayCategory == DisplayOther {
			result.DisplayCategory = DisplayStandard
		}
	}
	return result
}

// GetLookupEntry returns the full lookup table entry for a feature
// This provides access to display priority, viewing group, etc.
func (l *Library) GetLookupEntry(objectClass string, attributes map[string]interface{}) *LookupEntry {
	var candidates []*LookupTable
	var failsafe *LookupTable

	for _, lupt := range l.lookupTables {
		if lupt.ObjectClass == objectClass {
			candidates = append(candidates, lupt)
			// Select failsafe: FIRST entry with no attributes and valid display category
			// Skip entries with malformed DISC records (empty DisplayCategory)
			if len(lupt.Attributes) == 0 && len(lupt.Instructions) > 0 {
				if failsafe == nil {
					// Take first valid failsafe (has instructions and preferably has DisplayCategory)
					if lupt.DisplayCategory != "" {
						failsafe = lupt
					}
				} else if failsafe.DisplayCategory == "" && lupt.DisplayCategory != "" {
					// Upgrade from invalid to valid DisplayCategory
					failsafe = lupt
				}
			}
		}
	}

	if len(candidates) == 0 {
		return nil
	}

	if len(candidates) == 1 {
		return convertLookupTable(candidates[0])
	}

	bestMatch := l.findFirstAttributeMatch(candidates, attributes)
	if bestMatch != nil {
		return convertLookupTable(bestMatch)
	}

	if failsafe != nil {
		return convertLookupTable(failsafe)
	}

	return nil
}

// findFirstAttributeMatch finds the first LUT entry where ALL attributes match
func (l *Library) findFirstAttributeMatch(candidates []*LookupTable, attributes map[string]interface{}) *LookupTable {
	for _, candidate := range candidates {
		if len(candidate.Attributes) == 0 {
			continue
		}

		if l.matchesAllAttributes(candidate, attributes) {
			return candidate // Return FIRST match per S-52 spec
		}
	}
	return nil
}

// matchesAllAttributes checks if all LUT attribute conditions match the object
func (l *Library) matchesAllAttributes(lupt *LookupTable, attributes map[string]interface{}) bool {
	for _, attrCond := range lupt.Attributes {
		if !l.matchesAttribute(attrCond, attributes) {
			return false
		}
	}
	return true
}

// matchesAttribute checks if a single attribute condition matches
// S-52 Section 10.3.3.2: Three matching forms
func (l *Library) matchesAttribute(attrCond AttributeCondition, attributes map[string]interface{}) bool {
	attrName := attrCond.Attribute
	expectedValue := attrCond.Value

	actualValue, exists := attributes[attrName]

	// Case (ii): "?" matches only unknown/omitted
	if expectedValue == "?" {
		return !exists
	}

	// Case (i): No value - matches any value except unknown
	if expectedValue == "" {
		return exists
	}

	// Case (iii): Specific value(s) required
	if !exists {
		return false
	}

	actualStr := fmt.Sprintf("%v", actualValue)

	// Handle multiple values: "3,1" means both 3 AND 1
	if strings.Contains(expectedValue, ",") {
		expectedValues := strings.Split(expectedValue, ",")
		actualValues := strings.Split(actualStr, ",")

		if len(expectedValues) > len(actualValues) {
			return false
		}

		for i, expected := range expectedValues {
			if actualValues[i] != expected {
				return false
			}
		}
		return true
	}

	// Single value - exact match
	return actualStr == expectedValue
}

// ListObjectClasses returns all object classes that have lookup table entries
func (l *Library) ListObjectClasses() []string {
	objClasses := make(map[string]bool)
	for _, lupt := range l.lookupTables {
		objClasses[lupt.ObjectClass] = true
	}

	result := make([]string, 0, len(objClasses))
	for objClass := range objClasses {
		result = append(result, objClass)
	}

	// Sort for deterministic output
	sort.Strings(result)
	return result
}

// convertLookupTable converts internal LookupTable to public API type
func convertLookupTable(lupt *LookupTable) *LookupEntry {
	if lupt == nil {
		return nil
	}

	entry := &LookupEntry{
		ObjectClass:     lupt.ObjectClass,
		Attributes:      make([]AttributeCondition, len(lupt.Attributes)),
		DisplayPriority: lupt.DisplayPriority, // LUPT field 4 (DPRI)
		RadarPriority:   lupt.RadarOverlay,    // LUPT field 5 (RPRI)
		DisplayCategory: lupt.DisplayCategory, // LUPT field 6 (DISC)
		ViewingGroup:    lupt.ViewingGroup,    // LUPT field 7 (optional)
	}

	// Convert attributes
	for i, attr := range lupt.Attributes {
		entry.Attributes[i] = AttributeCondition{
			Attribute: attr.Attribute,
			Value:     attr.Value,
		}
	}

	// Get instruction
	if len(lupt.Instructions) > 0 {
		entry.Instruction = lupt.Instructions[0].RawCommand
	}

	return entry
}

// parseInstructions parses a compound instruction string into structured objects
// Handles compound instructions like "SY(ACHARE51);AC(CHBRN);TX('foo',1,2,3,'15110',0,0,CHBLK,21)"
// Uses the public ParseInstructions API
func parseInstructions(instruction string) []Instruction {
	// Use the public API
	parsed, err := ParseInstructions(instruction)
	if err != nil {
		// On error, return empty slice (should not happen with valid DAI data)
		return []Instruction{}
	}
	return parsed
}

// expandCSInstructions processes CS (Conditional Symbology) instructions and
// replaces them with concrete rendering instructions based on feature attributes
// and mariner settings.
func (l *Library) expandCSInstructions(instructions []Instruction, attributes map[string]interface{}, mariner *MarinerSettings) []Instruction {
	result := make([]Instruction, 0, len(instructions))

	for _, instr := range instructions {
		// If it's a CS instruction, execute the procedure
		if instr.Type() == InstructionCS {
			csInstr, ok := instr.(*CSInstruction)
			if !ok {
				// Should never happen, but skip if type assertion fails
				continue
			}

			// Execute CS procedure and get replacement instructions
			// Create context from attributes and mariner settings
			csctx := NewCSContext(attributes, "", nil, mariner)
			expanded, err := l.executeCS(csInstr.ProcedureName, csctx)
			if err != nil {
				// On error, skip this instruction
				continue
			}

			// Add all expanded instructions
			result = append(result, expanded...)
		} else {
			// Not a CS instruction, keep as-is
			result = append(result, instr)
		}
	}

	return result
}

// matchesTableName checks if a lookup table entry matches the required table name
// based on geometry type and mariner settings.
// S-52 Section 11.2.2: TNAM field specifies the table set
func (l *Library) matchesTableName(lupt *LookupTable, mariner *MarinerSettings) bool {
	// If no table name specified in entry, accept it (backward compatibility)
	if lupt.TableName == "" {
		return true
	}

	// Filter by geometry type and mariner settings
	switch lupt.GeometryType {
	case "A": // Area
		if mariner.SymbolizedBoundaries {
			return lupt.TableName == "SYMBOLIZED_BOUNDARIES"
		} else {
			return lupt.TableName == "PLAIN_BOUNDARIES"
		}

	case "P": // Point
		if mariner.SimplifiedPoints {
			return lupt.TableName == "SIMPLIFIED"
		} else {
			return lupt.TableName == "PAPER_CHART"
		}

	case "L": // Line
		return lupt.TableName == "LINES"

	default:
		// Unknown geometry type - accept all
		return true
	}
}

// stringToDisplayCategory converts DAI string display category to integer constant
func stringToDisplayCategory(category string) int {
	switch category {
	case DisplayCategoryBase, "DISPLAYBASE":
		return DisplayBase // 6
	case DisplayCategoryStandard, "STANDARD":
		return DisplayStandard // 7
	case DisplayCategoryOther, "OTHER":
		return DisplayOther // 8
	case DisplayCategoryMariners, "MARINERS":
		return DisplayStandard // 7 - Mariners features treated as standard
	default:
		return DisplayStandard // 7 - Empty or unknown defaults to standard
	}
}
