package s101

import (
	"strconv"

	"github.com/beetlebugorg/chartplotter/pkg/s100/fc"
	lua "github.com/yuin/gopher-lua"
)

// bindHost registers every Host* callback the S-101 framework calls. Type
// introspection is backed by the Feature Catalogue (e.cat); per-feature data is
// backed by the adapted features set in Portray.
func (e *Engine) bindHost() {
	L := e.L

	set := func(name string, fn lua.LGFunction) { L.SetGlobal(name, L.NewFunction(fn)) }
	emptyArray := func(s *lua.LState) int { s.Push(s.NewTable()); return 1 }

	// --- type code lists (GetTypeInfo iterates these) ---
	set("HostGetFeatureTypeCodes", func(s *lua.LState) int {
		s.Push(keysOfFeatureTypes(s, e.cat))
		return 1
	})
	set("HostGetSimpleAttributeTypeCodes", func(s *lua.LState) int {
		s.Push(keysOfSimpleAttrs(s, e.cat))
		return 1
	})
	set("HostGetInformationTypeCodes", func(s *lua.LState) int {
		t := s.NewTable()
		for code := range e.cat.InformationTypes {
			t.Append(lua.LString(code))
		}
		s.Push(t)
		return 1
	})
	set("HostGetComplexAttributeTypeCodes", func(s *lua.LState) int {
		t := s.NewTable()
		for code := range e.cat.ComplexAttrs {
			t.Append(lua.LString(code))
		}
		s.Push(t)
		return 1
	})
	set("HostGetRoleTypeCodes", emptyArray)
	set("HostGetInformationAssociationTypeCodes", emptyArray)
	set("HostGetFeatureAssociationTypeCodes", emptyArray)

	// --- per-code type info (fetched lazily) ---
	typeInfo := func(s *lua.LState, kind, code string, binds []fc.AttributeBinding) *lua.LTable {
		info := s.NewTable()
		s.SetField(info, "Type", lua.LString(kind))
		s.SetField(info, "Code", lua.LString(code))
		s.SetField(info, "AttributeBindings", bindingsTable(s, binds))
		return info
	}
	set("HostGetFeatureTypeInfo", func(s *lua.LState) int {
		code := s.CheckString(1)
		var binds []fc.AttributeBinding
		if ft := e.cat.FeatureTypes[code]; ft != nil {
			binds = ft.Bindings
		}
		binds = withGuaranteed(binds)
		s.Push(typeInfo(s, "FeatureTypeInfo", code, binds))
		return 1
	})
	set("HostGetSimpleAttributeTypeInfo", func(s *lua.LState) int {
		code := s.CheckString(1)
		info := s.NewTable()
		s.SetField(info, "Type", lua.LString("SimpleAttributeInfo"))
		s.SetField(info, "Code", lua.LString(code))
		if sa := e.cat.SimpleAttrs[code]; sa != nil {
			s.SetField(info, "ValueType", lua.LString(sa.ValueType))
		}
		s.Push(info)
		return 1
	})
	set("HostGetInformationTypeInfo", func(s *lua.LState) int {
		code := s.CheckString(1)
		var binds []fc.AttributeBinding
		if it := e.cat.InformationTypes[code]; it != nil {
			binds = it.Bindings
		}
		s.Push(typeInfo(s, "InformationTypeInfo", code, binds))
		return 1
	})
	set("HostGetComplexAttributeTypeInfo", func(s *lua.LState) int {
		code := s.CheckString(1)
		var binds []fc.AttributeBinding
		if ca := e.cat.ComplexAttrs[code]; ca != nil {
			binds = ca.Bindings
		}
		s.Push(typeInfo(s, "ComplexAttributeInfo", code, binds))
		return 1
	})

	// --- dataset / feature data ---
	set("HostGetFeatureIDs", func(s *lua.LState) int {
		t := s.NewTable()
		for _, id := range e.order {
			t.Append(lua.LString(id))
		}
		s.Push(t)
		return 1
	})
	set("HostFeatureGetCode", func(s *lua.LState) int {
		if a := e.adapted[s.CheckString(1)]; a != nil {
			s.Push(lua.LString(a.code))
		} else {
			s.Push(lua.LString(""))
		}
		return 1
	})
	// _HostFeaturePrimitive backs the Lua-side HostFeatureGetSpatialAssociations
	// glue (installed after the framework loads), which builds proper
	// SpatialAssociation objects via the framework constructors. Returns the
	// feature's primitive ("Point"|"Curve"|"Surface"), or "" if unknown.
	set("_HostFeaturePrimitive", func(s *lua.LState) int {
		if a := e.adapted[s.CheckString(1)]; a != nil {
			s.Push(lua.LString(a.primitive))
		} else {
			s.Push(lua.LString(""))
		}
		return 1
	})
	// _HostFeaturePoints backs the MultiPoint spatial glue (HostGetSpatial '#M'):
	// returns the feature's multipoint vertices as an array of {x,y,z} string
	// triples (CreatePoint takes strings), x=lon y=lat z=depth. Used for SOUNDG.
	set("_HostFeaturePoints", func(s *lua.LState) int {
		t := s.NewTable()
		if a := e.adapted[s.CheckString(1)]; a != nil {
			for _, p := range a.points {
				row := s.NewTable()
				row.Append(lua.LString(strconv.FormatFloat(p[0], 'f', -1, 64)))
				row.Append(lua.LString(strconv.FormatFloat(p[1], 'f', -1, 64)))
				row.Append(lua.LString(strconv.FormatFloat(p[2], 'f', -1, 64)))
				t.Append(row)
			}
		}
		s.Push(t)
		return 1
	})
	set("HostFeatureGetSimpleAttribute", func(s *lua.LState) int {
		// (featureID, attributePath, attributeCode) -> array of value strings.
		// Served from the synthesized attribute tree: resolve the container the
		// path points at, then return that node's simple values for the code.
		id := s.CheckString(1)
		path := s.CheckString(2)
		code := s.CheckString(3)
		t := s.NewTable()
		if a := e.adapted[id]; a != nil && a.root != nil {
			if node := a.root.resolve(path); node != nil {
				for _, v := range node.simple[code] {
					t.Append(lua.LString(v))
				}
			}
		}
		s.Push(t)
		return 1
	})
	set("HostFeatureGetComplexAttributeCount", func(s *lua.LState) int {
		// (featureID, attributePath, attributeCode) -> count of that complex
		// attribute's instances at the container the path points at.
		id := s.CheckString(1)
		path := s.CheckString(2)
		code := s.CheckString(3)
		n := 0
		if a := e.adapted[id]; a != nil && a.root != nil {
			if node := a.root.resolve(path); node != nil {
				n = len(node.children[code])
			}
		}
		s.Push(lua.LNumber(n))
		return 1
	})
	set("HostFeatureGetAssociatedFeatureIDs", emptyArray)
	set("HostFeatureGetAssociatedInformationIDs", emptyArray)
	// HostFeatureGetSpatialAssociations + HostGetSpatial are defined in Lua glue
	// (installSpatialGlue) after the framework loads, so they can use the
	// framework's CreateSpatialAssociation/CreateSurface constructors.
	set("HostSpatialGetAssociatedFeatureIDs", emptyArray)
	set("HostSpatialGetAssociatedInformationIDs", emptyArray)
	set("HostInformationTypeGetCode", func(s *lua.LState) int { s.Push(lua.LString("")); return 1 })
	set("HostInformationTypeGetSimpleAttribute", emptyArray)
	set("HostInformationTypeGetComplexAttributeCount", func(s *lua.LState) int { s.Push(lua.LNumber(0)); return 1 })

	set("HostPortrayalEmit", func(s *lua.LState) int { s.Push(lua.LTrue); return 1 })
	set("HostDebuggerEntry", func(*lua.LState) int { return 0 })
}

// clearances maps each S-101 clearance complex attribute to the S-57 simple
// attribute that backs it and the value sub-attribute the rules read. Bridges
// carry clearances as S-57 simple attributes (VERCCL/VERCLR/HORCLR/VERCOP); the
// S-101 catalogue models them as complex attributes wrapping a *Value field.
var clearances = map[string]struct{ s57, value string }{
	"verticalClearanceClosed":  {"VERCCL", "verticalClearanceValue"},
	"verticalClearanceFixed":   {"VERCLR", "verticalClearanceValue"},
	"verticalClearanceOpen":    {"VERCOP", "verticalClearanceValue"},
	"horizontalClearanceFixed": {"HORCLR", "horizontalClearanceValue"},
}

// guaranteedAttrs are attributes some DRAFT-catalogue rules read WITHOUT the
// nil-safe `!` prefix on feature types the catalogue doesn't bind them to — so
// the framework raises "Invalid attribute code". Binding them on every feature
// type makes such a read return nil (attribute absent) instead of erroring:
//   - inTheWater: read by Building, SlopeTopline, … (boolean).
//   - orientationValue: read by route/traffic rules (RadarLine, RecommendedTrack,
//     TwoWayRoutePart, …) that don't all bind it (the S-57 ORIENT alias).
//   - topmark: the TOPMAR02 CSP reads feature.topmark, but some classes that call
//     it (e.g. MooringBuoy) don't bind it. It's a complex attribute; the injected
//     Upper:1 binding resolves it as a single-valued complex (see LookupAttributeValue).
var guaranteedAttrs = []string{"inTheWater", "orientationValue", "topmark"}

// withGuaranteed appends any guaranteedAttrs binding the feature type is missing.
func withGuaranteed(binds []fc.AttributeBinding) []fc.AttributeBinding {
	out := binds
	for _, name := range guaranteedAttrs {
		found := false
		for _, b := range out {
			if b.AttributeRef == name {
				found = true
				break
			}
		}
		if !found {
			out = append(out[:len(out):len(out)], fc.AttributeBinding{AttributeRef: name, Lower: 0, Upper: 1})
		}
	}
	return out
}

// bindingsTable builds the AttributeBindings table (attr code → {Upper,Lower
// Multiplicity}) the framework reads to validate attribute access + decide
// single-valued vs array. Infinite upper is mapped to a large number.
func bindingsTable(s *lua.LState, binds []fc.AttributeBinding) *lua.LTable {
	t := s.NewTable()
	for _, b := range binds {
		upper := b.Upper
		if upper < 0 {
			upper = 1 << 30
		}
		bt := s.NewTable()
		s.SetField(bt, "UpperMultiplicity", lua.LNumber(upper))
		s.SetField(bt, "LowerMultiplicity", lua.LNumber(b.Lower))
		s.SetField(t, b.AttributeRef, bt)
	}
	return t
}

func keysOfFeatureTypes(s *lua.LState, cat *fc.Catalogue) *lua.LTable {
	t := s.NewTable()
	for code := range cat.FeatureTypes {
		t.Append(lua.LString(code))
	}
	return t
}

func keysOfSimpleAttrs(s *lua.LState, cat *fc.Catalogue) *lua.LTable {
	t := s.NewTable()
	for code := range cat.SimpleAttrs {
		t.Append(lua.LString(code))
	}
	return t
}
