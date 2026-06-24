#!/usr/bin/env bash
# Assemble a static, prebaked "prod" build of the chartplotter web app into dist/,
# ready to publish to GitHub Pages. The chart data (.pmtiles) is NOT bundled — it
# is hosted externally (a CDN / GitHub Release / object store) and the app is
# pointed at it via PMTILES_URL (a single archive) or CATALOG_URL (a charts-index
# .json manifest). Both can also be overridden per-load with ?pmtiles= / ?catalog=.
#
# Usage:
#   PMTILES_URL=https://cdn.example.com/charts.pmtiles scripts/build-pages.sh
#   CATALOG_URL=https://cdn.example.com/charts-index.json CENTER=-122.4,37.8 ZOOM=9 scripts/build-pages.sh
#
# Then publish dist/ (e.g. `git subtree push --prefix dist origin gh-pages`, or a
# Pages action). The build is prod mode: no chart-download (Library) UI, no Dev UI.
set -euo pipefail

here=$(cd "$(dirname "$0")/.." && pwd)
WEB="$here/web"
DIST="${DIST:-$here/dist}"

# The S-101 client portrayal assets (sprite/colortables/linestyles/patterns) are
# gitignored and generated from the catalogue; build them into web/ so the static
# dist includes them (make build generates them as a side effect).
if [ ! -f "$WEB/sprite.png" ]; then
  echo "generating S-101 client assets for the dist…"
  ( cd "$here" && make --no-print-directory build >/dev/null )
fi

PMTILES_URL="${PMTILES_URL:-}"   # one hosted .pmtiles archive (range-read)
CATALOG_URL="${CATALOG_URL:-}"   # OR a hosted charts-index.json manifest of archives
OSM_PMTILES_URL="${OSM_PMTILES_URL:-}"  # hosted OSM vector (Protomaps) .pmtiles → enables the Vector basemap
CENTER="${CENTER:--98.5,39.5}"   # initial map centre "lon,lat"
ZOOM="${ZOOM:-4}"
TITLE="${TITLE:-chartplotter}"

if [ -z "$PMTILES_URL" ] && [ -z "$CATALOG_URL" ]; then
  echo "warning: neither PMTILES_URL nor CATALOG_URL set — the app will boot with no charts." >&2
  echo "         set one, or append ?pmtiles=<url> / ?catalog=<url> at runtime." >&2
fi

rm -rf "$DIST"
mkdir -p "$DIST"

# Copy the runtime assets only — leave out server-only Go, dev/demo pages, and the
# large local ENC inputs (chart data is hosted externally).
rsync -a \
  --exclude '.regioncache-*.zip' \
  --exclude 'demo/' \
  --exclude 'wasm-demo.html' \
  --exclude 'index.html' \
  --exclude '_*.html' \
  --exclude '*.go' \
  --exclude 'demo-cell.000' \
  --exclude 'charts-user.json' \
  "$WEB"/ "$DIST"/

# GitHub Pages runs Jekyll by default, which skips files/dirs starting with "_"
# (we have none now, but worker/glyph paths are safer) and can rewrite assets.
touch "$DIST/.nojekyll"

# Build the prod element's attributes.
attrs="prod"
[ -n "$PMTILES_URL" ] && attrs="$attrs pmtiles=\"$PMTILES_URL\""
[ -n "$CATALOG_URL" ] && attrs="$attrs catalog=\"$CATALOG_URL\""
[ -n "$OSM_PMTILES_URL" ] && attrs="$attrs osm-pmtiles=\"$OSM_PMTILES_URL\""
attrs="$attrs center=\"$CENTER\" zoom=\"$ZOOM\""

cat > "$DIST/index.html" <<HTML
<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover" />
  <title>$TITLE</title>
  <style>
    html, body { margin: 0; height: 100%; }
    chart-plotter-app { display: block; height: 100%; width: 100%; }
  </style>
  <!-- Prebaked "prod" build: charts load from a hosted archive (configured below
       or via ?pmtiles= / ?catalog=). No download/Dev UI. -->
  <script type="module" src="./chartplotter-app.mjs"></script>
</head>
<body>
  <chart-plotter-app $attrs></chart-plotter-app>
</body>
</html>
HTML

echo "built $DIST ($(du -sh "$DIST" | cut -f1))"
[ -n "$PMTILES_URL" ] && echo "  pmtiles: $PMTILES_URL"
[ -n "$CATALOG_URL" ] && echo "  catalog: $CATALOG_URL"
echo "  publish: e.g. 'git subtree push --prefix dist origin gh-pages' or a Pages action"
