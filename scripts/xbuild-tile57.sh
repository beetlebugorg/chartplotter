#!/usr/bin/env bash
# Cross-compile chartplotter as a CGO binary linking the native libtile57 engine,
# using Zig as the C toolchain (`zig cc`). This is how the tile57-only build keeps
# single-command cross-compilation despite requiring CGO (see
# specs/tile57-only-engine.md, phase 2/3).
#
# Targets: linux + windows, amd64 & arm64 — all four proven to cross-link cleanly
# from any host with Zig alone. darwin is deliberately EXCLUDED: with GOOS=darwin,
# Go's own crypto/x509 links -framework Security / CoreFoundation, which Zig does
# not bundle (it ships a macOS libc, not Apple's frameworks). macOS release
# binaries are therefore built natively on a macOS CI runner, not here.
#
# For each target: build libtile57 for that arch into the engine's zig-out/lib
# (the Go binding links it by that fixed path), then cross-compile with zig cc.
# The host lib is rebuilt at the end so a later native `make build` / `go test`
# works without a manual step.
set -euo pipefail

REPO="$(cd "$(dirname "$0")/.." && pwd)"
# Engine checkout: the sibling ../tile57 by default; a relative override
# (e.g. TILE57=../tile57 from make) is resolved against the repo root.
TILE57="${TILE57:-$REPO/../tile57}"
case "$TILE57" in /*) ;; *) TILE57="$REPO/$TILE57" ;; esac
OUT="${OUT:-$REPO/dist}"
VERSION="${VERSION:-dev}"
# Engine-commit stamp (see Makefile ENGINE_COMMIT): make passes it in; a direct
# script run resolves it the same way. The `-e .git` guard keeps git from walking
# up into THIS repo when the submodule dir is empty.
ENGINE_COMMIT="${ENGINE_COMMIT:-$( { test -e "$TILE57/.git" && git -C "$TILE57" rev-parse --short=9 HEAD 2>/dev/null; } || echo unknown)}"
# Space-separated "GOOS/GOARCH" list; override for a subset, e.g.
# PLATFORMS="linux/arm64" scripts/xbuild-tile57.sh
PLATFORMS="${PLATFORMS:-linux/amd64 linux/arm64 windows/amd64 windows/arm64}"

command -v zig >/dev/null 2>&1 || { echo "zig (0.16) not on PATH"; exit 1; }
[ -f "$TILE57/include/tile57.h" ] || { echo "missing $TILE57 — clone github.com/beetlebugorg/tile57 as a sibling directory named tile57 (or set TILE57=<path>)"; exit 1; }

# GOOS/GOARCH → Zig target triple.
zig_triple() {
  case "$1" in
    linux/amd64)   echo x86_64-linux-gnu ;;
    linux/arm64)   echo aarch64-linux-gnu ;;
    windows/amd64) echo x86_64-windows-gnu ;;
    windows/arm64) echo aarch64-windows-gnu ;;
    darwin/*)      echo "" ;; # built on a macOS runner, not cross-compiled here
    *)             echo "" ;;
  esac
}

mkdir -p "$OUT"
libdir="$TILE57/zig-out/lib"

build_lib() { # $1 = zig triple ("" → host)
  # Clear any stale libtile57.a / tile57.lib so a prior arch never lingers (Zig
  # names the Windows static lib tile57.lib and won't overwrite libtile57.a).
  rm -f "$libdir/libtile57.a" "$libdir/tile57.lib"
  if [ -n "$1" ]; then
    ( cd "$TILE57" && zig build -Dtarget="$1" -Doptimize=ReleaseFast )
  else
    ( cd "$TILE57" && zig build -Doptimize=ReleaseFast )
  fi
  # Normalize the Windows MSVC-style name to the libtile57.a the binding links.
  [ -f "$libdir/libtile57.a" ] || cp "$libdir/tile57.lib" "$libdir/libtile57.a"
}

for plat in $PLATFORMS; do
  triple="$(zig_triple "$plat")"
  if [ -z "$triple" ]; then echo "skip $plat (no zig triple — darwin builds on a macOS runner)"; continue; fi
  goos="${plat%/*}"; goarch="${plat#*/}"; ext=""; [ "$goos" = windows ] && ext=.exe
  echo "→ $plat  (zig -target $triple)"
  build_lib "$triple"
  CGO_ENABLED=1 GOOS="$goos" GOARCH="$goarch" \
    CC="zig cc -target $triple" CXX="zig c++ -target $triple" \
    go build -trimpath \
      -ldflags "-s -w -X main.version=$VERSION -X main.engineCommit=$ENGINE_COMMIT" \
      -o "$OUT/chartplotter_${goos}_${goarch}${ext}" ./cmd/chartplotter
done

echo "→ restoring host libtile57.a"
build_lib ""

echo "→ $OUT"
ls -1 "$OUT"/chartplotter_* 2>/dev/null || true