package iso8211

// ISO 8211 Type Definitions
//
// Based on ISO/IEC 8211:1994 standard for binary file formats.
// For S-57/S-52 implementation details, see IHO S-57 Part 3:
// https://iho.int/uploads/user/pubs/standards/s-57/31Main.pdf

// ISO8211File represents a complete ISO 8211 file containing
// a Data Descriptive Record (DDR) and zero or more Data Records (DR)
type ISO8211File struct {
	DDR     *DataDescriptiveRecord // File structure definition
	Records []*DataRecord          // Data records following DDR structure
}

// DataDescriptiveRecord (DDR) defines the structure and metadata
// for all subsequent data records in the file
// IHO S-57 Part 3 Annex A.2: Complete DDR structure (leader A.2.2.1, directory A.2.3, field area A.2.4.1)
type DataDescriptiveRecord struct {
	Leader        *Leader                  // Record metadata (24 bytes)
	Directory     []*DirectoryEntry        // Field directory
	FieldControls map[string]*FieldControl // Field structure definitions (keyed by tag)
	FieldArea     []byte                   // Raw field data area
}

// DataRecord (DR) contains actual chart data organized according to DDR structure
// IHO S-57 Part 3 Annex A.2: Complete DR structure (leader A.2.2.2, directory A.2.3, field area A.2.4.2)
type DataRecord struct {
	Leader    *Leader           // Record metadata (24 bytes)
	Directory []*DirectoryEntry // Field directory for this record
	Fields    map[string][]byte // Field data keyed by tag (raw bytes)
}

// Leader contains record metadata in a 24-byte fixed header
// IHO S-57 Part 3 Annex A.2.2: DDR leader (Table A.1), DR leader (Table A.3), Entry map (Tables A.2, A.4)
//
// The leader is always exactly 24 bytes and describes how to parse the rest of the record.
// Positions 0-19 describe the record, positions 20-23 (the "entry map") describe the directory format.
type Leader struct {
	// Record description (positions 0-19)
	RecordLength         int     // RP 0-4 (5 bytes): Total record length in bytes
	InterchangeLevel     byte    // RP 5 (1 byte): Interchange level - "3" for S-57
	LeaderIdentifier     byte    // RP 6 (1 byte): 'L' for DDR, 'D' for DR
	InlineCodeExtension  byte    // RP 7 (1 byte): Code extension indicator - "E" for extended ASCII in S-57
	VersionNumber        byte    // RP 8 (1 byte): Format version - "1" for ISO 8211:1994
	ApplicationIndicator byte    // RP 9 (1 byte): Application indicator - SPACE for S-57
	FieldControlLength   int     // RP 10-11 (2 bytes): Length of field control field - "09" for S-57
	FieldAreaStart       int     // RP 12-16 (5 bytes): Base address of field area (bytes in leader + directory)
	ExtendedCharSet      [3]byte // RP 17-19 (3 bytes): Extended character set - " ! " (SPACE,!,SPACE) for S-57

	// Entry map - describes directory entry format (positions 20-23)
	// These values tell us how to parse each directory entry
	SizeOfFieldLength   int  // RP 20 (1 byte): Digits in field length field (variable 1-9, encoder-defined)
	SizeOfFieldPosition int  // RP 21 (1 byte): Digits in field position field (variable 1-9, encoder-defined)
	Reserved            byte // RP 22 (1 byte): Reserved - "0" in S-57
	SizeOfFieldTag      int  // RP 23 (1 byte): Characters in field tag - "4" for S-57
}

// DirectoryEntry maps field tags to their location and size within the field area
// IHO S-57 Part 3 Annex A.2.3: Directory - tag, length, and position of each field
type DirectoryEntry struct {
	Tag      string // Field tag identifier (e.g., "0001", "DSID")
	Length   int    // Field length in bytes
	Position int    // Field position offset from field area start
}

// FieldControl defines the internal structure of a field from the DDR
// IHO S-57 Part 3 Annex A.2.4.1: Field control field (Table A.5), Data descriptive field (Table A.6), Field controls (Table A.7)
type FieldControl struct {
	Tag            string         // Field tag this control applies to
	DataStructCode byte           // 0=elementary, 1=vector, 2=array
	DataTypeCode   byte           // 0=char, 1=implicit, 5=binary
	FieldName      string         // Human-readable field name
	Subfields      []*SubfieldDef // Subfield definitions
	FormatControls string         // Format string
}

// SubfieldDef defines a subfield within a field
type SubfieldDef struct {
	Label      string // Subfield label/name
	FormatType byte   // Data type (A=ASCII, I=integer, B=binary, etc.)
	Width      int    // Field width (0 = variable)
}
