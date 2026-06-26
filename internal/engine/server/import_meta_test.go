package server

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/beetlebugorg/chartplotter/internal/engine/baker"
)

// buildExchangeZip packs the committed real cell + CATALOG.031 fixtures into an
// in-memory ENC exchange-set zip, the shape an upload arrives as.
func buildExchangeZip(t *testing.T) []byte {
	t.Helper()
	cell, err := os.ReadFile("../../../testdata/US5MD1MC.000")
	if err != nil {
		t.Fatal(err)
	}
	cat, err := os.ReadFile("../../../pkg/s57/testdata/US5MD1MC_CATALOG.031")
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, data := range map[string][]byte{
		"ENC_ROOT/CATALOG.031":           cat,
		"ENC_ROOT/US5MD1MC/US5MD1MC.000": cell,
	} {
		f, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestImport_NoCatalog covers the common real-world case where an upload has NO
// CATALOG.031 (producers don't always include it): naming + full metadata still
// come from the cells' own headers — no dependency on any master index. The human
// title falls back to the cell's dataset name; the client resolves a nicer name
// where it can.
func TestImport_NoCatalog(t *testing.T) {
	s := New(t.TempDir(), t.TempDir(), t.TempDir(), false)
	cell, err := os.ReadFile("../../../testdata/US5MD1MC.000")
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	f, _ := zw.Create("ENC_ROOT/US5MD1MC/US5MD1MC.000") // cell only — no CATALOG.031
	if _, err := f.Write(cell); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	cells, _, cat, err := extractZipCells(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if cat != nil {
		t.Fatal("expected no catalogue (none in the zip)")
	}
	// Naming still works (cell-prefix fallback), and metadata comes from the header.
	if set := s.deriveUploadSet(cat, cells); set != "user-us5md1mc" {
		t.Errorf("deriveUploadSet = %q, want user-us5md1mc", set)
	}
	meta := buildSetMeta("user-us5md1mc", baker.ExtractCellMeta(cells, nil), cat)
	if meta.ScaleMin != 12000 || len(meta.BBox) != 4 || meta.Agency != "NOAA (US)" {
		t.Errorf("header metadata missing: scale=%d bbox=%v agency=%q", meta.ScaleMin, meta.BBox, meta.Agency)
	}
	// No catalogue → no human title (no master-index lookup); the cell Name carries
	// the identity, which the client shows.
	if len(meta.Cells) != 1 || meta.Cells[0].Name != "US5MD1MC" || meta.Cells[0].Title != "" {
		t.Errorf("per-cell = %+v; want Name US5MD1MC, empty Title", meta.Cells[0])
	}
}

// TestImport_AutoNameAndMeta exercises the upload metadata wiring (minus HTTP and
// the bake): extract → derive a CATALOG-identity pack name → extract per-cell
// metadata → write the sidecar → surface it on /api/packs and /api/pack/<name>.
// The bake itself needs the S-101 portrayer (-tags embed_s101) and is covered by
// the baker tests; this replicates the post-bake metadata tail of bakeAndRegister.
func TestImport_AutoNameAndMeta(t *testing.T) {
	cacheDir, dataDir := t.TempDir(), t.TempDir()
	s := New(t.TempDir(), cacheDir, dataDir, false)

	zipData := buildExchangeZip(t)
	cells, _, cat, err := extractZipCells(zipData)
	if err != nil {
		t.Fatal(err)
	}
	if cat == nil {
		t.Fatal("expected a parsed CATALOG.031 from the upload")
	}
	if len(cells) != 1 {
		t.Fatalf("cells = %d, want 1", len(cells))
	}

	// CATALOG identity → single cell → "user-us5md1mc".
	set := s.deriveUploadSet(cat, cells)
	if set != "user-us5md1mc" {
		t.Fatalf("deriveUploadSet = %q, want user-us5md1mc", set)
	}

	// The post-bake metadata tail (bakeAndRegister does exactly this after baking).
	cellMeta := baker.ExtractCellMeta(cells, nil)
	meta := buildSetMeta(set, cellMeta, cat)
	meta.Imported = "2026-06-25T00:00:00Z"
	if err := s.writeSetMeta(set, meta); err != nil {
		t.Fatal(err)
	}
	// Register a band-set so the district lists on /api/packs (a real bake does this
	// via packAdd; the empty path makes the bounds-open skip gracefully).
	s.packAdd(set+"-harbor", "")

	// The metadata sidecar carries the catalogue title + extracted header fields.
	m, ok := s.readSetMeta(set)
	if !ok {
		t.Fatal("no metadata sidecar written")
	}
	if m.Title != "Annapolis Harbor" {
		t.Errorf("Title = %q, want Annapolis Harbor", m.Title)
	}
	if m.Agency != "NOAA (US)" {
		t.Errorf("Agency = %q, want NOAA (US)", m.Agency)
	}
	if m.CellCount != 1 || m.ScaleMin != 12000 {
		t.Errorf("CellCount=%d ScaleMin=%d, want 1 / 12000", m.CellCount, m.ScaleMin)
	}
	if m.Imported == "" {
		t.Error("expected an import timestamp")
	}
	if len(m.Cells) != 1 || m.Cells[0].Title != "Annapolis Harbor" {
		t.Errorf("per-cell detail wrong: %+v", m.Cells)
	}

	// /api/packs lists the pack with its merged metadata.
	rec := httptest.NewRecorder()
	s.handlePacks(rec, httptest.NewRequest("GET", "/api/packs", nil))
	body := rec.Body.String()
	if !strings.Contains(body, `"name":"user-us5md1mc"`) || !strings.Contains(body, `"title":"Annapolis Harbor"`) {
		t.Errorf("/api/packs missing pack or title: %s", body)
	}

	// /api/pack/<name> returns the full detail incl. per-cell list.
	rec = httptest.NewRecorder()
	s.handlePackDetail(rec, httptest.NewRequest("GET", "/api/pack/"+set, nil))
	if rec.Code != 200 {
		t.Fatalf("pack detail status %d: %s", rec.Code, rec.Body.String())
	}
	var got SetMeta
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("pack detail JSON: %v", err)
	}
	if got.Set != set || len(got.Cells) != 1 {
		t.Errorf("pack detail = %+v", got)
	}
}
