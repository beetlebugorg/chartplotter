// Package tile holds tile geometry: Web-Mercator (z,x,y) <-> tile-local extent
// coordinates, plus the clipping the MVT encoder needs (Sutherland-Hodgman for
// polygons, Liang-Barsky for polylines).
//
// The whole world at zoom z is extent*2^z units wide; tile (x,y) owns the
// [x*extent, (x+1)*extent) window, and a lat/lon maps to a tile-local coordinate
// by subtracting that origin. Geometry is then clipped to [-buffer, extent+buffer]
// (the MVT render buffer) so a mark just off the tile edge still rasterises.
package tile

import (
	"math"

	"github.com/beetlebugorg/chartplotter/pkg/geo"
)

// FPoint is a tile-local coordinate before quantization.
type FPoint struct{ X, Y float64 }

// IPoint is a quantized integer MVT vertex.
type IPoint struct{ X, Y int32 }

// TileCoord is a (z,x,y) tile address.
type TileCoord struct{ Z, X, Y uint32 }

// WorldSize is the world width in tile-local units at zoom z (extent*2^z).
func WorldSize(z, extent uint32) float64 {
	return float64(extent) * math.Pow(2.0, float64(z))
}

func worldXpx(lon, worldSize float64) float64 {
	return (lon + 180.0) / 360.0 * worldSize
}

// latToWebMercatorPx is the portrayal projector's Web-Mercator y (origin at the
// north edge), matching the viewport projection so tiles align with the renderer.
func latToWebMercatorPx(latDeg, worldPx float64) float64 {
	latRad := latDeg * math.Pi / 180.0
	sinLat := math.Sin(latRad)
	y := 0.5 - math.Log((1.0+sinLat)/(1.0-sinLat))/(4.0*math.Pi)
	return y * worldPx
}

// Projector is a per-tile lat/lon -> tile-local transform. Build once per tile.
type Projector struct {
	worldSize float64
	originX   float64
	originY   float64
}

// NewProjector builds the transform for a tile of the given extent.
func NewProjector(coord TileCoord, extent uint32) Projector {
	return Projector{
		worldSize: WorldSize(coord.Z, extent),
		originX:   float64(coord.X) * float64(extent),
		originY:   float64(coord.Y) * float64(extent),
	}
}

// Project maps a lat/lon to this tile's local coordinate.
func (p Projector) Project(ll geo.LatLon) FPoint {
	return FPoint{
		X: worldXpx(ll.Lon, p.worldSize) - p.originX,
		Y: latToWebMercatorPx(ll.Lat, p.worldSize) - p.originY,
	}
}

// ProjectNorm projects a point already in normalized-world coordinates (X,Y in
// [0,1], Web-Mercator) into this tile's pixel space. This is a cheap affine
// transform — no log/sin/tan — so callers that project the same geometry into
// many tiles should normalize once (lon/lat → [0,1]) and use this per tile.
func (p Projector) ProjectNorm(n FPoint) FPoint {
	return FPoint{
		X: n.X*p.worldSize - p.originX,
		Y: n.Y*p.worldSize - p.originY,
	}
}

// Quantize rounds a clipped tile-local coordinate to an integer MVT vertex.
func Quantize(p FPoint) IPoint {
	return IPoint{X: int32(math.Round(p.X)), Y: int32(math.Round(p.Y))}
}

// TileRange is an inclusive tile index range covering a bbox at zoom z.
type TileRange struct {
	Z                      uint32
	XMin, XMax, YMin, YMax uint32
}

// Count is the number of tiles in the range.
func (r TileRange) Count() uint64 {
	return uint64(r.XMax-r.XMin+1) * uint64(r.YMax-r.YMin+1)
}

// RangeForBbox is the inclusive tile range covering bbox at zoom z. Y is clamped
// to the valid [0, 2^z) band; X is not wrapped (ENC cells don't cross the
// antimeridian in this corpus).
func RangeForBbox(z uint32, bbox geo.BoundingBox, extent uint32) TileRange {
	ws := WorldSize(z, extent)
	extF := float64(extent)
	nTiles := int64(math.Pow(2.0, float64(z)))
	last := nTiles - 1

	clampI := func(v, lo, hi int64) int64 {
		if v < lo {
			return lo
		}
		if v > hi {
			return hi
		}
		return v
	}

	tx0 := clampI(int64(math.Floor(worldXpx(bbox.MinLon, ws)/extF)), 0, last)
	tx1 := clampI(int64(math.Floor(worldXpx(bbox.MaxLon, ws)/extF)), 0, last)
	// Y grows southward: MaxLat is the smaller (northern) pixel value.
	ty0 := clampI(int64(math.Floor(latToWebMercatorPx(bbox.MaxLat, ws)/extF)), 0, last)
	ty1 := clampI(int64(math.Floor(latToWebMercatorPx(bbox.MinLat, ws)/extF)), 0, last)

	return TileRange{
		Z:    z,
		XMin: uint32(min64(tx0, tx1)),
		XMax: uint32(max64(tx0, tx1)),
		YMin: uint32(min64(ty0, ty1)),
		YMax: uint32(max64(ty0, ty1)),
	}
}

// -- clipping ----------------------------------------------------------------

// Rect is a clip rectangle in tile-local coordinates.
type Rect struct {
	MinX, MinY, MaxX, MaxY float64
}

// RectForTile is the clip rect for a tile of extent with buffer units of bleed.
func RectForTile(extent uint32, buffer float64) Rect {
	e := float64(extent)
	return Rect{MinX: -buffer, MinY: -buffer, MaxX: e + buffer, MaxY: e + buffer}
}

// ContainsF reports whether p is inside the rect (inclusive).
func (r Rect) ContainsF(p FPoint) bool {
	return p.X >= r.MinX && p.X <= r.MaxX && p.Y >= r.MinY && p.Y <= r.MaxY
}

type edge uint8

const (
	edgeLeft edge = iota
	edgeRight
	edgeTop
	edgeBottom
)

var fourEdges = [4]edge{edgeLeft, edgeRight, edgeTop, edgeBottom}

func inside(e edge, p FPoint, r Rect) bool {
	switch e {
	case edgeLeft:
		return p.X >= r.MinX
	case edgeRight:
		return p.X <= r.MaxX
	case edgeTop:
		return p.Y >= r.MinY
	default: // bottom
		return p.Y <= r.MaxY
	}
}

func intersectEdge(e edge, a, b FPoint, r Rect) FPoint {
	switch e {
	case edgeLeft:
		return lerpX(a, b, r.MinX)
	case edgeRight:
		return lerpX(a, b, r.MaxX)
	case edgeTop:
		return lerpY(a, b, r.MinY)
	default: // bottom
		return lerpY(a, b, r.MaxY)
	}
}

func lerpX(a, b FPoint, x float64) FPoint {
	t := (x - a.X) / (b.X - a.X)
	return FPoint{X: x, Y: a.Y + t*(b.Y-a.Y)}
}

func lerpY(a, b FPoint, y float64) FPoint {
	t := (y - a.Y) / (b.Y - a.Y)
	return FPoint{X: a.X + t*(b.X-a.X), Y: y}
}

// Clipper is a reusable polygon clipper: it holds the two Sutherland-Hodgman
// ping-pong buffers so a whole bake reuses one pair of allocations. The returned
// slice aliases internal storage and is valid only until the next Polygon call —
// callers quantize/copy it out immediately.
type Clipper struct {
	a, b []FPoint
}

// Polygon clips ring to r and returns the (possibly empty) result.
func (c *Clipper) Polygon(ring []FPoint, r Rect) []FPoint {
	// Fast path: a ring fully inside the rect clips to itself.
	allIn := true
	for _, p := range ring {
		if p.X < r.MinX || p.X > r.MaxX || p.Y < r.MinY || p.Y > r.MaxY {
			allIn = false
			break
		}
	}
	if allIn {
		c.a = append(c.a[:0], ring...)
		return c.a
	}

	// Ping-pong between c.a and c.b. src reads, scratch is written, then swap.
	src := append(c.a[:0], ring...)
	scratch := c.b
	for _, e := range fourEdges {
		scratch = scratch[:0]
		if len(src) != 0 {
			j := len(src) - 1
			for i := 0; i < len(src); i++ {
				cur, prev := src[i], src[j]
				ci, pi := inside(e, cur, r), inside(e, prev, r)
				if ci {
					if !pi {
						scratch = append(scratch, intersectEdge(e, prev, cur, r))
					}
					scratch = append(scratch, cur)
				} else if pi {
					scratch = append(scratch, intersectEdge(e, prev, cur, r))
				}
				j = i
			}
		}
		src, scratch = scratch, src
	}
	// After four swaps, src holds the result. Persist both backing arrays.
	c.a, c.b = src, scratch
	return src
}

// ClipPolygon clips a single ring against r with throwaway buffers.
func ClipPolygon(ring []FPoint, r Rect) []FPoint {
	var c Clipper
	out := c.Polygon(ring, r)
	// Detach from the clipper's buffer since c is discarded.
	cp := make([]FPoint, len(out))
	copy(cp, out)
	return cp
}

// clipSegment is the Liang-Barsky clip of segment a->b to r. ok=false if the
// segment misses the rect entirely.
type clippedSeg struct {
	a, b            FPoint
	entered, exited bool
	t0, t1          float64
}

func clipSegment(a, b FPoint, r Rect) (clippedSeg, bool) {
	dx := b.X - a.X
	dy := b.Y - a.Y
	t0, t1 := 0.0, 1.0
	p := [4]float64{-dx, dx, -dy, dy}
	q := [4]float64{a.X - r.MinX, r.MaxX - a.X, a.Y - r.MinY, r.MaxY - a.Y}
	for i := 0; i < 4; i++ {
		if p[i] == 0 {
			if q[i] < 0 {
				return clippedSeg{}, false // parallel and outside
			}
			continue
		}
		t := q[i] / p[i]
		if p[i] < 0 {
			if t > t1 {
				return clippedSeg{}, false
			}
			if t > t0 {
				t0 = t
			}
		} else {
			if t < t0 {
				return clippedSeg{}, false
			}
			if t < t1 {
				t1 = t
			}
		}
	}
	return clippedSeg{
		a:       FPoint{X: a.X + t0*dx, Y: a.Y + t0*dy},
		b:       FPoint{X: a.X + t1*dx, Y: a.Y + t1*dy},
		entered: t0 > 0.0,
		exited:  t1 < 1.0,
		t0:      t0,
		t1:      t1,
	}, true
}

func approxEq(a, b FPoint) bool {
	return math.Abs(a.X-b.X) < 1e-6 && math.Abs(a.Y-b.Y) < 1e-6
}

// ClipLine clips a polyline to r, returning the contiguous in-rect runs. A line
// that leaves and re-enters yields multiple runs.
func ClipLine(pts []FPoint, r Rect) [][]FPoint {
	var runs [][]FPoint
	if len(pts) < 2 {
		return runs
	}
	var cur []FPoint
	for i := 0; i+1 < len(pts); i++ {
		seg, ok := clipSegment(pts[i], pts[i+1], r)
		if !ok {
			if len(cur) > 0 {
				runs = append(runs, cur)
				cur = nil
			}
			continue
		}
		if len(cur) == 0 {
			cur = append(cur, seg.a, seg.b)
		} else if approxEq(cur[len(cur)-1], seg.a) {
			cur = append(cur, seg.b)
		} else {
			runs = append(runs, cur)
			cur = []FPoint{seg.a, seg.b}
		}
		if seg.exited {
			runs = append(runs, cur)
			cur = nil
		}
	}
	if len(cur) > 0 {
		runs = append(runs, cur)
	}
	return runs
}

// PhasedRun is a clipped polyline run plus the cumulative arc length at its first
// vertex (arc0), so a dashed line's pattern lines up across a tile boundary.
type PhasedRun struct {
	Points []FPoint
	Arc0   float64
}

// ClipLinePhased is like ClipLine, but each run carries arc0 — the arc length
// from the polyline's first vertex to the run's first vertex. arc[i] is the
// cumulative arc length at pts[i] and must have the same length as pts.
func ClipLinePhased(pts []FPoint, arc []float64, r Rect) []PhasedRun {
	var runs []PhasedRun
	if len(pts) < 2 {
		return runs
	}
	var cur []FPoint
	var curArc0 float64
	for i := 0; i+1 < len(pts); i++ {
		da := arc[i+1] - arc[i]
		seg, ok := clipSegment(pts[i], pts[i+1], r)
		if !ok {
			if len(cur) > 0 {
				runs = append(runs, PhasedRun{Points: cur, Arc0: curArc0})
				cur = nil
			}
			continue
		}
		arcA := arc[i] + seg.t0*da
		if len(cur) == 0 {
			curArc0 = arcA
			cur = append(cur, seg.a, seg.b)
		} else if approxEq(cur[len(cur)-1], seg.a) {
			cur = append(cur, seg.b)
		} else {
			runs = append(runs, PhasedRun{Points: cur, Arc0: curArc0})
			curArc0 = arcA
			cur = []FPoint{seg.a, seg.b}
		}
		if seg.exited {
			runs = append(runs, PhasedRun{Points: cur, Arc0: curArc0})
			cur = nil
		}
	}
	if len(cur) > 0 {
		runs = append(runs, PhasedRun{Points: cur, Arc0: curArc0})
	}
	return runs
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
