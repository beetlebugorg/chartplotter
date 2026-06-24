// Package s101 runs the IHO S-101 portrayal rules (gopher-lua) against our
// S-57 feature data, with the Lua host's type introspection backed by the
// S-101 Feature Catalogue (pkg/s100/fc) and an S-57→S-101 feature/attribute
// adapter built from the catalogue's <alias> mapping. It is the bridge +
// host-API integration of the S-101 backport (specs/s101-portrayal-backport.md,
// Workstreams D & E).
//
// This step drives each feature's rule directly (require(Code); the global of
// that name), which exercises the real feature/attribute model — lazy attribute
// resolution, PrimitiveType from geometry, enum decoding — through the bridge.
// The full PortrayalMain wrapper (feature-name text, nautical info, dates) and
// complex attributes / associations are the next integration step.
package s101

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/beetlebugorg/chartplotter/pkg/s100/fc"
	lua "github.com/yuin/gopher-lua"
)

// Feature is one S-57 feature to portray, in S-57 terms.
type Feature struct {
	ID          string
	ObjectClass string            // S-57 acronym, e.g. SILTNK
	Primitive   string            // "Point" | "MultiPoint" | "Curve" | "Surface"
	Attributes  map[string]string // S-57 attribute acronym -> encoded value, e.g. {"CATSIL":"3"}
	// Derived carries pre-computed S-101 attribute values keyed by their S-101
	// code (NOT an S-57 acronym) — for S-101 attributes with no direct S-57
	// source, e.g. defaultClearanceDepth (the underlying depth-area depth a
	// danger of unknown depth inherits, S-52 DEPVAL02).
	Derived map[string]string
	// Points carries the multipoint vertices (lon, lat, depth) for a MultiPoint
	// primitive (SOUNDG): the rule (Sounding→SOUNDG03) iterates them, reading each
	// point's X/Y and ScaledZ (depth). Empty for non-multipoint features.
	Points [][3]float64
}

// contextParameters is the full S-101 mariner-setting set the rules read
// (rules error on reading an unregistered setting); name, S-100 value type, and
// default. Depth contours default to S-52-typical metres.
var contextParameters = []struct{ name, typ, def string }{
	{"RadarOverlay", "boolean", "false"},
	{"PlainBoundaries", "boolean", "false"},
	{"SimplifiedSymbols", "boolean", "false"},
	{"FourShades", "boolean", "true"},
	{"FullLightLines", "boolean", "false"},
	{"IgnoreScaleMinimum", "boolean", "false"},
	{"ShallowWaterDangers", "boolean", "false"},
	{"SafetyContour", "real", "30"},
	{"SafetyDepth", "real", "30"},
	{"ShallowContour", "real", "2"},
	{"DeepContour", "real", "30"},
	{"SafetyHeight", "real", "0"},
	{"PreferredLanguage", "text", "eng"},
}

// Engine holds a Lua state with the S-101 framework loaded and the host
// callbacks bound to a Feature Catalogue.
type Engine struct {
	L   *lua.LState
	cat *fc.Catalogue

	// per-run feature data (set by Portray); keyed by feature ID.
	adapted map[string]*adapted
	order   []string
}

// adapted is an S-57 feature rewritten into S-101 terms via the bridge.
type adapted struct {
	id, code, primitive string
	objClass            string            // raw S-57 acronym (e.g. BRIDGE), for class-specific complex attrs
	attrs               map[string]string // S-101 attribute code -> value string
	s57                 map[string]string // raw S-57 acronym -> value string (complex-attr synthesis)
	name                string            // S-57 OBJNAM → featureName complex attribute (for name labels)
	points              [][3]float64      // multipoint vertices (lon,lat,depth) for SOUNDG
}

// NewEngine loads the S-101 framework from a Rules directory (path) and binds
// host callbacks to cat.
func NewEngine(rulesDir string, cat *fc.Catalogue) (*Engine, error) {
	return NewEngineFS(os.DirFS(rulesDir), cat)
}

// NewEngineFS loads the S-101 framework from an fs.FS rooted at the Rules
// directory (e.g. an embed.FS sub-tree) and binds host callbacks to cat.
func NewEngineFS(rules fs.FS, cat *fc.Catalogue) (*Engine, error) {
	// A growable registry: a single SOUNDG can carry thousands of soundings, and
	// resolving its multipoint builds one Lua object per point (plus SOUNDG03's
	// per-point instructions). The default fixed registry overflows on such a
	// feature, so allow it to grow well past the default ceiling.
	e := &Engine{L: lua.NewState(lua.Options{
		RegistrySize:        1024 * 64,
		RegistryMaxSize:     1024 * 1024,
		RegistryGrowStep:    1024 * 32,
		IncludeGoStackTrace: false,
	}), cat: cat}
	L := e.L

	noop := L.NewFunction(func(*lua.LState) int { return 0 })
	dbg := L.NewTable()
	for _, m := range []string{"StartPerformance", "StopPerformance", "ResetPerformance", "Break", "FirstChanceError", "Trace"} {
		L.SetField(dbg, m, noop)
	}
	L.SetGlobal("Debug", dbg)

	loaded := map[string]bool{}
	L.SetGlobal("require", L.NewFunction(func(s *lua.LState) int {
		name := s.CheckString(1)
		if loaded[name] {
			return 0
		}
		loaded[name] = true
		data, err := fs.ReadFile(rules, name+".lua")
		if err != nil {
			s.RaiseError("require(%q): %v", name, err)
		}
		fn, err := s.Load(bytes.NewReader(data), name+".lua")
		if err != nil {
			s.RaiseError("require(%q): %v", name, err)
		}
		s.Push(fn)
		if err := s.PCall(0, 0, nil); err != nil {
			s.RaiseError("require(%q): %v", name, err)
		}
		return 0
	}))

	e.bindHost()

	// main.lua defines globals the rules rely on (sqParams, unknownValue,
	// nilMarker, scaminInfinite, …) in addition to PortrayalMain, so it must be
	// loaded even though we dispatch rules directly rather than via PortrayalMain.
	if err := L.DoString(`require 'S100Scripting'; require 'PortrayalModel'; require 'PortrayalAPI'; require 'Default'; require 'main'`); err != nil {
		L.Close()
		return nil, fmt.Errorf("load framework: %w", err)
	}
	if err := L.DoString(spatialGlue); err != nil {
		L.Close()
		return nil, fmt.Errorf("install spatial glue: %w", err)
	}
	return e, nil
}

// spatialGlue installs HostFeatureGetSpatialAssociations + HostGetSpatial in
// Lua (after the framework loads) so they can build proper SpatialAssociation /
// Surface objects via the framework constructors. We model each feature's
// geometry as ONE association of its primitive type — enough for the line/area
// rules to iterate GetFlattenedSpatialAssociations without erroring; the actual
// boundary/fill geometry is attached by the Go lowering (LowerS101), not read
// from Lua. Surfaces resolve (HostGetSpatial) to a surface with a single
// exterior-ring curve. _HostFeaturePrimitive (Go) gives the primitive type.
const spatialGlue = `
function HostFeatureGetSpatialAssociations(featureID)
	local pt = _HostFeaturePrimitive(featureID)
	if pt == '' then return nil end
	local arr = { Type = 'array:SpatialAssociation' }
	arr[1] = CreateSpatialAssociation(pt, featureID .. '#' .. string.sub(pt, 1, 1), Orientation.Forward)
	return arr
end

function HostGetSpatial(spatialID)
	-- Surfaces (id suffix '#S') resolve a Spatial: a surface whose single
	-- exterior ring is one curve (so GetFlattenedSpatialAssociations yields it).
	if string.sub(spatialID, -2) == '#S' then
		local fid = string.sub(spatialID, 1, -3)
		local ext = CreateSpatialAssociation('Curve', fid .. '#exterior', Orientation.Forward)
		return CreateSurface(ext, {})
	end
	-- MultiPoints (id suffix '#M', SOUNDG) resolve to a real multipoint built
	-- from the feature's vertices, so SOUNDG03 can iterate point.X/Y/ScaledZ.
	if string.sub(spatialID, -2) == '#M' then
		local fid = string.sub(spatialID, 1, -3)
		local pts = _HostFeaturePoints(fid)
		local spatials = { Type = 'array:Spatial' }
		for _, p in ipairs(pts) do
			spatials[#spatials + 1] = CreatePoint(p[1], p[2], p[3])
		end
		return CreateMultiPoint(spatials)
	end
	-- Points (id suffix '#P') MUST resolve to a real Point, never nil: the
	-- framework's GetSpatial does self['Spatial'] = sa.Spatial then re-reads
	-- self['Spatial'] — assigning nil leaves the field absent, so the re-read
	-- re-enters GetSpatial forever (the OBSTRN/WRECKS deep-sounding overflow).
	if string.sub(spatialID, -2) == '#P' then
		local fid = string.sub(spatialID, 1, -3)
		local pts = _HostFeaturePoints(fid)
		if pts[1] then
			return CreatePoint(pts[1][1], pts[1][2], pts[1][3])
		end
		return CreatePoint('0', '0', nil)
	end
	return nil
end
`

// Close releases the Lua state.
func (e *Engine) Close() { e.L.Close() }

// Portray runs each feature's rule and returns featureID -> drawing-instruction
// stream. Unmapped object classes (no S-101 alias) yield an "UNMAPPED:" marker
// so gaps are visible rather than silent.
func (e *Engine) Portray(features []Feature) (map[string]string, error) {
	e.adapted = map[string]*adapted{}
	e.order = nil
	results := map[string]string{}

	for _, f := range features {
		code, ok := e.cat.FeatureCodeForS57(f.ObjectClass)
		if !ok {
			results[f.ID] = "UNMAPPED:" + f.ObjectClass
			continue
		}
		a := &adapted{id: f.ID, code: code, primitive: f.Primitive, objClass: f.ObjectClass, attrs: map[string]string{}, s57: f.Attributes, points: f.Points}
		for acr, val := range f.Attributes {
			if ac, ok := e.cat.AttrCodeForS57(acr); ok {
				a.attrs[ac] = val
			}
		}
		// Derived attributes are already in S-101 code form (no S-57 alias).
		for code, val := range f.Derived {
			a.attrs[code] = val
		}
		// S-57 OBJNAM/NOBJNM → the S-101 featureName complex attribute (served as
		// featureName sub-attrs by the host) so PortrayFeatureName emits a label.
		if n := f.Attributes["OBJNAM"]; n != "" {
			a.name = n
		} else if n := f.Attributes["NOBJNM"]; n != "" {
			a.name = n
		}
		e.adapted[f.ID] = a
		e.order = append(e.order, f.ID)
	}

	if len(e.order) > 0 {
		if err := e.run(); err != nil {
			return nil, err
		}
		res := e.L.GetGlobal("_RESULTS")
		if t, ok := res.(*lua.LTable); ok {
			for _, id := range e.order {
				if v := t.RawGetString(id); v != lua.LNil {
					results[id] = v.String()
				}
			}
		}
	}
	return results, nil
}

// run builds the portrayal context from the host callbacks and dispatches each
// feature's rule, collecting joined instruction streams into _RESULTS.
func (e *Engine) run() error {
	var b strings.Builder
	b.WriteString("local cps = { Type = 'array:ContextParameter' }\n")
	for _, p := range contextParameters {
		fmt.Fprintf(&b, "table.insert(cps, PortrayalCreateContextParameter(%q, %q, %q))\n", p.name, p.typ, p.def)
	}
	// Mirror main.lua's ProcessFeaturePortrayalItem success path (rule + feature
	// name + nautical info), but on error we suppress the feature rather than
	// fall back to Default (which would stamp QUESMRK1 everywhere).
	b.WriteString(`PortrayalInitializeContextParameters(cps)
_RESULTS = {}
local ctx = portrayalContext.ContextParameters
for _, item in ipairs(portrayalContext.FeaturePortrayalItems) do
	local feature = item.Feature
	local fp = item:NewFeaturePortrayal()
	local ok, err = pcall(function()
		require(feature.Code)
		local vg = _G[feature.Code](feature, fp, ctx)
		if not fp.GetFeatureNameCalled then
			PortrayFeatureName(feature, fp, ctx, 32, 24, vg, nil, 'TextAlignHorizontal:Center;TextAlignVertical:Top;LocalOffset:0,-3.51;FontColor:CHBLK')
		end
		ProcessNauticalInformation(feature, fp, ctx, vg)
	end)
	if ok then
		_RESULTS[feature.ID] = table.concat(fp.DrawingInstructions, ';')
	else
		_RESULTS[feature.ID] = 'ERROR:' .. tostring(err)
	end
end`)
	return e.L.DoString(b.String())
}
