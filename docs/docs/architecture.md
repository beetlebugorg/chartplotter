---
id: architecture
title: Architecture
sidebar_position: 5
---

# Architecture

This page explains how chartplotter turns an S-57 chart cell into vector tiles,
and how the pieces fit together.

## Two repositories, one program

chartplotter is built from two repos:

- **`chartplotter`** (this repo, Go) — the application: the HTTP server and
  chart library, the CLI, NMEA 0183 ingestion, and the web frontend.
- **[`tile57`](https://github.com/beetlebugorg/tile57)** (Zig) — the chart
  engine. It builds **libtile57**, a native static library that is linked into
  the Go binary via CGO and does *all* of the chart work: S-57 decoding, S-101
  portrayal, tiling, tile encoding, and generating the MapLibre style and
  client assets.

The Go code is the hub around the engine; the browser only renders what the
engine baked.

## The pipeline

A chart cell flows through these stages:

```
S-57 ENC cells (.000 + .001… updates)
   │
   ▼
libtile57 — the native engine (linked via CGO)
   │  ISO 8211 decode → S-57 feature model → S-101 portrayal →
   │  web-mercator tiling → MLT/MVT encode →
   │  MapLibre style + sprites, color tables, line styles
   ▼
Chart bundle: tiles/chart.pmtiles + style-{day,dusk,night}.json + assets
   │
   ▼
Go server — the hub (internal/engine/server)
   │  chart library + background bakes, /tiles + /api,
   │  settings, NMEA 0183 / AIS, aux attachments
   ▼
<chart-plotter> web component (web/) — MapLibre GL JS
```

Here is what each stage does:

1. **Decode and model (libtile57).** S-57 cells use the ISO 8211 binary
   container format. The engine decodes the records, applies the sequential
   update files (`.001`, `.002`, …), and builds the feature and geometry model.
2. **Apply S-101 portrayal (libtile57).** The S-101 Portrayal Catalogue —
   compiled into the engine — decides how to draw each feature: which symbol,
   which color, which line style, including conditional symbology.
3. **Tile and encode (libtile57).** Features are projected to web-mercator,
   clipped, and encoded as **MLT** (MapLibre Tile, the default) or **MVT**
   tiles, deduplicated and written into a **PMTiles** archive.
4. **Style and assets (libtile57).** The engine also generates the matching
   MapLibre style (per color scheme) and the client assets: the symbol sprite
   atlas, color tables, line styles, and area patterns.
5. **Serve (Go).** The server hosts the frontend and the tiles, runs background
   bake jobs for chart imports, proxies NOAA cell downloads, persists display
   settings, and ingests NMEA 0183 for own-ship and AIS.
6. **Render (browser).** MapLibre GL JS draws the pre-baked tiles with the
   engine's style. The browser does no portrayal of its own.

## Design decisions

A few choices shape the whole project:

- **One engine.** libtile57 is the *sole* tile, portrayal, style, and asset
  engine. The Go side never draws a chart; it orchestrates the engine and
  serves its output. This means CGO is required — `CGO_ENABLED=0` does not
  build — and cross-compilation uses Zig as the C toolchain.
- **All tile generation runs in the backend.** The CLI or server does the
  baking. The browser only renders pre-baked tiles.
- **Colors are names, not RGB.** Tiles store S-101 color *tokens*. The browser
  resolves Day, Dusk, or Night from `colortables.json`. Switching the lighting
  mode is an instant restyle, with no re-baking.
- **Generate once, adjust live.** Mariner settings — depth shading, soundings,
  contours, and danger highlighting — come from attributes baked into the tiles.
  The viewer applies them live.
- **The binary is self-contained.** The web frontend is embedded in the Go
  binary and the S-101 catalogue is compiled into libtile57, so
  `chartplotter serve` runs from a single file. Everything baked from a user
  action is written to the cache directory, never into the embedded assets.

## Code layout

| Path | What lives there |
| --- | --- |
| `../tile57` | The native engine (separate repo, Zig): S-57 decode, S-101 portrayal, tiling, MLT/MVT encode, style + asset generation. Linked as `libtile57.a`. |
| `pkg/iso8211` | A pure-Go ISO 8211 reader, kept for cell *metadata* (headers, coverage) — not for portrayal. |
| `pkg/s57` | The Go S-57 cell model, slimmed to metadata and simulator needs (e.g. depth areas for the traffic simulator). |
| `internal/engine/baker` | Cell metadata + parse helpers: base + update grouping, header/coverage extraction, and the compilation-scale → navigational-band mapping. It does not bake tiles. |
| `internal/engine/server` | The HTTP server: chart library, background bake jobs, tile serving, settings, aux files, NMEA APIs. |
| `internal/engine/tilesource` | The tile-source abstraction the server serves from: libtile57 live sets, PMTiles, MBTiles. |
| `internal/engine/pmtiles` | A minimal PMTiles v3 reader/writer used by the serving path. |
| `internal/engine/catalog` | Distils the NOAA product catalog into `catalog.json`. |
| `internal/engine/nmea` | NMEA 0183 ingestion (own-ship, AIS) and the traffic simulator. |
| `internal/engine/auxfiles` | ENC companion files (TXTDSC text notes, PICREP pictures) served via `/api/aux`. |
| `cmd/chartplotter` | The command-line interface: `bake`, `serve`, `simulate`, `emit-assets`, … |
| `web` | The MapLibre frontend that renders pre-baked tiles. |

## The web viewer

The frontend is a `<chart-plotter>` web component built on
[MapLibre GL JS](https://maplibre.org/maplibre-gl-js/docs/) (5.12+ is required
to decode the default MLT tiles; the vendored copy is newer). It loads the
engine-generated style and assets, reads the tiles the server hosts, draws the
chart, and handles the Day/Dusk/Night restyle. It does no tile generation of
its own.

## Learn more

- The exact tile layers and fields are in the [Tile Schema](./tile-schema.md),
  which also explains how cells map to navigational bands and how each band bakes
  its own range of zoom levels.
