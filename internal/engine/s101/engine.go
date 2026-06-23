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
	"fmt"
	"path/filepath"
	"strings"

	"github.com/beetlebugorg/chartplotter/pkg/s100/fc"
	lua "github.com/yuin/gopher-lua"
)

// Feature is one S-57 feature to portray, in S-57 terms.
type Feature struct {
	ID          string
	ObjectClass string            // S-57 acronym, e.g. SILTNK
	Primitive   string            // "Point" | "Curve" | "Surface"
	Attributes  map[string]string // S-57 attribute acronym -> encoded value, e.g. {"CATSIL":"3"}
}

// defaultMarinerSettings are the boolean context parameters registered for a
// run (rules error on reading an unregistered setting). Extend as needed.
var defaultMarinerSettings = []string{
	"RadarOverlay", "PlainBoundaries", "TwoColourSoundings", "ShallowPattern",
	"HonorScamin", "DisplayNOBJNM", "FullSectorLengths", "SymbolizedBoundaries",
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
	attrs               map[string]string // S-101 attribute code -> value string
}

// NewEngine loads the S-101 framework from rulesDir and binds host callbacks to
// cat. rulesDir is the S-101 PortrayalCatalog/Rules directory.
func NewEngine(rulesDir string, cat *fc.Catalogue) (*Engine, error) {
	e := &Engine{L: lua.NewState(), cat: cat}
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
		if err := s.DoFile(filepath.Join(rulesDir, name+".lua")); err != nil {
			s.RaiseError("require(%q): %v", name, err)
		}
		return 0
	}))

	e.bindHost()

	if err := L.DoString(`require 'S100Scripting'; require 'PortrayalModel'; require 'PortrayalAPI'; require 'Default'`); err != nil {
		L.Close()
		return nil, fmt.Errorf("load framework: %w", err)
	}
	return e, nil
}

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
		a := &adapted{id: f.ID, code: code, primitive: f.Primitive, attrs: map[string]string{}}
		for acr, val := range f.Attributes {
			if ac, ok := e.cat.AttrCodeForS57(acr); ok {
				a.attrs[ac] = val
			}
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
	for _, n := range defaultMarinerSettings {
		fmt.Fprintf(&b, "table.insert(cps, PortrayalCreateContextParameter(%q, 'boolean', 'false'))\n", n)
	}
	b.WriteString(`PortrayalInitializeContextParameters(cps)
_RESULTS = {}
for _, item in ipairs(portrayalContext.FeaturePortrayalItems) do
	local feature = item.Feature
	local fp = item:NewFeaturePortrayal()
	local ok, err = pcall(function()
		require(feature.Code)
		_G[feature.Code](feature, fp, portrayalContext.ContextParameters)
	end)
	if ok then
		_RESULTS[feature.ID] = table.concat(fp.DrawingInstructions, ';')
	else
		_RESULTS[feature.ID] = 'ERROR:' .. tostring(err)
	end
end`)
	return e.L.DoString(b.String())
}
