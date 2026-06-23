package assets

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/beetlebugorg/chartplotter/pkg/s100/symbols"
)

// S-101 sprite-atlas builder (specs/s101-portrayal-backport.md, Workstream B):
// rasterizes every IHO S-101 symbol SVG (CSS colour classes resolved against
// the chosen palette stylesheet) and shelf-packs it into the same atlas
// format the S-52 path emits — so sprites.json / the atlas PNG stay drop-in
// for the MapLibre client. Replaces the HP-GL interpreter with pure-Go SVG.

// s101PxPerMM is the final device px per millimetre, matching the S-52 atlas
// scale (pxPerUnit is px per 0.01-mm unit; ×100 = px per mm).
const s101PxPerMM = pxPerUnit * 100

// SpriteAtlasS101 builds the symbol atlas from an S-101 Symbols directory and
// one of its *SvgStyle.css palettes. Returns sprites.json + atlas PNG bytes.
func SpriteAtlasS101(symbolsDir, cssPath string) (jsonBytes, pngBytes []byte, err error) {
	cssData, err := os.ReadFile(cssPath)
	if err != nil {
		return nil, nil, fmt.Errorf("read css: %w", err)
	}
	css := symbols.LoadCSS(cssData)

	entries, err := os.ReadDir(symbolsDir)
	if err != nil {
		return nil, nil, fmt.Errorf("read symbols dir: %w", err)
	}

	var rasters []raster
	skipped := 0
	missing := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".svg") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		svg, err := os.ReadFile(filepath.Join(symbolsDir, e.Name()))
		if err != nil {
			return nil, nil, fmt.Errorf("read %s: %w", e.Name(), err)
		}
		r, err := symbols.Render(svg, css, s101PxPerMM)
		if err != nil {
			skipped++
			continue
		}
		for _, m := range r.Missing {
			missing[m] = true
		}
		w := uint32(r.Image.Rect.Dx())
		h := uint32(r.Image.Rect.Dy())
		if w == 0 || h == 0 || w > maxCellSide || h > maxCellSide {
			skipped++
			continue
		}
		rasters = append(rasters, raster{
			name:   id,
			w:      w,
			h:      h,
			pivotX: float32(r.PivotX),
			pivotY: float32(r.PivotY),
			rgba:   r.Image.Pix,
		})
	}

	a := packInto(rasters, skipped)
	pngBytes, err = a.encodePNG()
	if err != nil {
		return nil, nil, err
	}
	if len(missing) > 0 {
		fmt.Fprintf(os.Stderr, "SpriteAtlasS101: %d unresolved CSS class(es): %s\n", len(missing), strings.Join(sortedKeys(missing), " "))
	}
	return a.toJSON(), pngBytes, nil
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
