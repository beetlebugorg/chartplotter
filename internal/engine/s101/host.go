package s101

import (
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
	set("HostFeatureGetSimpleAttribute", func(s *lua.LState) int {
		// (featureID, attributePath, attributeCode) -> array of value strings.
		id := s.CheckString(1)
		code := s.CheckString(3)
		t := s.NewTable()
		if a := e.adapted[id]; a != nil {
			if v, ok := a.attrs[code]; ok {
				t.Append(lua.LString(v))
			}
		}
		s.Push(t)
		return 1
	})
	set("HostFeatureGetComplexAttributeCount", func(s *lua.LState) int {
		s.Push(lua.LNumber(0))
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
