package portrayal

import (
	"container/heap"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/beetlebugorg/chartplotter/pkg/geo"
	"github.com/beetlebugorg/chartplotter/pkg/s57"
)

// NaN marks "no depth" on sounding/danger fields (the sentinel value).
var nan32 = float32(math.NaN())

// FeatureBuild is the result of expanding one feature: its viewport-independent
// Primitive stream plus the S-52 display priority (buckets draw order) and
// display category (base/standard/other; the client's detail filter).
type FeatureBuild struct {
	Primitives      []Primitive
	DisplayPriority int
	DisplayCategory int
	// DateStart/DateEnd/TimeValid carry the feature's date dependency when it has
	// one (S-57 DATSTA/DATEND fixed window or PERSTA/PEREND periodic window), so the
	// baker can tag the feature and a date-aware client show/hide it against the
	// current date. Empty when the feature is not date-dependent. DateStart/DateEnd
	// are S-57 date strings — full "YYYYMMDD" (fixed) or partial "--MMDD" recurring
	// each year (periodic); TimeValid is the interval kind (closedInterval /
	// geSemiInterval / leSemiInterval). The feature also carries the CHDATD01
	// date-dependent marker symbol among its primitives.
	DateStart string
	DateEnd   string
	TimeValid string
}

// geom is the portrayal-space geometry handed to the instruction walk. It mirrors
// the s57 geometry union (point / soundings / line / area). currentDepthM is
// the per-point sounding depth (NaN otherwise), used by SOUNDG03 + carried on the
// emitted symbol so the client can do SNDFRM04 without a re-bake.
type geom struct {
	kind         geomKind
	point        geo.LatLon
	line         []geo.LatLon
	lineParts    [][]geo.LatLon // drawable line parts (masked/data-limit edges removed, S-52 §8.6.2); nil ⇒ stroke `line`
	area         [][]geo.LatLon
	boundary     [][]geo.LatLon // drawable border polylines (masked/data-limit edges removed); nil ⇒ stroke `area`
	currentDepth float64
	hasDepth     bool
}

type geomKind uint8

const (
	geomNone geomKind = iota
	geomPoint
	geomLine
	geomArea
)

// Boundary-symbolization tags (S-52 §8.6.1) stamped on each primitive's baked
// `bnd`, which the client's boundaryFilter keys off: 2 = style-independent
// (always shown), 0 = plain-boundary pass, 1 = symbolized-boundary pass.
const (
	BndCommon     = 2
	BndPlain      = 0
	BndSymbolized = 1
)

// Point-symbol style tags (S-52 §11.2.2) stamped on each primitive's baked
// `pts`, which the client's pointStyleFilter keys off — the same mechanism as
// `bnd`, but for the simplified vs paper-chart POINT lookup tables: 2 =
// style-independent (always shown), 0 = paper-chart pass, 1 = simplified pass.
// Geometry-disjoint from bnd: simplified/paper only applies to point features,
// plain/symbolized only to area boundaries.
const (
	PtsCommon     = 2
	PtsPaper      = 0
	PtsSimplified = 1
)

// FeatureBuildPass is one display-variant pass: the built primitives plus the
// bnd (boundary-style) and pts (point-symbol-style) tags the baker stamps on
// every primitive of the pass, so the client toggles each axis live (no re-bake).
type FeatureBuildPass struct {
	Build FeatureBuild
	Bnd   int
	Pts   int
}

// applyDangerDepth tags the DANGER01/DANGER02 symbol of a sounded obstruction /
// wreck / rock (one with VALSOU) with its depth and the deep variant, so the
// client swaps shallow<->deep (DANGER01<->DANGER02) against the LIVE safety
// contour with no re-bake (S-52 §13.2.x). It ONLY touches the DANGER01/02 pair —
// soundings, ISODGR01, OBSTRN11, DANGER03 and every other primitive the CSP
// emitted are left exactly as placed. The base symbol is normalised to DANGER01
// (the shallow variant) so the client's coalesce picks DANGER01/DANGER02 by the
// live contour.
//
// (The CSPs — OBSTRN07 Continuation A, WRECKS05 — now emit DANGER01/02 + the
// sounding directly, so this is a post-tag rather than the old symbol-replacing
// override that dropped the sounding glyphs.)
func applyDangerDepth(prims []Primitive, class string, attrs map[string]interface{}) []Primitive {
	if class != "OBSTRN" && class != "WRECKS" && class != "UWTROC" {
		return prims
	}
	valsou, ok := floatAttr(attrs, "VALSOU")
	if !ok {
		return prims
	}
	for i := range prims {
		sc, ok := prims[i].(SymbolCall)
		if !ok || (sc.SymbolName != "DANGER01" && sc.SymbolName != "DANGER02") {
			continue
		}
		sc.SymbolName = "DANGER01"
		sc.DangerDepthM = float32(valsou)
		sc.DeepSymbolName = "DANGER02"
		prims[i] = sc
	}
	return prims
}

// stringAttr returns an attribute's encoded string value, or "" when absent.
func stringAttr(attrs map[string]interface{}, key string) string {
	if v, ok := attrs[key]; ok {
		if s, ok := encodeAttr(v); ok {
			return s
		}
	}
	return ""
}

func floatAttr(attrs map[string]interface{}, key string) (float64, bool) {
	v, ok := attrs[key]
	if !ok || v == nil {
		return 0, false
	}
	switch t := v.(type) {
	case float64:
		return t, true
	case float32:
		return float64(t), true
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case string:
		if f, err := strconv.ParseFloat(strings.TrimSpace(t), 64); err == nil {
			return f, true
		}
	}
	return 0, false
}

// -- helpers -----------------------------------------------------------------

func isSoundingDigit(name string) bool {
	return strings.HasPrefix(name, "SOUNDG") || strings.HasPrefix(name, "SOUNDS")
}

// lookupAttributeText returns the textual value of an attribute for a label, or
// ok=false when absent/empty (which suppresses the label, per S-52).
func lookupAttributeText(attrs map[string]interface{}, acronym string) (string, bool) {
	v, ok := attrs[acronym]
	if !ok || v == nil {
		return "", false
	}
	switch t := v.(type) {
	case string:
		if t == "" {
			return "", false
		}
		return t, true
	case []string, []interface{}:
		return "", false // list attributes have no single label value
	default:
		return strings.TrimSpace(stringifyScalar(v)), true
	}
}

func textAnchor(g geom) (geo.LatLon, bool) {
	switch g.kind {
	case geomPoint:
		return g.point, true
	case geomLine:
		if len(g.line) == 0 {
			return geo.LatLon{}, false
		}
		return g.line[len(g.line)/2], true
	case geomArea:
		if len(g.area) == 0 || len(g.area[0]) == 0 {
			return geo.LatLon{}, false
		}
		return areaLabelPoint(g.area)
	default:
		return geo.LatLon{}, false
	}
}

// areaSurfacePoint returns a representative interior point for a single ring (no
// holes). It is a thin wrapper over areaLabelPoint, kept for callers that have
// only an exterior ring (e.g. the unknown-object question mark).
func areaSurfacePoint(ring []geo.LatLon) (geo.LatLon, bool) {
	return areaLabelPoint([][]geo.LatLon{ring})
}

// areaLabelPoint returns the polygon's "pole of inaccessibility" (the Mapbox
// polylabel algorithm): the interior point farthest from any edge. rings[0] is
// the exterior boundary; rings[1:] are holes. Containment is the even-odd rule
// over ALL rings, so the chosen point lies inside the exterior AND outside every
// hole — keeping a centred symbol (e.g. an anchorage anchor, a restricted-area
// mark) off an excluded structure, and off the missing limb of a concave (L- or
// U-shaped) area. This is the S-52 PresLib §8.5.3 "representative point of an
// area": robust for concave shapes and shapes whose centroid falls outside the
// area. Falls back to the vertex average for a degenerate (zero-area) ring.
//
// Distances are measured with longitude scaled by cos(lat) so "farthest from any
// edge" is in roughly equal ground units rather than skewed degrees.
func areaLabelPoint(rings [][]geo.LatLon) (geo.LatLon, bool) {
	if len(rings) == 0 || len(rings[0]) == 0 {
		return geo.LatLon{}, false
	}
	ext := rings[0]
	// S-52 §8.5.3: the representative point is the area's CENTRE OF GRAVITY by
	// default; only when the centroid falls outside the area (concave / holed
	// shapes) is another point used. Prefer the centroid when it's inside — it
	// sits at the true centre, whereas the pole of inaccessibility below drifts
	// off-centre along a wide shape's mid-line (a wide rectangle's pole is not
	// unique), which left centred symbols visibly off-centre.
	if c, ok := ringCentroid(ext); ok && pointInRingsEvenOdd(c, rings) {
		return c, true
	}
	minLat, minLon := math.Inf(1), math.Inf(1)
	maxLat, maxLon := math.Inf(-1), math.Inf(-1)
	var sumLat, sumLon float64
	for _, p := range ext {
		minLat, maxLat = math.Min(minLat, p.Lat), math.Max(maxLat, p.Lat)
		minLon, maxLon = math.Min(minLon, p.Lon), math.Max(maxLon, p.Lon)
		sumLat, sumLon = sumLat+p.Lat, sumLon+p.Lon
	}
	mean := geo.LatLon{Lat: sumLat / float64(len(ext)), Lon: sumLon / float64(len(ext))}
	kx := math.Cos((minLat + maxLat) / 2 * math.Pi / 180)
	if kx < 1e-9 {
		kx = 1 // near-polar guard; charts don't reach here
	}
	// Work in scaled space: X = lon*kx, Y = lat.
	xMin, xMax := minLon*kx, maxLon*kx
	w, h := xMax-xMin, maxLat-minLat
	cellSize := math.Min(w, h)
	if cellSize <= 0 {
		return mean, true // zero-area / degenerate ring
	}

	// Signed distance (positive inside) from a scaled point to the polygon: the
	// min distance to any edge of any ring, with the sign from even-odd inclusion.
	dist := func(px, py float64) float64 {
		inside := false
		best := math.Inf(1)
		for _, ring := range rings {
			n := len(ring)
			for i, j := 0, n-1; i < n; j, i = i, i+1 {
				ax, ay := ring[i].Lon*kx, ring[i].Lat
				bx, by := ring[j].Lon*kx, ring[j].Lat
				if (ay > py) != (by > py) && px < (bx-ax)*(py-ay)/(by-ay)+ax {
					inside = !inside
				}
				if d := segDist(px, py, ax, ay, bx, by); d < best {
					best = d
				}
			}
		}
		if inside {
			return best
		}
		return -best
	}
	mkCell := func(x, y, half float64) plCell {
		d := dist(x, y)
		return plCell{x: x, y: y, half: half, d: d, max: d + half*math.Sqrt2}
	}

	precision := math.Max(w, h) / 200 // ~0.5% of the span: ample for a label point
	half := cellSize / 2
	best := mkCell((xMin+xMax)/2, (minLat+maxLat)/2, 0) // bbox-centre seed
	pq := &plCellHeap{}
	for x := xMin; x < xMax; x += cellSize {
		for y := minLat; y < maxLat; y += cellSize {
			heap.Push(pq, mkCell(x+half, y+half, half))
		}
	}
	const maxCells = 20000 // safety cap for very large/high-vertex rings
	for processed := 0; pq.Len() > 0; processed++ {
		c := heap.Pop(pq).(plCell)
		if c.d > best.d {
			best = c
		}
		if c.max-best.d <= precision || processed >= maxCells {
			continue
		}
		hh := c.half / 2
		heap.Push(pq, mkCell(c.x-hh, c.y-hh, hh))
		heap.Push(pq, mkCell(c.x+hh, c.y-hh, hh))
		heap.Push(pq, mkCell(c.x-hh, c.y+hh, hh))
		heap.Push(pq, mkCell(c.x+hh, c.y+hh, hh))
	}
	return geo.LatLon{Lat: best.y, Lon: best.x / kx}, true
}

// ringCentroid returns the area centroid (centre of gravity) of a polygon ring
// via the shoelace formula. ok is false for a degenerate (zero-area) ring. The
// ring may be open or closed; edges wrap. Computed in raw lon/lat — the slight
// cos(lat) skew is immaterial for a centring point over a chart-sized area.
func ringCentroid(ring []geo.LatLon) (geo.LatLon, bool) {
	n := len(ring)
	if n < 3 {
		return geo.LatLon{}, false
	}
	var a, cx, cy float64
	for i := 0; i < n; i++ {
		j := (i + 1) % n
		cross := ring[i].Lon*ring[j].Lat - ring[j].Lon*ring[i].Lat
		a += cross
		cx += (ring[i].Lon + ring[j].Lon) * cross
		cy += (ring[i].Lat + ring[j].Lat) * cross
	}
	if math.Abs(a) < 1e-12 {
		return geo.LatLon{}, false
	}
	a *= 0.5
	return geo.LatLon{Lat: cy / (6 * a), Lon: cx / (6 * a)}, true
}

// pointInRingsEvenOdd reports whether p is inside the even-odd union of rings
// (exterior boundary + holes): inside the exterior AND outside every hole.
func pointInRingsEvenOdd(p geo.LatLon, rings [][]geo.LatLon) bool {
	inside := false
	for _, ring := range rings {
		n := len(ring)
		for i, j := 0, n-1; i < n; j, i = i, i+1 {
			if (ring[i].Lat > p.Lat) != (ring[j].Lat > p.Lat) &&
				p.Lon < (ring[j].Lon-ring[i].Lon)*(p.Lat-ring[i].Lat)/(ring[j].Lat-ring[i].Lat)+ring[i].Lon {
				inside = !inside
			}
		}
	}
	return inside
}

// plCell is one square candidate region in the polylabel search (scaled space).
type plCell struct {
	x, y, half float64 // centre and half-size
	d          float64 // signed distance from centre to the polygon (+ inside)
	max        float64 // upper bound on d anywhere in the cell (d + half*√2)
}

// plCellHeap is a max-heap on plCell.max (most-promising cell first).
type plCellHeap []plCell

func (h plCellHeap) Len() int           { return len(h) }
func (h plCellHeap) Less(i, j int) bool { return h[i].max > h[j].max }
func (h plCellHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *plCellHeap) Push(x any)        { *h = append(*h, x.(plCell)) }
func (h *plCellHeap) Pop() any {
	old := *h
	n := len(old)
	c := old[n-1]
	*h = old[:n-1]
	return c
}

// segDist returns the Euclidean distance from point (px,py) to segment a–b.
func segDist(px, py, ax, ay, bx, by float64) float64 {
	dx, dy := bx-ax, by-ay
	if dx != 0 || dy != 0 {
		t := ((px-ax)*dx + (py-ay)*dy) / (dx*dx + dy*dy)
		switch {
		case t > 1:
			ax, ay = bx, by
		case t > 0:
			ax, ay = ax+dx*t, ay+dy*t
		}
	}
	dx, dy = px-ax, py-ay
	return math.Sqrt(dx*dx + dy*dy)
}

// pointInRing reports whether p is inside the polygon ring (ray casting).
func pointInRing(p geo.LatLon, ring []geo.LatLon) bool {
	in := false
	for i, j := 0, len(ring)-1; i < len(ring); j, i = i, i+1 {
		yi, yj := ring[i].Lat, ring[j].Lat
		xi, xj := ring[i].Lon, ring[j].Lon
		if (yi > p.Lat) != (yj > p.Lat) &&
			p.Lon < (xj-xi)*(p.Lat-yi)/(yj-yi)+xi {
			in = !in
		}
	}
	return in
}

// geometryCode maps an s57 geometry type to the S-52 LUPT geometry code.
func geometryCode(t s57.GeometryType) string {
	switch t {
	case s57.GeometryTypePoint:
		return "P"
	case s57.GeometryTypeLineString:
		return "L"
	case s57.GeometryTypePolygon:
		return "A"
	default:
		return "P"
	}
}

// goGeomType is the CSContext.GeometryType string form.
func goGeomType(code string) string {
	switch code {
	case "L":
		return "Line"
	case "A":
		return "Area"
	default:
		return "Point"
	}
}

// geometryOf converts s57 geometry to the portrayal geom (lat/lon). SOUNDG is
// handled separately by BuildFeature (per-point).
func geometryOf(g s57.Geometry) geom {
	switch g.Type {
	case s57.GeometryTypePoint:
		if len(g.Coordinates) == 0 || len(g.Coordinates[0]) < 2 {
			return geom{kind: geomNone}
		}
		c := g.Coordinates[0]
		return geom{kind: geomPoint, point: geo.LatLon{Lat: c[1], Lon: c[0]}}
	case s57.GeometryTypeLineString:
		// Drawable line parts (masked / data-limit edges already removed by the
		// parser, S-52 §8.6.2). A non-nil Lines means the parser computed the
		// drawable line — stroke each part (empty ⇒ stroke nothing). Nil means no
		// masking applied → stroke the full flat line, unchanged.
		var lineParts [][]geo.LatLon
		if g.Lines != nil {
			lineParts = make([][]geo.LatLon, 0, len(g.Lines))
			for _, lp := range g.Lines {
				if pts := coordsToLatLon(lp); len(pts) >= 2 {
					lineParts = append(lineParts, pts)
				}
			}
		}
		return geom{kind: geomLine, line: coordsToLatLon(g.Coordinates), lineParts: lineParts}
	case s57.GeometryTypePolygon:
		var rings [][]geo.LatLon
		if len(g.Rings) > 0 {
			for _, r := range g.Rings {
				rings = append(rings, coordsToLatLon(r.Coordinates))
			}
		} else if len(g.Coordinates) > 0 {
			rings = append(rings, coordsToLatLon(g.Coordinates))
		}
		// Drawable border polylines (masked / data-limit edges already removed by
		// the parser, S-52 §8.6.2). The fill still uses the complete rings. A
		// non-nil (even if empty) BoundaryLines means the parser computed the
		// drawable border — use it (empty ⇒ stroke nothing). Nil means it wasn't
		// computed (fallback geometry) → stroke the full rings.
		var boundary [][]geo.LatLon
		if g.BoundaryLines != nil {
			boundary = make([][]geo.LatLon, 0, len(g.BoundaryLines))
			for _, bl := range g.BoundaryLines {
				if pts := coordsToLatLon(bl); len(pts) >= 2 {
					boundary = append(boundary, pts)
				}
			}
		}
		return geom{kind: geomArea, area: rings, boundary: boundary}
	default:
		return geom{kind: geomNone}
	}
}

func coordsToLatLon(coords [][]float64) []geo.LatLon {
	out := make([]geo.LatLon, 0, len(coords))
	for _, c := range coords {
		if len(c) < 2 {
			continue
		}
		out = append(out, geo.LatLon{Lat: c[1], Lon: c[0]})
	}
	return out
}

func cloneRings(rings [][]geo.LatLon) [][]geo.LatLon {
	out := make([][]geo.LatLon, len(rings))
	for i, r := range rings {
		out[i] = clonePts(r)
	}
	return out
}

func clonePts(pts []geo.LatLon) []geo.LatLon {
	out := make([]geo.LatLon, len(pts))
	copy(out, pts)
	return out
}

// mapHJust / mapVJust map S-52 SHOWTEXT justification codes (§9.1) to alignments.
// HJUST: 1=centre, 2=right, 3=left. VJUST: 1=bottom, 2=centre, 3=top.
func mapHJust(h int) HAlign {
	switch h {
	case 1:
		return HAlignCenter
	case 2:
		return HAlignRight
	default:
		return HAlignLeft
	}
}

func mapVJust(v int) VAlign {
	// S-52 §8.3.3.2 VJUST: 2 centre, 3 top, else (incl. 1) bottom.
	switch v {
	case 2:
		return VAlignMiddle
	case 3:
		return VAlignTop
	default:
		return VAlignBottom
	}
}

func maxF32(a, b float32) float32 {
	if a > b {
		return a
	}
	return b
}

// formatSubstitute substitutes attribute values into a TE/TX C-printf format
// string (S-52 §8.3.3.3 — e.g. "clr op %4.1lf" with VERCOP -> "clr op 12.3").
// Handles %[flags][width][.precision][l|h|L]conv; width/flags only affect
// fixed-pitch padding so they are ignored, precision is honoured for floats.
// Returns ok=false when a referenced attribute is absent — per S-52 a label with
// a missing mandatory field is not drawn.
func formatSubstitute(attrs map[string]interface{}, format string, attrNames []string) (string, bool) {
	var out strings.Builder
	attrIdx := 0
	i := 0
	for i < len(format) {
		if format[i] != '%' || i+1 >= len(format) {
			out.WriteByte(format[i])
			i++
			continue
		}
		if format[i+1] == '%' {
			out.WriteByte('%')
			i += 2
			continue
		}
		// Scan the printf spec: flags, width, .precision, length, conv.
		j := i + 1
		flagsStart := j
		for j < len(format) && strings.IndexByte("-+ #0", format[j]) >= 0 {
			j++
		}
		flags := format[flagsStart:j]
		width := 0
		for j < len(format) && format[j] >= '0' && format[j] <= '9' {
			width = width*10 + int(format[j]-'0')
			j++
		}
		precision := -1
		if j < len(format) && format[j] == '.' {
			j++
			p := 0
			for j < len(format) && format[j] >= '0' && format[j] <= '9' {
				p = p*10 + int(format[j]-'0')
				j++
			}
			precision = p
		}
		for j < len(format) && (format[j] == 'l' || format[j] == 'h' || format[j] == 'L') {
			j++
		}
		if j >= len(format) {
			out.WriteString(format[i:]) // malformed trailing spec -> keep literal
			break
		}
		switch conv := format[j]; conv {
		case 's', 'c', 'd', 'i', 'u', 'x', 'f', 'e', 'g':
			if attrIdx >= len(attrNames) {
				return "", false
			}
			acr := attrNames[attrIdx]
			attrIdx++
			val, ok := lookupAttributeText(attrs, acr)
			if !ok {
				return "", false
			}
			appendConverted(&out, val, conv, precision, width, flags)
		default:
			out.WriteString(format[i : j+1]) // unknown conversion -> literal
		}
		i = j + 1
	}
	return out.String(), true
}

// appendConverted appends val formatted per the printf conversion: floats honour
// precision, integer conversions round, everything else passes through. The
// zero-pad flag + width are applied (e.g. "%03.0lf" → 90 ⇒ "090", the S-52
// bearing format); space/width padding is intentionally NOT applied (proportional
// chart text needs no fixed-pitch alignment).
func appendConverted(out *strings.Builder, val string, conv byte, precision, width int, flags string) {
	var s string
	switch conv {
	case 'f', 'e', 'g':
		x, err := strconv.ParseFloat(strings.TrimSpace(val), 64)
		if err != nil {
			s = val
		} else if precision >= 0 {
			s = strconv.FormatFloat(x, 'f', precision, 64)
		} else {
			s = strconv.FormatFloat(x, 'g', -1, 64)
		}
	case 'd', 'i', 'u', 'x':
		x, err := strconv.ParseFloat(strings.TrimSpace(val), 64)
		if err != nil {
			s = val
		} else {
			s = strconv.FormatInt(int64(math.Round(x)), 10)
		}
	default:
		s = val
	}
	out.WriteString(zeroPad(s, width, flags))
}

// zeroPad left-pads s with zeros to width when the printf '0' flag is set (and
// not left-justified '-'), inserting after any leading sign. Other padding is
// ignored (see appendConverted).
func zeroPad(s string, width int, flags string) string {
	if width <= len(s) || !strings.ContainsRune(flags, '0') || strings.ContainsRune(flags, '-') {
		return s
	}
	pad := strings.Repeat("0", width-len(s))
	if len(s) > 0 && (s[0] == '-' || s[0] == '+' || s[0] == ' ') {
		return s[:1] + pad + s[1:]
	}
	return pad + s
}

// stringifyScalar renders a scalar attribute value as label text. Integer-valued
// floats drop the decimal, matching the lookup attribute-text "{d}" output.
func stringifyScalar(v interface{}) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		if t == math.Trunc(t) && !math.IsInf(t, 0) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'g', -1, 64)
	case float32:
		return stringifyScalar(float64(t))
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// isUnknownClass reports that the S-57 parser could not resolve the feature's
// numeric object code to a catalogue acronym — it names such classes "OBJL_<code>"
// (see internal/s57/parser/objectclass.go). These are proprietary / non-ENC
// classes (e.g. Inland ENC extensions) with no Presentation Library lookup entry.
// S-52 PresLib e4.0.0 §2.30 & §10.1.1: such objects must NOT be hidden — each is
// shown with the magenta question-mark SY(QUESMRK1) at IMO category Standard so
// the mariner is told an unknown object exists.
func isUnknownClass(objClass string) bool {
	return strings.HasPrefix(objClass, "OBJL_")
}

// unknownObjectBuild is the §10.1.1 portrayal of an unknown-class feature: a
// single QUESMRK1 question-mark symbol at the feature's position.
func unknownObjectBuild(f *s57.Feature) FeatureBuild {
	anchor, ok := representativePoint(f)
	if !ok {
		// No usable coordinate (e.g. a line/area feature whose spatial edges didn't
		// resolve) — there is nowhere to put the question mark, so emit nothing
		// rather than stamp it at null island (0,0).
		return FeatureBuild{DisplayCategory: displayStandard}
	}
	return FeatureBuild{
		Primitives: []Primitive{SymbolCall{
			Anchor:         anchor,
			SymbolName:     "QUESMRK1",
			Scale:          DefaultPxPerSymbolUnit,
			SoundingDepthM: nan32,
			DangerDepthM:   nan32,
		}},
		DisplayPriority: 6, // ordinary point-symbol priority
		DisplayCategory: displayStandard,
	}
}

// newObjectBuild portrays an S-57 NEWOBJ whose primitive the S-101 alias rule
// (VirtualAISAidToNavigation) rejects: that rule is POINT-only, so a line or area
// NEWOBJ errors and would otherwise be suppressed (drawing nothing). The S-52
// PresLib reference (§10.3.3.8, "Default symbol for NEWOBJ") draws line/area new
// objects with a dashed magenta boundary, so emit that. Point NEWOBJ never reaches
// here — it portrays through the V-AIS rule, which is correct for real S-101 data
// (V-AIS is encoded as a point NEWOBJ).
func newObjectBuild(f *s57.Feature) FeatureBuild {
	g := f.Geometry()
	toLL := func(cs [][]float64) []geo.LatLon {
		out := make([]geo.LatLon, 0, len(cs))
		for _, c := range cs {
			if len(c) >= 2 {
				out = append(out, geo.LatLon{Lat: c[1], Lon: c[0]})
			}
		}
		return out
	}
	dashed := func(pts []geo.LatLon, closed bool) Primitive {
		if closed && len(pts) > 1 && pts[0] != pts[len(pts)-1] {
			pts = append(pts, pts[0]) // close the ring
		}
		return StrokeLine{Points: pts, ColorToken: "CHMGF", WidthPx: 1.5, Dash: DashDashed}
	}
	var prims []Primitive
	switch g.Type {
	case s57.GeometryTypeLineString:
		if pts := toLL(g.Coordinates); len(pts) >= 2 {
			prims = append(prims, dashed(pts, false))
		}
	case s57.GeometryTypePolygon:
		for _, r := range g.Rings {
			if pts := toLL(r.Coordinates); len(pts) >= 2 {
				prims = append(prims, dashed(pts, true))
			}
		}
	}
	if len(prims) == 0 {
		return FeatureBuild{DisplayCategory: displayStandard}
	}
	return FeatureBuild{Primitives: prims, DisplayPriority: 6, DisplayCategory: displayStandard}
}

// sweptAreaBuild portrays an S-57 SWPARE (swept area). Its S-101 class is
// SweptArea, but the Portrayal Catalogue ships no SweptArea.lua rule (an IHO gap),
// so it errors and would be suppressed. The S-52 PresLib reference (page 243)
// draws a dashed boundary around the area plus a "swept to <DRVAL1>" depth label,
// so emit that.
func sweptAreaBuild(f *s57.Feature) FeatureBuild {
	g := f.Geometry()
	if g.Type != s57.GeometryTypePolygon {
		return FeatureBuild{DisplayCategory: displayStandard}
	}
	ringLL := func(cs [][]float64) []geo.LatLon {
		out := make([]geo.LatLon, 0, len(cs))
		for _, c := range cs {
			if len(c) >= 2 {
				out = append(out, geo.LatLon{Lat: c[1], Lon: c[0]})
			}
		}
		if len(out) > 1 && out[0] != out[len(out)-1] {
			out = append(out, out[0]) // close the ring
		}
		return out
	}
	var prims []Primitive
	for _, r := range g.Rings {
		if pts := ringLL(r.Coordinates); len(pts) >= 2 {
			prims = append(prims, StrokeLine{Points: pts, ColorToken: "CHGRD", WidthPx: 1, Dash: DashDashed})
		}
	}
	if len(prims) == 0 {
		return FeatureBuild{DisplayCategory: displayStandard}
	}
	// Swept-depth notation at the area's representative point: the SWPARE51 "⊔"
	// bracket centred on the point, with the "swept to <DRVAL1>" label just above
	// it (S-101 HighConfidenceDepthArea: SY(SWPARE51) + text at LocalOffset 0,-3.51
	// mm). The S-101 SweptArea rule is an IHO gap, so this Go fallback reproduces it.
	if a, ok := areaSurfacePoint(ringLL(exteriorRing(g))); ok {
		prims = append(prims, SymbolCall{
			Anchor: a, SymbolName: "SWPARE51", Scale: DefaultPxPerSymbolUnit,
			SoundingDepthM: nan32, DangerDepthM: nan32,
		})
		if d, ok := floatAttr(f.Attributes(), "DRVAL1"); ok {
			prims = append(prims, DrawText{
				// VAlignTop anchors the text at its top edge on the rep point, so it
				// drops BELOW the SWPARE51 bracket (which extends UP from the same
				// point) instead of overprinting it — the client text layer ignores
				// per-feature pixel offsets, so position via the anchor.
				Anchor: a, Text: "swept to " + strconv.FormatFloat(d, 'f', -1, 64),
				FontSizePx: 11, ColorToken: "CHBLK", HAlign: HAlignCenter, VAlign: VAlignTop,
			})
		}
	}
	return FeatureBuild{Primitives: prims, DisplayPriority: 6, DisplayCategory: displayStandard}
}

// representativePoint returns a single lat/lon to anchor a point symbol on a
// feature of any geometry: the point itself, a line's midpoint vertex, or an
// area's exterior-ring centroid. ok is false when the geometry carries no usable
// coordinate (so the caller must not place a symbol).
func representativePoint(f *s57.Feature) (geo.LatLon, bool) {
	g := f.Geometry()
	switch g.Type {
	case s57.GeometryTypeLineString:
		if n := len(g.Coordinates); n > 0 {
			if c := g.Coordinates[n/2]; len(c) >= 2 {
				return geo.LatLon{Lat: c[1], Lon: c[0]}, true
			}
		}
	case s57.GeometryTypePolygon:
		ring := exteriorRing(g)
		pts := make([]geo.LatLon, 0, len(ring))
		for _, c := range ring {
			if len(c) >= 2 {
				pts = append(pts, geo.LatLon{Lat: c[1], Lon: c[0]})
			}
		}
		if len(pts) > 0 {
			return areaSurfacePoint(pts)
		}
	}
	// Point geometry, or a fallback for any geometry whose first coordinate is set.
	if len(g.Coordinates) > 0 && len(g.Coordinates[0]) >= 2 {
		return geo.LatLon{Lat: g.Coordinates[0][1], Lon: g.Coordinates[0][0]}, true
	}
	return geo.LatLon{}, false
}

// exteriorRing returns the coordinates of a polygon's first exterior ring
// (USAG 1 or 3), falling back to the first ring present.
func exteriorRing(g s57.Geometry) [][]float64 {
	for _, r := range g.Rings {
		if r.Usage == 1 || r.Usage == 3 {
			return r.Coordinates
		}
	}
	if len(g.Rings) > 0 {
		return g.Rings[0].Coordinates
	}
	return nil
}
