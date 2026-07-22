// Command core.weather is a GRIB weather plugin (Tier A, WASM). It decodes GRIB2
// surface fields — 10 m wind plus gust, 2 m temperature, and total cloud cover —
// into a compact binary grid and publishes it as a served artifact at
// GET /plugins/core.weather/serve/wind.bin — the "grid, not tiles" model: the
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

type weather struct {
	h      *sdk.Host
	doc    windDoc // accumulates the GFS forecast series as hours are fetched
	hiDoc  windDoc // accumulates the HRRR high-res series (sailing-area window)
	midDoc windDoc // the same HRRR hours over all of CONUS at ~15 km (look-around)

	gfsLabel, hiLabel string  // published-series summaries for the status line
	gfsGen, hiGen     int     // invalidate an in-flight chain on refresh/config change
	hiLat, hiLon      float64 // window centre of the last HRRR fetch (NaN = none)
	hrrrStarted       bool    // the CONUS look-around layer has been kicked off
	lastRefresh       string  // last seen "refresh" config nonce (UI refresh button)
}

// gfsBucket is the NOAA GFS open-data archive (S3).
const gfsBucket = "https://noaa-gfs-bdp-pds.s3.amazonaws.com"

// hrrrBucket is the NOAA HRRR open-data archive (S3): the 3 km, hourly-cycled CONUS
// model. GFS answers "what's the weather offshore and next week"; HRRR answers it at
// bay scale where the vessel actually sails, so the frontend samples it first.
const hrrrBucket = "https://noaa-hrrr-bdp-pds.s3.amazonaws.com"

// hrrrHours are the HRRR forecast leads fetched (hourly near now, opening out to the
// +18 h horizon every cycle carries). Each hour is wind + gust only (~6 MB); the GFS
// series supplies temperature/cloud and the longer horizon.
var hrrrHours = []int{0, 1, 2, 3, 4, 6, 9, 12, 15, 18}

// The HRRR sailing-area window: resampled from the Lambert CONUS grid to a regular
// lat/lon box around the configured centre (hiLat/hiLon, set automatically by the UI
// from the GPS fix) at ~3 km fidelity.
const (
	hiWindowLat = 5.0  // window height, degrees
	hiWindowLon = 6.0  // window width, degrees
	hiStep      = 0.03 // resample step, degrees (~3 km N-S)
)

// The HRRR "look-around" layer: the SAME downloaded records resampled over the whole
// CONUS domain at ~15 km — panning the chart anywhere in US waters stays on HRRR
// instead of dropping to 0.25° GFS. Costs no extra download (a GRIB byte-range always
// delivers the full field); the coarser step keeps the published blob under the
// serve-size limit.
const (
	midLatMin, midLatMax = 21.5, 48.0
	midLonMin, midLonMax = -122.5, -61.0
	midStep              = 0.15
)

// gfsHours are the forecast lead times fetched for the time slider (each is a
// separate GFS product file). Dense 3-hourly steps through the first half day —
// where linear interpolation between sparse anchors visibly misplaces fronts —
// then widening out to two days.
var gfsHours = []int{0, 3, 6, 9, 12, 18, 24, 36, 48}

// extraFields are the per-hour scalar fields byte-range-fetched alongside 10 m wind
// for the forecast readout. Each is one GRIB record, located by its .idx line and
// verified by GRIB category/number after decode. Missing records are skipped (the
// field is published as NaN for that step).
var extraFields = []struct {
	name         string // step slot
	field, level string // .idx record match
	cat, num     int    // GRIB2 category/number sanity check
}{
	{"gust", "GUST", "surface", 2, 22},
	{"temp", "TMP", "2 m above ground", 0, 0},
	{"cloud", "TCDC", "entire atmosphere", 6, 1},
}

func (p *weather) Start(h *sdk.Host) {
	p.h = h
	p.hiLat, p.hiLon = math.NaN(), math.NaN()
	p.lastRefresh = h.ConfigString("refresh") // don't re-trigger on the persisted nonce
	src := h.ConfigString("source")
	if src == "" {
		src = "gfs" // default: the latest real GFS forecast
	}
	switch {
	case src == "sample":
		p.publish(sampleGRIB, "embedded sample")
	case src == "gfs":
		p.gfsGen++
		p.discoverGFS(p.gfsGen) // auto-resolve the latest cycle → fetch
		p.maybeStartHRRR()
	case strings.Contains(src, "pgrb2"): // an explicit GFS product URL
		h.Status("running", "fetching GFS…")
		p.fetchGFS(src, "GFS", func() { h.Status("degraded", "GFS product not available") })
	default: // any other GRIB2 URL
		h.Status("running", "fetching "+src)
		h.Fetch(src, func(resp *sdk.HTTPResponse, err error) { p.onFetch(resp, err, src) })
	}
}

// ConfigChanged reacts to hot config edits: a bumped "refresh" nonce (the panel's ↻
// button / auto-refresh) re-pulls the newest cycles; otherwise the sailing-area
// centre (hiLat/hiLon, written by the UI from the GPS fix) may have moved and the
// HRRR window follows it — either way without a plugin restart.
func (p *weather) ConfigChanged() {
	if r := p.h.ConfigString("refresh"); r != p.lastRefresh {
		p.lastRefresh = r
		p.h.Log("info", "refresh requested — re-resolving newest cycles")
		src := p.h.ConfigString("source")
		if src == "" || src == "gfs" {
			p.gfsGen++
			p.discoverGFS(p.gfsGen)
		}
		p.hrrrStarted = false // force a fresh HRRR cycle walk (same centre or not)
		p.hiLat, p.hiLon = math.NaN(), math.NaN()
	}
	p.maybeStartHRRR()
}

// configBool reads a boolean config value with a default (the settings toggle
// stores true/false; tolerate strings for hand-edited config).
func (p *weather) configBool(key string, def bool) bool {
	switch v := p.h.Config()[key].(type) {
	case bool:
		return v
	case string:
		return v != "false" && v != "off" && v != "0"
	case float64:
		return v != 0
	}
	return def
}

// configFloat reads a numeric config value (numbers arrive as float64; the settings
// UI may store strings).
func (p *weather) configFloat(key string) (float64, bool) {
	switch v := p.h.Config()[key].(type) {
	case float64:
		return v, true
	case string:
		f, err := strconv.ParseFloat(v, 64)
		return f, err == nil
	}
	return 0, false
}

// maybeStartHRRR kicks off (or re-kicks) the HRRR series. The CONUS look-around
// layer always fetches; the 3 km sailing window additionally needs a centre
// (hiLat/hiLon config, written by the UI from the GPS fix). An unchanged centre
// keeps the already-published series.
func (p *weather) maybeStartHRRR() {
	// The one user-facing knob: HRRR costs ~60 MB per refresh, which matters on
	// metered marine data. Off → GFS-only; withdraw any published layers.
	if !p.configBool("hires", true) {
		if p.hrrrStarted {
			p.hiGen++ // cancels any in-flight chain
			p.hrrrStarted = false
			p.hiLat, p.hiLon = math.NaN(), math.NaN()
			p.h.ServeClear("wind-hi.bin")
			p.h.ServeClear("wind-mid.bin")
			p.hiLabel = ""
			p.status()
		}
		return
	}
	lat, okLat := p.configFloat("hiLat")
	lon, okLon := p.configFloat("hiLon")
	hasCtr := okLat && okLon && lat >= 20 && lat <= 50 && lon >= -130 && lon <= -60
	if hasCtr {
		if math.Abs(lat-p.hiLat) < 0.25 && math.Abs(lon-p.hiLon) < 0.25 {
			return // same sailing area — the published window still covers it
		}
	} else {
		if p.hrrrStarted {
			return // CONUS layer already fetched/fetching; nothing new to do
		}
		lat, lon = math.NaN(), math.NaN() // no centre: mid layer only
	}
	p.hrrrStarted = true
	p.hiLat, p.hiLon = lat, lon
	p.hiGen++
	gen := p.hiGen
	c := time.Now().UTC().Add(-1 * time.Hour).Truncate(time.Hour)
	p.tryHRRRCycle(gen, c, 0)
}

// hrrrHourURL is the HRRR CONUS surface-product URL for a cycle + forecast hour.
func hrrrHourURL(date, hh string, fff int) string {
	return fmt.Sprintf("%s/hrrr.%s/conus/hrrr.t%sz.wrfsfcf%02d.grib2", hrrrBucket, date, hh, fff)
}

// tryHRRRCycle walks back hourly (HRRR cycles hourly, publishing ~1 h behind) until a
// cycle's f00 index answers, then fetches the series.
func (p *weather) tryHRRRCycle(gen int, c time.Time, back int) {
	if gen != p.hiGen {
		return // superseded by a newer window
	}
	if back > 6 {
		p.h.Log("warn", "no recent HRRR cycle available (walked 6 h back)")
		p.h.Status("degraded", "no recent HRRR cycle available")
		return
	}
	t := c.Add(time.Duration(-back) * time.Hour)
	date, hh := t.Format("20060102"), t.Format("15")
	p.h.Fetch(hrrrHourURL(date, hh, 0)+".idx", func(resp *sdk.HTTPResponse, err error) {
		if err != nil || resp == nil || resp.Status != 200 {
			p.tryHRRRCycle(gen, c, back+1)
			return
		}
		p.h.Log("info", "HRRR cycle "+date+" "+hh+"z")
		p.hiDoc, p.midDoc = windDoc{}, windDoc{}
		p.fetchHRRRHour(gen, date, hh, 0)
	})
}

// fetchHRRRHour fetches hrrrHours[i]'s 10 m wind + surface gust, resamples the
// Lambert field to the sailing-area lat/lon window, and chains to the next hour;
// after the last it publishes wind-hi.bin.
func (p *weather) fetchHRRRHour(gen int, date, hh string, i int) {
	if gen != p.hiGen {
		return
	}
	if i >= len(hrrrHours) {
		if len(p.midDoc.Steps) == 0 {
			p.hiLabel = ""
			return
		}
		done := func() {
			p.hiLabel = fmt.Sprintf("HRRR %sz: %d step(s)", hh, len(p.midDoc.Steps))
			p.h.Log("info", fmt.Sprintf("published HRRR layers: %d step(s), window %v", len(p.midDoc.Steps), len(p.hiDoc.Steps) > 0))
			p.status()
		}
		p.h.ServeSet("wind-mid.bin", encodeWindBin(&p.midDoc), func(_ string, err error) {
			if err != nil {
				p.h.Status("error", "publish mid: "+err.Error())
				return
			}
			if len(p.hiDoc.Steps) == 0 { // no sailing-area centre yet
				done()
				return
			}
			p.h.ServeSet("wind-hi.bin", encodeWindBin(&p.hiDoc), func(_ string, err error) {
				if err != nil {
					p.h.Status("error", "publish hi: "+err.Error())
					return
				}
				done()
			})
		})
		return
	}
	fff := hrrrHours[i]
	url := hrrrHourURL(date, hh, fff)
	p.h.Status("running", fmt.Sprintf("fetching HRRR %sz +%dh (%d/%d)…", hh, fff, i+1, len(hrrrHours)))
	next := func() { p.fetchHRRRHour(gen, date, hh, i+1) }
	p.h.Fetch(url+".idx", func(resp *sdk.HTTPResponse, err error) {
		if gen != p.hiGen {
			return
		}
		if err != nil || resp == nil || resp.Status != 200 {
			next()
			return
		}
		recs := parseIdx(string(resp.Body))
		start, end, ok := windRangeRecs(recs)
		if !ok {
			next()
			return
		}
		p.h.FetchOpts(url, map[string]string{"Range": fmt.Sprintf("bytes=%d-%d", start, end)}, func(r *sdk.HTTPResponse, e error) {
			if gen != p.hiGen {
				return
			}
			if e != nil || r == nil || (r.Status != 200 && r.Status != 206) {
				next()
				return
			}
			wind := r.Body
			gs, ge, gok := fieldRange(recs, "GUST", "surface")
			if !gok {
				p.addHRRRStep(fff, wind, nil)
				next()
				return
			}
			p.h.FetchOpts(url, map[string]string{"Range": fmt.Sprintf("bytes=%d-%d", gs, ge)}, func(gr *sdk.HTTPResponse, ge2 error) {
				if gen != p.hiGen {
					return
				}
				var gust []byte
				if ge2 == nil && gr != nil && (gr.Status == 200 || gr.Status == 206) {
					gust = gr.Body
				}
				p.addHRRRStep(fff, wind, gust)
				next()
			})
		})
	})
}

// addHRRRStep decodes an hour's Lambert wind (+ optional gust), resamples it to the
// window around the configured centre — rotating grid-relative winds to earth east/
// north — and appends the step.
func (p *weather) addHRRRStep(fff int, windBlob, gustBlob []byte) {
	grids, err := grib.Decode(windBlob)
	if err != nil {
		return
	}
	var u, v *grib.Grid
	for i := range grids {
		g := &grids[i]
		if g.Template != 30 {
			return
		}
		if g.Category == 2 && g.Number == 2 {
			u = g
		}
		if g.Category == 2 && g.Number == 3 {
			v = g
		}
	}
	if u == nil || v == nil {
		return
	}
	fields := []*grib.Grid{u, v}
	if gustBlob != nil {
		if gg, err := grib.Decode(gustBlob); err == nil && len(gg) > 0 &&
			gg[0].Template == 30 && gg[0].Category == 2 && gg[0].Number == 22 {
			fields = append(fields, &gg[0])
		}
	}
	// The same decoded CONUS field feeds both layers: full 3 km fidelity in the
	// sailing window, ~15 km across the whole domain for panning around.
	addTo := func(doc *windDoc, latMax, lonMin, stepDeg float64, nx, ny int) {
		planes := grib.ResampleLambert(fields, latMax, lonMin, nx, ny, stepDeg, 0, 1)
		if doc.Header == (gridHeader{}) {
			doc.RefTime = u.RefTime.Format(time.RFC3339)
			doc.Header = gridHeader{
				Nx: nx, Ny: ny, Dx: stepDeg, Dy: stepDeg,
				Lo1: lonMin, La1: latMax,
				Lo2: lonMin + float64(nx-1)*stepDeg, La2: latMax - float64(ny-1)*stepDeg,
			}
		}
		st := step{Hour: fff, U: planes[0], V: planes[1]}
		if len(planes) > 2 {
			st.Gust = planes[2]
		}
		doc.Steps = append(doc.Steps, st)
	}
	if !math.IsNaN(p.hiLat) { // the 3 km window needs a sailing-area centre
		addTo(&p.hiDoc, p.hiLat+hiWindowLat/2, p.hiLon-hiWindowLon/2, hiStep,
			int(math.Round(hiWindowLon/hiStep))+1, int(math.Round(hiWindowLat/hiStep))+1)
	}
	addTo(&p.midDoc, midLatMax, midLonMin, midStep,
		int(math.Round((midLonMax-midLonMin)/midStep))+1, int(math.Round((midLatMax-midLatMin)/midStep))+1)
}

// status writes the combined published-series summary.
func (p *weather) status() {
	s := p.gfsLabel
	if p.hiLabel != "" {
		if s != "" {
			s += " · "
		}
		s += p.hiLabel
	}
	if s != "" {
		p.h.Status("running", s)
	}
}

// discoverGFS fetches the latest available GFS cycle. GFS runs at 00/06/12/18Z and a
// cycle is published a few hours after its nominal time, so we start ~5h back, floor
// to a 6h boundary, and walk older until a cycle's .idx is available. Uses the wall
// clock (available to the module) — no bucket listing, which is paginated oldest-first.
func (p *weather) discoverGFS(gen int) {
	p.h.Status("running", "finding latest GFS cycle…")
	c := time.Now().UTC().Add(-5 * time.Hour).Truncate(6 * time.Hour) // newest likely-published cycle
	p.tryCycle(gen, c, 0)
}

// tryCycle attempts the cycle `back` steps before c, falling to the previous one if it
// isn't up yet, up to ~2 days back. Once a cycle is confirmed (its f000 index exists),
// it fetches the whole forecast series for the time slider.
func (p *weather) tryCycle(gen int, c time.Time, back int) {
	if gen != p.gfsGen {
		return // superseded by a refresh
	}
	if back > 8 {
		p.h.Log("warn", "no recent GFS cycle available (walked 2 days back)")
		p.h.Status("degraded", "no recent GFS cycle available")
		return
	}
	t := c.Add(time.Duration(-back*6) * time.Hour)
	date, hh := t.Format("20060102"), t.Format("15")
	p.h.Fetch(gfsHourURL(date, hh, 0)+".idx", func(resp *sdk.HTTPResponse, err error) {
		if gen != p.gfsGen {
			return
		}
		if err != nil || resp == nil || resp.Status != 200 {
			p.tryCycle(gen, c, back+1) // this cycle isn't up yet
			return
		}
		p.h.Log("info", "GFS cycle "+date+" "+hh+"z")
		p.doc = windDoc{}
		p.fetchHour(gen, date, hh, 0)
	})
}

// gfsHourURL is the 0.25° GFS product URL for a cycle + forecast hour. 0.25° resolves
// coastal/inland waters (bays, sounds) that the coarse 0.5° field smears into land —
// the global field is cropped to the chart region (see cropGrid) so the wire stays
// small despite the finer grid.
func gfsHourURL(date, hh string, fff int) string {
	return fmt.Sprintf("%s/gfs.%s/%s/atmos/gfs.t%sz.pgrb2.0p25.f%03d", gfsBucket, date, hh, hh, fff)
}

// fetchHour fetches gfsHours[i]'s 10 m wind (plus the extra readout fields), appends
// it as a forecast step, and chains to the next; after the last it publishes the
// multi-step series. Missing hours are skipped (a fresh cycle may not have the longer
// leads yet).
func (p *weather) fetchHour(gen int, date, hh string, i int) {
	if gen != p.gfsGen {
		return // superseded by a refresh
	}
	if i >= len(gfsHours) {
		if len(p.doc.Steps) == 0 {
			p.h.Status("degraded", "no GFS wind fetched")
			return
		}
		p.h.ServeSet("wind.bin", encodeWindBin(&p.doc), func(_ string, err error) {
			if err != nil {
				p.h.Status("error", "publish: "+err.Error())
				return
			}
			p.gfsLabel = fmt.Sprintf("GFS %s %sz: %d step(s)", date, hh, len(p.doc.Steps))
			p.h.Log("info", fmt.Sprintf("published wind.bin: %s, %d step(s)", isize(p.doc.Header.Nx, p.doc.Header.Ny), len(p.doc.Steps)))
			p.status()
		})
		return
	}
	fff := gfsHours[i]
	url := gfsHourURL(date, hh, fff)
	p.h.Status("running", fmt.Sprintf("fetching GFS %s %sz +%dh (%d/%d)…", date, hh, fff, i+1, len(gfsHours)))
	p.h.Fetch(url+".idx", func(resp *sdk.HTTPResponse, err error) {
		if gen != p.gfsGen {
			return
		}
		if err != nil || resp == nil || resp.Status != 200 {
			p.fetchHour(gen, date, hh, i+1)
			return
		}
		recs := parseIdx(string(resp.Body))
		start, end, ok := windRangeRecs(recs)
		if !ok {
			p.fetchHour(gen, date, hh, i+1)
			return
		}
		p.h.FetchOpts(url, map[string]string{"Range": fmt.Sprintf("bytes=%d-%d", start, end)}, func(r *sdk.HTTPResponse, e error) {
			if gen != p.gfsGen {
				return
			}
			if e != nil || r == nil || (r.Status != 200 && r.Status != 206) {
				p.fetchHour(gen, date, hh, i+1)
				return
			}
			st, ok := p.newStep(fff, r.Body)
			if !ok {
				p.fetchHour(gen, date, hh, i+1)
				return
			}
			p.fetchExtras(url, recs, st, 0, func() {
				p.doc.Steps = append(p.doc.Steps, *st)
				p.fetchHour(gen, date, hh, i+1)
			})
		})
	})
}

// newStep decodes one forecast file's UGRD/VGRD into a cropped step, adopting the
// grid header/reference time from the first successful hour.
func (p *weather) newStep(fff int, blob []byte) (*step, bool) {
	grids, err := grib.Decode(blob)
	if err != nil {
		return nil, false
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
		return nil, false
	}
	h, uu, vv := cropGrid(u, v)
	if p.doc.Header == (gridHeader{}) {
		p.doc.RefTime = u.RefTime.Format(time.RFC3339)
		p.doc.Header = h
	}
	return &step{Hour: fff, U: uu, V: vv}, true
}

// fetchExtras chains the optional per-hour readout fields (gust/temp/cloud) onto st,
// then calls done. A field whose record is missing or fails to decode is simply left
// nil (published as NaN) — the wind step still ships.
func (p *weather) fetchExtras(url string, recs []idxRec, st *step, j int, done func()) {
	if j >= len(extraFields) {
		done()
		return
	}
	f := extraFields[j]
	next := func() { p.fetchExtras(url, recs, st, j+1, done) }
	start, end, ok := fieldRange(recs, f.field, f.level)
	if !ok {
		next()
		return
	}
	p.h.FetchOpts(url, map[string]string{"Range": fmt.Sprintf("bytes=%d-%d", start, end)}, func(r *sdk.HTTPResponse, e error) {
		if e == nil && r != nil && (r.Status == 200 || r.Status == 206) {
			p.addExtra(st, j, r.Body)
		}
		next()
	})
}

// addExtra decodes a single-record blob and stores its cropped values in the step
// slot for extraFields[j], converting to display-friendly units (temp K → °C).
func (p *weather) addExtra(st *step, j int, blob []byte) {
	f := extraFields[j]
	grids, err := grib.Decode(blob)
	if err != nil || len(grids) == 0 {
		return
	}
	g := &grids[0]
	if g.Category != f.cat || g.Number != f.num { // idx line didn't match the record
		return
	}
	_, vals, _ := cropGrid(g, g) // single field: crop once, ignore the duplicate
	switch f.name {
	case "gust":
		st.Gust = vals
	case "temp":
		for i := range vals {
			vals[i] -= 273.15
		}
		st.Temp = vals
	case "cloud":
		st.Cloud = vals
	}
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

// idxRec is one parsed wgrib2 .idx line: byte offset, field name (UGRD, TMP, …),
// level ("10 m above ground", "surface", …) and the forecast-time marker
// ("anl", "24 hour fcst", "18-24 hour ave fcst", …).
type idxRec struct {
	off                 int
	field, level, ftime string
}

// parseIdx parses a wgrib2 .idx listing into records (malformed lines are skipped).
func parseIdx(idx string) []idxRec {
	var recs []idxRec
	for _, ln := range strings.Split(idx, "\n") {
		f := strings.Split(ln, ":")
		if len(f) < 6 {
			continue
		}
		off, e := strconv.Atoi(f[1])
		if e != nil {
			continue
		}
		recs = append(recs, idxRec{off, f[3], f[4], f[5]})
	}
	return recs
}

// fieldRange returns the byte range [start,end] of the first instantaneous record
// matching field+level (its end is the next record's offset). Time-averaged /
// accumulated variants ("18-24 hour ave fcst") are skipped — GFS publishes both for
// TCDC and the averaged one is a different quantity.
func fieldRange(recs []idxRec, field, level string) (start, end int, ok bool) {
	for i, r := range recs {
		if r.field != field || r.level != level || strings.Contains(r.ftime, "ave") || strings.Contains(r.ftime, "acc") {
			continue
		}
		end = r.off + (1 << 24) // last record: read a generous window
		if i+1 < len(recs) {
			end = recs[i+1].off - 1
		}
		return r.off, end, true
	}
	return 0, 0, false
}

// windRange parses a wgrib2 .idx and returns the byte range [start,end] spanning the
// "10 m above ground" UGRD and VGRD records (their end is the next record's offset).
func windRange(idx string) (start, end int, ok bool) {
	return windRangeRecs(parseIdx(idx))
}

// windRangeRecs is windRange over pre-parsed records; UGRD and VGRD are adjacent in
// GFS products, so one span covers both.
func windRangeRecs(recs []idxRec) (start, end int, ok bool) {
	uStart, uEnd, uOK := fieldRange(recs, "UGRD", "10 m above ground")
	vStart, vEnd, vOK := fieldRange(recs, "VGRD", "10 m above ground")
	if !uOK || !vOK {
		return 0, 0, false
	}
	start, end = uStart, vEnd
	if vStart < start {
		start = vStart
	}
	if uEnd > end {
		end = uEnd
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
		// Crop the global field to the chart region — a full 0.25° global grid is far too
		// big for the wire, but the region window keeps full fidelity where it's viewed.
		h, u, v := cropGrid(e.u, e.v)
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
//	"WGRD" | version u32 | nx u32 | ny u32 | nSteps u32 | lo1,la1,dx,dy f32 | refUnix f64
//	per step: hour i32 | mask u32 | the present planes, each [nx*ny] f32
//
// refUnix (the cycle reference time, seconds since epoch) sits at offset 36 as a
// float64, keeping the per-step arrays 4-byte aligned for a zero-copy view. The mask
// (v4) says which planes follow — bit 0 u, 1 v, 2 gust, 3 temp, 4 cloud — so a
// wind-only doc (the HRRR layers) doesn't ship NaN ballast for absent fields.
func encodeWindBin(d *windDoc) []byte {
	h := d.Header
	np := h.Nx * h.Ny
	out := make([]byte, 0, 44+len(d.Steps)*(8+np*20))
	put32 := func(v uint32) { out = append(out, byte(v), byte(v>>8), byte(v>>16), byte(v>>24)) }
	put64 := func(v uint64) {
		out = append(out, byte(v), byte(v>>8), byte(v>>16), byte(v>>24), byte(v>>32), byte(v>>40), byte(v>>48), byte(v>>56))
	}
	putF := func(f float64) { put32(math.Float32bits(float32(f))) }
	var refUnix float64
	if t, err := time.Parse(time.RFC3339, d.RefTime); err == nil {
		refUnix = float64(t.Unix())
	}
	out = append(out, 'W', 'G', 'R', 'D')
	put32(4)
	put32(uint32(h.Nx))
	put32(uint32(h.Ny))
	put32(uint32(len(d.Steps)))
	putF(h.Lo1)
	putF(h.La1)
	putF(h.Dx)
	putF(h.Dy)
	put64(math.Float64bits(refUnix))
	for _, s := range d.Steps {
		put32(uint32(int32(s.Hour)))
		planes := [][]float64{s.U, s.V, s.Gust, s.Temp, s.Cloud}
		mask := uint32(0)
		for i, pl := range planes {
			if pl != nil {
				mask |= 1 << i
			}
		}
		put32(mask)
		for _, pl := range planes {
			for _, x := range pl { // nil planes contribute nothing
				putF(x)
			}
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
	U    []float64 `json:"u"` // 10 m wind, m/s eastward
	V    []float64 `json:"v"` // 10 m wind, m/s northward
	// Optional readout fields — nil when the source didn't provide them (published
	// as NaN so the wire layout stays fixed).
	Gust  []float64 `json:"gust,omitempty"`  // surface gust, m/s
	Temp  []float64 `json:"temp,omitempty"`  // 2 m temperature, °C
	Cloud []float64 `json:"cloud,omitempty"` // total cloud cover, %
}

// maxGridPoints caps a published field so the binary blob (2 × Float32 per point,
// base64 on the wire) stays under the 16 MiB line limit. As binary, a full 0.25°
// global field (~1.04M points ≈ 8.3 MB → ~11 MB base64) fits without downsampling;
// finer/multi-step grids are still thinned.
const maxGridPoints = 1_100_000

// The published region: North America and adjacent waters. GFS global fields are
// cropped to this window so a 0.25° grid (~2° of it) stays small on the wire while
// keeping full fidelity over the charted area. Longitudes are −180..180.
const (
	regionLatMin, regionLatMax = 20.0, 55.0
	regionLonMin, regionLonMax = -130.0, -60.0
)

// cropGrid extracts the region sub-rectangle from a (typically global) grid at full
// resolution, re-basing the header to −180..180 longitude so the frontend samples it
// directly. Falls back to a strided cap if the region isn't within the grid.
func cropGrid(ug, vg *grib.Grid) (gridHeader, []float64, []float64) {
	nx, ny := ug.Nx, ug.Ny
	// Region longitudes into the grid's convention (GFS starts at Lo1=0, runs 0..360).
	toGrid := func(lon float64) float64 {
		for lon < ug.Lo1 {
			lon += 360
		}
		for lon > ug.Lo1+360 {
			lon -= 360
		}
		return lon
	}
	loA, loB := toGrid(regionLonMin), toGrid(regionLonMax)
	x0 := int(math.Ceil((loA - ug.Lo1) / ug.Dx))
	x1 := int(math.Floor((loB - ug.Lo1) / ug.Dx))
	y0 := int(math.Ceil((ug.La1 - regionLatMax) / ug.Dy)) // rows run north→south
	y1 := int(math.Floor((ug.La1 - regionLatMin) / ug.Dy))
	if x0 < 0 {
		x0 = 0
	}
	if x1 > nx-1 {
		x1 = nx - 1
	}
	if y0 < 0 {
		y0 = 0
	}
	if y1 > ny-1 {
		y1 = ny - 1
	}
	if x1 < x0 || y1 < y0 { // region not covered — keep the whole grid, thinned
		return capGrid(ug, vg, maxGridPoints)
	}
	nnx, nny := x1-x0+1, y1-y0+1
	u := make([]float64, 0, nnx*nny)
	v := make([]float64, 0, nnx*nny)
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			u = append(u, ug.Values[y*nx+x])
			v = append(v, vg.Values[y*nx+x])
		}
	}
	lo1 := ug.Lo1 + float64(x0)*ug.Dx
	if lo1 > 180 {
		lo1 -= 360
	}
	h := gridHeader{
		Nx: nnx, Ny: nny, Dx: ug.Dx, Dy: ug.Dy,
		Lo1: lo1, La1: ug.La1 - float64(y0)*ug.Dy,
	}
	h.Lo2 = h.Lo1 + float64(nnx-1)*h.Dx
	h.La2 = h.La1 - float64(nny-1)*h.Dy
	return h, u, v
}

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
