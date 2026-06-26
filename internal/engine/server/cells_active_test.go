package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/beetlebugorg/chartplotter/internal/engine/baker"
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

// TestActiveCellsDropOnDelete: a manifest-tracked pack's cells appear under
// ?active=1, and DELETEing the pack drops them AND removes the lingering
// <set>.cells.json manifest. This is the "search shows uninstalled cells" / "stale
// after remove" regression: the active set is driven by the live pack list + its
// manifest, so removing the pack (packDel) and its manifest clears the cells, while
// the source stays in ENC_ROOT for a future re-bake.
func TestActiveCellsDropOnDelete(t *testing.T) {
	dir := t.TempDir()
	s := New(dir, dir, dir, false)

	const cell = "US5MD11M"
	// The cell's source dir must exist in ENC_ROOT (serveCells lists it from there).
	if err := os.MkdirAll(filepath.Join(dir, "ENC_ROOT", cell), 0o755); err != nil {
		t.Fatal(err)
	}
	// Register a band-set with an exact cell manifest, and add it as an enabled pack.
	const set = "noaa-d5-harbor"
	if err := s.writeSetCells(set, map[string]baker.CellData{cell + ".000": {}}); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(s.setDir(set), set+".cells.json")
	if _, err := os.Stat(manifest); err != nil {
		t.Fatalf("manifest not written: %v", err)
	}
	s.packAdd(set, filepath.Join(s.setDir(set), set+".pmtiles"))

	ts := httptest.NewServer(s)
	defer ts.Close()

	if !activeCells(t, ts.URL)[cell] {
		t.Fatalf("cell %s should be active while its pack is installed", cell)
	}

	// DELETE the district → its band-sets are unregistered and their baked files +
	// manifests removed.
	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/set?set=noaa-d5", nil)
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
		t.Errorf("cell %s still active after its pack was deleted (stale search)", cell)
	}
	if _, err := os.Stat(manifest); !os.IsNotExist(err) {
		t.Errorf("manifest %s not removed on delete (err=%v)", manifest, err)
	}
	// The source cell is intentionally kept for a future re-bake.
	if _, err := os.Stat(filepath.Join(dir, "ENC_ROOT", cell)); err != nil {
		t.Errorf("source cell should be kept in ENC_ROOT: %v", err)
	}
}
