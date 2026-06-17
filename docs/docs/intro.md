---
id: intro
title: Introduction
slug: /
sidebar_position: 1
---

# chartplotter

**chartplotter** is a marine chart engine written in Go. It turns official NOAA
nautical charts into fast, offline map tiles that you can view in a web browser.

It reads **S-57** electronic navigational chart (ENC) cells, applies the
**S-52** display rules that govern how nautical charts look, and writes the
result to a single **PMTiles** archive of vector tiles. A small web component
built on [MapLibre GL JS](https://maplibre.org/maplibre-gl-js/docs/) draws the
chart.

## What it does

- **Bakes charts into tiles.** Give it one or more S-57 cells. It produces a
  `.pmtiles` archive you can serve or copy to another machine.
- **Works offline.** Once you bake a region, you do not need a tile server or an
  internet connection to view it.
- **Switches Day, Dusk, and Night instantly.** The engine stores colors as S-52
  color *names*, not fixed RGB values. The browser looks up the right palette and
  restyles the map. You never re-bake to change the lighting mode.
- **Applies chart settings in the browser.** Depth shading, soundings, depth
  contours, and danger highlights all come from data baked into the tiles. You
  bake once and adjust these settings live.
- **Ships as one binary.** The S-52 presentation library is built into the
  program. You do not install extra data files.

## Who it is for

Use chartplotter if you want to:

- Build a web app that shows NOAA ENC charts.
- Serve nautical charts that work without a network connection.
- Generate vector tiles from S-57 data in a backend pipeline.

## How to read these docs

1. [Install](./installation.md) the program.
2. Follow the [Getting Started](./getting-started.md) guide to bake your first
   chart and view it.
3. Look up any command in the [CLI Reference](./cli.md).
4. Learn how the pipeline works in [Architecture](./architecture.md).
5. See the exact tile layers and fields in the [Tile Schema](./tile-schema.md).

## A note on terms

| Term | What it means |
| --- | --- |
| **S-57** | The international file format for electronic navigational charts. NOAA distributes U.S. charts as S-57 cells. |
| **ENC cell** | One S-57 chart file, named like `US4MD81M.000`. |
| **S-52** | The international standard that defines how to draw an ENC: colors, symbols, and line styles. |
| **MVT** | Mapbox Vector Tile. A compact binary format for map data. |
| **PMTiles** | A single-file archive that holds many vector tiles. You can serve it with simple byte-range requests. |
