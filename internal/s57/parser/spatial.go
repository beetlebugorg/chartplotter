package parser

import (
	"encoding/binary"
	"strconv"
	"strings"

	"github.com/beetlebugorg/chartplotter/pkg/iso8211"
)

// vectorPointer represents a pointer to another spatial record
// S-57 §7.7.1.4 (31Main.pdf p79): Vector Record Pointer (VRPT) - 9 bytes per pointer
type vectorPointer struct {
	TargetRCNM  int   // Record type (110=node, 120=connected node, 130=edge, 140=face)
	TargetRCID  int64 // Record ID of target
	Orientation int   // 1=forward, 2=reverse, 255=null
	Usage       int   // 1=Exterior, 2=Interior, 3=Exterior boundary truncated
	Topology    int   // Topology indicator (1=begin, 2=end, 3=left, 4=right, 255=null)
	Mask        int   // Masking indicator (1=mask, 2=show, 255=null)
}

// spatialRecord represents a parsed S-57 spatial (vector) record
// S-57 §2.1 (31Main.pdf p23): Spatial records contain coordinate data
type spatialRecord struct {
	ID             int64           // Spatial record ID from VRID
	RecordType     spatialType     // Node, Edge, etc.
	Coordinates    [][]float64     // Variable-length coordinates [lon, lat] or [lon, lat, depth]
	VectorPointers []vectorPointer // VRPT pointers to other spatial records
	RecordVersion  int             // RVER - record version number
	UpdateInstr    int             // RUIN - update instruction
	Quapos         int             // QUAPOS quality of position (S-57 spatial-level ATTV); 0 if absent
}

// spatialType represents the type of spatial record
type spatialType int

const (
	// S-57 Appendix B.1: RCNM values for spatial records
	spatialTypeIsolatedNode  spatialType = 110 // VI - Isolated Node
	spatialTypeConnectedNode spatialType = 120 // VC - Connected Node
	spatialTypeEdge          spatialType = 130 // VE - Edge
	spatialTypeFace          spatialType = 140 // VF - Face
)

// parseSpatialRecordWithParams extracts spatial data from an ISO 8211 record with dataset params
// Returns nil if record is not a spatial record
// S-57 §7.7.1.1 (31Main.pdf p78): Spatial records identified by VRID field
func parseSpatialRecordWithParams(record *iso8211.DataRecord, params datasetParams) *spatialRecord {
	return parseSpatialRecordInternal(record, params.COMF, params.SOMF)
}

// parseSpatialRecordInternal is the internal implementation
func parseSpatialRecordInternal(record *iso8211.DataRecord, comf int32, somf int32) *spatialRecord {
	// Check if this is a spatial record (has VRID field)
	vridData, hasVRID := record.Fields["VRID"]
	if !hasVRID || len(vridData) < 8 {
		return nil // Need 8 bytes: RCNM(1) + RCID(4) + RVER(2) + RUIN(1)
	}

	// Parse VRID (Vector Record Identifier) per S-57 §7.7.1.1 (31Main.pdf p78)
	// Binary structure (8 bytes total):
	//   Byte 0: RCNM (Record Name) - 110=IsolatedNode, 120=ConnectedNode, 130=Edge, 140=Face
	//   Bytes 1-4: RCID (Record ID) - uint32 little-endian
	//   Bytes 5-6: RVER (Record Version) - uint16 little-endian
	//   Byte 7: RUIN (Record Update Instruction)

	rcnm := vridData[0]
	spatialRec := &spatialRecord{
		RecordType:     spatialType(rcnm),
		Coordinates:    make([][]float64, 0),
		VectorPointers: make([]vectorPointer, 0),
	}

	// Extract spatial record ID (4 bytes at offset 1)
	spatialRec.ID = int64(binary.LittleEndian.Uint32(vridData[1:5]))

	// Extract record version (2 bytes at offset 5)
	spatialRec.RecordVersion = int(binary.LittleEndian.Uint16(vridData[5:7]))

	// Extract update instruction (1 byte at offset 7)
	spatialRec.UpdateInstr = int(vridData[7])

	// Parse SG2D (2D Coordinate) field for coordinates
	// Coordinates are scaled by COMF parameter
	if sg2dData, ok := record.Fields["SG2D"]; ok {
		spatialRec.Coordinates = parseCoordinates2D(sg2dData, comf)
	}

	// Parse SG3D (3D Coordinate) field if present - includes depth (Z)
	// X/Y scaled by COMF, Z/depth scaled by SOMF
	if sg3dData, ok := record.Fields["SG3D"]; ok {
		spatialRec.Coordinates = parseCoordinates3D(sg3dData, comf, somf)
	}

	// Parse VRPT (Vector Record Pointer) - S-57 §7.7.1.4 (31Main.pdf p79)
	if vrptData, ok := record.Fields["VRPT"]; ok {
		spatialRec.VectorPointers = parseVectorPointers(vrptData)
	}

	// Parse ATTV (spatial-level attributes) — QUAPOS lives here, not on the
	// feature. Same ATTL+ATVL layout as a feature's ATTF, so reuse parseAttributes.
	if attvData, ok := record.Fields["ATTV"]; ok {
		if q, ok := atoiAttr(parseAttributes(attvData)["QUAPOS"]); ok {
			spatialRec.Quapos = q
		}
	}

	return spatialRec
}

// atoiAttr reads an S-57 attribute value (parsed as a string) as an int.
func atoiAttr(v interface{}) (int, bool) {
	s, ok := v.(string)
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0, false
	}
	return n, true
}

// parseCoordinates2D extracts 2D coordinates from SG2D field
// S-57 §7.7.1.6 (31Main.pdf p80): SG2D contains repeated coordinate pairs
// Coordinates are stored as signed integers (b24 = int32) that need scaling by COMF
func parseCoordinates2D(data []byte, comf int32) [][]float64 {
	coords := make([][]float64, 0)

	// SG2D structure per S-57 §7.7.1.6 (31Main.pdf p80): [YCOO(4 bytes), XCOO(4 bytes)]
	// Each coordinate pair is 8 bytes (2 * int32 signed)
	// First 4 bytes = Y (latitude), Second 4 bytes = X (longitude)
	offset := 0
	for offset+8 <= len(data) {
		// Parse first 4 bytes - Y (latitude) per S-57 spec
		y := int32(binary.LittleEndian.Uint32(data[offset : offset+4]))
		offset += 4

		// Parse second 4 bytes - X (longitude) per S-57 spec
		x := int32(binary.LittleEndian.Uint32(data[offset : offset+4]))
		offset += 4

		// Convert to float64 and scale by COMF
		// Per IHO S-57 §7.3.2.1 (31Main.pdf p66): Coordinates are scaled by DSPM_COMF (Coordinate Multiplication Factor)
		// COMF is typically 10000000 (10^7) for coordinates in units of 0.00000001 degrees
		lon := convertCoordinate(x, comf)
		lat := convertCoordinate(y, comf)

		// Store in GeoJSON order: [longitude, latitude]
		coords = append(coords, []float64{lon, lat})
	}

	return coords
}

// parseCoordinates3D extracts 3D coordinates from SG3D field including depth (Z)
// S-57 §7.7.1.7 (31Main.pdf p80): SG3D contains repeated 3D coordinate triples (soundings)
func parseCoordinates3D(data []byte, comf int32, somf int32) [][]float64 {
	coords := make([][]float64, 0)

	// SG3D structure per S-57 §7.7.1.7 (31Main.pdf p80): [YCOO(4 bytes), XCOO(4 bytes), VE3D(4 bytes)]
	// Each coordinate triple is 12 bytes (3 * int32 signed)
	// YCOO/XCOO scaled by COMF, VE3D scaled by SOMF
	// First 4 bytes = Y (latitude), Second 4 bytes = X (longitude), Third 4 bytes = Z (depth)
	offset := 0
	for offset+12 <= len(data) {
		// Parse first 4 bytes - Y (latitude) per S-57 spec
		y := int32(binary.LittleEndian.Uint32(data[offset : offset+4]))
		offset += 4

		// Parse second 4 bytes - X (longitude) per S-57 spec
		x := int32(binary.LittleEndian.Uint32(data[offset : offset+4]))
		offset += 4

		// Parse VE3D (depth/sounding) - 4 bytes signed int32
		// Scale by SOMF (Sounding Multiplication Factor)
		z := int32(binary.LittleEndian.Uint32(data[offset : offset+4]))
		offset += 4

		// Convert to float64 and scale: X/Y by COMF, Z by SOMF
		lon := convertCoordinate(x, comf)
		lat := convertCoordinate(y, comf)
		depth := convertCoordinate(z, somf)

		// Store in 3D format: [longitude, latitude, depth]
		coords = append(coords, []float64{lon, lat, depth})
	}

	return coords
}

// parseVectorPointers extracts vector record pointers from VRPT field
// S-57 §7.7.1.4 (31Main.pdf p79): VRPT structure - 9 bytes per pointer
func parseVectorPointers(data []byte) []vectorPointer {
	pointers := make([]vectorPointer, 0)

	// VRPT is repeating group per S-57 §7.7.1.4 (31Main.pdf p79), each entry is 9 bytes:
	// NAME: B(40) - 5 bytes (RCNM=1, RCID=4)
	//   Offset 0: NAME_RCNM (1 byte) - Target record type
	//   Offset 1-4: NAME_RCID (4 bytes) - Target record ID (uint32 LE)
	// Offset 5: ORNT (1 byte) - Orientation (1=Forward, 2=Reverse, 255=Null)
	// Offset 6: USAG (1 byte) - Usage indicator (1=Exterior, 2=Interior, 3=Exterior truncated)
	// Offset 7: TOPI (1 byte) - Topology indicator (1=Begin, 2=End, 3=Left, 4=Right, 255=Null)
	// Offset 8: MASK (1 byte) - Masking indicator (1=Mask, 2=Show, 255=Null)
	for i := 0; i+8 < len(data); i += 9 {
		ptr := vectorPointer{
			TargetRCNM:  int(data[i]),
			TargetRCID:  int64(binary.LittleEndian.Uint32(data[i+1 : i+5])),
			Orientation: int(data[i+5]),
			Usage:       int(data[i+6]),
			Topology:    int(data[i+7]),
			Mask:        int(data[i+8]),
		}
		pointers = append(pointers, ptr)
	}

	return pointers
}
