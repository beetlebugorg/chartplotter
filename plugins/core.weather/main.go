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

// publish decodes GRIB2 wind (UGRD/VGRD) and serves it as the standard wind-particle
// JSON (a two-record "velocity" document the frontend layer consumes).
func (p *weather) publish(gribBytes []byte, srcLabel string) {
	grids, err := grib.Decode(gribBytes)
	if err != nil {
		p.h.Status("error", "GRIB decode: "+err.Error())
		return
	}
	var u, v *grib.Grid
	for i := range grids {
		g := &grids[i]
		if g.Category == 2 && g.Number == 2 {
			u = g
		}
		if g.Category == 2 && g.Number == 3 {
			v = g
		}
	}
	if u == nil || v == nil {
		p.h.Status("degraded", "no UGRD/VGRD in GRIB")
		return
	}
	doc := []record{recordOf(u, "U-component_of_wind", "eastward_wind"), recordOf(v, "V-component_of_wind", "northward_wind")}
	body, _ := json.Marshal(doc)
	p.h.ServeSet("wind.json", body, func(url string, err error) {
		if err != nil {
			p.h.Status("error", "publish: "+err.Error())
			return
		}
		p.h.Status("running", "wind field published ("+srcLabel+"), "+isize(u.Nx, u.Ny))
	})
}

// record is one field in the wind-particle JSON (the de-facto "velocity" format).
type record struct {
	Header header    `json:"header"`
	Data   []float64 `json:"data"`
}

type header struct {
	ParameterCategory int     `json:"parameterCategory"`
	ParameterNumber   int     `json:"parameterNumber"`
	ParameterName     string  `json:"parameterNumberName"`
	ParameterUnit     string  `json:"parameterUnit"`
	Nx                int     `json:"nx"`
	Ny                int     `json:"ny"`
	Lo1               float64 `json:"lo1"`
	La1               float64 `json:"la1"`
	Lo2               float64 `json:"lo2"`
	La2               float64 `json:"la2"`
	Dx                float64 `json:"dx"`
	Dy                float64 `json:"dy"`
	RefTime           string  `json:"refTime"`
	ForecastTime      int     `json:"forecastTime"`
}

func recordOf(g *grib.Grid, name, _ string) record {
	return record{
		Header: header{
			ParameterCategory: g.Category, ParameterNumber: g.Number, ParameterName: name, ParameterUnit: "m.s-1",
			Nx: g.Nx, Ny: g.Ny, Lo1: g.Lo1, La1: g.La1, Lo2: g.Lo2, La2: g.La2, Dx: g.Dx, Dy: g.Dy,
			RefTime: g.RefTime.Format(time.RFC3339), ForecastTime: g.ForecastHour,
		},
		Data: g.Values,
	}
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
