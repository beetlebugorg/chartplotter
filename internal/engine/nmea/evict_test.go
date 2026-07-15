package nmea

import "testing"

func TestAISStoreUpsertAndEvictSource(t *testing.T) {
	s := NewAISStore(0)
	s.Upsert(AISTarget{MMSI: 1, Lat: 10, Lon: 20}, "plugin-a")
	s.Upsert(AISTarget{MMSI: 2, Lat: 11, Lon: 21}, "plugin-a")
	s.Upsert(AISTarget{MMSI: 3, Lat: 12, Lon: 22}, "plugin-b")
	if got := len(s.Snapshot()); got != 3 {
		t.Fatalf("want 3 targets, got %d", got)
	}
	ver := s.Version()

	// Revoking plugin-a's grant evicts only its targets and bumps the version.
	if n := s.EvictSource("plugin-a"); n != 2 {
		t.Fatalf("EvictSource removed %d, want 2", n)
	}
	if s.Version() == ver {
		t.Fatalf("version did not change after eviction")
	}
	snap := s.Snapshot()
	if len(snap) != 1 || snap[0].MMSI != 3 {
		t.Fatalf("want only plugin-b's target 3 left, got %+v", snap)
	}
	// Evicting an unknown source removes nothing.
	if n := s.EvictSource("nobody"); n != 0 {
		t.Fatalf("EvictSource(nobody) removed %d, want 0", n)
	}
}
