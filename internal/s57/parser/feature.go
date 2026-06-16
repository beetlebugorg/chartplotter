package parser

import (
	"encoding/binary"

	"github.com/beetlebugorg/chartplotter-go/pkg/iso8211"
)

// Feature represents a navigational object extracted from S-57 chart data
// S-57 §2.1 (31Main.pdf p23): Feature objects contain geometric and attribute information
type Feature struct {
	// ID is the unique feature identifier from the FRID record
	ID int64
	// ObjectClass is the S-57 object class code (e.g., "DEPCNT", "DEPARE", "BOYCAR")
	ObjectClass string
	// Geometry is the spatial representation of the feature
	Geometry Geometry
	// Attributes contains feature attributes as key-value pairs
	// Common attributes: DRVAL1 (depth), COLOUR (color), OBJNAM (name)
	Attributes map[string]interface{}
}

// spatialRef represents a feature-to-spatial pointer with orientation
// S-57 §7.6.8 (31Main.pdf p77): FSPT field contains RCID + ORNT + USAG + MASK
type spatialRef struct {
	RCID        int64 // Spatial record ID
	Orientation int   // 1=Forward, 2=Reverse, 255=Null
	Usage       int   // 1=Exterior, 2=Interior, 3=Exterior truncated
	Mask        int   // 1=Mask, 2=Show, 255=Null
}

// featureRecord represents a parsed S-57 feature record
// S-57 §7.6 (31Main.pdf p74): Feature records contain feature identification and attributes
type featureRecord struct {
	ID            int64                  // Feature ID from FOID (for backward compatibility, use FIDN)
	AGEN          uint16                 // Producing agency from FOID
	FIDN          uint32                 // Feature identification number from FOID
	FIDS          uint16                 // Feature identification subdivision from FOID
	ObjectClass   int                    // S-57 object class code (DEPARE=42, etc.)
	GeomPrim      int                    // Geometric primitive from FRID (1=Point, 2=Line, 3=Area, 255=N/A)
	Group         int                    // Group code from FRID
	RecordVersion int                    // RVER - record version number
	UpdateInstr   int                    // RUIN - update instruction
	Attributes    map[string]interface{} // Feature attributes
	SpatialRefs   []spatialRef           // References to spatial records (from FSPT) with orientation
}

// parseFeatureRecord extracts feature data from an ISO 8211 record
// Returns nil if record is not a feature record
// S-57 §7.6.1 (31Main.pdf p74): Feature records identified by FRID field
func parseFeatureRecord(record *iso8211.DataRecord) *featureRecord {
	// Check if this is a feature record (has FRID field)
	fridData, hasFRID := record.Fields["FRID"]
	if !hasFRID || len(fridData) < 12 {
		return nil // Need 12 bytes: RCNM(1) + RCID(4) + PRIM(1) + GRUP(1) + OBJL(2) + RVER(2) + RUIN(1)
	}

	// Parse FRID (Feature Record Identifier) per S-57 §7.6.1 (31Main.pdf p74)
	// Binary structure (12 bytes total):
	//   Byte 0: RCNM (1 byte) - Record name, value 100 = feature record
	//   Bytes 1-4: RCID (4 bytes) - Record identification number (uint32 LE)
	//   Byte 5: PRIM (1 byte) - Object geometric primitive (1=Point, 2=Line, 3=Area, 255=N/A)
	//   Byte 6: GRUP (1 byte) - Group code (1-254, 255=no group)
	//   Bytes 7-8: OBJL (2 bytes) - Object label/code (uint16 LE)
	//   Bytes 9-10: RVER (2 bytes) - Record version (uint16 LE)
	//   Byte 11: RUIN (1 byte) - Record update instruction

	rcnm := fridData[0]
	if rcnm != 100 {
		return nil // Not a feature record
	}

	featureRec := &featureRecord{
		Attributes:  make(map[string]interface{}),
		SpatialRefs: make([]spatialRef, 0),
	}

	// Extract RCID (not used currently, but available)
	// featureRec.RecordID = int64(binary.LittleEndian.Uint32(fridData[1:5]))

	// Extract PRIM (Object Geometric Primitive) from FRID byte [5]
	featureRec.GeomPrim = int(fridData[5])

	// Extract GRUP (Group) from FRID byte [6]
	featureRec.Group = int(fridData[6])

	// Extract OBJL (Object Label/Class) from FRID bytes [7:9]
	featureRec.ObjectClass = int(binary.LittleEndian.Uint16(fridData[7:9]))

	// Extract RVER (Record Version) from FRID bytes [9:11]
	featureRec.RecordVersion = int(binary.LittleEndian.Uint16(fridData[9:11]))

	// Extract RUIN (Record Update Instruction) from FRID byte [11]
	featureRec.UpdateInstr = int(fridData[11])

	// Parse FOID (Feature Object Identifier) for feature ID
	// S-57 §7.6.2 (31Main.pdf p75): FOID structure is AGEN (2 bytes) + FIDN (4 bytes) + FIDS (2 bytes)
	if foidData, ok := record.Fields["FOID"]; ok && len(foidData) >= 8 {
		// AGEN (2 bytes at offset 0) - Producing agency code
		featureRec.AGEN = binary.LittleEndian.Uint16(foidData[0:2])

		// FIDN (4 bytes at offset 2) - Feature identification number
		featureRec.FIDN = binary.LittleEndian.Uint32(foidData[2:6])

		// FIDS (2 bytes at offset 6) - Feature identification subdivision
		featureRec.FIDS = binary.LittleEndian.Uint16(foidData[6:8])

		// Set ID for backward compatibility (use FIDN)
		featureRec.ID = int64(featureRec.FIDN)
	}

	// Parse ATTF (Feature Record Attribute) for attributes
	if attfData, ok := record.Fields["ATTF"]; ok {
		featureRec.Attributes = parseAttributes(attfData)
	}

	// Parse FSPT (Feature to Spatial Pointer) for spatial references
	if fsptData, ok := record.Fields["FSPT"]; ok {
		featureRec.SpatialRefs = parseSpatialPointers(fsptData)
	}

	return featureRec
}

// parseAttributes extracts attributes from ATTF field
// S-57 Appendix B.1: ATTF contains repeated attribute structures
func parseAttributes(data []byte) map[string]interface{} {
	attributes := make(map[string]interface{})

	// ATTF structure: repeated [ATTL(2 bytes), ATVL(variable)]
	// This is a simplified parser - real implementation needs subfield parsing
	offset := 0
	for offset+2 <= len(data) {
		// Extract attribute code (2 bytes)
		attrCode := binary.LittleEndian.Uint16(data[offset : offset+2])
		offset += 2

		// Find attribute value (terminated by unit separator 0x1F or end)
		valueEnd := offset
		for valueEnd < len(data) && data[valueEnd] != 0x1F {
			valueEnd++
		}

		if valueEnd > offset {
			// Convert attribute code to name using attribute catalogue
			attrName := AttributeCodeToString(int(attrCode))
			attributes[attrName] = string(data[offset:valueEnd])
		}

		offset = valueEnd + 1 // Skip unit separator
	}

	return attributes
}

// parseSpatialPointers extracts spatial record references from FSPT field
// S-57 §7.6.8 (31Main.pdf p77): FSPT contains pointers to VRID records - 8 bytes per pointer
func parseSpatialPointers(data []byte) []spatialRef {
	refs := make([]spatialRef, 0)

	// FSPT is repeating group per S-57 §7.6.8 (31Main.pdf p77), each entry is 8 bytes:
	// NAME: B(40) - 5 bytes (RCNM=1, RCID=4)
	//   Offset 0: NAME_RCNM (1 byte) - Target record type
	//   Offset 1-4: NAME_RCID (4 bytes) - Target record ID (uint32 LE)
	// Offset 5: ORNT (1 byte) - Orientation (1=Forward, 2=Reverse, 255=Null)
	// Offset 6: USAG (1 byte) - Usage indicator (1=Exterior, 2=Interior, 3=Exterior truncated)
	// Offset 7: MASK (1 byte) - Masking indicator (1=Mask, 2=Show, 255=Null)

	// Binary mode: fixed 8-byte stride (not ASCII with separators)
	for i := 0; i+7 < len(data); i += 8 {
		// Extract NAME_RCID - this is the spatial record ID
		rcid := int64(binary.LittleEndian.Uint32(data[i+1 : i+5]))

		// Extract ORNT, USAG, MASK per S-57 §4.7.3.2 (31Main.pdf p51)
		orientation := int(data[i+5])
		usage := int(data[i+6])
		mask := int(data[i+7])

		refs = append(refs, spatialRef{
			RCID:        rcid,
			Orientation: orientation,
			Usage:       usage,
			Mask:        mask,
		})
	}

	return refs
}
