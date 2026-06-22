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
)

// aux.go serves an ENC's auxiliary content — the external resources a feature
// points at by filename: TXTDSC/NTXTDS textual descriptions (.TXT) and PICREP
// pictures (PNG-transcoded). The baker bundles them per district into a companion
// "<set>.aux.zip" beside the pmtiles. Rather than expose that raw archive to the
// client (which would force a whole-zip download just to render one pick), the
// server indexes every companion zip and serves a SINGLE file per request on
// demand. The pick report fetches GET /api/aux/<name> only when a picked feature
// actually references it.

// auxLoc points at one aux file: the companion zip that holds it, the entry name
// inside that zip (TIFF pictures are stored transcoded to .png), and the MIME type
// to serve it with.
type auxLoc struct {
	zip    string
	stored string
	typ    string
}

// auxIndex aggregates every set's companion "<set>.aux.zip" into one lookup keyed
// by the upper-cased referenced filename (the form S-57 stores TXTDSC/PICREP values
// in). Built lazily from the cache and invalidated whenever a set is (re)baked or
// removed, so a freshly imported district's attachments resolve without a restart.
type auxIndex struct {
	mu      sync.RWMutex
	loaded  bool
	entries map[string]auxLoc
}

func newAuxIndex() *auxIndex { return &auxIndex{entries: map[string]auxLoc{}} }

// invalidate marks the index stale; the next lookup rebuilds it.
func (a *auxIndex) invalidate() {
	a.mu.Lock()
	a.loaded = false
	a.mu.Unlock()
}

// ensure (re)builds the index from cacheDir if it is stale. It walks for every
// "*.aux.zip" companion and reads each one's index.json. Best-effort: a broken or
// missing zip is skipped, leaving those files unresolvable (the pick report then
// shows the bare filename).
func (a *auxIndex) ensure(cacheDir string) {
	a.mu.RLock()
	loaded := a.loaded
	a.mu.RUnlock()
	if loaded {
		return
	}
	entries := map[string]auxLoc{}
	_ = filepath.WalkDir(cacheDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".aux.zip") {
			return nil
		}
		readAuxZipIndex(path, entries)
		return nil
	})
	a.mu.Lock()
	a.entries = entries
	a.loaded = true
	a.mu.Unlock()
}

// auxIndexEntry mirrors one record of a companion zip's index.json (auxfiles.entry).
type auxIndexEntry struct {
	Stored string `json:"stored"`
	Type   string `json:"type"`
}

// readAuxZipIndex opens one companion aux.zip, decodes its index.json, and adds each
// referenced filename to out. Later zips win on a (rare) name clash — aux names are
// basenames and effectively district-unique.
func readAuxZipIndex(path string, out map[string]auxLoc) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return
	}
	defer zr.Close()
	for _, f := range zr.File {
		if f.Name != "index.json" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return
		}
		var meta struct {
			Files map[string]auxIndexEntry `json:"files"`
		}
		err = json.NewDecoder(rc).Decode(&meta)
		rc.Close()
		if err != nil {
			return
		}
		for name, e := range meta.Files {
			out[strings.ToUpper(name)] = auxLoc{zip: path, stored: e.Stored, typ: e.Type}
		}
		return
	}
}

// lookup resolves a referenced aux filename to its location, rebuilding first if stale.
func (a *auxIndex) lookup(cacheDir, name string) (auxLoc, bool) {
	a.ensure(cacheDir)
	a.mu.RLock()
	loc, ok := a.entries[strings.ToUpper(name)]
	a.mu.RUnlock()
	return loc, ok
}

// manifest returns NAME→MIME for every available aux file, rebuilding if stale. The
// client loads this once so the pick report knows which TXTDSC/PICREP refs resolve.
func (a *auxIndex) manifest(cacheDir string) map[string]string {
	a.ensure(cacheDir)
	a.mu.RLock()
	out := make(map[string]string, len(a.entries))
	for name, loc := range a.entries {
		out[name] = loc.typ
	}
	a.mu.RUnlock()
	return out
}

// serveAux serves ENC auxiliary content the pick report references by filename.
// GET /api/aux       → JSON {"files":{NAME:MIME,…}} of everything resolvable.
// GET /api/aux/<name> → that one file's bytes, extracted on demand from the cached
// companion aux.zip. The raw archive itself is never exposed.
func (s *Server) serveAux(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		apiErr(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	name := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, "/api/aux"), "/")
	if name == "" { // the manifest
		w.Header().Set("Content-Type", jsonCT)
		_ = json.NewEncoder(w).Encode(map[string]any{"files": s.auxIdx.manifest(s.cacheDir)})
		return
	}
	if dec, err := url.PathUnescape(name); err == nil {
		name = dec
	}
	loc, ok := s.auxIdx.lookup(s.cacheDir, name)
	if !ok {
		apiErr(w, http.StatusNotFound, "no such aux file")
		return
	}
	if err := writeAuxEntry(w, loc); err != nil {
		apiErr(w, http.StatusInternalServerError, err.Error())
	}
}

// writeAuxEntry streams one stored entry out of its companion zip to w with the
// indexed MIME type.
func writeAuxEntry(w http.ResponseWriter, loc auxLoc) error {
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
