// Package fc parses the IHO S-101 Feature Catalogue (S100FC/5.2) into a type
// registry, and — crucially for the S-57→S-101 bridge (see
// specs/s101-portrayal-backport.md, Workstream E) — exposes the alias mapping
// the catalogue ships: every feature type and attribute carries its legacy S-57
// 6-char acronym in <alias>, so the name half of the bridge is derived directly
// from this one file rather than a separate conversion table. Enumerated values
// keep S-57's integer codes, so attribute values pass through unchanged.
//
// The registry also backs the Lua portrayal engine's HostGet*TypeInfo /
// HostGet*TypeCodes introspection callbacks (Workstream D host contract).
package fc

import (
	"encoding/xml"
	"fmt"
	"os"
	"strings"
)

// ListedValue is one enumeration member (S-57-compatible integer code + label).
type ListedValue struct {
	Code  int
	Label string
}

// SimpleAttribute is an S-101 simple attribute type.
type SimpleAttribute struct {
	Code         string // camelCase S-101 code, e.g. beaconShape
	Aliases      []string
	Name         string
	ValueType    string // boolean | enumeration | integer | real | text | date | ...
	ListedValues []ListedValue
}

// AttributeBinding is a feature type's use of an attribute, with multiplicity.
type AttributeBinding struct {
	AttributeRef    string
	Lower           int
	Upper           int // -1 == infinite
	PermittedValues []int
}

// FeatureType is an S-101 feature type.
type FeatureType struct {
	Code       string // camelCase S-101 code, e.g. Anchorage
	Aliases    []string
	Name       string
	Abstract   bool
	Primitives []string // point | curve | surface
	Bindings   []AttributeBinding
}

// Catalogue is the parsed feature catalogue plus reverse (alias→code) indexes.
type Catalogue struct {
	FeatureTypes map[string]*FeatureType
	SimpleAttrs  map[string]*SimpleAttribute

	featureByAlias map[string]string // S-57 OBJL acronym → feature code
	attrByAlias    map[string]string // S-57 attribute acronym → attribute code
}

// FeatureCodeForS57 maps an S-57 object-class acronym (e.g. ACHARE) to its
// S-101 feature code (e.g. Anchorage).
func (c *Catalogue) FeatureCodeForS57(objl string) (string, bool) {
	code, ok := c.featureByAlias[strings.ToUpper(objl)]
	return code, ok
}

// AttrCodeForS57 maps an S-57 attribute acronym (e.g. DRVAL1) to its S-101
// attribute code (e.g. depthRangeMinimumValue).
func (c *Catalogue) AttrCodeForS57(acronym string) (string, bool) {
	code, ok := c.attrByAlias[strings.ToUpper(acronym)]
	return code, ok
}

// --- XML shapes ---

type xmlCatalogue struct {
	SimpleAttributes struct {
		Items []xmlSimpleAttr `xml:"S100_FC_SimpleAttribute"`
	} `xml:"S100_FC_SimpleAttributes"`
	FeatureTypes struct {
		Items []xmlFeatureType `xml:"S100_FC_FeatureType"`
	} `xml:"S100_FC_FeatureTypes"`
}

type xmlSimpleAttr struct {
	Name         string   `xml:"name"`
	Code         string   `xml:"code"`
	Aliases      []string `xml:"alias"`
	ValueType    string   `xml:"valueType"`
	ListedValues struct {
		Items []struct {
			Label string `xml:"label"`
			Code  int    `xml:"code"`
		} `xml:"listedValue"`
	} `xml:"listedValues"`
}

type xmlFeatureType struct {
	Abstract   string   `xml:"isAbstract,attr"`
	Name       string   `xml:"name"`
	Code       string   `xml:"code"`
	Aliases    []string `xml:"alias"`
	Primitives []string `xml:"permittedPrimitives"`
	Bindings   []struct {
		Multiplicity struct {
			Lower int `xml:"lower"`
			Upper struct {
				Infinite string `xml:"infinite,attr"`
				Value    int    `xml:",chardata"`
			} `xml:"upper"`
		} `xml:"multiplicity"`
		PermittedValues struct {
			Values []int `xml:"value"`
		} `xml:"permittedValues"`
		Attribute struct {
			Ref string `xml:"ref,attr"`
		} `xml:"attribute"`
	} `xml:"attributeBinding"`
}

// Load parses a FeatureCatalogue.xml file into a Catalogue.
func Load(path string) (*Catalogue, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var x xmlCatalogue
	if err := xml.Unmarshal(data, &x); err != nil {
		return nil, fmt.Errorf("parse feature catalogue: %w", err)
	}

	c := &Catalogue{
		FeatureTypes:   map[string]*FeatureType{},
		SimpleAttrs:    map[string]*SimpleAttribute{},
		featureByAlias: map[string]string{},
		attrByAlias:    map[string]string{},
	}

	for _, sa := range x.SimpleAttributes.Items {
		attr := &SimpleAttribute{
			Code:      sa.Code,
			Aliases:   sa.Aliases,
			Name:      sa.Name,
			ValueType: sa.ValueType,
		}
		for _, lv := range sa.ListedValues.Items {
			attr.ListedValues = append(attr.ListedValues, ListedValue{Code: lv.Code, Label: lv.Label})
		}
		c.SimpleAttrs[sa.Code] = attr
		for _, a := range sa.Aliases {
			c.attrByAlias[strings.ToUpper(a)] = sa.Code
		}
	}

	for _, ft := range x.FeatureTypes.Items {
		f := &FeatureType{
			Code:       ft.Code,
			Aliases:    ft.Aliases,
			Name:       ft.Name,
			Abstract:   ft.Abstract == "true",
			Primitives: ft.Primitives,
		}
		for _, b := range ft.Bindings {
			upper := b.Multiplicity.Upper.Value
			if b.Multiplicity.Upper.Infinite == "true" {
				upper = -1
			}
			f.Bindings = append(f.Bindings, AttributeBinding{
				AttributeRef:    b.Attribute.Ref,
				Lower:           b.Multiplicity.Lower,
				Upper:           upper,
				PermittedValues: b.PermittedValues.Values,
			})
		}
		c.FeatureTypes[ft.Code] = f
		for _, a := range ft.Aliases {
			c.featureByAlias[strings.ToUpper(a)] = ft.Code
		}
	}

	return c, nil
}
