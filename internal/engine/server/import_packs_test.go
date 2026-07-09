package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestImportPacks is a real end-to-end of POST /api/import/packs under the provider-
// enc-root model: two districts (d5, d7) of the SAME provider are downloaded into their
// ENC_ROOT subfolders and baked into ONE provider archive in a single job. The same cell
// is placed in both district folders, so this also exercises the engine's stem de-dup
// (it must bake once, not double-draw). Exercises handleImportPacks → runImportPacks →
// fetchPackCells → cacheDistrict → bakeProvider → registerBakedSet with the native
// libtile57 engine and a committed real S-57 cell.
func TestImportPacks(t *testing.T) {
	cell, err := os.ReadFile("../../../testdata/US5MD1MC.000")
	if err != nil {
		t.Skipf("testdata cell absent: %v", err)
	}
	cacheDir, dataDir := t.TempDir(), t.TempDir()
	s := New(t.TempDir(), cacheDir, dataDir, false)
	ts := httptest.NewServer(s)
	defer ts.Close()

	// Pre-place the real cell into two NOAA district folders under the ENC_ROOT so the
	// per-cell fetch with an empty URL finds them without hitting NOAA.
	for _, dist := range []string{"d5", "d7"} {
		p := filepath.Join(s.districtDir("noaa", dist), "US5MD1MC.000")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, cell, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	body := `{"packs":[
		{"set":"noaa-d5","cells":[{"name":"US5MD1MC","url":""}]},
		{"set":"noaa-d7","cells":[{"name":"US5MD1MC","url":""}]}
	]}`
	resp, err := http.Post(ts.URL+"/api/import/packs", jsonCT, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("accept: %d %s", resp.StatusCode, b)
	}
	var acc struct{ Job string }
	if err := json.Unmarshal(b, &acc); err != nil || acc.Job == "" {
		t.Fatalf("bad accept body %q: %v", b, err)
	}

	// Poll the job to completion (a single small harbor cell bakes in ~1-2s).
	var st struct {
		State, Error string
		Cells        int
	}
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		r, _ := http.Get(ts.URL + "/api/import/status?job=" + acc.Job)
		bb, _ := io.ReadAll(r.Body)
		r.Body.Close()
		json.Unmarshal(bb, &st)
		if st.State != "running" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if st.State != "done" {
		t.Fatalf("job state=%q error=%q", st.State, st.Error)
	}

	// ONE provider set ("noaa") registered, serving from the live-composite structure
	// under the cache dir (per-cell tiles/<STEM>.pmtiles + partition.tpart), and both
	// districts' source cells kept under the data dir.
	if _, ok := s.sets.get("noaa"); !ok {
		t.Errorf("provider set %q not registered", "noaa")
	}
	cellArc := filepath.Join(s.setDir("noaa"), "tiles", "US5MD1MC.pmtiles")
	if fi, err := os.Stat(cellArc); err != nil || fi.Size() == 0 {
		t.Errorf("provider %q: no baked per-cell tiles (%v)", "noaa", err)
	}
	if _, err := os.Stat(filepath.Join(s.setDir("noaa"), "partition.tpart")); err != nil {
		t.Errorf("provider %q: no partition sidecar (%v)", "noaa", err)
	}
	for _, dist := range []string{"d5", "d7"} {
		if _, err := os.Stat(filepath.Join(s.districtDir("noaa", dist), "US5MD1MC.000")); err != nil {
			t.Errorf("district %q: source cell missing after bake: %v", dist, err)
		}
	}
	// The shared cell bakes ONCE (stem de-dup): the provider cell manifest has one entry.
	if stems, ok := s.setCells("noaa"); !ok || len(stems) != 1 || stems[0] != "US5MD1MC" {
		t.Errorf("provider cell manifest = %v (ok=%t), want [US5MD1MC]", stems, ok)
	}
}
