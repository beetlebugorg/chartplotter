package assets

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"math"
	"sort"

	"github.com/beetlebugorg/chartplotter-go/pkg/s52"
)

// Sprite / pattern atlas exporter. Port of sprites.zig: every PresLib symbol
// (and pattern tile) is run through the HP-GL interpreter, rasterised with AA,
// shelf-packed into one 512-px-wide RGBA atlas, and described by JSON the
// MapLibre client references as Icon sub-rects. Pen colours resolve to the Day
// table (the only place RGB is baked into the artwork).

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

// dayColors builds token -> RGB from the Day colour table, using the same
// rounding as colortables.json (ConvertToRGB then *255+0.5).
func dayColors(lib *s52.Library) (map[string]rcolor, error) {
	cols, err := lib.GetColorsByScheme(s52.ColorSchemeDay)
	if err != nil {
		return nil, err
	}
	out := make(map[string]rcolor, len(cols))
	to8 := func(v float64) uint8 {
		n := int(v*255.0 + 0.5)
		if n < 0 {
			n = 0
		}
		if n > 255 {
			n = 255
		}
		return uint8(n)
	}
	for tok, c := range cols {
		r, g, b := c.ConvertToRGB()
		out[tok] = rcolor{to8(r), to8(g), to8(b)}
	}
	return out, nil
}

// buildSymbolAtlas rasterises and packs every PresLib symbol.
func buildSymbolAtlas(lib *s52.Library) (atlas, error) {
	day, err := dayColors(lib)
	if err != nil {
		return atlas{}, err
	}
	scale := float32(pxPerUnit * supersample)
	var rasters []raster
	skipped := 0
	for _, id := range lib.ListSymbols() {
		sym, err := lib.GetSymbol(id)
		if err != nil {
			continue
		}
		roles := s52.ParseSCRF(sym.ColorRef)
		cmds := renderOps(sym.VectorCommands, sym.PivotPoint.X, sym.PivotPoint.Y, roles, day, scale)
		if r, ok := cellFromCommands(id, cmds, &skipped); ok {
			rasters = append(rasters, r)
		}
	}
	return packInto(rasters, skipped), nil
}

// buildPatternAtlas rasterises and packs every PresLib pattern into a seamless
// tile sized to its spacing (×2 rows when staggered), with the art stamped at
// its 8 neighbours so it tiles without a seam.
func buildPatternAtlas(lib *s52.Library) (atlas, error) {
	day, err := dayColors(lib)
	if err != nil {
		return atlas{}, err
	}
	scale := float32(pxPerUnit * supersample)
	var rasters []raster
	skipped := 0
	for _, id := range lib.ListPatterns() {
		pat, err := lib.GetPattern(id)
		if err != nil {
			continue
		}
		if r, ok := rasterizePattern(pat, day, scale, &skipped); ok {
			rasters = append(rasters, r)
		}
	}
	return packInto(rasters, skipped), nil
}

// rasterizePattern renders one Pattern into a seamless tile. Pattern
// VectorCommands are already normalised so the bbox upper-left is at the origin;
// the registration pivot is therefore the bbox centre (w/2, h/2). Port of
// sprites.zig rasterizePattern.
func rasterizePattern(pat *s52.Pattern, day map[string]rcolor, scale float32, skipped *int) (raster, bool) {
	roles := pat.Colors.Roles
	// Pivot = bbox centre, in the normalised coordinate space.
	pivotX := float64(pat.TileWidth / 2)
	pivotY := float64(pat.TileHeight / 2)
	cmds := renderOps(pat.VectorCommands, pivotX, pivotY, roles, day, scale)
	if len(cmds) == 0 {
		return raster{}, false
	}

	twUnits := maxInt(pat.SpacingX, pat.TileWidth)
	thUnits := maxInt(pat.SpacingY, pat.TileHeight)
	if twUnits == 0 || thUnits == 0 {
		return raster{}, false
	}

	twSS := roundUp(maxU32(1, uint32(math.Ceil(float64(float32(twUnits)*scale)))), supersample)
	thOne := roundUp(maxU32(1, uint32(math.Ceil(float64(float32(thUnits)*scale)))), supersample)
	rows := uint32(1)
	if patStaggered(pat) {
		rows = 2
	}
	thSS := thOne * rows
	if twSS/supersample > maxCellSide || thSS/supersample > maxCellSide {
		*skipped++
		return raster{}, false
	}

	buf := make([]byte, int(twSS)*int(thSS)*4)

	// Grid positions (ss px) within one tile; staggered adds a half-offset row.
	fwSS := float32(twSS)
	fh1 := float32(thOne)
	positions := [][2]float32{{fwSS / 2, fh1 / 2}}
	if patStaggered(pat) {
		positions = append(positions, [2]float32{fwSS, fh1 * 1.5})
	}

	// Stamp each position plus its 8 neighbours (full tile period) so
	// edge-crossing art wraps in — a clean repeat.
	pw := float32(twSS)
	ph := float32(thSS)
	for _, pos := range positions {
		for dj := float32(-1); dj <= 1; dj++ {
			for di := float32(-1); di <= 1; di++ {
				rasterizeCommands(buf, twSS, thSS, cmds, pos[0]+di*pw, pos[1]+dj*ph)
			}
		}
	}

	fw := twSS / supersample
	fh := thSS / supersample
	final := make([]byte, int(fw)*int(fh)*4)
	downsample(final, buf, twSS, thSS)
	return raster{name: pat.ID, w: fw, h: fh, pivotX: 0, pivotY: 0, rgba: final}, true
}

// patStaggered reports whether the pattern tiles on a staggered grid (PATP STG).
func patStaggered(pat *s52.Pattern) bool {
	return len(pat.PatternType) >= 3 && pat.PatternType[:3] == "STG"
}

// packInto shelf-packs already-rasterised cells (tallest-first) into one atlas.
func packInto(rasters []raster, skipped int) atlas {
	sort.SliceStable(rasters, func(i, j int) bool { return rasters[i].h > rasters[j].h })

	cells := make(map[string]cell, len(rasters))
	penX := uint32(atlasPad)
	penY := uint32(atlasPad)
	shelfH := uint32(0)
	totalH := uint32(atlasPad)
	for _, r := range rasters {
		if penX+r.w+atlasPad > atlasWidth {
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

	rgba := make([]byte, int(atlasWidth)*int(height)*4)
	for _, r := range rasters {
		c := cells[r.name]
		blit(rgba, atlasWidth, r.rgba, r.w, r.h, c.x, c.y)
	}

	return atlas{width: atlasWidth, height: height, rgba: rgba, cells: cells, skipped: skipped}
}

// toJSON renders the atlas description (_meta + per-name cells), names sorted.
// Matches the Zig float formatting (px_per_unit plain, pivots %.2f).
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

// SpriteAtlas builds the symbol atlas and returns its JSON + PNG bytes.
func SpriteAtlas(lib *s52.Library) (jsonBytes, pngBytes []byte, err error) {
	a, err := buildSymbolAtlas(lib)
	if err != nil {
		return nil, nil, err
	}
	pngBytes, err = a.encodePNG()
	if err != nil {
		return nil, nil, err
	}
	return a.toJSON(), pngBytes, nil
}

// PatternAtlas builds the pattern atlas and returns its JSON + PNG bytes.
func PatternAtlas(lib *s52.Library) (jsonBytes, pngBytes []byte, err error) {
	a, err := buildPatternAtlas(lib)
	if err != nil {
		return nil, nil, err
	}
	pngBytes, err = a.encodePNG()
	if err != nil {
		return nil, nil, err
	}
	return a.toJSON(), pngBytes, nil
}

// fmtNum formats a float without trailing zeros (matches Zig {d} for px_per_unit).
func fmtNum(v float64) string {
	s := fmt.Sprintf("%g", v)
	return s
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
