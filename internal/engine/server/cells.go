package server

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// isBaseCell reports whether a zip entry is an S-57 base cell (…/<CELL>.000).
func isBaseCell(name string) bool { return strings.HasSuffix(name, ".000") }

// ClearCache removes only the REGENERABLE baked bundle parts under cacheDir — each
// provider's tiles/ and assets/ dirs — while sparing every provider's ENC_ROOT source
// tree (the downloaded SOURCE ENC) and any loose cells/ dir. In single-dir mode
// (dataDir == cacheDir) the ENC_ROOT sits under the SAME tree as the bundle, so a
// blanket RemoveAll of the provider trees would delete downloaded charts. Clearing the
// cache must not do that — providers rebake from their kept ENC_ROOT. Returns how many
// regenerable dirs were removed.
func ClearCache(cacheDir string) (int, error) {
	n := 0
	_ = filepath.WalkDir(cacheDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		switch d.Name() {
		case "ENC_ROOT", "cells":
			return filepath.SkipDir // NEVER remove or descend into source cells
		case "tiles", "assets":
			if os.RemoveAll(path) == nil {
				n++
			}
			return filepath.SkipDir
		}
		return nil
	})
	return n, nil
}

// fetchCellBase downloads a NOAA ENC zip and returns the first base cell's bytes.
func fetchCellBase(client *http.Client, url string) ([]byte, error) {
	if url == "" {
		return nil, fmt.Errorf("cell not cached and no download url given")
	}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http %d", resp.StatusCode)
	}
	zipBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		return nil, err
	}
	for _, zf := range zr.File {
		if !isBaseCell(zf.Name) {
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		return io.ReadAll(rc)
	}
	return nil, fmt.Errorf("no base cell in zip")
}

// loadCellCached returns the base-cell bytes for name, cached FLAT at
// destDir/<name>.000 so re-baking doesn't re-download. destDir is the target cells/
// dir — a pack's cells/ for a district fetch, or the loose-cell dir for the /api/cell
// download proxy. name is already validated (validCell), so it's a safe path component.
func loadCellCached(client *http.Client, destDir, name, url string) (data []byte, hit bool, err error) {
	cpath := filepath.Join(destDir, name+".000")
	if b, e := os.ReadFile(cpath); e == nil {
		return b, true, nil
	}
	// Retry transient download failures (NOAA occasionally 5xx / resets under
	// load) so a single hiccup doesn't drop a cell from the region.
	var b []byte
	for attempt := 1; ; attempt++ {
		b, err = fetchCellBase(client, url)
		if err == nil {
			break
		}
		if attempt >= 3 {
			return nil, false, err
		}
		time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)
	}
	// Cache best-effort (destDir/<name>.000); a write failure just means we re-fetch.
	if e := os.MkdirAll(destDir, 0o755); e == nil {
		_ = os.WriteFile(cpath, b, 0o644)
	}
	return b, false, nil
}
