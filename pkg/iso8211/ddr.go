package iso8211

import (
	"bytes"
	"fmt"
)

// parseFieldControls extracts field control information from DDR field area
// IHO S-57 Part 3 Annex A.2.4.1: DDR field area structure (Tables A.5, A.6, A.7)
func (p *Parser) parseFieldControls(directory []*DirectoryEntry, fieldArea []byte) (map[string]*FieldControl, error) {
	fieldControls := make(map[string]*FieldControl)

	// Find the field control field (tag "0000")
	var controlEntry *DirectoryEntry
	for _, entry := range directory {
		if entry.Tag == "0000" {
			controlEntry = entry
			break
		}
	}

	if controlEntry == nil {
		// Field control field is optional in some DDRs
		// Create basic field controls from directory
		for _, entry := range directory {
			if entry.Tag == "0000" || entry.Tag == "0001" {
				continue // Skip control fields
			}
			fieldControls[entry.Tag] = &FieldControl{
				Tag:            entry.Tag,
				DataStructCode: 0, // Default: elementary
				DataTypeCode:   0, // Default: character
				FieldName:      entry.Tag,
				Subfields:      nil,
				FormatControls: "",
			}
		}
		return fieldControls, nil
	}

	// Find the field definitions field (tag "0001")
	var defEntry *DirectoryEntry
	for _, entry := range directory {
		if entry.Tag == "0001" {
			defEntry = entry
			break
		}
	}

	if defEntry == nil {
		return nil, fmt.Errorf("DDR missing field definitions (tag 0001)")
	}

	// Extract field definitions data
	if defEntry.Position < 0 || defEntry.Position >= len(fieldArea) {
		return nil, fmt.Errorf("field definitions position %d out of bounds", defEntry.Position)
	}
	end := defEntry.Position + defEntry.Length
	if end > len(fieldArea) {
		return nil, fmt.Errorf("field definitions end %d exceeds field area size %d", end, len(fieldArea))
	}

	fieldDefData := fieldArea[defEntry.Position:end]
	if len(fieldDefData) > 0 && fieldDefData[len(fieldDefData)-1] == fieldTerminator {
		fieldDefData = fieldDefData[:len(fieldDefData)-1]
	}

	// Parse field definitions
	// Format: tag!fieldName!arrayDescriptor!formatControls
	// Fields separated by unit terminator (0x1F)
	definitions := bytes.Split(fieldDefData, []byte{unitTerminator})

	for _, def := range definitions {
		if len(def) == 0 {
			continue
		}

		// Parse definition parts separated by '!'
		parts := bytes.Split(def, []byte{'!'})
		if len(parts) < 1 {
			continue
		}

		tag := string(parts[0])
		if tag == "0000" {
			continue // Skip field control field (it contains metadata, not data field definitions)
		}

		fc := &FieldControl{
			Tag:            tag,
			DataStructCode: 0, // Will be set if present
			DataTypeCode:   0, // Will be set if present
			FieldName:      tag,
			Subfields:      make([]*SubfieldDef, 0),
			FormatControls: "",
		}

		// Parse field name if present
		if len(parts) > 1 && len(parts[1]) > 0 {
			fc.FieldName = string(parts[1])
		}

		// Parse array descriptor if present
		if len(parts) > 2 && len(parts[2]) > 0 {
			arrayDesc := parts[2]
			if len(arrayDesc) >= 2 {
				fc.DataStructCode = arrayDesc[0] - '0' // ASCII digit to int
				fc.DataTypeCode = arrayDesc[1] - '0'   // ASCII digit to int
			}
		}

		// Parse format controls if present
		if len(parts) > 3 {
			fc.FormatControls = string(parts[3])
			// Parse subfields from format controls
			fc.Subfields = parseSubfields(fc.FormatControls)
		}

		fieldControls[tag] = fc
	}

	// Add field controls for any tags in directory not in definitions
	for _, entry := range directory {
		if entry.Tag == "0000" {
			continue // Skip field control field
		}
		if _, exists := fieldControls[entry.Tag]; !exists {
			fieldControls[entry.Tag] = &FieldControl{
				Tag:            entry.Tag,
				DataStructCode: 0,
				DataTypeCode:   0,
				FieldName:      entry.Tag,
				Subfields:      make([]*SubfieldDef, 0),
				FormatControls: "",
			}
		}
	}

	return fieldControls, nil
}

// parseSubfields parses subfield definitions from format controls
// ISO 8211 format controls: (formatType[(width)][,formatType[(width)]]...)
// Format types: A=ASCII, I=integer, R=real, B=binary, S=bit string, C=complex
//
// Note: Real ENC files typically have empty format controls, so this function
// returns a default subfield in most cases. The full parsing logic is implemented
// for completeness and future file formats that may use it.
func parseSubfields(formatControls string) []*SubfieldDef {
	// For empty or whitespace-only format controls, return default
	if len(formatControls) == 0 || len(bytes.TrimSpace([]byte(formatControls))) == 0 {
		return []*SubfieldDef{{
			Label:      "DATA",
			FormatType: 'A', // ASCII default
			Width:      0,   // Variable width
		}}
	}

	// Parse non-empty format controls
	subfields := make([]*SubfieldDef, 0)
	controls := formatControls

	// Remove leading/trailing parentheses if present
	if controls[0] == '(' {
		controls = controls[1:]
	}
	if len(controls) > 0 && controls[len(controls)-1] == ')' {
		controls = controls[:len(controls)-1]
	}

	// Parse format specifications
	i := 0
	subfieldNum := 0
	for i < len(controls) {
		if i >= len(controls) {
			break
		}

		// Parse format type (single character)
		formatType := controls[i]
		i++

		// Parse optional width in parentheses
		width := 0
		if i < len(controls) && controls[i] == '(' {
			i++ // skip '('
			widthStr := ""
			for i < len(controls) && controls[i] != ')' {
				widthStr += string(controls[i])
				i++
			}
			if i < len(controls) {
				i++ // skip ')'
			}
			if widthStr != "" {
				fmt.Sscanf(widthStr, "%d", &width)
			}
		}

		subfields = append(subfields, &SubfieldDef{
			Label:      fmt.Sprintf("SUB%d", subfieldNum),
			FormatType: formatType,
			Width:      width,
		})
		subfieldNum++

		// Skip comma separator if present
		if i < len(controls) && controls[i] == ',' {
			i++
		}
	}

	// If parsing failed, return default
	if len(subfields) == 0 {
		return []*SubfieldDef{{
			Label:      "DATA",
			FormatType: 'A',
			Width:      0,
		}}
	}

	return subfields
}
