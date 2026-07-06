#!/usr/bin/env bash
# Download the curated NOAA ENC cells for the read-only Annapolis demo: the
# general + coastal cells that render the zoomed-out view, plus the approach and
# harbor cells clustered around Annapolis (the Severn/Magothy, Bay Bridge, Kent
# Island, and South/West/Rhode river charts) so you can zoom from the whole bay
# down to the docks across the immediate area. Still a few MB of tiles.
#
# Idempotent: a cell already present in the cache dir is skipped, so CI can cache
# the directory across runs. Driven by `make demo`; override via env:
#   DEMO_CELLS      space-separated NOAA cell IDs (default below)
#   DEMO_CACHE      output dir (default ./.demo-cache)
#   NOAA_URL_BASE   download host (default https://www.charts.noaa.gov/ENCs)
set -euo pipefail

BASE="${NOAA_URL_BASE:-https://www.charts.noaa.gov/ENCs}"
OUT="${DEMO_CACHE:-.demo-cache}"
CELLS="${DEMO_CELLS:-US2EC03M US3EC08M US4MD1DC US4MD1EC US5MD1MC US5MD1MD US5MD1ME US5MD1LB US5MD1LC US5MD1NB US5MD1NC}"

mkdir -p "$OUT"
for c in $CELLS; do
  if [ -s "$OUT/$c.zip" ]; then
    echo "cached  $c"
    continue
  fi
  echo "fetch   $c.zip"
  curl -fSL --retry 3 -o "$OUT/$c.zip" "$BASE/$c.zip"
done

echo "demo cells ready in $OUT:"
ls -1 "$OUT"
