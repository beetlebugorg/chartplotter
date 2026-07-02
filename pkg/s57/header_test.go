package s57

import (
	"os"
	"testing"

	"github.com/beetlebugorg/chartplotter/pkg/iso8211"
)

// TestReadHeader checks the cheap header-only reader against known values and,
// more importantly, against the authoritative full Parse: every header field it
// reports must match what a full parse derives for the same cell.
func TestReadHeader(t *testing.T) {
	const path = "../../testdata/US5MD1MC.000"
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	fsys := iso8211.MemFS{"/US5MD1MC.000": data}

	h, err := ReadHeaderFS(fsys, "/US5MD1MC.000")
	if err != nil {
		t.Fatal(err)
	}

	// This fixture encodes DSNM with the extension; downstream callers strip it via
	// cellStem. The header reader returns the field verbatim, as a full parse does.
	if h.DatasetName != "US5MD1MC.000" {
		t.Errorf("DatasetName = %q, want US5MD1MC.000", h.DatasetName)
	}
	if h.CompilationScale != 12000 {
		t.Errorf("CompilationScale = %d, want 12000", h.CompilationScale)
	}
	if h.ProducingAgency != 550 {
		t.Errorf("ProducingAgency = %d, want 550 (NOAA)", h.ProducingAgency)
	}

	// Cross-check every field against a full parse of the same cell.
	full, err := ParseFS(fsys, "/US5MD1MC.000")
	if err != nil {
		t.Fatal(err)
	}
	if h.DatasetName != full.DatasetName() {
		t.Errorf("DatasetName = %q, full parse = %q", h.DatasetName, full.DatasetName())
	}
	if h.Edition != full.Edition() {
		t.Errorf("Edition = %q, full parse = %q", h.Edition, full.Edition())
	}
	if h.UpdateNumber != full.UpdateNumber() {
		t.Errorf("UpdateNumber = %q, full parse = %q", h.UpdateNumber, full.UpdateNumber())
	}
	if h.IssueDate != full.IssueDate() {
		t.Errorf("IssueDate = %q, full parse = %q", h.IssueDate, full.IssueDate())
	}
	if h.ProducingAgency != full.ProducingAgency() {
		t.Errorf("ProducingAgency = %d, full parse = %d", h.ProducingAgency, full.ProducingAgency())
	}
	if h.CompilationScale != full.CompilationScale() {
		t.Errorf("CompilationScale = %d, full parse = %d", h.CompilationScale, full.CompilationScale())
	}
}
