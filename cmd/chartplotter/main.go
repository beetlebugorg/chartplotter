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

	"github.com/beetlebugorg/chartplotter/internal/engine/assets"
	"github.com/beetlebugorg/chartplotter/pkg/s52"
	"github.com/beetlebugorg/chartplotter/pkg/s52/preslib"
)

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "dev"

type cli struct {
	Version     versionCmd     `cmd:"" help:"Print version and embedded-asset info."`
	EmitAssets  emitAssetsCmd  `cmd:"" name:"emit-assets" help:"Generate S-52 client assets (colortables.json, ...) into a directory."`
	CatalogJSON catalogJSONCmd `cmd:"" name:"catalog-json" help:"Distil NOAA ENCProdCat.xml into a compact catalog.json."`
	Bake        bakeCmd        `cmd:"" name:"bake" help:"Bake S-57 ENC cells (.zip/.000/dir) into a PMTiles archive for a prebaked deployment."`
	Serve       serveCmd       `cmd:"" name:"serve" help:"Serve the web frontend (embedded static + wasm) + the NOAA cell proxy."`
	Simulate    simulateCmd    `cmd:"" name:"simulate" help:"Run a NMEA0183 traffic generator over TCP (own-ship + AIS targets) for testing."`
}

type emitAssetsCmd struct {
	Dir  string `arg:"" type:"path" help:"Output directory."`
	S101 string `name:"s101" type:"existingdir" help:"Emit from an S-101 PortrayalCatalog directory instead of the embedded S-52 PresLib (transitional, until the catalogue is embedded)."`
	CSS  string `name:"css" default:"daySvgStyle.css" help:"S-101 palette stylesheet (under Symbols/)."`
}

func (c emitAssetsCmd) Run() error {
	var (
		files []string
		err   error
	)
	if c.S101 != "" {
		files, err = assets.EmitS101(c.S101, c.CSS, c.Dir)
	} else {
		var lib *s52.Library
		if lib, err = s52.LoadLibraryFromBytes(preslib.DAI); err == nil {
			files, err = assets.Emit(lib, c.Dir)
		}
	}
	if err != nil {
		return err
	}
	for _, f := range files {
		fmt.Println("wrote", f)
	}
	return nil
}

type versionCmd struct{}

func (versionCmd) Run() error {
	fmt.Printf("chartplotter %s\n", version)
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
