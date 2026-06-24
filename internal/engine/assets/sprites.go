package assets

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"sort"
)

// Sprite / pattern atlas packer + encoder: already-rasterised cells are
// shelf-packed into one RGBA atlas and described by JSON the MapLibre client
// references as Icon sub-rects. The S-101 sprite/pattern builders
// (sprites_s101.go, patterns_s101.go) feed this; colour is the only place RGB
// is baked into the artwork.

// cell is one packed atlas cell.
type cell struct {
	x, y, w, h     uint32
	pivotX, pivotY float32
}

// atlas is a packed sprite/pattern atlas.
type atlas struct {
	width, height uint32
	rgba          []byte
	cells         map[string]cell
	skipped       int
}

// packInto shelf-packs already-rasterised cells (tallest-first) into one atlas
// of the given width. width must stay ≤ the WebGL MAX_TEXTURE_SIZE (commonly
// 4096 in Chrome) and so must the resulting height — pick a width wide enough
// that the atlas doesn't grow taller than that, or icons render broken (the
// whole atlas is one GL texture in the client).
func packInto(rasters []raster, skipped int, width uint32) atlas {
	sort.SliceStable(rasters, func(i, j int) bool { return rasters[i].h > rasters[j].h })

	cells := make(map[string]cell, len(rasters))
	penX := uint32(atlasPad)
	penY := uint32(atlasPad)
	shelfH := uint32(0)
	totalH := uint32(atlasPad)
	for _, r := range rasters {
		if penX+r.w+atlasPad > width {
			penX = atlasPad
			penY += shelfH + atlasPad
			shelfH = 0
		}
		cells[r.name] = cell{x: penX, y: penY, w: r.w, h: r.h, pivotX: r.pivotX, pivotY: r.pivotY}
		penX += r.w + atlasPad
		shelfH = maxU32(shelfH, r.h)
		totalH = maxU32(totalH, penY+r.h+atlasPad)
	}
	height := maxU32(totalH, 1)

	rgba := make([]byte, int(width)*int(height)*4)
	for _, r := range rasters {
		c := cells[r.name]
		blit(rgba, width, r.rgba, r.w, r.h, c.x, c.y)
	}

	return atlas{width: width, height: height, rgba: rgba, cells: cells, skipped: skipped}
}

// toJSON renders the atlas description (_meta + per-name cells), names sorted.
// Float formatting: px_per_unit plain, pivots %.2f.
func (a atlas) toJSON() []byte {
	names := make([]string, 0, len(a.cells))
	for n := range a.cells {
		names = append(names, n)
	}
	sort.Strings(names)

	var buf bytes.Buffer
	fmt.Fprintf(&buf, "{\n  \"_meta\": { \"px_per_unit\": %s, \"width\": %d, \"height\": %d }", fmtNum(pxPerUnit), a.width, a.height)
	for _, n := range names {
		c := a.cells[n]
		fmt.Fprintf(&buf, ",\n  %q: { \"x\": %d, \"y\": %d, \"w\": %d, \"h\": %d, \"pivot_x\": %.2f, \"pivot_y\": %.2f }",
			n, c.x, c.y, c.w, c.h, c.pivotX, c.pivotY)
	}
	buf.WriteString("\n}\n")
	return buf.Bytes()
}

// encodePNG encodes the atlas RGBA as a PNG.
func (a atlas) encodePNG() ([]byte, error) {
	img := &image.NRGBA{
		Pix:    a.rgba,
		Stride: int(a.width) * 4,
		Rect:   image.Rect(0, 0, int(a.width), int(a.height)),
	}
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestCompression}
	if err := enc.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// fmtNum formats a float without trailing zeros (used for px_per_unit).
func fmtNum(v float64) string {
	s := fmt.Sprintf("%g", v)
	return s
}
