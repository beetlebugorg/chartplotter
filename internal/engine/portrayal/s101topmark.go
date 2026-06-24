package portrayal

import (
	"strconv"
	"strings"

	"github.com/beetlebugorg/chartplotter/pkg/s57"
)

// S-57 encodes a buoy/beacon topmark as a SEPARATE TOPMAR point feature
// co-located with its parent; S-101 instead models the topmark as a complex
// attribute ON the parent (read by the buoy/beacon rules' TOPMAR02 CSP). This
// file bridges that: it indexes TOPMAR features by location so the baker can fold
// each one into its co-located parent (and drop the standalone TOPMAR, which has
// no S-101 feature class and would otherwise portray as a magenta unknown mark).

// buildTopmarkIndex maps a location key → the topmark data (shape from TOPSHP,
// colour from COLOUR) of the TOPMAR feature at that point.
func buildTopmarkIndex(features []*s57.Feature) map[string]map[string]string {
	idx := map[string]map[string]string{}
	for _, f := range features {
		if f.ObjectClass() != "TOPMAR" {
			continue
		}
		key, ok := pointLocKey(f.Geometry())
		if !ok {
			continue
		}
		tm := map[string]string{}
		if s := stringAttr(f.Attributes(), "TOPSHP"); s != "" {
			tm["shape"] = s
		}
		if c := stringAttr(f.Attributes(), "COLOUR"); c != "" {
			tm["colour"] = c
		}
		if len(tm) > 0 {
			idx[key] = tm
		}
	}
	return idx
}

// isTopmarkParent reports whether an S-57 class is a buoy/beacon (or light float)
// that carries a topmark — i.e. an S-101 class whose rule reads feature.topmark.
func isTopmarkParent(cls string) bool {
	return strings.HasPrefix(cls, "BOY") || strings.HasPrefix(cls, "BCN") || cls == "LITFLT"
}

// pointLocKey is a stable location key for a point feature's first vertex,
// quantized so a parent and its topmark (which share the vertex) collide.
func pointLocKey(g s57.Geometry) (string, bool) {
	if g.Type != s57.GeometryTypePoint || len(g.Coordinates) == 0 || len(g.Coordinates[0]) < 2 {
		return "", false
	}
	c := g.Coordinates[0]
	return strconv.FormatFloat(c[0], 'f', 7, 64) + "," + strconv.FormatFloat(c[1], 'f', 7, 64), true
}
