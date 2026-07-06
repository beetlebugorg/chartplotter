package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/beetlebugorg/chartplotter/internal/engine/auxfiles"
)

// Aux ("auxiliary") files are the external resources an ENC feature points at by
// filename rather than carrying inline: TXTDSC/NTXTDS textual descriptions (.TXT)
// and PICREP pictures (.TIF/.JPG). They ship in the exchange set alongside the
// .000 cells. The baked tiles carry only the *filename* (in the s57 blob), so we
// lay the referenced files out as loose static files under a companion "<stem>-aux/"
// dir with an index.json — exactly what the server's import/bake writes (both go
// through internal/engine/auxfiles), so the pick report resolves attachments the
// same way whether the bundle came from the CLI or the C-ABI server bake, and it
// works OFFLINE as plain files with no zip and no server.

// isAuxContent reports whether a non-cell file is aux *content* we ship. It keys
// off the content extensions (text + pictures) and excludes the exchange-set
// catalogue (CATALOG.031) and readmes, which are set plumbing, not feature data.
func isAuxContent(name string) bool {
	base := strings.ToUpper(filepath.Base(name))
	if strings.HasPrefix(base, "README") || strings.HasPrefix(base, "CATALOG") {
		return false
	}
	switch strings.ToLower(filepath.Ext(name)) {
	case ".txt", ".tif", ".tiff", ".jpg", ".jpeg", ".png":
		return true
	}
	return false
}

// auxKey normalises an aux filename to the form features reference it by (the bare
// UPPER basename) — the shared key the pick report looks up by.
func auxKey(name string) string { return auxfiles.Key(name) }

// writeAuxDir lays the collected aux files out as loose static files under a
// companion "<stem>-aux/" dir (with an index.json), transcoding TIFF pictures to
// PNG. Returns the dir's manifest path RELATIVE to the bundle (for the charts-index
// "aux" field), or "" when there's nothing to ship.
func writeAuxDir(stem string, aux map[string][]byte) (string, error) {
	if len(aux) == 0 {
		return "", nil
	}
	dir := stem + "-aux"
	n, err := auxfiles.WriteDir(dir, aux)
	if err != nil {
		return "", err
	}
	fmt.Printf("wrote %d aux file(s) → %s/\n", n, dir)
	// The client resolves this relative to the manifest URL, so ship a relative path
	// (dir basename + index.json), not the absolute on-disk path.
	return filepath.Base(dir) + "/" + auxfiles.IndexName, nil
}

// collectAuxDir walks a directory tree for referenced aux content (TXTDSC /
// PICREP text + pictures) and returns it keyed like collectCells' aux map
// (auxKey — UPPER basename, first occurrence wins). Lets the streaming
// flat-archive bake ship the aux dir without reading any cell into memory.
func collectAuxDir(dir string) map[string][]byte {
	aux := map[string][]byte{}
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !isAuxContent(p) {
			return nil
		}
		if k := auxKey(p); aux[k] == nil {
			if b, e := os.ReadFile(p); e == nil {
				aux[k] = b
			}
		}
		return nil
	})
	return aux
}
