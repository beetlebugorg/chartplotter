package parser

import (
	"path/filepath"
	"testing"

	"github.com/beetlebugorg/chartplotter/pkg/iso8211"
)

func TestAutoDiscoverUpdates(t *testing.T) {
	baseFile := "../../../testdata/US4MD81M.000"

	updates, err := findUpdateFiles(iso8211.OSFS(), baseFile)
	if err != nil {
		t.Fatal(err)
	}

	// Should find .001, .002, .003
	if len(updates) != 3 {
		t.Errorf("Expected 3 updates, got %d", len(updates))
	}

	// Verify they're in order
	expectedFiles := []string{
		filepath.Join(filepath.Dir(baseFile), "US4MD81M.001"),
		filepath.Join(filepath.Dir(baseFile), "US4MD81M.002"),
		filepath.Join(filepath.Dir(baseFile), "US4MD81M.003"),
	}

	for i, expected := range expectedFiles {
		if updates[i] != expected {
			t.Errorf("Update %d: expected %s, got %s", i, expected, updates[i])
		}
	}
}

func TestParseWithUpdates(t *testing.T) {
	parser := NewParser()

	// Parse with updates enabled (default)
	baseFile := "../../../testdata/US4MD81M.000"
	chart, err := parser.Parse(baseFile)
	if err != nil {
		t.Fatal(err)
	}

	// Verify update number reflects latest update
	if chart.UpdateNumber() != "3" {
		t.Errorf("Expected update number 3, got %s", chart.UpdateNumber())
	}

	// Verify we have features (basic sanity check)
	if len(chart.Features) == 0 {
		t.Error("Expected features in chart after applying updates")
	}

	t.Logf("Chart parsed successfully with %d features, update number: %s", len(chart.Features), chart.UpdateNumber())
}

func TestParseWithoutUpdates(t *testing.T) {
	parser := NewParser()

	baseFile := "../../../testdata/US4MD81M.000"

	opts := ParseOptions{
		ApplyUpdates:        false, // Disable update merging
		SkipUnknownFeatures: false,
		ValidateGeometry:    true,
		ObjectClassFilter:   nil,
	}

	chart, err := parser.ParseWithOptions(baseFile, opts)
	if err != nil {
		t.Fatal(err)
	}

	// Should have base cell data only
	if chart.UpdateNumber() != "0" {
		t.Errorf("Expected update number 0, got %s", chart.UpdateNumber())
	}

	t.Logf("Chart parsed successfully without updates, %d features, update number: %s", len(chart.Features), chart.UpdateNumber())
}

func TestUpdateInstructionConstants(t *testing.T) {
	// Verify the RUIN constants match S-57 spec
	if UpdateInsert != 1 {
		t.Errorf("UpdateInsert should be 1, got %d", UpdateInsert)
	}
	if UpdateDelete != 2 {
		t.Errorf("UpdateDelete should be 2, got %d", UpdateDelete)
	}
	if UpdateModify != 3 {
		t.Errorf("UpdateModify should be 3, got %d", UpdateModify)
	}
}
