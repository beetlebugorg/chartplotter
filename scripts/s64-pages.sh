#!/usr/bin/env bash
# Render the IHO S-64 ENC test dataset's rendering pages with our implementation,
# one PNG per test, for diffing against the S-64 reference plots (testdata/"S-64
# Ed 3.0.3_EN_Clean_Final.pdf"). Self-contained: extracts the cells, bakes+serves
# them through the normal server-side import path, drives scripts/s64-pages.mjs,
# then tears the server down. Re-runnable. Sibling of scripts/preslib-chart1.sh.
#
#   scripts/s64-pages.sh [OUT_DIR]
#
# OUT_DIR defaults to testdata/s64-pages-out/ (gitignored). Requires the S-64 zip
# in testdata/ (an untracked IHO download) and a headless Chromium.
set -euo pipefail
cd "$(dirname "$0")/.."

ZIP="testdata/S-64_ENC_Unencrypted_TDS.zip"
OUT="${1:-testdata/s64-pages-out}"
PORT="${PORT:-8124}"
BIN="bin/chartplotter"

[[ -f "$ZIP" ]] || { echo "missing $ZIP — download the S-64 ENC unencrypted TDS into testdata/" >&2; exit 1; }

echo "==> building $BIN"
make build >/dev/null

WORK="$(mktemp -d)"
SRV_PID=""
cleanup() { [[ -n "$SRV_PID" ]] && kill "$SRV_PID" 2>/dev/null || true; rm -rf "$WORK"; }
trap cleanup EXIT

echo "==> serving on :$PORT (temp cache/data)"
"$BIN" serve --assets web/ --cache "$WORK/cache" --data "$WORK/data" --port "$PORT" >"$WORK/serve.log" 2>&1 &
SRV_PID=$!
for _ in $(seq 1 30); do curl -fsS "http://127.0.0.1:$PORT/" >/dev/null 2>&1 && break; sleep 0.5; done

echo "==> importing S-64 (server-side bake of all cells across the test sections)"
JOB="$(curl -fsS -X POST "http://127.0.0.1:$PORT/api/import?set=s64" \
  --data-binary @"$ZIP" -H 'Content-Type: application/zip' \
  | python3 -c 'import sys,json;print(json.load(sys.stdin)["job"])')"
for _ in $(seq 1 120); do
  STATE="$(curl -fsS "http://127.0.0.1:$PORT/api/import/status?job=$JOB" \
    | python3 -c 'import sys,json;print(json.load(sys.stdin).get("state",""))' 2>/dev/null || true)"
  [[ "$STATE" == "done" ]] && break
  [[ "$STATE" == "error" ]] && { echo "import failed"; cat "$WORK/serve.log"; exit 1; }
  sleep 1
done

echo "==> rendering pages → $OUT"
node scripts/s64-pages.mjs "http://127.0.0.1:$PORT" "$OUT"

echo "==> done: $(ls "$OUT" | wc -l) PNG(s) in $OUT"
