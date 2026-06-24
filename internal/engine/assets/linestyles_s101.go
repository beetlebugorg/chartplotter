package assets

import (
	"bytes"
	"fmt"
	"sort"

	"github.com/beetlebugorg/chartplotter/pkg/s100/catalog"
)

// linestylePxPerMM converts S-101 millimetre dimensions to the on-screen feature
// scale used by linestyles.json (featureScale is px per 0.01-mm unit; ×100 =
// px per mm).
const linestylePxPerMM = featureScale * 100

// LinestylesJSONS101 renders linestyles.json from the S-101 LineStyles, matching
// the schema LinestylesJSON emits from the S-52 library: per id, period_px, a
// flat [on,off,…] dash array, the pen colour token + width, and the symbols
// placed along the period. A compositeLineStyle (double line) is emitted as its
// first component (the client schema is single-component); ids are sorted.
func LinestylesJSONS101(lines map[string]*catalog.LineStyle) ([]byte, error) {
	ids := make([]string, 0, len(lines))
	for id := range lines {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var buf bytes.Buffer
	buf.WriteString("{\n")
	first := true
	for _, id := range ids {
		ls := lines[id]
		if len(ls.Components) > 0 {
			ls = &ls.Components[0] // composite: emit the primary component
		}
		pat, ok := s101Pattern(ls)
		if !ok {
			continue
		}
		dash := dashArray(pat)

		if !first {
			buf.WriteString(",\n")
		}
		first = false
		fmt.Fprintf(&buf, "  %q: { \"period_px\": %s, \"dash\": [", id, f3(pat.periodPx))
		for i, v := range dash {
			if i > 0 {
				buf.WriteString(", ")
			}
			buf.WriteString(f3(v))
		}
		fmt.Fprintf(&buf, "], \"color_token\": %q, \"width_px\": %s, \"symbols\": [", pat.colorToken, f3(pat.widthPx))
		for i, sym := range pat.symbols {
			if i > 0 {
				buf.WriteString(", ")
			}
			fmt.Fprintf(&buf, "{ \"o\": %s, \"n\": %q, \"r\": %s }", f3(sym.offset), sym.name, f3(sym.rot))
		}
		buf.WriteString("] }")
	}
	buf.WriteString("\n}\n")
	return buf.Bytes(), nil
}

// s101Pattern converts one S-101 line style (single component) into the analysed
// dash pattern. No <dash> means a solid pen line (one run over the whole
// period); symbols carry no rotation in the S-101 schema.
func s101Pattern(ls *catalog.LineStyle) (lsPattern, bool) {
	periodPx := ls.IntervalLength * linestylePxPerMM
	if periodPx < 0.5 {
		// No interval (e.g. a pure-symbol style with no length): nothing to tile.
		return lsPattern{}, false
	}

	var runs []onRun
	if len(ls.Dashes) == 0 {
		runs = []onRun{{lo: 0, hi: periodPx}} // solid pen
	} else {
		for _, d := range ls.Dashes {
			lo := clampf(d.Start*linestylePxPerMM, 0, periodPx)
			hi := clampf((d.Start+d.Length)*linestylePxPerMM, 0, periodPx)
			if hi-lo > 1e-6 {
				runs = append(runs, onRun{lo: lo, hi: hi})
			}
		}
		sort.SliceStable(runs, func(i, j int) bool { return runs[i].lo < runs[j].lo })
	}

	var symbols []lsSym
	for _, s := range ls.Symbols {
		symbols = append(symbols, lsSym{offset: s.Position * linestylePxPerMM, name: s.Reference})
	}

	return lsPattern{
		periodPx:   periodPx,
		runs:       runs,
		symbols:    symbols,
		colorToken: ls.PenColor,
		widthPx:    ls.PenWidth * linestylePxPerMM,
	}, true
}
