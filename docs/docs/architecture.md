---
id: architecture
title: Architecture
sidebar_position: 5
---

# Architecture

This page explains how chartplotter turns an S-57 chart cell into vector tiles,
and how the pieces of the codebase fit together.

## The pipeline

A chart cell flows through these stages:

```
S-57 ENC cell (.000)
   │  decode the binary file        pkg/iso8211
   ▼
S-57 feature + geometry model       pkg/s57
   │  apply S-52 display rules       pkg/s52
   ▼
Primitive drawing list (lat/lon)    internal/engine/portrayal
   │  project to web mercator + clip internal/engine/tile
   ▼
Mapbox Vector Tiles                 internal/engine/mvt
   │  dedup + stream to one file     internal/engine/pmtiles
   ▼
charts.pmtiles  ───────────────▶  the web viewer (MapLibre GL JS)
```

Here is what each stage does:

1. **Decode (ISO 8211).** S-57 cells use the ISO 8211 binary container format.
   The decoder reads the raw records and fields.
2. **Build the S-57 model.** The features (depth areas, buoys, coastlines, and so
   on), their attributes, and their geometry become a queryable in-memory model.
3. **Apply S-52 rules.** The S-52 presentation library decides how to draw each
   feature: which symbol, which color, which line style. This includes
   conditional symbology, where the right symbol depends on a feature's
   attributes.
4. **Build primitives.** The output of S-52 is a list of simple drawing
   primitives in latitude/longitude: filled polygons, stroked lines, symbols,
   patterns, text, and sector lights.
5. **Project and clip.** Each primitive is projected to web-mercator tile
   coordinates and clipped to tile boundaries.
6. **Encode to MVT.** The clipped geometry becomes Mapbox Vector Tile bytes.
7. **Write PMTiles.** Identical tiles are stored once (deduplicated), and all
   tiles stream into a single PMTiles archive.

## Design decisions

A few choices shape the whole project:

- **All tile generation runs in the backend.** The CLI or server does the baking.
  The browser only renders pre-baked tiles. There is no heavy in-browser pipeline
  to ship.
- **Colors are names, not RGB.** Tiles store S-52 color *tokens*. The browser
  resolves Day, Dusk, or Night from `colortables.json`. Switching the lighting
  mode is an instant restyle, with no re-baking.
- **Bake once, never re-bake.** Mariner settings — depth shading, soundings,
  contours, and danger highlighting — come from attributes baked into the tiles.
  The viewer applies them live.
- **One archive is the source of truth.** A baked region is a single `.pmtiles`
  file. Long downloads and bakes run as background tasks that the viewer watches
  through `/api/tasks`.

## Code layout

| Path | What lives there |
| --- | --- |
| `pkg/geo` | Shared `LatLon`, `Point`, and `BoundingBox` types. |
| `pkg/iso8211` | The ISO 8211 binary decoder. |
| `pkg/s57` | The S-57 cell model and spatial/class queries. |
| `pkg/s52` | The S-52 presentation library: rule parsing, lookup, conditional symbology, and colors. |
| `internal/engine/portrayal` | Turns S-52 output into the primitive drawing list. |
| `internal/engine/tile` | Web-mercator projection and clipping. |
| `internal/engine/mvt` | The Mapbox Vector Tile encoder. |
| `internal/engine/pmtiles` | The streaming, deduplicating PMTiles writer. |
| `internal/engine/bake` | The baker: cells in, tiles out. |
| `internal/engine/baker` | High-level bake helpers used by the CLI and server. |
| `internal/engine/catalog` | Distils the NOAA product catalog into `catalog.json`. |
| `internal/engine/assets` | Generates client assets: color tables, line styles, and sprite atlases. |
| `internal/engine/server` | The HTTP server and provisioning API. |
| `cmd/chartplotter` | The command-line interface and dev server. |
| `web` | The MapLibre frontend that renders pre-baked tiles. |

## The web viewer

The frontend is a `<chart-plotter>` web component built on
[MapLibre GL JS](https://maplibre.org/maplibre-gl-js/docs/). It reads the
`.pmtiles` archive and the client assets the server hosts, draws the chart, and
handles the Day/Dusk/Night restyle. It does no tile generation of its own.

## Learn more

- The exact tile layers and fields are in the [Tile Schema](./tile-schema.md).
