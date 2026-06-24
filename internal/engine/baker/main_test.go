package baker

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestMain installs the S-101 portrayer for the package's bake tests. The baker
// requires a portrayer, and untagged `go test` builds have no embedded
// catalogue — so load the external dev catalogue if present, else skip the whole
// package (e.g. CI without it).
func TestMain(m *testing.M) {
	pc := os.Getenv("S101_CATALOG")
	if pc == "" {
		pc = filepath.Join(os.Getenv("HOME"), "Projects", "s101-portrayal-catalogue", "PortrayalCatalog")
	}
	fc := os.Getenv("S101_FC")
	if fc == "" {
		fc = filepath.Join(os.Getenv("HOME"), "Projects", "s101-feature-catalogue", "S-101FC", "FeatureCatalogue.xml")
	}
	if _, err := os.Stat(filepath.Join(pc, "Rules", "main.lua")); err != nil {
		fmt.Println("baker tests: S-101 catalogue not present; skipping (set S101_CATALOG/S101_FC)")
		return
	}
	if err := UseS101Catalog(pc, fc); err != nil {
		fmt.Println("baker tests: S-101 catalogue load failed:", err)
		return
	}
	os.Exit(m.Run())
}
