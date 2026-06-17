package assets

import (
	"bytes"
	"fmt"
	"math"
	"sort"

	"github.com/beetlebugorg/chartplotter-go/internal/engine/portrayal"
	"github.com/beetlebugorg/chartplotter-go/pkg/s52"
)

// featureScale is screen px per 0.01-mm PresLib symbol unit — the identical
// scale the portrayal backend uses, so the dash period lands at the same screen
// dimension as the SC symbol marks it interleaves with. (linestyles.zig:feature_scale)
const featureScale = float64(portrayal.DefaultPxPerSymbolUnit)

// onRun is one drawn run within a single pattern period, in screen px relative
// to the bbox left edge.
type onRun struct{ lo, hi float64 }

// lsSym is one embedded SC symbol within a single pattern period.
type lsSym struct {
	offset float64 // along-line offset px from bbox left edge (arc distance 0)
	name   string
	rot    float64 // sub-rotation in degrees (PresLib tenths ×0.1)
}

// lsPattern is the analysed dash data for one linestyle.
type lsPattern struct {
	periodPx   float64
	runs       []onRun
	symbols    []lsSym
	colorToken string
	widthPx    float64
}

// analyzeLinestyle walks a linestyle's vector ops over one period and collects
// its PD "on" runs (px, relative to bbox.x), the embedded SC symbol placements,
// the first PD pen's colour token, and the stroke width active at the first PD.
// Polygon/arc ops are ignored — the dash array is only the line part. Returns
// false for a degenerate (zero-width / sub-px) linestyle. Port of
// linestyles.zig analyze().
func analyzeLinestyle(ls *s52.Linestyle) (lsPattern, bool) {
	if ls.BBoxWidth == 0 {
		return lsPattern{}, false
	}
	periodPx := float64(ls.BBoxWidth) * featureScale
	if periodPx < 0.5 {
		return lsPattern{}, false
	}
	bboxX := float64(ls.BBoxX)

	var runs []onRun
	var symbols []lsSym
	colorToken := ""
	widthPx := 0.0
	haveColor := false

	for i := range ls.VectorCommands {
		cmd := &ls.VectorCommands[i]
		switch {
		case cmd.Type == "PD":
			// Each PD command carries a polyline; walk consecutive point pairs,
			// mirroring the Zig per-segment pen_down_to handling.
			pts := cmd.Points
			for j := 0; j+1 < len(pts); j++ {
				a := (math.Min(pts[j].X, pts[j+1].X) - bboxX) * featureScale
				b := (math.Max(pts[j].X, pts[j+1].X) - bboxX) * featureScale
				lo := clampf(a, 0, periodPx)
				hi := clampf(b, 0, periodPx)
				if hi-lo < 1e-6 {
					continue
				}
				if !haveColor {
					colorToken = ls.Colors.Roles[cmd.Role]
					// sw=1 → 0.32 mm = 32 units; matches complexline.zig.
					widthPx = float64(cmd.StrokeWidth) * 32.0 * featureScale
					haveColor = true
				}
				runs = append(runs, onRun{lo: lo, hi: hi})
			}
		case cmd.Type == "SC" && cmd.SymbolCall != nil:
			sc := cmd.SymbolCall
			symbols = append(symbols, lsSym{
				offset: (sc.CallPosition.X - bboxX) * featureScale,
				name:   sc.SymbolName,
				rot:    float64(sc.Orientation) * 0.1,
			})
		}
	}

	sort.SliceStable(runs, func(i, j int) bool { return runs[i].lo < runs[j].lo })
	return lsPattern{
		periodPx:   periodPx,
		runs:       runs,
		symbols:    symbols,
		colorToken: colorToken,
		widthPx:    widthPx,
	}, true
}

// dashArray converts sorted on-runs over [0, period] into a flat portrayal dash
// array: [on,off,on,off,…], starting with an "on" entry (a leading 0 is
// inserted when the pattern opens with a gap) and padded to even length so the
// pattern repeats cleanly. Port of linestyles.zig dashArray().
func dashArray(p lsPattern) []float64 {
	var out []float64
	pos := 0.0 // end of the last consumed run

	flush := func(lo, hi float64) {
		if lo > pos+1e-6 {
			if len(out) == 0 {
				out = append(out, 0) // leading gap → 0 "on"
			}
			out = append(out, lo-pos) // off
		}
		out = append(out, hi-lo) // on
		pos = hi
	}

	havePrev := false
	var prevLo, prevHi float64
	for _, run := range p.runs {
		if !havePrev {
			havePrev = true
			prevLo, prevHi = run.lo, run.hi
			continue
		}
		if run.lo <= prevHi+1e-6 {
			prevHi = math.Max(prevHi, run.hi) // overlap/adjacent → merge
		} else {
			flush(prevLo, prevHi)
			prevLo, prevHi = run.lo, run.hi
		}
	}
	if havePrev {
		flush(prevLo, prevHi)
	}

	// Trailing gap to the period end.
	if p.periodPx-pos > 1e-6 {
		if len(out) == 0 {
			out = append(out, 0) // pure-gap pattern
		}
		out = append(out, p.periodPx-pos) // off
	}
	// Even length so the pattern tiles cleanly (a trailing "on" needs a 0 off).
	if len(out)%2 == 1 {
		out = append(out, 0)
	}
	if len(out) == 0 {
		out = append(out, 0, p.periodPx)
	}
	return out
}

// LinestylesJSON renders linestyles.json. Linestyles are emitted in sorted id
// order for deterministic output, matching the Zig float formatting (%.3f).
// Port of linestyles.zig toJson().
func LinestylesJSON(lib *s52.Library) ([]byte, error) {
	ids := lib.ListLineStyles()
	sort.Strings(ids)

	var buf bytes.Buffer
	buf.WriteString("{\n")
	first := true
	for _, id := range ids {
		ls, err := lib.GetLineStyle(id)
		if err != nil {
			continue
		}
		pat, ok := analyzeLinestyle(ls)
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

// f3 formats a float with 3 decimals (matches Zig {d:.3}).
func f3(v float64) string { return fmt.Sprintf("%.3f", v) }

func clampf(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
