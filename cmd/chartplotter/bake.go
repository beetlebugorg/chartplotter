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

// bakeCmd bakes S-57 ENC base cells into a PMTiles archive (the same MVT tiles the
// in-browser wasm baker produces), for hosting a prebaked deployment. Updates
// (.001+) are NOT applied — cells are baked at their base .000 edition.
type bakeCmd struct {
	In       []string `arg:"" type:"path" help:"ENC inputs: .zip bundles, directories (scanned for *.000), and/or .000 files."`
	Out      string   `short:"o" type:"path" default:"charts.pmtiles" help:"Output PMTiles archive."`
	Manifest string   `help:"Also write a charts-index.json manifest (for the app's catalog=… option)."`
	BaseURL  string   `name:"base-url" help:"URL/prefix for the archive in the manifest (default: the archive's basename)."`
}

func (c bakeCmd) Run() error {
	cells, err := collectCells(c.In)
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

	b, ok, err := baker.BuildBakerWithUpdates(cells, func(name string, err error) {
		fmt.Fprintf(os.Stderr, "  skip %s: %v\n", name, err)
	})
	if err != nil {
		return err
	}
	if len(ok) == 0 {
		return fmt.Errorf("no cells parsed successfully")
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
		mf, err := os.Create(c.Manifest)
		if err != nil {
			return err
		}
		enc := json.NewEncoder(mf)
		enc.SetIndent("", "  ")
		if err := enc.Encode(man); err != nil {
			mf.Close()
			return err
		}
		mf.Close()
		fmt.Printf("wrote manifest %s (file=%s)\n", c.Manifest, file)
	}
	return nil
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
func collectCells(paths []string) (map[string]baker.CellData, error) {
	type acc struct {
		base    []byte
		updates map[string][]byte
	}
	byCell := map[string]*acc{} // keyed by cell stem (e.g. US4MD81M)
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
			return nil, err
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
				}
				return nil
			})
			if err != nil {
				return nil, err
			}
		case strings.EqualFold(filepath.Ext(p), ".zip"):
			if err := addZipCells(p, add); err != nil {
				return nil, err
			}
		case encExt(p) != "":
			b, e := os.ReadFile(p)
			if e != nil {
				return nil, e
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
	return cells, nil
}

func addZipCells(zipPath string, add func(name string, data []byte)) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, e := range zr.File {
		if encExt(e.Name) == "" {
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
		add(e.Name, data)
	}
	return nil
}
