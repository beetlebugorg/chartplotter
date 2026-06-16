// Command chartplotter is the Go chartplotter engine: it bakes NOAA S-57 ENC
// cells into S-52-portrayed Mapbox Vector Tile / PMTiles archives and serves
// the region-centric web frontend. All tile generation happens here in the
// backend; the browser only renders pre-baked tiles.
//
// Subcommands are added phase by phase (bake, emit-assets, serve, ...). See
// the port plan and chartplotter/CHARTS-UI-SPEC.md.
package main

import (
	"fmt"

	"github.com/alecthomas/kong"

	"github.com/beetlebugorg/chartplotter-go/pkg/s52/preslib"
)

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "dev"

type cli struct {
	Version versionCmd `cmd:"" help:"Print version and embedded-asset info."`
}

type versionCmd struct{}

func (versionCmd) Run() error {
	fmt.Printf("chartplotter-go %s\n", version)
	fmt.Printf("embedded S-52 PresLib DAI: %d bytes\n", len(preslib.DAI))
	return nil
}

func main() {
	var c cli
	ctx := kong.Parse(&c,
		kong.Name("chartplotter"),
		kong.Description("S-52 marine chart tile engine (Go)."),
		kong.UsageOnError(),
	)
	ctx.FatalIfErrorf(ctx.Run())
}
