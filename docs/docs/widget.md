---
id: widget
title: Widget
sidebar_position: 4
---

# Widget mode

**Widget mode** is a static, embeddable, read-only build of `<chart-plotter>` — the
same viewer you see [on the home page](./intro.mdx). It has **no backend and does no
in-browser baking**: the chart is a handful of prebaked
[PMTiles](./architecture.md) archives streamed straight from a static host over HTTP
range requests. It is how you put a chartplotter chart on a web page or ship a
self-contained demo.

Widget mode strips the management chrome down to a pure viewer:

- **No chart library, import, or downloads** — the charts are fixed.
- **No share link, no connections (NMEA/AIS), no Advanced / developer tools.**
- **Only the client-side display settings** — General, Text, Units, and Depths —
  applied live and stored locally in the browser. Nothing is sent anywhere.

:::warning Not for navigation
A widget renders a fixed set of cached cells. **Do not use it for real-world
navigation.** See [Known limitations](./limitations.md).
:::

## Enable it

Widget mode is turned on by the `widget` attribute (or the `?widget` query param).
Point the viewer at a prebaked archive — either a per-band set discovered from a
`charts-index.json` next to the page, or a single archive via `pmtiles="…"`:

```html
<script type="module" src="/demo/src/chartplotter.mjs"></script>
<chart-plotter widget assets="/demo/" center="-76.482,38.978" zoom="13"></chart-plotter>
```

The `assets` attribute is the key: the widget resolves **everything** relative to it —
`vendor/maplibre-gl.js`, the S-101 client assets, `glyphs/`, `basemap/`,
`catalog.json`, and `charts-index.json`. Put the whole bundle in one directory and
point `assets` at it. (That is exactly how the [home page](./intro.mdx) embeds the
live chart, via the `<LiveChart/>` React component in `docs/src/components/`.)

## Package it

One make target assembles a complete bundle — it downloads the curated cells, bakes
the per-band archives, emits the S-101 client assets, and copies in the static
frontend:

```sh
make demo                        # → dist/demo/
make demo DEMO_OUT=path/to/out   # → a directory of your choosing
make serve-demo                  # build it, then preview at http://127.0.0.1:8080
```

The result is a self-contained static site. `dist/demo/` contains:

| What | Files |
| --- | --- |
| Per-band tiles + manifest | `demo-<band>.pmtiles`, `charts-index.json` |
| External text/picture files | `demo-aux.zip` |
| S-101 client assets | `colortables.json`, `linestyles.json`, `sprite.{json,png}`, `patterns.{json,png}` |
| Frontend | `index.html`, `src/`, `vendor/`, `glyphs/`, `basemap/`, `catalog.json` |

To bake a different location, override the cells (and re-centre the page):

```sh
make demo DEMO_CELLS="US2EC03M US3EC08M US4MD1DC US5MD1MC"
```

You can also assemble a bundle by hand from the CLI:

```sh
chartplotter bake CELLS… -o out/demo.pmtiles --bands --manifest out/charts-index.json
chartplotter emit-assets out/          # the S-101 client assets
# then copy web/{index.html,src,vendor,glyphs,basemap,catalog.json} into out/
```

## Deploy it

The bundle is **pure static files** — copy the directory to any static host. The only
hard requirement is **HTTP range request support**, because the PMTiles archives are
read with byte ranges. Every real static host has it:

- **GitHub Pages** — what this project uses; CI builds the bundle into
  `docs/static/demo/` so it deploys at `/<baseUrl>/demo/`.
- **Amazon S3 + CloudFront** — serve `.pmtiles` as `application/octet-stream` and
  allow long-lived immutable caching (see `web/customHttp.yml` for the header set).
- **nginx / Caddy / Netlify / Cloudflare Pages** — work out of the box.

What does **not** work: `python3 -m http.server` — it ignores `Range` and returns the
whole archive for every tile fetch. For a quick local preview without deploying,
`make serve-demo` runs the `chartplotter` binary purely as a range-capable static file
server (the widget page itself makes no API calls).

A few small things hosts occasionally need:

- Serve `.pmtiles` with `Content-Type: application/octet-stream` and
  `Accept-Ranges: bytes`.
- Keep relative paths intact — the bundle uses only relative URLs, so it works under
  any sub-path (e.g. `/chartplotter/demo/`) without configuration.
- **Cross-origin embedding:** if the page and the bundle are on *different* origins
  (e.g. your blog embeds a chart hosted on GitHub Pages), the bundle's host must send
  `Access-Control-Allow-Origin` — the assets are fetched with `fetch()` and the sprite
  image with `crossOrigin="anonymous"`. GitHub Pages, S3/CloudFront, and the
  `chartplotter` server already send `*`. A **same-origin** embed — like this docs
  site, where the page and the bundle share a host — needs nothing.
