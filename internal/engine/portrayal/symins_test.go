package portrayal

import (
	"testing"

	"github.com/beetlebugorg/chartplotter/pkg/s57"
)

// TestSYMINSPointSymbol: a NEWOBJ point with SYMINS="SY(INFORM01)" portrays the
// named symbol (the producer's instruction), NOT the V-AIS alias.
func TestSYMINSPointSymbol(t *testing.T) {
	f := s57.NewFeature(1, "NEWOBJ",
		s57.Geometry{Type: s57.GeometryTypePoint, Coordinates: [][]float64{{-5.1, 15.1}}},
		map[string]any{"SYMINS": "SY(INFORM01)"},
	)
	fb, ok := parseSYMINS(&f)
	if !ok {
		t.Fatal("parseSYMINS returned ok=false")
	}
	if len(fb.Primitives) != 1 {
		t.Fatalf("want 1 primitive, got %d", len(fb.Primitives))
	}
	sc, ok := fb.Primitives[0].(SymbolCall)
	if !ok || sc.SymbolName != "INFORM01" {
		t.Fatalf("want SymbolCall INFORM01, got %#v", fb.Primitives[0])
	}
}

// TestSYMINSTextLabel: a TX literal label is parsed into a DrawText with the text,
// colour and text group (display field) from the instruction.
func TestSYMINSTextLabel(t *testing.T) {
	f := s57.NewFeature(2, "NEWOBJ",
		s57.Geometry{Type: s57.GeometryTypePoint, Coordinates: [][]float64{{-5.1, 15.1}}},
		map[string]any{"SYMINS": "TX('Information about',3,2,2,'14108',0,0,CHBLK,11)"},
	)
	fb, ok := parseSYMINS(&f)
	if !ok {
		t.Fatal("ok=false")
	}
	dt, ok := fb.Primitives[0].(DrawText)
	if !ok {
		t.Fatalf("want DrawText, got %#v", fb.Primitives[0])
	}
	if dt.Text != "Information about" {
		t.Errorf("text = %q, want %q", dt.Text, "Information about")
	}
	if dt.ColorToken != "CHBLK" {
		t.Errorf("colour = %q, want CHBLK", dt.ColorToken)
	}
	if dt.Group != 11 {
		t.Errorf("text group = %d, want 11", dt.Group)
	}
	if dt.HAlign != HAlignLeft { // HJUST 3 = left
		t.Errorf("HAlign = %v, want left", dt.HAlign)
	}
}

// TestSYMINSAreaBoundaryAndFill: an area NEWOBJ with a dashed boundary + colour
// fill emits a StrokeLine per ring and a FillPolygon.
func TestSYMINSAreaBoundaryAndFill(t *testing.T) {
	ring := [][]float64{{-5.1, 15.1}, {-5.0, 15.1}, {-5.0, 15.2}, {-5.1, 15.2}, {-5.1, 15.1}}
	f := s57.NewFeature(3, "NEWOBJ",
		s57.Geometry{Type: s57.GeometryTypePolygon, Coordinates: ring},
		map[string]any{"SYMINS": "AC(CHMGF);LS(DASH,2,CHMGD)"},
	)
	fb, ok := parseSYMINS(&f)
	if !ok {
		t.Fatal("ok=false")
	}
	var hasFill, hasDashedStroke bool
	for _, p := range fb.Primitives {
		switch v := p.(type) {
		case FillPolygon:
			if v.ColorToken == "CHMGF" {
				hasFill = true
			}
		case StrokeLine:
			if v.ColorToken == "CHMGD" && v.Dash == DashDashed {
				hasDashedStroke = true
			}
		}
	}
	if !hasFill || !hasDashedStroke {
		t.Fatalf("want CHMGF fill + dashed CHMGD stroke, got %#v", fb.Primitives)
	}
}

// TestSYMINSEmptyFallsThrough: no SYMINS ⇒ ok=false so the caller uses the default
// new-object symbology.
func TestSYMINSEmptyFallsThrough(t *testing.T) {
	f := s57.NewFeature(4, "NEWOBJ",
		s57.Geometry{Type: s57.GeometryTypePoint, Coordinates: [][]float64{{-5.1, 15.1}}},
		map[string]any{},
	)
	if _, ok := parseSYMINS(&f); ok {
		t.Fatal("want ok=false for a feature with no SYMINS")
	}
}
