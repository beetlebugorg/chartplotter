package portrayal

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/beetlebugorg/chartplotter-go/pkg/geo"
	"github.com/beetlebugorg/chartplotter-go/pkg/s52"
	"github.com/beetlebugorg/chartplotter-go/pkg/s57"
)

// maxCSPDepth bounds recursive CS dispatch (mirrors primitive.zig max_csp_depth).
const maxCSPDepth = 4

// NaN marks "no depth" on sounding/danger fields, matching the Zig sentinel.
var nan32 = float32(math.NaN())

// FeatureBuild is the result of expanding one feature: its viewport-independent
// Primitive stream plus the S-52 display priority that buckets it.
type FeatureBuild struct {
	Primitives      []Primitive
	DisplayPriority int
}

// geom is the portrayal-space geometry handed to the instruction walk. It mirrors
// the Zig s57.Geometry union (point / soundings / line / area). currentDepthM is
// the per-point sounding depth (NaN otherwise), used by SOUNDG03 + carried on the
// emitted symbol so the client can do SNDFRM04 without a re-bake.
type geom struct {
	kind         geomKind
	point        geo.LatLon
	line         []geo.LatLon
	area         [][]geo.LatLon
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

// BuildFeature runs the S-52 expand step (lookup + CSP + instruction walk) for
// one feature and returns its lat/lon Primitive stream. ok is false when the
// PresLib has no lookup entry for the feature's class/geometry. Mirrors
// portrayal/primitive.zig buildFeaturePrimitives.
func BuildFeature(lib *s52.Library, mariner *s52.MarinerSettings, f *s57.Feature) (FeatureBuild, bool) {
	if mariner == nil {
		mariner = s52.DefaultMarinerSettings()
	}
	objClass := f.ObjectClass()
	g := f.Geometry()
	attrs := f.Attributes()

	geomCode := geometryCode(g.Type)
	set := lib.LookupFeatureRaw(objClass, geomCode, attrs, mariner)
	if set == nil {
		return FeatureBuild{}, false
	}

	b := &walker{lib: lib, mariner: mariner, feature: f, attrs: attrs, geomCode: geomCode}

	// Multi-point soundings: SOUNDG carries one depth per coordinate (the z of
	// each [lon,lat,depth]). Run the instruction set once per point with that
	// depth, exactly like the Zig per-point loop, so SOUNDG03 emits a digit
	// chain per sounding.
	if objClass == "SOUNDG" && g.Type == s57.GeometryTypePoint {
		for _, c := range g.Coordinates {
			if len(c) < 2 {
				continue
			}
			pg := geom{kind: geomPoint, point: geo.LatLon{Lat: c[1], Lon: c[0]}}
			if len(c) >= 3 {
				pg.currentDepth = c[2]
				pg.hasDepth = true
			}
			b.emit(set.Instructions, pg, 0)
		}
		return FeatureBuild{Primitives: b.out, DisplayPriority: set.DisplayPriority}, true
	}

	b.emit(set.Instructions, geometryOf(g), 0)
	return FeatureBuild{Primitives: b.out, DisplayPriority: set.DisplayPriority}, true
}

type walker struct {
	lib      *s52.Library
	mariner  *s52.MarinerSettings
	feature  *s57.Feature
	attrs    map[string]interface{}
	geomCode string
	out      []Primitive
}

// emit walks one instruction list against geometry g, appending Primitives.
// Mirrors emitInstructions in primitive.zig.
func (w *walker) emit(list []s52.Instruction, g geom, depth int) {
	for _, ins := range list {
		switch in := ins.(type) {
		case *s52.ACInstruction:
			w.emitFill(in.Color, g)
		case *s52.APInstruction:
			w.emitPatternFill(in.PatternID, g)
		case *s52.LSInstruction:
			w.emitStroke(in.Color, lsStyleToDash(in.Style), float32(in.Width), g)
		case *s52.LCInstruction:
			w.emitLinePattern(in.LineStyleID, g)
		case *s52.SYInstruction:
			w.emitSymbol(in.SymbolID, float32(in.Rotation), g)
		case *s52.SectorInstruction:
			if g.kind == geomPoint {
				w.out = append(w.out, SectorLight{
					Anchor: g.point,
					Sector: SectorParams{
						StartAngleDeg: in.StartAngle,
						EndAngleDeg:   in.EndAngle,
						RadiusNM:      in.Radius,
						ColorToken:    in.Color,
						Transparency:  in.Transparency,
						ShowLegs:      in.ShowLegs,
					},
				})
			}
		case *s52.TXInstruction:
			w.emitText(in.TextInstruction, g)
		case *s52.CSInstruction:
			if depth >= maxCSPDepth {
				continue
			}
			ctx := s52.NewCSContext(w.csAttrs(g), goGeomType(w.geomCode), nil, w.mariner)
			generated, err := w.lib.ExecuteCS(in.ProcedureName, ctx)
			if err != nil {
				continue
			}
			w.emit(generated, g, depth+1)
		}
	}
}

// csAttrs is the attribute map seen by a CS procedure. For soundings it injects
// DEPTH (metres) so SOUNDG03 picks the digit chain for this point.
func (w *walker) csAttrs(g geom) map[string]interface{} {
	if !g.hasDepth {
		return w.attrs
	}
	m := make(map[string]interface{}, len(w.attrs)+1)
	for k, v := range w.attrs {
		m[k] = v
	}
	m["DEPTH"] = g.currentDepth
	return m
}

func (w *walker) emitFill(colorToken string, g geom) {
	if g.kind != geomArea || len(g.area) == 0 {
		return
	}
	w.out = append(w.out, FillPolygon{Rings: cloneRings(g.area), ColorToken: colorToken})
}

func (w *walker) emitPatternFill(patternName string, g geom) {
	if g.kind != geomArea || len(g.area) == 0 || len(g.area[0]) < 3 {
		return
	}
	w.out = append(w.out, PatternFill{Rings: cloneRings(g.area), PatternName: patternName})
}

func (w *walker) emitStroke(colorToken string, dash Dash, widthPx float32, g geom) {
	switch g.kind {
	case geomLine:
		w.emitStrokeOne(colorToken, dash, widthPx, g.line)
	case geomArea:
		for _, ring := range g.area {
			w.emitStrokeOne(colorToken, dash, widthPx, ring)
		}
	}
}

func (w *walker) emitStrokeOne(colorToken string, dash Dash, widthPx float32, pts []geo.LatLon) {
	if len(pts) < 2 {
		return
	}
	w.out = append(w.out, StrokeLine{Points: clonePts(pts), ColorToken: colorToken, WidthPx: widthPx, Dash: dash})
}

func (w *walker) emitLinePattern(linestyleName string, g geom) {
	switch g.kind {
	case geomLine:
		w.emitLinePatternOne(linestyleName, g.line)
	case geomArea:
		for _, ring := range g.area {
			w.emitLinePatternOne(linestyleName, ring)
		}
	}
}

func (w *walker) emitLinePatternOne(linestyleName string, pts []geo.LatLon) {
	if len(pts) < 2 {
		return
	}
	w.out = append(w.out, LinePattern{Points: clonePts(pts), LinestyleName: linestyleName})
}

func (w *walker) emitSymbol(symbolName string, rotationDeg float32, g geom) {
	var anchor geo.LatLon
	switch g.kind {
	case geomPoint:
		anchor = g.point
	default:
		return // symbols anchor to points / sounding points only
	}
	isSounding := isSoundingDigit(symbolName)
	var halo *SymbolHalo
	soundingDepth := nan32
	if isSounding {
		halo = &SymbolHalo{ColorToken: "CHWHT", ExtraWidthPx: 1.5}
		if g.hasDepth {
			soundingDepth = float32(g.currentDepth)
		}
	}
	w.out = append(w.out, SymbolCall{
		Anchor:         anchor,
		SymbolName:     symbolName,
		RotationDeg:    rotationDeg,
		Scale:          DefaultPxPerSymbolUnit,
		Halo:           halo,
		SoundingDepthM: soundingDepth,
		DangerDepthM:   nan32,
	})
}

func (w *walker) emitText(tx *s52.TextInstruction, g geom) {
	if tx == nil {
		return
	}
	text := tx.Text
	if tx.IsAttributeReference {
		v, ok := lookupAttributeText(w.attrs, tx.Text)
		if !ok || v == "" {
			return // missing mandatory field -> label not drawn (S-52)
		}
		text = v
	}
	if text == "" {
		return
	}
	anchor, ok := textAnchor(g)
	if !ok {
		return
	}
	fontSizePx := float32(tx.Font.BodySize)
	var halo *TextHalo
	if fontSizePx >= 10.0 {
		halo = &TextHalo{ColorToken: "CHWHT", WidthPx: maxF32(0.8, fontSizePx*0.08)}
	}
	w.out = append(w.out, DrawText{
		Anchor:     anchor,
		Text:       text,
		FontSizePx: fontSizePx,
		ColorToken: tx.Color,
		Halo:       halo,
		HAlign:     mapHJust(tx.HJust),
		VAlign:     mapVJust(tx.VJust),
		OffsetXPx:  float32(tx.XOffset) * fontSizePx,
		OffsetYPx:  float32(tx.YOffset) * fontSizePx,
	})
}

// -- helpers -----------------------------------------------------------------

func lsStyleToDash(style string) Dash {
	switch strings.ToUpper(style) {
	case "DASH":
		return DashDashed
	case "DOTT":
		return DashDotted
	default:
		return DashSolid
	}
}

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
		var sumLat, sumLon float64
		for _, p := range g.area[0] {
			sumLat += p.Lat
			sumLon += p.Lon
		}
		n := float64(len(g.area[0]))
		return geo.LatLon{Lat: sumLat / n, Lon: sumLon / n}, true
	default:
		return geo.LatLon{}, false
	}
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
		return geom{kind: geomLine, line: coordsToLatLon(g.Coordinates)}
	case s57.GeometryTypePolygon:
		var rings [][]geo.LatLon
		if len(g.Rings) > 0 {
			for _, r := range g.Rings {
				rings = append(rings, coordsToLatLon(r.Coordinates))
			}
		} else if len(g.Coordinates) > 0 {
			rings = append(rings, coordsToLatLon(g.Coordinates))
		}
		return geom{kind: geomArea, area: rings}
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
	switch v {
	case 1:
		return VAlignBottom
	case 2:
		return VAlignMiddle
	default:
		return VAlignTop
	}
}

func maxF32(a, b float32) float32 {
	if a > b {
		return a
	}
	return b
}

// stringifyScalar renders a scalar attribute value as label text. Integer-valued
// floats drop the decimal, matching the Zig lookupAttributeText "{d}" output.
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
