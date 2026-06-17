package main

import (
	"fmt"
	"net"
	"net/http"

	"github.com/beetlebugorg/chartplotter/internal/engine/server"
)

// provisionCmd downloads the named NOAA cells (URLs from DIR/catalog.json) and
// native-bakes them into DIR/charts-user.pmtiles + charts-user.json. Port of
// `chartplotter --provision`.
type provisionCmd struct {
	Dir   string   `arg:"" type:"existingdir" help:"Working dir (must contain catalog.json)."`
	Cells []string `arg:"" name:"cell" help:"NOAA cell name(s), e.g. US5MD1MC."`
}

func (c provisionCmd) Run() error {
	r, err := server.ProvisionCore(c.Dir, c.Cells, server.StdoutSink())
	if err != nil {
		fmt.Printf(`{"ok":false,"error":%q}`+"\n", err.Error())
		return err
	}
	fmt.Println(r.ResultJSON())
	return nil
}

// serveCmd hosts the web frontend (static files + Range) and the /api
// onboarding surface. Port of `chartplotter --serve`.
type serveCmd struct {
	Host       string `default:"127.0.0.1" help:"Bind host."`
	Port       int    `default:"8080" help:"Bind port."`
	Assets     string `default:"web" type:"existingdir" help:"Directory of static assets to serve."`
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

	addr := net.JoinHostPort(c.Host, fmt.Sprintf("%d", c.Port))
	remoteNote := ""
	if allowRemote {
		remoteNote = ", remote OK"
	}
	fmt.Printf("chartplotter → http://%s/  (assets=%s, cache=%s%s)\n", addr, c.Assets, cacheDir, remoteNote)
	return http.ListenAndServe(addr, srv)
}
