# chartplotter-go

An **S-52 marine chart plotter**, in Go. A backend engine parses NOAA-distributed
S-57 ENC cells (ISO 8211 → S-57 → S-52 portrayal → Mapbox Vector Tiles → PMTiles)
and bakes them into offline-capable tile archives; a single `<chart-plotter>` web
component over [MapLibre GL JS](https://maplibre.org/maplibre-gl-js/docs/) renders
them.

This is a Go port of the Zig [`chartplotter`](../chartplotter) architecture, with
one deliberate divergence: **all tile generation is a backend task in Go** (CLI /
server), not in-browser WebAssembly. The browser only renders pre-baked tiles. It
reuses and modernizes the Go S-57/S-52 code from
[`chartplotter-original`](../chartplotter-original). See the port plan for the
phased roadmap.

## Layout

```
pkg/geo        shared LatLon/Point/BoundingBox primitives
pkg/iso8211    ISO 8211 binary decoder
pkg/s57        S-57 cell model + spatial/class query
pkg/s52        S-52 presentation library (DAI parse, lookup, CSP, colours)
internal/engine
  portrayal    S-52 -> lat/lon Primitive IR
  tile         web-mercator projection + clipping
  mvt          Mapbox Vector Tile encode
  pmtiles      streaming + dedup PMTiles writer
  bake         Baker: cells -> tiles -> archive
  basemap      GSHHG shoreline -> GeoJSON / PMTiles
  chartcat     NOAA ENC product catalog -> catalog.json
  assets       colortables / linestyles / sprite atlas generators
cmd/chartplotter   CLI + dev server
web                copied MapLibre frontend (renders pre-baked tiles)
```

## Build

```sh
make build            # -> bin/chartplotter
bin/chartplotter version
make test
```

Requires Go 1.26+. The S-52 Presentation Library (`pkg/s52/preslib`) is embedded
at build time; no runtime data dependency.

## Key invariants (inherited from the Zig product)

- **Colour is emitted as S-52 token strings, never RGB** — the client resolves
  Day/Dusk/Night from `colortables.json` with no re-tile.
- **Bake once, never re-bake** — mariner settings are applied client-side from
  baked attributes.
- **One source of truth on disk** for chart inventory; long downloads/bakes are
  server background tasks observed via `/api/tasks`.

## License

See [`LICENSE`](LICENSE).
