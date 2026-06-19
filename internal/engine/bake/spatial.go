package bake

import (
	"github.com/beetlebugorg/chartplotter/pkg/s52"
	"github.com/beetlebugorg/chartplotter/pkg/s57"
)

// depthArea is one indexed depth/dredged area (DEPARE/DRGARE) — its polygon rings
// plus a bbox for cheap rejection and the S-57 attributes (DRVAL1/DRVAL2) the
// danger CSPs read.
type depthArea struct {
	class string
	attrs map[string]interface{}
	rings [][][]float64 // each ring is a list of [lon,lat]
	minLon, minLat, maxLon, maxLat float64
}

// depthIndex is a per-cell index of depth areas, used to answer "which depth
// area(s) underlie this point" for UDWHAZ05 (isolated-danger deep-water test) and
// DEPVAL02 (least-depth derivation). A bbox-filtered linear scan — depth areas
// are bounded per cell and hazards are few, so no grid is needed yet.
type depthIndex struct {
	areas []depthArea
}

// buildDepthIndex collects the cell's DEPARE/DRGARE polygons.
func buildDepthIndex(chart *s57.Chart) *depthIndex {
	idx := &depthIndex{}
	features := chart.Features()
	for i := range features {
		f := &features[i]
		cls := f.ObjectClass()
		if cls != "DEPARE" && cls != "DRGARE" {
			continue
		}
		g := f.Geometry()
		if g.Type != s57.GeometryTypePolygon {
			continue
		}
		rings := geometryRings(g)
		if len(rings) == 0 {
			continue
		}
		da := depthArea{class: cls, attrs: f.Attributes(), rings: rings}
		da.minLon, da.minLat = rings[0][0][0], rings[0][0][1]
		da.maxLon, da.maxLat = da.minLon, da.minLat
		for _, r := range rings {
			for _, c := range r {
				if c[0] < da.minLon {
					da.minLon = c[0]
				}
				if c[0] > da.maxLon {
					da.maxLon = c[0]
				}
				if c[1] < da.minLat {
					da.minLat = c[1]
				}
				if c[1] > da.maxLat {
					da.maxLat = c[1]
				}
			}
		}
		idx.areas = append(idx.areas, da)
	}
	return idx
}

// underlyingAt returns the depth areas whose polygon contains (lat, lon), as
// s52.UnderlyingObjects (class + attributes). nil if none.
func (idx *depthIndex) underlyingAt(lat, lon float64) []s52.UnderlyingObject {
	if idx == nil {
		return nil
	}
	var out []s52.UnderlyingObject
	for i := range idx.areas {
		a := &idx.areas[i]
		if lon < a.minLon || lon > a.maxLon || lat < a.minLat || lat > a.maxLat {
			continue
		}
		if pointInRings(lon, lat, a.rings) {
			out = append(out, s52.UnderlyingObject{ObjectClass: a.class, Attributes: a.attrs})
		}
	}
	return out
}

// spatialFor builds a SpatialContext for a feature, or nil if the class doesn't
// need spatial topology. Only the danger CSPs (OBSTRN/WRECKS/UWTROC, via
// UDWHAZ05/DEPVAL02) consume underlying depth areas today, so resolving is
// limited to those to keep the bake cheap.
func (idx *depthIndex) spatialFor(f *s57.Feature) *s52.SpatialContext {
	switch f.ObjectClass() {
	case "OBSTRN", "WRECKS", "UWTROC":
	default:
		return nil
	}
	lat, lon, ok := featurePoint(f)
	if !ok {
		return nil
	}
	under := idx.underlyingAt(lat, lon)
	if len(under) == 0 {
		return nil
	}
	return &s52.SpatialContext{UnderlyingObjects: under}
}

// geometryRings returns a polygon's rings as [lon,lat] lists, preferring the Rings
// field and falling back to the deprecated flat Coordinates.
func geometryRings(g s57.Geometry) [][][]float64 {
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
// holes), so a point inside a hole correctly reads as outside the area.
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
				xint := (xj-xi)*(lat-yi)/(yj-yi) + xi
				if lon < xint {
					inside = !inside
				}
			}
			j = i
		}
	}
	return inside
}

// featurePoint returns a representative (lat, lon) for a feature: a point's
// location, a line's midpoint, or a polygon exterior ring's centroid.
func featurePoint(f *s57.Feature) (lat, lon float64, ok bool) {
	g := f.Geometry()
	switch g.Type {
	case s57.GeometryTypePoint:
		if len(g.Coordinates) > 0 && len(g.Coordinates[0]) >= 2 {
			return g.Coordinates[0][1], g.Coordinates[0][0], true
		}
	case s57.GeometryTypeLineString:
		if n := len(g.Coordinates); n > 0 && len(g.Coordinates[n/2]) >= 2 {
			c := g.Coordinates[n/2]
			return c[1], c[0], true
		}
	case s57.GeometryTypePolygon:
		rings := geometryRings(g)
		if len(rings) > 0 && len(rings[0]) > 0 {
			var sumLat, sumLon float64
			for _, c := range rings[0] {
				sumLon += c[0]
				sumLat += c[1]
			}
			n := float64(len(rings[0]))
			return sumLat / n, sumLon / n, true
		}
	}
	return 0, 0, false
}
