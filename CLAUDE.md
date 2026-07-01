# chartplotter

A marine chart plotter in Go. It reads NOAA S-57 ENC cells, draws them with the
S-101 Portrayal Catalogue, and bakes them into PMTiles vector-tile archives. A
`<chart-plotter>` web component renders the tiles with MapLibre GL JS. The native
`libtile57` engine (Zig, `../tile57` → chartplotter-native) does all tiling,
portrayal, style, and asset generation, linked into the Go binary via CGO; the
browser only renders pre-baked tiles. S-102 bathymetric support is planned.

## Commands

- `make build` — build `bin/chartplotter`. CGO build linking `libtile57` (built on
  demand from `../tile57` with Zig 0.16); the S-101 catalogue is embedded in the
  lib, so there's no separate sync/embed step. Needs the `../tile57` symlink + Zig.
  (`make build-tile57` is a back-compat alias.)
- `make test` / `make vet` / `make fmt` — run before you commit.
- `make serve` — build and serve `web/` on `:8080`. `make serve-tile57 ENC_ROOT=<dir>`
  also registers a live libtile57 set generated on demand from those cells.
- `make xbuild` — cross-compile release binaries with `zig cc` (linux + windows,
  amd64/arm64). darwin is built natively on a macOS CI runner (Go's own
  `runtime/cgo`/`crypto/x509` link Apple frameworks Zig doesn't bundle).

## Conventions

- **CGO is required.** libtile57 is the sole tile/portrayal engine, linked via CGO,
  so `CGO_ENABLED=0` no longer builds. Cross-compilation is preserved with **Zig as
  the C toolchain** (`zig cc`), not by staying CGO-free. Pure-Go deps are still
  preferred where a native lib isn't the point (e.g. `modernc.org/sqlite`, not
  `mattn/go-sqlite3`). The `../tile57` checkout + Zig 0.16 are hard build deps.
- Use https://www.openbridge.no/ for design and icons.
- Match the style of the code around you.
- Never run `git add -A` or `git add .`. The repo holds large untracked files
  (testdata zips, PDFs, screenshots). Stage specific paths only.
