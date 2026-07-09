package server

import (
	"os"
	"path/filepath"
	"testing"
)

// TestScanPacksStandaloneArchivesOnly: scanPacks discovers only STANDALONE archives dropped
// into the flat <cache>/tiles dir (keyed by basename). A live runtime-compositor provider owns
// <provider>/tiles/*.pmtiles + partition.tpart — compositor INPUTS discovered
// provider-centrically by registerLiveProviders — and must NEVER be scavenged here (no phantom
// per-cell sets), even when its partition sidecar isn't saved yet.
func TestScanPacksStandaloneArchivesOnly(t *testing.T) {
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

	// Standalone archives in the flat <cache>/tiles dir → registered by basename.
	write("tiles/charts.pmtiles", "pm")
	write("tiles/overlay.mbtiles", "mb")

	// A live provider's per-cell inputs under <provider>/tiles — WITH a partition sidecar...
	write("NOAA/tiles/US5MD1MC.pmtiles", "pm")
	write("NOAA/partition.tpart", "part")
	// ...and one mid-import (archives present, partition not saved yet).
	write("IENC/tiles/US4MD81M.pmtiles", "pm")

	got := scanPacks(cache)

	for _, want := range []string{"charts", "overlay"} {
		if _, ok := got[want]; !ok {
			t.Errorf("standalone archive %q not discovered: %v", want, got)
		}
	}
	for _, phantom := range []string{"noaa", "ienc", "us5md1mc", "us4md81m", "US5MD1MC", "US4MD81M", "partition"} {
		if p, ok := got[phantom]; ok {
			t.Errorf("live-provider input mis-registered as set %q -> %q", phantom, p)
		}
	}
	if len(got) != 2 {
		t.Errorf("scanPacks = %v, want {charts, overlay}", got)
	}
}
