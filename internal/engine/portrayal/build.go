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

// BuildFeature runs the S-52 expand step (lookup + CSP + instruction walk) for
// one feature and returns its lat/lon Primitive stream. ok is false when the
// PresLib has no lookup entry for the feature's class/geometry.
func BuildFeature(lib *s52.Library, mariner *s52.MarinerSettings, f *s57.Feature) (FeatureBuild, bool) {
	if mariner == nil {
		mariner = s52.DefaultMarinerSettings()
	}
	if isUnknownClass(f.ObjectClass()) {
		return unknownObjectBuild(f), true
	}
	set := lib.LookupFeatureRaw(f.ObjectClass(), geometryCode(f.Geometry().Type), f.Attributes(), mariner)
	if set == nil {
		return FeatureBuild{}, false
	}
	return buildFromSet(lib, mariner, f, set, nil), true
}

// buildFromSet runs the S-52 instruction walk for a feature against an
// already-resolved LUPT InstructionSet. BuildFeature is the lookup+walk wrapper;
// BuildFeaturePasses calls this directly to REUSE the raw lookups it already
// performed for the plain/symbolized diff, instead of re-resolving them inside a
// second BuildFeature call (the 3–4×-per-area-feature lookup cost).
func buildFromSet(lib *s52.Library, mariner *s52.MarinerSettings, f *s57.Feature, set *s52.InstructionSet, spatial *s52.SpatialContext) FeatureBuild {
	objClass := f.ObjectClass()
	g := f.Geometry()
	attrs := f.Attributes()
	geomCode := geometryCode(g.Type)

	b := &walker{lib: lib, mariner: mariner, feature: f, attrs: attrs, geomCode: geomCode, spatial: spatial}

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
	b.out = applyDangerDepth(b.out, objClass, attrs)
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

// BuildFeaturePasses expands a feature into its boundary-symbolization passes
// (S-52 §8.6.1). Non-area features, and areas whose plain and symbolized
// boundaries are identical, get ONE pass tagged bnd=2 (style-independent). A
// style-variant area — a distinct SYMBOLIZED_BOUNDARIES lookup, or one routing
// through RESARE04 (the only CSP that reads the boundary style) — gets TWO
// passes, plain (bnd=0) and symbolized (bnd=1), so the client toggles boundary
// style live with no re-bake.
func BuildFeaturePasses(lib *s52.Library, mariner *s52.MarinerSettings, f *s57.Feature, spatial *s52.SpatialContext) []FeatureBuildPass {
	if mariner == nil {
		mariner = s52.DefaultMarinerSettings()
	}
	objClass := f.ObjectClass()
	gtype := f.Geometry().Type
	geomCode := geometryCode(gtype)
	attrs := f.Attributes()

	// Unknown object class (no catalogue acronym → no PresLib lookup): mark it
	// with SY(QUESMRK1) instead of dropping it (S-52 PresLib §2.30 / §10.1.1).
	if isUnknownClass(objClass) {
		return []FeatureBuildPass{{Build: unknownObjectBuild(f), Bnd: BndCommon, Pts: PtsCommon}}
	}

	// Point features: the only style axis is simplified vs paper-chart symbols
	// (matchesTableName switches the point LUP set on SimplifiedPoints).
	// SymbolizedBoundaries is area-only, so points never split on bnd.
	if gtype == s57.GeometryTypePoint {
		return pointPasses(lib, mariner, f, objClass, geomCode, attrs, spatial)
	}

	mPlain := *mariner
	mPlain.SymbolizedBoundaries = false
	// One plain lookup, reused for the build below (no second internal lookup).
	setP := lib.LookupFeatureRaw(objClass, geomCode, attrs, &mPlain)
	if setP == nil {
		return nil
	}
	buildP := buildFromSet(lib, &mPlain, f, setP, spatial)
	one := []FeatureBuildPass{{Build: buildP, Bnd: BndCommon, Pts: PtsCommon}}
	if gtype != s57.GeometryTypePolygon {
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
	buildS := buildFromSet(lib, &mSym, f, setS, spatial)
	return []FeatureBuildPass{
		{Build: buildP, Bnd: BndPlain, Pts: PtsCommon},
		{Build: buildS, Bnd: BndSymbolized, Pts: PtsCommon},
	}
}

// pointPasses builds a point feature under both the paper-chart and simplified
// point lookup tables (S-52 §11.2.2). Most point classes resolve identically in
// both (one pass, pts=2); buoys/beacons and the like differ — those get two
// passes (paper pts=0 / simplified pts=1) so the client's "simplified symbols"
// toggle swaps them live with no re-bake.
func pointPasses(lib *s52.Library, mariner *s52.MarinerSettings, f *s57.Feature, objClass, geomCode string, attrs map[string]interface{}, spatial *s52.SpatialContext) []FeatureBuildPass {
	mPaper := *mariner
	mPaper.SimplifiedPoints = false
	setPaper := lib.LookupFeatureRaw(objClass, geomCode, attrs, &mPaper)
	if setPaper == nil {
		return nil
	}
	buildPaper := buildFromSet(lib, &mPaper, f, setPaper, spatial)
	one := []FeatureBuildPass{{Build: buildPaper, Bnd: BndCommon, Pts: PtsCommon}}
	mSimp := *mariner
	mSimp.SimplifiedPoints = true
	setSimp := lib.LookupFeatureRaw(objClass, geomCode, attrs, &mSimp)
	if setSimp == nil || !instructionSetsDiffer(setPaper, setSimp) {
		return one
	}
	buildSimp := buildFromSet(lib, &mSimp, f, setSimp, spatial)
	return []FeatureBuildPass{
		{Build: buildPaper, Bnd: BndCommon, Pts: PtsPaper},
		{Build: buildSimp, Bnd: BndCommon, Pts: PtsSimplified},
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
	spatial  *s52.SpatialContext // underlying/adjacent objects for CSPs that need topology (nil if none)
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
			// A rotation sourced from an S-57 attribute (e.g. SY(RECTRC57,ORIENT)) is
			// referenced to TRUE NORTH and must turn with the chart; a literal angle or
			// no rotation stays upright to the screen (S-52 PresLib §9.2 ROT 1/2 vs 3).
			trueNorth := in.RotationAttr != ""
			if trueNorth {
				if v, ok := floatAttr(w.attrs, in.RotationAttr); ok {
					rot = v // e.g. SY(RECTRC57,ORIENT) → the feature's ORIENT bearing
				}
			}
			w.emitSymbol(in.SymbolID, float32(rot), trueNorth, g)
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
			ctx := s52.NewCSContext(w.csAttrs(g), goGeomType(w.geomCode), w.spatial, w.mariner)
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
		// S-52 §8.6.2: stroke the drawable line parts (masked / data-limit edges
		// removed) when the parser provided them; otherwise the full flat line.
		if g.lineParts != nil {
			for _, part := range g.lineParts {
				w.emitStrokeOne(colorToken, dash, widthPx, part)
			}
		} else {
			w.emitStrokeOne(colorToken, dash, widthPx, g.line)
		}
	case geomArea:
		// S-52 §8.6.2: stroke the drawable border (masked / data-limit edges
		// removed) when the parser provided it; otherwise fall back to the full
		// rings (cells whose topology didn't resolve to per-edge boundaries).
		borders := g.boundary
		if borders == nil {
			borders = g.area
		}
		for _, ring := range borders {
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
		// S-52 §8.6.2: pattern-stroke the drawable line parts (masked / data-limit
		// edges removed) when available; otherwise the full flat line.
		if g.lineParts != nil {
			for _, part := range g.lineParts {
				w.emitLinePatternOne(linestyleName, part)
			}
		} else {
			w.emitLinePatternOne(linestyleName, g.line)
		}
	case geomArea:
		// S-52 §8.6.2: stroke the drawable border (masked / data-limit edges
		// removed) when available; otherwise the full rings.
		borders := g.boundary
		if borders == nil {
			borders = g.area
		}
		for _, ring := range borders {
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

func (w *walker) emitSymbol(symbolName string, rotationDeg float32, rotationTrueNorth bool, g geom) {
	// A point feature symbolises at its point; an area/line feature carries its
	// SY() as a *centred* symbol at the centroid/midpoint (S-52 §8.3.1) — e.g.
	// CTNARE's "!" (CTNARE51) or ACHARE's anchor (ACHARE51). Use the same anchor
	// resolution as centred text so symbol and label sit together.
	anchor, ok := textAnchor(g)
	if !ok {
		return
	}
	isSounding := g.kind == geomPoint && isSoundingDigit(symbolName)
	var halo *SymbolHalo
	soundingDepth := nan32
	if isSounding {
		halo = &SymbolHalo{ColorToken: "CHWHT", ExtraWidthPx: 1.5}
		if g.hasDepth {
			soundingDepth = float32(g.currentDepth)
		}
	}
	w.out = append(w.out, SymbolCall{
		Anchor:            anchor,
		SymbolName:        symbolName,
		RotationDeg:       rotationDeg,
		RotationTrueNorth: rotationTrueNorth,
		Scale:             DefaultPxPerSymbolUnit,
		Halo:              halo,
		SoundingDepthM:    soundingDepth,
		DangerDepthM:      nan32,
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
		Group:      tx.Display,
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
		return FeatureBuild{DisplayCategory: s52.DisplayStandard}
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
		DisplayCategory: s52.DisplayStandard,
	}
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
		var sx, sy, n float64
		for _, c := range ring {
			if len(c) >= 2 {
				sx, sy, n = sx+c[0], sy+c[1], n+1
			}
		}
		if n > 0 {
			return geo.LatLon{Lat: sy / n, Lon: sx / n}, true
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
