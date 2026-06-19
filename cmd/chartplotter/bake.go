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
	fmt.Fprintf(os.Stderr, "baking %d cell(s)…\n", len(cells))

	b, ok, err := baker.BuildBaker(cells, func(name string, err error) {
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

// cellName is the bare cell name + .000 (path.Base form) used as the bake key.
func cellNameKey(p string) string {
	base := filepath.Base(p)
	if strings.EqualFold(filepath.Ext(base), ".000") {
		return base
	}
	return ""
}

// collectCells gathers each base .000 cell's bytes from the inputs (zip bundles,
// directories, or individual .000 files), keyed by cell filename. First wins on
// a duplicate cell name.
func collectCells(paths []string) (map[string][]byte, error) {
	cells := map[string][]byte{}
	add := func(name string, data []byte) {
		key := cellNameKey(name)
		if key == "" {
			return
		}
		if _, dup := cells[key]; dup {
			fmt.Fprintf(os.Stderr, "  dup cell %s — keeping first\n", key)
			return
		}
		cells[key] = data
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
				if cellNameKey(path) != "" {
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
		case cellNameKey(p) != "":
			b, e := os.ReadFile(p)
			if e != nil {
				return nil, e
			}
			add(p, b)
		default:
			fmt.Fprintf(os.Stderr, "  ignoring %s (not a .zip, dir, or .000)\n", p)
		}
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
		if cellNameKey(e.Name) == "" {
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
