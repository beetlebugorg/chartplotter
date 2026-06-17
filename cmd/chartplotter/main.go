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

	"github.com/beetlebugorg/chartplotter-go/internal/engine/assets"
	"github.com/beetlebugorg/chartplotter-go/pkg/s52"
	"github.com/beetlebugorg/chartplotter-go/pkg/s52/preslib"
)

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "dev"

type cli struct {
	Version     versionCmd     `cmd:"" help:"Print version and embedded-asset info."`
	EmitAssets  emitAssetsCmd  `cmd:"" name:"emit-assets" help:"Generate S-52 client assets (colortables.json, ...) into a directory."`
	EmitPmtiles emitPmtilesCmd `cmd:"" name:"emit-pmtiles" help:"Bake S-57 cells into a PMTiles archive."`
	BakeZip     bakeZipCmd     `cmd:"" name:"bake-zip" help:"Bake all base cells in a NOAA ENC zip into a PMTiles archive."`
	CatalogJSON catalogJSONCmd `cmd:"" name:"catalog-json" help:"Distil NOAA ENCProdCat.xml into a compact catalog.json."`
	Provision   provisionCmd   `cmd:"" name:"provision" help:"Download NOAA cells and bake charts-user.pmtiles."`
	Serve       serveCmd       `cmd:"" name:"serve" help:"Serve the web frontend + provisioning API."`
}

type emitAssetsCmd struct {
	Dir string `arg:"" type:"path" help:"Output directory."`
}

func (c emitAssetsCmd) Run() error {
	lib, err := s52.LoadLibraryFromBytes(preslib.DAI)
	if err != nil {
		return err
	}
	files, err := assets.Emit(lib, c.Dir)
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
