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

	"github.com/alecthomas/kong"

	tile57 "github.com/beetlebugorg/tile57/bindings/go"
)

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "dev"

// engineCommit is the tile57 (libtile57) checkout's commit this binary was built
// against, stamped via -ldflags "-X main.engineCommit=..." (Makefile
// ENGINE_COMMIT; resolves the default sibling ../tile57 or a TILE57=… override).
// Every bake records it beside the pack so the client can show which engine
// commit produced the visible tiles. "unknown" for a bare `go build`.
var engineCommit = "unknown"

type cli struct {
	Version     versionCmd     `cmd:"" help:"Print version and embedded-asset info."`
	EmitAssets  emitAssetsCmd  `cmd:"" name:"emit-assets" help:"Generate S-101 client assets (colortables.json, ...) into a directory."`
	CatalogJSON catalogJSONCmd `cmd:"" name:"catalog-json" help:"Distil NOAA ENCProdCat.xml into a compact catalog.json."`
	Bake        bakeCmd        `cmd:"" name:"bake" help:"Bake S-57 ENC cells (.zip/.000/dir) into a PMTiles archive for a prebaked deployment."`
	Serve       serveCmd       `cmd:"" name:"serve" help:"Serve the web frontend (embedded static) + the NOAA cell proxy."`
	Simulate    simulateCmd    `cmd:"" name:"simulate" help:"Run a NMEA0183 traffic generator over TCP (own-ship + AIS targets) for testing."`
}

type emitAssetsCmd struct {
	Dir  string `arg:"" type:"path" help:"Output directory."`
	S101 string `name:"s101" type:"existingdir" help:"Emit from an external S-101 PortrayalCatalog directory instead of the build-time embedded catalogue."`
	CSS  string `name:"css" default:"daySvgStyle.css" help:"S-101 palette stylesheet (under Symbols/)."`
}

func (c emitAssetsCmd) Run() error {
	// Emit the client assets via the native libtile57 asset baker: c.S101 "" uses
	// libtile57's embedded S-101 catalogue, else an on-disk PortrayalCatalog dir.
	files, err := emitS101Assets(c.S101, c.Dir)
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
	fmt.Printf("libtile57 %s (engine commit %s, S-101 catalogue embedded)\n", tile57.Version(), engineCommit)
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
