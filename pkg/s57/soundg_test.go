package s57_test

import (
	"testing"

	s57 "github.com/beetlebugorg/chartplotter/pkg/s57"
)

func TestSOUNDGWith3DCoordinates(t *testing.T) {
	// Parse test chart from test/ directory
	chartPath := "../../testdata/US4MD81M.000"

	chart, err := s57.ParseWithOptions(chartPath, s57.DefaultParseOptions())
	if err != nil {
		t.Fatalf("Failed to parse chart: %v", err)
	}

	// Find all SOUNDG features
	features := chart.Features()
	var soundings []s57.Feature
	for _, f := range features {
		if f.ObjectClass() == "SOUNDG" {
			soundings = append(soundings, f)
		}
	}

	if len(soundings) == 0 {
		t.Skip("No SOUNDG features found in test chart")
	}

	t.Logf("Found %d SOUNDG feature(s)", len(soundings))

	// Check first SOUNDG feature to verify 3D coordinates
	soundg := soundings[0]
	geom := soundg.Geometry()

	if geom.Type != s57.GeometryTypePoint {
		t.Errorf("Expected SOUNDG geometry type Point, got %v", geom.Type)
	}

	if len(geom.Coordinates) == 0 {
		t.Fatal("SOUNDG has no coordinates")
	}

	// Verify 3D coordinates (lon, lat, depth)
	foundWith3D := 0
	foundWithout3D := 0

	for _, coord := range geom.Coordinates {
		if len(coord) < 2 {
			t.Errorf("Coordinate has less than 2 values: %v", coord)
			continue
		}

		if len(coord) >= 3 {
			foundWith3D++
		} else {
			foundWithout3D++
		}
	}

	t.Logf("First SOUNDG feature has %d coordinates: %d with depth (3D), %d without depth (2D)",
		len(geom.Coordinates), foundWith3D, foundWithout3D)

	if foundWith3D == 0 {
		t.Error("Expected at least some coordinates with Z value (depth)")
	}
}
