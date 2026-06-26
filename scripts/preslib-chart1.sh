#!/usr/bin/env bash
# Render every panel of the S-52 PresLib "ECDIS Chart 1" with our implementation,
# one PNG per reference-plot page (PresLib e4.0.0 Part I §16, doc pages 238–253),
# for diffing against the spec. Self-contained: extracts the cells, bakes+serves
# them through the normal server-side import path, drives scripts/preslib-chart1.mjs,
# then tears the server down. Re-runnable as-is.
#
#   scripts/preslib-chart1.sh [OUT_DIR]
#
# OUT_DIR defaults to testdata/preslib-chart1-out/ (gitignored). Requires the
# PresLib zip in testdata/ (an untracked IHO download) and a headless Chromium.
set -euo pipefail
cd "$(dirname "$0")/.."

ZIP="testdata/S-52_PresLib_e4.0.0_Digital_Files_Draft.zip"
OUT="${1:-testdata/preslib-chart1-out}"
PORT="${PORT:-8123}"
BIN="bin/chartplotter"

if [[ ! -f "$ZIP" ]]; then
  echo "missing $ZIP — download the S-52 PresLib e4.0.0 digital files into testdata/" >&2
  exit 1
fi

echo "==> building $BIN"
make build >/dev/null

WORK="$(mktemp -d)"
SRV_PID=""
cleanup() {
  [[ -n "$SRV_PID" ]] && kill "$SRV_PID" 2>/dev/null || true
  rm -rf "$WORK"
}
trap cleanup EXIT

echo "==> extracting Chart-1 cells"
unzip -qo "$ZIP" -d "$WORK"
ENC_ROOT="$(dirname "$(find "$WORK" -name '*.000' | head -1)")"
( cd "$ENC_ROOT/.." && zip -qr "$WORK/chart1.zip" "$(basename "$ENC_ROOT")" )

echo "==> serving on :$PORT (temp cache/data)"
"$BIN" serve --assets web/ --cache "$WORK/cache" --data "$WORK/data" --port "$PORT" >"$WORK/serve.log" 2>&1 &
SRV_PID=$!
for _ in $(seq 1 30); do curl -fsS "http://127.0.0.1:$PORT/" >/dev/null 2>&1 && break; sleep 0.5; done

echo "==> importing (server-side bake)"
JOB="$(curl -fsS -X POST "http://127.0.0.1:$PORT/api/import?set=preslib" \
  --data-binary @"$WORK/chart1.zip" -H 'Content-Type: application/zip' \
  | python3 -c 'import sys,json;print(json.load(sys.stdin)["job"])')"
for _ in $(seq 1 60); do
  STATE="$(curl -fsS "http://127.0.0.1:$PORT/api/import/status?job=$JOB" \
    | python3 -c 'import sys,json;print(json.load(sys.stdin).get("state",""))' 2>/dev/null || true)"
  [[ "$STATE" == "done" ]] && break
  [[ "$STATE" == "error" ]] && { echo "import failed"; cat "$WORK/serve.log"; exit 1; }
  sleep 1
done

echo "==> rendering panels → $OUT"
node scripts/preslib-chart1.mjs "http://127.0.0.1:$PORT" "$OUT"

echo "==> done: $(ls "$OUT" | wc -l) PNG(s) in $OUT"
