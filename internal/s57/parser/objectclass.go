package parser

import (
	_ "embed"
	"encoding/csv"
	"fmt"
	"strconv"
	"strings"
	"sync"
)

// S-57 Object Class lookup table
// Source: IHO S-57 Edition 3.1 Appendix A - Object Catalogue (verified against 31ApAch1.pdf)
var objectClassNames = map[int]string{
	1:   "ADMARE",
	2:   "AIRARE",
	3:   "ACHBRT",
	4:   "ACHARE",
	5:   "BCNCAR",
	6:   "BCNISD",
	7:   "BCNLAT",
	8:   "BCNSAW",
	9:   "BCNSPP",
	10:  "BERTHS",
	11:  "BRIDGE",
	12:  "BUISGL",
	13:  "BUAARE",
	14:  "BOYCAR",
	15:  "BOYINB",
	16:  "BOYISD",
	17:  "BOYLAT",
	18:  "BOYSAW",
	19:  "BOYSPP",
	20:  "CBLARE",
	21:  "CBLOHD",
	22:  "CBLSUB",
	23:  "CANALS",
	24:  "CANBNK",
	25:  "CTSARE",
	26:  "CAUSWY",
	27:  "CTNARE",
	28:  "CHKPNT",
	29:  "CGUSTA",
	30:  "COALNE",
	31:  "CONZNE",
	32:  "COSARE",
	33:  "CTRPNT",
	34:  "CONVYR",
	35:  "CRANES",
	36:  "CURENT",
	37:  "CUSZNE",
	38:  "DAMCON",
	39:  "DAYMAR",
	40:  "DWRTCL",
	41:  "DWRTPT",
	42:  "DEPARE",
	43:  "DEPCNT",
	44:  "DISMAR",
	45:  "DOCARE",
	46:  "DRGARE",
	47:  "DRYDOC",
	48:  "DMPGRD",
	49:  "DYKCON",
	50:  "EXEZNE",
	51:  "FAIRWY",
	52:  "FNCLNE",
	53:  "FERYRT",
	54:  "FSHZNE",
	55:  "FSHFAC",
	56:  "FSHGRD",
	57:  "FLODOC",
	58:  "FOGSIG",
	59:  "FORSTC",
	60:  "FRPARE",
	61:  "GATCON",
	62:  "GRIDRN",
	63:  "HRBARE",
	64:  "HRBFAC",
	65:  "HULKES",
	66:  "ICEARE",
	67:  "ICNARE",
	68:  "ISTZNE",
	69:  "LAKARE",
	70:  "LAKSHR",
	71:  "LNDARE",
	72:  "LNDELV",
	73:  "LNDRGN",
	74:  "LNDMRK",
	75:  "LIGHTS",
	76:  "LITFLT",
	77:  "LITVES",
	78:  "LOCMAG",
	79:  "LOKBSN",
	80:  "LOGPON",
	81:  "MAGVAR",
	82:  "MARCUL",
	83:  "MIPARE",
	84:  "MORFAC",
	85:  "NAVLNE",
	86:  "OBSTRN",
	87:  "OFSPLF",
	88:  "OSPARE",
	89:  "OILBAR",
	90:  "PILPNT",
	91:  "PILBOP",
	92:  "PIPARE",
	93:  "PIPOHD",
	94:  "PIPSOL",
	95:  "PONTON",
	96:  "PRCARE",
	97:  "PRDARE",
	98:  "PYLONS",
	99:  "RADLNE",
	100: "RADRNG",
	101: "RADRFL",
	102: "RADSTA",
	103: "RTPBCN",
	104: "RDOCAL",
	105: "RDOSTA",
	106: "RAILWY",
	107: "RAPIDS",
	108: "RCRTCL",
	109: "RECTRC",
	110: "RCTLPT",
	111: "RSCSTA",
	112: "RESARE",
	113: "RETRFL",
	114: "RIVERS",
	115: "RIVBNK",
	116: "ROADWY",
	117: "RUNWAY",
	118: "SNDWAV",
	119: "SEAARE",
	120: "SPLARE",
	121: "SBDARE",
	122: "SLCONS",
	123: "SISTAT",
	124: "SISTAW",
	125: "SILTNK",
	126: "SLOTOP",
	127: "SLOGRD",
	128: "SMCFAC",
	129: "SOUNDG",
	130: "SPRING",
	131: "SQUARE",
	132: "STSLNE",
	133: "SUBTLN",
	134: "SWPARE",
	135: "TESARE",
	136: "TS_PRH",
	137: "TS_PNH",
	138: "TS_PAD",
	139: "TS_TIS",
	140: "T_HMON",
	141: "T_NHMN",
	142: "T_TIMS",
	143: "TIDEWY",
	144: "TOPMAR",
	145: "TSELNE",
	146: "TSSBND",
	147: "TSSCRS",
	148: "TSSLPT",
	149: "TSSRON",
	150: "TSEZNE",
	151: "TUNNEL",
	152: "TWRTPT",
	153: "UWTROC",
	154: "UNSARE",
	155: "VEGATN",
	156: "WATTUR",
	157: "WATFAL",
	158: "WEDKLP",
	159: "WRECKS",
	160: "TS_FEB", // Tidal stream - flood/ebb (S-57 App A §1.173; was missing → rendered QUESMRK)
	161: "ARCSLN", // Archipelagic Sea Lane (area)  → S-101 ArchipelagicSeaLaneArea
	162: "ASLXIS", // Archipelagic Sea Lane Axis (line) → S-101 ArchipelagicSeaLaneAxis
	// NEWOBJ (code 163) is not in the base Ed 3.1 catalogue but is the ENC
	// convention for producer "new objects"; it carries SYMINS and the PresLib
	// NEWOBJ lookup routes SYMINS-bearing features through CS(SYMINS02). Used by
	// the S-64 test data (V-AIS, temporary/preliminary NtoM).
	163: "NEWOBJ",
	300: "M_ACCY",
	301: "M_CSCL",
	302: "M_COVR",
	303: "M_HDAT",
	304: "M_HOPA",
	305: "M_NPUB",
	306: "M_NSYS",
	307: "M_PROD",
	308: "M_QUAL",
	309: "M_SDAT",
	310: "M_SREL",
	311: "M_UNIT",
	312: "M_VDAT",
	400: "C_AGGR",
	401: "C_ASSO",
	402: "C_STAC",
}

// S-57 attribute catalogue CSV from GDAL project
// Source: https://gdal.org/ - licensed under MIT/X11
// This file maps S-57 attribute codes to their standard acronyms per IHO S-57 Appendix A Chapter 2
//
//go:embed s57attributes.csv
var s57AttributesCSV string

var (
	attributeNames     map[int]string
	attributeNamesOnce sync.Once
)

// loadAttributeNames loads the S-57 attribute catalogue from embedded CSV
func loadAttributeNames() {
	attributeNames = make(map[int]string)

	reader := csv.NewReader(strings.NewReader(s57AttributesCSV))
	records, err := reader.ReadAll()
	if err != nil {
		// Fall back to empty map on error
		return
	}

	// Skip header row
	for _, record := range records[1:] {
		if len(record) < 3 {
			continue
		}

		// Parse: Code, Attribute, Acronym, ...
		code, err := strconv.Atoi(strings.Trim(record[0], "\""))
		if err != nil {
			continue
		}

		acronym := strings.Trim(record[2], "\"")
		if acronym != "" {
			attributeNames[code] = acronym
		}
	}
}

// AttributeCodeToString converts S-57 numeric attribute code to string acronym
// S-57 Appendix A Chapter 2: Attribute Catalogue
func AttributeCodeToString(code int) string {
	// Lazy load attribute names from CSV
	attributeNamesOnce.Do(loadAttributeNames)

	if name, ok := attributeNames[code]; ok {
		return name
	}
	// Unknown attribute - return generic code
	return fmt.Sprintf("ATTR_%d", code)
}

// ObjectClassToString converts S-57 numeric object class to string code
// The object class codes are read from the S-57 file's FRID records
// and mapped using the S-57 Object Catalogue
// S-57 Appendix A: Object Catalogue
func ObjectClassToString(code int) (string, error) {
	if code <= 0 {
		return "", &ErrUnknownObjectClass{Code: code}
	}

	// Look up in object class table
	if name, ok := objectClassNames[code]; ok {
		return name, nil
	}

	// Unknown object class - return numeric code
	return fmt.Sprintf("OBJL_%d", code), nil
}

// ObjectClassToInt converts string code to numeric object class
// S-57 object classes are identified by numeric codes in the binary data
func ObjectClassToInt(code string) (int, error) {
	// TODO: Implement reverse lookup from object catalogue
	return 0, fmt.Errorf("object class lookup not yet implemented: %s", code)
}

// IsSupported checks if an object class code is valid
// All object classes defined in S-57 standard are "supported" for parsing
// The question is whether we have rendering logic for them (deferred to later iterations)
func IsSupported(code int) bool {
	// Any positive object class code is valid for parsing
	// We parse all object classes generically (geometry + attributes)
	// Rendering/styling logic will filter which ones to display
	return code > 0
}
