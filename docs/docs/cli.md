---
id: cli
title: CLI Reference
sidebar_position: 4
---

# CLI Reference

This page lists every command. Run `chartplotter <command> --help` to see the
flags for a command at any time.

## version

Print the version and the size of the embedded S-52 presentation library.

```sh
chartplotter version
```

## emit-assets

Generate the S-52 client assets into a directory. These files tell the browser
how to draw the chart: the color tables, the symbol sprites, the line styles, and
the area patterns.

```sh
chartplotter emit-assets DIR
```

| Argument | Description |
| --- | --- |
| `DIR` | Output directory. The command writes the asset files here. |

## emit-pmtiles

Bake one or more S-57 base cells into a single PMTiles archive.

```sh
chartplotter emit-pmtiles OUT.pmtiles CELL.000 [CELL.000 ...]
```

| Argument | Description |
| --- | --- |
| `OUT.pmtiles` | Path to the archive to write. |
| `CELL.000` | One or more S-57 base cells. List as many as you want. |

The command prints the feature count for each cell and the total tile count.

## bake-zip

Extract every S-57 base cell from a NOAA ENC zip and bake them all into one
archive.

```sh
chartplotter bake-zip OUT.pmtiles IN.zip
```

| Argument | Description |
| --- | --- |
| `OUT.pmtiles` | Path to the archive to write. |
| `IN.zip` | A NOAA ENC zip. It may hold one cell or many. |

The command counts S-57 update files (`.001`, `.002`, and so on) but does not
apply them yet.

## catalog-json

Distil NOAA's `ENCProdCat.xml` product catalog into a compact `catalog.json`. The
viewer loads this file to show the list of regions you can download.

```sh
chartplotter catalog-json IN.xml OUT.json
```

| Argument | Description |
| --- | --- |
| `IN.xml` | NOAA `ENCProdCat.xml`. |
| `OUT.json` | Path to the compact catalog to write. |

## provision

Download named NOAA cells and bake them into `charts-user.pmtiles`. The working
directory must already hold a `catalog.json` (see [catalog-json](#catalog-json)),
which provides the download URLs.

```sh
chartplotter provision DIR CELL [CELL ...]
```

| Argument | Description |
| --- | --- |
| `DIR` | Working directory. Must contain `catalog.json`. |
| `CELL` | One or more NOAA cell names, such as `US5MD1MC`. |

The command writes `charts-user.pmtiles` and a `charts-user.json` sidecar into the
directory, and prints a JSON result.

## serve

Serve the web frontend and the provisioning API. The frontend is built into the
binary, so the server needs no files on disk. Everything it bakes is written to
the cache directory, never into the embedded assets.

```sh
chartplotter serve [flags]
```

| Flag | Default | Description |
| --- | --- | --- |
| `--host` | `127.0.0.1` | Address to bind. |
| `--port` | `8080` | Port to bind. |
| `--assets DIR` | embedded | Serve static assets from this directory instead of the built-in embedded bundle. Use this when you develop the frontend. |
| `--cache DIR` | XDG cache | Directory for downloaded region zips and baked archives. Defaults to `~/.cache/chartplotter`. |
| `--clear-cache` | off | Delete the cached zips and archives on startup for a clean slate. |

When you bind to a loopback address (`127.0.0.1`, `localhost`, or `::1`), the
server enforces a Host-header check on the API to guard against DNS-rebind
attacks. Binding to any other address turns this off, because you have chosen to
expose the server on the network.

### The API

The viewer talks to the server over a small HTTP API:

| Method and path | What it does |
| --- | --- |
| `POST /api/provision` | Start a background bake job for the requested cells. Returns right away with a task id. |
| `GET /api/tasks` | Report the current task's status and progress. The viewer polls this. |
| `DELETE /api/charts` | Delete `charts-user.pmtiles` and `charts-user.json`. |
