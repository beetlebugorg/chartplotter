package grib

import (
	"math"
	"os"
	"testing"
)

// TestDecodeRealGFSComplex validates the complex-packing + spatial-differencing path
// (template 5.3) against a real GFS wind field: the 1° UGRD/VGRD at 10 m above
// ground, byte-range-fetched from the NOAA GFS open-data archive
// (gfs.20230927/00). If the decoder desyncs (e.g. missing the byte-alignment between
// group subsections), the second-order integration runs away and the value range
// explodes — so the plausibility bounds below are a real correctness check.
func TestDecodeRealGFSComplex(t *testing.T) {
	b, err := os.ReadFile("testdata/gfs_1deg_uv.grib2")
	if err != nil {
		t.Skipf("fixture missing: %v", err)
	}
	grids, err := Decode(b)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(grids) != 2 {
		t.Fatalf("want 2 messages (UGRD+VGRD), got %d", len(grids))
	}
	for _, g := range grids {
		if g.Nx != 360 || g.Ny != 181 {
			t.Fatalf("grid %dx%d, want 360x181 (1° global)", g.Nx, g.Ny)
		}
		if g.Category != 2 || (g.Number != 2 && g.Number != 3) {
			t.Fatalf("unexpected param %d/%d", g.Category, g.Number)
		}
		mn, mx := math.Inf(1), math.Inf(-1)
		for _, v := range g.Values {
			if math.IsNaN(v) || math.IsInf(v, 0) {
				t.Fatalf("param %d/%d produced NaN/Inf", g.Category, g.Number)
			}
			mn, mx = math.Min(mn, v), math.Max(mx, v)
		}
		// Surface wind: realistic magnitudes are well under 100 m/s. A desync blows
		// this to billions.
		if mn < -100 || mx > 100 {
			t.Fatalf("param %d/%d range [%.1f, %.1f] m/s is implausible (decode desync?)", g.Category, g.Number, mn, mx)
		}
		if mx-mn < 1 {
			t.Fatalf("param %d/%d looks constant [%.2f, %.2f] — decode likely wrong", g.Category, g.Number, mn, mx)
		}
	}
}
