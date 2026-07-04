package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestProviderKeys covers the provider/district split of a client pack key: the
// provider is the part before the first "-" (the baked SET name), the district is
// everything after it (the ENC_ROOT subfolder).
func TestProviderKeys(t *testing.T) {
	cases := []struct{ in, provider, district string }{
		{"noaa-d5", "noaa", "d5"},
		{"ienc-allegheny", "ienc", "allegheny"},
		{"user-us5md1mc", "user", "us5md1mc"},
		{"user-us5md1mc-2", "user", "us5md1mc-2"}, // district keeps everything after the first "-"
		{"noaa", "noaa", ""},                      // bare provider (no district)
	}
	for _, c := range cases {
		if p := providerOf(c.in); p != c.provider {
			t.Errorf("providerOf(%q) = %q, want %q", c.in, p, c.provider)
		}
		if d := districtOf(c.in); d != c.district {
			t.Errorf("districtOf(%q) = %q, want %q", c.in, d, c.district)
		}
	}
}

// TestHandlePacksProviders checks /api/packs lists ONE entry per provider with its
// installed districts read from the ENC_ROOT folder listing (not per-district sets).
func TestHandlePacksProviders(t *testing.T) {
	dir := t.TempDir()
	s := New(dir, dir, dir, false)

	// Create ENC_ROOT district folders (each with a placeholder .000) for two providers.
	for _, dd := range []struct{ prov, dist string }{
		{"noaa", "d5"}, {"noaa", "d7"}, {"ienc", "allegheny"},
	} {
		p := filepath.Join(s.districtDir(dd.prov, dd.dist), "US5XX000.000")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Register baked provider sets (fake paths — bounds/meta are just skipped when the
	// archive can't be opened). Disable ienc so it reports enabled=false.
	s.packAdd("noaa", dir+"/noaa.pmtiles")
	s.packAdd("ienc", dir+"/ienc.pmtiles")
	s.prefs.setDisabled("ienc", true)

	ts := httptest.NewServer(s)
	defer ts.Close()
	r, err := http.Get(ts.URL + "/api/packs")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(r.Body)
	r.Body.Close()

	var got struct {
		Packs []struct {
			Name      string   `json:"name"`
			Enabled   bool     `json:"enabled"`
			Districts []string `json:"districts"`
		} `json:"packs"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("bad JSON %s: %v", body, err)
	}
	// Providers sorted: ienc, noaa; districts sorted within each.
	want := []struct {
		name      string
		enabled   bool
		districts []string
	}{
		{"ienc", false, []string{"allegheny"}},
		{"noaa", true, []string{"d5", "d7"}},
	}
	if len(got.Packs) != len(want) {
		t.Fatalf("got %d packs, want %d: %s", len(got.Packs), len(want), body)
	}
	for i, w := range want {
		p := got.Packs[i]
		if p.Name != w.name || p.Enabled != w.enabled || !reflect.DeepEqual(p.Districts, w.districts) {
			t.Errorf("pack[%d] = {%q,%t,%v}, want {%q,%t,%v}", i, p.Name, p.Enabled, p.Districts, w.name, w.enabled, w.districts)
		}
	}
}
