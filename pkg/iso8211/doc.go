// Package iso8211 provides a parser for ISO/IEC 8211 binary files.
//
// ISO 8211 is an international standard for file structure and record format
// for information interchange. It is widely used in geospatial data formats
// and other applications requiring structured data exchange.
//
// # Overview
//
// The ISO 8211 format consists of:
//   - A Data Descriptive Record (DDR) that defines the structure of the data
//   - Zero or more Data Records (DRs) containing the actual data
//
// Each record has a 24-byte leader, a directory of field tags, and a field area
// containing the actual data.
//
// # Basic Usage
//
// To parse an ISO 8211 file:
//
//	reader, err := iso8211.NewReader("path/to/file.dat")
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer reader.Close()
//
//	file, err := reader.Parse()
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	// Access the DDR
//	fmt.Printf("DDR has %d field controls\n", len(file.DDR.FieldControls))
//
//	// Iterate through data records
//	for i, record := range file.Records {
//	    fmt.Printf("Record %d has %d fields\n", i, len(record.Fields))
//	}
//
// # Standard References
//
// This implementation follows ISO/IEC 8211:1994 - Specification for a data
// descriptive file for information interchange.
//
// Official specification:
//   - ISO/IEC 8211:1994: https://www.iso.org/standard/20694.html
//
// For S-57/S-52 implementation details, see:
//   - IHO S-57 Part 3 Annex A: https://iho.int/uploads/user/pubs/standards/s-57/31Main.pdf
//     (ISO 8211 summary and examples for hydrographic data - see Annex A starting at page 3.A.1)
//
// ISO 8211 is used in various geospatial and data exchange standards including
// S-57 Electronic Navigational Charts (ENC).
package iso8211
