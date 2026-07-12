package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// activeCells GETs /api/cells?active=1 and returns the cell-name set.
func activeCells(t *testing.T, base string) map[string]bool {
	t.Helper()
	r, err := http.Get(base + "/api/cells?active=1")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	var got struct {
		Cells []string `json:"cells"`
	}
	if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	set := map[string]bool{}
	for _, c := range got.Cells {
		set[c] = true
	}
	return set
}

// TestActiveCellsDropOnDelete: a provider's manifest-tracked cells appear under
// ?active=1, and DELETEing the provider drops them AND removes the lingering
// <provider>.cells.json manifest + the ENC_ROOT source tree. This is the "search shows
// uninstalled cells" / "stale after remove" regression: the active set is driven by the
// live pack list + its manifest, so dropping the provider set clears the cells, and
// delete also reclaims the provider's source ENC_ROOT.
func TestActiveCellsDropOnDelete(t *testing.T) {
	dir := t.TempDir()
	s := New(dir, dir, dir, false, "")

	const cell = "US5MD11M"
	// The cell's source .000 must exist in the provider ENC_ROOT (serveCells lists
	// installed cells from the ENC_ROOT trees; delete reclaims them).
	scell := filepath.Join(s.districtDir("noaa", "d5"), cell+".000")
	if err := os.MkdirAll(filepath.Dir(scell), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(scell, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Register the provider set with an exact cell manifest, and add it as an enabled pack.
	const provider = "noaa"
	if err := s.writeSetCells(provider, []string{cell}); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(s.setDir(provider), provider+".cells.json")
	if _, err := os.Stat(manifest); err != nil {
		t.Fatalf("manifest not written: %v", err)
	}
	s.packAdd(provider, filepath.Join(s.setDir(provider), "tiles", "chart.pmtiles"))

	ts := httptest.NewServer(s)
	defer ts.Close()

	if !activeCells(t, ts.URL)[cell] {
		t.Fatalf("cell %s should be active while its provider is installed", cell)
	}

	// DELETE the provider → unregistered, baked bundle + manifest + ENC_ROOT removed.
	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/set?set=noaa", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete status %d", resp.StatusCode)
	}

	if activeCells(t, ts.URL)[cell] {
		t.Errorf("cell %s still active after its provider was deleted (stale search)", cell)
	}
	if _, err := os.Stat(manifest); !os.IsNotExist(err) {
		t.Errorf("manifest %s not removed on delete (err=%v)", manifest, err)
	}
	// Delete reclaims disk: the provider's source ENC_ROOT is removed too (re-download to
	// restore); disable, by contrast, keeps the cells + bundle and only hides the set.
	if _, err := os.Stat(scell); !os.IsNotExist(err) {
		t.Errorf("source cell should be removed on delete (err=%v)", err)
	}
}
