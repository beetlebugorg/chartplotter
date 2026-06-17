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
	if mariner == nil {
		mariner = DefaultMarinerSettings()
	}

	instructionStr, selectedEntry := l.selectInstruction(objectClass, geometryType, attributes, mariner)
	if instructionStr == "" {
		return nil
	}

	// Parse instructions, then expand CS procedures (one-shot, attribute-only
	// context). The portrayal walk instead uses LookupFeatureRaw + its own
	// recursive CS dispatch; this path stays for callers that want the
	// flattened instruction list.
	instructions := l.expandCSInstructions(parseInstructions(instructionStr), attributes, mariner)
	return l.makeInstructionSet(objectClass, instructions, selectedEntry)
}

// selectInstruction runs the S-52 LUPT selection (object class + geometry +
// table-name/mariner filter, first-all-attributes-match, then failsafe) and
// returns the chosen raw instruction string and the entry it came from. Shared
// by LookupFeature and LookupFeatureRaw so both select identically.
func (l *Library) selectInstruction(objectClass, geometryType string, attributes map[string]interface{}, mariner *MarinerSettings) (string, *LookupTable) {
	var candidates []*LookupTable
	var failsafe *LookupTable

	for _, lupt := range l.lookupTables {
		if lupt.ObjectClass != objectClass {
			continue
		}
		if lupt.GeometryType != "" && lupt.GeometryType != geometryType {
			continue
		}
		if !l.matchesTableName(lupt, mariner) {
			continue
		}
		candidates = append(candidates, lupt)
		if len(lupt.Attributes) == 0 && len(lupt.Instructions) > 0 {
			if failsafe == nil || len(failsafe.Instructions) == 0 {
				failsafe = lupt
			}
		}
	}

	if len(candidates) == 0 {
		return "", nil
	}

	if len(candidates) == 1 {
		entry := candidates[0]
		if len(entry.Instructions) > 0 {
			return entry.Instructions[0].RawCommand, entry
		}
		return "", entry
	}

	bestMatch := l.findFirstAttributeMatch(candidates, attributes)
	if bestMatch != nil && len(bestMatch.Instructions) > 0 {
		return bestMatch.Instructions[0].RawCommand, bestMatch
	}
	if failsafe != nil && len(failsafe.Instructions) > 0 {
		return failsafe.Instructions[0].RawCommand, failsafe
	}
	if bestMatch != nil {
		return "", bestMatch
	}
	return "", failsafe
}

// makeInstructionSet attaches the LUPT metadata (display priority/category) to a
// parsed instruction list, applying the SOUNDG display-category override.
func (l *Library) makeInstructionSet(objectClass string, instructions []Instruction, selectedEntry *LookupTable) *InstructionSet {
	result := &InstructionSet{Instructions: instructions}
	if selectedEntry != nil {
		result.DisplayPriority = selectedEntry.DisplayPriority
		result.DisplayCategory = stringToDisplayCategory(selectedEntry.DisplayCategory)
		result.RadarPriority = selectedEntry.RadarOverlay

		// SOUNDG are fundamental navigation data; promote a DisplayOther
		// classification to DisplayStandard like other navigation data.
		if objectClass == "SOUNDG" && result.DisplayCategory == DisplayOther {
			result.DisplayCategory = DisplayStandard
		}
	}
	return result
}

// LookupFeatureRaw is like LookupFeature but returns the matched LUPT
// instructions WITHOUT expanding CS procedures (CS stays as *CSInstruction).
// The portrayal walk uses this so it can run the S-52 instruction walk itself —
// recursive CS dispatch and per-sounding-point expansion. Returns nil if no
// LUPT entry matches.
func (l *Library) LookupFeatureRaw(objectClass, geometryType string, attributes map[string]interface{}, mariner *MarinerSettings) *InstructionSet {
	if mariner == nil {
		mariner = DefaultMarinerSettings()
	}
	instructionStr, selectedEntry := l.selectInstruction(objectClass, geometryType, attributes, mariner)
	if instructionStr == "" {
		return nil
	}
	return l.makeInstructionSet(objectClass, parseInstructions(instructionStr), selectedEntry)
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
