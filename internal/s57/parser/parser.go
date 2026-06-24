package parser

import (
	"encoding/binary"
	"fmt"
	"io/fs"
	"strings"

	"github.com/beetlebugorg/chartplotter/pkg/iso8211"
)

// Parser parses S-57 ENC files and extracts features.
//
// S-57 defines an "exchange set" as a collection of files for transferring hydrographic data.
// Each file contains records (metadata, features, spatial data) structured per ISO 8211.
// This parser reads the ISO 8211 structure and interprets it according to S-57 semantics.
//
// References:
//   - S-57 Part 1 (31Main.pdf p1.1): Definition of "exchange set"
//   - S-57 Part 3 §7 (31Main.pdf p94): Complete record and field structure specification
type Parser interface {
	// Parse reads an S-57 file and returns extracted chart
	// Returns error if file cannot be read or parsed
	Parse(filename string) (*Chart, error)

	// ParseWithOptions parses with custom options
	ParseWithOptions(filename string, opts ParseOptions) (*Chart, error)

	// SupportedObjectClasses returns list of supported S-57 object classes
	SupportedObjectClasses() []string
}

// ParseOptions configures parsing behavior
type ParseOptions struct {
	// SkipUnknownFeatures: if true, skip features with unknown object classes
	// Default: false (return error on unknown types)
	SkipUnknownFeatures bool

	// ValidateGeometry: if true, validate all coordinates and geometry rules
	// Default: true
	ValidateGeometry bool

	// ObjectClassFilter: if non-empty, only extract these object classes
	// Empty means extract all supported types
	ObjectClassFilter []string

	// ApplyUpdates: if true, automatically discover and apply update files (.001, .002, etc.)
	// Default: true
	ApplyUpdates bool

	// ValidateConformance: if true, any S-57 / ISO-8211 spec deviation detected
	// during parsing (see conformance.go) is promoted to a parse error instead of
	// a non-fatal warning. Default: false — deviations are collected on the Chart
	// (Chart.Warnings) and the cell still renders.
	ValidateConformance bool

	// MaskCoastlineCoincidentBoundaries: if true, DERIVE coastline-coincident edge
	// masking for area features. S-57 Appendix B.1 Annex A §17 scenario 2 says area
	// boundary edges that coincide with the coastline should be masked to avoid
	// clutter, but the masking flag (FSPT MASK=1) is a producer choice that NOAA
	// cells never set. When this option is on, any area feature's drawn boundary
	// edge whose RCID is ALSO referenced by a COALNE feature is dropped from
	// BoundaryLines (the drawn border), while the fill (Rings) and flat Coordinates
	// are left intact. The coast-definer LNDARE is exempt, so the visible coast is
	// still drawn (by COALNE and LNDARE's own boundary). Default: false.
	MaskCoastlineCoincidentBoundaries bool

	// Fs is the filesystem to use for reading files
	// If nil, the OS filesystem is used
	Fs fs.FS
}

// DefaultParseOptions returns parse options with defaults
func DefaultParseOptions() ParseOptions {
	return ParseOptions{
		SkipUnknownFeatures: false,
		ValidateGeometry:    true,
		ObjectClassFilter:   nil,
		ApplyUpdates:        true,
	}
}

// defaultParser implements the Parser interface
type defaultParser struct {
}

// NewParser creates a new S-57 parser
func NewParser() Parser {
	return &defaultParser{}
}

// DefaultParser returns parser with default options
func DefaultParser() (Parser, error) {
	return NewParser(), nil
}

// Parse reads an S-57 file and returns extracted chart
func (p *defaultParser) Parse(filename string) (*Chart, error) {
	return p.ParseWithOptions(filename, DefaultParseOptions())
}

// ParseWithOptions parses with custom options
func (p *defaultParser) ParseWithOptions(filename string, opts ParseOptions) (*Chart, error) {
	// Use OS filesystem if none specified
	fsys := opts.Fs
	if fsys == nil {
		fsys = iso8211.OSFS()
	}

	// Collector for non-fatal spec-conformance deviations (see conformance.go).
	// In strict mode (ValidateConformance) these are promoted to an error below;
	// otherwise they are attached to the returned Chart.
	conf := &conformance{}

	// 1. Parse base file and extract raw records
	baseData, params, metadata, err := parseBaseFile(fsys, filename, opts, conf)
	if err != nil {
		return nil, err
	}
	baseData.warnings = conf

	// 2. Discover and apply updates if enabled
	if opts.ApplyUpdates {
		updateFiles, err := findUpdateFiles(fsys, filename)
		if err != nil {
			return nil, fmt.Errorf("failed to discover update files: %w", err)
		}
		if len(updateFiles) > 0 {
			if err := applyUpdates(fsys, baseData, updateFiles, params); err != nil {
				return nil, fmt.Errorf("failed to apply updates: %w", err)
			}
		}
	}

	// 3. Build final chart with geometries
	chart, err := buildChart(baseData, metadata, params, opts)
	if err != nil {
		return nil, err
	}

	// 4. Surface conformance deviations: error in strict mode, else attach.
	if opts.ValidateConformance {
		if cerr := conf.asError(); cerr != nil {
			return nil, cerr
		}
	}
	chart.warnings = conf.warnings()
	return chart, nil
}

// parseBaseFile extracts raw feature and spatial records without building geometries.
// This allows update files to be applied before geometry construction.
func parseBaseFile(fsys fs.FS, filename string, opts ParseOptions, conf *conformance) (*chartData, datasetParams, *datasetMetadata, error) {
	// Open ISO 8211 file from filesystem using OpenFS
	parser, err := iso8211.OpenFS(fsys, filename)
	if err != nil {
		return nil, datasetParams{}, nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer parser.Close()

	// Parse ISO 8211 structure
	isoFile, err := parser.Parse()
	if err != nil {
		return nil, datasetParams{}, nil, fmt.Errorf("failed to parse ISO 8211: %w", err)
	}

	// ISO/IEC 8211 leader conformance (Annex A.2.2 fixed values).
	validateLeaders(isoFile, conf)

	// Extract dataset parameters (COMF, SOMF, etc.) from DSPM record
	params := extractDatasetParams(isoFile)
	// DSPM coordinate/sounding multipliers (§7.3.2.1): a non-positive COMF/SOMF is
	// invalid; we fall back to the standard 10^7 / 10 to keep rendering, but report.
	if params.comfDefaulted {
		conf.add("7.3.2.1", "DSPM_COMF_INVALID", "DSPM COMF was missing or <= 0; defaulted to 10000000")
	}
	if params.somfDefaulted {
		conf.add("7.3.2.1", "DSPM_SOMF_INVALID", "DSPM SOMF was missing or <= 0; defaulted to 10")
	}

	// Extract dataset metadata from DSID record
	metadata := extractDSID(isoFile)

	// Extract feature records (without geometry)
	features := []*featureRecord{}
	featuresByID := make(map[featureID]*featureRecord)
	for _, record := range isoFile.Records {
		if featureRec := parseFeatureRecord(record); featureRec != nil {
			validateFeatureConformance(featureRec, conf)
			features = append(features, featureRec)
			// Create composite key from FOID fields
			key := featureID{
				AGEN: featureRec.AGEN,
				FIDN: featureRec.FIDN,
				FIDS: featureRec.FIDS,
			}
			featuresByID[key] = featureRec
		}
	}

	// Extract spatial records
	spatialRecords := make(map[spatialKey]*spatialRecord)
	for _, record := range isoFile.Records {
		if spatialRec := parseSpatialRecordWithParams(record, params); spatialRec != nil {
			validateSpatialConformance(spatialRec, conf)
			key := spatialKey{RCNM: int(spatialRec.RecordType), RCID: spatialRec.ID}
			spatialRecords[key] = spatialRec
		}
	}

	return &chartData{
		features:       features,
		spatialRecords: spatialRecords,
		metadata:       metadata,
		featuresByID:   featuresByID,
	}, params, metadata, nil
}

// buildChart constructs final Chart with geometries from merged data.
// This is called after all updates have been applied to the raw records.
func buildChart(data *chartData, metadata *datasetMetadata, params datasetParams, opts ParseOptions) (*Chart, error) {
	// Build geometries for all features
	finalFeatures := []Feature{}

	// Derived coastline-coincident edge masking (S-57 App. B.1 Annex A §17 scn 2).
	// Build the set of edge RCIDs referenced by ANY coast-definer (COALNE, LNDARE,
	// SLCONS — see coastDefinerClasses) once; other area features then drop boundary
	// edges that share these RCIDs. See ParseOptions.MaskCoastlineCoincidentBoundaries.
	var coastEdges map[int64]bool
	if opts.MaskCoastlineCoincidentBoundaries {
		coastEdges = map[int64]bool{}
		for _, fr := range data.features {
			if objClass, _ := ObjectClassToString(fr.ObjectClass); !coastDefinerClasses[objClass] {
				continue
			}
			for _, ref := range fr.SpatialRefs {
				// Coast-definers reference edges directly (lines) or via a face (areas).
				// Accept direct edge refs (RCNM=130 / unknown) and, for area-typed
				// definers like LNDARE, edges pulled from any referenced face's VRPT.
				if ref.RCNM == 0 || ref.RCNM == int(spatialTypeEdge) {
					coastEdges[ref.RCID] = true
					continue
				}
				if ref.RCNM == int(spatialTypeFace) {
					if face, ok := data.spatialRecords[spatialKey{RCNM: ref.RCNM, RCID: ref.RCID}]; ok {
						for _, ptr := range face.VectorPointers {
							if ptr.TargetRCNM == int(spatialTypeEdge) {
								coastEdges[ptr.TargetRCID] = true
							}
						}
					}
				}
			}
		}
	}

	for _, featureRec := range data.features {
		// Convert object class code to string once (needed for filter, masking, and
		// the final Feature). Errors are deferred to the existing handling below.
		objClass, objClassErr := ObjectClassToString(featureRec.ObjectClass)

		// Check object class filter
		if len(opts.ObjectClassFilter) > 0 {
			if !contains(opts.ObjectClassFilter, objClass) {
				continue // Filtered out
			}
		}

		// Derived coastline masking applies only to area features (GeomPrim=3) that
		// are not coast-definers (LNDARE). Suppression touches only BoundaryLines.
		maskCoast := opts.MaskCoastlineCoincidentBoundaries &&
			featureRec.GeomPrim == 3 && !isCoastlineMaskExempt(objClass)

		// Construct geometry from spatial records
		geometry, err := constructGeometry(featureRec, data.spatialRecords, coastEdges, maskCoast)
		if err != nil {
			if opts.SkipUnknownFeatures {
				continue // Skip this feature
			}
			// Add context about which feature failed
			return nil, fmt.Errorf("feature ID=%d, ObjectClass=%s (OBJL=%d), GeomPrim=%d: %w",
				featureRec.ID, objClass, featureRec.ObjectClass, featureRec.GeomPrim, err)
		}

		// Apply geometry validation if enabled
		if opts.ValidateGeometry {
			if err := ValidateGeometry(&geometry); err != nil {
				if opts.SkipUnknownFeatures {
					continue
				}
				return nil, fmt.Errorf("feature %d: %w", featureRec.ID, err)
			}
		}

		// Surface any object-class decode error (computed once above).
		if objClassErr != nil {
			if opts.SkipUnknownFeatures {
				continue
			}
			return nil, objClassErr
		}

		// Create feature
		feature := Feature{
			ID:          featureRec.ID,
			ObjectClass: objClass,
			Geometry:    geometry,
			Attributes:  featureRec.Attributes,
		}

		finalFeatures = append(finalFeatures, feature)
	}

	return &Chart{
		metadata:       metadata,
		params:         params,
		Features:       finalFeatures,
		spatialRecords: data.spatialRecords, // Keep for potential future updates
	}, nil
}

// extractDSID extracts and parses the DSID record from the ISO 8211 file.
//
// DSID (Data Set Identification) is the first field in every S-57 dataset's general
// information record. It contains critical metadata like edition number, issue date,
// update information, and the S-57 version used. This function searches all records
// for the DSID field and parses it into a structured datasetMetadata object.
//
// Reference: S-57 Part 3 §7.3.1.1 (31Main.pdf p64, table 7.4): Complete DSID
// field structure showing all 16 subfields including their formats and meanings.
func extractDSID(isoFile *iso8211.ISO8211File) *datasetMetadata {
	// Look for DSID record (Dataset Identification)
	for _, record := range isoFile.Records {
		if dsidData, ok := record.Fields["DSID"]; ok {
			return parseDSID(dsidData)
		}
	}
	return nil
}

// parseDSID parses DSID field binary data into a datasetMetadata structure.
//
// The DSID field uses a mixed format with both fixed-length binary fields and
// variable-length ASCII fields. Binary fields (RCNM, RCID, EXPP, INTU, etc.) come
// first at fixed offsets, followed by ASCII fields terminated by 0x1F (unit separator).
// This two-phase structure allows efficient parsing while supporting variable-length
// text like dataset names and comments.
//
// Parsing strategy:
//   - Phase 1: Read fixed-length binary fields at known offsets
//   - Phase 2: Read variable-length ASCII fields sequentially until 0x1F delimiter
//
// Reference: S-57 Part 3 §7.3.1.1 (31Main.pdf p64, table 7.4):
// Shows complete field structure with format codes:
//   - b11 = 1-byte binary
//   - b12 = 2-byte binary (uint16 LE)
//   - b14 = 4-byte binary (uint32 LE)
//   - A( ) = variable-length ASCII
//   - R(4) = 4-character real number
func parseDSID(data []byte) *datasetMetadata {
	dsid := &datasetMetadata{}

	// Minimum size check: RCNM(1) + RCID(4) + EXPP(1) + INTU(1) = 7 bytes minimum
	if len(data) < 7 {
		return dsid
	}

	offset := 0

	// RCNM (1 byte) - Record name. Per table 2.2 (31Main.pdf p3.8), value 10 = "DS" (Dataset)
	// This identifies the record type in the S-57 data structure.
	if offset < len(data) {
		dsid.rcnm = int(data[offset])
		offset++
	}

	// RCID (4 bytes, uint32 LE) - Record identification number
	// Combined with RCNM, forms unique record key within the file (31Main.pdf p3.7 §2.2)
	if offset+4 <= len(data) {
		dsid.rcid = int64(binary.LittleEndian.Uint32(data[offset : offset+4]))
		offset += 4
	}

	// EXPP (1 byte) - Exchange purpose: 1=New dataset, 2=Revision
	// Indicates whether this is original data or an update (table 7.4)
	if offset < len(data) {
		dsid.expp = int(data[offset])
		offset++
	}

	// INTU (1 byte) - Intended usage
	// Numeric code indicating data purpose (defined in product specifications)
	if offset < len(data) {
		dsid.intu = int(data[offset])
		offset++
	}

	// Variable-length ASCII fields follow, each terminated by 0x1F (Unit Separator).
	// Per ISO 8211, 0x1F marks the boundary between subfields in variable-length data.
	// We read sequentially: scan until 0x1F, extract string, skip delimiter, repeat.
	extractASCII := func() string {
		if offset >= len(data) {
			return ""
		}
		start := offset
		for offset < len(data) && data[offset] != 0x1F {
			offset++
		}
		result := string(data[start:offset])
		if offset < len(data) && data[offset] == 0x1F {
			offset++ // Skip unit separator
		}
		return result
	}

	// DSNM - Data set name. Primary identifier for the chart cell (e.g., "US5MA22M").
	dsid.dsnm = extractASCII()

	// EDTN - Edition number. String representing the chart edition (e.g., "12").
	dsid.edtn = extractASCII()

	// UPDN - Update number. String showing cumulative update count (e.g., "5").
	dsid.updn = extractASCII()

	// UADT - Update application date. A(8) fixed-length field. Format: YYYYMMDD.
	// All updates on or before this date must be applied to have current data.
	// This is a FIXED 8-byte ASCII field, NOT 0x1F-terminated.
	if offset+8 <= len(data) {
		dsid.uadt = strings.TrimRight(string(data[offset:offset+8]), "\x00 ")
		offset += 8
	}

	// ISDT - Issue date. A(8) fixed-length field. Format: YYYYMMDD.
	// When the dataset was released by producer.
	// This is a FIXED 8-byte ASCII field, NOT 0x1F-terminated.
	if offset+8 <= len(data) {
		dsid.isdt = strings.TrimRight(string(data[offset:offset+8]), "\x00 ")
		offset += 8
	}

	// STED - Edition number of S-57 standard. R(4) fixed-length field.
	// Real number as 4-byte ASCII (e.g., "3.1" or "03.1" for Edition 3.1).
	// This is a FIXED 4-byte ASCII field, NOT 0x1F-terminated.
	if offset+4 <= len(data) {
		dsid.sted = strings.TrimRight(string(data[offset:offset+4]), "\x00 ")
		offset += 4
	}

	// PRSP (1 byte) - Product specification code
	// 1 = ENC (Electronic Navigational Chart)
	// 2 = ODD (Object Catalogue Data Dictionary)
	// Returned to binary format after the ASCII fields above.
	if offset < len(data) {
		dsid.prsp = int(data[offset])
		offset++
	}

	// PSDN - Product specification description. Human-readable name for non-standard specs.
	dsid.psdn = extractASCII()

	// PRED - Product specification edition number. Version of the product spec used.
	dsid.pred = extractASCII()

	// PROF (1 byte) - Application profile identification
	// 1 = EN (ENC New edition), 2 = ER (ENC Revision), 3 = DD (Data Dictionary)
	// Defines the data exchange profile being used.
	if offset < len(data) {
		dsid.prof = int(data[offset])
		offset++
	}

	// AGEN (2 bytes, uint16 LE) - Producing agency code
	// References IHO agency code table (see Appendix A - Object Catalogue).
	// Example: 550 = NOAA (United States).
	if offset+2 <= len(data) {
		dsid.agen = int(binary.LittleEndian.Uint16(data[offset : offset+2]))
		offset += 2
	}

	// COMT - Comment. Free-form text, last field, may extend to end of data.
	dsid.comt = extractASCII()

	return dsid
}

// SupportedObjectClasses returns list of supported S-57 object classes
func (p *defaultParser) SupportedObjectClasses() []string {
	// All object classes are supported - read dynamically from file
	return []string{"All object classes supported - read dynamically from file"}
}

// coastDefinerClasses are the object classes that DEFINE the visible coast / shore
// edge. They play two roles in derived coastline-coincident masking:
//  1. their boundary edge RCIDs form the "coast edge set" (see buildChart), and
//  2. they are EXEMPT from masking — they keep their own coincident edges so the
//     shore stays drawn.
//
// COALNE (coastline) and SLCONS (shoreline construction: piers, wharves, seawalls)
// are usually lines; LNDARE (land area) is the area whose boundary IS the shore.
// In NOAA cells the land/water boundary is frequently encoded only as an LNDARE
// (or SLCONS) edge with no coincident COALNE — so masking against COALNE alone
// leaves stray boundary "chevrons" along the coast. Including all three catches
// them. Add classes here to extend the set.
var coastDefinerClasses = map[string]bool{
	"COALNE": true,
	"LNDARE": true,
	"SLCONS": true,
}

// isCoastlineMaskExempt reports whether an area object class is a coast-definer and
// therefore exempt from derived coastline-coincident boundary masking.
func isCoastlineMaskExempt(objClass string) bool {
	return coastDefinerClasses[objClass]
}

// contains checks if a slice contains a string
func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
