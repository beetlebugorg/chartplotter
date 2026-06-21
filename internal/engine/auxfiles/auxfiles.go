// Package aux packages an ENC exchange set's auxiliary files — the external
// resources features point at by filename rather than carrying inline: TXTDSC/
// NTXTDS textual descriptions (.TXT) and PICREP pictures (.TIF/.JPG) — into a
// single companion archive ("<stem>-aux.zip") the client fetches once and reads by
// filename for the pick report. Pictures in TIFF (which browsers can't render) are
// transcoded to PNG; an index.json maps each referenced filename to its stored
// entry. Shared by the CLI bake and the server's import/bake.
package auxfiles

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"image/png"
	"io"
	"path/filepath"
	"strings"

	"golang.org/x/image/tiff"
)

// IsContent reports whether a non-cell file is aux *content* we ship. It keys off
// the content extensions (text + pictures) and excludes the exchange-set catalogue
// (CATALOG.031) and readmes, which are set plumbing, not feature data.
func IsContent(name string) bool {
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

// Key normalises an aux filename to the form features reference it by: the bare
// basename, upper-cased (S-57 stores TXTDSC/PICREP values upper-cased, and exchange
// sets are case-inconsistent across platforms).
func Key(name string) string { return strings.ToUpper(filepath.Base(name)) }

// entry is one file's record in the companion zip's index.json.
type entry struct {
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

// WriteZip writes the aux files (keyed by Key) as a companion zip to w, transcoding
// TIFF pictures to PNG and writing an index.json mapping each referenced filename
// to its stored entry. Returns the number of files written. A zero-length aux map
// writes nothing and returns (0, nil) — callers should skip emitting a file then.
func WriteZip(w io.Writer, files map[string][]byte) (int, error) {
	if len(files) == 0 {
		return 0, nil
	}
	zw := zip.NewWriter(w)
	index := map[string]entry{}
	for key, data := range files {
		stored := filepath.Base(key)
		typ := contentType(stored)
		from := ""
		// Browsers have no TIFF decoder; transcode to PNG so the picture renders
		// inline. On failure, ship the original and let the client offer a download.
		if ext := strings.ToLower(filepath.Ext(stored)); ext == ".tif" || ext == ".tiff" {
			if pngBytes, e := tiffToPNG(data); e == nil {
				from = stored
				stored = strings.TrimSuffix(stored, filepath.Ext(stored)) + ".png"
				typ = "image/png"
				data = pngBytes
			}
		}
		fw, err := zw.Create(stored)
		if err != nil {
			zw.Close()
			return 0, err
		}
		if _, err := fw.Write(data); err != nil {
			zw.Close()
			return 0, err
		}
		index[key] = entry{Stored: stored, Type: typ, From: from}
	}
	iw, err := zw.Create("index.json")
	if err != nil {
		zw.Close()
		return 0, err
	}
	enc := json.NewEncoder(iw)
	enc.SetIndent("", "  ")
	if err := enc.Encode(map[string]any{"version": 1, "files": index}); err != nil {
		zw.Close()
		return 0, err
	}
	if err := zw.Close(); err != nil {
		return 0, err
	}
	return len(index), nil
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
