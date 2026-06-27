package bake

import "github.com/beetlebugorg/chartplotter/pkg/s100/catalog"

// lsPxPerMM converts S-100 line-style millimetre measures to screen px. It is the
// inverse of the PresLib's 0.35278 mm-per-px (lsFeatureScale = 0.01/0.35278 px per
// 0.01-mm unit ⇒ pxPerMM = 100*lsFeatureScale), so a line style shared by name
// between the catalogue and the PresLib lands at the same period geometry.
const lsPxPerMM = 100.0 * lsFeatureScale

// buildLinestyleTableFromCatalog analyses every S-101 catalogue line style into
// its period geometry — the catalogue replacement for buildLinestyleTable (which
// read the S-52 PresLib). The baker reuses the table for every tile.
func buildLinestyleTableFromCatalog(cat *catalog.Catalog) map[string]*lsInfo {
	out := make(map[string]*lsInfo)
	if cat == nil {
		return out
	}
	for id, ls := range cat.LineStyles {
		if info := lsInfoFromCatalog(ls); info != nil {
			out[id] = info
		}
	}
	return out
}

// lsInfoFromCatalog converts one catalogue LineStyle (flattening composite
// Components onto the longest interval) to the tessellator's lsInfo.
func lsInfoFromCatalog(ls *catalog.LineStyle) *lsInfo {
	info := &lsInfo{colorToken: ls.PenColor, widthPx: ls.PenWidth * lsPxPerMM}
	hasDash := false
	addRuns := func(src *catalog.LineStyle) {
		if src.IntervalLength*lsPxPerMM > info.periodPx {
			info.periodPx = src.IntervalLength * lsPxPerMM
		}
		// Each pen of a (possibly composite) line is stroked. A compositeLineStyle
		// (double line) stacks a wide dark backing then a narrower bright pen ON TOP —
		// e.g. the indication highlight INDHLT02 = black 1.28 under yellow 0.64.
		// Components are listed background-first, so emitting them in order draws the
		// bright pen inside a dark outline. colorToken/widthPx mirror the foreground
		// (last) pen — the prim's fallback tag.
		if src.PenColor != "" {
			w := src.PenWidth * lsPxPerMM
			info.pens = append(info.pens, lsPen{colorToken: src.PenColor, widthPx: w})
			info.colorToken = src.PenColor
			if src.PenWidth > 0 {
				info.widthPx = w
			}
		}
		for _, d := range src.Dashes {
			lo, hi := d.Start*lsPxPerMM, (d.Start+d.Length)*lsPxPerMM
			if hi-lo > 1e-6 {
				info.onRuns = append(info.onRuns, lsOnRun{lo: lo, hi: hi})
				hasDash = true
			}
		}
		for _, s := range src.Symbols {
			info.symbols = append(info.symbols, lsEmbed{offset: s.Position * lsPxPerMM, name: s.Reference})
		}
	}
	if len(ls.Components) > 0 {
		for i := range ls.Components {
			addRuns(&ls.Components[i])
		}
	} else {
		addRuns(ls)
	}
	// Solid pen (no dash pattern, e.g. INDHLT02): stroke the whole line continuously.
	// Without this the tessellator finds no on-runs and emits nothing — the line is
	// invisible. Give it a period (if the style declared none) and one full-coverage
	// run so every period is 100% "on" ⇒ a continuous stroke. Mirrors s101Pattern in
	// internal/engine/assets/linestyles_s101.go (the client-asset path already does this).
	if !hasDash {
		if info.periodPx < 0.5 {
			info.periodPx = 16 * lsPxPerMM // arbitrary; a 100%-on run makes the value moot
		}
		info.onRuns = []lsOnRun{{lo: 0, hi: info.periodPx}}
	}
	if info.periodPx < 0.5 {
		return nil
	}
	if len(info.pens) == 0 {
		info.pens = []lsPen{{colorToken: info.colorToken, widthPx: info.widthPx}}
	}
	// Sub-pixel pens vanish; floor them (matches the legacy single-width floor).
	for i := range info.pens {
		if info.pens[i].widthPx < 0.6 {
			info.pens[i].widthPx = 0.9
		}
	}
	info.widthPx = info.pens[len(info.pens)-1].widthPx // foreground mirror
	return info
}
