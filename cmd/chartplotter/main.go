// Command chartplotter is the Go chartplotter engine: it bakes NOAA S-57 ENC
// cells into S-101-portrayed Mapbox Vector Tile / PMTiles archives and serves
// the region-centric web frontend. All tile generation happens here in the
// backend; the browser only renders pre-baked tiles.
//
// Subcommands are added phase by phase (bake, emit-assets, serve, ...). See
// the port plan and chartplotter/CHARTS-UI-SPEC.md.
package main

import (
	"fmt"
	"io/fs"
	"os"

	"github.com/alecthomas/kong"

	"github.com/beetlebugorg/chartplotter/internal/engine/s101catalog"
)

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "dev"

type cli struct {
	Version     versionCmd     `cmd:"" help:"Print version and embedded-asset info."`
	EmitAssets  emitAssetsCmd  `cmd:"" name:"emit-assets" help:"Generate S-101 client assets (colortables.json, ...) into a directory."`
	CatalogJSON catalogJSONCmd `cmd:"" name:"catalog-json" help:"Distil NOAA ENCProdCat.xml into a compact catalog.json."`
	Bake        bakeCmd        `cmd:"" name:"bake" help:"Bake S-57 ENC cells (.zip/.000/dir) into a PMTiles archive for a prebaked deployment."`
	Serve       serveCmd       `cmd:"" name:"serve" help:"Serve the web frontend (embedded static + wasm) + the NOAA cell proxy."`
	Simulate    simulateCmd    `cmd:"" name:"simulate" help:"Run a NMEA0183 traffic generator over TCP (own-ship + AIS targets) for testing."`
}

type emitAssetsCmd struct {
	Dir  string `arg:"" type:"path" help:"Output directory."`
	S101 string `name:"s101" type:"existingdir" help:"Emit from an external S-101 PortrayalCatalog directory instead of the build-time embedded catalogue."`
	CSS  string `name:"css" default:"daySvgStyle.css" help:"S-101 palette stylesheet (under Symbols/)."`
}

func (c emitAssetsCmd) Run() error {
	// Emit the client assets from the S-101 catalogue via the native libtile57
	// asset emitter (emitS101Assets); a CGO-free build has none and errors.
	var catalogFS fs.FS
	switch {
	case c.S101 != "":
		catalogFS = os.DirFS(c.S101)
	case s101catalog.Available():
		fsys, err := s101catalog.PortrayalFS()
		if err != nil {
			return err
		}
		catalogFS = fsys
	default:
		return fmt.Errorf("no S-101 catalogue (build with `make` or pass --s101)")
	}
	files, err := emitS101Assets(catalogFS, c.CSS, c.Dir)
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
	fmt.Printf("embedded S-101 catalogue: %v\n", s101catalog.Available())
	return nil
}

func main() {
	var c cli
	ctx := kong.Parse(&c,
		kong.Name("chartplotter"),
		kong.Description("S-101 marine chart tile engine (Go)."),
		kong.UsageOnError(),
	)
	ctx.FatalIfErrorf(ctx.Run())
}
