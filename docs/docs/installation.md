---
id: installation
title: Installation
sidebar_position: 2
---

# Installation

The quickest path is to **download a release**: every tagged release publishes a
self-contained `chartplotter` for **linux, macOS, and windows** (amd64 + arm64)
on the [releases page](https://github.com/beetlebugorg/chartplotter-go/releases).
Unpack the archive for your platform and run it — the web frontend and the S-101
catalogue are compiled in, so you supply only the ENC cells. To build it
yourself instead, follow [Build from source](#build-from-source) below.
`go install …@latest` does **not** work — the build statically links a native
library and uses a local `replace` directive.

:::info About the embedded IHO catalogues

The build embeds the **IHO S-101 Portrayal and Feature Catalogues** into the
chart engine. The IHO publishes those catalogues in its own GitHub repositories
with **no declared license**. The build fetches them via git submodules directly
from the IHO's own repositories, and the resulting binaries — both what you build
locally and what the project publishes on the releases page — embed that IHO
material. The project distributes those binaries as an accepted position; see
[THIRD-PARTY-NOTICES.md](https://github.com/beetlebugorg/chartplotter-go/blob/main/THIRD-PARTY-NOTICES.md).

:::

## Run with Docker (recommended)

The simplest way to run chartplotter — and the primary path for the
**server-hub-on-a-boat** deployment (a Raspberry Pi, laptop, or mini PC that
holds all chart state while every screen just points a browser at it) — is the
published container image:

```sh
docker run -p 8080:8080 -v chartplotter-data:/data \
  ghcr.io/beetlebugorg/chartplotter
# open http://localhost:8080
```

Or with Docker Compose, using the [`compose.yaml`](https://github.com/beetlebugorg/chartplotter-go/blob/main/compose.yaml)
in the repo:

```sh
docker compose up -d
```

The image is **multi-arch** (`linux/amd64` + `linux/arm64`), so the same command
runs on a Raspberry Pi (arm64) and on an amd64 box. It is built `FROM scratch`
around a **fully-static musl binary**, so it is tiny — essentially just the
~26 MB binary plus a CA-certificate bundle. The named `/data` volume holds the
downloaded ENC source, the baked tiles, and your settings, and survives image
upgrades (`docker compose pull && docker compose up -d`).

The container binds `0.0.0.0:8080` inside, so map it with `-p 8080:8080` (or any
host port). It writes source ENC to `/data` and regenerable baked tiles to
`/data/cache`.

:::tip macOS and Windows
Run the same image via **Docker Desktop** — no native Mac or Windows binary is
needed. The native binaries below remain available as a secondary option for
bare-metal installs.
:::

## Requirements

- **Go 1.26 or newer.**
- **Zig 0.16** — builds the native `libtile57` chart engine.
- **git** — clones the repos and the engine's IHO catalogue submodules.

## Build from source

chartplotter is two repos that must sit **side by side**:

- `chartplotter` (Go) — the app: server, CLI, web frontend.
- `tile57` (Zig) — the chart engine, built as the `libtile57` static library.
  It has nested submodules of its own (the IHO catalogues).

The app's `go.mod` points at `../tile57/bindings/go`, and its Makefile builds
`../tile57/zig-out/lib/libtile57.a` on demand — so the engine checkout (or a
symlink to it) must be a **sibling directory named `tile57`**.

```sh
# 1. The engine, with its IHO catalogue submodules.
git clone https://github.com/beetlebugorg/tile57.git
cd tile57
git submodule update --init --recursive
cd ..

# 2. The app, as a sibling.
git clone https://github.com/beetlebugorg/chartplotter-go.git
cd chartplotter

# 3. Build: zig-builds libtile57, then a CGO go build.
make build
```

The build writes the binary to `bin/chartplotter`. Check that it works:

```sh
bin/chartplotter version
```

It prints the chartplotter version and the libtile57 engine version. The binary
is self-contained — the web frontend and the S-101 catalogue are compiled in —
so you can copy it to your `PATH` and run it anywhere on the same platform.

If you keep the engine checkout somewhere else, symlink it into place instead:

```sh
ln -s /path/to/your/tile57-checkout ../tile57
```

## Memory and disk

Baking tiles is the heavy step. Memory scales with the size and number of cells
you bake at once, and baking many regions in parallel multiplies it. If you run
on a small machine, such as a Raspberry Pi, bake one region at a time.

Once the tiles are built, the cost drops sharply. Serving charts streams
pre-baked tiles from disk, so a running `chartplotter serve` uses only **modest
RAM** — well within a small machine's budget. Plan your memory for the bake, not
for everyday use.

Baked tiles live in your cache directory (`~/.cache/chartplotter`). Size depends
on the area and detail, from a few megabytes for one harbor to gigabytes for a
whole district.

## Next steps

Bake your first chart in the [Getting Started](./getting-started.md) guide.
