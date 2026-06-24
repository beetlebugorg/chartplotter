---
id: cli
title: CLI Reference
sidebar_position: 4
---

# CLI Reference

This page lists every command. Run `chartplotter <command> --help` to see the
flags for a command at any time.

## version

Print the version and whether the S-101 Portrayal Catalogue is built into the
binary.

```sh
chartplotter version
```

## emit-assets

Generate the S-101 client assets into a directory. These files tell the browser
how to draw the chart: the color tables, the symbol sprites, the line styles, and
the area patterns.

```sh
chartplotter emit-assets DIR
```

| Argument / flag | Description |
| --- | --- |
| `DIR` | Output directory. The command writes the asset files here. |
| `--s101 DIR` | Emit from an external S-101 PortrayalCatalog directory instead of the embedded catalogue. |
| `--css FILE` | Palette stylesheet under `Symbols/` (default `daySvgStyle.css`). |

## bake

Generate a PMTiles archive from S-57 ENC data. Inputs can be `.000` base cells,
directories (scanned for `*.000` and `*.zip`), and NOAA ENC `.zip` bundles. The
command groups each cell with its update files (`.001`, `.002`, …) and applies
them.

```sh
chartplotter bake -o OUT.pmtiles IN [IN ...]
```

| Flag | Default | Description |
| --- | --- | --- |
| `-o, --out FILE` | `charts.pmtiles` | Output archive. |
| `--bands` | off | Write one gap-clipped archive per navigational band (`<out>-<slug>.pmtiles`) so the client reproduces the best-available display. |
| `--manifest FILE` | — | Also write a `charts-index.json` manifest for the app's `catalog=…` option. |
| `--base-url URL` | archive basename | URL or prefix for the archive in the manifest. |
| `--overzoom` | off | Overzoom every band down to the world view, so a standalone large-scale set stays visible when zoomed out. |
| `--max-zoom N` | native | Cap the highest baked zoom (`0` = each cell's native band max), then let the client overzoom. |
| `--s101 DIR` | embedded | Override the embedded catalogue with an external S-101 PortrayalCatalog (requires `--s101-fc`). |
| `--s101-fc FILE` | — | S-101 `FeatureCatalogue.xml` path, used with `--s101`. |

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

## serve

Serve the web frontend together with the server-side baking and tile-serving API.
The frontend is built into the binary, so the server needs no files on disk.
Chart imports are parsed and baked into tiles in the backend; the browser only
renders pre-baked tiles.

```sh
chartplotter serve [flags]
```

| Flag | Default | Description |
| --- | --- | --- |
| `--host` | `127.0.0.1` | Address to bind. |
| `--port` | `8080` | Port to bind. |
| `--assets DIR` | embedded | Serve static assets from this directory instead of the embedded bundle. Use this when you develop the frontend. |
| `--cache DIR` | XDG cache | Directory for regenerable baked `.pmtiles` tile sets. Defaults to `~/.cache/chartplotter`. |
| `--data DIR` | XDG data | Directory for source ENC (district zips, raw cells). This is kept safe and never auto-deleted. |
| `--clear-cache` | off | Delete the cached baked archives on startup for a clean slate (source ENC is kept). |
| `--s101 DIR` | embedded | Override the embedded catalogue with an external S-101 PortrayalCatalog (requires `--s101-fc`). |
| `--s101-fc FILE` | — | S-101 `FeatureCatalogue.xml` path, used with `--s101`. |

When you bind to a loopback address (`127.0.0.1`, `localhost`, or `::1`), the
server enforces a Host-header check on the API to guard against DNS-rebind
attacks. Binding to any other address turns this off, because you have chosen to
expose the server on the network.

### The API

The viewer talks to the server over a small HTTP API. The main endpoints:

| Method and path | What it does |
| --- | --- |
| `GET /api/health` | Liveness check. |
| `POST /api/import` | Start a background bake job for uploaded or named ENC data. |
| `GET /api/import/status`, `/api/import/events` | Poll a job's status, or stream its progress (SSE). |
| `GET /api/packs` | List every baked tile pack and whether it is enabled. |
| `POST /api/set/enable`, `/api/set/disable` | Show or hide a pack on the map (data is kept). |
| `DELETE /api/set` | Unregister a tile set and remove its baked files. |
| `GET /api/cell/<NAME>` | Serve a raw `.000` cell, acting as a NOAA download proxy and cache. |
| `GET /api/settings` | Get or post the persisted display settings (shared across screens). |
| `GET /api/share` | Get or post the latest "share my view" snapshot. |
| `GET /api/aux`, `/api/aux/<name>` | Aux attachment manifest, or one TXTDSC/PICREP file on demand. |
| `GET /api/vessel`, `/api/ais` (+ `/stream`) | NMEA 0183 vessel state and AIS targets, with SSE streams. |

The frontend also fetches tiles from `/tiles/<set>/…`.

## simulate

Run an NMEA 0183 traffic generator over TCP, useful for testing the own-ship and
AIS overlays without live hardware.

```sh
chartplotter simulate [flags]
```

| Flag | Default | Description |
| --- | --- | --- |
| `--host` | `127.0.0.1` | Bind host. |
| `--port` | `10110` | Bind port (IANA NMEA-0183-over-IP). |
| `--center LAT,LON` | `38.978,-76.478` | Own-ship start position. |
| `--course` | `45` | Own-ship course, degrees true. |
| `--speed` | `6` | Own-ship speed, knots. |
| `--targets` | `6` | Number of AIS targets. |
| `--collision` | on | Put one target on a collision course (`--no-collision` to disable). |
| `--seed` | `1` | RNG seed for reproducible scenarios. |
| `--cell FILE` | — | S-57 cell to keep traffic in navigable water. |
