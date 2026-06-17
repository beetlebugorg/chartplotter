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
- Nothing extra to run a pre-built binary. The S-52 presentation library is built
  into the program.

## Next steps

Bake your first chart in the [Getting Started](./getting-started.md) guide.
