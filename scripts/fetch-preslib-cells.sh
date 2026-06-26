#!/usr/bin/env bash
# Fetch the S-52 PresLib "ECDIS Chart 1" ENC cells for the live docs demo. The
# source is the IHO PresLib e4.0.0 digital-files draft zip; we extract just the
# ENC cells (*.000 + any update files) into $OUT/cells so `make demo-chart1` can
# bake them. Mirrors scripts/fetch-demo-cells.sh.
#
# Idempotent: if $OUT/cells already holds a *.000 it does nothing, so CI can cache
# the directory across runs. Prefers a local copy of the zip (the untracked
# testdata download) so local builds don't re-download. Override via env:
#   PRESLIB_CACHE   output dir (default ./.preslib-cache)
#   PRESLIB_URL     download URL (default the IHO legacy host)
#   PRESLIB_ZIP     path to a local zip to use instead of downloading
set -euo pipefail

OUT="${PRESLIB_CACHE:-.preslib-cache}"
URL="${PRESLIB_URL:-https://legacy.iho.int/iho_pubs/draft_pubs/PresLib_e4.0.0/Digital_Files/S-52_PresLib_e4.0.0_Digital_Files_Draft.zip}"
LOCAL="${PRESLIB_ZIP:-testdata/S-52_PresLib_e4.0.0_Digital_Files_Draft.zip}"

mkdir -p "$OUT/cells"
if compgen -G "$OUT/cells/*.000" >/dev/null; then
  echo "cached  PresLib Chart 1 cells in $OUT/cells"
  ls -1 "$OUT/cells"
  exit 0
fi

ZIP="$OUT/preslib.zip"
if [ -s "$LOCAL" ]; then
  echo "local   $LOCAL"
  ZIP="$LOCAL"
elif [ ! -s "$ZIP" ]; then
  echo "fetch   $URL"
  curl -fSL --retry 3 -o "$ZIP" "$URL"
fi

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
echo "extract Chart 1 ENC cells"
unzip -qo "$ZIP" -d "$TMP"
ENC_ROOT="$(dirname "$(find "$TMP" -name '*.000' | head -1)")"
[ -n "$ENC_ROOT" ] || { echo "no *.000 cells in $ZIP" >&2; exit 1; }
# Copy the cell base files and any ENC update files (*.001, *.002, …) flat.
find "$ENC_ROOT" -maxdepth 1 -type f \( -name '*.000' -o -regex '.*\.[0-9][0-9][0-9]$' \) \
  -exec cp {} "$OUT/cells/" \;

echo "PresLib Chart 1 cells ready in $OUT/cells:"
ls -1 "$OUT/cells"
