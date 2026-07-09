package server

import (
	"os"
	"path/filepath"
	"testing"
)

// TestScanPacksSkipsLiveProvider: a live runtime-compositor provider keeps its per-cell
// PMTiles under <provider>/tiles next to a partition.tpart sidecar. scanPacks must skip that
// whole dir — those archives are compositor INPUTS — so no cell surfaces as a phantom provider,
// while a legacy batch bundle's tiles/chart.pmtiles (no sidecar) still registers by provider name.
func TestScanPacksSkipsLiveProvider(t *testing.T) {
	cache := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(cache, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// A live provider: per-cell archives under tiles/ + the partition sidecar alongside.
	write("NOAA/tiles/US5MD1MC.pmtiles", "pm")
	write("NOAA/tiles/US4MD81M.pmtiles", "pm")
	write("NOAA/partition.tpart", "part")

	// A legacy batch bundle: tiles/chart.pmtiles, no sidecar.
	write("LEGACY/tiles/chart.pmtiles", "pm")

	got := scanPacks(cache)

	if _, ok := got["legacy"]; !ok {
		t.Errorf("legacy batch bundle not registered: %v", got)
	}
	for _, phantom := range []string{"noaa", "us5md1mc", "us4md81m", "chart", "partition"} {
		if p, ok := got[phantom]; ok {
			t.Errorf("live-provider input mis-registered as pack %q -> %q", phantom, p)
		}
	}
	if len(got) != 1 {
		t.Errorf("scanPacks = %v, want only {legacy}", got)
	}
}
