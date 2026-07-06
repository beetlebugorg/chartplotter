---
id: cli
title: CLI Reference
sidebar_position: 4
---

# CLI Reference

This page lists every command. Run `chartplotter <command> --help` to see the
flags for a command at any time.

## version

Print the chartplotter version and the version of the linked libtile57 engine
(which carries the embedded S-101 catalogue).

```sh
chartplotter version
```

## emit-assets

Generate the S-101 client assets into a directory, using the same libtile57
asset baker that renders the tiles. These files tell the browser how to draw
the chart: the color tables, the symbol sprites, the line styles, and the area
patterns.

```sh
chartplotter emit-assets DIR
```

| Argument / flag | Description |
| --- | --- |
| `DIR` | Output directory. The command writes the asset files here. |
| `--s101 DIR` | Emit from an external S-101 PortrayalCatalog directory instead of libtile57's embedded catalogue (for iterating on symbology rules). |
| `--css FILE` | Palette stylesheet under `Symbols/` (default `daySvgStyle.css`). |

## bake

Bake S-57 ENC data into chart tiles with the libtile57 engine. Inputs can be
`.000` base cells, directories (scanned for `*.000` and `*.zip`), and NOAA ENC
`.zip` bundles. The command groups each cell with its update files (`.001`,
`.002`, …) and the engine applies them.

By default the output is a **self-contained chart bundle** directory:
`tiles/chart.pmtiles`, a per-scheme MapLibre style
(`assets/style-{day,dusk,night}.json`), the client assets, and a
`manifest.json`. A `-o` value ending in `.pmtiles` names the bundle directory
by its stem (`-o charts.pmtiles` → `charts/`).

```sh
chartplotter bake -o charts IN [IN ...]
```

With `--bands`, the output is instead one gap-clipped PMTiles archive **per
navigational band** (`<out>-<slug>.pmtiles`), plus an optional manifest and a
`<out>-aux.zip` of companion files — the format the static demo and widget
workflows use.

| Flag | Default | Description |
| --- | --- | --- |
| `-o, --out PATH` | `charts.pmtiles` | Output bundle directory (default mode) or archive stem (`--bands`). |
| `--bands` | off | Write one gap-clipped archive per navigational band so the client reproduces the best-available display. |
| `--format mlt\|mvt` | `mlt` | Tile encoding. MLT (MapLibre Tile) is the engine default and needs MapLibre GL JS 5.12+ to decode; use `mvt` for consumers without an MLT decoder. |
| `--manifest FILE` | — | With `--bands`, also write a `charts-index.json` manifest for the app's `catalog=…` option. |
| `--base-url URL` | archive basename | URL or prefix for the archive in the manifest. |
| `--overzoom` | off | With `--bands`, overzoom every band down to the world view, so a standalone large-scale set stays visible when zoomed out. |
| `--max-zoom N` | native | Cap the highest baked zoom (`0` = each cell's native band max), then let the client overzoom. |
| `--tile57` | — | Accepted for backwards compatibility and ignored: libtile57 is the only engine, and the bundle output it selected is now the default. |

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
Chart imports are baked into tiles in the backend by libtile57; the browser only
renders pre-baked tiles.

```sh
chartplotter serve [flags]
```

| Flag | Default | Description |
| --- | --- | --- |
| `--host` | `127.0.0.1` | Address to bind. |
| `--port` | `8080` | Port to bind. |
| `--assets DIR` | embedded | Serve static assets from this directory instead of the embedded bundle. Use this when you develop the frontend. |
| `--cache DIR` | XDG cache | Directory for regenerable baked tile sets. Defaults to `~/.cache/chartplotter`. |
| `--data DIR` | XDG data | Directory for source ENC (district zips, raw cells). This is kept safe and never auto-deleted. |
| `--clear-cache` | off | Delete the cached baked archives on startup for a clean slate (source ENC is kept). |
| `--tile57 PATH` | — | Also register a **live** libtile57 tile set from this ENC root, `.zip`, or `.000`: tiles are generated on demand from the cells, with no prebake. The set is registered as `tile57` (TileJSON at `/tiles/tile57.json`). |
| `--s101 DIR` | embedded | Generate the served S-101 client assets from an external PortrayalCatalog directory instead of libtile57's embedded catalogue (for iterating on symbology rules; requires `--s101-fc`). |
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
| `--scenario NAME` | — | Named Annapolis preset that sets the start, route, and traffic. Use `--scenario list` to print the presets. |
| `--center LAT,LON` | `38.978,-76.478` | Own-ship start position (ignored when `--scenario` is set). |
| `--course` | `45` | Own-ship course, degrees true. |
| `--speed` | `6` | Own-ship speed, knots. |
| `--targets` | `6` | Number of AIS targets. |
| `--collision` | on | Put one target on a collision course (`--no-collision` to disable). |
| `--sailing` | off | Own-ship tacks (COG weaves) with varying leeway, so heading ≠ COG. |
| `--drop-gps N` | `0` | Stop own-ship position fixes after N seconds, to test stale or lost GPS (`0` = never). |
| `--seed` | `1` | RNG seed for reproducible scenarios. |
| `--cell FILE` | — | S-57 cell (`.000` or exchange `.zip`) to keep traffic in navigable water. |
| `--min-depth M` | `2` | Minimum charted depth (DRVAL1, meters) that counts as navigable water when `--cell` is set. |
