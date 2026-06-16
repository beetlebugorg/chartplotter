package tile

import (
	"math"
	"testing"

	"github.com/beetlebugorg/chartplotter-go/pkg/geo"
)

func TestProjectorInsideTileMonotonic(t *testing.T) {
	pt := geo.LatLon{Lat: 38.978, Lon: -76.49}
	rng := RangeForBbox(14, geo.BoundingBox{MinLat: pt.Lat, MinLon: pt.Lon, MaxLat: pt.Lat, MaxLon: pt.Lon}, 4096)
	proj := NewProjector(TileCoord{Z: 14, X: rng.XMin, Y: rng.YMin}, 4096)
	in := proj.Project(pt)
	if in.X < 0 || in.X > 4096 || in.Y < 0 || in.Y > 4096 {
		t.Fatalf("point not inside its tile: %+v", in)
	}
	east := proj.Project(geo.LatLon{Lat: 38.978, Lon: -76.40})
	north := proj.Project(geo.LatLon{Lat: 39.05, Lon: -76.49})
	if east.X <= in.X {
		t.Error("increasing lon should increase x")
	}
	if north.Y >= in.Y {
		t.Error("increasing lat should decrease y (north up)")
	}
}

func TestRangeForBbox(t *testing.T) {
	bbox := geo.BoundingBox{MinLat: 38.9, MinLon: -76.55, MaxLat: 39.05, MaxLon: -76.40}
	rng := RangeForBbox(14, bbox, 4096)
	if rng.Count() < 1 {
		t.Fatal("expected >= 1 tile")
	}
	if rng.XMin > rng.XMax || rng.YMin > rng.YMax {
		t.Fatal("range inverted")
	}
	if rng.XMax >= (uint32(1) << 14) {
		t.Fatal("x out of band")
	}
}

func TestClipPolygonStraddlingEdge(t *testing.T) {
	r := Rect{MinX: 0, MinY: 0, MaxX: 100, MaxY: 100}
	ring := []FPoint{{50, 50}, {150, 50}, {150, 80}, {50, 80}}
	out := ClipPolygon(ring, r)
	if len(out) < 4 {
		t.Fatalf("expected >=4 vertices, got %d", len(out))
	}
	for _, p := range out {
		if p.X > 100+1e-6 {
			t.Errorf("vertex past clip edge: %+v", p)
		}
	}
}

func TestClipPolygonAllFourEdges(t *testing.T) {
	r := Rect{MinX: 0, MinY: 0, MaxX: 100, MaxY: 100}
	ring := []FPoint{{-10, -10}, {110, -10}, {110, 110}, {-10, 110}}
	out := ClipPolygon(ring, r)
	if len(out) < 4 {
		t.Fatalf("expected >=4 vertices, got %d", len(out))
	}
	for _, p := range out {
		if p.X < -1e-6 || p.X > 100+1e-6 || p.Y < -1e-6 || p.Y > 100+1e-6 {
			t.Errorf("vertex outside rect: %+v", p)
		}
	}
}

func TestClipperReuse(t *testing.T) {
	// Reusing one Clipper across rings must not leak the prior ring's data.
	r := Rect{MinX: 0, MinY: 0, MaxX: 100, MaxY: 100}
	var c Clipper
	big := []FPoint{{-10, -10}, {110, -10}, {110, 110}, {-10, 110}}
	_ = c.Polygon(big, r)
	insideRing := []FPoint{{10, 10}, {40, 10}, {40, 40}, {10, 40}}
	got := c.Polygon(insideRing, r)
	if len(got) != 4 {
		t.Fatalf("expected 4 vertices, got %d", len(got))
	}
	for i, g := range got {
		if g != insideRing[i] {
			t.Errorf("vertex %d = %+v, want %+v", i, g, insideRing[i])
		}
	}
}

func TestClipLineSplits(t *testing.T) {
	r := Rect{MinX: 0, MinY: 0, MaxX: 100, MaxY: 100}
	pts := []FPoint{{10, 10}, {200, 10}, {200, 50}, {10, 50}} // in -> out -> in
	runs := ClipLine(pts, r)
	if len(runs) != 2 {
		t.Fatalf("expected 2 runs, got %d", len(runs))
	}
	for _, run := range runs {
		for _, p := range run {
			if p.X > 100+1e-6 {
				t.Errorf("vertex past edge: %+v", p)
			}
		}
	}
}

func TestClipLineFullyInside(t *testing.T) {
	r := Rect{MinX: 0, MinY: 0, MaxX: 100, MaxY: 100}
	pts := []FPoint{{10, 10}, {20, 20}, {30, 40}}
	runs := ClipLine(pts, r)
	if len(runs) != 1 || len(runs[0]) != 3 {
		t.Fatalf("expected one 3-vertex run, got %d runs", len(runs))
	}
}

func TestClipLinePhasedArc0(t *testing.T) {
	r := Rect{MinX: 0, MinY: 0, MaxX: 100, MaxY: 100}
	pts := []FPoint{{-50, 50}, {150, 50}}
	arc := []float64{0, 200}
	runs := ClipLinePhased(pts, arc, r)
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}
	if math.Abs(runs[0].Arc0-50) > 1e-9 {
		t.Errorf("arc0 = %v, want 50", runs[0].Arc0)
	}
	if math.Abs(runs[0].Points[0].X-0) > 1e-9 {
		t.Errorf("run should start at left edge, got x=%v", runs[0].Points[0].X)
	}
}

func TestClipLinePhasedSeamPhase(t *testing.T) {
	r := Rect{MinX: 0, MinY: 0, MaxX: 100, MaxY: 100}
	pts := []FPoint{{10, 10}, {200, 10}, {200, 50}, {10, 50}}
	arc := []float64{0, 190, 230, 420}
	runs := ClipLinePhased(pts, arc, r)
	if len(runs) != 2 {
		t.Fatalf("expected 2 runs, got %d", len(runs))
	}
	if math.Abs(runs[0].Arc0-0) > 1e-9 {
		t.Errorf("run0 arc0 = %v, want 0", runs[0].Arc0)
	}
	if math.Abs(runs[1].Arc0-330) > 1e-6 {
		t.Errorf("run1 arc0 = %v, want 330", runs[1].Arc0)
	}
}
