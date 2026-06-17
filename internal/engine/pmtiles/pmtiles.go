// Package pmtiles is a minimal PMTiles v3 archive writer. PMTiles packs a whole
// tile pyramid into one file: a 127-byte header, a Hilbert-ordered directory
// (sharded into leaf pages when large), JSON metadata, and the concatenated tile
// blobs. The browser reads tiles from it directly (pmtiles.js + MapLibre's
// pmtiles:// protocol) — no tile server.
//
// Single root directory until large, no per-tile compression, MVT tile type,
// content-deduped (identical tiles stored once). Output is a deterministic
// function of the (tile-id -> bytes) set, independent of insertion order.
//
// Spec: https://github.com/protomaps/PMTiles/blob/main/spec/v3/spec.md
package pmtiles

import (
	"encoding/binary"
	"hash/fnv"
	"io"
	"math"
	"sort"
)

const (
	tileTypeMVT     uint8 = 1
	compressionNone uint8 = 1
	leafSize              = 4096
)

// ZxyToTileID maps (z,x,y) to a PMTiles tile id: tiles below this zoom plus the
// Hilbert-curve index within the zoom. Matches the spec's zxy_to_tileid.
func ZxyToTileID(z uint8, x, y uint32) uint64 {
	var acc, base uint64 = 0, 1
	for i := uint8(0); i < z; i++ {
		acc += base
		base *= 4
	}
	side := uint64(1) << z
	xx, yy := uint64(x), uint64(y)
	var d uint64
	for s := side / 2; s > 0; s /= 2 {
		var rx, ry uint64
		if xx&s > 0 {
			rx = 1
		}
		if yy&s > 0 {
			ry = 1
		}
		d += s * s * ((3 * rx) ^ ry)
		if ry == 0 {
			if rx == 1 {
				xx = side - 1 - xx
				yy = side - 1 - yy
			}
			xx, yy = yy, xx
		}
	}
	return acc + d
}

func appendVarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

// entry is a directory entry. runLength==0 marks a leaf-directory pointer;
// runLength>=1 is a tile run (we only use 1 = tile, 0 = leaf ptr).
type entry struct {
	tileID    uint64
	offset    uint64
	length    uint64
	runLength uint32
}

type offLen struct{ offset, length uint64 }

// Builder accumulates tiles and writes a PMTiles archive. Tile bytes are held in
// an in-memory blob (deduped). The only per-tile state is the directory entry.
type Builder struct {
	entries    []entry
	blob       []byte
	dataLen    uint64
	dedup      map[uint64]offLen
	minZ       uint8
	maxZ       uint8
	w, s, e, n float64
}

// New returns an empty Builder.
func New() *Builder {
	return &Builder{
		dedup: map[uint64]offLen{},
		minZ:  255,
		w:     180, s: 90, e: -180, n: -90,
	}
}

// AddTile adds one tile. Empty tiles are dropped (a missing tile reads as blank).
// Identical bytes are deduped (stored once, pointed at by multiple entries).
func (b *Builder) AddTile(z uint8, x, y uint32, bytes []byte) {
	if len(bytes) == 0 {
		return
	}
	h := fnv.New64a()
	_, _ = h.Write(bytes)
	key := h.Sum64()

	offset := b.dataLen
	if ol, ok := b.dedup[key]; ok {
		if ol.length == uint64(len(bytes)) && b.blobEquals(ol.offset, bytes) {
			offset = ol.offset
		}
	}
	if offset == b.dataLen { // not a dedup hit -> commit the bytes
		b.blob = append(b.blob, bytes...)
		b.dataLen += uint64(len(bytes))
		b.dedup[key] = offLen{offset: offset, length: uint64(len(bytes))}
	}

	b.entries = append(b.entries, entry{tileID: ZxyToTileID(z, x, y), offset: offset, length: uint64(len(bytes)), runLength: 1})
	if z < b.minZ {
		b.minZ = z
	}
	if z > b.maxZ {
		b.maxZ = z
	}
	scale := math.Pow(2, float64(z))
	lon0 := float64(x)/scale*360 - 180
	lon1 := float64(x+1)/scale*360 - 180
	b.w = math.Min(b.w, lon0)
	b.e = math.Max(b.e, lon1)
	b.n = math.Max(b.n, tile2lat(y, z))
	b.s = math.Min(b.s, tile2lat(y+1, z))
}

func (b *Builder) blobEquals(offset uint64, bytes []byte) bool {
	if offset+uint64(len(bytes)) > uint64(len(b.blob)) {
		return false
	}
	return string(b.blob[offset:offset+uint64(len(bytes))]) == string(bytes)
}

// Count returns the number of addressed tiles.
func (b *Builder) Count() int { return len(b.entries) }

// WriteArchive streams the whole archive to out: header + directories + metadata, then
// the data section. The archive depends only on the (tile-id -> bytes) set.
func (b *Builder) WriteArchive(out io.Writer) error {
	sort.Slice(b.entries, func(i, j int) bool { return b.entries[i].tileID < b.entries[j].tileID })

	// Assign final data offsets by walking entries in tile-id order, deduping by
	// sink offset (identical tiles already share one blob via AddTile).
	finalOf := map[uint64]uint64{}
	dir := make([]entry, 0, len(b.entries))
	var dataLen uint64
	for _, e := range b.entries {
		fo, ok := finalOf[e.offset]
		if !ok {
			fo = dataLen
			finalOf[e.offset] = fo
			dataLen += e.length
		}
		dir = append(dir, entry{tileID: e.tileID, offset: fo, length: e.length, runLength: 1})
	}

	// Build root + (optional) leaf directories.
	var root, leaves []byte
	if len(dir) <= leafSize {
		root = serializeDir(dir)
	} else {
		var rootEntries []entry
		for i := 0; i < len(dir); i += leafSize {
			end := i + leafSize
			if end > len(dir) {
				end = len(dir)
			}
			chunk := dir[i:end]
			leaf := serializeDir(chunk)
			rootEntries = append(rootEntries, entry{tileID: chunk[0].tileID, offset: uint64(len(leaves)), length: uint64(len(leaf)), runLength: 0})
			leaves = append(leaves, leaf...)
		}
		root = serializeDir(rootEntries)
	}

	meta := []byte(metadataJSON)
	const rootOff uint64 = 127
	rootLen := uint64(len(root))
	metaOff := rootOff + rootLen
	metaLen := uint64(len(meta))
	leafOff := metaOff + metaLen
	leafLen := uint64(len(leaves))
	dataOff := leafOff + leafLen
	nEntries := uint64(len(b.entries))
	unique := uint64(len(finalOf))
	minZ := b.minZ
	if minZ == 255 {
		minZ = 0
	}

	var h [127]byte
	copy(h[0:7], "PMTiles")
	h[7] = 3
	binary.LittleEndian.PutUint64(h[8:16], rootOff)
	binary.LittleEndian.PutUint64(h[16:24], rootLen)
	binary.LittleEndian.PutUint64(h[24:32], metaOff)
	binary.LittleEndian.PutUint64(h[32:40], metaLen)
	binary.LittleEndian.PutUint64(h[40:48], leafOff)
	binary.LittleEndian.PutUint64(h[48:56], leafLen)
	binary.LittleEndian.PutUint64(h[56:64], dataOff)
	binary.LittleEndian.PutUint64(h[64:72], dataLen)
	binary.LittleEndian.PutUint64(h[72:80], nEntries) // addressed tiles
	binary.LittleEndian.PutUint64(h[80:88], nEntries) // tile entries
	binary.LittleEndian.PutUint64(h[88:96], unique)   // tile contents (deduped)
	h[96] = 0                                         // not clustered (dedup back-references)
	h[97] = compressionNone
	h[98] = compressionNone
	h[99] = tileTypeMVT
	h[100] = minZ
	h[101] = b.maxZ
	has := len(b.entries) > 0
	putI32 := func(off int, real, def int32) {
		v := def
		if has {
			v = real
		}
		binary.LittleEndian.PutUint32(h[off:off+4], uint32(v))
	}
	putI32(102, e7(b.w), -1800000000)
	putI32(106, e7(b.s), -850000000)
	putI32(110, e7(b.e), 1800000000)
	putI32(114, e7(b.n), 850000000)
	h[118] = minZ // center zoom
	putI32(119, e7((b.w+b.e)/2), 0)
	putI32(123, e7((b.s+b.n)/2), 0)

	if _, err := out.Write(h[:]); err != nil {
		return err
	}
	if _, err := out.Write(root); err != nil {
		return err
	}
	if _, err := out.Write(meta); err != nil {
		return err
	}
	if _, err := out.Write(leaves); err != nil {
		return err
	}

	// Data section: each distinct blob written once, in tile-id order. An entry is
	// the first reference to its blob exactly when its final offset == the running
	// write position.
	var written uint64
	for _, e := range b.entries {
		if finalOf[e.offset] != written {
			continue // back-reference, already written
		}
		if _, err := out.Write(b.blob[e.offset : e.offset+e.length]); err != nil {
			return err
		}
		written += e.length
	}
	return nil
}

// Finish assembles the whole archive into a freshly-allocated buffer.
func (b *Builder) Finish() []byte {
	var buf sliceWriter
	_ = b.WriteArchive(&buf)
	return buf.data
}

type sliceWriter struct{ data []byte }

func (s *sliceWriter) Write(p []byte) (int, error) {
	s.data = append(s.data, p...)
	return len(p), nil
}

func e7(d float64) int32 { return int32(d * 1e7) }

// tile2lat is the north-edge latitude (degrees) of tile y at zoom z (Web Mercator).
func tile2lat(y uint32, z uint8) float64 {
	scale := math.Pow(2, float64(z))
	t := math.Pi - 2*math.Pi*float64(y)/scale
	return math.Atan(math.Sinh(t)) * 180 / math.Pi
}

func serializeDir(entries []entry) []byte {
	var buf []byte
	buf = appendVarint(buf, uint64(len(entries)))
	var prev uint64
	for _, e := range entries {
		buf = appendVarint(buf, e.tileID-prev)
		prev = e.tileID
	}
	for _, e := range entries {
		buf = appendVarint(buf, uint64(e.runLength))
	}
	for _, e := range entries {
		buf = appendVarint(buf, e.length)
	}
	for i, e := range entries {
		if i > 0 && e.offset == entries[i-1].offset+entries[i-1].length {
			buf = appendVarint(buf, 0) // contiguous with previous
		} else {
			buf = appendVarint(buf, e.offset+1)
		}
	}
	return buf
}

// metadataJSON lists the vector layers MapLibre reads from the archive — these
// match the layer names the baker emits.
const metadataJSON = `{"name":"chartplotter","format":"pbf","vector_layers":[{"id":"areas","fields":{}},{"id":"area_patterns","fields":{}},{"id":"lines","fields":{}},{"id":"complex_lines","fields":{}},{"id":"point_symbols","fields":{}},{"id":"soundings","fields":{}},{"id":"text","fields":{}}]}`
