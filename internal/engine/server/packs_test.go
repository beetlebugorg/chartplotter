package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

// TestSplitSet covers the district/band split: a "-<knownband>" suffix splits off as
// the band; anything else is a whole-name district with band "all".
func TestSplitSet(t *testing.T) {
	cases := []struct{ in, district, band string }{
		{"noaa-d5-general", "noaa-d5", "general"},
		{"noaa-d5-overview", "noaa-d5", "overview"},
		{"ienc-allegheny-berthing", "ienc-allegheny", "berthing"},
		{"noaa-d5", "noaa-d5", "all"},           // no band suffix → whole name, "all"
		{"user", "user", "all"},                 // merged / local import
		{"import-harbor", "import", "harbor"},   // a band slug is still split off
		{"general", "general", "all"},           // bare slug (len == suffix) is NOT split
		{"x-coastalish", "x-coastalish", "all"}, // "-coastalish" is not "-coastal"
	}
	for _, c := range cases {
		d, b := splitSet(c.in)
		if d != c.district || b != c.band {
			t.Errorf("splitSet(%q) = (%q,%q), want (%q,%q)", c.in, d, b, c.district, c.band)
		}
	}
}

// TestHandlePacksGrouping checks /api/packs groups a district's band-sets into one
// entry, bands sorted coarse→fine, enabled iff any band is enabled.
func TestHandlePacksGrouping(t *testing.T) {
	dir := t.TempDir()
	s := New(dir, dir, dir, false)

	// Register band-sets out of order across two districts plus a merged set.
	for _, n := range []string{
		"noaa-d5-harbor", "noaa-d5-overview", "noaa-d5-general", "noaa-d5-coastal",
		"ienc-x-berthing",
		"legacy", // merged set → band "all"
	} {
		s.packAdd(n, dir+"/"+n+".pmtiles")
	}
	// Disable only ONE of noaa-d5's bands — the district stays enabled (any enabled).
	s.prefs.setDisabled("noaa-d5-harbor", true)
	// Disable BOTH of ienc-x's sole band → district disabled.
	s.prefs.setDisabled("ienc-x-berthing", true)

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
			Name    string   `json:"name"`
			Enabled bool     `json:"enabled"`
			Bands   []string `json:"bands"`
		} `json:"packs"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("bad JSON %s: %v", body, err)
	}
	// Districts sorted: ienc-x, legacy, noaa-d5.
	want := []struct {
		name    string
		enabled bool
		bands   []string
	}{
		{"ienc-x", false, []string{"berthing"}},
		{"legacy", true, []string{"all"}},
		{"noaa-d5", true, []string{"overview", "general", "coastal", "harbor"}}, // coarse→fine
	}
	if len(got.Packs) != len(want) {
		t.Fatalf("got %d packs, want %d: %s", len(got.Packs), len(want), body)
	}
	for i, w := range want {
		p := got.Packs[i]
		if p.Name != w.name || p.Enabled != w.enabled || !reflect.DeepEqual(p.Bands, w.bands) {
			t.Errorf("pack[%d] = {%q,%t,%v}, want {%q,%t,%v}", i, p.Name, p.Enabled, p.Bands, w.name, w.enabled, w.bands)
		}
	}
}
