// Package web embeds the chartplotter frontend into the binary so the
// distribution is a single self-contained file: the UI, the S-52 client assets,
// the basemap, glyphs, MapLibre vendor bundle, the NOAA catalog, and the wasm
// ENC baker (chartplotter.wasm + its wasm_exec.js + the wasm-tiles worker).
// All parse/bake/render runs in-browser; the binary only serves these assets and
// proxies NOAA cell downloads.
//
// NOTE: chartplotter.wasm is a generated artifact (gitignored). Build it with
// `make wasm` before `go build`/`make build` — go:embed fails if it's absent.
//
// The default `chartplotter serve` serves these embedded assets. Pass
// `--assets DIR` to serve from a directory on disk instead (for development),
// in which case files present in DIR take precedence and anything missing falls
// back to the embedded copy.
package web

import "embed"

// Assets holds the embedded static frontend. Paths are slash-separated and
// rooted at the web/ directory (for example, "index.html",
// "glyphs/Noto Sans Regular/0-255.pbf").
//
//go:embed index.html
//go:embed chartplotter.mjs chartplotter-app.mjs chart-store.mjs pmtiles-source.mjs zip-import.mjs
//go:embed wasm-tiles.mjs wasm-tiles-worker.js tile-cache.mjs chartplotter.wasm
//go:embed colortables.json linestyles.json patterns.json sprite.json catalog.json
//go:embed patterns.png sprite.png
//go:embed basemap/coastline.geojson
//go:embed glyphs
//go:embed vendor
var Assets embed.FS
