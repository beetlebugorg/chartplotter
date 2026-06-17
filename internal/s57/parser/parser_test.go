package parser

import (
	"encoding/binary"
	"fmt"
	"testing"

	"github.com/beetlebugorg/chartplotter/pkg/iso8211"
)

// BenchmarkParse benchmarks parsing a real S-57 chart
func BenchmarkParse(b *testing.B) {
	parser, err := DefaultParser()
	if err != nil {
		b.Fatalf("failed to create parser: %v", err)
	}

	opts := ParseOptions{
		SkipUnknownFeatures: true,
		ValidateGeometry:    true,
	}

	// Run the parse operation b.N times
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := parser.ParseWithOptions("../../../testdata/US4MD81M.000", opts)
		if err != nil {
			b.Fatalf("parse failed: %v", err)
		}
	}
}

// BenchmarkVRPTResolution benchmarks just the VRPT resolution logic
func BenchmarkVRPTResolution(b *testing.B) {
	// Create test spatial records with VRPT chains
	spatialRecords := map[spatialKey]*spatialRecord{
		{RCNM: int(spatialTypeIsolatedNode), RCID: 100}: {
			ID:         100,
			RecordType: spatialTypeIsolatedNode,
			Coordinates: [][]float64{
				{-71.0, 42.0}, {-71.1, 42.1}, {-71.2, 42.2},
			},
		},
		{RCNM: int(spatialTypeEdge), RCID: 200}: {
			ID:         200,
			RecordType: spatialTypeEdge,
			VectorPointers: []vectorPointer{
				{TargetRCID: 100, Orientation: 1},
			},
		},
		{RCNM: int(spatialTypeEdge), RCID: 300}: {
			ID:         300,
			RecordType: spatialTypeEdge,
			VectorPointers: []vectorPointer{
				{TargetRCID: 200, Orientation: 1},
			},
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = resolveVectorPointers(spatialRecords[spatialKey{RCNM: int(spatialTypeEdge), RCID: 300}], spatialRecords)
	}
}

func TestParseDSID(t *testing.T) {
	// Create a mock DSID field with all fields
	data := make([]byte, 0, 256)

	// RCNM (1 byte) = 10
	data = append(data, 10)

	// RCID (4 bytes) = 1
	rcidBytes := make([]byte, 4)
	binary.LittleEndian.PutUint32(rcidBytes, 1)
	data = append(data, rcidBytes...)

	// EXPP (1 byte) = 1 (New)
	data = append(data, 1)

	// INTU (1 byte) = 1
	data = append(data, 1)

	// DSNM (variable) = "GB5X01NE"
	data = append(data, []byte("GB5X01NE")...)
	data = append(data, 0x1F) // Unit separator

	// EDTN (variable) = "2"
	data = append(data, []byte("2")...)
	data = append(data, 0x1F)

	// UPDN (variable) = "0"
	data = append(data, []byte("0")...)
	data = append(data, 0x1F)

	// UADT (fixed A(8)) = "20250107"
	data = append(data, []byte("20250107")...)

	// ISDT (fixed A(8)) = "20240101"
	data = append(data, []byte("20240101")...)

	// STED (fixed R(4)) = "03.1"
	data = append(data, []byte("03.1")...)

	// PRSP (1 byte) = 1 (ENC)
	data = append(data, 1)

	// PSDN (variable) = "ENC"
	data = append(data, []byte("ENC")...)
	data = append(data, 0x1F)

	// PRED (variable) = "2.0"
	data = append(data, []byte("2.0")...)
	data = append(data, 0x1F)

	// PROF (1 byte) = 1 (EN - ENC New)
	data = append(data, 1)

	// AGEN (2 bytes) = 540 (UK Hydrographic Office)
	agenBytes := make([]byte, 2)
	binary.LittleEndian.PutUint16(agenBytes, 540)
	data = append(data, agenBytes...)

	// COMT (variable) = "Test chart"
	data = append(data, []byte("Test chart")...)
	data = append(data, 0x1F)

	// Parse the data
	dsid := parseDSID(data)

	// Verify all fields via accessor methods
	tests := []struct {
		name     string
		got      interface{}
		expected interface{}
	}{
		{"RCNM", dsid.rcnm, 10},
		{"RCID", dsid.rcid, int64(1)},
		{"EXPP", dsid.expp, 1},
		{"INTU", dsid.intu, 1},
		{"DSNM", dsid.DatasetName(), "GB5X01NE"},
		{"EDTN", dsid.Edition(), "2"},
		{"UPDN", dsid.UpdateNumber(), "0"},
		{"UADT", dsid.UpdateDate(), "20250107"},
		{"ISDT", dsid.IssueDate(), "20240101"},
		{"STED", dsid.S57Edition(), "03.1"},
		{"PRSP", dsid.prsp, 1},
		{"PSDN", dsid.psdn, "ENC"},
		{"PRED", dsid.pred, "2.0"},
		{"PROF", dsid.prof, 1},
		{"AGEN", dsid.ProducingAgency(), 540},
		{"COMT", dsid.Comment(), "Test chart"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.expected {
				t.Errorf("%s: got %v, expected %v", tt.name, tt.got, tt.expected)
			}
		})
	}
}

func TestParseDSIDMinimal(t *testing.T) {
	// Test with minimal data (just fixed fields)
	data := make([]byte, 0, 64)

	// RCNM (1 byte) = 10
	data = append(data, 10)

	// RCID (4 bytes) = 1
	rcidBytes := make([]byte, 4)
	binary.LittleEndian.PutUint32(rcidBytes, 1)
	data = append(data, rcidBytes...)

	// EXPP (1 byte) = 1 (New)
	data = append(data, 1)

	// INTU (1 byte) = 1
	data = append(data, 1)

	// DSNM only
	data = append(data, []byte("TEST")...)
	data = append(data, 0x1F)

	dsid := parseDSID(data)

	if dsid.rcnm != 10 {
		t.Errorf("RCNM: got %d, expected 10", dsid.rcnm)
	}
	if dsid.DatasetName() != "TEST" {
		t.Errorf("DSNM: got %s, expected TEST", dsid.DatasetName())
	}
	// Other fields should be empty/zero
	if dsid.Edition() != "" {
		t.Errorf("EDTN: got %s, expected empty", dsid.Edition())
	}
}

func TestParseDSIDEmpty(t *testing.T) {
	// Test with empty/truncated data
	data := []byte{}
	dsid := parseDSID(data)

	if dsid == nil {
		t.Error("parseDSID returned nil for empty data")
	}
	if dsid.DatasetName() != "" {
		t.Errorf("DSNM should be empty for empty data, got: %s", dsid.DatasetName())
	}
}

func TestParserPopulatesMetadata(t *testing.T) {
	// Test that parser properly populates metadata in Chart
	parser := NewParser()

	// Parse a test file with validation disabled to avoid test data issues
	opts := ParseOptions{
		SkipUnknownFeatures: true,
		ValidateGeometry:    false,
	}
	chart, err := parser.ParseWithOptions("../../../testdata/US4MD81M.000", opts)
	if err != nil {
		t.Fatalf("Failed to parse test file: %v", err)
	}

	// Verify metadata is populated via accessor methods
	if chart.DatasetName() == "" {
		t.Error("Chart dataset name is empty")
	}

	// Log metadata for inspection
	t.Logf("Chart Metadata:")
	t.Logf("  DSNM (Chart Name): %s", chart.DatasetName())
	t.Logf("  EDTN (Edition):    %s", chart.Edition())
	t.Logf("  UPDN (Update):     %s", chart.UpdateNumber())
	t.Logf("  ISDT (Issue Date): %s", chart.IssueDate())
	t.Logf("  STED (S-57 Ed):    %s", chart.S57Edition())
	t.Logf("  EXPP (Purpose):    %s", chart.ExchangePurpose())
	t.Logf("  PRSP (Product):    %s", chart.ProductSpecification())
	t.Logf("  PROF (Profile):    %s", chart.ApplicationProfile())
	t.Logf("  AGEN (Agency):     %d", chart.ProducingAgency())
	if chart.Comment() != "" {
		t.Logf("  COMT (Comment):    %s", chart.Comment())
	}
}

// TestDSIDFieldStructure examines the actual DSID field structure from a real file
func TestDSIDFieldStructure(t *testing.T) {
	parser, err := iso8211.Open("../../../testdata/US4MD81M.000")
	if err != nil {
		t.Fatalf("Failed to open test file: %v", err)
	}
	defer parser.Close()

	isoFile, err := parser.Parse()
	if err != nil {
		t.Fatalf("Failed to parse ISO 8211: %v", err)
	}

	// Check DDR for DSID field structure
	if isoFile.DDR == nil {
		t.Fatal("DDR is nil")
	}

	dsidControl, ok := isoFile.DDR.FieldControls["DSID"]
	if !ok {
		t.Fatal("DSID field control not found in DDR")
	}

	t.Logf("DSID Field Control:")
	t.Logf("  DataStructCode: %d", dsidControl.DataStructCode)
	t.Logf("  DataTypeCode: %d", dsidControl.DataTypeCode)
	t.Logf("  FieldName: %s", dsidControl.FieldName)
	t.Logf("  FormatControls: %s", dsidControl.FormatControls)
	t.Logf("  Subfields: %d", len(dsidControl.Subfields))

	for i, subfield := range dsidControl.Subfields {
		t.Logf("    [%d] Label=%s, FormatType=%c, Width=%d",
			i, subfield.Label, subfield.FormatType, subfield.Width)
	}

	// Get actual DSID data from first record
	for _, record := range isoFile.Records {
		if dsidData, ok := record.Fields["DSID"]; ok {
			t.Logf("\nDSID Raw Data (%d bytes):", len(dsidData))
			// Print first 100 bytes as hex and ASCII
			limit := len(dsidData)
			if limit > 100 {
				limit = 100
			}
			for i := 0; i < limit; i += 16 {
				end := i + 16
				if end > limit {
					end = limit
				}
				// Hex
				hex := ""
				for j := i; j < end; j++ {
					hex += fmt.Sprintf("%02x ", dsidData[j])
				}
				// ASCII
				ascii := ""
				for j := i; j < end; j++ {
					if dsidData[j] >= 32 && dsidData[j] < 127 {
						ascii += string(dsidData[j])
					} else {
						ascii += "."
					}
				}
				t.Logf("  %04x: %-48s  %s", i, hex, ascii)
			}
			break
		}
	}
}
