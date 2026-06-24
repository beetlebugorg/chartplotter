package assets

import (
	"fmt"
	"math"

	"github.com/beetlebugorg/chartplotter/internal/engine/portrayal"
)

// featureScale is screen px per 0.01-mm PresLib symbol unit — the identical
// scale the portrayal backend uses, so the dash period lands at the same screen
// dimension as the SC symbol marks it interleaves with.
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

// dashArray converts sorted on-runs over [0, period] into a flat portrayal dash
// array: [on,off,on,off,…], starting with an "on" entry (a leading 0 is
// inserted when the pattern opens with a gap) and padded to even length so the
// pattern repeats cleanly.
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

// f3 formats a float with 3 decimals (%.3f).
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
