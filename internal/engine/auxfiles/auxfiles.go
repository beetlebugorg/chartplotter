// Package aux packages an ENC exchange set's auxiliary files — the external
// resources features point at by filename rather than carrying inline: TXTDSC/
// NTXTDS textual descriptions (.TXT) and PICREP pictures (.TIF/.JPG) — as LOOSE
// static files under an "aux/" directory the client reads by filename for the
// pick report. Pictures in TIFF (which browsers can't render) are transcoded to
// PNG; an index.json maps each referenced filename to its stored file. The loose
// layout works OFFLINE from any static host (SD card, file://, CDN) with no zip to
// download and no server extraction endpoint. Shared by the CLI bake and the
// server's import/bake.
package auxfiles

import (
	"bytes"
	"encoding/json"
	"image/png"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/image/tiff"
)

// Key normalises an aux filename to the form features reference it by: the bare
// basename, upper-cased (S-57 stores TXTDSC/PICREP values upper-cased, and exchange
// sets are case-inconsistent across platforms).
func Key(name string) string { return strings.ToUpper(filepath.Base(name)) }

// Entry is one file's record in the aux dir's index.json. Exported so readers (the
// server index, the CLI) can decode the manifest without redeclaring the shape.
type Entry struct {
	Stored string `json:"stored"`         // stored filename inside the aux dir
	Type   string `json:"type"`           // MIME type the client should make a Blob with
	From   string `json:"from,omitempty"` // original filename, when transcoded (TIFF→PNG)
}

// Manifest is the aux dir's index.json: a version tag plus every referenced
// filename (Key) → its stored file.
type Manifest struct {
	Version int              `json:"version"`
	Files   map[string]Entry `json:"files"`
}

// IndexName is the manifest filename written into (and read from) an aux dir.
const IndexName = "index.json"

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

// transcode resolves one aux file to the (stored filename, MIME type, original name
// when transcoded, bytes to write). TIFF pictures browsers can't render are re-encoded
// to PNG; everything else is stored verbatim.
func transcode(key string, data []byte) (stored, typ, from string, out []byte) {
	stored = filepath.Base(key)
	typ = contentType(stored)
	out = data
	if ext := strings.ToLower(filepath.Ext(stored)); ext == ".tif" || ext == ".tiff" {
		if pngBytes, e := tiffToPNG(data); e == nil {
			from = stored
			stored = strings.TrimSuffix(stored, filepath.Ext(stored)) + ".png"
			typ = "image/png"
			out = pngBytes
		}
	}
	return
}

// WriteDir writes the aux files (keyed by Key) as LOOSE static files under dir,
// transcoding TIFF pictures to PNG and writing an index.json mapping each referenced
// filename to its stored file. The client fetches dir/index.json then dir/<stored>
// by plain HTTP — no zip to download, no server extraction endpoint, so it works
// offline from a static export. Returns the number of files written; an empty map
// writes nothing and returns (0, nil) — callers should skip emitting a dir then.
func WriteDir(dir string, files map[string][]byte) (int, error) {
	if len(files) == 0 {
		return 0, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, err
	}
	index := map[string]Entry{}
	for key, data := range files {
		stored, typ, from, out := transcode(key, data)
		if err := os.WriteFile(filepath.Join(dir, stored), out, 0o644); err != nil {
			return 0, err
		}
		index[key] = Entry{Stored: stored, Type: typ, From: from}
	}
	b, err := json.MarshalIndent(Manifest{Version: 1, Files: index}, "", "  ")
	if err != nil {
		return 0, err
	}
	if err := os.WriteFile(filepath.Join(dir, IndexName), b, 0o644); err != nil {
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
