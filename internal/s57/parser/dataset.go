package parser

import (
	"encoding/binary"

	"github.com/beetlebugorg/chartplotter-go/pkg/iso8211"
)

// datasetMetadata contains dataset identification and metadata from DSID record.
// This is private - access metadata through Chart methods.
//
// Reference: S-57 Part 3 §7.3.1.1 (31Main.pdf p64, table 7.4):
// Data Set Identification field structure with all subfields.
type datasetMetadata struct {
	rcnm int    // Record name (10 = dataset)
	rcid int64  // Record identification number
	expp int    // Exchange purpose (1=New, 2=Revision)
	intu int    // Intended usage
	dsnm string // Data set name - chart identifier (e.g., "GB5X01NE")
	edtn string // Edition number (e.g., "2")
	updn string // Update number (e.g., "0" for base cell)
	uadt string // Update application date (YYYYMMDD format)
	isdt string // Issue date (YYYYMMDD format)
	sted string // Edition number of S-57 (e.g., "03.1")
	prsp int    // Product specification (1=ENC, 2=ODD)
	psdn string // Product specification description
	pred string // Product specification edition number
	prof int    // Application profile (1=EN new, 2=ER revision, 3=DD)
	agen int    // Producing agency code
	comt string // Comment field
}

// DatasetName returns the dataset name (chart identifier).
// Example: "US5MA22M", "GB5X01NE"
func (m *datasetMetadata) DatasetName() string {
	return m.dsnm
}

// Edition returns the edition number as a string.
func (m *datasetMetadata) Edition() string {
	return m.edtn
}

// UpdateNumber returns the update number as a string.
func (m *datasetMetadata) UpdateNumber() string {
	return m.updn
}

// UpdateDate returns the update application date (YYYYMMDD format).
func (m *datasetMetadata) UpdateDate() string {
	return m.uadt
}

// IssueDate returns the issue date (YYYYMMDD format).
func (m *datasetMetadata) IssueDate() string {
	return m.isdt
}

// S57Edition returns the S-57 standard edition (e.g., "03.1").
func (m *datasetMetadata) S57Edition() string {
	return m.sted
}

// ProducingAgency returns the agency code.
func (m *datasetMetadata) ProducingAgency() int {
	return m.agen
}

// Comment returns the comment field.
func (m *datasetMetadata) Comment() string {
	return m.comt
}

// ExchangePurpose returns a human-readable exchange purpose string.
func (m *datasetMetadata) ExchangePurpose() string {
	switch m.expp {
	case 1:
		return "New"
	case 2:
		return "Revision"
	default:
		return "Unknown"
	}
}

// ProductSpecification returns a human-readable product specification string.
func (m *datasetMetadata) ProductSpecification() string {
	switch m.prsp {
	case 1:
		return "ENC"
	case 2:
		return "ODD"
	default:
		return "Unknown"
	}
}

// ApplicationProfile returns a human-readable application profile string.
func (m *datasetMetadata) ApplicationProfile() string {
	switch m.prof {
	case 1:
		return "EN (ENC New)"
	case 2:
		return "ER (ENC Revision)"
	case 3:
		return "DD (Data Dictionary)"
	default:
		return "Unknown"
	}
}

// datasetParams holds dataset-level parameters from DSPM record
// S-57 §7.3.2 (31Main.pdf p66): Data Set Parameter Record
type datasetParams struct {
	COMF int32 // Coordinate multiplication factor (typically 10^7)
	SOMF int32 // Sounding (3D) multiplication factor (typically 10)
	HDAT int   // Horizontal geodetic datum
	VDAT int   // Vertical datum
	SDAT int   // Sounding datum
	CSCL int32 // Compilation scale
	DUNI int   // Depth units: 1=meters, 2=fathoms+feet, 3=feet, 4=fathoms+fractions
	COUN int   // Coordinate units: 1=lat/lon, 2=projected
}

// defaultDatasetParams returns default parameters when DSPM is not found
func defaultDatasetParams() datasetParams {
	return datasetParams{
		COMF: 10000000, // 10^7 - standard for lat/lon in units of 0.00000001 degrees
		SOMF: 10,       // Standard for depth in decimeters
	}
}

// extractDatasetParams extracts DSPM record parameters
// S-57 §7.3.2.1 (31Main.pdf p66): DSPM field structure
func extractDatasetParams(isoFile *iso8211.ISO8211File) datasetParams {
	params := defaultDatasetParams()

	// Look for DSPM record (Data Set Parameters)
	for _, record := range isoFile.Records {
		if dspmData, ok := record.Fields["DSPM"]; ok {
			params = parseDSPM(dspmData)
			break // Use first DSPM found
		}
	}

	return params
}

// parseDSPM parses the DSPM field per S-57 §7.3.2.1 (31Main.pdf p66)
// Binary format:
//
//	RCNM (1 byte) - Record name, value 20 = dataset parameters
//	RCID (4 bytes) - Record ID (uint32 LE)
//	HDAT (1 byte) - Horizontal datum
//	VDAT (1 byte) - Vertical datum
//	SDAT (1 byte) - Sounding datum
//	CSCL (4 bytes) - Compilation scale (uint32 LE)
//	DUNI (1 byte) - Depth units
//	HUNI (1 byte) - Height units
//	PUNI (1 byte) - Position units
//	COUN (1 byte) - Coordinate units
//	COMF (4 bytes) - Coordinate multiplication factor (int32 LE)
//	SOMF (4 bytes) - Sounding multiplication factor (int32 LE)
//	COMT (variable) - Comment
func parseDSPM(data []byte) datasetParams {
	params := defaultDatasetParams()

	// Minimum size check: RCNM(1) + RCID(4) + HDAT(1) + VDAT(1) + SDAT(1) + CSCL(4)
	//                     + DUNI(1) + HUNI(1) + PUNI(1) + COUN(1) + COMF(4) + SOMF(4) = 24 bytes
	if len(data) < 24 {
		return params
	}

	// Check RCNM (should be 20 for DSPM)
	rcnm := data[0]
	if rcnm != 20 {
		return params
	}

	// Extract fields at fixed offsets
	offset := 1 // Skip RCNM

	// RCID (4 bytes) - not used currently
	offset += 4

	// HDAT (1 byte)
	params.HDAT = int(data[offset])
	offset++

	// VDAT (1 byte)
	params.VDAT = int(data[offset])
	offset++

	// SDAT (1 byte)
	params.SDAT = int(data[offset])
	offset++

	// CSCL (4 bytes)
	params.CSCL = int32(binary.LittleEndian.Uint32(data[offset : offset+4]))
	offset += 4

	// DUNI (1 byte) - Depth units
	params.DUNI = int(data[offset])
	offset++

	// Skip HUNI, PUNI (2 bytes total)
	offset += 2

	// COUN (1 byte) - Coordinate units
	params.COUN = int(data[offset])
	offset++

	// COMF (4 bytes) - int32 signed
	if offset+4 <= len(data) {
		params.COMF = int32(binary.LittleEndian.Uint32(data[offset : offset+4]))
		offset += 4
	}

	// SOMF (4 bytes) - int32 signed
	if offset+4 <= len(data) {
		params.SOMF = int32(binary.LittleEndian.Uint32(data[offset : offset+4]))
	}

	// Validation: If COMF is 0 or negative, use default
	if params.COMF <= 0 {
		params.COMF = 10000000
	}
	if params.SOMF <= 0 {
		params.SOMF = 10
	}

	return params
}

// convertCoordinate converts an integer coordinate to float64 using COMF
func convertCoordinate(value int32, comf int32) float64 {
	if comf <= 0 {
		comf = 10000000 // Default if invalid
	}
	return float64(value) / float64(comf)
}
