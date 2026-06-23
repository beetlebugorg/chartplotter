package sim

import (
	"math"
	"math/rand"

	"github.com/beetlebugorg/chartplotter/pkg/s57"
)

// WaterMask is the navigable-water footprint of an S-57 cell: the exterior rings
// of depth areas (DEPARE/DRGARE) deep enough to float a vessel (DRVAL1 ≥ minDepth).
// Targets and routes are sampled inside it so simulated traffic stays in real
// channels — on the water and off the shoals — not driving over land.
//
// A real cell has thousands of depth-area rings, so each ring carries a bounding
// box (skip the ray-cast unless the point is inside it), and a coarse grid of
// known-navigable points is precomputed at build time so Sample is O(1) and works
// even when water is a thin channel in a large bbox.
type WaterMask struct {
	polys                          []ringPoly
	minLon, minLat, maxLon, maxLat float64
	samples                        [][2]float64 // precomputed navigable [lat,lon] points
}

type ringPoly struct {
	pts                            [][2]float64 // [lon,lat]
	minLon, minLat, maxLon, maxLat float64
}

// NewWaterMask collects navigable depth-area polygons from a parsed chart. A
// DEPARE is kept when its shoalest depth (DRVAL1) is ≥ minDepth (m); DEPARE with
// no DRVAL1 and dredged areas (DRGARE) are kept. Returns nil if the cell has no
// usable depth areas (caller falls back to unconstrained placement).
func NewWaterMask(chart *s57.Chart, minDepth float64) *WaterMask {
	var rings [][][2]float64
	for _, f := range chart.Features() {
		switch f.ObjectClass() {
		case "DRGARE":
		case "DEPARE":
			if d, ok := f.Attribute("DRVAL1"); ok {
				if v, ok2 := toFloat(d); ok2 && v < minDepth {
					continue
				}
			}
		default:
			continue
		}
		for _, r := range f.Geometry().Rings {
			if r.Usage != 1 && r.Usage != 3 {
				continue
			}
			poly := make([][2]float64, 0, len(r.Coordinates))
			for _, c := range r.Coordinates {
				if len(c) >= 2 {
					poly = append(poly, [2]float64{c[0], c[1]})
				}
			}
			if len(poly) >= 3 {
				rings = append(rings, poly)
			}
		}
	}
	return newWaterMask(rings)
}

// newWaterMask builds the mask (bboxes + sample grid) from exterior rings.
func newWaterMask(rings [][][2]float64) *WaterMask {
	if len(rings) == 0 {
		return nil
	}
	m := &WaterMask{minLon: math.Inf(1), minLat: math.Inf(1), maxLon: math.Inf(-1), maxLat: math.Inf(-1)}
	for _, pts := range rings {
		p := ringPoly{pts: pts, minLon: math.Inf(1), minLat: math.Inf(1), maxLon: math.Inf(-1), maxLat: math.Inf(-1)}
		for _, c := range pts {
			p.minLon, p.maxLon = math.Min(p.minLon, c[0]), math.Max(p.maxLon, c[0])
			p.minLat, p.maxLat = math.Min(p.minLat, c[1]), math.Max(p.maxLat, c[1])
		}
		m.polys = append(m.polys, p)
		m.minLon, m.maxLon = math.Min(m.minLon, p.minLon), math.Max(m.maxLon, p.maxLon)
		m.minLat, m.maxLat = math.Min(m.minLat, p.minLat), math.Max(m.maxLat, p.maxLat)
	}
	// Precompute navigable points on a coarse grid for fast, reliable sampling.
	const grid = 160
	for iy := range grid {
		la := m.minLat + (float64(iy)+0.5)/grid*(m.maxLat-m.minLat)
		for ix := range grid {
			lo := m.minLon + (float64(ix)+0.5)/grid*(m.maxLon-m.minLon)
			if m.IsWater(la, lo) {
				m.samples = append(m.samples, [2]float64{la, lo})
			}
		}
	}
	if len(m.samples) == 0 {
		return nil
	}
	return m
}

// IsWater reports whether (lat,lon) lies in any navigable depth area.
func (m *WaterMask) IsWater(lat, lon float64) bool {
	for i := range m.polys {
		p := &m.polys[i]
		if lon < p.minLon || lon > p.maxLon || lat < p.minLat || lat > p.maxLat {
			continue // outside this ring's bbox — cheap reject
		}
		if pointInPoly(lon, lat, p.pts) {
			return true
		}
	}
	return false
}

// Sample returns a random precomputed navigable point. ok is false only if the
// mask is empty.
func (m *WaterMask) Sample(rng *rand.Rand) (lat, lon float64, ok bool) {
	if len(m.samples) == 0 {
		return 0, 0, false
	}
	p := m.samples[rng.Intn(len(m.samples))]
	return p[0], p[1], true
}

// pointInPoly is a standard ray-casting test (x=lon, y=lat).
func pointInPoly(x, y float64, poly [][2]float64) bool {
	in := false
	for i, j := 0, len(poly)-1; i < len(poly); j, i = i, i+1 {
		xi, yi, xj, yj := poly[i][0], poly[i][1], poly[j][0], poly[j][1]
		if (yi > y) != (yj > y) && x < (xj-xi)*(y-yi)/(yj-yi)+xi {
			in = !in
		}
	}
	return in
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	}
	return 0, false
}
