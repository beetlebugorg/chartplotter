package server

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// makeZip builds an in-memory zip from name→bytes entries.
func makeZip(t *testing.T, entries map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, data := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestExtractZipCells(t *testing.T) {
	z := makeZip(t, map[string][]byte{
		"ENC_ROOT/US5MD1MC/US5MD1MC.000": []byte("base"),
		"ENC_ROOT/US5MD1MC/US5MD1MC.001": []byte("upd1"),
		"ENC_ROOT/US4VA50M/US4VA50M.000": []byte("base2"),
		"ENC_ROOT/US5MD1MC/PIC01.TIF":    []byte("tiff"),
		"ENC_ROOT/CATALOG.031":           []byte("catalogue"), // excluded
		"ENC_ROOT/README.TXT":            []byte("readme"),    // excluded
		"ENC_ROOT/US5MD1MC/US5MD1MC.TXT": []byte("desc"),      // aux text
	})
	cells, aux, err := extractZipCells(z)
	if err != nil {
		t.Fatalf("extractZipCells: %v", err)
	}
	if len(cells) != 2 {
		t.Fatalf("cells = %d, want 2", len(cells))
	}
	cd, ok := cells["US5MD1MC.000"]
	if !ok {
		t.Fatalf("missing US5MD1MC.000; got %v", keys(cells))
	}
	if string(cd.Base) != "base" || string(cd.Updates["US5MD1MC.001"]) != "upd1" {
		t.Fatalf("US5MD1MC base/update wrong: %q / %v", cd.Base, cd.Updates)
	}
	// Aux: the .TIF and .TXT are kept (keyed upper-cased basename); CATALOG/README not.
	if _, ok := aux["PIC01.TIF"]; !ok {
		t.Errorf("aux missing PIC01.TIF; got %v", keys(auxStr(aux)))
	}
	if _, ok := aux["US5MD1MC.TXT"]; !ok {
		t.Errorf("aux missing US5MD1MC.TXT")
	}
	if _, ok := aux["CATALOG.031"]; ok {
		t.Errorf("CATALOG.031 should be excluded from aux")
	}
}

func TestImportValidation(t *testing.T) {
	dir := t.TempDir()
	ts := httptest.NewServer(New(dir, dir, false))
	defer ts.Close()

	// Bad set name → 400.
	resp, _ := http.Post(ts.URL+"/api/import?set=bad..name", "application/zip", bytes.NewReader([]byte("PK\x03\x04")))
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("bad set: got %d, want 400", resp.StatusCode)
	}

	// Reserved 'dynamic' set name → 400.
	resp, _ = http.Post(ts.URL+"/api/import?set=dynamic", "application/zip", bytes.NewReader([]byte("PK\x03\x04")))
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("dynamic set: got %d, want 400", resp.StatusCode)
	}

	// GET /api/import → 405.
	resp, _ = http.Get(ts.URL + "/api/import?set=charts")
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET import: got %d, want 405", resp.StatusCode)
	}

	// Unknown job status → 404.
	resp, _ = http.Get(ts.URL + "/api/import/status?job=nope")
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown job: got %d, want 404", resp.StatusCode)
	}

	// A valid set but no cells in the (empty) zip → 400.
	resp, _ = http.Post(ts.URL+"/api/import?set=charts", "application/zip", bytes.NewReader(makeZip(t, map[string][]byte{"README.TXT": []byte("x")})))
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("no cells: got %d, want 400", resp.StatusCode)
	}
}

// TestImportJobErrorPath posts a zip whose .000 is not valid S-57. The endpoint
// accepts the job (202), the background bake parses nothing, and the job ends in
// "error" — exercising the goroutine, progress store, and status polling without a
// real cell. No archive is registered.
func TestImportJobErrorPath(t *testing.T) {
	dir := t.TempDir()
	srv := New(dir, dir, false)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	z := makeZip(t, map[string][]byte{"US5MD1MC/US5MD1MC.000": []byte("not-a-real-s57-cell")})
	resp, err := http.Post(ts.URL+"/api/import?set=charts", "application/zip", bytes.NewReader(z))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("accept: status %d body %s", resp.StatusCode, body)
	}
	var acc struct {
		OK  bool   `json:"ok"`
		Job string `json:"job"`
		Set string `json:"set"`
	}
	if err := json.Unmarshal(body, &acc); err != nil || acc.Job == "" {
		t.Fatalf("accept body %s (err %v)", body, err)
	}

	// Poll until the job leaves "running".
	var state string
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r, _ := http.Get(ts.URL + "/api/import/status?job=" + acc.Job)
		b, _ := io.ReadAll(r.Body)
		r.Body.Close()
		var st struct {
			State string `json:"state"`
		}
		json.Unmarshal(b, &st)
		state = st.State
		if state != "running" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if state != "error" {
		t.Fatalf("job state = %q, want error", state)
	}
	// Nothing should have been written or registered.
	if _, err := os.Stat(filepath.Join(tilesDir(dir), "charts.pmtiles")); !os.IsNotExist(err) {
		t.Errorf("archive should not exist on a failed bake")
	}
	if _, ok := srv.sets.get("charts"); ok {
		t.Errorf("set should not be registered on a failed bake")
	}
}

func TestImportFetchValidation(t *testing.T) {
	dir := t.TempDir()
	ts := httptest.NewServer(New(dir, dir, false))
	defer ts.Close()

	post := func(body string) int {
		resp, _ := http.Post(ts.URL+"/api/import", "application/json", bytes.NewReader([]byte(body)))
		resp.Body.Close()
		return resp.StatusCode
	}
	if c := post(`{"set":"bad..name","cells":[{"name":"X","url":""}]}`); c != http.StatusBadRequest {
		t.Errorf("bad set: got %d", c)
	}
	if c := post(`{"set":"user"}`); c != http.StatusBadRequest {
		t.Errorf("no spec: got %d, want 400", c)
	}
	if c := post(`{"set":"user","zipUrl":"https://evil.example/x.zip"}`); c != http.StatusBadRequest {
		t.Errorf("non-NOAA zipUrl: got %d, want 400", c)
	}
	if c := post(`{"set":"user","cells":[{"name":"US5MD1MC","url":"https://evil.example/c.zip"}]}`); c != http.StatusBadRequest {
		t.Errorf("non-NOAA cell url: got %d, want 400", c)
	}
}

// TestImportFetchDownloadOnly places a cell in the cache, then a per-cell
// download-only fetch with an empty URL finds it cached (no NOAA) and finishes
// "done" without baking — verifying the server-side download path + cache.
func TestImportFetchDownloadOnly(t *testing.T) {
	dir := t.TempDir()
	cp := filepath.Join(dir, "ENC_ROOT", "US5MD1MC", "US5MD1MC.000")
	if err := os.MkdirAll(filepath.Dir(cp), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cp, []byte("cell-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := New(dir, dir, false)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/import", "application/json",
		bytes.NewReader([]byte(`{"set":"user","downloadOnly":true,"cells":[{"name":"US5MD1MC","url":""}]}`)))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("accept: %d %s", resp.StatusCode, body)
	}
	var acc struct{ Job string }
	json.Unmarshal(body, &acc)

	var st struct {
		State, Phase string
		Cells        int
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r, _ := http.Get(ts.URL + "/api/import/status?job=" + acc.Job)
		b, _ := io.ReadAll(r.Body)
		r.Body.Close()
		json.Unmarshal(b, &st)
		if st.State != "running" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if st.State != "done" || st.Cells != 1 {
		t.Fatalf("download-only: state=%q cells=%d, want done/1", st.State, st.Cells)
	}
	// Download-only must NOT register a tile set.
	if _, ok := srv.sets.get("user"); ok {
		t.Errorf("download-only should not register a set")
	}
}

// TestServeCells lists the cells in the ENC_ROOT cache (so the client's installed
// set + persisted "user" tile set survive a reload — cells live server-side now).
func TestServeCells(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"US5MD1MC", "U37AG001"} {
		p := filepath.Join(dir, "ENC_ROOT", n, n+".000")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		os.WriteFile(p, []byte("x"), 0o644)
	}
	ts := httptest.NewServer(New(dir, dir, false))
	defer ts.Close()
	resp, _ := http.Get(ts.URL + "/api/cells")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var got struct{ Cells []string }
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("parse: %v (%s)", err, body)
	}
	if len(got.Cells) != 2 || got.Cells[0] != "U37AG001" || got.Cells[1] != "US5MD1MC" {
		t.Errorf("cells = %v, want sorted [U37AG001 US5MD1MC]", got.Cells)
	}
}

func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// auxStr adapts a []byte map for keys() in error messages.
func auxStr(m map[string][]byte) map[string]string {
	out := make(map[string]string, len(m))
	for k := range m {
		out[k] = ""
	}
	return out
}
