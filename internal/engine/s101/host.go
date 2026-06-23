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
	set("HostGetInformationTypeCodes", emptyArray)
	set("HostGetComplexAttributeTypeCodes", emptyArray)
	set("HostGetRoleTypeCodes", emptyArray)
	set("HostGetInformationAssociationTypeCodes", emptyArray)
	set("HostGetFeatureAssociationTypeCodes", emptyArray)

	// --- per-code type info (fetched lazily) ---
	set("HostGetFeatureTypeInfo", func(s *lua.LState) int {
		code := s.CheckString(1)
		ft := e.cat.FeatureTypes[code]
		info := s.NewTable()
		s.SetField(info, "Type", lua.LString("FeatureTypeInfo"))
		s.SetField(info, "Code", lua.LString(code))
		bindings := s.NewTable()
		if ft != nil {
			for _, b := range ft.Bindings {
				bt := s.NewTable()
				upper := b.Upper
				if upper < 0 {
					upper = 1 << 30 // "infinite"
				}
				s.SetField(bt, "UpperMultiplicity", lua.LNumber(upper))
				s.SetField(bt, "LowerMultiplicity", lua.LNumber(b.Lower))
				s.SetField(bindings, b.AttributeRef, bt)
			}
		}
		s.SetField(info, "AttributeBindings", bindings)
		s.Push(info)
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
	set("HostGetInformationTypeInfo", func(s *lua.LState) int { s.Push(lua.LNil); return 1 })
	set("HostGetComplexAttributeTypeInfo", func(s *lua.LState) int {
		// Minimal: empty bindings, so any complex access resolves to "absent"
		// rather than erroring. Full complex support is a later step.
		code := s.CheckString(1)
		info := s.NewTable()
		s.SetField(info, "Type", lua.LString("ComplexAttributeInfo"))
		s.SetField(info, "Code", lua.LString(code))
		s.SetField(info, "AttributeBindings", s.NewTable())
		s.Push(info)
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
	set("HostFeatureGetSpatialAssociations", func(s *lua.LState) int {
		a := e.adapted[s.CheckString(1)]
		if a == nil || a.primitive == "" {
			s.Push(lua.LNil)
			return 1
		}
		spatialType := s.GetField(s.GetGlobal("SpatialType"), a.primitive)
		orientation := s.GetField(s.GetGlobal("Orientation"), "Forward")
		sa := s.NewTable()
		s.SetField(sa, "Type", lua.LString("SpatialAssociation"))
		s.SetField(sa, "SpatialType", spatialType)
		s.SetField(sa, "SpatialID", lua.LString(a.id+".sp"))
		s.SetField(sa, "Orientation", orientation)
		s.SetField(sa, "InformationAssociations", s.NewTable())
		arr := s.NewTable()
		s.SetField(arr, "Type", lua.LString("array:SpatialAssociation"))
		arr.Append(sa)
		s.Push(arr)
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
	set("HostGetSpatial", func(s *lua.LState) int { s.Push(lua.LNil); return 1 })
	set("HostSpatialGetAssociatedFeatureIDs", emptyArray)
	set("HostSpatialGetAssociatedInformationIDs", emptyArray)
	set("HostInformationTypeGetCode", func(s *lua.LState) int { s.Push(lua.LString("")); return 1 })
	set("HostInformationTypeGetSimpleAttribute", emptyArray)
	set("HostInformationTypeGetComplexAttributeCount", func(s *lua.LState) int { s.Push(lua.LNumber(0)); return 1 })

	set("HostPortrayalEmit", func(s *lua.LState) int { s.Push(lua.LTrue); return 1 })
	set("HostDebuggerEntry", func(*lua.LState) int { return 0 })
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
