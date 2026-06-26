// Package s101 runs the IHO S-101 portrayal rules (gopher-lua) against our
// S-57 feature data, with the Lua host's type introspection backed by the
// S-101 Feature Catalogue (pkg/s100/fc) and an S-57→S-101 feature/attribute
// adapter built from the catalogue's <alias> mapping. It is the bridge +
// host-API integration that connects S-57 features to the S-101 rules.
//
// Each feature's rule runs directly (require(Code); the global of that name),
// exercising the real feature/attribute model — lazy attribute resolution,
// PrimitiveType from geometry, enum decoding — through the bridge.
package s101

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"sync"

	"github.com/beetlebugorg/chartplotter/pkg/s100/fc"
	lua "github.com/yuin/gopher-lua"
	"github.com/yuin/gopher-lua/parse"
)

// ProtoCache memoizes compiled Lua chunk prototypes by require name so engines
// built from the same Rules tree don't re-parse and re-compile the framework
// (and per-class rule files) from source on every construction. A *FunctionProto
// is immutable after compilation and safe to instantiate into many independent
// LStates via NewFunctionFromProto, so one cache is shared across every engine a
// builder creates. Safe for concurrent use.
type ProtoCache struct {
	mu     sync.Mutex
	protos map[string]*lua.FunctionProto
}

// NewProtoCache returns an empty prototype cache.
func NewProtoCache() *ProtoCache { return &ProtoCache{protos: map[string]*lua.FunctionProto{}} }

// proto returns the compiled prototype for <name>.lua under rules, compiling and
// caching it on first request. The lock is held across compile so a cold cache
// compiles each chunk once; after warmup it's a single map read.
func (c *ProtoCache) proto(rules fs.FS, name string) (*lua.FunctionProto, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if p, ok := c.protos[name]; ok {
		return p, nil
	}
	data, err := fs.ReadFile(rules, name+".lua")
	if err != nil {
		return nil, err
	}
	chunk, err := parse.Parse(bytes.NewReader(data), name+".lua")
	if err != nil {
		return nil, err
	}
	p, err := lua.Compile(chunk, name+".lua")
	if err != nil {
		return nil, err
	}
	c.protos[name] = p
	return p, nil
}

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
	// Topmark carries a co-located S-57 TOPMAR's data ("shape" from TOPSHP,
	// "colour" from COLOUR) for a buoy/beacon. In S-101 the topmark is a complex
	// attribute on the parent, not a separate feature, so the baker folds the
	// co-located TOPMAR in here and the bridge synthesizes the topmark complex
	// attribute the buoy/beacon rules' TOPMAR02 CSP reads. Empty otherwise.
	Topmark map[string]string
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

	// overrides replaces a context parameter's default for this engine (e.g.
	// PlainBoundaries=true), so the baker can portray boundary-style / point-style
	// variants. Empty = the defaults in contextParameters.
	overrides map[string]string

	// per-run feature data (set by Portray); keyed by feature ID.
	adapted map[string]*adapted
	order   []string
	// coLocated indexes mapped point features by their exact position, so the
	// host can answer HostSpatialGetAssociatedFeatureIDs — i.e. which features
	// share a node. Co-located lights use it (S-101 LightFlareAndDescription) to
	// stack their descriptions (GetColocatedTextCount) and fan their flares.
	coLocated map[[2]float64][]string
}

// coLocatedFeatureIDs returns the IDs of mapped point features that share the
// given feature's node (including the feature itself; the caller filters self).
// It backs HostSpatialGetAssociatedFeatureIDs for point spatials — the data the
// S-101 LightFlareAndDescription rule needs to stack co-located light
// descriptions and fan their flares. Non-point / unknown features yield nil.
func (e *Engine) coLocatedFeatureIDs(featureID string) []string {
	a := e.adapted[featureID]
	if a == nil || a.primitive != "Point" || len(a.points) == 0 {
		return nil
	}
	k := [2]float64{a.points[0][0], a.points[0][1]}
	ids := e.coLocated[k]
	if len(ids) < 2 {
		return nil // only itself: no co-located neighbours
	}
	return ids
}

// SetContextOverrides overrides context-parameter defaults (e.g.
// {"PlainBoundaries": "true"}) before Portray, so a variant pass renders plain
// boundaries / simplified symbols. Call right after construction.
func (e *Engine) SetContextOverrides(o map[string]string) { e.overrides = o }

// adapted is an S-57 feature rewritten into S-101 terms via the bridge. root is
// the synthesized attribute tree (simple attributes + featureName / clearance /
// orientation / sector complex attributes) the host serves to the rule.
type adapted struct {
	id, code, primitive string
	root                *cnode       // synthesized attribute tree (see complex.go)
	points              [][3]float64 // multipoint vertices (lon,lat,depth) for SOUNDG
}

// NewEngine loads the S-101 framework from a Rules directory (path) and binds
// host callbacks to cat.
func NewEngine(rulesDir string, cat *fc.Catalogue) (*Engine, error) {
	return NewEngineFS(os.DirFS(rulesDir), cat)
}

// NewEngineFS loads the S-101 framework from an fs.FS rooted at the Rules
// directory (e.g. an embed.FS sub-tree) and binds host callbacks to cat. It
// compiles the framework fresh; reuse a ProtoCache via NewEngineFSCached to share
// compiled chunks across engines.
func NewEngineFS(rules fs.FS, cat *fc.Catalogue) (*Engine, error) {
	return NewEngineFSCached(rules, cat, NewProtoCache())
}

// NewEngineFSCached is NewEngineFS sharing a caller-owned ProtoCache, so a batch
// of engines built from the same Rules tree parse + compile each chunk once
// instead of per engine. Each engine still gets a fresh LState (its per-cell
// catalogue caches are freed on Close); only the immutable compiled prototypes
// are shared.
func NewEngineFSCached(rules fs.FS, cat *fc.Catalogue, cache *ProtoCache) (*Engine, error) {
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
		proto, err := cache.proto(rules, name)
		if err != nil {
			s.RaiseError("require(%q): %v", name, err)
		}
		// A top-level chunk has no upvalues and binds globals via ls.Env, so
		// NewFunctionFromProto is equivalent to Load here — just without the
		// re-parse/compile.
		s.Push(s.NewFunctionFromProto(proto))
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
// boundary/fill geometry is attached by the Go side when it emits primitives, not read
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
	e.coLocated = map[[2]float64][]string{}
	results := map[string]string{}

	for _, f := range features {
		code, ok := e.resolveCode(f.ObjectClass, f.Attributes)
		if !ok {
			results[f.ID] = "UNMAPPED:" + f.ObjectClass
			continue
		}
		// S-57 OBJNAM/NOBJNM → the S-101 featureName complex attribute (served by
		// the host) so PortrayFeatureName emits a label.
		name := f.Attributes["OBJNAM"]
		if name == "" {
			name = f.Attributes["NOBJNM"]
		}
		a := &adapted{
			id:        f.ID,
			code:      code,
			primitive: f.Primitive,
			points:    f.Points,
			root:      e.buildRoot(f.ObjectClass, f.Attributes, f.Derived, name, f.Topmark),
		}
		e.adapted[f.ID] = a
		e.order = append(e.order, f.ID)
		// Index point features by exact position for co-location queries. Only
		// point primitives carry a single resolvable node; lines/areas don't
		// participate in the LIGHTS06 co-located stacking/fanning.
		if f.Primitive == "Point" && len(f.Points) > 0 {
			k := [2]float64{f.Points[0][0], f.Points[0][1]}
			e.coLocated[k] = append(e.coLocated[k], f.ID)
		}
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
		def := p.def
		if v, ok := e.overrides[p.name]; ok {
			def = v
		}
		fmt.Fprintf(&b, "table.insert(cps, PortrayalCreateContextParameter(%q, %q, %q))\n", p.name, p.typ, def)
	}
	// Mirror main.lua's ProcessFeaturePortrayalItem success path (date ranges +
	// rule + feature name + nautical info + date-dependent marker), but on error
	// we suppress the feature rather than fall back to Default (which would stamp
	// QUESMRK1 everywhere).
	b.WriteString(`PortrayalInitializeContextParameters(cps)
_RESULTS = {}
local ctx = portrayalContext.ContextParameters
for _, item in ipairs(portrayalContext.FeaturePortrayalItems) do
	local feature = item.Feature
	local fp = item:NewFeaturePortrayal()
	local ok, err = pcall(function()
		-- Fixed/periodic date ranges (synthesized from S-57 DATSTA/DATEND +
		-- PERSTA/PEREND): emit Date:/TimeValid: annotations and report whether the
		-- feature is date-dependent so the CHDATD01 marker is added below.
		local dateDependent = ProcessFixedAndPeriodicDates(feature, fp)
		require(feature.Code)
		local vg = _G[feature.Code](feature, fp, ctx)
		if not fp.GetFeatureNameCalled then
			PortrayFeatureName(feature, fp, ctx, 32, 24, vg, nil, 'TextAlignHorizontal:Center;TextAlignVertical:Top;LocalOffset:0,-3.51;FontColor:CHBLK')
		end
		ProcessNauticalInformation(feature, fp, ctx, vg)
		if dateDependent then
			AddDateDependentSymbol(feature, fp, ctx, vg)
		end
	end)
	if ok then
		_RESULTS[feature.ID] = table.concat(fp.DrawingInstructions, ';')
	else
		_RESULTS[feature.ID] = 'ERROR:' .. tostring(err)
	end
end`)
	return e.L.DoString(b.String())
}
