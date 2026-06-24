package portrayal

import (
	"strconv"

	"github.com/beetlebugorg/chartplotter/pkg/s57"
)

// This file derives the S-101 `defaultClearanceDepth` for under/awash dangers of
// unknown depth (the S-52 DEPVAL02 "underlying depth area" rule): OBSTRN07 /
// WRECKS05 hard-require a depth (valueOfSounding OR defaultClearanceDepth) and
// error if both are nil. S-57 supplies valueOfSounding via VALSOU; when that's
// absent the danger inherits the shoalest DRVAL1 of the DEPARE/DRGARE it lies in.

// depthArea is one indexed DEPARE/DRGARE polygon plus a bbox for cheap rejection.
type depthArea struct {
	drval1                         float64
	hasDrval1                      bool
	rings                          [][][]float64 // each ring is a list of [lon,lat]
	minLon, minLat, maxLon, maxLat float64
}

// DepthIndex is the per-cell set of depth/dredged areas, scanned linearly with a
// bbox pre-filter (areas are bounded per cell, so no grid is needed).
type DepthIndex struct{ areas []depthArea }

// BuildDepthIndex collects the cell's DEPARE/DRGARE polygons with their DRVAL1.
func BuildDepthIndex(features []*s57.Feature) *DepthIndex {
	idx := &DepthIndex{}
	for _, f := range features {
		cls := f.ObjectClass()
		if cls != "DEPARE" && cls != "DRGARE" {
			continue
		}
		rings := polygonRings(f.Geometry())
		if len(rings) == 0 {
			continue
		}
		da := depthArea{rings: rings}
		da.drval1, da.hasDrval1 = floatAttr(f.Attributes(), "DRVAL1")
		da.minLon, da.minLat = rings[0][0][0], rings[0][0][1]
		da.maxLon, da.maxLat = da.minLon, da.minLat
		for _, r := range rings {
			for _, c := range r {
				da.minLon, da.maxLon = min(da.minLon, c[0]), max(da.maxLon, c[0])
				da.minLat, da.maxLat = min(da.minLat, c[1]), max(da.maxLat, c[1])
			}
		}
		idx.areas = append(idx.areas, da)
	}
	return idx
}

// shoalestDRVAL1 returns the smallest (shoalest) DRVAL1 among the depth areas
// containing (lat, lon). ok is false if the point lies in no depth area with a
// known DRVAL1 (the danger's depth then stays unknown).
func (idx *DepthIndex) shoalestDRVAL1(lat, lon float64) (float64, bool) {
	if idx == nil {
		return 0, false
	}
	best, found := 0.0, false
	for i := range idx.areas {
		a := &idx.areas[i]
		if !a.hasDrval1 || lon < a.minLon || lon > a.maxLon || lat < a.minLat || lat > a.maxLat {
			continue
		}
		if pointInRings(lon, lat, a.rings) && (!found || a.drval1 < best) {
			best, found = a.drval1, true
		}
	}
	return best, found
}

// DerivedAttrs computes the S-101-coded attributes a feature needs but S-57
// doesn't carry directly. For under/awash dangers it supplies defaultClearanceDepth
// so OBSTRN07/WRECKS05 (which hard-require valueOfSounding OR defaultClearanceDepth)
// don't error on a missing depth and drop the hazard.
//
// Per S-52 DEPVAL the danger inherits the shoalest DRVAL1 of the depth area it
// lies in. With no such area the depth is genuinely unknown — default to 0
// (awash) so UDWHAZ05 treats it as a hazard (an unknown-depth danger is assumed
// dangerous) rather than the feature being suppressed by the rule error.
func DerivedAttrs(f *s57.Feature, idx *DepthIndex) map[string]string {
	switch f.ObjectClass() {
	case "OBSTRN", "WRECKS", "UWTROC":
	default:
		return nil
	}
	depth := 0.0
	if pt, ok := representativePoint(f); ok {
		if d, ok := idx.shoalestDRVAL1(pt.Lat, pt.Lon); ok {
			depth = d
		}
	}
	return map[string]string{"defaultClearanceDepth": strconv.FormatFloat(depth, 'f', -1, 64)}
}

// polygonRings returns a polygon's rings as [lon,lat] lists (Rings field first,
// falling back to the flat Coordinates).
func polygonRings(g s57.Geometry) [][][]float64 {
	if g.Type != s57.GeometryTypePolygon {
		return nil
	}
	if len(g.Rings) > 0 {
		out := make([][][]float64, 0, len(g.Rings))
		for _, r := range g.Rings {
			if len(r.Coordinates) >= 3 {
				out = append(out, r.Coordinates)
			}
		}
		return out
	}
	if len(g.Coordinates) >= 3 {
		return [][][]float64{g.Coordinates}
	}
	return nil
}

// pointInRings is an even-odd point-in-polygon test across all rings (exterior +
// holes), so a point inside a hole reads as outside the area.
func pointInRings(lon, lat float64, rings [][][]float64) bool {
	inside := false
	for _, ring := range rings {
		n := len(ring)
		if n < 3 {
			continue
		}
		j := n - 1
		for i := 0; i < n; i++ {
			xi, yi := ring[i][0], ring[i][1]
			xj, yj := ring[j][0], ring[j][1]
			if (yi > lat) != (yj > lat) {
				if lon < (xj-xi)*(lat-yi)/(yj-yi)+xi {
					inside = !inside
				}
			}
			j = i
		}
	}
	return inside
}
