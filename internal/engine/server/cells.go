package server

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// isBaseCell reports whether a zip entry is an S-57 base cell (…/<CELL>.000).
func isBaseCell(name string) bool { return strings.HasSuffix(name, ".000") }

// ClearCache removes only the REGENERABLE baked tile sets under cacheDir (the
// per-district NOAA/, import/, and flat tiles/ trees). It deliberately leaves the
// SOURCE ENC (ENC_ROOT/, in the data dir) untouched — clearing the cache must not
// delete downloaded charts; they rebake from source. Returns how many trees removed.
func ClearCache(cacheDir string) (int, error) {
	n := 0
	for _, sub := range []string{"NOAA", "import", "tiles"} {
		p := filepath.Join(cacheDir, sub)
		if _, err := os.Stat(p); err == nil {
			if os.RemoveAll(p) == nil {
				n++
			}
		}
	}
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

// loadCellCached returns the base-cell bytes for name, cached in NOAA's
// ALL_ENCs.zip layout at dir/ENC_ROOT/<CELL>/<CELL>.000 so re-baking doesn't
// re-download — and so an ALL_ENCs.zip the user extracted into the cache dir is
// recognised as-is (a standard, interoperable ENC root). name is already
// validated (validCell), so it's a safe path component.
func loadCellCached(client *http.Client, dir, name, url string) (data []byte, hit bool, err error) {
	cpath := filepath.Join(dir, "ENC_ROOT", name, name+".000")
	if b, e := os.ReadFile(cpath); e == nil {
		return b, true, nil
	}
	// Also honour a flat cache location (dir/.cellcache-<name>.000), so cells
	// already on disk there are reused instead of re-downloaded.
	if b, e := os.ReadFile(filepath.Join(dir, ".cellcache-"+name+".000")); e == nil {
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
	// Cache best-effort (ENC_ROOT/<CELL>/<CELL>.000); a write failure just means
	// we re-fetch next time.
	if e := os.MkdirAll(filepath.Dir(cpath), 0o755); e == nil {
		_ = os.WriteFile(cpath, b, 0o644)
	}
	return b, false, nil
}
