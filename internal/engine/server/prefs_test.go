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

// TestLiveGenTokenContentAddressed: a live provider's ?g token is a content sha-of-shas over its
// per-cell archives — deterministic (same content → same token), order-independent, and it moves
// only when a cell's content changes or the set gains/loses a cell. A no-op re-bake keeps the
// token so the client's cached tiles stay valid.
func TestLiveGenTokenContentAddressed(t *testing.T) {
	s := &Server{cacheDir: t.TempDir()}
	dir := s.liveCellsDir("noaa")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cell := func(stem, sha string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, stem+".pmtiles"), []byte("pm"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, stem+".pmtiles.sha"), []byte(sha), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cell("US5MD1MC", "aaaa")
	cell("US4MD81M", "bbbb")
	base := s.liveGenToken("noaa")
	if base <= 0 {
		t.Fatalf("token = %d, want positive", base)
	}
	if got := s.liveGenToken("noaa"); got != base { // deterministic
		t.Errorf("token not stable: %d vs %d", got, base)
	}

	cell("US4MD81M", "cccc") // a cell's content changed → token changes
	if got := s.liveGenToken("noaa"); got == base {
		t.Errorf("token unchanged after a cell's sha changed")
	}
	cell("US4MD81M", "bbbb") // restore → original token (content-addressed, history-independent)
	if got := s.liveGenToken("noaa"); got != base {
		t.Errorf("token not restored: %d vs %d", got, base)
	}

	cell("US3MD01M", "dddd") // adding a cell changes it; removing restores
	if got := s.liveGenToken("noaa"); got == base {
		t.Errorf("token unchanged after adding a cell")
	}
	_ = os.Remove(filepath.Join(dir, "US3MD01M.pmtiles"))
	_ = os.Remove(filepath.Join(dir, "US3MD01M.pmtiles.sha"))
	if got := s.liveGenToken("noaa"); got != base {
		t.Errorf("token not restored after removing the cell: %d vs %d", got, base)
	}

	// The engine composes live tiles at serve time, so its commit is part of the
	// content address: a rebuilt engine must move the token even over identical
	// archives (serve-path fixes bust client caches), deterministically.
	s.EngineCommit = "abc123"
	withEngine := s.liveGenToken("noaa")
	if withEngine == base {
		t.Errorf("token unchanged after engine commit set")
	}
	if got := s.liveGenToken("noaa"); got != withEngine {
		t.Errorf("engine-stamped token not stable: %d vs %d", got, withEngine)
	}
	s.EngineCommit = "def456"
	if got := s.liveGenToken("noaa"); got == withEngine {
		t.Errorf("token unchanged after engine commit changed")
	}
	s.EngineCommit = ""

	if got := (&Server{cacheDir: t.TempDir()}).liveGenToken("noaa"); got != 0 { // empty set → 0
		t.Errorf("empty token = %d, want 0", got)
	}
}
