package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/beetlebugorg/chartplotter/internal/engine/baker"
	tile57 "github.com/beetlebugorg/tile57/bindings/go"
)

// bakeTile57Bundle bakes an on-disk ENC input (a .000 cell or a directory of cells)
// into a self-contained chart bundle under outDir via the native libtile57 engine.
// maxZoom caps the highest baked zoom (0 = no cap). format selects the tile
// encoding ("mlt"/"mvt"; "" = the engine default, MLT). progress nil uses the
// lib's built-in console progress. Returns the cell count + bbox (w,s,e,n).
func bakeTile57Bundle(input, outDir string, maxZoom int, format string, progress func(tile57.BakeProgress)) (int, [4]float64, error) {
	// MaxZoom 24 = the ABI's "no clamp" (bake each cell's full native band); MaxZoom 0
	// would clamp every band down to z0 — an EMPTY archive. Only narrow on --max-zoom.
	opts := tile57.BakeOpts{MaxZoom: 24, Format: bakeFormat(format)}
	if maxZoom > 0 && maxZoom < 24 {
		opts.MaxZoom = uint8(maxZoom)
	}
	return tile57.BakeBundle(input, outDir, opts, progress)
}

// bakeFormat maps the --format flag to the engine's bake format. "" = the engine
// default (MLT); "mvt" keeps the legacy Mapbox Vector Tile output.
func bakeFormat(format string) tile57.TileFormat {
	switch format {
	case "mvt":
		return tile57.FormatMVT
	case "mlt":
		return tile57.FormatMLT
	}
	return tile57.FormatDefault
}

// runTile57Archive bakes the ENC inputs into ONE flat merged archive at -o via
// the native libtile57 engine. The coverage-clipped composite resolves
// best-available inside the archive (the finest covering cell owns each patch;
// each band also bakes FILLUP_DZ zooms past its window), so the per-band
// --bands split is retired: one archive per district, one client source.
// MinZoom 0 — the coarsest populated band extends down to the world view
// (extend_min), which also covers the retired --overzoom (a standalone
// large-scale set floats down automatically). Honors --max-zoom; writes
// --manifest (one band-less entry) + aux.zip.
func (c bakeCmd) runTile57Archive() error {
	cells, aux, err := collectCells(c.In)
	if err != nil {
		return err
	}
	if len(cells) == 0 {
		return fmt.Errorf("no .000 base cells found in: %s", strings.Join(c.In, ", "))
	}

	// Per-cell coverage bbox (cheap coverage-only parse) for the manifest bounds.
	metas := baker.ExtractCellMeta(cells, func(name string, err error) {
		fmt.Fprintf(os.Stderr, "  skip %s: %v\n", name, err)
	})

	all := make([]tile57.Cell, 0, len(cells))
	overall := emptyBBox()
	for name, cd := range cells { // name is "<stem>.000"
		stem := strings.TrimSuffix(name, filepath.Ext(name))
		all = append(all, tile57.Cell{
			Base:    cd.Base,
			Updates: orderedUpdates(cd.Updates),
			Name:    stem,
		})
		if m, ok := metas[stem]; ok && m.HasBBox {
			overall = unionBBox(overall, m.BBox)
		}
	}

	maxZ := uint8(0)
	if c.MaxZoom > 0 {
		maxZ = uint8(c.MaxZoom)
	}
	data, err := tile57.BakePmtiles(all, tile57.BakeOpts{MinZoom: 0, MaxZoom: maxZ, Format: bakeFormat(c.Format)}, nil)
	if err != nil {
		return err
	}
	if err := os.WriteFile(c.Out, data, 0o644); err != nil {
		return err
	}
	st, _ := os.Stat(c.Out)
	fmt.Printf("baked %d cell(s) → %s (%.1f MB) via libtile57\n", len(cells), c.Out, float64(st.Size())/(1<<20))

	ext := filepath.Ext(c.Out)
	stem := strings.TrimSuffix(c.Out, ext)
	auxFile, err := writeAuxZip(stem, aux)
	if err != nil {
		return err
	}

	if c.Manifest != "" {
		entry := map[string]any{"file": filepath.Base(c.Out)}
		if overall.set {
			entry["bounds"] = overall.slice()
		}
		man := map[string]any{"districts": []map[string]any{entry}}
		if auxFile != "" {
			man["aux"] = auxFile
		}
		if err := writeManifestJSON(c.Manifest, man); err != nil {
			return err
		}
		fmt.Printf("wrote manifest %s\n", c.Manifest)
	}
	return nil
}

// bbox4 is a running [west,south,east,north] union.
type bbox4 struct {
	minLon, minLat, maxLon, maxLat float64
	set                            bool
}

func emptyBBox() bbox4 { return bbox4{} }

func unionBBox(b bbox4, o [4]float64) bbox4 {
	if o[2] <= o[0] || o[3] <= o[1] { // degenerate
		return b
	}
	if !b.set {
		return bbox4{o[0], o[1], o[2], o[3], true}
	}
	if o[0] < b.minLon {
		b.minLon = o[0]
	}
	if o[1] < b.minLat {
		b.minLat = o[1]
	}
	if o[2] > b.maxLon {
		b.maxLon = o[2]
	}
	if o[3] > b.maxLat {
		b.maxLat = o[3]
	}
	return b
}

func (b bbox4) slice() [4]float64 {
	return [4]float64{b.minLon, b.minLat, b.maxLon, b.maxLat}
}

// orderedUpdates returns a cell's update bodies sorted by filename so libtile57
// applies them in sequence (.001, .002, …).
func orderedUpdates(m map[string][]byte) [][]byte {
	if len(m) == 0 {
		return nil
	}
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([][]byte, len(names))
	for i, n := range names {
		out[i] = m[n]
	}
	return out
}
