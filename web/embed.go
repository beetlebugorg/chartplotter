// Package web embeds the chartplotter frontend (the static web assets) into the
// binary so a release is a single self-contained file. Only the shipped assets
// are embedded — the UI, the S-52 client assets, the basemap, the glyphs, the
// MapLibre vendor bundle, and the NOAA catalog. User-provided runtime data
// (charts-user.pmtiles, baked region archives, download caches) is never
// embedded; the server reads and writes that on disk.
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
//go:embed colortables.json linestyles.json patterns.json sprite.json catalog.json
//go:embed patterns.png sprite.png
//go:embed basemap/coastline.geojson
//go:embed glyphs
//go:embed vendor
var Assets embed.FS
