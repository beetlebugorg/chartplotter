package assets

import (
	"fmt"
	"image"
	"io/fs"
	"math"

	"github.com/beetlebugorg/chartplotter/pkg/s100/catalog"
	"github.com/beetlebugorg/chartplotter/pkg/s100/symbols"
)

// PatternAtlasS101FS builds the area-fill pattern atlas (patterns.{png,json})
// from the S-101 AreaFills: each fill rasterizes its referenced symbol and tiles
// it on the v1/v2 lattice into a seamless rectangular tile (one GL fill-pattern
// per fill name).
//
// v1 is always horizontal in the S-101 catalogue, so a STRAIGHT lattice (v2.x==0)
// is a v1.x × v2.y rectangle with the symbol stamped at the cell centre plus its
// 8 neighbours (edge-crossing art wraps cleanly). A STAGGERED lattice (v2.x != 0)
// is rendered as a half-drop brick: a TWO-row tile (height 2·v2.y) whose second
// row is offset half a cell horizontally, so the pattern closes seamlessly — the
// same construction the S-52 STG atlas uses (e.g. DQUALA11 → 248×414, not the
// single-row 248×207 that repeated at half the period and clipped). The exact
// v2.x offset is approximated by half-width — the only stagger a 2-row rectangle
// can close — so it is a textured fill, not a pixel-exact stagger.
func PatternAtlasS101FS(catalogFS fs.FS, cssName string) (jsonBytes, pngBytes []byte, err error) {
	cat, err := catalog.LoadFS(catalogFS)
	if err != nil {
		return nil, nil, fmt.Errorf("area fills: %w", err)
	}
	fills := cat.AreaFills
	symbolsFS, err := fs.Sub(catalogFS, "Symbols")
	if err != nil {
		return nil, nil, err
	}
	cssData, err := fs.ReadFile(symbolsFS, cssName)
	if err != nil {
		return nil, nil, fmt.Errorf("read css: %w", err)
	}
	css := symbols.LoadCSS(cssData)

	var rasters []raster
	skipped := 0
	for id, af := range fills {
		if af.SymbolRef == "" {
			skipped++
			continue
		}
		svg, err := fs.ReadFile(symbolsFS, af.SymbolRef+".svg")
		if err != nil {
			skipped++
			continue
		}
		sym, err := symbols.Render(svg, css, s101PxPerMM)
		if err != nil {
			skipped++
			continue
		}
		tile, ok := seamlessTile(sym, af.V1, af.V2)
		if !ok {
			skipped++
			continue
		}
		w := uint32(tile.Rect.Dx())
		h := uint32(tile.Rect.Dy())
		rasters = append(rasters, raster{name: id, w: w, h: h, rgba: tile.Pix})
	}

	a := packInto(rasters, skipped, s101AtlasWidth)
	pngBytes, err = a.encodePNG()
	if err != nil {
		return nil, nil, err
	}
	return a.toJSON(), pngBytes, nil
}

// seamlessTile stamps the rendered symbol onto the lattice cell (in px),
// wrapping at the edges. A staggered v2 (v2.x != 0) doubles the tile height and
// half-drops the second row. ok is false for a degenerate/oversized cell.
func seamlessTile(sym *symbols.Rendered, v1, v2 catalog.Vec) (*image.NRGBA, bool) {
	w := int(math.Round(v1.X * s101PxPerMM))
	rowH := int(math.Round(v2.Y * s101PxPerMM)) // one row's vertical pitch
	if w < 1 || rowH < 1 {
		return nil, false
	}
	rows := 1
	if math.Abs(v2.X) > 1e-6 { // staggered lattice → 2-row half-drop brick
		rows = 2
	}
	h := rowH * rows
	if w > maxCellSide || h > maxCellSide {
		return nil, false
	}
	tile := image.NewNRGBA(image.Rect(0, 0, w, h))
	// Row centres within the tile; the staggered second row sits half a cell to
	// the right (x = w wraps to x = 0, i.e. offset w/2 from the first row).
	centres := [][2]float64{{float64(w) / 2, float64(rowH) / 2}}
	if rows == 2 {
		centres = append(centres, [2]float64{float64(w), float64(rowH) * 1.5})
	}
	pw, ph := float64(w), float64(h)
	for _, c := range centres {
		// Stamp each row centre plus its 8 neighbours (full tile period) so
		// edge-crossing art wraps in cleanly.
		for jy := -1; jy <= 1; jy++ {
			for ix := -1; ix <= 1; ix++ {
				dx := int(math.Round(c[0] + float64(ix)*pw - sym.PivotX))
				dy := int(math.Round(c[1] + float64(jy)*ph - sym.PivotY))
				stampOver(tile, sym.Image, dx, dy)
			}
		}
	}
	return tile, true
}

// stampOver copies src's non-transparent pixels into dst at (dx,dy), clipped.
func stampOver(dst, src *image.NRGBA, dx, dy int) {
	sw, sh := src.Rect.Dx(), src.Rect.Dy()
	dw, dh := dst.Rect.Dx(), dst.Rect.Dy()
	for sy := 0; sy < sh; sy++ {
		y := dy + sy
		if y < 0 || y >= dh {
			continue
		}
		for sx := 0; sx < sw; sx++ {
			x := dx + sx
			if x < 0 || x >= dw {
				continue
			}
			si := src.PixOffset(sx, sy)
			if src.Pix[si+3] == 0 {
				continue
			}
			di := dst.PixOffset(x, y)
			copy(dst.Pix[di:di+4], src.Pix[si:si+4])
		}
	}
}
