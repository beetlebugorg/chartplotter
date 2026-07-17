#!/usr/bin/env bash
# Cross-compile the dock desktop launcher (cmd/dock) for linux + windows.
# Pure Go — no libtile57, no Zig: systray is Win32 syscalls on windows and D-Bus
# StatusNotifierItem on linux, so CGO stays off and plain GOOS/GOARCH covers it.
# Windows links with -H=windowsgui so double-clicking dock.exe opens no console
# window. darwin is deliberately EXCLUDED: systray needs cgo/Cocoa there, so the
# macOS runner assembles ChartPlotter.app natively via scripts/macos-app.sh.
#
# Outputs dist/dock_<os>_<arch>[.exe], plus the Linux desktop-integration files
# (chartplotter.desktop + chartplotter.png icon) release zips ship alongside.
set -euo pipefail

REPO="$(cd "$(dirname "$0")/.." && pwd)"
OUT="${OUT:-$REPO/dist}"
VERSION="${VERSION:-dev}"
PLATFORMS="${PLATFORMS:-linux/amd64 linux/arm64 windows/amd64 windows/arm64}"

mkdir -p "$OUT"
for plat in $PLATFORMS; do
  goos="${plat%/*}"; goarch="${plat#*/}"; ext=""; gui=""
  case "$goos" in
    windows) ext=.exe; gui="-H=windowsgui" ;;
    darwin)  echo "skip $plat (darwin builds natively via scripts/macos-app.sh)"; continue ;;
  esac
  echo "→ dock $plat"
  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
    go build -trimpath -ldflags "-s -w $gui -X main.version=$VERSION" \
    -o "$OUT/dock_${goos}_${goarch}${ext}" ./cmd/dock
done

cp "$REPO/packaging/linux/chartplotter.desktop" "$OUT/"
cp "$REPO/cmd/dock/appicon.png" "$OUT/chartplotter.png"

echo "→ $OUT"
ls -1 "$OUT"/dock_* "$OUT"/chartplotter.desktop 2>/dev/null || true
