package main

import (
	"fmt"
	"net"
	"net/http"
	"os"

	"github.com/beetlebugorg/chartplotter/internal/engine/server"
)

// serveCmd hosts the web frontend (embedded static assets) plus the server-side
// S-101 baking + tile-serving API: chart imports are parsed and baked into tiles
// in the backend (the browser only renders pre-baked tiles), alongside the
// /api/cell NOAA-download proxy.
type serveCmd struct {
	Host       string `default:"127.0.0.1" help:"Bind host."`
	Port       int    `default:"8080" help:"Bind port."`
	Assets     string `type:"existingdir" help:"Serve static assets from this directory instead of the built-in embedded bundle (for development)."`
	Cache      string `help:"Cache dir for REGENERABLE baked .pmtiles tile sets (default: XDG cache)."`
	Data       string `help:"Data dir for SOURCE ENC (district zips, raw cells) — safe, not auto-deleted (default: XDG data)."`
	ClearCache bool   `name:"clear-cache" help:"On startup, delete the cached baked archives for a clean slate (source ENC is kept)."`
	S101       string `name:"s101" type:"existingdir" help:"Override the embedded catalogue with an external S-101 PortrayalCatalog directory (for iterating on rules). Every chart baked by the server (chart library imports) uses this catalogue's symbology, and the matching client assets are served. Requires --s101-fc."`
	S101FC     string `name:"s101-fc" type:"existingfile" help:"S-101 FeatureCatalogue.xml path (with --s101)."`
	Tile57     string `name:"tile57" type:"path" help:"(requires a -tags tile57 build) Serve a LIVE libtile57-backed tile set from this ENC_ROOT / .zip / .000, generating MVT on demand from the cells instead of prebaking. Registered as the 'tile57' set (point a client at /tiles/tile57.json)."`
	Tile57Bake bool   `name:"tile57-bake" help:"(requires a -tags tile57 build) DEFAULT server imports to the native libtile57 bundle baker (tiles + SCAMIN-bucketed styles + assets) instead of the Go per-band baker. The Advanced→\"Bake engine\" UI setting overrides this per deployment; this flag just sets the headless default."`
}

func (c serveCmd) Run() error {
	// Portrayal is S-101. Emit the client assets (colortables/linestyles/sprite/
	// patterns) via libtile57's asset baker and serve them as a fallback: an explicit
	// --s101 PortrayalCatalog dir overrides libtile57's embedded catalogue.
	catalogDir := c.S101 // "" = libtile57's embedded catalogue
	assetDir, err := os.MkdirTemp("", "cp-s101-assets-")
	if err != nil {
		return err
	}
	if _, err := emitS101Assets(catalogDir, assetDir); err != nil {
		return fmt.Errorf("emit S-101 assets: %w", err)
	}
	// The emitted assets are a FALLBACK, not a replacement: an explicit --assets dir
	// stays primary (a prebaked widget bundle serves its own index.html /
	// charts-index.json / .pmtiles), this temp dir fills in the generated S-101 files
	// it lacks, and the embedded bundle backs the rest. Registered on the Server below.
	s101AssetDir := assetDir
	if catalogDir != "" {
		fmt.Printf("portrayal: S-101 (catalogue=%s)\n", catalogDir)
	} else {
		fmt.Println("portrayal: S-101 (libtile57 embedded catalogue)")
	}

	cacheDir := c.Cache
	if cacheDir == "" {
		cacheDir = server.DefaultCacheDir()
	}
	dataDir := c.Data
	if dataDir == "" {
		dataDir = server.DefaultDataDir()
	}

	if c.ClearCache {
		n, err := server.ClearCache(cacheDir)
		if err != nil {
			return fmt.Errorf("clear cache: %w", err)
		}
		fmt.Printf("cleared %d cached file(s) from %s\n", n, cacheDir)
	}

	// Loopback bind → enforce the Host-header DNS-rebind check on /api. Any
	// other bind means the operator opted into network exposure.
	allowRemote := !(c.Host == "127.0.0.1" || c.Host == "localhost" || c.Host == "::1")
	srv := server.New(c.Assets, cacheDir, dataDir, allowRemote)
	srv.SetAssetFallback(s101AssetDir) // emitted S-101 assets, searched after --assets, before embedded
	srv.Version = version
	srv.ReportStaleCache() // loud warning if any served pack predates this binary

	// Optional libtile57 live backend (-tags tile57): generate MVT on demand from
	// raw ENC cells instead of serving a prebaked archive.
	if c.Tile57 != "" {
		if err := registerTile57Set(srv, "tile57", c.Tile57, c.S101); err != nil {
			return err
		}
	} else if tile57Available {
		fmt.Println("tile57: libtile57 backend available — pass --tile57 <ENC_ROOT> for a live set")
	}
	if c.Tile57Bake {
		if !tile57Available {
			return fmt.Errorf("--tile57-bake requires a binary built with -tags tile57 (run `make build-tile57`)")
		}
		srv.BakeEngine = "tile57"
		fmt.Println("tile57: server chart imports will bake native libtile57 bundles")
	}

	addr := net.JoinHostPort(c.Host, fmt.Sprintf("%d", c.Port))
	remoteNote := ""
	if allowRemote {
		remoteNote = ", remote OK"
	}
	assetsDesc := "embedded"
	if c.Assets != "" {
		assetsDesc = c.Assets
	}
	fmt.Printf("chartplotter → http://%s/  (assets=%s, cache=%s, data=%s%s)\n", addr, assetsDesc, cacheDir, dataDir, remoteNote)
	return http.ListenAndServe(addr, srv)
}
