package portrayal

import (
	"math"
	"testing"

	"github.com/beetlebugorg/chartplotter/pkg/geo"
	"github.com/beetlebugorg/chartplotter/pkg/s100/catalog"
	"github.com/beetlebugorg/chartplotter/pkg/s100/instructions"
)

// emitStream parses+reduces an S-101 stream and emits primitives for each draw command.
func emitStream(t *testing.T, stream string, geom S101Geometry, cat *catalog.Catalog) []Primitive {
	t.Helper()
	cmds, unsup := instructions.Reduce(instructions.ParseStream(stream))
	if len(unsup) != 0 {
		t.Fatalf("unsupported kinds: %v", unsup)
	}
	var out []Primitive
	for _, c := range cmds {
		out = append(out, emitPrimitives(c, geom, cat)...)
	}
	return out
}

func TestEmitRapidsCurveToStrokeLine(t *testing.T) {
	geom := S101Geometry{Lines: [][]geo.LatLon{{{}, {}}}}
	stream := "ViewingGroup:32050;DrawingPriority:9;DisplayPlane:UnderRadar;LineStyle:_simple_,,0.96,CHGRD;LineInstruction:_simple_"
	prims := emitStream(t, stream, geom, nil)
	if len(prims) != 1 {
		t.Fatalf("want 1 primitive, got %d", len(prims))
	}
	sl, ok := prims[0].(StrokeLine)
	if !ok {
		t.Fatalf("want StrokeLine, got %T", prims[0])
	}
	if sl.ColorToken != "CHGRD" || sl.Dash != DashSolid || len(sl.Points) != 2 {
		t.Errorf("stroke wrong: %+v", sl)
	}
	if want := float32(0.96 * pxPerMM); !approx(sl.WidthPx, want) {
		t.Errorf("WidthPx = %v, want %v", sl.WidthPx, want)
	}
}

func TestEmitRapidsSurfaceToFillPolygon(t *testing.T) {
	geom := S101Geometry{Rings: [][]geo.LatLon{{{}, {}, {}}}}
	prims := emitStream(t, "ViewingGroup:32050;ColorFill:CHGRD", geom, nil)
	fp, ok := prims[0].(FillPolygon)
	if !ok || fp.ColorToken != "CHGRD" || len(fp.Rings) != 1 {
		t.Fatalf("want FillPolygon CHGRD, got %T %+v", prims[0], prims[0])
	}
}

func TestEmitRapidsPointNullSuppressed(t *testing.T) {
	prims := emitStream(t, "ViewingGroup:32050;NullInstruction", S101Geometry{}, nil)
	if len(prims) != 0 {
		t.Fatalf("NullInstruction should emit nothing, got %d", len(prims))
	}
}

func TestEmitPointSymbol(t *testing.T) {
	geom := S101Geometry{Anchor: geo.LatLon{}}
	stream := "ViewingGroup:25010;LocalOffset:1,-2;Rotation:45;PointInstruction:BCNCAR01"
	sc, ok := emitStream(t, stream, geom, nil)[0].(SymbolCall)
	if !ok {
		t.Fatalf("want SymbolCall")
	}
	if sc.SymbolName != "BCNCAR01" || sc.RotationDeg != 45 {
		t.Errorf("symbol wrong: %+v", sc)
	}
	if sc.OffsetXUnits != float32(1*unitsPerMM) || sc.OffsetYUnits != float32(-2*unitsPerMM) {
		t.Errorf("offset wrong: %v,%v", sc.OffsetXUnits, sc.OffsetYUnits)
	}
	if !math.IsNaN(float64(sc.SoundingDepthM)) {
		t.Errorf("ordinary symbol should have NaN sounding depth")
	}
}

func TestEmitComplexLineResolvesPenColor(t *testing.T) {
	cat := &catalog.Catalog{LineStyles: map[string]*catalog.LineStyle{
		"ACHARE51": {ID: "ACHARE51", PenColor: "CHMGD"},
	}}
	geom := S101Geometry{Lines: [][]geo.LatLon{{{}, {}}}}
	lp, ok := emitStream(t, "LineInstruction:ACHARE51", geom, cat)[0].(LinePattern)
	if !ok {
		t.Fatalf("want LinePattern")
	}
	if lp.LinestyleName != "ACHARE51" || lp.ColorToken != "CHMGD" {
		t.Errorf("line pattern wrong: %+v", lp)
	}
}

func TestEmitAreaFillReference(t *testing.T) {
	geom := S101Geometry{Rings: [][]geo.LatLon{{{}, {}}}}
	prims := emitStream(t, "AreaFillReference:DRGARE01", geom, nil)
	pf, ok := prims[0].(PatternFill)
	if !ok || pf.PatternName != "DRGARE01" || len(pf.Rings) == 0 {
		t.Fatalf("want PatternFill DRGARE01 on rings, got %#v", prims)
	}
}

// TestEmitAreaBoundaryLine: a boundary line strokes EACH drawable run, not
// empty geometry. The regression: an area feature has no Lines unless the
// builder fills them from its (masked) boundary; emitting onto empty geometry
// yielded a NaN/Inf bbox the baker dropped ("skipping prim with implausible
// bbox"). Here two drawable runs ⇒ two LinePatterns.
func TestEmitAreaBoundaryLine(t *testing.T) {
	run1 := []geo.LatLon{{Lat: 0, Lon: 0}, {Lat: 0, Lon: 1}, {Lat: 1, Lon: 1}}
	run2 := []geo.LatLon{{Lat: 0.2, Lon: 0.2}, {Lat: 0.2, Lon: 0.4}, {Lat: 0.4, Lon: 0.4}}
	geom := S101Geometry{Lines: [][]geo.LatLon{run1, run2}}
	prims := emitStream(t, "LineInstruction:CTNARE51", geom, nil)
	if len(prims) != 2 {
		t.Fatalf("want one line per run (2), got %d: %#v", len(prims), prims)
	}
	for i, p := range prims {
		lp, ok := p.(LinePattern)
		if !ok {
			t.Fatalf("run %d: want LinePattern, got %T", i, p)
		}
		if lp.LinestyleName != "CTNARE51" || len(lp.Points) < 2 {
			t.Errorf("run %d emitted onto empty/wrong geometry: %+v", i, lp)
		}
	}
}

// TestEmitLineNoGeometry: a line draw with no drawable runs emits nothing
// rather than a degenerate primitive.
func TestEmitLineNoGeometry(t *testing.T) {
	if prims := emitStream(t, "LineInstruction:CTNARE51", S101Geometry{}, nil); len(prims) != 0 {
		t.Fatalf("want no primitives for empty geometry, got %d", len(prims))
	}
}

// TestStrokeRunsForMasking: the builder strokes the MASKED boundary/parts when
// the parser computed them (coastline-coincident edges removed), and falls back
// to the full geometry only when masking wasn't computed. Area fills keep the
// whole rings either way.
func TestStrokeRunsForMasking(t *testing.T) {
	ring := []geo.LatLon{{Lat: 0, Lon: 0}, {Lat: 0, Lon: 1}, {Lat: 1, Lon: 1}, {Lat: 0, Lon: 0}}
	masked := []geo.LatLon{{Lat: 0, Lon: 0}, {Lat: 0, Lon: 1}} // only the seaward edge

	// boundary computed (non-nil) → use it verbatim, NOT the full ring.
	area := geom{kind: geomArea, area: [][]geo.LatLon{ring}, boundary: [][]geo.LatLon{masked}}
	if runs := strokeRunsFor(area); len(runs) != 1 || len(runs[0]) != 2 {
		t.Errorf("masked area boundary = %#v, want the single 2-pt seaward run", runs)
	}

	// boundary NOT computed (nil) → fall back to the full rings.
	areaNoMask := geom{kind: geomArea, area: [][]geo.LatLon{ring}}
	if runs := strokeRunsFor(areaNoMask); len(runs) != 1 || len(runs[0]) != len(ring) {
		t.Errorf("unmasked area boundary = %#v, want the full ring", runs)
	}

	// empty (non-nil) boundary → stroke nothing (fully coastline-coincident).
	areaAllMasked := geom{kind: geomArea, area: [][]geo.LatLon{ring}, boundary: [][]geo.LatLon{}}
	if runs := strokeRunsFor(areaAllMasked); len(runs) != 0 {
		t.Errorf("fully-masked area boundary = %#v, want no runs", runs)
	}

	// a line feature uses its masked parts when present.
	line := geom{kind: geomLine, line: ring, lineParts: [][]geo.LatLon{masked}}
	if runs := strokeRunsFor(line); len(runs) != 1 || len(runs[0]) != 2 {
		t.Errorf("masked line parts = %#v, want the single 2-pt part", runs)
	}
}

func TestEmitText(t *testing.T) {
	geom := S101Geometry{Anchor: geo.LatLon{}}
	stream := "ViewingGroup:25010;FontColor:CHBLK;TextAlignHorizontal:Center;TextInstruction:Fl.R.4s"
	dt, ok := emitStream(t, stream, geom, nil)[0].(DrawText)
	if !ok {
		t.Fatalf("want DrawText")
	}
	if dt.Text != "Fl.R.4s" || dt.ColorToken != "CHBLK" || dt.HAlign != HAlignCenter {
		t.Errorf("text wrong: %+v", dt)
	}
}

func approx(a, b float32) bool {
	d := a - b
	return d < 0.001 && d > -0.001
}
