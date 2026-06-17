package main

import (
	"fmt"
	"os"

	"github.com/beetlebugorg/chartplotter-go/internal/engine/bake"
	"github.com/beetlebugorg/chartplotter-go/internal/engine/baker"
	"github.com/beetlebugorg/chartplotter-go/pkg/s52"
	"github.com/beetlebugorg/chartplotter-go/pkg/s52/preslib"
	"github.com/beetlebugorg/chartplotter-go/pkg/s57"
)

// emitPmtilesCmd bakes one or more S-57 base cells (.000) into a single static
// PMTiles v3 archive. Port of `chartplotter --emit-pmtiles` (main.zig).
type emitPmtilesCmd struct {
	Out   string   `arg:"" type:"path" help:"Output PMTiles archive."`
	Cells []string `arg:"" type:"existingfile" name:"cell" help:"S-57 base cell(s) (CELL.000)."`
}

func (c emitPmtilesCmd) Run() error {
	lib, err := s52.LoadLibraryFromBytes(preslib.DAI)
	if err != nil {
		return fmt.Errorf("load PresLib: %w", err)
	}
	mariner := s52.DefaultMarinerSettings()

	b := bake.New()
	for _, path := range c.Cells {
		chart, err := s57.Parse(path)
		if err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		b.AddCell(chart, lib, mariner)
		fmt.Printf("  added %s (%d features)\n", path, chart.FeatureCount())
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
	return nil
}
