package main

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/beetlebugorg/chartplotter/internal/engine/baker"
	"github.com/beetlebugorg/chartplotter/internal/engine/pmtiles"
)

// bakeCmd bakes S-57 ENC base cells into a PMTiles archive of MVT tiles, for
// hosting a prebaked deployment. Updates (.001+) are NOT applied — cells are
// baked at their base .000 edition.
type bakeCmd struct {
	In       []string `arg:"" type:"path" help:"ENC inputs: .zip bundles, directories (scanned for *.000 and *.zip), and/or .000 files."`
	Out      string   `short:"o" type:"path" default:"charts.pmtiles" help:"Output PMTiles archive."`
	Manifest string   `help:"Also write a charts-index.json manifest (for the app's catalog=… option)."`
	BaseURL  string   `name:"base-url" help:"URL/prefix for the archive in the manifest (default: the archive's basename)."`
	Overzoom bool     `help:"Overzoom all bands DOWN to the world view, so a standalone large-scale set (e.g. an IENC bundle with no overview cells) stays visible when zoomed out."`
	MaxZoom  int      `name:"max-zoom" help:"Cap the highest baked zoom (0 = each cell's native band max). Large-scale cells over a wide area (e.g. IENC at 1:5000) emit tens of millions of z17–18 tiles; cap the bake and let the client overzoom the vector tiles."`
	Bands    bool     `help:"Write one gap-clipped archive PER navigational band (<out>-<slug>.pmtiles) instead of one merged archive, so the client reproduces the realtime best-available display: each band's source client-overzooms its own data, coarser bands fill finer gaps, none bleed."`
	S101     string   `name:"s101" type:"existingdir" help:"Override the embedded catalogue with an external S-101 PortrayalCatalog directory (for iterating on rules). Requires --s101-fc."`
	S101FC   string   `name:"s101-fc" type:"existingfile" help:"S-101 FeatureCatalogue.xml path (with --s101)."`
	Tile57   bool     `name:"tile57" help:"(requires a -tags tile57 build) Bake with the native libtile57 engine into a self-contained chart BUNDLE (tiles/chart.pmtiles + assets/style-*.json + manifest.json) under -o (treated as a directory). Honors --max-zoom; --bands/--manifest/--overzoom don't apply (the bundle is zoom-banded per cell and self-describing)."`
}

func (c bakeCmd) Run() error {
	if c.S101 != "" {
		if c.S101FC == "" {
			return fmt.Errorf("--s101 requires --s101-fc")
		}
		if err := baker.UseS101Catalog(c.S101, c.S101FC); err != nil {
			return fmt.Errorf("load S-101 catalogue: %w", err)
		}
		fmt.Fprintln(os.Stderr, "portrayal: S-101 rule engine")
	}

	// Native libtile57 path (opt-in, -tags tile57). With --bands it writes one
	// gap-clipped PMTiles archive per navigational band (+ manifest) — parity with
	// the Go baker's per-band output, so the district/demo/widget workflows keep
	// working. Without --bands it writes a self-contained bundle directory. The
	// engine reads the ENC from disk, so both run BEFORE the Go baker's collectCells.
	if c.Tile57 {
		if c.Bands {
			return c.runTile57Bands()
		}
		return c.runTile57Bundle()
	}

	cells, aux, err := collectCells(c.In)
	if err != nil {
		return err
	}
	if len(cells) == 0 {
		return fmt.Errorf("no .000 base cells found in: %s", strings.Join(c.In, ", "))
	}
	nUpd := 0
	for _, cd := range cells {
		nUpd += len(cd.Updates)
	}
	fmt.Fprintf(os.Stderr, "baking %d cell(s) (%d update file(s) applied)…\n", len(cells), nUpd)

	// Per-band streaming holds only one band's geometry at a time, so it skips the
	// all-cells BuildBakerWithUpdates entirely.
	if c.Bands {
		return c.runBands(cells, aux)
	}

	b, ok, err := baker.BuildBakerWithUpdates(cells, c.Overzoom, func(name string, err error) {
		fmt.Fprintf(os.Stderr, "  skip %s: %v\n", name, err)
	})
	if err != nil {
		return err
	}
	if len(ok) == 0 {
		return fmt.Errorf("no cells parsed successfully")
	}
	if c.MaxZoom > 0 {
		b.MaxBakeZoom = uint32(c.MaxZoom)
	}

	lastPct := -1
	pb := baker.BakeToPMTiles(b, func(done, total int) {
		if total == 0 {
			return
		}
		if pct := done * 100 / total; pct != lastPct && pct%5 == 0 {
			lastPct = pct
			fmt.Fprintf(os.Stderr, "\r  tiles %d/%d (%d%%)", done, total, pct)
		}
	})
	fmt.Fprintln(os.Stderr)

	f, err := os.Create(c.Out)
	if err != nil {
		return err
	}
	if err := pb.WriteArchive(f); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	st, _ := os.Stat(c.Out)
	fmt.Printf("baked %d cell(s) → %s (%d tiles, %.1f MB)\n", len(ok), c.Out, pb.Count(), float64(st.Size())/(1<<20))

	stem := strings.TrimSuffix(c.Out, filepath.Ext(c.Out))
	auxFile, err := writeAuxZip(stem, aux)
	if err != nil {
		return err
	}

	if c.Manifest != "" {
		file := c.BaseURL
		if file == "" {
			file = filepath.Base(c.Out)
		}
		bb := b.Bounds()
		man := map[string]any{
			"districts": []map[string]any{{
				"file":   file,
				"band":   "all",
				"bounds": []float64{bb.MinLon, bb.MinLat, bb.MaxLon, bb.MaxLat},
			}},
		}
		if auxFile != "" {
			man["aux"] = auxFile
		}
		if err := writeManifestJSON(c.Manifest, man); err != nil {
			return err
		}
		fmt.Printf("wrote manifest %s (file=%s)\n", c.Manifest, file)
	}
	return nil
}

// runBands writes one gap-clipped PMTiles archive per navigational band
// (<out-stem>-<slug>.pmtiles) plus a manifest tagging each with its band slug, so
// the frontend loads each into its own chart-<slug> source.
func (c bakeCmd) runBands(cells map[string]baker.CellData, aux map[string][]byte) error {
	ext := filepath.Ext(c.Out)
	stem := strings.TrimSuffix(c.Out, ext)
	var entries []map[string]any
	lastPct := -1

	// Streaming: pass 1 derives coverage per cell; pass 2 re-parses + bakes one band
	// at a time, so only a single band's geometry + archive is ever resident.
	bb, nCells, err := baker.BakeToPMTilesBandsStreaming(cells, uint32(c.MaxZoom),
		func(name string, err error) {
			fmt.Fprintf(os.Stderr, "  skip %s: %v\n", name, err)
		},
		func(stage string, done, total int, band string) {
			if total == 0 {
				return
			}
			if pct := done * 100 / total; pct != lastPct && pct%5 == 0 {
				lastPct = pct
				where := band
				if where == "" {
					where = "coverage"
				}
				fmt.Fprintf(os.Stderr, "\r  %-9s %-8s %d/%d (%d%%)   ", where, stage, done, total, pct)
			}
		},
		func(slug string, pb *pmtiles.Builder) error {
			out := stem + "-" + slug + ext
			f, err := os.Create(out)
			if err != nil {
				return err
			}
			if err := pb.WriteArchive(f); err != nil {
				f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
			st, _ := os.Stat(out)
			fmt.Fprintf(os.Stderr, "\r")
			fmt.Printf("  %-9s → %s (%d tiles, %.1f MB)\n", slug, out, pb.Count(), float64(st.Size())/(1<<20))
			entries = append(entries, map[string]any{
				"file": filepath.Base(out),
				"band": slug,
			})
			return nil
		})
	if err != nil {
		return err
	}
	// District bounds (cell-union) are known only after both passes; stamp them
	// onto every band entry now.
	for _, e := range entries {
		e["bounds"] = []float64{bb.MinLon, bb.MinLat, bb.MaxLon, bb.MaxLat}
	}
	fmt.Printf("baked %d cell(s) → %d band archive(s)\n", nCells, len(entries))

	auxFile, err := writeAuxZip(stem, aux)
	if err != nil {
		return err
	}

	if c.Manifest != "" {
		man := map[string]any{"districts": entries}
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

// runTile57Bundle bakes the ENC inputs with the native libtile57 engine into a
// self-contained chart bundle (tiles/chart.pmtiles + per-scheme SCAMIN-bucketed
// style-*.json + assets + manifest.json) under the output directory. The engine
// reads the ENC from disk, so a lone directory or .000 is handed over directly and
// only zips / multiple inputs are staged into a temp directory of cells first.
func (c bakeCmd) runTile57Bundle() error {
	input, cleanup, err := c.tile57Input()
	if err != nil {
		return err
	}
	defer cleanup()

	outDir := bundleOutDir(c.Out)
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	// nil progress → the lib's built-in per-band console progress (good CLI output).
	n, bbox, err := bakeTile57Bundle(input, outDir, c.MaxZoom, nil)
	if err != nil {
		return err
	}
	fmt.Printf("baked %d cell(s) → %s/ via libtile57 — bundle: tiles/chart.pmtiles + assets/style-{day,dusk,night}.json + manifest.json (bbox %.4f,%.4f,%.4f,%.4f)\n",
		n, outDir, bbox[0], bbox[1], bbox[2], bbox[3])
	return nil
}

// tile57Input resolves the ENC inputs to a single on-disk path for the bundle
// baker. A lone existing directory or .000 file is used as-is (no cleanup). Zips,
// multiple inputs, or anything else are gathered with collectCells and written to a
// temp directory of cells (returned with a cleanup that removes it).
func (c bakeCmd) tile57Input() (path string, cleanup func(), err error) {
	noop := func() {}
	if len(c.In) == 1 {
		if fi, e := os.Stat(c.In[0]); e == nil && (fi.IsDir() || encExt(c.In[0]) == ".000") {
			return c.In[0], noop, nil
		}
	}
	cells, _, err := collectCells(c.In)
	if err != nil {
		return "", noop, err
	}
	if len(cells) == 0 {
		return "", noop, fmt.Errorf("no .000 base cells found in: %s", strings.Join(c.In, ", "))
	}
	dir, err := os.MkdirTemp("", "cp-tile57-enc-")
	if err != nil {
		return "", noop, err
	}
	for name, cd := range cells { // name is "<stem>.000"
		if err := os.WriteFile(filepath.Join(dir, name), cd.Base, 0o644); err != nil {
			os.RemoveAll(dir)
			return "", noop, err
		}
		for un, ub := range cd.Updates { // sequential .001+ alongside the base
			if err := os.WriteFile(filepath.Join(dir, filepath.Base(un)), ub, 0o644); err != nil {
				os.RemoveAll(dir)
				return "", noop, err
			}
		}
	}
	return dir, func() { os.RemoveAll(dir) }, nil
}

// writeManifestJSON writes a charts-index.json manifest (indented) to path.
func writeManifestJSON(path string, man map[string]any) error {
	mf, err := os.Create(path)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(mf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(man); err != nil {
		mf.Close()
		return err
	}
	return mf.Close()
}

// bundleOutDir derives the bundle output DIRECTORY from the -o value: a *.pmtiles /
// *.mbtiles path becomes its stem (charts.pmtiles → charts/), otherwise -o is used
// as the directory verbatim.
func bundleOutDir(out string) string {
	switch strings.ToLower(filepath.Ext(out)) {
	case ".pmtiles", ".mbtiles":
		return strings.TrimSuffix(out, filepath.Ext(out))
	default:
		return out
	}
}

// encExt reports the 3-digit S-57 cell extension (".000" base, ".001"+ updates)
// for a path, or "" if it isn't an ENC cell file.
func encExt(p string) string {
	ext := strings.ToLower(filepath.Ext(p))
	if len(ext) == 4 && ext[0] == '.' && ext[1] >= '0' && ext[1] <= '9' && ext[2] >= '0' && ext[2] <= '9' && ext[3] >= '0' && ext[3] <= '9' {
		return ext
	}
	return ""
}

// collectCells gathers each cell's base (.000) plus its update files (.001…) from
// the inputs (zip bundles, directories, and/or individual cell files), grouped by
// cell name. First base wins on a duplicate; updates accumulate.
func collectCells(paths []string) (map[string]baker.CellData, map[string][]byte, error) {
	type acc struct {
		base    []byte
		updates map[string][]byte
	}
	byCell := map[string]*acc{} // keyed by cell stem (e.g. US4MD81M)
	aux := map[string][]byte{}  // referenced aux files, keyed by auxKey (UPPER basename)
	addAux := func(name string, data []byte) {
		if !isAuxContent(name) {
			return
		}
		if k := auxKey(name); aux[k] == nil {
			aux[k] = data
		}
	}
	add := func(name string, data []byte) {
		ext := encExt(name)
		if ext == "" {
			return
		}
		base := filepath.Base(name)
		stem := strings.TrimSuffix(base, filepath.Ext(base))
		a := byCell[stem]
		if a == nil {
			a = &acc{updates: map[string][]byte{}}
			byCell[stem] = a
		}
		if ext == ".000" {
			if a.base != nil {
				fmt.Fprintf(os.Stderr, "  dup base %s — keeping first\n", base)
				return
			}
			a.base = data
		} else {
			a.updates[base] = data
		}
	}
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			return nil, nil, err
		}
		switch {
		case info.IsDir():
			err = filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
				if err != nil || d.IsDir() {
					return nil
				}
				if encExt(path) != "" {
					if b, e := os.ReadFile(path); e == nil {
						add(path, b)
					}
				} else if strings.EqualFold(filepath.Ext(path), ".zip") {
					// A directory of per-cell .zip bundles (e.g. an IENC download) — unpack
					// each in place rather than requiring the cells be extracted first.
					if e := addZipCells(path, add, addAux); e != nil {
						fmt.Fprintf(os.Stderr, "  skip zip %s: %v\n", path, e)
					}
				} else if isAuxContent(path) {
					if b, e := os.ReadFile(path); e == nil {
						addAux(path, b)
					}
				}
				return nil
			})
			if err != nil {
				return nil, nil, err
			}
		case strings.EqualFold(filepath.Ext(p), ".zip"):
			if err := addZipCells(p, add, addAux); err != nil {
				return nil, nil, err
			}
		case encExt(p) != "":
			b, e := os.ReadFile(p)
			if e != nil {
				return nil, nil, e
			}
			add(p, b)
		default:
			fmt.Fprintf(os.Stderr, "  ignoring %s (not a .zip, dir, or ENC cell)\n", p)
		}
	}
	cells := map[string]baker.CellData{}
	for stem, a := range byCell {
		if a.base == nil {
			fmt.Fprintf(os.Stderr, "  skip %s — update file(s) with no base .000\n", stem)
			continue
		}
		cells[stem+".000"] = baker.CellData{Base: a.base, Updates: a.updates}
	}
	return cells, aux, nil
}

func addZipCells(zipPath string, add, addAux func(name string, data []byte)) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, e := range zr.File {
		isCell := encExt(e.Name) != ""
		if !isCell && !isAuxContent(e.Name) {
			continue
		}
		rc, err := e.Open()
		if err != nil {
			return err
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return err
		}
		if isCell {
			add(e.Name, data)
		} else {
			addAux(e.Name, data)
		}
	}
	return nil
}
