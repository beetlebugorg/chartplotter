package main

import (
	"archive/zip"
	"fmt"
	"io"
	"os"

	"github.com/beetlebugorg/chartplotter-go/internal/engine/baker"
)

// bakeZipCmd extracts every S-57 base cell (.000) from a NOAA ENC zip and bakes
// them into one PMTiles archive. S-57 update files (.001, .002, …) are counted
// but not applied. Port of `chartplotter --bake-zip` (main.zig bakeZip).
type bakeZipCmd struct {
	Out string `arg:"" type:"path" help:"Output PMTiles archive."`
	Zip string `arg:"" type:"existingfile" help:"NOAA ENC zip (one or many cells)."`
}

func (c bakeZipCmd) Run() error {
	zr, err := zip.OpenReader(c.Zip)
	if err != nil {
		return err
	}
	defer zr.Close()

	cells := map[string][]byte{}
	updates := 0
	for _, f := range zr.File {
		if baker.IsUpdateCell(f.Name) {
			updates++
			continue
		}
		if !baker.IsBaseCell(f.Name) {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			fmt.Printf("  ! %s: %v (skipped)\n", f.Name, err)
			continue
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			fmt.Printf("  ! %s: %v (skipped)\n", f.Name, err)
			continue
		}
		cells[f.Name] = data
		if len(cells)%50 == 0 {
			fmt.Printf("  extracted %d cells…\n", len(cells))
		}
	}
	fmt.Printf("bake-zip: %d base cell(s) extracted, baking tiles…\n", len(cells))

	b, _, err := baker.BuildBaker(cells, func(name string, err error) {
		fmt.Printf("  ! %s: %v (skipped)\n", name, err)
	})
	if err != nil {
		return err
	}
	pb := baker.BakeToPMTiles(b, nil)
	fmt.Printf("baked %d tile(s)\n", pb.Count())

	f, err := os.Create(c.Out)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := pb.WriteArchive(f); err != nil {
		return fmt.Errorf("write archive: %w", err)
	}
	fmt.Printf("wrote %s\n", c.Out)
	if updates > 0 {
		fmt.Printf("note: %d update file(s) found (S-57 updates not yet applied)\n", updates)
	}
	return nil
}
