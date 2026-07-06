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
)

// bakeCmd bakes S-57 ENC base cells into a PMTiles archive of MVT tiles, for
// hosting a prebaked deployment. Updates (.001+) are NOT applied — cells are
// baked at their base .000 edition.
type bakeCmd struct {
	In       []string `arg:"" type:"path" help:"ENC inputs: .zip bundles, directories (scanned for *.000 and *.zip), and/or .000 files."`
	Out      string   `short:"o" type:"path" default:"charts.pmtiles" help:"Output PMTiles archive."`
	Manifest string   `help:"Also write a charts-index.json manifest (for the app's catalog=… option)."`
	BaseURL  string   `name:"base-url" help:"URL/prefix for the archive in the manifest (default: the archive's basename)."`
	MaxZoom  int      `name:"max-zoom" help:"Cap the highest baked zoom (0 = each cell's native band max). Large-scale cells over a wide area (e.g. IENC at 1:5000) emit tens of millions of z17–18 tiles; cap the bake and let the client overzoom the vector tiles."`
	Format   string   `enum:"mlt,mvt," default:"" help:"Tile encoding: mlt (MapLibre Tile, the engine default) or mvt (Mapbox Vector Tile, for consumers without an MLT decoder). Empty = the engine default (mlt)."`
	S101     string   `name:"s101" type:"existingdir" help:"Override the embedded catalogue with an external S-101 PortrayalCatalog directory (for iterating on rules). Requires --s101-fc."`
	S101FC   string   `name:"s101-fc" type:"existingfile" help:"S-101 FeatureCatalogue.xml path (with --s101)."`
}

func (c bakeCmd) Run() error {
	// libtile57 is the sole bake engine. A *.pmtiles/-mbtiles -o writes ONE flat
	// merged archive (+ optional --manifest / aux.zip) — the coverage-clipped
	// composite resolves best-available inside the single archive, so there are
	// no per-band archives any more (the retired --bands). Any other -o is a
	// self-contained bundle directory (tiles/chart.pmtiles + per-scheme style +
	// assets + manifest.json).
	switch strings.ToLower(filepath.Ext(c.Out)) {
	case ".pmtiles", ".mbtiles":
		return c.runTile57Archive()
	}
	return c.runTile57Bundle()
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
	n, bbox, err := bakeTile57Bundle(input, outDir, c.MaxZoom, c.Format, nil)
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
		if fi, e := os.Stat(c.In[0]); e == nil {
			// A lone .000, or a directory that ALREADY holds extracted .000 cells
			// (a real ENC_ROOT), is handed to the engine as-is. A directory of only
			// exchange-set .zip bundles (the demo cache, an IENC download) has no
			// .000 for the engine to read — it must be unpacked via collectCells
			// below, else the bake covers nothing.
			if encExt(c.In[0]) == ".000" || (fi.IsDir() && dirHasBaseCell(c.In[0])) {
				return c.In[0], noop, nil
			}
		}
	}
	cells, aux, err := collectCells(c.In)
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
	// Aux content (TXTDSC/PICREP text + pictures) rides beside the cells, exactly
	// like a real ENC_ROOT — so the flat-archive path's aux.zip walk finds it for
	// zip inputs too.
	for name, data := range aux {
		if err := os.WriteFile(filepath.Join(dir, filepath.Base(name)), data, 0o644); err != nil {
			os.RemoveAll(dir)
			return "", noop, err
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

// dirHasBaseCell reports whether dir already contains at least one extracted .000
// base cell — i.e. it is a bakeable ENC_ROOT the engine can read directly, not a
// directory of .zip exchange-set bundles (which must be unpacked via collectCells
// first). Stops at the first hit.
func dirHasBaseCell(dir string) bool {
	found := false
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if encExt(path) == ".000" {
			found = true
			return fs.SkipAll
		}
		return nil
	})
	return found
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
