package assets

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/beetlebugorg/chartplotter/pkg/s100/symbols"
)

// S-101 sprite-atlas builder: rasterizes every IHO S-101 symbol SVG (CSS colour
// classes resolved against the chosen palette stylesheet) with pure-Go SVG and
// shelf-packs it into the sprites.json / atlas PNG the MapLibre client consumes.

// s101PxPerMM is the final device px per millimetre (pxPerUnit is px per
// 0.01-mm unit; ×100 = px per mm).
const s101PxPerMM = pxPerUnit * 100

// s101AtlasWidth is the packed-atlas width for the ~724 S-101 symbols. Wider
// than the S-52 512 so the atlas stays under the 4096 WebGL texture limit in
// height too (one GL texture; Chrome's MAX_TEXTURE_SIZE is 4096).
const s101AtlasWidth = 2048

// SpriteAtlasS101 builds the symbol atlas from an S-101 Symbols directory
// (path) and one of its *SvgStyle.css palettes. Returns sprites.json + atlas
// PNG bytes.
func SpriteAtlasS101(symbolsDir, cssPath string) (jsonBytes, pngBytes []byte, err error) {
	return SpriteAtlasS101FS(os.DirFS(symbolsDir), filepath.Base(cssPath))
}

// SpriteAtlasS101FS builds the atlas from an fs.FS rooted at the Symbols
// directory (e.g. an embed.FS sub-tree); cssName is the stylesheet file within
// it (e.g. "daySvgStyle.css").
func SpriteAtlasS101FS(symbolsFS fs.FS, cssName string) (jsonBytes, pngBytes []byte, err error) {
	cssData, err := fs.ReadFile(symbolsFS, cssName)
	if err != nil {
		return nil, nil, fmt.Errorf("read css: %w", err)
	}
	css := symbols.LoadCSS(cssData)

	entries, err := fs.ReadDir(symbolsFS, ".")
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
		svg, err := fs.ReadFile(symbolsFS, e.Name())
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

	// Wider than the S-52 atlas (512) so the ~724 S-101 symbols pack into an
	// atlas that stays well under the 4096 WebGL texture limit in BOTH
	// dimensions (at 512 wide it grew to ~4823 tall and broke in Chrome).
	a := packInto(rasters, skipped, s101AtlasWidth)
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
