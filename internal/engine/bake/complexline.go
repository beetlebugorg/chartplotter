package bake

import (
	"math"

	"github.com/beetlebugorg/chartplotter/internal/engine/mvt"
	"github.com/beetlebugorg/chartplotter/internal/engine/tile"
)

// Complex (symbolised) linestyles are tessellated PER ZOOM at emit time, exactly
// like sector lights (their period is screen-px sized, so a fixed lat/lon
// geometry can't carry it). For each tile we walk the line by arc length —
// anchored at the line's first vertex so the pattern is continuous across tile
// boundaries (tile.ClipLinePhased keeps that phase) — and emit, per period:
//   - the dash "on" runs as real line segments into the restyleable complex_lines
//     layer (colour stays a client expression → Day/Dusk/Night switches live);
//   - each embedded symbol as a point into point_symbols, rotated to the local
//     line tangent (so chevrons/anchors/"!" sit ON the line at every zoom with no
//     pattern-stretch).
// This replaces drawing the polyline once + a client-side line-pattern (which
// stretched during zoom) or a phase-free symbol pass (symbols drifted off).

// lsOnRun is one drawn dash run within a period, in screen px from period start.
type lsOnRun struct{ lo, hi float64 }

// lsEmbed is one embedded symbol within a period.
type lsEmbed struct {
	offset float64 // screen px from period start
	name   string
}

// lsPen is one stroke of a (possibly composite) line style. A compositeLineStyle
// (double line) has several, listed background-first: e.g. INDHLT02 = a wide black
// backing then a narrow yellow pen ON TOP, drawn in order so the yellow highlight
// sits inside a black outline.
type lsPen struct {
	colorToken string
	widthPx    float64
}

// lsInfo is the per-zoom-independent geometry of one complex linestyle.
type lsInfo struct {
	periodPx float64
	onRuns   []lsOnRun
	symbols  []lsEmbed
	pens     []lsPen // ≥1, background→foreground; the dash geometry is stroked once per pen
	// colorToken/widthPx mirror the foreground (last) pen — the prim's fallback tag.
	colorToken string
	widthPx    float64
}

// lsFeatureScale is screen px per 0.01-mm PresLib unit — the scale the dash/
// symbol offsets are measured in (matches the assets + portrayal backend).
const lsFeatureScale = float64(0.01 / 0.35278)

// emitComplexLine tessellates one complex-line prim into this tile at coord.Z.
func (b *Baker) emitComplexLine(r *routed, proj tile.Projector, rect tile.Rect, z uint32, extent uint32, tb *mvt.TileBuilder, scratch *[]tile.FPoint, attrScratch *[]mvt.KeyValue) {
	info := r.ls
	if info == nil || len(r.nline) < 2 {
		return
	}
	pts := projectNormRing(r.nline, proj, *scratch)
	*scratch = pts
	if len(pts) < 2 {
		return
	}
	// Cumulative arc length (tile-pixel units) along the full projected line.
	arc := make([]float64, len(pts))
	for i := 1; i < len(pts); i++ {
		arc[i] = arc[i-1] + math.Hypot(pts[i].X-pts[i-1].X, pts[i].Y-pts[i-1].Y)
	}

	// Screen px -> tile units. The baker lays figures out in 256-px-per-tile space
	// (see sectorRadiusNorm/tessellateFigure); one tile is `extent` units wide.
	pxScale := float64(extent) / 256.0
	period := info.periodPx * pxScale
	if period < 1e-6 {
		return
	}

	full := b.attrsFor(r, attrScratch) // rebuild base+variable (aliases attrScratch; stable here)
	// color_token + width_px are added PER PEN below (a composite line strokes each),
	// so strip any the prim carried — otherwise the foreground colour would leak onto
	// the backing pen.
	dashBase := make([]mvt.KeyValue, 0, len(full)+2)
	for _, kv := range full {
		if kv.Key == "color_token" || kv.Key == "width_px" {
			continue
		}
		dashBase = append(dashBase, kv)
	}
	symBase := full // class/cell/draw_prio/cat/bnd (+inspector extras)
	symScale := float64(0.01 / 0.35278)

	var dashPaths [][]mvt.IPoint
	var symPts []mvt.IPoint
	var symAttrs [][]mvt.KeyValue

	// Phase-stable clip: each run carries Arc0 (global arc at its first vertex).
	for _, run := range tile.ClipLinePhased(pts, arc, rect) {
		rp := run.Points
		if len(rp) < 2 {
			continue
		}
		rarc := make([]float64, len(rp))
		for i := 1; i < len(rp); i++ {
			rarc[i] = rarc[i-1] + math.Hypot(rp[i].X-rp[i-1].X, rp[i].Y-rp[i-1].Y)
		}
		g0 := run.Arc0
		runEnd := g0 + rarc[len(rarc)-1]
		kStart := int(math.Floor(g0 / period))
		for k := kStart; float64(k)*period < runEnd; k++ {
			base := float64(k) * period
			// Dash on-runs.
			for _, on := range info.onRuns {
				lo := math.Max(base+on.lo*pxScale, g0)
				hi := math.Min(base+on.hi*pxScale, runEnd)
				if hi-lo < 1e-6 {
					continue
				}
				if sub := subPathByArc(rp, rarc, lo-g0, hi-g0); len(sub) >= 2 {
					dashPaths = append(dashPaths, quantizeRing(sub))
				}
			}
			// Embedded symbols.
			for _, sym := range info.symbols {
				if sym.name == "" {
					continue
				}
				gp := base + sym.offset*pxScale
				if gp < g0 || gp > runEnd {
					continue
				}
				pt, ok, dx, dy := pointAndTangent(rp, rarc, gp-g0)
				if !ok {
					continue
				}
				rot := math.Atan2(dy, dx) * 180.0 / math.Pi
				symPts = append(symPts, tile.Quantize(pt))
				symAttrs = append(symAttrs, append(append([]mvt.KeyValue(nil), symBase...),
					mvt.KeyValue{Key: "symbol_name", Value: mvt.StringVal(sym.name)},
					mvt.KeyValue{Key: "rotation_deg", Value: mvt.FloatVal(float32(rot))},
					mvt.KeyValue{Key: "scale", Value: mvt.FloatVal(float32(symScale))},
					// The mark is oriented to the edge tangent in chart space, so it must
					// turn with the chart — map-aligned, like an ORIENT symbol (rot_north).
					mvt.KeyValue{Key: "rot_north", Value: mvt.IntVal(1)},
				))
			}
		}
	}

	if len(dashPaths) > 0 {
		// r.layer is "complex_lines" normally, or "complex_lines_scamin" when the
		// feature carries SCAMIN (set by route via scaminLayer) so the dashes land
		// in the SCAMIN-bucketed source-layer. The embedded symbols below stay in
		// point_symbols (already bucketed; they carry `scamin` via attrsFor).
		// Stroke the geometry once per pen, background→foreground, so a composite
		// double line (e.g. INDHLT02: black backing + yellow top) renders the bright
		// pen inside a dark outline — same paths, each pen's own colour + width.
		lay := tb.Layer(r.layer)
		for _, pen := range info.pens {
			attrs := append(append([]mvt.KeyValue(nil), dashBase...),
				mvt.KeyValue{Key: "color_token", Value: mvt.StringVal(pen.colorToken)},
				mvt.KeyValue{Key: "width_px", Value: mvt.IntVal(int64(pen.widthPx + 0.5))})
			lay.AddLines(dashPaths, attrs)
		}
	}
	// AddPoints shares one attr set per call, so emit each symbol on its own (their
	// rotations differ).
	lay := tb.Layer("point_symbols")
	for i := range symPts {
		lay.AddPoints(symPts[i:i+1], symAttrs[i])
	}
}

// subPathByArc returns the sub-polyline of rp between local arc distances d0..d1
// (rarc is the per-vertex cumulative arc). Endpoints are interpolated.
func subPathByArc(rp []tile.FPoint, rarc []float64, d0, d1 float64) []tile.FPoint {
	total := rarc[len(rarc)-1]
	d0 = clampf64(d0, 0, total)
	d1 = clampf64(d1, 0, total)
	if d1-d0 < 1e-9 {
		return nil
	}
	out := []tile.FPoint{lerpArc(rp, rarc, d0)}
	for i := range rp {
		if rarc[i] > d0 && rarc[i] < d1 {
			out = append(out, rp[i])
		}
	}
	out = append(out, lerpArc(rp, rarc, d1))
	return out
}

// pointAndTangent returns the point at local arc d plus the (un-normalised)
// tangent direction (dx,dy) of the segment containing it.
func pointAndTangent(rp []tile.FPoint, rarc []float64, d float64) (tile.FPoint, bool, float64, float64) {
	total := rarc[len(rarc)-1]
	d = clampf64(d, 0, total)
	for i := 0; i+1 < len(rp); i++ {
		if d <= rarc[i+1] || i+2 == len(rp) {
			seg := rarc[i+1] - rarc[i]
			t := 0.0
			if seg > 1e-12 {
				t = (d - rarc[i]) / seg
			}
			p := tile.FPoint{X: rp[i].X + t*(rp[i+1].X-rp[i].X), Y: rp[i].Y + t*(rp[i+1].Y-rp[i].Y)}
			return p, true, rp[i+1].X - rp[i].X, rp[i+1].Y - rp[i].Y
		}
	}
	return tile.FPoint{}, false, 0, 0
}

// lerpArc returns the point at local arc distance d along rp.
func lerpArc(rp []tile.FPoint, rarc []float64, d float64) tile.FPoint {
	p, _, _, _ := pointAndTangent(rp, rarc, d)
	return p
}

func clampf64(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
