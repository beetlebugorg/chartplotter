package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

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
// the STREAMING bundle driver — super-tile locality, geometry LRU, coarse
// riders, capped overscale fill-up, TILE57_SUPER_DZ / TILE57_LRU_BUDGET
// tuning, tiles streamed to disk instead of an in-memory whole-archive buffer.
// BakeBundle bakes into a temp dir beside -o; the finished tiles/chart.pmtiles
// then renames onto -o (same filesystem) and the bundle scaffolding (assets/
// styles/manifest.json) is discarded. Honors --max-zoom; writes --manifest
// (one band-less entry) + aux.zip (aux content collected from the input tree —
// zip inputs stage theirs beside the cells).
func (c bakeCmd) runTile57Archive() error {
	input, cleanup, err := c.tile57Input()
	if err != nil {
		return err
	}
	defer cleanup()

	outAbs, err := filepath.Abs(c.Out)
	if err != nil {
		return err
	}

	var n int
	var bbox [4]float64
	if os.Getenv("TILE57_LEGACY_BAKE") != "" {
		// Legacy in-bake combiner: BakeBundle into a temp dir beside -o, then rename the
		// finished tiles/chart.pmtiles onto -o and discard the bundle scaffolding.
		tmp, terr := os.MkdirTemp(filepath.Dir(outAbs), ".bake-*")
		if terr != nil {
			return terr
		}
		defer os.RemoveAll(tmp)
		n, bbox, err = bakeTile57Bundle(input, tmp, c.MaxZoom, c.Format, nil)
		if err != nil {
			return err
		}
		if err := os.Rename(filepath.Join(tmp, "tiles", "chart.pmtiles"), outAbs); err != nil {
			return err
		}
		st, _ := os.Stat(outAbs)
		fmt.Printf("baked %d cell(s) → %s (%.1f MB) via libtile57 (streamed)\n", n, c.Out, float64(st.Size())/(1<<20))
	} else {
		// Per-cell COMPOSITE (default): bake each cell at its native scale, then combine them
		// via the engine's ownership partition straight into -o. --max-zoom/--format do not
		// apply — each cell bakes at its native band and the compositor expands zoom.
		start := time.Now()
		n, err = baker.ComposeENCRoot(input, outAbs,
			func(done, total int, cell string) {
				if done >= total {
					fmt.Printf("\rcomposing %d cells…                 ", total)
				} else if done > 0 {
					per := time.Since(start) / time.Duration(done)
					fmt.Printf("\rbaking %s (%d/%d) · ~%s left      ", cell, done, total, (per * time.Duration(total-done)).Round(time.Second))
				} else {
					fmt.Printf("\rbaking %s (%d/%d)…      ", cell, done, total)
				}
			},
			nil, // CLI shows no separate compose bar; the per-cell line above suffices
			func(cell string, e error) { fmt.Fprintf(os.Stderr, "\nwarning: bake %s: %v (skipping)\n", cell, e) })
		fmt.Println()
		if err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("no coverage: no valid S-57 cells under %s", input)
		}
		st, _ := os.Stat(outAbs)
		fmt.Printf("composed %d cell(s) → %s (%.1f MB) via the per-cell ownership partition\n", n, c.Out, float64(st.Size())/(1<<20))
		// bbox for the manifest, read back from the composed archive.
		if src, e := tile57.OpenPMTiles(outAbs); e == nil {
			info := src.Info()
			bbox = [4]float64{info.West, info.South, info.East, info.North}
			src.Close()
		}
	}

	// Aux content walks the INPUT tree (for a lone .000, its directory).
	auxRoot := input
	if fi, e := os.Stat(input); e == nil && !fi.IsDir() {
		auxRoot = filepath.Dir(input)
	}
	ext := filepath.Ext(c.Out)
	stem := strings.TrimSuffix(c.Out, ext)
	auxManifest, err := writeAuxDir(stem, collectAuxDir(auxRoot))
	if err != nil {
		return err
	}

	if c.Manifest != "" {
		entry := map[string]any{"file": filepath.Base(c.Out), "bounds": bbox[:]}
		man := map[string]any{"districts": []map[string]any{entry}}
		if auxManifest != "" {
			man["aux"] = auxManifest
		}
		if err := writeManifestJSON(c.Manifest, man); err != nil {
			return err
		}
		fmt.Printf("wrote manifest %s\n", c.Manifest)
	}
	return nil
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
