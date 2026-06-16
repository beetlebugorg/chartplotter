package iso8211

import (
	"fmt"
	"io"
)

const (
	// ISO 8211 field terminator (0x1E = ASCII RS - Record Separator)
	fieldTerminator = 0x1E
	// ISO 8211 unit terminator (0x1F = ASCII US - Unit Separator)
	unitTerminator = 0x1F
)

// parseDirectory parses the directory section of a record
// IHO S-57 Part 3 Annex A.2.3: Directory structure and entry format
// Directory entries are variable-length, terminated by field terminator (0x1E)
func (p *Parser) parseDirectory(leader *Leader) ([]*DirectoryEntry, error) {
	directory := make([]*DirectoryEntry, 0)

	// Calculate entry size from leader
	entrySize := leader.SizeOfFieldTag + leader.SizeOfFieldLength + leader.SizeOfFieldPosition

	// Calculate directory size (from after leader to field area start, minus 1 for terminator)
	directoryStart := 24                                        // Leader is always 24 bytes
	directorySize := leader.FieldAreaStart - directoryStart - 1 // -1 for field terminator

	// Read entire directory area
	dirBuf := make([]byte, directorySize+1) // +1 to include terminator
	n, err := io.ReadFull(p.getReader(), dirBuf)
	if err != nil {
		return nil, err
	}
	if n != directorySize+1 {
		return nil, fmt.Errorf("expected %d bytes for directory, got %d", directorySize+1, n)
	}
	p.offset += int64(n)

	// Verify field terminator
	if dirBuf[directorySize] != fieldTerminator {
		return nil, fmt.Errorf("expected field terminator (0x1E) at end of directory, got 0x%02X", dirBuf[directorySize])
	}

	// Parse directory entries
	numEntries := directorySize / entrySize
	for i := 0; i < numEntries; i++ {
		offset := i * entrySize
		entryBuf := dirBuf[offset : offset+entrySize]

		// Parse tag
		tag := string(entryBuf[0:leader.SizeOfFieldTag])

		// Parse length
		lengthBuf := entryBuf[leader.SizeOfFieldTag : leader.SizeOfFieldTag+leader.SizeOfFieldLength]
		length, err := parseASCIIInt(lengthBuf)
		if err != nil {
			return nil, fmt.Errorf("failed to parse field length for tag %s: %w", tag, err)
		}

		// Parse position
		positionBuf := entryBuf[leader.SizeOfFieldTag+leader.SizeOfFieldLength : entrySize]
		position, err := parseASCIIInt(positionBuf)
		if err != nil {
			return nil, fmt.Errorf("failed to parse field position for tag %s: %w", tag, err)
		}

		entry := &DirectoryEntry{
			Tag:      tag,
			Length:   length,
			Position: position,
		}

		directory = append(directory, entry)
	}

	return directory, nil
}

// extractFields extracts field data from the field area using directory entries
func (p *Parser) extractFields(directory []*DirectoryEntry, fieldArea []byte) (map[string][]byte, error) {
	fields := make(map[string][]byte)

	for _, entry := range directory {
		// Validate position and length
		if entry.Position < 0 || entry.Position >= len(fieldArea) {
			return nil, fmt.Errorf("field %s: position %d out of bounds (field area size: %d)", entry.Tag, entry.Position, len(fieldArea))
		}
		end := entry.Position + entry.Length
		if end > len(fieldArea) {
			return nil, fmt.Errorf("field %s: end position %d exceeds field area size %d", entry.Tag, end, len(fieldArea))
		}

		// Extract field data (including terminator)
		fieldData := fieldArea[entry.Position:end]

		// Verify and remove field terminator if present
		if len(fieldData) > 0 && fieldData[len(fieldData)-1] == fieldTerminator {
			fieldData = fieldData[:len(fieldData)-1]
		}

		fields[entry.Tag] = fieldData
	}

	return fields, nil
}
