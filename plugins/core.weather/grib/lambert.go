package grib

import "math"

// earthRadius is the NCEP spherical earth (GRIB2 shape-of-earth 6) used by the
// Lambert-conformal products this package targets (HRRR, NAM).
const earthRadius = 6371229.0

// lambertProj returns a forward projection lat/lon (degrees) → fractional grid
// index (i, j) for a template-3.30 grid, honouring the grid's scan direction.
func lambertProj(g *Grid) func(lat, lon float64) (float64, float64) {
	rad := math.Pi / 180
	phi1, phi2 := g.Latin1*rad, g.Latin2*rad
	var n float64
	if math.Abs(phi1-phi2) < 1e-9 {
		n = math.Sin(phi1)
	} else {
		n = math.Log(math.Cos(phi1)/math.Cos(phi2)) /
			math.Log(math.Tan(math.Pi/4+phi2/2)/math.Tan(math.Pi/4+phi1/2))
	}
	f := math.Cos(phi1) * math.Pow(math.Tan(math.Pi/4+phi1/2), n) / n
	rho := func(latDeg float64) float64 {
		return earthRadius * f / math.Pow(math.Tan(math.Pi/4+latDeg*rad/2), n)
	}
	theta := func(lonDeg float64) float64 {
		d := math.Mod(lonDeg-g.LoV, 360)
		if d > 180 {
			d -= 360
		}
		if d < -180 {
			d += 360
		}
		return n * d * rad
	}
	xy := func(lat, lon float64) (float64, float64) {
		r, t := rho(lat), theta(lon)
		return r * math.Sin(t), -r * math.Cos(t)
	}
	x1, y1 := xy(g.La1, g.Lo1) // the grid's first point anchors the index origin
	return func(lat, lon float64) (float64, float64) {
		x, y := xy(lat, lon)
		i := (x - x1) / g.Dx
		j := (y - y1) / g.Dy // +y is north; ScanYUp grids scan the same way
		if !g.ScanYUp {
			j = -j
		}
		return i, j
	}
}

// windAngle is the local rotation from grid axes to earth east/north at lon:
// earthU = u·cos(a) + v·sin(a); earthV = −u·sin(a) + v·cos(a).
func windAngle(g *Grid, lon float64) float64 {
	rad := math.Pi / 180
	phi1, phi2 := g.Latin1*rad, g.Latin2*rad
	var n float64
	if math.Abs(phi1-phi2) < 1e-9 {
		n = math.Sin(phi1)
	} else {
		n = math.Log(math.Cos(phi1)/math.Cos(phi2)) /
			math.Log(math.Tan(math.Pi/4+phi2/2)/math.Tan(math.Pi/4+phi1/2))
	}
	d := math.Mod(lon-g.LoV, 360)
	if d > 180 {
		d -= 360
	}
	if d < -180 {
		d += 360
	}
	return n * d * rad
}

// ResampleLambert resamples fields sharing one Lambert grid onto a regular
// lat/lon window (rows north→south from latMax, step degrees), bilinear in grid
// space; points outside the grid come back NaN. When uIdx/vIdx are both ≥ 0 those
// planes are a wind pair and — if the grid flags winds as grid-relative — are
// rotated to earth east/north during resampling (skipping this skews directions by
// >10° at the edges of a CONUS Lambert domain).
func ResampleLambert(gs []*Grid, latMax, lonMin float64, nx, ny int, step float64, uIdx, vIdx int) [][]float64 {
	if len(gs) == 0 {
		return nil
	}
	g0 := gs[0]
	proj := lambertProj(g0)
	out := make([][]float64, len(gs))
	for k := range out {
		out[k] = make([]float64, nx*ny)
	}
	rotate := uIdx >= 0 && vIdx >= 0 && g0.WindsGridRelative
	bil := func(vals []float64, i, j float64) float64 {
		x0, y0 := int(math.Floor(i)), int(math.Floor(j))
		if x0 < 0 || y0 < 0 || x0 >= g0.Nx-1 || y0 >= g0.Ny-1 {
			return math.NaN()
		}
		tx, ty := i-float64(x0), j-float64(y0)
		at := func(x, y int) float64 { return vals[y*g0.Nx+x] }
		return at(x0, y0)*(1-tx)*(1-ty) + at(x0+1, y0)*tx*(1-ty) +
			at(x0, y0+1)*(1-tx)*ty + at(x0+1, y0+1)*tx*ty
	}
	for r := 0; r < ny; r++ {
		lat := latMax - float64(r)*step
		for c := 0; c < nx; c++ {
			lon := lonMin + float64(c)*step
			i, j := proj(lat, lon)
			p := r*nx + c
			for k, g := range gs {
				out[k][p] = bil(g.Values, i, j)
			}
			if rotate && !math.IsNaN(out[uIdx][p]) && !math.IsNaN(out[vIdx][p]) {
				a := windAngle(g0, lon)
				u, v := out[uIdx][p], out[vIdx][p]
				out[uIdx][p] = u*math.Cos(a) + v*math.Sin(a)
				out[vIdx][p] = -u*math.Sin(a) + v*math.Cos(a)
			}
		}
	}
	return out
}
