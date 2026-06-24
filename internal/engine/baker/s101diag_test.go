package baker_test

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/beetlebugorg/chartplotter/internal/engine/baker"
	"github.com/beetlebugorg/chartplotter/internal/engine/portrayal"
	"github.com/beetlebugorg/chartplotter/internal/engine/s101"
	"github.com/beetlebugorg/chartplotter/pkg/s100/fc"
	"github.com/beetlebugorg/chartplotter/pkg/s57"
)

// TestS101Diag runs the S-101 engine over a real cell and tallies which rules
// error (→ the QUESMRK1 placeholder flood) and the error messages. Run with -v.
func TestS101Diag(t *testing.T) {
	pc := os.Getenv("S101_CATALOG")
	if pc == "" {
		pc = "/home/jcollins/Projects/s101-portrayal-catalogue/PortrayalCatalog"
	}
	fcPath := os.Getenv("S101_FC")
	if fcPath == "" {
		fcPath = "/home/jcollins/Projects/s101-feature-catalogue/S-101FC/FeatureCatalogue.xml"
	}
	if _, err := os.Stat(filepath.Join(pc, "Rules", "main.lua")); err != nil {
		t.Skip("no catalogue")
	}
	cat, err := fc.Load(fcPath)
	if err != nil {
		t.Fatal(err)
	}
	eng, err := s101.NewEngine(filepath.Join(pc, "Rules"), cat)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	data, err := os.ReadFile("../../../testdata/US4MD81M.000")
	if err != nil {
		t.Fatal(err)
	}
	chart, err := baker.ParseCellBytes("US4MD81M.000", data)
	if err != nil {
		t.Fatal(err)
	}

	features := chart.Features()
	ptrs := make([]*s57.Feature, len(features))
	for i := range features {
		ptrs[i] = &features[i]
	}
	depthIdx := portrayal.BuildDepthIndex(ptrs) // mirrors S101Builder (danger depths)
	var batch []s101.Feature
	idClass := map[string]string{}
	for i := range features {
		f := &features[i]
		g := f.Geometry()
		if len(g.Coordinates) == 0 && len(g.Rings) == 0 {
			continue // non-spatial collection object (C_AGGR/C_ASSO) — mirrors S101Builder
		}
		id := strconv.Itoa(i)
		idClass[id] = f.ObjectClass()
		primitive := prim(f.Geometry().Type)
		var points [][3]float64
		if f.ObjectClass() == "SOUNDG" { // multipoint bridge (mirrors S101Builder)
			primitive = "MultiPoint"
			for _, c := range f.Geometry().Coordinates {
				if len(c) >= 3 {
					points = append(points, [3]float64{c[0], c[1], c[2]})
				} else if len(c) == 2 {
					points = append(points, [3]float64{c[0], c[1], 0})
				}
			}
		}
		batch = append(batch, s101.Feature{
			ID:          id,
			ObjectClass: f.ObjectClass(),
			Primitive:   primitive,
			Attributes:  strAttrs(f.Attributes()),
			Derived:     portrayal.DerivedAttrs(f, depthIdx),
			Points:      points,
		})
	}
	res, err := eng.Portray(batch)
	if err != nil {
		t.Fatal(err)
	}

	errByClass := map[string]int{}
	errMsg := map[string]int{}
	ok, errs, unmapped := 0, 0, 0
	for id, stream := range res {
		switch {
		case strings.HasPrefix(stream, "UNMAPPED:"):
			unmapped++
		case strings.HasPrefix(stream, "ERROR:"):
			errs++
			errByClass[idClass[id]]++
			errMsg[normErr(stream)]++
		default:
			ok++
		}
	}
	t.Logf("features=%d  ok=%d  errored=%d  unmapped=%d", len(features), ok, errs, unmapped)
	t.Logf("errors by class: %s", top(errByClass, 15))
	t.Logf("error messages: %s", topMsg(errMsg, 8))
}

func prim(t s57.GeometryType) string {
	switch t {
	case s57.GeometryTypeLineString:
		return "Curve"
	case s57.GeometryTypePolygon:
		return "Surface"
	default:
		return "Point"
	}
}

func strAttrs(a map[string]interface{}) map[string]string {
	out := map[string]string{}
	for k, v := range a {
		switch t := v.(type) {
		case string:
			out[k] = t
		case int:
			out[k] = strconv.Itoa(t)
		case int64:
			out[k] = strconv.FormatInt(t, 10)
		case float64:
			out[k] = strconv.FormatFloat(t, 'g', -1, 64)
		}
	}
	return out
}

// normErr strips the variable feature-id tail so messages group.
func normErr(s string) string {
	if i := strings.Index(s, ".lua:"); i >= 0 {
		// keep file:line + a bit of the message
		end := i + 5
		for end < len(s) && s[end] >= '0' && s[end] <= '9' {
			end++
		}
		rest := s[end:]
		if len(rest) > 60 {
			rest = rest[:60]
		}
		// keep from the last '/' of the path
		start := strings.LastIndexByte(s[:i], '/') + 1
		return s[start:i] + ".lua" + s[i+4:end] + rest
	}
	if len(s) > 80 {
		return s[:80]
	}
	return s
}

func top(m map[string]int, n int) string {
	type kv struct {
		k string
		v int
	}
	var s []kv
	for k, v := range m {
		s = append(s, kv{k, v})
	}
	sort.Slice(s, func(i, j int) bool { return s[i].v > s[j].v })
	out := ""
	for i := 0; i < n && i < len(s); i++ {
		out += s[i].k + "=" + strconv.Itoa(s[i].v) + " "
	}
	return out
}

func topMsg(m map[string]int, n int) string {
	type kv struct {
		k string
		v int
	}
	var s []kv
	for k, v := range m {
		s = append(s, kv{k, v})
	}
	sort.Slice(s, func(i, j int) bool { return s[i].v > s[j].v })
	out := "\n"
	for i := 0; i < n && i < len(s); i++ {
		out += "    [" + strconv.Itoa(s[i].v) + "] " + s[i].k + "\n"
	}
	return out
}
