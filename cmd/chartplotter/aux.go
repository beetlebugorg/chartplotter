package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/image/tiff"
)

// Aux ("auxiliary") files are the external resources an ENC feature points at by
// filename rather than carrying inline: TXTDSC/NTXTDS textual descriptions (.TXT)
// and PICREP pictures (.TIF/.JPG). They ship in the exchange set alongside the
// .000 cells. The baked tiles carry only the *filename* (in the s57 blob), so we
// bundle the referenced files into a single companion archive — "<stem>-aux.zip"
// — that the client fetches once and reads by filename for the pick report.

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

// auxKey normalises an aux filename to the form features reference it by: the
// bare basename, upper-cased (S-57 stores TXTDSC/PICREP values upper-cased, and
// exchange sets are case-inconsistent across platforms).
func auxKey(name string) string { return strings.ToUpper(filepath.Base(name)) }

// auxEntry is one file's record in the companion zip's index.json.
type auxEntry struct {
	Stored string `json:"stored"`         // entry name inside the zip
	Type   string `json:"type"`           // MIME type the client should make a Blob with
	From   string `json:"from,omitempty"` // original filename, when transcoded (TIFF→PNG)
}

// contentType maps a stored filename to the MIME type the pick report renders.
func contentType(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".txt":
		return "text/plain"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".tif", ".tiff":
		return "image/tiff"
	}
	return "application/octet-stream"
}

// writeAuxZip packages the collected aux files into "<stem>-aux.zip" next to the
// baked archive(s), transcoding TIFF pictures to PNG (browsers can't render TIFF)
// and writing an index.json mapping each referenced filename to its stored entry.
// Returns the zip's basename (for the manifest), or "" when there's nothing to ship.
func writeAuxZip(stem string, aux map[string][]byte) (string, error) {
	if len(aux) == 0 {
		return "", nil
	}
	out := stem + "-aux.zip"
	f, err := os.Create(out)
	if err != nil {
		return "", err
	}
	zw := zip.NewWriter(f)

	index := map[string]auxEntry{}
	for key, data := range aux {
		stored := filepath.Base(key)
		typ := contentType(stored)
		from := ""

		// Browsers have no TIFF decoder; transcode to PNG so the picture renders
		// inline. On failure, ship the original and let the client offer a download.
		if ext := strings.ToLower(filepath.Ext(stored)); ext == ".tif" || ext == ".tiff" {
			if png, e := tiffToPNG(data); e == nil {
				from = stored
				stored = strings.TrimSuffix(stored, filepath.Ext(stored)) + ".png"
				typ = "image/png"
				data = png
			} else {
				fmt.Fprintf(os.Stderr, "  aux: keeping %s as TIFF (transcode failed: %v)\n", stored, e)
			}
		}

		w, err := zw.Create(stored)
		if err != nil {
			zw.Close()
			f.Close()
			return "", err
		}
		if _, err := w.Write(data); err != nil {
			zw.Close()
			f.Close()
			return "", err
		}
		index[key] = auxEntry{Stored: stored, Type: typ, From: from}
	}

	iw, err := zw.Create("index.json")
	if err != nil {
		zw.Close()
		f.Close()
		return "", err
	}
	enc := json.NewEncoder(iw)
	enc.SetIndent("", "  ")
	if err := enc.Encode(map[string]any{"version": 1, "files": index}); err != nil {
		zw.Close()
		f.Close()
		return "", err
	}

	if err := zw.Close(); err != nil {
		f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	st, _ := os.Stat(out)
	fmt.Printf("wrote %d aux file(s) → %s (%.1f KB)\n", len(index), out, float64(st.Size())/1024)
	return filepath.Base(out), nil
}

// tiffToPNG decodes a TIFF image and re-encodes it as PNG.
func tiffToPNG(data []byte) ([]byte, error) {
	img, err := tiff.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
