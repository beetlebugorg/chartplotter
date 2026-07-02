package pmtiles

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"os"
)

// Reader provides random tile access to a PMTiles v3 archive written by Builder.
// It is the mirror of WriteArchive/serializeDir: parse the 127-byte header, then
// walk the root directory (descending into a leaf page when the archive is large)
// to find a tile's (offset,length) in the data section. Directories are read once
// and cached; tile bodies are read per request via the ReaderAt.
//
// Only the subset Builder emits is supported: uncompressed directories, MVT tiles
// (optionally gzipped), single root until >leafSize entries then one leaf level.
type Reader struct {
	src     io.ReaderAt
	closer  io.Closer
	root    []entry
	leaves  []byte // raw leaf-directory section (parsed on demand)
	leafOff uint64
	dataOff uint64
	tileGz  bool
	meta    TileMeta
}

// TileMeta is an archive's display metadata (header fields + JSON metadata).
type TileMeta struct {
	MinZoom, MaxZoom uint8
	W, S, E, N       float64  // lon/lat bounds (degrees)
	Gzipped          bool     // tile bodies are gzip-compressed
	Scamin           []uint32 // distinct SCAMIN denominators present (from JSON metadata)
	// TileType is the stored tile encoding: "mvt" or "mlt" (a tile57 MLT-default
	// bake); "" for an unrecognised header type. Serving stays bytes-verbatim
	// either way — this only drives the TileJSON/style `encoding` hint (and the
	// tile content type) so maplibre-gl picks the matching decoder.
	TileType string
}

// Open opens a .pmtiles file for reading. Close releases the file handle.
func Open(path string) (*Reader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	rd, err := NewReader(f, st.Size())
	if err != nil {
		f.Close()
		return nil, err
	}
	rd.closer = f
	return rd, nil
}

// NewReader parses the header + root directory from an archive of the given size.
func NewReader(src io.ReaderAt, size int64) (*Reader, error) {
	if size < 127 {
		return nil, errors.New("pmtiles: too small for a header")
	}
	var h [127]byte
	if _, err := src.ReadAt(h[:], 0); err != nil {
		return nil, err
	}
	if string(h[0:7]) != "PMTiles" || h[7] != 3 {
		return nil, errors.New("pmtiles: bad magic or version")
	}
	rootOff := binary.LittleEndian.Uint64(h[8:16])
	rootLen := binary.LittleEndian.Uint64(h[16:24])
	metaOff := binary.LittleEndian.Uint64(h[24:32])
	metaLen := binary.LittleEndian.Uint64(h[32:40])
	leafOff := binary.LittleEndian.Uint64(h[40:48])
	dataOff := binary.LittleEndian.Uint64(h[56:64])

	rootBytes := make([]byte, rootLen)
	if _, err := src.ReadAt(rootBytes, int64(rootOff)); err != nil {
		return nil, err
	}
	root, err := deserializeDir(rootBytes)
	if err != nil {
		return nil, err
	}

	rd := &Reader{
		src:     src,
		root:    root,
		leafOff: leafOff,
		dataOff: dataOff,
		tileGz:  h[98] == compressionGzip,
		meta: TileMeta{
			MinZoom:  h[100],
			MaxZoom:  h[101],
			W:        float64(int32(binary.LittleEndian.Uint32(h[102:106]))) / 1e7,
			S:        float64(int32(binary.LittleEndian.Uint32(h[106:110]))) / 1e7,
			E:        float64(int32(binary.LittleEndian.Uint32(h[110:114]))) / 1e7,
			N:        float64(int32(binary.LittleEndian.Uint32(h[114:118]))) / 1e7,
			Gzipped:  h[98] == compressionGzip,
			TileType: tileTypeName(h[99]),
		},
	}
	// JSON metadata (between metaOff and leafOff): parse the SCAMIN manifest so the
	// client can build per-SCAMIN bucket layers at load. Best-effort — absence just
	// means the older runtime-collection path is used.
	if metaLen > 0 && metaLen < 1<<20 {
		mb := make([]byte, metaLen)
		if _, err := src.ReadAt(mb, int64(metaOff)); err == nil {
			var md struct {
				Scamin []uint32 `json:"scamin"`
			}
			if json.Unmarshal(mb, &md) == nil {
				rd.meta.Scamin = md.Scamin
			}
		}
	}
	// Leaf section sits between leafOff and dataOff; load it once if present.
	if dataOff > leafOff {
		rd.leaves = make([]byte, dataOff-leafOff)
		if _, err := src.ReadAt(rd.leaves, int64(leafOff)); err != nil {
			return nil, err
		}
	}
	return rd, nil
}

// Meta returns the archive's header metadata.
func (rd *Reader) Meta() TileMeta { return rd.meta }

// Close releases the underlying file, if Reader owns one.
func (rd *Reader) Close() error {
	if rd.closer != nil {
		return rd.closer.Close()
	}
	return nil
}

// Tile returns the (decompressed) MVT body for z/x/y, or nil if the archive holds
// no tile there (a missing tile reads as blank, matching the writer's contract).
func (rd *Reader) Tile(z uint8, x, y uint32) ([]byte, error) {
	id := ZxyToTileID(z, x, y)

	e, ok := findEntry(rd.root, id)
	if !ok {
		return nil, nil
	}
	if e.runLength == 0 { // leaf pointer: descend one level
		leaf, err := deserializeDir(rd.leaves[e.offset : e.offset+e.length])
		if err != nil {
			return nil, err
		}
		e, ok = findEntry(leaf, id)
		if !ok || e.runLength == 0 {
			return nil, nil
		}
	}

	body := make([]byte, e.length)
	if _, err := rd.src.ReadAt(body, int64(rd.dataOff+e.offset)); err != nil {
		return nil, err
	}
	if rd.tileGz {
		return gunzip(body)
	}
	return body, nil
}

// findEntry returns the directory entry covering tileID: the greatest entry whose
// tileID <= target, provided target falls within its run (runLength 0 == a leaf
// pointer, which the caller descends). Entries are sorted by tileID.
func findEntry(entries []entry, target uint64) (entry, bool) {
	lo, hi := 0, len(entries)
	for lo < hi { // upper_bound: first entry with tileID > target
		mid := (lo + hi) / 2
		if entries[mid].tileID <= target {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo == 0 {
		return entry{}, false
	}
	e := entries[lo-1]
	if e.runLength == 0 { // leaf pointer covers everything up to the next entry
		return e, true
	}
	if target-e.tileID < uint64(e.runLength) {
		return e, true
	}
	return entry{}, false
}

// deserializeDir is the inverse of serializeDir.
func deserializeDir(buf []byte) ([]entry, error) {
	r := &bytesReader{b: buf}
	n, err := r.uvarint()
	if err != nil {
		return nil, err
	}
	entries := make([]entry, n)

	var prev uint64
	for i := range entries { // delta-encoded tile ids
		d, err := r.uvarint()
		if err != nil {
			return nil, err
		}
		prev += d
		entries[i].tileID = prev
	}
	for i := range entries { // run lengths
		v, err := r.uvarint()
		if err != nil {
			return nil, err
		}
		entries[i].runLength = uint32(v)
	}
	for i := range entries { // lengths
		v, err := r.uvarint()
		if err != nil {
			return nil, err
		}
		entries[i].length = v
	}
	for i := range entries { // offsets: 0 == contiguous with previous, else value-1
		v, err := r.uvarint()
		if err != nil {
			return nil, err
		}
		if v == 0 && i > 0 {
			entries[i].offset = entries[i-1].offset + entries[i-1].length
		} else {
			entries[i].offset = v - 1
		}
	}
	return entries, nil
}

type bytesReader struct {
	b []byte
	i int
}

func (r *bytesReader) uvarint() (uint64, error) {
	v, n := binary.Uvarint(r.b[r.i:])
	if n <= 0 {
		return 0, errors.New("pmtiles: truncated varint in directory")
	}
	r.i += n
	return v, nil
}

func gunzip(b []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	return io.ReadAll(zr)
}
