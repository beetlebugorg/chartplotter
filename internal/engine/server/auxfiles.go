package server

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/beetlebugorg/chartplotter/internal/engine/auxfiles"
)

// aux.go serves an ENC's auxiliary content — the external resources a feature
// points at by filename: TXTDSC/NTXTDS textual descriptions (.TXT) and PICREP
// pictures (PNG-transcoded). The baker writes them per provider into a companion
// "aux/" dir beside the pmtiles: loose static files + an index.json. The server
// indexes every aux dir and serves a SINGLE file per request on demand (GET
// /aux/<stored>), so a pick never downloads more than the one file it shows —
// and, because the files are loose, the same dir resolves OFFLINE as plain static
// files with no server at all. Legacy "<set>.aux.zip" companions (baked before the
// loose layout) are still indexed so their attachments keep resolving until re-bake.

// auxLoc points at one aux file: EITHER the loose aux dir that holds it (dir) or the
// legacy companion zip (zip); the stored filename (TIFF pictures are stored transcoded
// to .png); and the MIME type to serve it with.
type auxLoc struct {
	dir    string // loose aux dir holding the file (preferred); "" if it's in a zip
	zip    string // legacy companion aux.zip holding the file; "" if it's loose
	stored string
	typ    string
}

// auxIndex aggregates every provider's aux content into one lookup keyed by the
// upper-cased referenced filename (the form S-57 stores TXTDSC/PICREP values in).
// Built lazily from the cache and invalidated whenever a set is (re)baked or removed,
// so a freshly imported district's attachments resolve without a restart.
type auxIndex struct {
	mu       sync.RWMutex
	loaded   bool
	entries  map[string]auxLoc // referenced name (UPPER) → loc — drives the manifest
	byStored map[string]auxLoc // stored filename (UPPER) → loc — resolves GET /aux/<stored>
}

func newAuxIndex() *auxIndex {
	return &auxIndex{entries: map[string]auxLoc{}, byStored: map[string]auxLoc{}}
}

// invalidate marks the index stale; the next lookup rebuilds it.
func (a *auxIndex) invalidate() {
	a.mu.Lock()
	a.loaded = false
	a.mu.Unlock()
}

// ensure (re)builds the index from cacheDir if it is stale. It walks for every loose
// "aux/index.json" (the current layout) and every legacy "*.aux.zip" companion,
// reading each manifest. Best-effort: a broken or missing manifest is skipped,
// leaving those files unresolvable (the pick report then shows the bare filename).
func (a *auxIndex) ensure(cacheDir string) {
	a.mu.RLock()
	loaded := a.loaded
	a.mu.RUnlock()
	if loaded {
		return
	}
	entries := map[string]auxLoc{}
	_ = filepath.WalkDir(cacheDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		switch {
		case filepath.Base(path) == auxfiles.IndexName && filepath.Base(filepath.Dir(path)) == "aux":
			readAuxDirIndex(path, entries) // loose aux/ dir (current layout)
		case strings.HasSuffix(path, ".aux.zip"):
			readAuxZipIndex(path, entries) // legacy companion zip
		}
		return nil
	})
	byStored := make(map[string]auxLoc, len(entries))
	for _, loc := range entries {
		byStored[strings.ToUpper(loc.stored)] = loc
	}
	a.mu.Lock()
	a.entries = entries
	a.byStored = byStored
	a.loaded = true
	a.mu.Unlock()
}

// readAuxDirIndex decodes a loose aux dir's index.json and records each referenced
// filename as a loose-file location under that dir. Later manifests win on a (rare)
// name clash — aux names are basenames and effectively provider-unique.
func readAuxDirIndex(indexPath string, out map[string]auxLoc) {
	b, err := os.ReadFile(indexPath)
	if err != nil {
		return
	}
	var man auxfiles.Manifest
	if json.Unmarshal(b, &man) != nil {
		return
	}
	dir := filepath.Dir(indexPath)
	for name, e := range man.Files {
		out[strings.ToUpper(name)] = auxLoc{dir: dir, stored: e.Stored, typ: e.Type}
	}
}

// readAuxZipIndex opens one legacy companion aux.zip, decodes its index.json, and
// adds each referenced filename to out.
func readAuxZipIndex(path string, out map[string]auxLoc) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return
	}
	defer zr.Close()
	for _, f := range zr.File {
		if f.Name != auxfiles.IndexName {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return
		}
		var man auxfiles.Manifest
		err = json.NewDecoder(rc).Decode(&man)
		rc.Close()
		if err != nil {
			return
		}
		for name, e := range man.Files {
			out[strings.ToUpper(name)] = auxLoc{zip: path, stored: e.Stored, typ: e.Type}
		}
		return
	}
}

// lookupStored resolves a STORED aux filename (the name the manifest points the
// client at, and the client requests) to its location, rebuilding first if stale.
func (a *auxIndex) lookupStored(cacheDir, stored string) (auxLoc, bool) {
	a.ensure(cacheDir)
	a.mu.RLock()
	loc, ok := a.byStored[strings.ToUpper(stored)]
	a.mu.RUnlock()
	return loc, ok
}

// manifest returns referencedName→{stored,type} for every available aux file,
// rebuilding if stale — the same index.json shape the offline aux dir carries, so
// the client loads it identically online or off.
func (a *auxIndex) manifest(cacheDir string) map[string]auxfiles.Entry {
	a.ensure(cacheDir)
	a.mu.RLock()
	out := make(map[string]auxfiles.Entry, len(a.entries))
	for name, loc := range a.entries {
		out[name] = auxfiles.Entry{Stored: loc.stored, Type: loc.typ}
	}
	a.mu.RUnlock()
	return out
}

// serveAux serves ENC feature attachments (TXTDSC/PICREP) as loose static files — the
// SAME layout the offline bundle carries, so the client loads aux identically whether
// a server is running or not:
//
//	GET /aux/index.json  → {"version":1,"files":{REF:{stored,type},…}} of everything resolvable
//	GET /aux/<stored>    → that one file's bytes
//
// CORS-open so a static-hosted client can fetch from a different origin.
func (s *Server) serveAux(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if r.Method != http.MethodGet {
		apiErr(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	name := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, "/aux"), "/")
	if name == "" || name == "index.json" { // the manifest
		w.Header().Set("Content-Type", jsonCT)
		w.Header().Set("Cache-Control", "no-cache")
		_ = json.NewEncoder(w).Encode(auxfiles.Manifest{Version: 1, Files: s.auxIdx.manifest(s.cacheDir)})
		return
	}
	if dec, err := url.PathUnescape(name); err == nil {
		name = dec
	}
	loc, ok := s.auxIdx.lookupStored(s.cacheDir, name)
	if !ok {
		apiErr(w, http.StatusNotFound, "no such aux file")
		return
	}
	if err := writeAuxEntry(w, loc); err != nil {
		apiErr(w, http.StatusInternalServerError, err.Error())
	}
}

// writeAuxEntry streams one stored aux file to w with the indexed MIME type — a loose
// file read straight off disk (current layout), or, for a legacy companion, the entry
// extracted from its zip.
func writeAuxEntry(w http.ResponseWriter, loc auxLoc) error {
	if loc.dir != "" {
		b, err := os.ReadFile(filepath.Join(loc.dir, loc.stored))
		if err != nil {
			return err
		}
		w.Header().Set("Content-Type", loc.typ)
		w.Header().Set("Cache-Control", "max-age=86400")
		_, err = w.Write(b)
		return err
	}
	zr, err := zip.OpenReader(loc.zip)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, f := range zr.File {
		if f.Name != loc.stored {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		defer rc.Close()
		w.Header().Set("Content-Type", loc.typ)
		w.Header().Set("Cache-Control", "max-age=86400")
		_, err = io.Copy(w, rc)
		return err
	}
	return fmt.Errorf("entry %q missing from %s", loc.stored, filepath.Base(loc.zip))
}
