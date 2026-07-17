---
id: weather-grid
title: "weather.grid: consuming weather data"
---

# The `weather.grid` service

The weather plugin doesn't keep its forecasts to itself: every grid it decodes is
published as a **served artifact** any other plugin (or external client) can
fetch and sample. This page is the contract. If you want forecast wind, gusts,
temperature, cloud cover, or barometric pressure in your own plugin — routing,
anchor-watch wind alarms, polar performance overlays — consume this; don't
re-download GRIB.

## How the provider works

The bundled provider (`org.beetlebug.weather`) publishes three layers,
best-first: an HRRR 3 km window (~5°×6°) centred on the vessel — or the viewed
area when there's no fix — with wind + gust to +18 h; an HRRR ~15 km
CONUS-wide resample of the same records; and a GFS 0.25° base over North
America with wind, gust, 2 m temperature, cloud, and MSL pressure to +48 h.

Both models come from NOAA's open-data S3 archives by **byte-ranging** exactly
the needed GRIB records (offsets from the `.idx` files) — never whole
multi-hundred-MB products. HRRR's Lambert-conformal grid (template 3.30) is
decoded natively, resampled to regular lat/lon, and its grid-relative winds are
rotated to earth-relative (skipping that skews directions by >10° across the
domain; see `plugins/core.weather/grib/lambert.go`). Cycles are auto-discovered
by walking back from the wall clock; a `refresh` config nonce (written by the
UI's ↻ button) re-resolves them via `ConfigWatcher`, with generation counters
cancelling superseded fetch chains. The Go-encode → JS-parse byte layout is
locked by `plugins/core.weather/pipeline_test.go`.

## Discovery

Find a provider through the plugin registry: `GET /api/plugins`, look for a
plugin whose manifest declares

```json
"provides": [{ "service": "weather.grid", "apiVersion": 1 }]
```

then fetch its index at `GET /plugins/<id>/serve/weather.json`:

```json
{
  "service": "weather.grid",
  "apiVersion": 1,
  "format": "WGRD/4",
  "layers": [
    { "name": "window", "url": "wind-hi.bin",  "model": "HRRR 3 km",
      "refTime": "2026-07-17T08:00:00Z", "hours": [0,1,2,3,4,6,9,12,15,18],
      "fields": ["wind10m","gust"],
      "grid": { "nx": 201, "ny": 168, "lo1": -79.5, "la1": 41.45,
                "lo2": -73.5, "la2": 36.44, "dx": 0.03, "dy": 0.03 } },
    { "name": "conus", "url": "wind-mid.bin", "model": "HRRR 15 km", ... },
    { "name": "base",  "url": "wind.bin",     "model": "GFS 0.25°",  ... }
  ],
  "units": { "wind10m": "m/s (u east, v north)", "gust": "m/s",
             "temp2m": "°C", "cloud": "%", "pressure": "hPa" }
}
```

Layers are listed **best-first**: sample the first layer that covers your point
and valid time, fall through to the next. `refTime` is the model cycle; a step's
valid time is `refTime + hours[i]`. Layers can disappear (the high-res toggle,
no GPS fix) — always handle a missing `url` with a fallthrough. Re-fetch the
index when you see new data (the plugin refreshes cycles automatically; poll the
index every few minutes, or on your own refresh cadence).

## The WGRD/4 binary format

Little-endian throughout. All Float32 arrays are 4-byte aligned, so a browser
can view them zero-copy (`new Float32Array(buf, offset, n)`).

```
offset  size  field
0       4     magic "WGRD"
4       4     u32 version (4)
8       4     u32 nx
12      4     u32 ny
16      4     u32 nSteps
20      16    f32 lo1, la1, dx, dy      (grid origin = NW corner; rows run south)
36      8     f64 refUnix               (cycle time, seconds since epoch; 0 = unknown)
44      …     nSteps × step
```

Each step:

```
i32 hour                       forecast lead, hours from refTime
u32 mask                       which planes follow (only present planes are stored)
[nx*ny] f32 per present plane, in mask-bit order
```

Mask bits: `1` u (wind east, m/s) · `2` v (wind north, m/s) · `4` gust (m/s) ·
`8` temp (°C) · `16` cloud (%) · `32` pressure (hPa). Values are row-major from
(`la1`,`lo1`), row stride `nx`, rows going **south** (`la1` is the north edge).
`NaN` cells mean "no coverage here" (e.g. a high-res window's corners outside
the model domain) — treat NaN as a miss and fall through to the next layer.

Versions 2 and 3 exist historically (fixed plane sets, no mask); a consumer only
needs v4 but should check the version field and reject what it doesn't know.

## Sampling reference

Bilinear interpolation in grid space, then linear in time:

```js
function sample(layer, plane, lng, lat) {         // → value or NaN
  const g = layer.grid;
  const fx = (lng - g.lo1) / g.dx, fy = (g.la1 - lat) / g.dy;
  if (fx < 0 || fy < 0 || fx > g.nx - 1 || fy > g.ny - 1) return NaN;
  const x0 = Math.floor(fx), y0 = Math.floor(fy);
  const x1 = Math.min(x0 + 1, g.nx - 1), y1 = Math.min(y0 + 1, g.ny - 1);
  const tx = fx - x0, ty = fy - y0, at = (x, y) => plane[y * g.nx + x];
  return at(x0, y0) * (1 - tx) * (1 - ty) + at(x1, y0) * tx * (1 - ty)
       + at(x0, y1) * (1 - tx) * ty + at(x1, y1) * tx * ty;
}
```

For a valid time between two steps, sample both and blend linearly. Wind
direction (meteorological "from"):
`(atan2(-u, -v) · 180/π + 360) % 360`; speed = `hypot(u, v)`. If you draw wind
**on the chart**, rotate screen vectors by the map bearing — the chart may be
course-up (see the wind overlay in `plugins/core.weather/ui/plugin.mjs` for the
worked pattern).

## Rules for consumers

- **Never** hardcode blob names, grids, hours, or field sets — read the index.
- Respect layer order (`window` → `conus` → `base`); NaN or out-of-coverage
  means try the next layer, not "no data".
- Don't re-download source GRIB yourself for fields the service already carries;
  if you need a field it lacks, ask for it to be added to the provider (the
  format grows by mask bits without breaking existing consumers).
