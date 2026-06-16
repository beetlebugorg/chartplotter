package s57

// AttributeSchema defines essential and conditional attributes for an S-57 object class.
// This is based on the S-57 Object Catalogue and common cartographic practice.
type AttributeSchema struct {
	// ObjectClass is the S-57 object class code (e.g., "DEPARE", "LIGHTS")
	ObjectClass string

	// Essential attributes that should always be encoded/displayed
	Essential []string

	// Conditional attributes that should be encoded if present
	Conditional map[string]bool

	// ArrayAttributes that contain multiple values (need special encoding)
	ArrayAttributes []string
}

// GetAttributeSchema returns the attribute schema for a given S-57 object class.
// Returns nil if no schema is defined for the object class.
//
// These schemas define which attributes are cartographically significant for
// each object class, based on S-57 Object Catalogue and IHO S-52 presentation rules.
func GetAttributeSchema(objectClass string) *AttributeSchema {
	schema, exists := attributeSchemas[objectClass]
	if !exists {
		return nil
	}
	return &schema
}

// GetEssentialAttributes returns the list of essential attributes for an object class.
// If no schema exists, returns a default set of common navigation attributes.
func GetEssentialAttributes(objectClass string) []string {
	schema := GetAttributeSchema(objectClass)
	if schema == nil {
		// Default essential attributes for unknown object classes
		return []string{"OBJL", "SCAMIN"}
	}
	return schema.Essential
}

// IsArrayAttribute checks if an attribute is known to contain array values.
func IsArrayAttribute(objectClass string, attributeName string) bool {
	schema := GetAttributeSchema(objectClass)
	if schema == nil {
		return false
	}

	for _, attr := range schema.ArrayAttributes {
		if attr == attributeName {
			return true
		}
	}
	return false
}

// attributeSchemas maps S-57 object classes to their attribute schemas.
// Based on IHO S-57 Object Catalogue Edition 3.1 and S-52 presentation rules.
var attributeSchemas = map[string]AttributeSchema{
	"DEPARE": {
		ObjectClass:     "DEPARE",
		Essential:       []string{"DRVAL1", "DRVAL2"},
		Conditional:     map[string]bool{"QUASOU": true, "TECSOU": true},
		ArrayAttributes: []string{},
	},
	"DRGARE": {
		ObjectClass:     "DRGARE",
		Essential:       []string{"DRVAL1", "DRVAL2"},
		Conditional:     map[string]bool{},
		ArrayAttributes: []string{},
	},
	"SOUNDG": {
		ObjectClass:     "SOUNDG",
		Essential:       []string{"OBJL"},
		Conditional:     map[string]bool{"QUASOU": true, "TECSOU": true, "STATUS": true},
		ArrayAttributes: []string{"DEPTHS"},
	},
	"DEPCNT": {
		ObjectClass:     "DEPCNT",
		Essential:       []string{"VALDCO"},
		Conditional:     map[string]bool{},
		ArrayAttributes: []string{},
	},
	"BOYLAT": {
		ObjectClass:     "BOYLAT",
		Essential:       []string{"BOYSHP", "COLOUR", "COLPAT"},
		Conditional:     map[string]bool{"CATLIT": true, "MARSYS": true},
		ArrayAttributes: []string{"COLOUR"},
	},
	"BOYSAW": {
		ObjectClass:     "BOYSAW",
		Essential:       []string{"BOYSHP", "COLOUR"},
		Conditional:     map[string]bool{"COLPAT": true},
		ArrayAttributes: []string{"COLOUR"},
	},
	"BOYCAR": {
		ObjectClass:     "BOYCAR",
		Essential:       []string{"BOYSHP", "COLOUR", "COLPAT"},
		Conditional:     map[string]bool{"CATCAM": true},
		ArrayAttributes: []string{"COLOUR"},
	},
	"BOYISD": {
		ObjectClass:     "BOYISD",
		Essential:       []string{"BOYSHP", "COLOUR"},
		Conditional:     map[string]bool{"COLPAT": true},
		ArrayAttributes: []string{"COLOUR"},
	},
	"BOYSPP": {
		ObjectClass:     "BOYSPP",
		Essential:       []string{"BOYSHP", "COLOUR"},
		Conditional:     map[string]bool{"COLPAT": true, "CATSPM": true},
		ArrayAttributes: []string{"COLOUR"},
	},
	"BCNLAT": {
		ObjectClass:     "BCNLAT",
		Essential:       []string{"BCNSHP", "COLOUR"},
		Conditional:     map[string]bool{"COLPAT": true},
		ArrayAttributes: []string{"COLOUR"},
	},
	"BCNCAR": {
		ObjectClass:     "BCNCAR",
		Essential:       []string{"BCNSHP", "COLOUR", "COLPAT"},
		Conditional:     map[string]bool{"CATCAM": true},
		ArrayAttributes: []string{"COLOUR"},
	},
	"BCNSAW": {
		ObjectClass:     "BCNSAW",
		Essential:       []string{"BCNSHP", "COLOUR"},
		Conditional:     map[string]bool{"COLPAT": true},
		ArrayAttributes: []string{"COLOUR"},
	},
	"BCNISD": {
		ObjectClass:     "BCNISD",
		Essential:       []string{"BCNSHP", "COLOUR"},
		Conditional:     map[string]bool{"COLPAT": true},
		ArrayAttributes: []string{"COLOUR"},
	},
	"BCNSPP": {
		ObjectClass:     "BCNSPP",
		Essential:       []string{"BCNSHP", "COLOUR"},
		Conditional:     map[string]bool{"COLPAT": true, "CATSPM": true},
		ArrayAttributes: []string{"COLOUR"},
	},
	"LIGHTS": {
		ObjectClass:     "LIGHTS",
		Essential:       []string{"CATLIT", "COLOUR"},
		Conditional:     map[string]bool{"LITCHR": true, "HEIGHT": true, "VALNMR": true, "SIGPER": true},
		ArrayAttributes: []string{"COLOUR"},
	},
	"LNDARE": {
		ObjectClass:     "LNDARE",
		Essential:       []string{"OBJL"},
		Conditional:     map[string]bool{"CATLND": true},
		ArrayAttributes: []string{},
	},
	"OBSTRN": {
		ObjectClass:     "OBSTRN",
		Essential:       []string{"CATOBS", "VALSOU"},
		Conditional:     map[string]bool{"WATLEV": true, "QUASOU": true},
		ArrayAttributes: []string{},
	},
	"UWTROC": {
		ObjectClass:     "UWTROC",
		Essential:       []string{"VALSOU", "WATLEV"},
		Conditional:     map[string]bool{"QUASOU": true},
		ArrayAttributes: []string{},
	},
	"WRECKS": {
		ObjectClass:     "WRECKS",
		Essential:       []string{"CATWRK", "VALSOU"},
		Conditional:     map[string]bool{"WATLEV": true, "QUASOU": true},
		ArrayAttributes: []string{},
	},
	"RESARE": {
		ObjectClass:     "RESARE",
		Essential:       []string{"CATREA"},
		Conditional:     map[string]bool{"RESTRN": true},
		ArrayAttributes: []string{"RESTRN"},
	},
	"ACHARE": {
		ObjectClass:     "ACHARE",
		Essential:       []string{"CATACH"},
		Conditional:     map[string]bool{},
		ArrayAttributes: []string{},
	},
	"CBLARE": {
		ObjectClass:     "CBLARE",
		Essential:       []string{"CATCBL"},
		Conditional:     map[string]bool{"RESTRN": true},
		ArrayAttributes: []string{},
	},
	"PIPARE": {
		ObjectClass:     "PIPARE",
		Essential:       []string{"CATPIP"},
		Conditional:     map[string]bool{"PRODCT": true},
		ArrayAttributes: []string{"PRODCT"},
	},
	"TOPMAR": {
		ObjectClass:     "TOPMAR",
		Essential:       []string{"TOPSHP"},
		Conditional:     map[string]bool{"COLOUR": true},
		ArrayAttributes: []string{"COLOUR"},
	},
}
