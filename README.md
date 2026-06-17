<h1 align="center">chartplotter</h1>

<p align="center">
  <b>⚓ An S-52 marine chart plotter, in Go.</b><br>
  Bake NOAA S-57 ENC cells into offline vector tile archives and render them in the browser.
</p>

<p align="center">
  <a href="https://github.com/beetlebugorg/chartplotter/actions/workflows/ci.yml"><img src="https://github.com/beetlebugorg/chartplotter/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://github.com/beetlebugorg/chartplotter/releases"><img src="https://img.shields.io/github/v/release/beetlebugorg/chartplotter?sort=semver" alt="Release"></a>
  <a href="https://goreportcard.com/report/github.com/beetlebugorg/chartplotter"><img src="https://goreportcard.com/badge/github.com/beetlebugorg/chartplotter" alt="Go Report Card"></a>
  <a href="LICENSE"><img src="https://img.shields.io/github/license/beetlebugorg/chartplotter" alt="License"></a>
</p>

<p align="center">
  📚 <b><a href="https://beetlebugorg.github.io/chartplotter/">Read the documentation →</a></b>
</p>

---

**chartplotter** turns official NOAA nautical charts into fast, offline map tiles
that you can view in a web browser.

It reads **S-57** electronic navigational chart (ENC) cells, applies the **S-52**
display rules that govern how nautical charts look, and writes the result to a
single **PMTiles** archive of **Mapbox Vector Tiles**. A small `<chart-plotter>`
web component built on [MapLibre GL JS](https://maplibre.org/maplibre-gl-js/docs/)
draws the chart — online or fully offline.

All tile generation runs in the Go backend. The browser only renders pre-baked
tiles, so there is no heavy WebAssembly pipeline to ship.

## ✨ Features

- **A complete chart pipeline.** chartplotter handles every step: ISO 8211
  decode, the S-57 cell model, S-52 portrayal, web-mercator tiling, vector-tile
  encode, and a streaming PMTiles writer.
- **Works offline.** Bake a region once into a single `.pmtiles` archive, then
  serve or ship it. You do not need a tile server to view it.
- **Switches Day, Dusk, and Night instantly.** chartplotter stores colors as
  S-52 color *names*, not RGB values. The browser resolves the palette from
  `colortables.json`, so changing the lighting mode restyles the map at once — no
  re-baking.
- **Bake once, never re-bake.** Mariner settings — depth shading, soundings,
  contours, and safety-depth danger highlighting — come from attributes baked
  into the tiles. The viewer applies them live.
- **Ships as one binary.** The S-52 presentation library is built into the
  program. The only data you supply is the ENC cells you bake.
- **Includes a provisioning server.** A built-in HTTP server downloads NOAA
  cells, bakes them in the background, and serves the web frontend with
  byte-range support.

## 📦 Install

### Pre-built binaries

Download an archive for your platform from the
[**Releases**](https://github.com/beetlebugorg/chartplotter/releases) page,
extract it, and put `chartplotter` on your `PATH`.

### With go install

Requires **Go 1.26+**.

```sh
go install github.com/beetlebugorg/chartplotter/cmd/chartplotter@latest
```

### From source

```sh
git clone https://github.com/beetlebugorg/chartplotter.git
cd chartplotter
make build          # -> bin/chartplotter
bin/chartplotter version
```

## 🚀 Getting Started

Bake an ENC cell into a tile archive and serve the viewer:

```sh
# 1. Bake one or more S-57 cells into a single offline archive.
chartplotter emit-pmtiles charts.pmtiles US4MD81M.000

# 2. Or bake every base cell inside a NOAA ENC zip.
chartplotter bake-zip charts.pmtiles US4MD81M.zip

# 3. Serve the web frontend and the provisioning API.
chartplotter serve --assets web --port 8080
# open http://127.0.0.1:8080
```

To let the viewer download and bake regions on demand, distil the NOAA catalog
once, then run the server:

```sh
chartplotter catalog-json ENCProdCat.xml web/catalog.json
chartplotter serve --assets web
# pick a region in the UI → the server downloads and bakes it → the chart appears
```

## ⛶ Commands

| Command | Description |
| --- | --- |
| `version` | Print the version and embedded-asset info. |
| `emit-assets DIR` | Generate the S-52 client assets (color tables, sprites, line styles, patterns) into a directory. |
| `emit-pmtiles OUT.pmtiles CELL.000…` | Bake one or more S-57 cells into a PMTiles archive. |
| `bake-zip OUT.pmtiles IN.zip` | Bake all base cells in a NOAA ENC zip into a PMTiles archive. |
| `catalog-json IN.xml OUT.json` | Distil NOAA `ENCProdCat.xml` into a compact `catalog.json`. |
| `provision DIR CELL…` | Download NOAA cells and bake `charts-user.pmtiles`. |
| `serve [--host] [--port] [--assets DIR]` | Serve the web frontend and the provisioning API. |

Run `chartplotter <command> --help` for the full flags.

## 🧭 How it works

```
S-57 ENC cell (.000)
   │  ISO 8211 decode             pkg/iso8211
   ▼
S-57 feature + geometry model     pkg/s57
   │  S-52 lookup + symbology      pkg/s52
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

CI runs `gofmt`, `go vet`, `go test`, and `go build` on every push. When you push
a `v*` tag, [GoReleaser](https://goreleaser.com/) cuts a release with binaries for
Linux, macOS, and Windows on amd64 and arm64.

## 📚 Documentation

Full documentation lives at
**[beetlebugorg.github.io/chartplotter](https://beetlebugorg.github.io/chartplotter/)**:
installation, the CLI reference, the chart pipeline, and the vector-tile schema.

## 📄 License

[MIT](LICENSE) © Jeremy Collins
