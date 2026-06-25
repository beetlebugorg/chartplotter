package portrayal

import (
	"math"
	"strconv"
	"strings"

	"github.com/beetlebugorg/chartplotter/pkg/geo"
	"github.com/beetlebugorg/chartplotter/pkg/s100/catalog"
	"github.com/beetlebugorg/chartplotter/pkg/s100/instructions"
)

// This file emits primitives from S-101 drawing commands: it turns one resolved
// S-101 drawing command (from pkg/s100/instructions) plus the feature geometry
// into a viewport-independent Primitive that everything downstream (projection,
// MVT bake, client colour resolution) consumes. Colour stays a token; line-style
// refs resolve against the S-101 catalogue.

// mmPerSymbolUnit-derived conversions. S-101 widths/offsets are millimetres;
// the engine uses pixels (at DefaultPxPerSymbolUnit) and 0.01-mm symbol units.
const (
	pxPerMM    = float64(DefaultPxPerSymbolUnit) * 100 // px per mm (1mm = 100 symbol units)
	unitsPerMM = 100.0                                 // 0.01-mm symbol units per mm
)

// S101Geometry carries the feature geometry a draw command attaches to. The
// command's Op selects which field is used: Anchor (point symbols/text), Lines
// (line strokes), Rings (area fills, outer ring first then holes).
type S101Geometry struct {
	Anchor geo.LatLon
	// Rings are the feature's COMPLETE area rings — area fills (ColorFill/
	// AreaFill) use these unchanged. Empty for non-area features.
	Rings [][]geo.LatLon
	// Lines are the DRAWABLE polylines a line draw strokes, already reduced to
	// the masked / data-limit parts (S-52 §8.6.2): a line feature's drawable
	// parts, or — for an area — its drawable boundary (coastline-coincident and
	// MASK=1/USAG=3 edges removed). One stroke primitive is emitted per run, so a
	// masked area boundary skips its land-shared edges while the fill stays whole.
	Lines [][]geo.LatLon
}

// emitPrimitives maps one resolved S-101 draw command onto engine Primitives,
// attaching geometry and resolving line-style references against the catalogue.
// It returns a slice because an area-boundary line fans into one line primitive
// per ring; fills/symbols/text are a single primitive.
// An empty slice means nothing to draw (a no-op, an unhandled draw kind, or a
// draw whose geometry is missing).
func emitPrimitives(cmd instructions.DrawCommand, geom S101Geometry, cat *catalog.Catalog) []Primitive {
	switch cmd.Op {
	case instructions.OpColorFill:
		if len(geom.Rings) == 0 {
			return nil
		}
		return []Primitive{FillPolygon{Rings: geom.Rings, ColorToken: cmd.Reference}}

	case instructions.OpAreaFill:
		if len(geom.Rings) == 0 {
			return nil
		}
		return []Primitive{PatternFill{Rings: geom.Rings, PatternName: cmd.Reference}}

	case instructions.OpLine:
		out := make([]Primitive, 0, len(geom.Lines))
		for _, pts := range geom.Lines {
			if len(pts) < 2 {
				continue // a degenerate ring/run can't be stroked
			}
			if cmd.Reference == "_simple_" && cmd.SimpleLine != nil {
				out = append(out, StrokeLine{
					Points:     pts,
					ColorToken: cmd.SimpleLine.Color,
					WidthPx:    float32(cmd.SimpleLine.Width * pxPerMM),
					Dash:       dashFor(cmd.SimpleLine.DashLength),
				})
			} else {
				out = append(out, LinePattern{
					Points:        pts,
					LinestyleName: cmd.Reference,
					ColorToken:    linePenColor(cmd.Reference, cat),
				})
			}
		}
		return out

	case instructions.OpPoint:
		anchor := geom.Anchor
		if cmd.HasAnchor { // an AugmentedPoint draw (e.g. one SOUNDG sounding)
			anchor = geo.LatLon{Lat: cmd.Anchor[1], Lon: cmd.Anchor[0]}
		} else if p, ok := pointAlongLines(geom.Lines, cmd.LinePlacement); ok {
			// A point symbol placed at a relative position along a line feature
			// (LinePlacement:Relative,<frac>) — e.g. a recommended-track / route
			// arrow or a cable/pipeline marker. Without this every such symbol
			// collapsed to the feature's midpoint anchor.
			anchor = p
		}
		return []Primitive{SymbolCall{
			Anchor:            anchor,
			SymbolName:        cmd.Reference,
			RotationDeg:       float32(cmd.Rotation),
			RotationTrueNorth: cmd.RotationTrueNorth,
			OffsetXUnits:      float32(cmd.Offset[0] * unitsPerMM),
			OffsetYUnits:      float32(cmd.Offset[1] * unitsPerMM),
			Scale:             DefaultPxPerSymbolUnit, // matches the sprite atlas px_per_unit
			SoundingDepthM:    float32(math.NaN()),
			DangerDepthM:      float32(math.NaN()),
		}}

	case instructions.OpText:
		if cmd.Reference == "" {
			return nil
		}
		fontPx := float32(cmd.FontSizePx)
		if fontPx <= 0 {
			fontPx = 12 // default body size
		}
		var halo *TextHalo
		if fontPx >= 10 {
			halo = &TextHalo{ColorToken: "CHWHT", WidthPx: 1}
		}
		color := cmd.FontColor
		if color == "" {
			color = "CHBLK"
		}
		return []Primitive{DrawText{
			Anchor:     geom.Anchor,
			Text:       cmd.Reference,
			FontSizePx: fontPx,
			ColorToken: color,
			Halo:       halo,
			HAlign:     hAlign(cmd.TextAlignH),
			VAlign:     vAlign(cmd.TextAlignV),
			OffsetYPx:  float32(cmd.TextVOffset),
			// AddTextInstruction emits "ViewingGroup:<textViewingGroup>,<viewingGroup>",
			// so for text cmd.ViewingGroup is the S-52 text group (11 important,
			// 21/26/29 names, 23 light, …). Carry it as `tgrp` so the client's
			// §14.5 text-group toggles work. Without it every label is group 0 →
			// "Other", and Important/Names toggle nothing.
			Group: cmd.ViewingGroup,
		}}

	default: // OpNull, OpOther
		return nil
	}
}

// pointAlongLines returns the point at the LinePlacement position along the line
// runs, or ok=false when the placement isn't a usable "Relative,<frac>" spec or
// there's no line geometry (the caller then keeps the feature anchor). frac is
// clamped to [0,1] and measured by arc length across all runs, with a cos-lat
// correction so the fraction tracks ground distance rather than raw degrees.
func pointAlongLines(lines [][]geo.LatLon, placement string) (geo.LatLon, bool) {
	if placement == "" || len(lines) == 0 {
		return geo.LatLon{}, false
	}
	mode, val, _ := strings.Cut(placement, ",")
	if !strings.EqualFold(strings.TrimSpace(mode), "Relative") {
		return geo.LatLon{}, false // Absolute / unknown placement: keep the anchor
	}
	frac, err := strconv.ParseFloat(strings.TrimSpace(val), 64)
	if err != nil {
		return geo.LatLon{}, false
	}
	frac = math.Max(0, math.Min(1, frac))

	// Total arc length of all runs (cos-lat corrected planar approximation —
	// chart line features are short enough that great-circle curvature is
	// negligible at this placement precision).
	seg := func(a, b geo.LatLon) float64 {
		dLat := b.Lat - a.Lat
		dLon := (b.Lon - a.Lon) * math.Cos((a.Lat+b.Lat)*0.5*math.Pi/180)
		return math.Hypot(dLat, dLon)
	}
	var total float64
	for _, run := range lines {
		for i := 1; i < len(run); i++ {
			total += seg(run[i-1], run[i])
		}
	}
	if total == 0 {
		// Degenerate (all coincident): fall back to the first vertex.
		for _, run := range lines {
			if len(run) > 0 {
				return run[0], true
			}
		}
		return geo.LatLon{}, false
	}

	target := frac * total
	var acc float64
	for _, run := range lines {
		for i := 1; i < len(run); i++ {
			d := seg(run[i-1], run[i])
			if acc+d >= target {
				t := 0.0
				if d > 0 {
					t = (target - acc) / d
				}
				return geo.LatLon{
					Lat: run[i-1].Lat + t*(run[i].Lat-run[i-1].Lat),
					Lon: run[i-1].Lon + t*(run[i].Lon-run[i-1].Lon),
				}, true
			}
			acc += d
		}
	}
	// frac == 1 (or rounding): the last vertex of the last non-empty run.
	for i := len(lines) - 1; i >= 0; i-- {
		if n := len(lines[i]); n > 0 {
			return lines[i][n-1], true
		}
	}
	return geo.LatLon{}, false
}

func hAlign(s string) HAlign {
	switch s {
	case "Center":
		return HAlignCenter
	case "Right":
		return HAlignRight
	default:
		return HAlignLeft
	}
}

func vAlign(s string) VAlign {
	switch s {
	case "Top":
		return VAlignTop
	case "Center":
		return VAlignMiddle
	default:
		return VAlignBottom
	}
}

func dashFor(dashLength float64) Dash {
	if dashLength > 0 {
		return DashDashed
	}
	return DashSolid
}

// linePenColor returns a named line style's primary pen colour token, used to
// tint the complex-line dash run client-side. "" when unknown.
func linePenColor(ref string, cat *catalog.Catalog) string {
	if cat == nil {
		return ""
	}
	ls, ok := cat.LineStyles[ref]
	if !ok {
		return ""
	}
	if ls.PenColor != "" {
		return ls.PenColor
	}
	for _, c := range ls.Components { // composite: take the first component's pen
		if c.PenColor != "" {
			return c.PenColor
		}
	}
	return ""
}
