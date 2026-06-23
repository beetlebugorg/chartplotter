// Command lua-portray-test is the Workstream D vertical slice for the S-101
// backport (specs/s101-portrayal-backport.md). It proves the architectural
// claim: a *real* S-101 portrayal rule executes in gopher-lua against a
// Go-driven host and emits the expected drawing-instruction stream.
//
// To keep the slice honest-but-bounded it loads the real framework
// (S100Scripting, PortrayalModel, PortrayalAPI) and a real rule, but stubs the
// HostGet* catalogue-introspection layer (that whole layer is Workstreams D+E
// proper). The chosen rule (Rapids) needs no attributes/spatial/HostGet*, only
// PrimitiveType, contextParameters, and FeaturePortrayal:AddInstructions /
// SimpleLineStyle — so it exercises the genuine API end to end.
//
// Usage:
//
//	go run ./cmd/lua-portray-test [--rules DIR] [--rule Rapids] [--primitive Curve]
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"strings"

	"github.com/beetlebugorg/chartplotter/pkg/s100/instructions"
	lua "github.com/yuin/gopher-lua"
)

// Host* callbacks the framework references; stubbed to no-ops for this slice.
var hostStubs = []string{
	"HostGetFeatureTypeCodes", "HostGetInformationTypeCodes", "HostGetSimpleAttributeTypeCodes",
	"HostGetComplexAttributeTypeCodes", "HostGetRoleTypeCodes", "HostGetInformationAssociationTypeCodes",
	"HostGetFeatureAssociationTypeCodes", "HostGetFeatureTypeInfo", "HostGetInformationTypeInfo",
	"HostGetSimpleAttributeTypeInfo", "HostGetComplexAttributeTypeInfo", "HostGetFeatureIDs",
	"HostFeatureGetCode", "HostFeatureGetSimpleAttribute", "HostFeatureGetComplexAttributeCount",
	"HostFeatureGetSpatialAssociations", "HostFeatureGetAssociatedFeatureIDs",
	"HostFeatureGetAssociatedInformationIDs", "HostGetSimpleAttribute", "HostGetComplexAttributeCount",
	"HostGetSpatial", "HostSpatialGetAssociatedFeatureIDs", "HostSpatialGetAssociatedInformationIDs",
	"HostInformationTypeGetCode", "HostInformationTypeGetSimpleAttribute",
	"HostInformationTypeGetComplexAttributeCount", "HostDebuggerEntry",
}

func main() {
	rules := flag.String("rules", "/home/jcollins/Projects/s101-portrayal-catalogue/PortrayalCatalog/Rules", "S-101 Rules directory")
	rule := flag.String("rule", "Rapids", "feature-class rule to run")
	primitive := flag.String("primitive", "Curve", "PrimitiveType: Point | Curve | Surface")
	flag.Parse()

	L := lua.NewState()
	defer L.Close()

	// --- host environment ---
	noop := L.NewFunction(func(*lua.LState) int { return 0 })

	// Debug: no-op performance counters; Trace prints.
	dbg := L.NewTable()
	for _, m := range []string{"StartPerformance", "StopPerformance", "ResetPerformance", "Break", "FirstChanceError"} {
		L.SetField(dbg, m, noop)
	}
	L.SetField(dbg, "Trace", L.NewFunction(func(s *lua.LState) int {
		fmt.Fprintf(os.Stderr, "  [Lua Debug.Trace] %s\n", s.CheckString(1))
		return 0
	}))
	L.SetGlobal("Debug", dbg)
	for _, name := range hostStubs {
		L.SetGlobal(name, noop)
	}

	// require: load <rules>/<name>.lua once. Overrides the stdlib require so the
	// framework's `require 'S100Scripting'` etc. resolve from our catalogue.
	loaded := map[string]bool{}
	L.SetGlobal("require", L.NewFunction(func(s *lua.LState) int {
		name := s.CheckString(1)
		if loaded[name] {
			return 0
		}
		loaded[name] = true
		if err := s.DoFile(filepath.Join(*rules, name+".lua")); err != nil {
			s.RaiseError("require(%q): %v", name, err)
		}
		return 0
	}))

	// --- load the real framework ---
	for _, mod := range []string{"S100Scripting", "PortrayalModel", "PortrayalAPI"} {
		if err := L.DoFile(filepath.Join(*rules, mod+".lua")); err != nil {
			fatal("load framework %s: %v", mod, err)
		}
		loaded[mod] = true
	}

	// --- real host data callbacks (Go drives the feature set) ---
	// One feature, no attributes, one spatial association whose SpatialType
	// determines PrimitiveType. SpatialType/Orientation enums exist now (post
	// PortrayalAPI load); we reference the canonical tables so the rule's
	// identity comparisons (sa.SpatialType == SpatialType.Curve) hold.
	L.SetGlobal("HostGetFeatureIDs", L.NewFunction(func(s *lua.LState) int {
		t := s.NewTable()
		t.Append(lua.LString("test.1"))
		s.Push(t)
		return 1
	}))
	L.SetGlobal("HostFeatureGetCode", L.NewFunction(func(s *lua.LState) int {
		s.Push(lua.LString(*rule))
		return 1
	}))
	L.SetGlobal("HostFeatureGetSpatialAssociations", L.NewFunction(func(s *lua.LState) int {
		spatialType := s.GetField(s.GetGlobal("SpatialType"), *primitive)
		orientation := s.GetField(s.GetGlobal("Orientation"), "Forward")
		sa := s.NewTable()
		s.SetField(sa, "Type", lua.LString("SpatialAssociation"))
		s.SetField(sa, "SpatialType", spatialType)
		s.SetField(sa, "SpatialID", lua.LString("sp.1"))
		s.SetField(sa, "Orientation", orientation)
		s.SetField(sa, "InformationAssociations", s.NewTable())
		arr := s.NewTable()
		s.SetField(arr, "Type", lua.LString("array:SpatialAssociation"))
		arr.Append(sa)
		s.Push(arr)
		return 1
	}))

	// --- drive the real pipeline: register mariner settings, build context,
	// run the rule via the public API. PortrayalInitializeContextParameters
	// itself builds the global portrayalContext from our host callbacks. The
	// mariner settings are host-defined; we register the common booleans (a
	// rule needing an unregistered one will surface it as a clear error). ---
	if err := L.DoString(fmt.Sprintf(`
		local cps = { Type = 'array:ContextParameter' }
		for _, name in ipairs({'RadarOverlay','PlainBoundaries','TwoColourSoundings','ShallowPattern','HonorScamin','DisplayNOBJNM'}) do
			table.insert(cps, PortrayalCreateContextParameter(name, 'boolean', 'false'))
		end
		PortrayalInitializeContextParameters(cps)
		local item = portrayalContext.FeaturePortrayalItems['test.1']
		local fp = item:NewFeaturePortrayal()
		require(%q)
		local vg = _G[%q](item.Feature, fp, portrayalContext.ContextParameters)
		_RESULT = { viewingGroup = vg, primitiveType = item.Feature.PrimitiveType.Name, instructions = fp.DrawingInstructions }
	`, *rule, *rule)); err != nil {
		fatal("run rule %s: %v", *rule, err)
	}

	// --- read back the emitted instruction stream ---
	res := L.GetGlobal("_RESULT").(*lua.LTable)
	vg := res.RawGetString("viewingGroup")
	pt := res.RawGetString("primitiveType")
	instr := res.RawGetString("instructions").(*lua.LTable)

	fmt.Printf("Rule %s ran. resolved PrimitiveType=%v viewingGroup=%v\n", *rule, pt, vg)
	var tokens []string
	fmt.Printf("Emitted %d drawing instruction(s):\n", instr.Len())
	for i := 1; i <= instr.Len(); i++ {
		v := instr.RawGetInt(i)
		if v != lua.LNil {
			fmt.Printf("  [%d] %s\n", i, v.String())
			tokens = append(tokens, v.String())
		}
	}

	// Close the seam: parse the emitted stream into resolved draw commands.
	cmds, unsupported := instructions.Reduce(instructions.ParseStream(strings.Join(tokens, ";")))
	fmt.Printf("\nParsed into %d draw command(s):\n", len(cmds))
	for _, c := range cmds {
		extra := ""
		if c.SimpleLine != nil {
			extra = fmt.Sprintf(" line{w=%g,col=%s,dash=%g}", c.SimpleLine.Width, c.SimpleLine.Color, c.SimpleLine.DashLength)
		}
		if c.HasRotation || c.Offset != [2]float64{} {
			extra += fmt.Sprintf(" off=%v rot=%g", c.Offset, c.Rotation)
		}
		fmt.Printf("  %-9s ref=%-10s vg=%d prio=%d plane=%s%s\n", c.Op, c.Reference, c.ViewingGroup, c.Priority, c.DisplayPlane, extra)
	}
	if len(unsupported) > 0 {
		fmt.Printf("Unsupported instruction kinds (gaps): %v\n", unsupported)
	}
	fmt.Println("\nRESULT: PASS — real S-101 rule → gopher-lua via Go host → parsed draw commands.")
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "lua-portray-test: "+format+"\n", args...)
	os.Exit(1)
}
