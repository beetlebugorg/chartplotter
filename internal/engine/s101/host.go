package s101

import (
	"strconv"
	"strings"

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
		binds = withInTheWater(binds)
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
		id := s.CheckString(1)
		path := s.CheckString(2)
		code := s.CheckString(3)
		t := s.NewTable()
		a := e.adapted[id]
		if a == nil {
			s.Push(t)
			return 1
		}
		// featureName complex sub-attributes (path "featureName:1") from OBJNAM.
		if strings.HasPrefix(path, "featureName") {
			switch code {
			case "name":
				if a.name != "" {
					t.Append(lua.LString(a.name))
				}
			case "language":
				t.Append(lua.LString("eng"))
			case "nameUsage":
				t.Append(lua.LString("1")) // default → selected even if language differs
			}
			s.Push(t)
			return 1
		}
		// Clearance complex sub-attributes (e.g. path "verticalClearanceClosed:1",
		// code "verticalClearanceValue"): synthesise from the backing S-57 simple
		// attribute (VERCCL etc.) so the bridge rules can read the value.
		if cl := clearanceForPath(path); cl != nil && code == cl.value {
			if v := a.s57[cl.s57]; v != "" {
				t.Append(lua.LString(v))
			}
			s.Push(t)
			return 1
		}
		if v, ok := a.attrs[code]; ok {
			t.Append(lua.LString(v))
		}
		s.Push(t)
		return 1
	})
	set("HostFeatureGetComplexAttributeCount", func(s *lua.LState) int {
		id := s.CheckString(1)
		code := s.CheckString(3)
		n := 0
		a := e.adapted[id]
		switch {
		case code == "featureName":
			if a != nil && a.name != "" {
				n = 1
			}
		case clearances[code].value != "":
			// A clearance complex attribute is present when its backing S-57
			// attribute is, OR — for an opening bridge's verticalClearanceClosed —
			// always, so SpanOpening's unguarded `verticalClearanceClosed.
			// verticalClearanceValue` deref (a DRAFT-catalogue bug) reads a nil
			// value (→ hazard alert) instead of crashing on a nil table.
			if a != nil && (a.s57[clearances[code].s57] != "" ||
				(code == "verticalClearanceClosed" && a.objClass == "BRIDGE")) {
				n = 1
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

// clearanceForPath returns the clearance mapping for a complex-attribute path
// like "verticalClearanceClosed:1" (the head before ':'), or nil if the path is
// not a clearance complex attribute.
func clearanceForPath(path string) *struct{ s57, value string } {
	head, _, _ := strings.Cut(path, ":")
	if c, ok := clearances[head]; ok {
		return &c
	}
	return nil
}

// withInTheWater guarantees the boolean `inTheWater` attribute is bound on every
// feature type. Several DRAFT-catalogue rules (Building, SlopeTopline, …) read
// `feature.inTheWater` WITHOUT the nil-safe `!` prefix, but the feature
// catalogue only binds it to some of those classes — so on the others the
// framework raises "Invalid attribute code". Binding it everywhere makes the
// read return nil (the attribute is simply absent) instead of erroring, which is
// the intended "not in the water" outcome.
func withInTheWater(binds []fc.AttributeBinding) []fc.AttributeBinding {
	for _, b := range binds {
		if b.AttributeRef == "inTheWater" {
			return binds
		}
	}
	return append(binds[:len(binds):len(binds)], fc.AttributeBinding{AttributeRef: "inTheWater", Lower: 0, Upper: 1})
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
