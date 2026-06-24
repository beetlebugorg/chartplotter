package assets

import (
	"math"
	"sort"
)

// Software anti-aliased rasteriser for the sprite / pattern atlases. Geometry
// is computed in float32 so cell sizes and pivots match the reference atlas to
// 2 dp.

const (
	pxPerUnit   = 0.08 // device px per 0.01-mm symbol unit (8 px/mm)
	supersample = 4    // AA: rasterise at 4× and box-downsample
	atlasPad    = 1    // final-px gap between atlas cells
	maxCellSide = 640  // skip pathological cells larger than this (final px)
)

type rcolor struct{ r, g, b uint8 }

type rpoint struct{ x, y float32 }

// drawCmd is one painted primitive in supersampled page px (pivot at origin).
type drawCmd struct {
	fill  bool
	rings [][]rpoint // fill rings (even-odd)
	pts   []rpoint   // stroke polyline
	width float32    // stroke width (supersampled px)
	color rcolor
}

// raster is a rasterised-but-not-yet-packed cell.
type raster struct {
	name   string
	w, h   uint32
	pivotX float32
	pivotY float32
	rgba   []byte
}

func rasterizeCommands(buf []byte, w, h uint32, cmds []drawCmd, tx, ty float32) {
	for _, cmd := range cmds {
		if cmd.fill {
			fillRings(buf, w, h, cmd.rings, cmd.color, tx, ty)
		} else {
			strokePolyline(buf, w, h, cmd.pts, cmd.width, cmd.color, tx, ty)
		}
	}
}

func putOpaque(buf []byte, w, h uint32, x, y int64, c rcolor) {
	if x < 0 || y < 0 || x >= int64(w) || y >= int64(h) {
		return
	}
	i := (int(y)*int(w) + int(x)) * 4
	buf[i] = c.r
	buf[i+1] = c.g
	buf[i+2] = c.b
	buf[i+3] = 255
}

// fillRings does an even-odd scanline fill of a set of rings.
func fillRings(buf []byte, w, h uint32, rings [][]rpoint, c rcolor, tx, ty float32) {
	minYf := float32(math.Inf(1))
	maxYf := float32(math.Inf(-1))
	for _, ring := range rings {
		for _, p := range ring {
			if p.y+ty < minYf {
				minYf = p.y + ty
			}
			if p.y+ty > maxYf {
				maxYf = p.y + ty
			}
		}
	}
	if math.IsInf(float64(minYf), 0) {
		return
	}
	y0 := maxI64(0, int64(math.Floor(float64(minYf))))
	y1 := minI64(int64(h)-1, int64(math.Ceil(float64(maxYf))))

	var xs []float32
	for y := y0; y <= y1; y++ {
		sy := float32(y) + 0.5
		xs = xs[:0]
		for _, ring := range rings {
			pts := ring
			if len(pts) < 2 {
				continue
			}
			j := len(pts) - 1
			for i := 0; i < len(pts); i++ {
				ay := pts[j].y + ty
				by := pts[i].y + ty
				ax := pts[j].x + tx
				bx := pts[i].x + tx
				j = i
				if (ay > sy) != (by > sy) {
					t := (sy - ay) / (by - ay)
					xs = append(xs, ax+t*(bx-ax))
				}
			}
		}
		if len(xs) < 2 {
			continue
		}
		sort.Slice(xs, func(a, b int) bool { return xs[a] < xs[b] })
		for k := 0; k+1 < len(xs); k += 2 {
			xa := int64(math.Ceil(float64(xs[k]) - 0.5))
			xb := int64(math.Floor(float64(xs[k+1]) - 0.5))
			x := maxI64(0, xa)
			xend := minI64(int64(w)-1, xb)
			for ; x <= xend; x++ {
				putOpaque(buf, w, h, x, y, c)
			}
		}
	}
}

// strokePolyline strokes a polyline as filled segment quads plus round caps/joins.
func strokePolyline(buf []byte, w, h uint32, pts []rpoint, width float32, c rcolor, tx, ty float32) {
	hw := width * 0.5
	if hw < 0.5 {
		hw = 0.5
	}
	if len(pts) == 0 {
		return
	}
	// A degenerate polyline (one point or all coincident) draws as a disc.
	degenerate := true
	for _, p := range pts {
		if absf(p.x-pts[0].x) > 1e-3 || absf(p.y-pts[0].y) > 1e-3 {
			degenerate = false
			break
		}
	}
	if degenerate {
		fillDisc(buf, w, h, pts[0].x+tx, pts[0].y+ty, hw, c)
		return
	}
	for i := 0; i+1 < len(pts); i++ {
		ax, ay := pts[i].x+tx, pts[i].y+ty
		bx, by := pts[i+1].x+tx, pts[i+1].y+ty
		dx := bx - ax
		dy := by - ay
		l := float32(math.Sqrt(float64(dx*dx + dy*dy)))
		if l < 1e-4 {
			continue
		}
		dx /= l
		dy /= l
		nx := -dy * hw
		ny := dx * hw
		quad := [][]rpoint{{
			{ax + nx, ay + ny},
			{bx + nx, by + ny},
			{bx - nx, by - ny},
			{ax - nx, ay - ny},
		}}
		fillRings(buf, w, h, quad, c, 0, 0)
		fillDisc(buf, w, h, ax, ay, hw, c)
		fillDisc(buf, w, h, bx, by, hw, c)
	}
}

func fillDisc(buf []byte, w, h uint32, cx, cy, r float32, c rcolor) {
	y0 := maxI64(0, int64(math.Floor(float64(cy-r))))
	y1 := minI64(int64(h)-1, int64(math.Ceil(float64(cy+r))))
	for y := y0; y <= y1; y++ {
		dy := (float32(y) + 0.5) - cy
		span2 := r*r - dy*dy
		if span2 < 0 {
			continue
		}
		span := float32(math.Sqrt(float64(span2)))
		x := maxI64(0, int64(math.Floor(float64(cx-span))))
		xend := minI64(int64(w)-1, int64(math.Ceil(float64(cx+span))))
		for ; x <= xend; x++ {
			putOpaque(buf, w, h, x, y, c)
		}
	}
}

// downsample box-downsamples a supersampled opaque buffer to straight-alpha RGBA.
func downsample(out, src []byte, wSS, hSS uint32) {
	fw := wSS / supersample
	fh := hSS / supersample
	block := uint32(supersample * supersample)
	for fy := uint32(0); fy < fh; fy++ {
		for fx := uint32(0); fx < fw; fx++ {
			var sr, sg, sb, cover uint32
			for by := uint32(0); by < supersample; by++ {
				for bx := uint32(0); bx < supersample; bx++ {
					sx := fx*supersample + bx
					sy := fy*supersample + by
					i := (int(sy)*int(wSS) + int(sx)) * 4
					if src[i+3] != 0 {
						sr += uint32(src[i])
						sg += uint32(src[i+1])
						sb += uint32(src[i+2])
						cover++
					}
				}
			}
			o := (int(fy)*int(fw) + int(fx)) * 4
			if cover == 0 {
				out[o], out[o+1], out[o+2], out[o+3] = 0, 0, 0, 0
			} else {
				out[o] = byte(sr / cover)
				out[o+1] = byte(sg / cover)
				out[o+2] = byte(sb / cover)
				out[o+3] = byte((cover * 255) / block)
			}
		}
	}
}

// blit copies a cell's RGBA into the atlas at (dx, dy).
func blit(atlas []byte, atlasW uint32, cell []byte, cw, ch, dx, dy uint32) {
	for y := uint32(0); y < ch; y++ {
		srcOff := int(y) * int(cw) * 4
		dstOff := (int(dy+y)*int(atlasW) + int(dx)) * 4
		copy(atlas[dstOff:dstOff+int(cw)*4], cell[srcOff:srcOff+int(cw)*4])
	}
}

func roundUp(v, m uint32) uint32 { return ((v + m - 1) / m) * m }

func maxU32(a, b uint32) uint32 {
	if a > b {
		return a
	}
	return b
}
func maxI64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
func minI64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
func absf(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}
