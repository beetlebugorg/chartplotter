#!/usr/bin/env bash
# Cross-compile chartplotter as a CGO binary linking the native libtile57 engine,
# using Zig as the C toolchain (`zig cc`). This is how the tile57-only build keeps
# single-command cross-compilation despite requiring CGO.
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
#
# LIBC selects the linux C ABI:
#   gnu  (default) — dynamically links glibc (portable across mainstream distros)
#   musl           — links musl STATICALLY (`-extldflags "-static -s"`), yielding a
#                    fully self-contained binary (`ldd` → "not a dynamic executable")
#                    that drops straight into a FROM scratch Docker image — the
#                    "go-dims" pattern. musl is linux-only; windows stays gnu and
#                    darwin is skipped, so `LIBC=musl` builds only the linux targets.
# SKIP_RESTORE=1 skips the final host-lib rebuild — for single-shot container builds
#                where the engine tree is thrown away after the binary is produced.
set -euo pipefail

REPO="$(cd "$(dirname "$0")/.." && pwd)"
# Engine checkout: the ./tile57 submodule by default; a relative override
# (e.g. TILE57=../tile57 from make) is resolved against the repo root.
TILE57="${TILE57:-$REPO/tile57}"
case "$TILE57" in /*) ;; *) TILE57="$REPO/$TILE57" ;; esac
OUT="${OUT:-$REPO/dist}"
VERSION="${VERSION:-dev}"
# Engine-commit stamp (see Makefile ENGINE_COMMIT): make passes it in; a direct
# script run resolves it the same way. The `-e .git` guard keeps git from walking
# up into THIS repo when the submodule dir is empty.
ENGINE_COMMIT="${ENGINE_COMMIT:-$( { test -e "$TILE57/.git" && git -C "$TILE57" rev-parse --short=9 HEAD 2>/dev/null; } || echo unknown)}"
# gnu (default) → dynamic glibc; musl → fully-static, FROM scratch-ready. See header.
LIBC="${LIBC:-gnu}"
# 1 → don't rebuild the host lib at the end (single-shot container builds).
SKIP_RESTORE="${SKIP_RESTORE:-0}"

# Space-separated "GOOS/GOARCH" list; override for a subset, e.g.
# PLATFORMS="linux/arm64" scripts/xbuild-tile57.sh. musl is linux-only, so its
# default drops the windows targets (an explicit windows/* is skipped with a note).
if [ "$LIBC" = musl ]; then
  PLATFORMS="${PLATFORMS:-linux/amd64 linux/arm64}"
else
  PLATFORMS="${PLATFORMS:-linux/amd64 linux/arm64 windows/amd64 windows/arm64}"
fi

command -v zig >/dev/null 2>&1 || { echo "zig (0.16) not on PATH"; exit 1; }
[ -f "$TILE57/include/tile57.h" ] || { echo "missing $TILE57 — run 'git submodule update --init --recursive' for the ./tile57 submodule (or set TILE57=<path> to another engine checkout)"; exit 1; }

# GOOS/GOARCH → Zig target triple. The linux ABI follows $LIBC (gnu|musl); windows
# is always gnu (mingw), darwin builds on a macOS runner.
zig_triple() {
  case "$1" in
    linux/amd64)   echo "x86_64-linux-$LIBC" ;;
    linux/arm64)   echo "aarch64-linux-$LIBC" ;;
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

# musl → static suffix on the artifact name so a musl build never overwrites a gnu
# one in the same dist/, and add the fully-static external-link flags. `-s -w`
# strips Go's own tables; `-extldflags "-static -s"` makes zig's lld emit a static
# ELF and strip the C side too (libtile57 + its deps) — proven to halve the size.
sfx=""; static_ldflags=""
if [ "$LIBC" = musl ]; then
  sfx="_musl"
  static_ldflags='-linkmode external -extldflags "-static -s"'
fi

for plat in $PLATFORMS; do
  goos="${plat%/*}"; goarch="${plat#*/}"; ext=""; [ "$goos" = windows ] && ext=.exe
  if [ "$LIBC" = musl ] && [ "$goos" != linux ]; then echo "skip $plat (musl is linux-only)"; continue; fi
  triple="$(zig_triple "$plat")"
  if [ -z "$triple" ]; then echo "skip $plat (no zig triple — darwin builds on a macOS runner)"; continue; fi
  echo "→ $plat  (zig -target $triple, libc=$LIBC)"
  build_lib "$triple"
  CGO_ENABLED=1 GOOS="$goos" GOARCH="$goarch" \
    CC="zig cc -target $triple" CXX="zig c++ -target $triple" \
    go build -trimpath \
      -ldflags "-s -w $static_ldflags -X main.version=$VERSION -X main.engineCommit=$ENGINE_COMMIT" \
      -o "$OUT/chartplotter_${goos}_${goarch}${sfx}${ext}" ./cmd/chartplotter
done

if [ "$SKIP_RESTORE" = 1 ]; then
  echo "→ SKIP_RESTORE=1: leaving the last cross-built libtile57.a in place"
else
  echo "→ restoring host libtile57.a"
  build_lib ""
fi

echo "→ $OUT"
ls -1 "$OUT"/chartplotter_* 2>/dev/null || true