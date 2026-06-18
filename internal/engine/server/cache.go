package server

import (
	"os"
	"path/filepath"
)

// DefaultCacheDir is chartplotter's XDG cache root for the downloaded raw cells
// (ENC_ROOT/): $XDG_CACHE_HOME/chartplotter, else ~/.cache/chartplotter.
func DefaultCacheDir() string {
	if x := os.Getenv("XDG_CACHE_HOME"); x != "" {
		return filepath.Join(x, "chartplotter")
	}
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		return filepath.Join(h, ".cache", "chartplotter")
	}
	return filepath.Join(os.TempDir(), "chartplotter-cache")
}
