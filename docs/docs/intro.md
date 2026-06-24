---
id: intro
title: Introduction
slug: /
sidebar_position: 1
---

# chartplotter

:::warning Not for navigation

This project is coded almost entirely with AI (Claude). It is an experiment in
building a large, complex specification (IHO S-101) with AI, and a personal
learning tool — not a certified or tested product. **Do not rely on it for
real-world navigation.**

:::

**chartplotter** is a marine chart engine written in Go. It turns official NOAA
nautical charts into fast, offline map tiles that you can view in a web browser.

![A chartplotter chart of Chesapeake Bay](/img/ui/chart-day.png)

It reads **S-57** electronic navigational chart (ENC) cells, draws them with the
**S-101 Portrayal Catalogue** — the modern IHO standard for how charts look — and
writes the result to a single **PMTiles** archive of vector tiles. A small web
component built on [MapLibre GL JS](https://maplibre.org/maplibre-gl-js/docs/)
draws the chart.

## Goal

Implement the IHO chart standards — **S-57** (ENC data), **S-101** portrayal (the
successor to S-52), and the wider **S-100 / S-102** family — in **pure Go**, with
**minimal dependencies and no CGO**, so the whole thing cross-compiles to a single
static binary for any platform with just `GOOS`/`GOARCH`.

## What it does

- **Turns charts into tiles.** Give it one or more S-57 cells. It produces a
  `.pmtiles` archive you can serve or copy to another machine.
- **Works offline.** Once you generate a region, you do not need a tile server or
  an internet connection to view it.
- **Switches Day, Dusk, and Night instantly.** The engine stores colors as S-101
  color *names*, not fixed RGB values. The browser looks up the right palette and
  restyles the map. You never regenerate the tiles to change the lighting mode.
- **Applies chart settings in the browser.** Depth shading, soundings, depth
  contours, and danger highlights all come from data baked into the tiles. You
  generate the tiles once and adjust these settings live.
- **Ships as one binary.** The S-101 catalogue is built into the program. You do
  not install extra data files.

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
| **S-101** | The IHO standard that defines how to draw an ENC: colors, symbols, and line styles. Its Portrayal Catalogue holds those rules. |
| **MVT** | Mapbox Vector Tile. A compact binary format for map data. |
| **PMTiles** | A single-file archive that holds many vector tiles. You can serve it with simple byte-range requests. |
