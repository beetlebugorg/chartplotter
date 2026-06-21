// Package mvt is a Mapbox Vector Tile (MVT 2.1) encoder. A TileBuilder collects
// named layers; a LayerBuilder collects features and owns the per-layer
// key/value dictionaries (MVT interns attribute keys and string values once per
// layer; features reference them by index). Geometry is the MVT command/parameter
// integer stream (MoveTo/LineTo/ClosePath, zig-zag deltas).
//
// Callers hand geometry already in tile-local integer coordinates (see package
// tile). Crucially, colour is carried as a string *token* attribute, never RGB.
package mvt

import (
	"math"

	"github.com/beetlebugorg/chartplotter/internal/engine/tile"
)

func boolToU64(b bool) uint64 {
	if b {
		return 1
	}
	return 0
}

func float32bits(f float32) uint32 { return math.Float32bits(f) }

// ExtentDefault is the standard MVT tile extent.
const ExtentDefault uint32 = 4096

// GeomType is the MVT geometry type.
type GeomType uint32

const (
	GeomUnknown    GeomType = 0
	GeomPoint      GeomType = 1
	GeomLineString GeomType = 2
	GeomPolygon    GeomType = 3
)

// IPoint is a tile-local integer vertex (alias of the tile package's type).
type IPoint = tile.IPoint

// Value is one attribute value. MVT's Value message is a typed union.
type Value struct {
	kind  valueKind
	str   string
	intV  int64
	float float32
	boolV bool
}

type valueKind uint8

const (
	valString valueKind = iota
	valInt
	valFloat
	valBool
)

// StringVal / IntVal / FloatVal / BoolVal construct attribute values.
func StringVal(s string) Value { return Value{kind: valString, str: s} }
func IntVal(n int64) Value     { return Value{kind: valInt, intV: n} }
func FloatVal(f float32) Value { return Value{kind: valFloat, float: f} }
func BoolVal(b bool) Value     { return Value{kind: valBool, boolV: b} }

// KeyValue is one feature attribute.
type KeyValue struct {
	Key   string
	Value Value
}

func commandInteger(id, count uint32) uint32 {
	return (id & 0x7) | (count << 3)
}

// -- geometry command stream -------------------------------------------------

// encodePolygon encodes one (multi)polygon feature. The input is a flat list of
// rings (exteriors and holes intermixed). Rings are classified by geometric
// NESTING depth — how many other rings contain them — so a feature that is really
// several disjoint polygons (e.g. a dredged area split into separate basins, or a
// depth area with island holes) encodes as a proper MVT multipolygon rather than
// forcing only ring 0 to be the exterior and punching every other ring out as a
// hole (which left no-data slivers where a second exterior was wrongly treated as
// a hole). Even depth ⇒ exterior (starts a new polygon, positive winding); odd
// depth ⇒ hole (negative winding). Exteriors are emitted immediately followed by
// their child holes so a decoder attaches each hole to the right exterior.
func encodePolygon(rings [][]IPoint) []uint32 {
	type pring struct {
		pts                    []IPoint
		minX, minY, maxX, maxY int32
		depth                  int
	}
	ps := make([]pring, 0, len(rings))
	for _, raw := range rings {
		ring := dropClosingDuplicate(raw)
		if len(ring) < 3 {
			continue
		}
		pr := pring{pts: ring, minX: ring[0].X, minY: ring[0].Y, maxX: ring[0].X, maxY: ring[0].Y}
		for _, p := range ring {
			if p.X < pr.minX {
				pr.minX = p.X
			}
			if p.X > pr.maxX {
				pr.maxX = p.X
			}
			if p.Y < pr.minY {
				pr.minY = p.Y
			}
			if p.Y > pr.maxY {
				pr.maxY = p.Y
			}
		}
		ps = append(ps, pr)
	}
	if len(ps) == 0 {
		return nil
	}

	// Nesting depth: count rings that contain this ring's first vertex. Distinct
	// rings never share a vertex, so vertex[0] is strictly inside-or-outside every
	// other ring (no on-boundary ambiguity). Bounding boxes prune most pairs.
	contains := func(j int, p IPoint) bool {
		b := &ps[j]
		if p.X < b.minX || p.X > b.maxX || p.Y < b.minY || p.Y > b.maxY {
			return false
		}
		return pointInRingI(p, b.pts)
	}
	for i := range ps {
		d := 0
		v := ps[i].pts[0]
		for j := range ps {
			if i != j && contains(j, v) {
				d++
			}
		}
		ps[i].depth = d
	}

	// Emit each exterior (even depth) followed by the holes it directly contains
	// (depth exactly one greater and geometrically inside it).
	var out []uint32
	cursor := IPoint{X: 0, Y: 0}
	emit := func(pr *pring) {
		wantPositive := pr.depth%2 == 0
		reversed := (signedArea(pr.pts) >= 0) != wantPositive
		out = emitRing(out, &cursor, pr.pts, reversed, true)
	}
	done := make([]bool, len(ps))
	for i := range ps {
		if done[i] || ps[i].depth%2 != 0 {
			continue
		}
		done[i] = true
		emit(&ps[i])
		for j := range ps {
			if done[j] || ps[j].depth != ps[i].depth+1 {
				continue
			}
			if contains(i, ps[j].pts[0]) {
				done[j] = true
				emit(&ps[j])
			}
		}
	}
	// Safety net: emit anything not yet placed (malformed nesting) as its own ring.
	for i := range ps {
		if !done[i] {
			emit(&ps[i])
		}
	}
	return out
}

// pointInRingI reports whether p is inside the polygon ring (even-odd rule).
func pointInRingI(p IPoint, ring []IPoint) bool {
	in := false
	j := len(ring) - 1
	for i := range ring {
		pi, pj := ring[i], ring[j]
		if (pi.Y > p.Y) != (pj.Y > p.Y) {
			xCross := float64(pi.X) + float64(pj.X-pi.X)*float64(p.Y-pi.Y)/float64(pj.Y-pi.Y)
			if float64(p.X) < xCross {
				in = !in
			}
		}
		j = i
	}
	return in
}

func encodeLines(lines [][]IPoint) []uint32 {
	var out []uint32
	cursor := IPoint{X: 0, Y: 0}
	for _, line := range lines {
		if len(line) < 2 {
			continue
		}
		out = emitRing(out, &cursor, line, false, false)
	}
	return out
}

func encodePoints(pts []IPoint) []uint32 {
	if len(pts) == 0 {
		return nil
	}
	out := make([]uint32, 0, 1+2*len(pts))
	cursor := IPoint{X: 0, Y: 0}
	out = append(out, commandInteger(1, uint32(len(pts))))
	for _, p := range pts {
		out = append(out, zigzag32(p.X-cursor.X), zigzag32(p.Y-cursor.Y))
		cursor = p
	}
	return out
}

func emitRing(out []uint32, cursor *IPoint, ring []IPoint, reversed, close bool) []uint32 {
	n := len(ring)
	at := func(i int) IPoint {
		if reversed {
			return ring[n-1-i]
		}
		return ring[i]
	}
	first := at(0)
	out = append(out, commandInteger(1, 1)) // MoveTo, count 1
	out = append(out, zigzag32(first.X-cursor.X), zigzag32(first.Y-cursor.Y))
	*cursor = first

	out = append(out, commandInteger(2, uint32(n-1))) // LineTo
	for i := 1; i < n; i++ {
		p := at(i)
		out = append(out, zigzag32(p.X-cursor.X), zigzag32(p.Y-cursor.Y))
		*cursor = p
	}
	if close {
		out = append(out, commandInteger(7, 1)) // ClosePath
	}
	return out
}

// signedArea is the shoelace signed area (x2); only its sign is used.
func signedArea(ring []IPoint) int64 {
	var area int64
	j := len(ring) - 1
	for i, p := range ring {
		q := ring[j]
		area += int64(q.X)*int64(p.Y) - int64(p.X)*int64(q.Y)
		j = i
	}
	return area
}

func dropClosingDuplicate(ring []IPoint) []IPoint {
	if len(ring) >= 2 {
		a, b := ring[0], ring[len(ring)-1]
		if a.X == b.X && a.Y == b.Y {
			return ring[:len(ring)-1]
		}
	}
	return ring
}

// -- builders ----------------------------------------------------------------

type feature struct {
	geomType GeomType
	geometry []uint32
	tags     []uint32
}

// LayerBuilder collects features and the layer's key/value dictionaries.
type LayerBuilder struct {
	name      string
	extent    uint32
	features  []feature
	keys      []string
	values    []Value
	keyIndex  map[string]uint32
	strValIdx map[string]uint32
}

// FeatureCount returns how many features the layer holds.
func (l *LayerBuilder) FeatureCount() int { return len(l.features) }

func (l *LayerBuilder) internKey(key string) uint32 {
	if idx, ok := l.keyIndex[key]; ok {
		return idx
	}
	idx := uint32(len(l.keys))
	l.keys = append(l.keys, key)
	l.keyIndex[key] = idx
	return idx
}

func (l *LayerBuilder) internValue(v Value) uint32 {
	// Only strings are deduped (tokens / class names repeat on most features).
	if v.kind == valString {
		if idx, ok := l.strValIdx[v.str]; ok {
			return idx
		}
		idx := uint32(len(l.values))
		l.values = append(l.values, v)
		l.strValIdx[v.str] = idx
		return idx
	}
	idx := uint32(len(l.values))
	l.values = append(l.values, v)
	return idx
}

func (l *LayerBuilder) addFeature(geomType GeomType, geometry []uint32, attrs []KeyValue) {
	if len(geometry) == 0 {
		return
	}
	tags := make([]uint32, len(attrs)*2)
	for i, a := range attrs {
		tags[i*2] = l.internKey(a.Key)
		tags[i*2+1] = l.internValue(a.Value)
	}
	l.features = append(l.features, feature{geomType: geomType, geometry: geometry, tags: tags})
}

// AddPolygon adds a polygon feature (exterior ring first, holes after).
func (l *LayerBuilder) AddPolygon(rings [][]IPoint, attrs []KeyValue) {
	l.addFeature(GeomPolygon, encodePolygon(rings), attrs)
}

// AddLines adds a (multi-)linestring feature.
func (l *LayerBuilder) AddLines(lines [][]IPoint, attrs []KeyValue) {
	l.addFeature(GeomLineString, encodeLines(lines), attrs)
}

// AddPoints adds a (multi-)point feature.
func (l *LayerBuilder) AddPoints(pts []IPoint, attrs []KeyValue) {
	l.addFeature(GeomPoint, encodePoints(pts), attrs)
}

func featureBodyLen(f feature) int {
	n := 0
	n += packedU32FieldLen(2, f.tags)
	n += varintFieldLen(3, uint64(f.geomType))
	n += packedU32FieldLen(4, f.geometry)
	return n
}

func encodeFeatureInto(w *writer, f feature) {
	w.writeTag(2, wireLen)
	w.writeVarint(uint64(featureBodyLen(f)))
	w.writePackedU32(2, f.tags)
	w.writeVarintField(3, uint64(f.geomType))
	w.writePackedU32(4, f.geometry)
}

func valueBodyLen(v Value) int {
	switch v.kind {
	case valString:
		return bytesFieldLen(1, len(v.str))
	case valFloat:
		return fixed32FieldLen(2)
	case valInt:
		return varintFieldLen(4, uint64(v.intV))
	default: // bool
		return varintFieldLen(7, boolToU64(v.boolV))
	}
}

func encodeValueInto(w *writer, v Value) {
	w.writeTag(4, wireLen)
	w.writeVarint(uint64(valueBodyLen(v)))
	switch v.kind {
	case valString:
		w.writeStringField(1, v.str)
	case valFloat:
		w.writeFixed32Field(2, float32bits(v.float))
	case valInt:
		w.writeVarintField(4, uint64(v.intV)) // int64 two's-complement bits
	default: // bool
		w.writeVarintField(7, boolToU64(v.boolV))
	}
}

func (l *LayerBuilder) bodyLen() int {
	n := 0
	n += varintFieldLen(15, 2) // version
	n += bytesFieldLen(1, len(l.name))
	for _, f := range l.features {
		n += msgFieldLen(2, featureBodyLen(f))
	}
	for _, k := range l.keys {
		n += bytesFieldLen(3, len(k))
	}
	for _, v := range l.values {
		n += msgFieldLen(4, valueBodyLen(v))
	}
	n += varintFieldLen(5, uint64(l.extent))
	return n
}

func (l *LayerBuilder) encodeInto(w *writer) {
	w.writeVarintField(15, 2) // version
	w.writeStringField(1, l.name)
	for _, f := range l.features {
		encodeFeatureInto(w, f)
	}
	for _, k := range l.keys {
		w.writeStringField(3, k)
	}
	for _, v := range l.values {
		encodeValueInto(w, v)
	}
	w.writeVarintField(5, uint64(l.extent))
}

// TileBuilder collects named layers and serialises the Tile message.
type TileBuilder struct {
	layers []*LayerBuilder
	extent uint32
}

// NewTileBuilder creates a tile builder at the given MVT extent.
func NewTileBuilder(extent uint32) *TileBuilder {
	return &TileBuilder{extent: extent}
}

// Layer gets or creates a named layer (names are unique per tile).
func (t *TileBuilder) Layer(name string) *LayerBuilder {
	for _, l := range t.layers {
		if l.name == name {
			return l
		}
	}
	lb := &LayerBuilder{
		name:      name,
		extent:    t.extent,
		keyIndex:  map[string]uint32{},
		strValIdx: map[string]uint32{},
	}
	t.layers = append(t.layers, lb)
	return lb
}

// IsEmpty reports whether no layer holds any feature.
func (t *TileBuilder) IsEmpty() bool {
	for _, l := range t.layers {
		if len(l.features) > 0 {
			return false
		}
	}
	return true
}

// Encode serialises the whole tile to MVT bytes.
func (t *TileBuilder) Encode() []byte {
	var w writer
	for _, l := range t.layers {
		if len(l.features) == 0 {
			continue // skip empty layers
		}
		w.writeTag(3, wireLen) // Tile.layers = 3
		w.writeVarint(uint64(l.bodyLen()))
		l.encodeInto(&w)
	}
	return w.bytes()
}
