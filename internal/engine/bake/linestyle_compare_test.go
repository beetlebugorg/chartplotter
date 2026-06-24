package bake

import (
	"math"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/beetlebugorg/chartplotter/pkg/s100/catalog"
	"github.com/beetlebugorg/chartplotter/pkg/s52"
	"github.com/beetlebugorg/chartplotter/pkg/s52/preslib"
)

// TestLinestyleCatalogVsPresLib (diagnostic) compares the catalogue-derived
// complex-linestyle table against the PresLib one for the names they share, so we
// can tell whether switching the baker's source is byte-identical-safe.
func TestLinestyleCatalogVsPresLib(t *testing.T) {
	pc := os.Getenv("S101_CATALOG")
	if pc == "" {
		pc = filepath.Join(os.Getenv("HOME"), "Projects", "s101-portrayal-catalogue", "PortrayalCatalog")
	}
	if _, err := os.Stat(filepath.Join(pc, "LineStyles")); err != nil {
		t.Skip("S-101 catalogue not present")
	}
	cat, err := catalog.Load(pc)
	if err != nil {
		t.Fatal(err)
	}
	catTbl := buildLinestyleTableFromCatalog(cat)
	lib, err := s52.LoadLibraryFromBytes(preslib.DAI)
	if err != nil {
		t.Fatal(err)
	}
	preTbl := buildLinestyleTable(lib)

	var shared []string
	for name := range catTbl {
		if _, ok := preTbl[name]; ok {
			shared = append(shared, name)
		}
	}
	sort.Strings(shared)
	t.Logf("catalogue=%d preslib=%d shared=%d", len(catTbl), len(preTbl), len(shared))

	approx := func(a, b float64) bool { return math.Abs(a-b) < 0.5 }
	match, diff := 0, 0
	for _, name := range shared {
		c, p := catTbl[name], preTbl[name]
		ok := approx(c.periodPx, p.periodPx) && c.colorToken == p.colorToken &&
			len(c.onRuns) == len(p.onRuns) && len(c.symbols) == len(p.symbols)
		if ok {
			match++
			continue
		}
		diff++
		if diff <= 20 {
			t.Logf("DIFF %-10s period c=%.1f p=%.1f | color c=%q p=%q | onRuns c=%d p=%d | syms c=%d p=%d",
				name, c.periodPx, p.periodPx, c.colorToken, p.colorToken, len(c.onRuns), len(p.onRuns), len(c.symbols), len(p.symbols))
		}
	}
	t.Logf("shared: %d match, %d differ", match, diff)
}
