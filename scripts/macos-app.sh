#!/usr/bin/env bash
# Assemble ChartPlotter.app — the macOS menu-bar launcher bundle:
# Contents/MacOS/dock (the LSUIElement menu-bar app) beside
# Contents/MacOS/chartplotter (the engine it spawns). Runs NATIVELY on macOS
# only — the engine links Apple frameworks and systray needs cgo/Cocoa — making
# this the mac-runner counterpart of scripts/xbuild-tile57.sh + xbuild-dock.sh.
#
# Ad-hoc signs the bundle (codesign --sign -); real identity signing +
# notarisation are a release-pipeline concern layered on top (pass IDENTITY=).
# Outputs dist/ChartPlotter_<version>_darwin_<arch>.zip.
set -euo pipefail

[ "$(uname -s)" = Darwin ] || { echo "macos-app.sh must run on macOS (see header)"; exit 1; }

REPO="$(cd "$(dirname "$0")/.." && pwd)"
OUT="${OUT:-$REPO/dist}"
VERSION="${VERSION:-$(git -C "$REPO" describe --tags --always --dirty 2>/dev/null || echo dev)}"
IDENTITY="${IDENTITY:--}" # "-" = ad-hoc
case "$(uname -m)" in arm64) ARCH=arm64 ;; *) ARCH=amd64 ;; esac

make -C "$REPO" build build-dock VERSION="$VERSION"

APP="$OUT/ChartPlotter.app"
rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
sed "s/@VERSION@/$VERSION/g" "$REPO/packaging/macos/Info.plist" > "$APP/Contents/Info.plist"
cp "$REPO/bin/dock" "$APP/Contents/MacOS/dock"
cp "$REPO/bin/chartplotter" "$APP/Contents/MacOS/chartplotter"

# AppIcon.icns from the generated 512px glyph — sips + iconutil ship with macOS.
iconset="$(mktemp -d)/AppIcon.iconset"
mkdir -p "$iconset"
for s in 16 32 64 128 256; do
  sips -z "$s" "$s" "$REPO/cmd/dock/appicon.png" --out "$iconset/icon_${s}x${s}.png" >/dev/null
done
cp "$REPO/cmd/dock/appicon.png" "$iconset/icon_512x512.png"
iconutil -c icns "$iconset" -o "$APP/Contents/Resources/AppIcon.icns"

# Sign inside-out: the nested engine binary first, then the bundle.
codesign --force --sign "$IDENTITY" "$APP/Contents/MacOS/chartplotter"
codesign --force --sign "$IDENTITY" "$APP"

zip="$OUT/ChartPlotter_${VERSION}_darwin_${ARCH}.zip"
rm -f "$zip"
ditto -c -k --keepParent "$APP" "$zip"
echo "→ $zip"
