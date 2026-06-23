package baker_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/beetlebugorg/chartplotter/internal/engine/bake"
	"github.com/beetlebugorg/chartplotter/internal/engine/baker"
	"github.com/beetlebugorg/chartplotter/pkg/s52"
	"github.com/beetlebugorg/chartplotter/pkg/s52/preslib"
)

// TestS101BakeProducesTiles bakes a real NOAA cell through the S-101 portrayal
// engine (the cutover seam) and confirms tiles come out — the first real
// end-to-end S-101 render in the bake pipeline. Skips without the vendored
// catalogue. (lib is still loaded transitionally for the complex-linestyle
// period table + light characteristics; only the portrayal is S-101 here.)
func TestS101BakeProducesTiles(t *testing.T) {
	pc := os.Getenv("S101_CATALOG")
	if pc == "" {
		pc = "/home/jcollins/Projects/s101-portrayal-catalogue/PortrayalCatalog"
	}
	fcPath := os.Getenv("S101_FC")
	if fcPath == "" {
		fcPath = "/home/jcollins/Projects/s101-feature-catalogue/S-101FC/FeatureCatalogue.xml"
	}
	if _, err := os.Stat(filepath.Join(pc, "Rules", "main.lua")); err != nil {
		t.Skipf("S-101 catalogue not present; set S101_CATALOG/S101_FC")
	}
	if _, err := os.Stat(fcPath); err != nil {
		t.Skipf("S-101 feature catalogue not present")
	}

	portrayer, err := bake.NewS101Portrayer(pc, fcPath)
	if err != nil {
		t.Fatalf("build S-101 portrayer: %v", err)
	}

	data, err := os.ReadFile("../../../testdata/US4MD81M.000")
	if err != nil {
		t.Fatalf("read cell: %v", err)
	}
	chart, err := baker.ParseCellBytes("US4MD81M.000", data)
	if err != nil {
		t.Fatalf("parse cell: %v", err)
	}

	lib, err := s52.LoadLibraryFromBytes(preslib.DAI) // transitional: linestyle table + light char
	if err != nil {
		t.Fatal(err)
	}

	b := bake.New()
	b.SetPortrayer(portrayer) // <- S-101 symbology instead of S-52
	b.AddCell(chart, lib, s52.DefaultMarinerSettings())

	pb := baker.BakeToPMTiles(b, nil)
	if pb.Count() == 0 {
		t.Fatal("S-101 bake produced no tiles")
	}
	t.Logf("S-101 bake of US4MD81M produced %d tiles", pb.Count())
}
