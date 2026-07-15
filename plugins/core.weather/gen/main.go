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

	u := make([]float64, nx*ny)
	v := make([]float64, nx*ny)
	// Cyclonic low centred offshore; strength falls off with distance.
	clat, clon := 37.5, -73.0
	for y := 0; y < ny; y++ {
		lat := la1 - float64(y)*dy
		for x := 0; x < nx; x++ {
			lon := lo1 + float64(x)*dx
			i := y*nx + x
			// Background westerly.
			bu, bv := 8.0, 1.5
			// Vortex: tangential flow around (clat,clon), counter-clockwise.
			ddx := (lon - clon) * math.Cos(lat*math.Pi/180) // scale lon by latitude
			ddy := lat - clat
			r := math.Hypot(ddx, ddy)
			strength := 22 * math.Exp(-r*r/18) // m/s, Gaussian falloff
			if r > 1e-6 {
				// Perpendicular (counter-clockwise): (-ddy, ddx)/r.
				u[i] = bu + strength*(-ddy/r)
				v[i] = bv + strength*(ddx/r)
			} else {
				u[i], v[i] = bu, bv
			}
		}
	}

	ref := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	base := grib.Grid{Nx: nx, Ny: ny, La1: la1, Lo1: lo1, La2: la2, Lo2: lo2, Dx: dx, Dy: dy, RefTime: ref}
	ug := base
	ug.Category, ug.Number, ug.Values = 2, 2, u // UGRD (2/2)
	vg := base
	vg.Category, vg.Number, vg.Values = 2, 3, v // VGRD (2/3)

	data := append(grib.Encode(ug), grib.Encode(vg)...)
	if err := os.WriteFile(*out, data, 0o644); err != nil {
		panic(err)
	}
}
