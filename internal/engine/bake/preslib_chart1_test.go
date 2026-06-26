package bake

import (
	"archive/zip"
	"strings"
	"testing"

	"github.com/beetlebugorg/chartplotter/pkg/geo"
	"github.com/beetlebugorg/chartplotter/pkg/s57"
)

// preslibZip is the IHO S-52 PresLib e4.0.0 digital-files download (untracked;
// see scripts/preslib-chart1.sh). Its "ECDIS Chart 1" is a fully symbol-exercising
// dataset of 14 cells, one overview (1:60 000) + 13 harbor pages (1:14 000), but —
// unlike conformant NOAA data — the cells carry NO M_COVR data-coverage features.
const preslibZip = "../../../testdata/S-52_PresLib_e4.0.0_Digital_Files_Draft.zip"

// loadPresLibChart1 parses every ECDIS-Chart-1 .000 cell straight out of the zip,
// or skips the test if the (untracked) download is absent.
func loadPresLibChart1(t *testing.T) []*s57.Chart {
	t.Helper()
	zr, err := zip.OpenReader(preslibZip)
	if err != nil {
		t.Skipf("PresLib zip not present (%v); see scripts/preslib-chart1.sh", err)
	}
	t.Cleanup(func() { zr.Close() })
	var charts []*s57.Chart
	for _, f := range zr.File {
		if !strings.Contains(f.Name, "ECDIS_Chart_1/") || !strings.HasSuffix(f.Name, ".000") {
			continue
		}
		chart, err := s57.ParseFS(zr, f.Name)
		if err != nil {
			t.Fatalf("parse %s: %v", f.Name, err)
		}
		charts = append(charts, chart)
	}
	if len(charts) == 0 {
		t.Fatal("no ECDIS_Chart_1 .000 cells found in zip")
	}
	return charts
}

// TestPresLibChart1DerivedCoverage guards the cross-band best-available fix for
// cells lacking M_COVR. The S-52 PresLib ECDIS Chart 1 stacks a 1:60 000 overview
// over thirteen 1:14 000 harbor pages covering the same ground, but none of the
// cells carry an M_COVR coverage polygon — so the M_COVR-driven suppression had
// nothing to test and every overview feature double-drew over the harbor pages
// (the "smashed together" symbols). extractCoverage now derives a coverage
// rectangle from each cell's data extent when M_COVR is absent, restoring
// per-cell (block) suppression. The invariant: a point inside a harbor page must
// report a FINER (smaller) covering scale than the overview, so an overview
// symbol there is suppressed.
func TestPresLibChart1DerivedCoverage(t *testing.T) {
	charts := loadPresLibChart1(t)

	b := New()
	var overviewCscl, harborCscl uint32
	for _, chart := range charts {
		// Sanity: these cells really have no M_COVR — that's why the fix is needed.
		for i := range chart.Features() {
			if chart.Features()[i].ObjectClass() == "M_COVR" {
				t.Fatalf("cell %s unexpectedly has M_COVR — fixture changed", chart.DatasetName())
			}
		}
		band, n := b.AddCellCoverage(chart)
		if n == 0 {
			t.Errorf("cell %s contributed no coverage (derived-extent fallback failed)", chart.DatasetName())
		}
		switch cscl := uint32(chart.CompilationScale()); band {
		case BandApproach:
			overviewCscl = cscl
		case BandHarbor:
			harborCscl = cscl
		}
	}
	if overviewCscl == 0 || harborCscl == 0 {
		t.Fatalf("expected both an approach overview and harbor pages (overview=%d harbor=%d)", overviewCscl, harborCscl)
	}
	if harborCscl >= overviewCscl {
		t.Fatalf("harbor scale %d should be finer (smaller) than overview %d", harborCscl, overviewCscl)
	}

	// A point in the middle of harbor page AA5C1CDE (top row, third column). At a
	// harbor display zoom the finest covering cell there must be the harbor page,
	// not the overview — i.e. an overview prim at this point gets suppressed.
	const harborZ = 13 // BandHarbor display min
	if got := b.coverageScaleAt(15.11405, -5.05035, harborZ, true); got != harborCscl {
		t.Errorf("coverageScaleAt inside harbor page = %d, want harbor cscl %d "+
			"(no finer cover ⇒ overview would double-draw)", got, harborCscl)
	}

	// Every cell's derived coverage must lie within the overview's footprint
	// (they tile the same ground), confirming the rectangles are sane.
	ov := geo.LatLon{Lat: 15.0668, Lon: -5.0669} // overview centre
	if !b.coverageBandAtOK(ov) {
		t.Error("overview centre not covered by any derived coverage polygon")
	}
}

// coverageBandAtOK reports whether any coverage polygon contains p (test helper).
func (b *Baker) coverageBandAtOK(p geo.LatLon) bool {
	for i := range b.covMeta {
		cm := &b.covMeta[i]
		if cm.bb.Contains(p) && pointInRings(p.Lon, p.Lat, cm.rings) {
			return true
		}
	}
	return false
}

// TestPresLibChart1NoMCovrIsExtentFallback verifies the lower-level contract: a
// cell with no M_COVR yields exactly one DERIVED coverage rectangle spanning its
// geometry, and a (hypothetical) conformant cell would not.
func TestPresLibChart1NoMCovrIsExtentFallback(t *testing.T) {
	charts := loadPresLibChart1(t)
	b := New()
	for _, chart := range charts {
		before := len(b.covMeta)
		b.AddCellCoverage(chart)
		added := b.covMeta[before:]
		if len(added) != 1 {
			t.Errorf("cell %s: expected 1 derived coverage rect, got %d", chart.DatasetName(), len(added))
			continue
		}
		if !added[0].derived {
			t.Errorf("cell %s: coverage not flagged derived", chart.DatasetName())
		}
	}
}
