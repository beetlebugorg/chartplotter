package server

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/beetlebugorg/chartplotter-go/internal/engine/baker"
)

// ProvisionResult is the outcome of a provision run (the JSON contract shared by
// the CLI and POST /api/provision).
type ProvisionResult struct {
	Cells      int
	Tiles      uint64
	Bytes      uint64
	W, S, E, N float64
}

// ProgressSink receives provision progress; nil-safe via the methods below.
type ProgressSink struct {
	download func(done, total int, cell string)
	imp      func(done, total int)
	logf     func(format string, a ...any)
}

func (p *ProgressSink) onDownload(done, total int, cell string) {
	if p != nil && p.download != nil {
		p.download(done, total, cell)
	}
}
func (p *ProgressSink) onImport(done, total int) {
	if p != nil && p.imp != nil {
		p.imp(done, total)
	}
}
func (p *ProgressSink) log(format string, a ...any) {
	if p != nil && p.logf != nil {
		p.logf(format, a...)
	}
}

// ClearCache removes the provisioned state from dir: the per-cell download
// cache (.cellcache-*.000), the baked archive (charts-user.pmtiles), its
// sidecar manifest (charts-user.json), and any stray spill. Returns how many
// files were removed. Used by `serve --clear-cache` for a clean slate.
func ClearCache(dir string) (int, error) {
	n := 0
	for _, pat := range []string{".cellcache-*.000", ".regioncache-*.zip"} {
		matches, err := filepath.Glob(filepath.Join(dir, pat))
		if err != nil {
			return 0, err
		}
		for _, m := range matches {
			if os.Remove(m) == nil {
				n++
			}
		}
	}
	for _, name := range []string{"charts-user.pmtiles", "charts-user.json", "charts-user.pmtiles.spill"} {
		if os.Remove(filepath.Join(dir, name)) == nil {
			n++
		}
	}
	return n, nil
}

// StdoutSink returns a ProgressSink that prints human progress lines to stdout
// (for the provision CLI).
func StdoutSink() *ProgressSink {
	return &ProgressSink{
		logf: func(format string, a ...any) { fmt.Printf(format+"\n", a...) },
	}
}

// catalogEntry is the subset of catalog.json a provision needs.
type catalogEntry struct {
	N string `json:"n"`
	Z string `json:"z"`
}

type catalogDoc struct {
	Cells []catalogEntry `json:"cells"`
}

// ProvisionCore resolves each named cell's NOAA zip URL from dir/catalog.json,
// downloads it (server-side — no browser CORS), caches the extracted base cell
// at dir/.cellcache-<CELL>.000, native-bakes them all into
// dir/charts-user.pmtiles, and writes the dir/charts-user.json sidecar. Port of
// main.zig provisionCore.
func ProvisionCore(dir string, names []string, p *ProgressSink) (ProvisionResult, error) {
	catBytes, err := os.ReadFile(filepath.Join(dir, "catalog.json"))
	if err != nil {
		return ProvisionResult{}, fmt.Errorf("read catalog.json: %w", err)
	}
	var cat catalogDoc
	if err := json.Unmarshal(catBytes, &cat); err != nil {
		return ProvisionResult{}, fmt.Errorf("parse catalog.json: %w", err)
	}
	urlOf := make(map[string]string, len(cat.Cells))
	for _, c := range cat.Cells {
		urlOf[c.N] = c.Z
	}

	client := &http.Client{Timeout: 120 * time.Second}
	cells := map[string][]byte{}
	var failed []string
	var mu sync.Mutex
	var done atomic.Int64
	total := len(names)

	// Download in parallel (NOAA serves many at once) with per-cell retry, so a
	// large region (hundreds of cells) completes quickly and a transient failure
	// doesn't silently drop a cell. Each cell caches to its own file, so the
	// concurrent writes don't conflict.
	const downloadWorkers = 8
	sem := make(chan struct{}, downloadWorkers)
	var wg sync.WaitGroup
	for _, name := range names {
		url := urlOf[name]
		if url == "" {
			mu.Lock()
			failed = append(failed, name)
			mu.Unlock()
			p.log("  ! %s: no download URL in catalog (skipped)", name)
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(name, url string) {
			defer wg.Done()
			defer func() { <-sem }()
			data, hit, err := loadCellCached(client, dir, name, url)
			n := done.Add(1)
			mu.Lock()
			if err != nil {
				failed = append(failed, name)
				p.log("  ! %s: %v (skipped)", name, err)
			} else {
				cells[name+".000"] = data
			}
			mu.Unlock()
			src := "downloaded"
			if hit {
				src = "cached"
			}
			p.log("%s %d/%d %s…", src, int(n), total, name)
			p.onDownload(int(n), total, name)
		}(name, url)
	}
	wg.Wait()
	if len(failed) > 0 {
		p.log("note: %d/%d cell(s) failed to download", len(failed), total)
	}
	if len(cells) == 0 {
		return ProvisionResult{}, fmt.Errorf("no cells downloaded (%d requested)", total)
	}

	b, ok, err := baker.BuildBaker(cells, func(n string, e error) { p.log("  ! %s: %v (skipped)", n, e) })
	if err != nil {
		return ProvisionResult{}, err
	}
	pb := baker.BakeToPMTiles(b, func(done, total int) { p.onImport(done, total) })

	out := filepath.Join(dir, "charts-user.pmtiles")
	f, err := os.Create(out)
	if err != nil {
		return ProvisionResult{}, err
	}
	if err := pb.WriteArchive(f); err != nil {
		f.Close()
		return ProvisionResult{}, err
	}
	f.Close()

	info, err := pmtilesInfo(out)
	if err != nil {
		return ProvisionResult{}, err
	}
	okNames := make([]string, len(ok))
	for i, n := range ok {
		okNames[i] = trimCellExt(n)
	}
	writeUserManifest(dir, okNames, nil, info)

	return ProvisionResult{
		Cells: len(ok), Tiles: info.tiles, Bytes: info.bytes,
		W: info.w, S: info.s, E: info.e, N: info.n,
	}, nil
}

// noaaENCBase is the NOAA ENC download root (per-cell and per-region zips live
// here). Server-side so the browser never has to fetch NOAA cross-origin.
const noaaENCBase = "https://www.charts.noaa.gov/ENCs/"

// ProvisionRegions provisions whole NOAA ENC regions by downloading each
// region's official bundle zip (`<NN>Region_ENCs.zip`) — ONE big download that
// is the authoritative, complete cell list for the region — extracting every
// base cell, and baking the union into dir/charts-user.pmtiles. The region zips
// are cached at dir/.regioncache-<NN>.zip so re-baking (add/remove a region)
// doesn't re-download. The manifest records the installed region numbers.
func ProvisionRegions(dir string, regions []int, p *ProgressSink) (ProvisionResult, error) {
	client := &http.Client{Timeout: 30 * time.Minute} // region zips are tens of MB
	cells := map[string][]byte{}
	total := len(regions)
	for i, num := range regions {
		zipName := fmt.Sprintf("%02dRegion_ENCs.zip", num)
		p.onDownload(i, total, zipName)
		data, hit, err := loadRegionZipCached(client, dir, num)
		if err != nil {
			p.log("  ! region %d: %v (skipped)", num, err)
			continue
		}
		n, err := extractBaseCells(data, cells)
		if err != nil {
			p.log("  ! region %d: extract: %v (skipped)", num, err)
			continue
		}
		src := "downloaded"
		if hit {
			src = "cached"
		}
		p.log("%s region %d: %d cells (%d/%d)", src, num, n, i+1, total)
		p.onDownload(i+1, total, zipName)
	}
	if len(cells) == 0 {
		return ProvisionResult{}, fmt.Errorf("no cells from %d region(s)", total)
	}

	b, ok, err := baker.BuildBaker(cells, func(n string, e error) { p.log("  ! %s: %v (skipped)", n, e) })
	if err != nil {
		return ProvisionResult{}, err
	}
	pb := baker.BakeToPMTiles(b, func(done, t int) { p.onImport(done, t) })

	out := filepath.Join(dir, "charts-user.pmtiles")
	f, err := os.Create(out)
	if err != nil {
		return ProvisionResult{}, err
	}
	if err := pb.WriteArchive(f); err != nil {
		f.Close()
		return ProvisionResult{}, err
	}
	f.Close()

	info, err := pmtilesInfo(out)
	if err != nil {
		return ProvisionResult{}, err
	}
	okNames := make([]string, len(ok))
	for i, n := range ok {
		okNames[i] = trimCellExt(n)
	}
	writeUserManifest(dir, okNames, regions, info)

	return ProvisionResult{
		Cells: len(ok), Tiles: info.tiles, Bytes: info.bytes,
		W: info.w, S: info.s, E: info.e, N: info.n,
	}, nil
}

// loadRegionZipCached returns a region bundle zip's bytes, cached at
// dir/.regioncache-<NN>.zip. Downloads with retry on transient failure.
func loadRegionZipCached(client *http.Client, dir string, num int) (data []byte, hit bool, err error) {
	cpath := filepath.Join(dir, fmt.Sprintf(".regioncache-%02d.zip", num))
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
	_ = os.WriteFile(cpath, data, 0o644)
	return data, false, nil
}

// httpGet fetches a URL and returns the body, erroring on non-200.
func httpGet(client *http.Client, url string) ([]byte, error) {
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// extractBaseCells reads every S-57 base cell (.000) from a zip into dst (keyed
// by "<CELL>.000"), returning how many were added. Update files are ignored.
func extractBaseCells(zipBytes []byte, dst map[string][]byte) (int, error) {
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		return 0, err
	}
	n := 0
	for _, zf := range zr.File {
		if !baker.IsBaseCell(zf.Name) {
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			continue
		}
		b, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			continue
		}
		dst[filepath.Base(zf.Name)] = b
		n++
	}
	return n, nil
}

// loadCellCached returns the base-cell bytes for name, cached at
// dir/.cellcache-<name>.000 so re-baking doesn't re-download. name is already
// validated (validCell), so it's a safe filename component.
func loadCellCached(client *http.Client, dir, name, url string) (data []byte, hit bool, err error) {
	cpath := filepath.Join(dir, ".cellcache-"+name+".000")
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
	// Cache best-effort; a write failure just means we re-fetch next time.
	_ = os.WriteFile(cpath, b, 0o644)
	return b, false, nil
}

// fetchCellBase downloads a NOAA ENC zip and returns the first base cell's bytes.
func fetchCellBase(client *http.Client, url string) ([]byte, error) {
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
		if !baker.IsBaseCell(zf.Name) {
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

// pmtInfo holds the PMTiles header fields the manifest needs.
type pmtInfo struct {
	tiles      uint64
	bytes      uint64
	minz, maxz uint8
	w, s, e, n float64
}

// pmtilesInfo reads a PMTiles archive's 127-byte header (tile count, zoom range,
// data bounds). Mirrors main.zig pmtilesInfo.
func pmtilesInfo(path string) (pmtInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return pmtInfo{}, err
	}
	defer f.Close()
	var h [127]byte
	if _, err := io.ReadFull(f, h[:]); err != nil {
		return pmtInfo{}, err
	}
	fi, err := f.Stat()
	if err != nil {
		return pmtInfo{}, err
	}
	e7 := func(off int) float64 {
		return float64(int32(binary.LittleEndian.Uint32(h[off:off+4]))) / 1e7
	}
	return pmtInfo{
		tiles: binary.LittleEndian.Uint64(h[80:88]),
		bytes: uint64(fi.Size()),
		minz:  h[100],
		maxz:  h[101],
		w:     e7(102),
		s:     e7(106),
		e:     e7(110),
		n:     e7(114),
	}, nil
}

// writeUserManifest writes dir/charts-user.json — the provisioned cell names,
// the installed NOAA region numbers (nil for a cell-list provision), and the
// data bounds — so the web app detects prebaked coverage + installed regions on
// boot. Best-effort.
func writeUserManifest(dir string, names []string, regions []int, info pmtInfo) {
	var b bytes.Buffer
	b.WriteString(`{"cells":[`)
	for i, n := range names {
		if i != 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%q", n)
	}
	b.WriteString(`],"regions":[`)
	for i, num := range regions {
		if i != 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%d", num)
	}
	fmt.Fprintf(&b, `],"bounds":[%.5f,%.5f,%.5f,%.5f]}`, info.w, info.s, info.e, info.n)
	_ = os.WriteFile(filepath.Join(dir, "charts-user.json"), b.Bytes(), 0o644)
}

func trimCellExt(name string) string {
	if len(name) > 4 && name[len(name)-4] == '.' {
		return name[:len(name)-4]
	}
	return name
}

// ResultJSON formats a provision result as the one-line JSON contract.
func (r ProvisionResult) ResultJSON() string {
	return fmt.Sprintf(`{"ok":true,"file":"charts-user.pmtiles","cells":%d,"tiles":%d,"bytes":%d,"bounds":[%.5f,%.5f,%.5f,%.5f]}`,
		r.Cells, r.Tiles, r.Bytes, r.W, r.S, r.E, r.N)
}
