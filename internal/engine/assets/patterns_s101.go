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
// per fill name). Same atlas format the S-52 path emits.
//
// The tile is the v1.x × v2.y rectangle (v1 is always horizontal in the S-101
// catalogue); the symbol is stamped at the cell centre plus its 8 neighbours so
// art crossing an edge wraps cleanly. A staggered v2 (v2.x != 0) is approximated
// as a straight grid — a textured fill, not a pixel-exact stagger.
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

// seamlessTile stamps the rendered symbol onto the v1.x × v2.y lattice cell (in
// px), wrapping at the edges. ok is false for a degenerate/oversized cell.
func seamlessTile(sym *symbols.Rendered, v1, v2 catalog.Vec) (*image.NRGBA, bool) {
	w := int(math.Round(v1.X * s101PxPerMM))
	h := int(math.Round(v2.Y * s101PxPerMM))
	if w < 1 || h < 1 || w > maxCellSide || h > maxCellSide {
		return nil, false
	}
	tile := image.NewNRGBA(image.Rect(0, 0, w, h))
	cx, cy := float64(w)/2, float64(h)/2
	for jy := -1; jy <= 1; jy++ {
		for ix := -1; ix <= 1; ix++ {
			dx := int(math.Round(cx + float64(ix*w) - sym.PivotX))
			dy := int(math.Round(cy + float64(jy*h) - sym.PivotY))
			stampOver(tile, sym.Image, dx, dy)
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
