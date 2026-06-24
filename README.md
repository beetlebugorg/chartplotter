<h1 align="center">chartplotter</h1>

<p align="center">
  <b>⚓ A marine chart plotter, in Go.</b><br>
  Bake NOAA S-57 ENC cells into offline vector-tile archives and render them in the browser.
</p>

<p align="center">
  <a href="https://github.com/beetlebugorg/chartplotter/actions/workflows/ci.yml"><img src="https://github.com/beetlebugorg/chartplotter/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://github.com/beetlebugorg/chartplotter/releases"><img src="https://img.shields.io/github/v/release/beetlebugorg/chartplotter?sort=semver" alt="Release"></a>
  <a href="https://goreportcard.com/report/github.com/beetlebugorg/chartplotter"><img src="https://goreportcard.com/badge/github.com/beetlebugorg/chartplotter" alt="Go Report Card"></a>
  <a href="LICENSE"><img src="https://img.shields.io/github/license/beetlebugorg/chartplotter" alt="License"></a>
</p>

<p align="center">
  📚 <b><a href="https://beetlebugorg.github.io/chartplotter/">Read the docs →</a></b>
</p>

---

chartplotter turns official NOAA nautical charts into fast map tiles you can view
in a web browser, online or fully offline.

It reads **S-57** electronic navigational chart (ENC) cells and draws them with the
**S-101 Portrayal Catalogue**, the modern IHO standard for how charts look. It
writes the result to a single **PMTiles** archive of **Mapbox Vector Tiles**. A small
`<chart-plotter>` web component, built on
[MapLibre GL JS](https://maplibre.org/maplibre-gl-js/docs/), draws the chart.

The Go backend does all the work. The browser only renders pre-baked tiles, so there
is no heavy WebAssembly pipeline to ship.

> **S-102** bathymetric surface support is planned.

## ✨ Features

- **A complete chart pipeline.** chartplotter does every step: ISO 8211 decode, the
  S-57 feature model, S-101 portrayal, web-Mercator tiling, vector-tile encode, and a
  streaming PMTiles writer.
- **Works offline.** Bake a region once into one `.pmtiles` archive, then serve or
  ship it. You do not need a tile server to view it.
- **Switches Day, Dusk, and Night instantly.** chartplotter stores colors as S-101
  color *names*, not RGB values. The browser resolves the palette from
  `colortables.json`, so changing the lighting mode restyles the map at once — no
  re-bake.
- **Bake once.** Mariner settings — depth shading, soundings, contours, and
  safety-depth danger highlighting — ride along as tile attributes. The viewer applies
  them live.
- **Ships as one binary.** The S-101 catalogue *and* the web frontend build into the
  program. A self-contained `chartplotter serve` needs no files on disk — you supply
  only the ENC cells you bake.
- **Runs a server.** The built-in HTTP server downloads NOAA cells, bakes them in the
  background, and serves the frontend with byte-range support.

## 📦 Install

### Pre-built binaries

Download an archive for your platform from the
[**Releases**](https://github.com/beetlebugorg/chartplotter/releases) page, extract it,
and put `chartplotter` on your `PATH`. The `_s101` builds embed the S-101 catalogue;
the plain builds need `--s101` at runtime.

### With go install

Requires **Go 1.26+**.

```sh
go install github.com/beetlebugorg/chartplotter/cmd/chartplotter@latest
```

### From source

```sh
git clone https://github.com/beetlebugorg/chartplotter.git
cd chartplotter
make build          # -> bin/chartplotter (embeds the catalogue if it is available)
bin/chartplotter version
```

## 🚀 Get started

The frontend is built into the binary, so one file is all you need. Start the server
and open the viewer:

```sh
chartplotter serve
# open http://127.0.0.1:8080 → pick a region → it downloads and bakes → the chart appears
```

The server writes everything it bakes to your cache directory
(`~/.cache/chartplotter`), never into the binary's assets.

You can also bake S-57 cells yourself into a standalone archive:

```sh
# Bake cells, a directory, or a NOAA ENC zip into one archive.
chartplotter bake -o charts.pmtiles US4MD81M.000

# Bake one archive per navigational band (best-available display).
chartplotter bake --bands -o charts.pmtiles US5MD_ENCs.zip
```

To develop the frontend, serve the assets from disk instead of the embedded bundle:

```sh
chartplotter serve --assets web
```

## ⛶ Commands

| Command | What it does |
| --- | --- |
| `version` | Print the version and whether the S-101 catalogue is embedded. |
| `emit-assets DIR` | Write the S-101 client assets (color tables, sprites, line styles, patterns) to a directory. |
| `catalog-json IN.xml OUT.json` | Distil NOAA `ENCProdCat.xml` into a compact `catalog.json`. |
| `bake -o OUT.pmtiles IN…` | Bake S-57 cells, directories, or NOAA ENC zips into a PMTiles archive. |
| `serve [--host] [--port] [--assets DIR]` | Serve the web frontend, the baking API, and the NOAA cell proxy. |
| `simulate` | Run an NMEA 0183 traffic generator over TCP (own-ship + AIS targets) for testing. |

Run `chartplotter <command> --help` for the full flags.

## 🧭 How it works

```
S-57 ENC cell (.000)
   │  ISO 8211 decode             pkg/iso8211
   ▼
S-57 feature + geometry model     pkg/s57
   │  S-101 portrayal             pkg/s100, internal/engine/s101
   ▼
Primitive drawing list (lat/lon)  internal/engine/portrayal
   │  project + clip              internal/engine/tile
   ▼
Mapbox Vector Tiles               internal/engine/mvt
   │  dedup + streaming write     internal/engine/pmtiles
   ▼
charts.pmtiles  ───────────────▶  <chart-plotter> / MapLibre GL JS  (web/)
```

Read the [**Architecture**](https://beetlebugorg.github.io/chartplotter/architecture)
docs for the full pipeline, and the
[**Tile Schema**](https://beetlebugorg.github.io/chartplotter/tile-schema) for the
layer and field contract the frontend depends on.

## 🛠️ Development

```sh
make build      # build bin/chartplotter
make test       # go test ./...
make vet        # go vet ./...
make fmt        # gofmt -w .
make serve      # build + serve web/ on :8080
```

CI runs `gofmt`, `go vet`, `go test`, and `go build` on every push. When you push a
`v*` tag, [GoReleaser](https://goreleaser.com/) cuts a release with binaries for Linux,
macOS, and Windows on amd64 and arm64.

## 📚 Documentation

Full docs live at
**[beetlebugorg.github.io/chartplotter](https://beetlebugorg.github.io/chartplotter/)**:
install, the CLI reference, the chart pipeline, and the vector-tile schema.

## 📄 License

[MIT](LICENSE) © Jeremy Collins
