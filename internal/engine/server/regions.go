package server

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/beetlebugorg/chartplotter-go/internal/engine/baker"
)

// DefaultCacheDir is chartplotter's XDG cache root for the per-region download
// zips + baked .pmtiles: $XDG_CACHE_HOME/chartplotter, else ~/.cache/chartplotter.
func DefaultCacheDir() string {
	if x := os.Getenv("XDG_CACHE_HOME"); x != "" {
		return filepath.Join(x, "chartplotter")
	}
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		return filepath.Join(h, ".cache", "chartplotter")
	}
	return filepath.Join(os.TempDir(), "chartplotter-cache")
}

// regionsDir holds the per-region cache: <NN>.zip (NOAA bundle) + <NN>.pmtiles
// (baked archive). One archive per NOAA region, so add/remove is per-file with
// no re-bake of the others.
func regionsDir(cacheDir string) string { return filepath.Join(cacheDir, "regions") }

func regionZipPath(cacheDir string, num int) string {
	return filepath.Join(regionsDir(cacheDir), fmt.Sprintf("%02d.zip", num))
}

func regionPMTilesName(num int) string { return fmt.Sprintf("%02d.pmtiles", num) }

func regionPMTilesPath(cacheDir string, num int) string {
	return filepath.Join(regionsDir(cacheDir), regionPMTilesName(num))
}

// regionNumFromPMTiles parses NN from "<NN>.pmtiles"; (0,false) if it doesn't match.
func regionNumFromPMTiles(name string) (int, bool) {
	if !strings.HasSuffix(name, ".pmtiles") {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSuffix(name, ".pmtiles"))
	if err != nil {
		return 0, false
	}
	return n, true
}

// ProvisionRegionToCache downloads region `num`'s NOAA bundle zip (cached at
// regions/<NN>.zip) and bakes JUST that region into regions/<NN>.pmtiles. A
// region already baked is a no-op. The bake is written to a temp file and
// renamed, so a partial/cancelled run never leaves a corrupt archive.
func ProvisionRegionToCache(cacheDir string, num int, p *ProgressSink) error {
	rdir := regionsDir(cacheDir)
	if err := os.MkdirAll(rdir, 0o755); err != nil {
		return err
	}
	out := regionPMTilesPath(cacheDir, num)
	if fi, err := os.Stat(out); err == nil && fi.Size() > 0 {
		return nil // already baked
	}

	client := &http.Client{Timeout: 30 * time.Minute} // region zips are tens of MB
	data, hit, err := loadRegionZip(client, cacheDir, num)
	if err != nil {
		return fmt.Errorf("region %d: %w", num, err)
	}
	cells := map[string][]byte{}
	if _, err := extractBaseCells(data, cells); err != nil {
		return fmt.Errorf("region %d extract: %w", num, err)
	}
	if len(cells) == 0 {
		return fmt.Errorf("region %d: no cells", num)
	}
	src := "downloaded"
	if hit {
		src = "cached"
	}
	p.log("%s region %d: %d cells", src, num, len(cells))

	b, _, err := baker.BuildBaker(cells, func(n string, e error) { p.log("  ! %s: %v (skipped)", n, e) })
	if err != nil {
		return err
	}
	pb := baker.BakeToPMTiles(b, func(done, total int) { p.onImport(done, total) })

	tmp := out + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if err := pb.WriteArchive(f); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return err
	}
	f.Close()
	return os.Rename(tmp, out)
}

// loadRegionZip returns a region bundle zip's bytes, cached at regions/<NN>.zip.
func loadRegionZip(client *http.Client, cacheDir string, num int) (data []byte, hit bool, err error) {
	cpath := regionZipPath(cacheDir, num)
	if b, e := os.ReadFile(cpath); e == nil && len(b) > 0 {
		return b, true, nil
	}
	url := fmt.Sprintf("%s%02dRegion_ENCs.zip", noaaENCBase, num)
	for attempt := 1; ; attempt++ {
		data, err = httpGet(client, url)
		if err == nil {
			break
		}
		if attempt >= 3 {
			return nil, false, err
		}
		time.Sleep(time.Duration(attempt) * time.Second)
	}
	if err := os.MkdirAll(regionsDir(cacheDir), 0o755); err == nil {
		_ = os.WriteFile(cpath, data, 0o644)
	}
	return data, false, nil
}

// DeleteRegion removes one region's baked archive (regions/<NN>.pmtiles). The
// cached zip is kept, so re-adding the region re-bakes without re-downloading.
func DeleteRegion(cacheDir string, num int) error {
	err := os.Remove(regionPMTilesPath(cacheDir, num))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// regionManifest lists every baked region archive in the cache with its bounds,
// the JSON the client loads to know which per-region pmtiles to render.
func regionManifest(cacheDir string) []byte {
	entries, _ := os.ReadDir(regionsDir(cacheDir))
	nums := make([]int, 0, len(entries))
	for _, e := range entries {
		if n, ok := regionNumFromPMTiles(e.Name()); ok {
			nums = append(nums, n)
		}
	}
	sort.Ints(nums)

	var b bytes.Buffer
	b.WriteString(`{"regions":[`)
	first := true
	for _, num := range nums {
		info, err := pmtilesInfo(regionPMTilesPath(cacheDir, num))
		if err != nil {
			continue
		}
		if !first {
			b.WriteByte(',')
		}
		first = false
		fmt.Fprintf(&b, `{"num":%d,"file":%q,"bounds":[%.5f,%.5f,%.5f,%.5f]}`,
			num, regionPMTilesName(num), info.w, info.s, info.e, info.n)
	}
	b.WriteString(`]}`)
	return b.Bytes()
}
