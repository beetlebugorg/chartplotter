package bake

import (
	"os"
	"path/filepath"
	"testing"
)

// testS101Portrayer builds an S-101 portrayer for the bake tests. The S-52
// engine was removed, so AddCell now REQUIRES a portrayer; every test that
// bakes a cell installs this one via SetPortrayer.
//
// The catalogue locations come from S101_CATALOG / S101_FC (falling back to the
// developer's default checkout paths). If the portrayal catalogue isn't present
// the test is skipped rather than failed, since it's an external dependency.
func testS101Portrayer(tb testing.TB) Portrayer {
	tb.Helper()

	home, _ := os.UserHomeDir()
	pc := os.Getenv("S101_CATALOG")
	if pc == "" {
		pc = filepath.Join(home, "Projects", "s101-portrayal-catalogue", "PortrayalCatalog")
	}
	fc := os.Getenv("S101_FC")
	if fc == "" {
		fc = filepath.Join(home, "Projects", "s101-feature-catalogue", "S-101FC", "FeatureCatalogue.xml")
	}

	if _, err := os.Stat(filepath.Join(pc, "Rules", "main.lua")); err != nil {
		tb.Skip("S-101 catalogue not present; set S101_CATALOG/S101_FC")
		return nil
	}

	p, err := NewS101Portrayer(pc, fc)
	if err != nil {
		tb.Fatalf("build S-101 portrayer: %v", err)
	}
	return p
}
