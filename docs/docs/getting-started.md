---
id: getting-started
title: Getting Started
sidebar_position: 3
---

# Getting Started

This guide shows you how to bake a NOAA chart and view it in a browser. It takes
a few minutes.

## Before you start

- [Install chartplotter](./installation.md).
- Download at least one S-57 ENC cell from NOAA. Cells come as `.000` files,
  often inside a `.zip` named for the cell (for example, `US4MD81M.zip`).

## Step 1: Bake a chart

Turn an ENC cell into a tile archive:

```sh
chartplotter emit-pmtiles charts.pmtiles US4MD81M.000
```

This writes `charts.pmtiles`. The archive holds every tile for that cell.

You can bake several cells into one archive. List them all:

```sh
chartplotter emit-pmtiles charts.pmtiles US4MD81M.000 US5MD11M.000
```

If your cell is still inside a NOAA zip, bake straight from the zip:

```sh
chartplotter bake-zip charts.pmtiles US4MD81M.zip
```

## Step 2: Serve the viewer

Start the built-in server and point it at the web frontend:

```sh
chartplotter serve --assets web --port 8080
```

Open `http://127.0.0.1:8080` in your browser. The chart appears.

Switch between Day, Dusk, and Night in the viewer. The map restyles right away
because the engine stores color names, not fixed colors.

## Step 3 (optional): Let the viewer download charts

Instead of baking by hand, you can let the server download and bake regions on
demand.

First, distil the NOAA product catalog one time:

```sh
chartplotter catalog-json ENCProdCat.xml web/catalog.json
```

Then start the server:

```sh
chartplotter serve --assets web
```

Pick a region in the viewer. The server downloads the cells, bakes them in the
background, and the chart appears when it finishes. The viewer shows progress
while it works.

## What you built

- `charts.pmtiles` — a single, offline-ready archive of vector tiles.
- A running viewer at `http://127.0.0.1:8080`.

## Where to go next

- Look up every flag in the [CLI Reference](./cli.md).
- See how a cell becomes tiles in [Architecture](./architecture.md).
