// Command core.weather is a GRIB weather plugin (Tier A, WASM). It decodes a GRIB2
// surface-wind field into a compact grid and publishes it as a served artifact at
// GET /plugins/core.weather/serve/wind.json — the "grid, not tiles" model: the plugin
// is never in the render path, and the frontend animates the grid as wind particles
// entirely client-side.
//
// Data source (config "source"):
//   - "sample" (default): an embedded offline GRIB2 sample, decoded on start.
//   - a URL: fetched via the host (net.http) and decoded — for a live GRIB feed
//     that uses grid-point simple packing.
//
// Build (Tier A): GOOS=wasip1 GOARCH=wasm go build -o plugin.wasm ./plugins/core.weather
package main

import (
	_ "embed"
	"encoding/json"
	"sort"
	"time"

	"github.com/beetlebugorg/chartplotter/plugins/core.weather/grib"
	"github.com/beetlebugorg/chartplotter/sdk"
)

//go:embed sample.grib2
var sampleGRIB []byte

type weather struct{ h *sdk.Host }

func (p *weather) Start(h *sdk.Host) {
	p.h = h
	src := h.ConfigString("source")
	if src == "" || src == "sample" {
		p.publish(sampleGRIB, "embedded sample")
		return
	}
	// Live GRIB over the host-mediated, allow-listed net.http capability.
	h.Status("running", "fetching "+src)
	h.Fetch(src, func(resp *sdk.HTTPResponse, err error) {
		if err != nil || resp == nil || resp.Status != 200 {
			detail := "fetch failed"
			if err != nil {
				detail += ": " + err.Error()
			}
			h.Status("degraded", detail)
			return
		}
		p.publish(resp.Body, src)
	})
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
		if doc.Header == (gridHeader{}) {
			g := e.u
			doc.RefTime = g.RefTime.Format(time.RFC3339)
			doc.Header = gridHeader{Nx: g.Nx, Ny: g.Ny, Lo1: g.Lo1, La1: g.La1, Lo2: g.Lo2, La2: g.La2, Dx: g.Dx, Dy: g.Dy}
		}
		doc.Steps = append(doc.Steps, step{Hour: hr, U: e.u.Values, V: e.v.Values})
	}
	if len(doc.Steps) == 0 {
		p.h.Status("degraded", "no UGRD/VGRD in GRIB")
		return
	}
	body, _ := json.Marshal(doc)
	p.h.ServeSet("wind.json", body, func(url string, err error) {
		if err != nil {
			p.h.Status("error", "publish: "+err.Error())
			return
		}
		p.h.Status("running", "wind published ("+srcLabel+"): "+isize(doc.Header.Nx, doc.Header.Ny)+", "+itoa(len(doc.Steps))+" step(s)")
	})
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
