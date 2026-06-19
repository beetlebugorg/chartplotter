package main

import (
	"fmt"
	"net"
	"net/http"

	"github.com/beetlebugorg/chartplotter/internal/engine/server"
)

// serveCmd hosts the web frontend (embedded static assets + the wasm baker) and
// the /api/cell NOAA-download proxy. Everything else — parse, bake, render — runs
// in the browser, so this is just a static server + a thin CORS proxy.
type serveCmd struct {
	Host       string `default:"127.0.0.1" help:"Bind host."`
	Port       int    `default:"8080" help:"Bind port."`
	Assets     string `type:"existingdir" help:"Serve static assets from this directory instead of the built-in embedded bundle (for development)."`
	Cache      string `help:"Cache dir for per-region zips + baked .pmtiles (default: XDG cache)."`
	ClearCache bool   `name:"clear-cache" help:"On startup, delete the cached region zips + baked archives for a clean slate."`
}

func (c serveCmd) Run() error {
	cacheDir := c.Cache
	if cacheDir == "" {
		cacheDir = server.DefaultCacheDir()
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
	srv := server.New(c.Assets, cacheDir, allowRemote)
	srv.Version = version

	addr := net.JoinHostPort(c.Host, fmt.Sprintf("%d", c.Port))
	remoteNote := ""
	if allowRemote {
		remoteNote = ", remote OK"
	}
	assetsDesc := "embedded"
	if c.Assets != "" {
		assetsDesc = c.Assets
	}
	fmt.Printf("chartplotter → http://%s/  (assets=%s, cache=%s%s)\n", addr, assetsDesc, cacheDir, remoteNote)
	return http.ListenAndServe(addr, srv)
}
