package grib

import (
	"math"
	"testing"
	"time"
)

func TestEncodeDecodeRoundTrip(t *testing.T) {
	nx, ny := 8, 5
	in := Grid{
		Nx: nx, Ny: ny,
		La1: 40, Lo1: -80, La2: 36, Lo2: -73,
		Dx: 1, Dy: 1,
		RefTime:  time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC),
		Category: 2, Number: 2, // UGRD
		ForecastHour: 0,
		Values:       make([]float64, nx*ny),
	}
	for i := range in.Values {
		in.Values[i] = -12.5 + 0.25*float64(i) // spans negative → positive, non-integer
	}

	msgs, err := Decode(Encode(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 {
		t.Fatalf("want 1 message, got %d", len(msgs))
	}
	g := msgs[0]
	if g.Nx != nx || g.Ny != ny {
		t.Fatalf("grid size %dx%d, want %dx%d", g.Nx, g.Ny, nx, ny)
	}
	if g.Category != 2 || g.Number != 2 {
		t.Fatalf("param %d/%d, want 2/2", g.Category, g.Number)
	}
	if math.Abs(g.La1-40) > 1e-4 || math.Abs(g.Lo1-(-80)) > 1e-4 {
		t.Fatalf("corner (%.4f,%.4f), want (40,-80)", g.La1, g.Lo1)
	}
	if !g.RefTime.Equal(in.RefTime) {
		t.Fatalf("refTime %v, want %v", g.RefTime, in.RefTime)
	}
	for i := range in.Values {
		if math.Abs(g.Values[i]-in.Values[i]) > 0.01 { // 0.01 packing precision
			t.Fatalf("value[%d]=%.4f, want %.4f", i, g.Values[i], in.Values[i])
		}
	}
}
