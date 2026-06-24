package portrayal

import (
	"math"
	"testing"

	"github.com/beetlebugorg/chartplotter/pkg/geo"
	"github.com/beetlebugorg/chartplotter/pkg/s100/catalog"
	"github.com/beetlebugorg/chartplotter/pkg/s100/instructions"
)

// lowerStream parses+reduces an S-101 stream and lowers each draw command.
func lowerStream(t *testing.T, stream string, geom S101Geometry, cat *catalog.Catalog) []Primitive {
	t.Helper()
	cmds, unsup := instructions.Reduce(instructions.ParseStream(stream))
	if len(unsup) != 0 {
		t.Fatalf("unsupported kinds: %v", unsup)
	}
	var out []Primitive
	for _, c := range cmds {
		if p, ok := LowerS101(c, geom, cat); ok {
			out = append(out, p)
		}
	}
	return out
}

func TestLowerRapidsCurveToStrokeLine(t *testing.T) {
	geom := S101Geometry{Points: []geo.LatLon{{}, {}}}
	stream := "ViewingGroup:32050;DrawingPriority:9;DisplayPlane:UnderRadar;LineStyle:_simple_,,0.96,CHGRD;LineInstruction:_simple_"
	prims := lowerStream(t, stream, geom, nil)
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

func TestLowerRapidsSurfaceToFillPolygon(t *testing.T) {
	geom := S101Geometry{Rings: [][]geo.LatLon{{{}, {}, {}}}}
	prims := lowerStream(t, "ViewingGroup:32050;ColorFill:CHGRD", geom, nil)
	fp, ok := prims[0].(FillPolygon)
	if !ok || fp.ColorToken != "CHGRD" || len(fp.Rings) != 1 {
		t.Fatalf("want FillPolygon CHGRD, got %T %+v", prims[0], prims[0])
	}
}

func TestLowerRapidsPointNullSuppressed(t *testing.T) {
	prims := lowerStream(t, "ViewingGroup:32050;NullInstruction", S101Geometry{}, nil)
	if len(prims) != 0 {
		t.Fatalf("NullInstruction should lower to nothing, got %d", len(prims))
	}
}

func TestLowerPointSymbol(t *testing.T) {
	geom := S101Geometry{Anchor: geo.LatLon{}}
	stream := "ViewingGroup:25010;LocalOffset:1,-2;Rotation:45;PointInstruction:BCNCAR01"
	sc, ok := lowerStream(t, stream, geom, nil)[0].(SymbolCall)
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

func TestLowerComplexLineResolvesPenColor(t *testing.T) {
	cat := &catalog.Catalog{LineStyles: map[string]*catalog.LineStyle{
		"ACHARE51": {ID: "ACHARE51", PenColor: "CHMGD"},
	}}
	geom := S101Geometry{Points: []geo.LatLon{{}, {}}}
	lp, ok := lowerStream(t, "LineInstruction:ACHARE51", geom, cat)[0].(LinePattern)
	if !ok {
		t.Fatalf("want LinePattern")
	}
	if lp.LinestyleName != "ACHARE51" || lp.ColorToken != "CHMGD" {
		t.Errorf("line pattern wrong: %+v", lp)
	}
}

func TestLowerAreaFillReference(t *testing.T) {
	geom := S101Geometry{Rings: [][]geo.LatLon{{{}, {}}}}
	prims := lowerStream(t, "AreaFillReference:DRGARE01", geom, nil)
	pf, ok := prims[0].(PatternFill)
	if !ok || pf.PatternName != "DRGARE01" || len(pf.Rings) == 0 {
		t.Fatalf("want PatternFill DRGARE01 on rings, got %#v", prims)
	}
}

func TestLowerText(t *testing.T) {
	geom := S101Geometry{Anchor: geo.LatLon{}}
	stream := "ViewingGroup:25010;FontColor:CHBLK;TextAlignHorizontal:Center;TextInstruction:Fl.R.4s"
	dt, ok := lowerStream(t, stream, geom, nil)[0].(DrawText)
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
