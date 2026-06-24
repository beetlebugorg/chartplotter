---
id: installation
title: Installation
sidebar_position: 2
---

# Installation

You can install chartplotter from a pre-built binary, with `go install`, or from
source.

## Pre-built binaries

1. Go to the [Releases](https://github.com/beetlebugorg/chartplotter/releases)
   page.
2. Download the archive for your operating system and CPU. Builds cover Linux,
   macOS, and Windows on both amd64 (Intel) and arm64 (Apple Silicon, ARM).
3. Extract the archive.
4. Move the `chartplotter` binary somewhere on your `PATH`.

Check that it works:

```sh
chartplotter version
```

## With go install

If you have Go 1.26 or newer, install the latest release with one command:

```sh
go install github.com/beetlebugorg/chartplotter/cmd/chartplotter@latest
```

Go places the binary in `$(go env GOBIN)` (or `$(go env GOPATH)/bin`). Add that
directory to your `PATH` if it is not there already.

## From source

Clone the repository and build it:

```sh
git clone https://github.com/beetlebugorg/chartplotter.git
cd chartplotter
make build
```

The build writes the binary to `bin/chartplotter`. Run it:

```sh
bin/chartplotter version
```

## Requirements

- **Go 1.26 or newer** to build from source or use `go install`.
- Nothing extra to run a pre-built binary. The S-101 catalogue is built into the
  program.

## Memory and disk

Baking tiles is the heavy step. It is memory-intensive: a single large cell holds
all of its geometry in memory while it builds tiles, so a bake can use **several
gigabytes of RAM**. Memory scales with the size and number of cells you bake at
once, and baking many regions in parallel multiplies it. If you run on a small
machine, such as a Raspberry Pi, bake one region at a time.

Once the tiles are built, the cost drops sharply. Serving charts streams
pre-baked tiles from disk, so a running `chartplotter serve` uses only **modest
RAM** — well within a small machine's budget. Plan your memory for the bake, not
for everyday use.

Baked tiles live in your cache directory (`~/.cache/chartplotter`). A region is a
single `.pmtiles` archive; size depends on the area and detail, from a few
megabytes for one harbor to gigabytes for a whole district.

## Next steps

Bake your first chart in the [Getting Started](./getting-started.md) guide.
