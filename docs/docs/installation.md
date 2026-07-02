---
id: installation
title: Installation
sidebar_position: 2
---

# Installation

chartplotter is **source-only**: you build it yourself from one repository (with
its vendored engine submodule). There are no pre-built binaries, and
`go install …@latest` does not work — the build statically links a native
library and uses a local `replace` directive.

:::info Why no binaries?

The build embeds the **IHO S-101 Portrayal and Feature Catalogues** into the
chart engine. The IHO publishes those catalogues in its own GitHub repositories
with **no declared license**, so the right to *redistribute* them — and any
binary that embeds them — is unresolved. Instead, the build fetches the
catalogues via git submodules **directly from the IHO's own repositories**, so
you obtain the IHO material from the IHO and the project never redistributes
it.

:::

## Requirements

- **Go 1.26 or newer.**
- **Zig 0.16** — builds the native `libtile57` chart engine.
- **git** — clones the repo, the vendored engine, and the IHO catalogue
  submodules.

## Build from source

chartplotter is two repos that build as one:

- `chartplotter` (Go) — the app: server, CLI, web frontend.
- `tile57` (Zig) — the chart engine, built as the `libtile57` static library.
  It is **vendored as the `tile57/` git submodule**, pinned to a known-good
  engine commit, and it has nested submodules of its own (the IHO catalogues).

So one **recursive** clone fetches everything:

```sh
# 1. The app, the engine, and the IHO catalogues, in one clone.
git clone --recurse-submodules https://github.com/beetlebugorg/chartplotter-go.git
cd chartplotter-go

# 2. Build: zig-builds libtile57, then a CGO go build.
make build
```

If you already cloned without `--recurse-submodules`, populate the engine in
place:

```sh
git submodule update --init --recursive
```

The build writes the binary to `bin/chartplotter`. Check that it works:

```sh
bin/chartplotter version
```

It prints the chartplotter version and the libtile57 engine version. The binary
is self-contained — the web frontend and the S-101 catalogue are compiled in —
so you can copy it to your `PATH` and run it anywhere on the same platform.

## Keeping the engine up to date

`git pull` alone does not move submodules: after pulling, run
`git submodule update --init --recursive` to sync the engine to the commit the
repo pins.

If you maintain the repo and want to advance the pin to the engine's latest
`main`, `make bump-tile57` fetches it (nested catalogues included) and stages
the new pointer for you to commit — equivalent to
`git -C tile57 pull origin main && git add tile57`.

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
