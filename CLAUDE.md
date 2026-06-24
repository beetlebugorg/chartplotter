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

## Conventions

- Use https://www.openbridge.no/ for design and icons.
- Match the style of the code around you.
- Never run `git add -A` or `git add .`. The repo holds large untracked files
  (testdata zips, PDFs, screenshots). Stage specific paths only.
