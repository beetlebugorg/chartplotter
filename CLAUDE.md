# chartplotter

A marine chart plotter in Go. It reads NOAA S-57 ENC cells, draws them with the
S-101 Portrayal Catalogue, and bakes them into PMTiles vector-tile archives. A
`<chart-plotter>` web component renders the tiles with MapLibre GL JS. The Go
backend does all baking; the browser only renders pre-baked tiles. S-102
bathymetric support is planned.

## Commands

- `make build` — build `bin/chartplotter` (embeds the S-101 catalogue when it is
  available locally).
- `make test` / `make vet` / `make fmt` — run before you commit.
- `make serve` — build and serve `web/` on `:8080`.
- `make build-tile57` — OPT-IN CGO build linking the native `tile57` engine
  (`../tile57` → chartplotter-native) as the tile/asset/style backend (`-tags
  tile57`). `make serve-tile57 ENC_ROOT=<dir>` serves a live libtile57 set;
  `cp bake --tile57` / `cp serve --tile57 <ENC_ROOT>` are the runtime flags. Needs
  the `../tile57` symlink and Zig 0.16.

## Conventions

- **Stay CGO-free.** Builds run with `CGO_ENABLED=0` (the cross-compiled release
  binaries depend on it). Use pure-Go libraries only — e.g. `modernc.org/sqlite`,
  not `mattn/go-sqlite3`. Do not add a dependency that needs cgo. The ONLY
  exception is the opt-in `-tags tile57` backend (`make build-tile57`), which links
  the native libtile57 engine via CGO; it is fully build-tagged, so the default
  build and all release/`xbuild` binaries stay CGO-free. Keep it that way — never
  let `tile57`-tagged code leak into a file that the default build compiles.
- Use https://www.openbridge.no/ for design and icons.
- Match the style of the code around you.
- Never run `git add -A` or `git add .`. The repo holds large untracked files
  (testdata zips, PDFs, screenshots). Stage specific paths only.
