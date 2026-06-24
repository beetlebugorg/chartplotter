// Package web embeds the chartplotter frontend into the binary so the
// distribution is a single self-contained file: the UI, the S-52 client assets,
// the basemap, glyphs, MapLibre vendor bundle, and the NOAA catalog. Tiles are
// baked server-side (POST /api/import → /tiles/{set}); the browser only renders
// them. The binary serves these assets, serves tiles, and proxies NOAA cell
// downloads.
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
// The S-101 client portrayal assets (colortables.json, linestyles.json,
// patterns.json/.png, sprite.json/.png) are NOT embedded or committed — they're
// generated from the S-101 catalogue at build time (`make` runs emit-assets into
// web/) and regenerated at serve time (the server emits them from the embedded
// catalogue and serves those). So they're never stale.
//
//go:embed index.html
//go:embed manifest.webmanifest
//go:embed src
//go:embed catalog.json
//go:embed icon-192.png icon-512.png apple-touch-icon.png
//go:embed basemap/coastline.geojson
//go:embed glyphs
//go:embed vendor
var Assets embed.FS
