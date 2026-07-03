# syntax=docker/dockerfile:1

# =============================================================================
# chartplotter — a tiny multi-arch image built from a FULLY STATIC musl binary
# (the "go-dims" pattern). The Go binary is already self-contained: the web
# assets, the S-101 catalogue, and the native libtile57 engine are all embedded
# / statically linked, so a static musl build dropped into `FROM scratch` yields
# an image that is basically just the ~25 MB binary (+ a CA bundle).
#
# Multi-arch WITHOUT QEMU emulation: the builder runs on the native BUILDPLATFORM
# and cross-links each TARGETARCH with `zig cc` (the same recipe as `make musl`),
# so one fast builder produces both linux/amd64 and linux/arm64 by target swap.
#   docker buildx build --platform linux/amd64,linux/arm64 -t chartplotter .
# A plain `docker build` builds only the host arch.
# =============================================================================

# ---- builder: Go 1.26 + Zig 0.16, cross-links the static musl binary ---------
# Pinned to BUILDPLATFORM so it stays native (no emulation) even when the target
# is a different arch — Go + zig do the cross-compile.
FROM --platform=$BUILDPLATFORM golang:1.26-bookworm AS builder

# Zig 0.16 is the C toolchain for the CGO cross-link (see scripts/xbuild-tile57.sh).
ARG ZIG_VERSION=0.16.0
# Predefined by buildx: BUILDARCH = the builder's own arch (fetch the matching
# host Zig); TARGETARCH = the arch we cross-compile FOR.
ARG BUILDARCH
ARG TARGETARCH
# Stamped into the binary (main.version). Pass --build-arg VERSION=v1.2.3 in CI.
ARG VERSION=docker
# Optional: pin the engine to a branch/tag for reproducible builds (default: the
# repo's default branch). The engine ABI lives in include/ + bindings/go.
ARG TILE57_REF=

# Install Zig 0.16 (host arch) — the C cross-compiler `zig cc` links libtile57 +
# the cgo objects against musl for any target triple from this one host. xz-utils
# is needed to unpack Zig's .tar.xz (not in the golang image by default).
RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends xz-utils; \
    rm -rf /var/lib/apt/lists/*; \
    case "$BUILDARCH" in \
      amd64) zarch=x86_64 ;; \
      arm64) zarch=aarch64 ;; \
      *) echo "unsupported build arch: $BUILDARCH" >&2; exit 1 ;; \
    esac; \
    url="https://ziglang.org/download/${ZIG_VERSION}/zig-${zarch}-linux-${ZIG_VERSION}.tar.xz"; \
    curl -fsSL "$url" -o /tmp/zig.tar.xz; \
    mkdir -p /opt/zig; \
    tar -xJf /tmp/zig.tar.xz -C /opt/zig --strip-components=1; \
    rm /tmp/zig.tar.xz; \
    ln -s /opt/zig/zig /usr/local/bin/zig; \
    zig version

# The engine is the PUBLIC github.com/beetlebugorg/tile57, cloned as a SIBLING of
# the source tree so go.mod's `replace … => ../tile57/bindings/go` and the
# binding's cgo LDFLAGS resolve; its submodules carry the IHO S-101 catalogues.
WORKDIR /src
RUN set -eux; \
    if [ -n "$TILE57_REF" ]; then br="--branch $TILE57_REF"; else br=""; fi; \
    git clone --depth 1 $br --recurse-submodules --shallow-submodules \
      https://github.com/beetlebugorg/tile57 /src/tile57; \
    git -C /src/tile57 rev-parse --short=9 HEAD

# chartplotter source beside the engine.
COPY . /src/chartplotter
WORKDIR /src/chartplotter

# Cross-link the fully-static musl binary for TARGETARCH. LIBC=musl makes zig's
# lld emit a static ELF (`ldd` → "not a dynamic executable"); SKIP_RESTORE=1 skips
# the throwaway host-lib rebuild (the tree is discarded after this stage). The Go
# build cache + module cache are mounted so rebuilds are fast.
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    set -eux; \
    LIBC=musl SKIP_RESTORE=1 \
      PLATFORMS="linux/${TARGETARCH}" \
      OUT=/out \
      VERSION="${VERSION}" \
      TILE57=/src/tile57 \
      ENGINE_COMMIT="$(git -C /src/tile57 rev-parse --short=9 HEAD)" \
      scripts/xbuild-tile57.sh; \
    cp "/out/chartplotter_linux_${TARGETARCH}_musl" /chartplotter; \
    # Smoke-test only on a native build — a cross-built (e.g. arm64-on-amd64)
    # binary can't be executed by the amd64 builder (no emulation here).
    if [ "$TARGETARCH" = "$BUILDARCH" ]; then /chartplotter version; fi

# An empty, world-writable /tmp to seed into the scratch image: `serve` uses
# os.MkdirTemp for the regenerated S-101 client assets, and scratch has no /tmp.
RUN mkdir -m 1777 -p /image-tmp

# ---- final: FROM scratch — just the binary + a CA bundle --------------------
FROM scratch

# A static musl binary has NO system trust store, and chartplotter makes outbound
# HTTPS calls (charts.noaa.gov / ienccloud.us chart downloads). VERIFIED: without
# a CA bundle those fetches fail with `x509: certificate signed by unknown
# authority`. Bundle the roots from the builder and point Go's crypto/x509 at them.
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
ENV SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt

COPY --from=builder /chartplotter /chartplotter

# scratch ships no /tmp; `serve` needs one for the regenerated S-101 assets.
COPY --from=builder /image-tmp /tmp

# Persistent chart state (source ENC + baked tiles) — back it with a named volume.
VOLUME ["/data"]
EXPOSE 8080

# `serve` defaults to --host 127.0.0.1, which is unreachable from outside the
# container, so bind 0.0.0.0. Source ENC + baked tiles land under the volume.
ENTRYPOINT ["/chartplotter"]
CMD ["serve", "--host", "0.0.0.0", "--data", "/data", "--cache", "/data/cache"]
