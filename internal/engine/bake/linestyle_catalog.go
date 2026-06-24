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
	addRuns := func(src *catalog.LineStyle) {
		if src.IntervalLength*lsPxPerMM > info.periodPx {
			info.periodPx = src.IntervalLength * lsPxPerMM
		}
		if info.colorToken == "" {
			info.colorToken = src.PenColor
		}
		if info.widthPx == 0 && src.PenWidth > 0 {
			info.widthPx = src.PenWidth * lsPxPerMM
		}
		for _, d := range src.Dashes {
			lo, hi := d.Start*lsPxPerMM, (d.Start+d.Length)*lsPxPerMM
			if hi-lo > 1e-6 {
				info.onRuns = append(info.onRuns, lsOnRun{lo: lo, hi: hi})
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
	if info.periodPx < 0.5 {
		return nil
	}
	if info.widthPx < 0.6 {
		info.widthPx = 0.9
	}
	return info
}
