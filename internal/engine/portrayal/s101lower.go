package portrayal

import (
	"math"

	"github.com/beetlebugorg/chartplotter/pkg/geo"
	"github.com/beetlebugorg/chartplotter/pkg/s100/catalog"
	"github.com/beetlebugorg/chartplotter/pkg/s100/instructions"
)

// This file is the S-101 drawing-command lowering: it turns one resolved S-101
// drawing command (from pkg/s100/instructions) plus the feature geometry into a
// viewport-independent Primitive that everything downstream (projection, MVT
// bake, client colour resolution) consumes. Colour stays a token; line-style
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

// LowerS101 maps one resolved S-101 draw command onto engine Primitives,
// attaching geometry and resolving line-style references against the catalogue.
// It returns a slice because an area-boundary line fans into one line primitive
// per ring; fills/symbols/text are a single primitive.
// An empty slice means nothing to draw (a no-op, an unlowered draw kind, or a
// draw whose geometry is missing).
func LowerS101(cmd instructions.DrawCommand, geom S101Geometry, cat *catalog.Catalog) []Primitive {
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
