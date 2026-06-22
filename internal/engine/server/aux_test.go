package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/beetlebugorg/chartplotter/internal/engine/auxfiles"
)

// writeAux writes a companion aux.zip with the given files into dir/name.aux.zip.
func writeAux(t *testing.T, dir, name string, files map[string][]byte) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(dir, name+".aux.zip"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := auxfiles.WriteZip(f, files); err != nil {
		t.Fatal(err)
	}
}

func TestServeAux(t *testing.T) {
	cache := t.TempDir()
	// Two districts, each with its own companion aux.zip somewhere under the cache.
	writeAux(t, filepath.Join(cache, "NOAA", "D5-OVERVIEW"), "noaa-d5-overview", map[string][]byte{
		"US5MD1MC.TXT": []byte("Channel maintained to 35ft"),
	})
	writeAux(t, filepath.Join(cache, "NOAA", "D17-OVERVIEW"), "noaa-d17-overview", map[string][]byte{
		"NOTE17.TXT": []byte("Caution: ice"),
	})
	s := New("", cache, cache, true)

	// Manifest lists every referenced filename (upper-cased) → MIME, across districts.
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/aux", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("manifest status = %d", rec.Code)
	}
	var man struct {
		Files map[string]string `json:"files"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &man); err != nil {
		t.Fatal(err)
	}
	if man.Files["US5MD1MC.TXT"] != "text/plain" {
		t.Errorf("manifest US5MD1MC.TXT = %q, want text/plain", man.Files["US5MD1MC.TXT"])
	}
	if man.Files["NOTE17.TXT"] != "text/plain" {
		t.Errorf("manifest missing the second district's file: %v", man.Files)
	}

	// One file by name (case-insensitive) → its bytes + content type, not the zip.
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/aux/us5md1mc.txt", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("file status = %d", rec.Code)
	}
	if got := rec.Body.String(); got != "Channel maintained to 35ft" {
		t.Errorf("file body = %q", got)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/plain" {
		t.Errorf("file content-type = %q", ct)
	}

	// Unknown name → 404 (and the raw zip is never reachable).
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/aux/NOPE.TXT", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown file status = %d, want 404", rec.Code)
	}
}

func TestAuxIndexInvalidate(t *testing.T) {
	cache := t.TempDir()
	s := New("", cache, cache, true)
	if got := s.auxIdx.manifest(cache); len(got) != 0 {
		t.Fatalf("empty cache manifest = %v", got)
	}
	// Add a companion zip after the first (empty) build, then invalidate.
	writeAux(t, filepath.Join(cache, "import"), "import", map[string][]byte{"A.TXT": []byte("x")})
	s.auxIdx.invalidate()
	if _, ok := s.auxIdx.lookup(cache, "A.TXT"); !ok {
		t.Errorf("A.TXT not found after invalidate + re-index")
	}
}
