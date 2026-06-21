package server

import (
	"os"
	"path/filepath"
)

// DefaultCacheDir is chartplotter's XDG cache root for REGENERABLE derived data —
// baked tile sets (NOAA/<district>/*.pmtiles + *.aux.zip): $XDG_CACHE_HOME/
// chartplotter, else ~/.cache/chartplotter. Safe to delete; rebuilt from the data dir.
func DefaultCacheDir() string {
	if x := os.Getenv("XDG_CACHE_HOME"); x != "" {
		return filepath.Join(x, "chartplotter")
	}
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		return filepath.Join(h, ".cache", "chartplotter")
	}
	return filepath.Join(os.TempDir(), "chartplotter-cache")
}

// DefaultDataDir is chartplotter's XDG DATA root for SOURCE data that must not be
// deleted without intention — the downloaded ENC source (district zips, raw cells):
// $XDG_DATA_HOME/chartplotter, else ~/.local/share/chartplotter. The cache dir
// (baked tiles) is regenerated from here.
func DefaultDataDir() string {
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		return filepath.Join(x, "chartplotter")
	}
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		return filepath.Join(h, ".local", "share", "chartplotter")
	}
	return filepath.Join(os.TempDir(), "chartplotter-data")
}
