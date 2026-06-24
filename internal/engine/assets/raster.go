package assets

// Atlas packing helpers shared by the sprite / pattern emitters. The actual
// glyph rasterisation is done from SVG via oksvg/rasterx (see pkg/s100/symbols);
// this file holds the page constants and the buffer-blit used to pack rasterised
// cells into an atlas.

const (
	pxPerUnit   = 0.08 // device px per 0.01-mm symbol unit (8 px/mm)
	atlasPad    = 1    // final-px gap between atlas cells
	maxCellSide = 640  // skip pathological cells larger than this (final px)
)

// raster is a rasterised-but-not-yet-packed cell.
type raster struct {
	name   string
	w, h   uint32
	pivotX float32
	pivotY float32
	rgba   []byte
}

// blit copies a cell's RGBA into the atlas at (dx, dy).
func blit(atlas []byte, atlasW uint32, cell []byte, cw, ch, dx, dy uint32) {
	for y := uint32(0); y < ch; y++ {
		srcOff := int(y) * int(cw) * 4
		dstOff := (int(dy+y)*int(atlasW) + int(dx)) * 4
		copy(atlas[dstOff:dstOff+int(cw)*4], cell[srcOff:srcOff+int(cw)*4])
	}
}

func maxU32(a, b uint32) uint32 {
	if a > b {
		return a
	}
	return b
}
