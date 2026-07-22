package main

import (
	"encoding/binary"
	"math"
	"os"
	"testing"

	"github.com/beetlebugorg/chartplotter/plugins/core.weather/grib"
)

// jsDoc mirrors what the frontend's parseWindBin builds from wind.bin.
type jsDoc struct {
	nx, ny            int
	lo1, la1, dx, dy  float64
	refUnix           float64
	hours             []int
	u, v              [][]float64
	gust, temp, cloud [][]float64 // nil per step when the plane is absent (all-NaN)
}

// parseWindBinJS re-implements ui/plugin.mjs parseWindBin byte-for-byte (little
// endian, Float32 header fields, f64 refUnix at 36, steps at 44; v3 carries five
// planes per step with all-NaN reading as absent; v4 has a per-step plane mask and
// ships only present planes).
func parseWindBinJS(b []byte) *jsDoc {
	if string(b[0:4]) != "WGRD" {
		return nil
	}
	le := binary.LittleEndian
	ver := int(le.Uint32(b[4:8]))
	if ver < 2 || ver > 4 {
		return nil
	}
	d := &jsDoc{
		nx:  int(le.Uint32(b[8:12])),
		ny:  int(le.Uint32(b[12:16])),
		lo1: float64(math.Float32frombits(le.Uint32(b[20:24]))),
		la1: float64(math.Float32frombits(le.Uint32(b[24:28]))),
		dx:  float64(math.Float32frombits(le.Uint32(b[28:32]))),
		dy:  float64(math.Float32frombits(le.Uint32(b[32:36]))),
	}
	nSteps := int(le.Uint32(b[16:20]))
	d.refUnix = math.Float64frombits(le.Uint64(b[36:44]))
	np := d.nx * d.ny
	o := 44
	plane := func() []float64 {
		a := make([]float64, np)
		for i := 0; i < np; i++ {
			a[i] = float64(math.Float32frombits(le.Uint32(b[o : o+4])))
			o += 4
		}
		if math.IsNaN(a[0]) && math.IsNaN(a[np-1]) { // all-NaN = absent
			return nil
		}
		return a
	}
	for s := 0; s < nSteps; s++ {
		d.hours = append(d.hours, int(int32(le.Uint32(b[o:o+4]))))
		o += 4
		mask := 0b11
		if ver >= 4 {
			mask = int(le.Uint32(b[o : o+4]))
			o += 4
		} else if ver == 3 {
			mask = 0b11111
		}
		rd := func(bit int) []float64 {
			if mask&bit == 0 {
				return nil
			}
			return plane()
		}
		d.u = append(d.u, rd(1))
		d.v = append(d.v, rd(2))
		d.gust = append(d.gust, rd(4))
		d.temp = append(d.temp, rd(8))
		d.cloud = append(d.cloud, rd(16))
	}
	return d
}

// sampleJS re-implements ui/plugin.mjs _sample (bilinear, la1 = north edge).
func (d *jsDoc) sampleJS(step int, lng, lat float64) (float64, float64, bool) {
	global := float64(d.nx)*d.dx >= 359
	fx := (lng - d.lo1) / d.dx
	if global {
		fx = math.Mod(math.Mod(fx, float64(d.nx))+float64(d.nx), float64(d.nx))
	}
	fy := (d.la1 - lat) / d.dy
	if fy < 0 || fy > float64(d.ny-1) {
		return 0, 0, false
	}
	if !global && (fx < 0 || fx > float64(d.nx-1)) {
		return 0, 0, false
	}
	x0, y0 := int(math.Floor(fx)), int(math.Floor(fy))
	x1 := x0 + 1
	if global {
		x1 = (x0 + 1) % d.nx
	} else if x1 > d.nx-1 {
		x1 = d.nx - 1
	}
	y1 := y0 + 1
	if y1 > d.ny-1 {
		y1 = d.ny - 1
	}
	tx, ty := fx-float64(x0), fy-float64(y0)
	at := func(a []float64, x, y int) float64 { return a[y*d.nx+x] }
	bil := func(a []float64) float64 {
		return at(a, x0, y0)*(1-tx)*(1-ty) + at(a, x1, y0)*tx*(1-ty) +
			at(a, x0, y1)*(1-tx)*ty + at(a, x1, y1)*tx*ty
	}
	return bil(d.u[step]), bil(d.v[step]), true
}

// sampleRaw bilinearly samples a decoded global grid directly (ground truth).
func sampleRaw(g *grib.Grid, lng, lat float64) float64 {
	lon := lng
	for lon < g.Lo1 {
		lon += 360
	}
	fx := (lon - g.Lo1) / g.Dx
	fy := (g.La1 - lat) / g.Dy
	x0, y0 := int(math.Floor(fx)), int(math.Floor(fy))
	x1, y1 := (x0+1)%g.Nx, y0+1
	if y1 > g.Ny-1 {
		y1 = g.Ny - 1
	}
	tx, ty := fx-float64(x0), fy-float64(y0)
	at := func(x, y int) float64 { return g.Values[y*g.Nx+x] }
	return at(x0, y0)*(1-tx)*(1-ty) + at(x1, y0)*tx*(1-ty) +
		at(x0, y1)*(1-tx)*ty + at(x1, y1)*tx*ty
}

// TestPipelineCropEncodeSample runs the real publish pipeline (decode → cropGrid →
// encodeWindBin) on a real GFS field, then reads the blob back exactly the way the
// frontend does and checks the sampled wind matches the raw decoded grid. Catches
// crop offsets, header/base mismatches, and byte-layout drift between Go and JS.
func TestPipelineCropEncodeSample(t *testing.T) {
	b, err := os.ReadFile("grib/testdata/gfs_1deg_uv.grib2")
	if err != nil {
		t.Skipf("fixture missing: %v", err)
	}
	grids, err := grib.Decode(b)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	var ug, vg *grib.Grid
	for i := range grids {
		g := &grids[i]
		if g.Category == 2 && g.Number == 2 {
			ug = g
		}
		if g.Category == 2 && g.Number == 3 {
			vg = g
		}
	}
	if ug == nil || vg == nil {
		t.Fatal("fixture missing UGRD/VGRD")
	}

	h, u, v := cropGrid(ug, vg)
	// A fake temp plane derived from U exercises the optional-field path; gust and
	// cloud stay nil and must read back as absent.
	temp := make([]float64, len(u))
	for i := range u {
		temp[i] = 15 + u[i]
	}
	doc := windDoc{RefTime: ug.RefTime.Format("2006-01-02T15:04:05Z"), Header: h,
		Steps: []step{{Hour: 0, U: u, V: v, Temp: temp}}}
	blob := encodeWindBin(&doc)

	d := parseWindBinJS(blob)
	if d == nil {
		t.Fatal("parseWindBinJS: bad magic/version")
	}
	if d.nx != h.Nx || d.ny != h.Ny {
		t.Fatalf("header mismatch: js %dx%d vs go %dx%d", d.nx, d.ny, h.Nx, h.Ny)
	}
	if d.gust[0] != nil || d.cloud[0] != nil {
		t.Fatal("absent gust/cloud planes must parse as nil")
	}
	if d.temp[0] == nil {
		t.Fatal("temp plane lost in round-trip")
	}
	if math.Abs(d.temp[0][100]-(15+u[100])) > 0.01 {
		t.Fatalf("temp round-trip: got %v want %v", d.temp[0][100], 15+u[100])
	}

	pts := []struct{ lng, lat float64 }{
		{-76.0, 37.0}, {-123.0, 37.5}, {-79.5, 25.5},
		{-69.0, 43.0}, {-87.0, 43.5}, {-60.0, 30.0}, {-129.9, 20.1}, {-60.1, 54.9},
	}
	for _, p := range pts {
		su, sv, ok := d.sampleJS(0, p.lng, p.lat)
		if !ok {
			t.Fatalf("sampleJS off-grid at %v,%v (grid lo1=%v la1=%v nx=%d ny=%d dx=%v dy=%v)",
				p.lng, p.lat, d.lo1, d.la1, d.nx, d.ny, d.dx, d.dy)
		}
		ru := sampleRaw(ug, p.lng, p.lat)
		rv := sampleRaw(vg, p.lng, p.lat)
		// Float32 quantisation + a Float32 grid origin can shift sampling slightly;
		// tolerate small differences, fail on anything structural.
		if math.Abs(su-ru) > 0.35 || math.Abs(sv-rv) > 0.35 {
			t.Errorf("wind mismatch at %v,%v: pipeline (%.2f,%.2f) vs raw (%.2f,%.2f)",
				p.lng, p.lat, su, sv, ru, rv)
		}
	}
}
