// Command gen synthesizes a plausible surface-wind field and writes it as a real
// two-message (UGRD + VGRD) GRIB2 file, used as the weather plugin's embedded offline
// sample. Run from the plugin dir: `go run ./gen > sample.grib2` (or with -o).
//
// The field is a background westerly plus a cyclonic (counter-clockwise) vortex off
// the mid-Atlantic coast, so the animated streamlines curve around a "low" — a
// recognisable weather picture over the demo area (Chesapeake).
package main

import (
	"flag"
	"math"
	"os"
	"time"

	"github.com/beetlebugorg/chartplotter/plugins/core.weather/grib"
)

func main() {
	out := flag.String("o", "sample.grib2", "output GRIB2 path")
	flag.Parse()

	// Region: mid-Atlantic / US East Coast, 0.5°.
	la1, lo1 := 44.0, -84.0 // NW corner (first point)
	la2, lo2 := 30.0, -64.0 // SE corner
	dx, dy := 0.5, 0.5
	nx := int(math.Round((lo2-lo1)/dx)) + 1
	ny := int(math.Round((la1-la2)/dy)) + 1

	ref := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	base := grib.Grid{Nx: nx, Ny: ny, La1: la1, Lo1: lo1, La2: la2, Lo2: lo2, Dx: dx, Dy: dy, RefTime: ref}

	// A forecast time series: the cyclonic low drifts north-east and intensifies,
	// so scrubbing the time slider shows the storm track evolve.
	hours := []int{0, 6, 12, 18, 24, 36, 48}
	var data []byte
	for _, hr := range hours {
		f := float64(hr) / 48.0
		clat := 37.5 + 3.0*f  // drifts north
		clon := -73.0 + 4.0*f // drifts east
		peak := 18.0 + 12.0*f // intensifies
		u := make([]float64, nx*ny)
		v := make([]float64, nx*ny)
		for y := 0; y < ny; y++ {
			lat := la1 - float64(y)*dy
			for x := 0; x < nx; x++ {
				lon := lo1 + float64(x)*dx
				i := y*nx + x
				bu, bv := 8.0, 1.5 // background westerly
				ddx := (lon - clon) * math.Cos(lat*math.Pi/180)
				ddy := lat - clat
				r := math.Hypot(ddx, ddy)
				strength := peak * math.Exp(-r*r/18) // Gaussian falloff, m/s
				if r > 1e-6 {
					u[i] = bu + strength*(-ddy/r) // counter-clockwise tangential
					v[i] = bv + strength*(ddx/r)
				} else {
					u[i], v[i] = bu, bv
				}
			}
		}
		ug := base
		ug.Category, ug.Number, ug.ForecastHour, ug.Values = 2, 2, hr, u // UGRD
		vg := base
		vg.Category, vg.Number, vg.ForecastHour, vg.Values = 2, 3, hr, v // VGRD
		data = append(data, grib.Encode(ug)...)
		data = append(data, grib.Encode(vg)...)
	}
	if err := os.WriteFile(*out, data, 0o644); err != nil {
		panic(err)
	}
}
