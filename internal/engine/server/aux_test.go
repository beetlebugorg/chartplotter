package server

import (
	"archive/zip"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/beetlebugorg/chartplotter/internal/engine/auxfiles"
)

// writeAuxDirT writes a loose aux/ dir (the current layout) under dir.
func writeAuxDirT(t *testing.T, dir string, files map[string][]byte) {
	t.Helper()
	if _, err := auxfiles.WriteDir(filepath.Join(dir, "aux"), files); err != nil {
		t.Fatal(err)
	}
}

// writeLegacyAuxZip writes a legacy companion "<name>.aux.zip" (loose files + an
// index.json in the auxfiles.Manifest shape) so the back-compat reader stays covered
// — pre-loose bakes (e.g. the user's existing noaa.aux.zip) must keep resolving.
func writeLegacyAuxZip(t *testing.T, dir, name string, files map[string][]byte) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(dir, name+".aux.zip"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	index := map[string]auxfiles.Entry{}
	for key, data := range files {
		stored := filepath.Base(key)
		w, err := zw.Create(stored)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatal(err)
		}
		index[key] = auxfiles.Entry{Stored: stored, Type: "text/plain"}
	}
	iw, err := zw.Create(auxfiles.IndexName)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.NewEncoder(iw).Encode(auxfiles.Manifest{Version: 1, Files: index}); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestServeAux(t *testing.T) {
	cache := t.TempDir()
	// One district as a loose aux/ dir (current layout), one as a legacy companion
	// zip — both must resolve through the same /api/aux.
	writeAuxDirT(t, filepath.Join(cache, "NOAA", "D5-OVERVIEW"), map[string][]byte{
		"US5MD1MC.TXT": []byte("Channel maintained to 35ft"),
	})
	writeLegacyAuxZip(t, filepath.Join(cache, "NOAA", "D17-OVERVIEW"), "noaa-d17-overview", map[string][]byte{
		"NOTE17.TXT": []byte("Caution: ice"),
	})
	s := New("", cache, cache, true)

	// Manifest lists every referenced filename → {stored,type}, across both layouts.
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/aux/index.json", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("manifest status = %d", rec.Code)
	}
	var man auxfiles.Manifest
	if err := json.Unmarshal(rec.Body.Bytes(), &man); err != nil {
		t.Fatal(err)
	}
	if man.Files["US5MD1MC.TXT"].Type != "text/plain" || man.Files["US5MD1MC.TXT"].Stored != "US5MD1MC.TXT" {
		t.Errorf("manifest US5MD1MC.TXT = %+v, want stored=US5MD1MC.TXT type=text/plain", man.Files["US5MD1MC.TXT"])
	}
	if man.Files["NOTE17.TXT"].Type != "text/plain" { // the legacy-zip district
		t.Errorf("manifest missing the legacy-zip district's file: %v", man.Files)
	}

	// One file by its stored name (case-insensitive) → its bytes + content type.
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/aux/us5md1mc.txt", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("file status = %d", rec.Code)
	}
	if got := rec.Body.String(); got != "Channel maintained to 35ft" {
		t.Errorf("file body = %q", got)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/plain" {
		t.Errorf("file content-type = %q", ct)
	}

	// The legacy-zip district's file resolves through the same /aux/<stored> path.
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/aux/NOTE17.TXT", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "Caution: ice" {
		t.Errorf("legacy-zip file: status=%d body=%q", rec.Code, rec.Body.String())
	}

	// Unknown name → 404.
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/aux/NOPE.TXT", nil))
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
	// Add an aux dir after the first (empty) build, then invalidate.
	writeAuxDirT(t, filepath.Join(cache, "import"), map[string][]byte{"A.TXT": []byte("x")})
	s.auxIdx.invalidate()
	if _, ok := s.auxIdx.lookupStored(cache, "A.TXT"); !ok {
		t.Errorf("A.TXT not found after invalidate + re-index")
	}
}
