#!/usr/bin/env bash
# Download the curated NOAA ENC cells for the read-only Annapolis demo, one per
# usage band. NOAA publishes no overview (band 1) or berthing (band 6) cell over
# this spot, so the set is bands 2-5 (general -> harbor); the general band renders
# the zoomed-out view, so the full zoom range still works on little disk.
#
# Idempotent: a cell already present in the cache dir is skipped, so CI can cache
# the directory across runs. Driven by `make demo`; override via env:
#   DEMO_CELLS      space-separated NOAA cell IDs (default below)
#   DEMO_CACHE      output dir (default ./.demo-cache)
#   NOAA_URL_BASE   download host (default https://www.charts.noaa.gov/ENCs)
set -euo pipefail

BASE="${NOAA_URL_BASE:-https://www.charts.noaa.gov/ENCs}"
OUT="${DEMO_CACHE:-.demo-cache}"
CELLS="${DEMO_CELLS:-US2EC03M US3EC08M US4MD1DC US5MD1MC}"

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
