// Command core.weather is a GRIB weather plugin (Tier A, WASM). It decodes a GRIB2
// surface-wind field into a compact binary grid and publishes it as a served artifact
// at GET /plugins/core.weather/serve/wind.bin — the "grid, not tiles" model: the
// plugin is never in the render path, and the frontend animates the grid as wind
// particles entirely client-side.
//
// Data source (config "source"):
//   - "gfs" (default): the latest real GFS forecast, auto-discovered from the NOAA
//     open-data archive and byte-range-fetched (10 m wind only) via net.http.
//   - "sample": an embedded offline GRIB2 sample (simple packing), for demos/offline.
//   - a GFS product URL or any GRIB2 URL: fetched and decoded.
//
// Build (Tier A): GOOS=wasip1 GOARCH=wasm go build -o plugin.wasm ./plugins/core.weather
package main

import (
	_ "embed"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/beetlebugorg/chartplotter/plugins/core.weather/grib"
	"github.com/beetlebugorg/chartplotter/sdk"
)

//go:embed sample.grib2
var sampleGRIB []byte

type weather struct{ h *sdk.Host }

// gfsBucket is the NOAA GFS open-data archive (S3).
const gfsBucket = "https://noaa-gfs-bdp-pds.s3.amazonaws.com"

func (p *weather) Start(h *sdk.Host) {
	p.h = h
	src := h.ConfigString("source")
	if src == "" {
		src = "gfs" // default: the latest real GFS forecast
	}
	switch {
	case src == "sample":
		p.publish(sampleGRIB, "embedded sample")
	case src == "gfs":
		p.discoverGFS() // auto-resolve the latest cycle → fetch
	case strings.Contains(src, "pgrb2"): // an explicit GFS product URL
		h.Status("running", "fetching GFS…")
		p.fetchGFS(src, "GFS", func() { h.Status("degraded", "GFS product not available") })
	default: // any other GRIB2 URL
		h.Status("running", "fetching "+src)
		h.Fetch(src, func(resp *sdk.HTTPResponse, err error) { p.onFetch(resp, err, src) })
	}
}

// discoverGFS fetches the latest available GFS cycle. GFS runs at 00/06/12/18Z and a
// cycle is published a few hours after its nominal time, so we start ~5h back, floor
// to a 6h boundary, and walk older until a cycle's .idx is available. Uses the wall
// clock (available to the module) — no bucket listing, which is paginated oldest-first.
func (p *weather) discoverGFS() {
	p.h.Status("running", "finding latest GFS cycle…")
	c := time.Now().UTC().Add(-5 * time.Hour).Truncate(6 * time.Hour) // newest likely-published cycle
	p.tryCycle(c, 0)
}

// tryCycle attempts the cycle `back` steps before c, falling to the previous one if it
// isn't up yet, up to ~2 days back.
func (p *weather) tryCycle(c time.Time, back int) {
	if back > 8 {
		p.h.Status("degraded", "no recent GFS cycle available")
		return
	}
	t := c.Add(time.Duration(-back*6) * time.Hour)
	date, hh := t.Format("20060102"), t.Format("15")
	// 0.5° (not 0.25°): ~4× fewer points → far faster to decode + transfer, and plenty
	// for streamlines. Users wanting finer can set an explicit 0p25 URL.
	url := fmt.Sprintf("%s/gfs.%s/%s/atmos/gfs.t%sz.pgrb2.0p50.f000", gfsBucket, date, hh, hh)
	p.fetchGFS(url, "GFS "+date+" "+hh+"z", func() { p.tryCycle(c, back+1) })
}

// fetchGFS byte-ranges only the 10 m UGRD/VGRD messages out of a GFS product, using
// its wgrib2 .idx to find their offsets — so a plugin never downloads the whole
// multi-hundred-MB file. onMiss is called if this product isn't available (the caller
// may try an older cycle); on success it publishes with label.
func (p *weather) fetchGFS(url, label string, onMiss func()) {
	p.h.Fetch(url+".idx", func(resp *sdk.HTTPResponse, err error) {
		if err != nil || resp == nil || resp.Status != 200 {
			onMiss()
			return
		}
		start, end, ok := windRange(string(resp.Body))
		if !ok {
			onMiss()
			return
		}
		rng := fmt.Sprintf("bytes=%d-%d", start, end)
		p.h.Status("running", "fetching "+label+"…")
		p.h.FetchOpts(url, map[string]string{"Range": rng}, func(r *sdk.HTTPResponse, e error) {
			if e != nil || r == nil || (r.Status != 200 && r.Status != 206) {
				onMiss()
				return
			}
			p.publish(r.Body, label)
		})
	})
}

func (p *weather) onFetch(resp *sdk.HTTPResponse, err error, label string) {
	if err != nil || resp == nil || (resp.Status != 200 && resp.Status != 206) {
		d := "fetch failed"
		if err != nil {
			d += ": " + err.Error()
		} else if resp != nil {
			d += " (HTTP " + itoa(resp.Status) + ")"
		}
		p.h.Status("degraded", d)
		return
	}
	p.publish(resp.Body, label)
}

// windRange parses a wgrib2 .idx and returns the byte range [start,end] spanning the
// "10 m above ground" UGRD and VGRD records (their end is the next record's offset).
func windRange(idx string) (start, end int, ok bool) {
	type rec struct {
		off          int
		field, level string
	}
	var recs []rec
	for _, ln := range strings.Split(idx, "\n") {
		f := strings.Split(ln, ":")
		if len(f) < 5 {
			continue
		}
		off, e := strconv.Atoi(f[1])
		if e != nil {
			continue
		}
		recs = append(recs, rec{off, f[3], f[4]})
	}
	uStart, vStart, vNext := -1, -1, -1
	for i, r := range recs {
		if r.level != "10 m above ground" {
			continue
		}
		if r.field == "UGRD" {
			uStart = r.off
		}
		if r.field == "VGRD" {
			vStart = r.off
			if i+1 < len(recs) {
				vNext = recs[i+1].off
			}
		}
	}
	if uStart < 0 || vStart < 0 {
		return 0, 0, false
	}
	start = uStart
	if vStart < start {
		start = vStart
	}
	end = vNext - 1
	if vNext <= 0 { // VGRD is the last record: read a generous window
		end = start + (1 << 24)
	}
	return start, end, true
}

func (p *weather) Stop() {}

// publish decodes GRIB2 wind (UGRD/VGRD, possibly several forecast hours) and serves
// it as a multi-step wind document the frontend layer scrubs through.
func (p *weather) publish(gribBytes []byte, srcLabel string) {
	grids, err := grib.Decode(gribBytes)
	if err != nil {
		p.h.Status("error", "GRIB decode: "+err.Error())
		return
	}
	// Group U/V by forecast hour, preserving hour order.
	type uv struct{ u, v *grib.Grid }
	byHour := map[int]*uv{}
	var order []int
	for i := range grids {
		g := &grids[i]
		if g.Category != 2 || (g.Number != 2 && g.Number != 3) {
			continue
		}
		e := byHour[g.ForecastHour]
		if e == nil {
			e = &uv{}
			byHour[g.ForecastHour] = e
			order = append(order, g.ForecastHour)
		}
		if g.Number == 2 {
			e.u = g
		} else {
			e.v = g
		}
	}
	sort.Ints(order)

	var doc windDoc
	for _, hr := range order {
		e := byHour[hr]
		if e.u == nil || e.v == nil {
			continue
		}
		// A global 0.25° field is ~2M points (~30 MB JSON) — too big for the wire and
		// far finer than streamlines need. Downsample oversized grids to a sane size;
		// regional fields pass through untouched.
		h, u, v := capGrid(e.u, e.v, maxGridPoints)
		if doc.Header == (gridHeader{}) {
			doc.RefTime = e.u.RefTime.Format(time.RFC3339)
			doc.Header = h
		}
		doc.Steps = append(doc.Steps, step{Hour: hr, U: u, V: v})
	}
	if len(doc.Steps) == 0 {
		p.h.Status("degraded", "no UGRD/VGRD in GRIB")
		return
	}
	// Publish as a compact binary blob (Float32), not JSON: ~8× smaller, so a full
	// 0.25° field fits the wire without downsampling. The frontend zero-copies the
	// Float32 arrays out of the buffer.
	body := encodeWindBin(&doc)
	p.h.ServeSet("wind.bin", body, func(url string, err error) {
		if err != nil {
			p.h.Status("error", "publish: "+err.Error())
			return
		}
		p.h.Status("running", "wind published ("+srcLabel+"): "+isize(doc.Header.Nx, doc.Header.Ny)+", "+itoa(len(doc.Steps))+" step(s)")
	})
}

// encodeWindBin serialises the multi-step wind field as little-endian binary. Every
// section is 4-byte aligned so the browser can view the u/v arrays as Float32Array
// with zero copying:
//
//	"WGRD" | version u32 | nx u32 | ny u32 | nSteps u32 | lo1,la1,dx,dy f32
//	per step: hour i32 | u[nx*ny] f32 | v[nx*ny] f32
func encodeWindBin(d *windDoc) []byte {
	h := d.Header
	np := h.Nx * h.Ny
	out := make([]byte, 0, 36+len(d.Steps)*(4+np*8))
	put32 := func(v uint32) { out = append(out, byte(v), byte(v>>8), byte(v>>16), byte(v>>24)) }
	putF := func(f float64) { put32(math.Float32bits(float32(f))) }
	out = append(out, 'W', 'G', 'R', 'D')
	put32(1)
	put32(uint32(h.Nx))
	put32(uint32(h.Ny))
	put32(uint32(len(d.Steps)))
	putF(h.Lo1)
	putF(h.La1)
	putF(h.Dx)
	putF(h.Dy)
	for _, s := range d.Steps {
		put32(uint32(int32(s.Hour)))
		for _, x := range s.U {
			putF(x)
		}
		for _, x := range s.V {
			putF(x)
		}
	}
	return out
}

// windDoc is the published multi-step wind field (one grid header, N forecast steps).
type windDoc struct {
	RefTime string     `json:"refTime"`
	Header  gridHeader `json:"header"`
	Steps   []step     `json:"steps"`
}

type gridHeader struct {
	Nx  int     `json:"nx"`
	Ny  int     `json:"ny"`
	Lo1 float64 `json:"lo1"`
	La1 float64 `json:"la1"`
	Lo2 float64 `json:"lo2"`
	La2 float64 `json:"la2"`
	Dx  float64 `json:"dx"`
	Dy  float64 `json:"dy"`
}

type step struct {
	Hour int       `json:"hour"`
	U    []float64 `json:"u"`
	V    []float64 `json:"v"`
}

// maxGridPoints caps a published field so the binary blob (2 × Float32 per point,
// base64 on the wire) stays under the 16 MiB line limit. As binary, a full 0.25°
// global field (~1.04M points ≈ 8.3 MB → ~11 MB base64) fits without downsampling;
// finer/multi-step grids are still thinned.
const maxGridPoints = 1_100_000

// capGrid downsamples u/v (row-major from the grid's first point) by an integer
// stride if the grid exceeds max points, adjusting the header increments. Streamlines
// don't need 0.25° fidelity, and a global field must be thinned to fit the wire.
func capGrid(ug, vg *grib.Grid, max int) (gridHeader, []float64, []float64) {
	nx, ny := ug.Nx, ug.Ny
	h := gridHeader{Nx: nx, Ny: ny, Lo1: ug.Lo1, La1: ug.La1, Lo2: ug.Lo2, La2: ug.La2, Dx: ug.Dx, Dy: ug.Dy}
	if nx*ny <= max {
		return h, ug.Values, vg.Values
	}
	stride := int(math.Ceil(math.Sqrt(float64(nx*ny) / float64(max))))
	nnx, nny := (nx+stride-1)/stride, (ny+stride-1)/stride
	u := make([]float64, 0, nnx*nny)
	v := make([]float64, 0, nnx*nny)
	for y := 0; y < ny; y += stride {
		for x := 0; x < nx; x += stride {
			u = append(u, ug.Values[y*nx+x])
			v = append(v, vg.Values[y*nx+x])
		}
	}
	h.Nx, h.Ny = nnx, nny
	h.Dx, h.Dy = ug.Dx*float64(stride), ug.Dy*float64(stride)
	return h, u, v
}

func isize(nx, ny int) string { return itoa(nx) + "×" + itoa(ny) }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func main() {
	if err := sdk.Run(&weather{}); err != nil {
		panic(err)
	}
}
