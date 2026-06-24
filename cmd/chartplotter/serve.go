package main

import (
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"

	"github.com/beetlebugorg/chartplotter/internal/engine/assets"
	"github.com/beetlebugorg/chartplotter/internal/engine/baker"
	"github.com/beetlebugorg/chartplotter/internal/engine/s101catalog"
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
}

func (c serveCmd) Run() error {
	// Portrayal is S-101. Pick the catalogue source: an explicit --s101 dir wins
	// (override / rule iteration); otherwise the build-time embedded catalogue (the
	// default — `make` builds it in). The baker defaults to the embedded portrayer
	// on its own (baker.applyPortrayer); here we emit the matching client assets
	// (colortables/sprite/patterns/linestyles) into a temp dir and serve them.
	var catalogFS fs.FS
	switch {
	case c.S101 != "":
		if c.S101FC == "" {
			return fmt.Errorf("--s101 requires --s101-fc")
		}
		if err := baker.UseS101Catalog(c.S101, c.S101FC); err != nil {
			return fmt.Errorf("load S-101 catalogue: %w", err)
		}
		catalogFS = os.DirFS(c.S101)
		fmt.Printf("portrayal: S-101 (catalogue=%s)\n", c.S101)
	case s101catalog.Available():
		fsys, err := s101catalog.PortrayalFS()
		if err != nil {
			return fmt.Errorf("embedded S-101 catalogue: %w", err)
		}
		catalogFS = fsys
		fmt.Println("portrayal: S-101 (embedded catalogue)")
	default:
		fmt.Println("portrayal: none embedded — pass --s101 or build with `make` (-tags embed_s101)")
	}
	if catalogFS != nil {
		assetDir, err := os.MkdirTemp("", "cp-s101-assets-")
		if err != nil {
			return err
		}
		if _, err := assets.EmitS101FS(catalogFS, "daySvgStyle.css", assetDir); err != nil {
			return fmt.Errorf("emit S-101 assets: %w", err)
		}
		c.Assets = assetDir // override colortables/linestyles/sprite; rest falls back to embedded
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
	srv.Version = version
	srv.ReportStaleCache() // loud warning if any served pack predates this binary

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
