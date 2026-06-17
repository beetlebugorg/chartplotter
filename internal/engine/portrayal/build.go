package portrayal

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/beetlebugorg/chartplotter/pkg/geo"
	"github.com/beetlebugorg/chartplotter/pkg/s52"
	"github.com/beetlebugorg/chartplotter/pkg/s57"
)

// maxCSPDepth bounds recursive CS dispatch.
const maxCSPDepth = 4

// NaN marks "no depth" on sounding/danger fields (the sentinel value).
var nan32 = float32(math.NaN())

// FeatureBuild is the result of expanding one feature: its viewport-independent
// Primitive stream plus the S-52 display priority (buckets draw order) and
// display category (base/standard/other; the client's detail filter).
type FeatureBuild struct {
	Primitives      []Primitive
	DisplayPriority int
	DisplayCategory int
}

// geom is the portrayal-space geometry handed to the instruction walk. It mirrors
// the s57 geometry union (point / soundings / line / area). currentDepthM is
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
// PresLib has no lookup entry for the feature's class/geometry.
func BuildFeature(lib *s52.Library, mariner *s52.MarinerSettings, f *s57.Feature) (FeatureBuild, bool) {
	if mariner == nil {
		mariner = s52.DefaultMarinerSettings()
	}
	set := lib.LookupFeatureRaw(f.ObjectClass(), geometryCode(f.Geometry().Type), f.Attributes(), mariner)
	if set == nil {
		return FeatureBuild{}, false
	}
	return buildFromSet(lib, mariner, f, set), true
}

// buildFromSet runs the S-52 instruction walk for a feature against an
// already-resolved LUPT InstructionSet. BuildFeature is the lookup+walk wrapper;
// BuildFeaturePasses calls this directly to REUSE the raw lookups it already
// performed for the plain/symbolized diff, instead of re-resolving them inside a
// second BuildFeature call (the 3–4×-per-area-feature lookup cost).
func buildFromSet(lib *s52.Library, mariner *s52.MarinerSettings, f *s57.Feature, set *s52.InstructionSet) FeatureBuild {
	objClass := f.ObjectClass()
	g := f.Geometry()
	attrs := f.Attributes()
	geomCode := geometryCode(g.Type)

	b := &walker{lib: lib, mariner: mariner, feature: f, attrs: attrs, geomCode: geomCode}

	// Multi-point soundings: SOUNDG carries one depth per coordinate (the z of
	// each [lon,lat,depth]). Run the instruction set once per point with that
	// depth, once per point, so SOUNDG03 emits a digit chain per sounding.
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
		return FeatureBuild{Primitives: b.out, DisplayPriority: set.DisplayPriority, DisplayCategory: set.DisplayCategory}
	}

	pg := geometryOf(g)
	b.emit(set.Instructions, pg, 0)
	b.out = applyDangerDepth(b.out, objClass, attrs, pg)
	return FeatureBuild{Primitives: b.out, DisplayPriority: set.DisplayPriority, DisplayCategory: set.DisplayCategory}
}

// Boundary-symbolization tags (S-52 §8.6.1) stamped on each primitive's baked
// `bnd`, which the client's boundaryFilter keys off: 2 = style-independent
// (always shown), 0 = plain-boundary pass, 1 = symbolized-boundary pass.
const (
	BndCommon     = 2
	BndPlain      = 0
	BndSymbolized = 1
)

// FeatureBuildPass is one boundary-symbolization pass: the built primitives plus
// the bnd tag the baker stamps on every primitive of the pass.
type FeatureBuildPass struct {
	Build FeatureBuild
	Bnd   int
}

// BuildFeaturePasses expands a feature into its boundary-symbolization passes
// (S-52 §8.6.1). Non-area features, and areas whose plain and symbolized
// boundaries are identical, get ONE pass tagged bnd=2 (style-independent). A
// style-variant area — a distinct SYMBOLIZED_BOUNDARIES lookup, or one routing
// through RESARE04 (the only CSP that reads the boundary style) — gets TWO
// passes, plain (bnd=0) and symbolized (bnd=1), so the client toggles boundary
// style live with no re-bake.
func BuildFeaturePasses(lib *s52.Library, mariner *s52.MarinerSettings, f *s57.Feature) []FeatureBuildPass {
	if mariner == nil {
		mariner = s52.DefaultMarinerSettings()
	}
	objClass := f.ObjectClass()
	geomCode := geometryCode(f.Geometry().Type)
	attrs := f.Attributes()

	mPlain := *mariner
	mPlain.SymbolizedBoundaries = false
	// One plain lookup, reused for the build below (no second internal lookup).
	setP := lib.LookupFeatureRaw(objClass, geomCode, attrs, &mPlain)
	if setP == nil {
		return nil
	}
	buildP := buildFromSet(lib, &mPlain, f, setP)
	one := []FeatureBuildPass{{Build: buildP, Bnd: BndCommon}}
	if f.Geometry().Type != s57.GeometryTypePolygon {
		return one
	}
	mSym := *mariner
	mSym.SymbolizedBoundaries = true
	setS := lib.LookupFeatureRaw(objClass, geomCode, attrs, &mSym)
	if setS == nil {
		return one
	}
	if !instructionSetsDiffer(setP, setS) && !routesToResare(setP) {
		return one
	}
	// Symbolized boundaries differ — build the second pass, reusing setS.
	buildS := buildFromSet(lib, &mSym, f, setS)
	return []FeatureBuildPass{
		{Build: buildP, Bnd: BndPlain},
		{Build: buildS, Bnd: BndSymbolized},
	}
}

// routesToResare reports whether a lookup dispatches RESARE04 — the one CSP whose
// output (the area boundary, LC vs LS) depends on the mariner's boundary style.
func routesToResare(set *s52.InstructionSet) bool {
	for _, ins := range set.Instructions {
		if cs, ok := ins.(*s52.CSInstruction); ok && cs.ProcedureName == "RESARE04" {
			return true
		}
	}
	return false
}

// instructionSetsDiffer reports whether two lookups resolved to different
// instructions — e.g. a distinct PLAIN_BOUNDARIES vs SYMBOLIZED_BOUNDARIES LUP.
func instructionSetsDiffer(a, b *s52.InstructionSet) bool {
	if len(a.Instructions) != len(b.Instructions) {
		return true
	}
	for i := range a.Instructions {
		if a.Instructions[i].String() != b.Instructions[i].String() {
			return true
		}
	}
	return false
}

// applyDangerDepth reproduces the net OBSTRN06/WRECKS05 danger behaviour (S-52
// §13.2.6/§13.2.20): a sounded obstruction/wreck (one with VALSOU) is portrayed
// as the dangerous symbol DANGER01 carrying its depth + the deep variant
// DANGER02, so the client swaps shallow<->deep against the LIVE safety contour
// with no re-bake. The reused Go CSPs resolve this at bake time (ISODGR01/
// OBSTRNxx) instead, so we override here. Non-symbol primitives (e.g. the dotted
// foul boundary) are kept; the CSP's symbol(s) are replaced by the one
// danger-tagged DANGER01.
func applyDangerDepth(prims []Primitive, class string, attrs map[string]interface{}, g geom) []Primitive {
	if class != "OBSTRN" && class != "WRECKS" {
		return prims
	}
	valsou, ok := floatAttr(attrs, "VALSOU")
	if !ok {
		return prims
	}
	anchor, hasAnchor := dangerAnchor(prims, g)
	if !hasAnchor {
		return prims
	}
	danger := SymbolCall{
		Anchor:         anchor,
		SymbolName:     "DANGER01",
		Scale:          DefaultPxPerSymbolUnit,
		SoundingDepthM: nan32,
		DangerDepthM:   float32(valsou),
		DeepSymbolName: "DANGER02",
	}
	out := make([]Primitive, 0, len(prims))
	replaced := false
	for _, p := range prims {
		if _, isSym := p.(SymbolCall); isSym {
			if !replaced {
				out = append(out, danger)
				replaced = true
			}
			continue // drop the CSP's other symbols
		}
		out = append(out, p)
	}
	if !replaced {
		out = append(out, danger)
	}
	return out
}

// dangerAnchor is the anchor for the replacement danger symbol: the first emitted
// SymbolCall's anchor, else the feature's point geometry.
func dangerAnchor(prims []Primitive, g geom) (geo.LatLon, bool) {
	for _, p := range prims {
		if sc, ok := p.(SymbolCall); ok {
			return sc.Anchor, true
		}
	}
	if g.kind == geomPoint {
		return g.point, true
	}
	return geo.LatLon{}, false
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

type walker struct {
	lib      *s52.Library
	mariner  *s52.MarinerSettings
	feature  *s57.Feature
	attrs    map[string]interface{}
	geomCode string
	out      []Primitive
}

// emit walks one instruction list against geometry g, appending Primitives.
// Mirrors the instruction-emit walk.
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
			rot := in.Rotation
			if in.RotationAttr != "" {
				if v, ok := floatAttr(w.attrs, in.RotationAttr); ok {
					rot = v // e.g. SY(RECTRC57,ORIENT) → the feature's ORIENT bearing
				}
			}
			w.emitSymbol(in.SymbolID, float32(rot), g)
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
			ctx.ObjectClass = w.feature.ObjectClass()
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
	w.out = append(w.out, LinePattern{Points: clonePts(pts), LinestyleName: linestyleName, ColorToken: w.linestyleColor(linestyleName)})
}

// linestyleColor resolves a complex linestyle's primary pen colour token — the
// first PD run's LCRF-mapped pen, mirroring linestyles.json. "" if none.
func (w *walker) linestyleColor(name string) string {
	ls, err := w.lib.GetLineStyle(name)
	if err != nil || ls == nil {
		return ""
	}
	for i := range ls.VectorCommands {
		c := &ls.VectorCommands[i]
		if c.Type != "PD" {
			continue
		}
		// Match linestyles.go: take the pen from the first PD that actually draws
		// (a segment with distinct endpoints), so the token equals linestyles.json.
		for j := 0; j+1 < len(c.Points); j++ {
			if c.Points[j] != c.Points[j+1] {
				return ls.Colors.Roles[c.Role]
			}
		}
	}
	return ""
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
	var text string
	switch {
	case tx.Format != "":
		// TE: substitute attribute values into the C-printf format (S-52
		// §8.3.3.3). A missing referenced attribute suppresses the whole label.
		s, ok := formatSubstitute(w.attrs, tx.Format, tx.FormatAttrs)
		if !ok || s == "" {
			return
		}
		text = s
	case tx.IsAttributeReference:
		v, ok := lookupAttributeText(w.attrs, tx.Text)
		if !ok || v == "" {
			return // missing mandatory field -> label not drawn (S-52)
		}
		text = v
	default:
		text = tx.Text
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
		for j < len(format) && strings.IndexByte("-+ #0", format[j]) >= 0 {
			j++
		}
		for j < len(format) && format[j] >= '0' && format[j] <= '9' {
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
			appendConverted(&out, val, conv, precision)
		default:
			out.WriteString(format[i : j+1]) // unknown conversion -> literal
		}
		i = j + 1
	}
	return out.String(), true
}

// appendConverted appends val formatted per the printf conversion: floats honour
// precision, integer conversions round, everything else passes through.
func appendConverted(out *strings.Builder, val string, conv byte, precision int) {
	switch conv {
	case 'f', 'e', 'g':
		x, err := strconv.ParseFloat(strings.TrimSpace(val), 64)
		if err != nil {
			out.WriteString(val)
			return
		}
		if precision >= 0 {
			out.WriteString(strconv.FormatFloat(x, 'f', precision, 64))
		} else {
			out.WriteString(strconv.FormatFloat(x, 'g', -1, 64))
		}
	case 'd', 'i', 'u', 'x':
		x, err := strconv.ParseFloat(strings.TrimSpace(val), 64)
		if err != nil {
			out.WriteString(val)
			return
		}
		out.WriteString(strconv.FormatInt(int64(math.Round(x)), 10))
	default:
		out.WriteString(val)
	}
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
