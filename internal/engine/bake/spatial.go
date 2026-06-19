package bake

import (
	"math"

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

// posKey quantises a coordinate to ~0.1 m so a topmark and the buoy/beacon it
// sits on (sharing the same S-57 node) hash to the same bucket.
type posKey struct{ lat, lon int64 }

func makePosKey(lat, lon float64) posKey {
	const q = 1e6
	return posKey{int64(math.Round(lat * q)), int64(math.Round(lon * q))}
}

// cellIndex is a per-cell spatial index: depth-area polygons (for "which depth
// area underlies this point" — UDWHAZ05/DEPVAL02) plus co-located floating-
// platform point aids keyed by position (for TOPMAR01's floating-vs-rigid test).
// A bbox-filtered linear scan for areas; a hash bucket for points — depth areas
// are bounded per cell and aids are few, so no grid is needed yet.
type cellIndex struct {
	areas  []depthArea
	points map[posKey][]s52.AdjacentObject
}

// buildCellIndex collects the cell's DEPARE/DRGARE polygons and floating-platform
// point aids (LITFLT/LITVES/MORFAC/BOY*).
func buildCellIndex(chart *s57.Chart) *cellIndex {
	idx := &cellIndex{points: map[posKey][]s52.AdjacentObject{}}
	features := chart.Features()
	for i := range features {
		f := &features[i]
		cls := f.ObjectClass()
		g := f.Geometry()

		if cls == "DEPARE" || cls == "DRGARE" {
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
			continue
		}

		if isPlatformCandidate(cls) && g.Type == s57.GeometryTypePoint &&
			len(g.Coordinates) > 0 && len(g.Coordinates[0]) >= 2 {
			lat, lon := g.Coordinates[0][1], g.Coordinates[0][0]
			k := makePosKey(lat, lon)
			idx.points[k] = append(idx.points[k], s52.AdjacentObject{ObjectClass: cls, Attributes: f.Attributes()})
		}
	}
	return idx
}

// isPlatformCandidate reports whether a point class is a platform a topmark can
// sit on (S-52 TOPMAR01) — BOTH floating (BOY*/LITFLT/LITVES/MORFAC) and rigid
// (BCN*/DAYMAR/PILPNT/…) candidates are indexed, so the procedure can tell a
// co-located beacon (→ rigid) from no co-located object at all (→ BCNSHP
// fallback). The floating-vs-rigid decision itself is made by isFloatingPlatform.
func isPlatformCandidate(cls string) bool {
	if len(cls) >= 3 && (cls[:3] == "BOY" || cls[:3] == "BCN") {
		return true
	}
	switch cls {
	case "LITFLT", "LITVES", "MORFAC", // floating
		"DAYMAR", "PILPNT", "OFSPLF", "LNDMRK", "BUISGL", "PYLONS", "SILTNK", "FORSTC": // rigid
		return true
	}
	return false
}

// colocatedAt returns the platform aids at (lat, lon).
func (idx *cellIndex) colocatedAt(lat, lon float64) []s52.AdjacentObject {
	if idx == nil || idx.points == nil {
		return nil
	}
	return idx.points[makePosKey(lat, lon)]
}

// underlyingAt returns the depth areas whose polygon contains (lat, lon), as
// s52.UnderlyingObjects (class + attributes). nil if none.
func (idx *cellIndex) underlyingAt(lat, lon float64) []s52.UnderlyingObject {
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
// need spatial topology: danger CSPs (OBSTRN/WRECKS/UWTROC) get the underlying
// depth areas (UDWHAZ05/DEPVAL02); TOPMAR gets the co-located floating-platform
// aids (TOPMAR01). Resolution is limited to these classes to keep the bake cheap.
func (idx *cellIndex) spatialFor(f *s57.Feature) *s52.SpatialContext {
	lat, lon, ok := featurePoint(f)
	if !ok {
		return nil
	}
	switch f.ObjectClass() {
	case "OBSTRN", "WRECKS", "UWTROC":
		if under := idx.underlyingAt(lat, lon); len(under) > 0 {
			return &s52.SpatialContext{UnderlyingObjects: under}
		}
	case "TOPMAR":
		if adj := idx.colocatedAt(lat, lon); len(adj) > 0 {
			return &s52.SpatialContext{AdjacentObjects: adj}
		}
	}
	return nil
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
