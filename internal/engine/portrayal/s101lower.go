package portrayal

import (
	"math"

	"github.com/beetlebugorg/chartplotter/pkg/geo"
	"github.com/beetlebugorg/chartplotter/pkg/s100/catalog"
	"github.com/beetlebugorg/chartplotter/pkg/s100/instructions"
)

// This file is the S-101 backport's D→primitive lowering (see
// specs/s101-portrayal-backport.md): it turns one resolved S-101 drawing
// command (from pkg/s100/instructions) plus the feature geometry into the same
// viewport-independent Primitive the S-52 path produces, so everything
// downstream (projection, MVT bake, client colour resolution) is unchanged.
// Colour stays a token; line-style refs resolve against the S-101 catalogue.

// mmPerSymbolUnit-derived conversions. S-101 widths/offsets are millimetres;
// the engine uses pixels (at DefaultPxPerSymbolUnit) and 0.01-mm symbol units.
const (
	pxPerMM    = float64(DefaultPxPerSymbolUnit) * 100 // px per mm (1mm = 100 symbol units)
	unitsPerMM = 100.0                                 // 0.01-mm symbol units per mm
)

// S101Geometry carries the feature geometry a draw command attaches to. The
// command's Op selects which field is used: Anchor (points), Points (lines),
// Rings (areas, outer ring first then holes).
type S101Geometry struct {
	Anchor geo.LatLon
	Points []geo.LatLon
	Rings  [][]geo.LatLon
}

// LowerS101 maps one resolved S-101 draw command onto an engine Primitive,
// attaching geometry and resolving line-style references against the catalogue.
// ok is false for a no-op (Null) or a draw kind not yet lowered (the caller can
// emit a placeholder).
func LowerS101(cmd instructions.DrawCommand, geom S101Geometry, cat *catalog.Catalog) (Primitive, bool) {
	switch cmd.Op {
	case instructions.OpColorFill:
		return FillPolygon{Rings: geom.Rings, ColorToken: cmd.Reference}, true

	case instructions.OpAreaFill:
		// S-101 area-fill patterns reference S-101 symbol tiles, but the S-101
		// pattern atlas (patterns.{png,json}) isn't emitted yet — so the client
		// would resolve these against the S-52 pattern atlas and render garbage
		// (wrong sprite offsets). Suppress until S-101 patterns are emitted; the
		// underlying ColorFill still shows. (Tracked gap.)
		return nil, false

	case instructions.OpLine:
		if cmd.Reference == "_simple_" && cmd.SimpleLine != nil {
			return StrokeLine{
				Points:     geom.Points,
				ColorToken: cmd.SimpleLine.Color,
				WidthPx:    float32(cmd.SimpleLine.Width * pxPerMM),
				Dash:       dashFor(cmd.SimpleLine.DashLength),
			}, true
		}
		return LinePattern{
			Points:        geom.Points,
			LinestyleName: cmd.Reference,
			ColorToken:    linePenColor(cmd.Reference, cat),
		}, true

	case instructions.OpPoint:
		return SymbolCall{
			Anchor:         geom.Anchor,
			SymbolName:     cmd.Reference,
			RotationDeg:    float32(cmd.Rotation),
			OffsetXUnits:   float32(cmd.Offset[0] * unitsPerMM),
			OffsetYUnits:   float32(cmd.Offset[1] * unitsPerMM),
			Scale:          DefaultPxPerSymbolUnit, // same as the S-52 path; matches the sprite atlas px_per_unit
			SoundingDepthM: float32(math.NaN()),
			DangerDepthM:   float32(math.NaN()),
		}, true

	case instructions.OpText:
		if cmd.Reference == "" {
			return nil, false
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
		return DrawText{
			Anchor:     geom.Anchor,
			Text:       cmd.Reference,
			FontSizePx: fontPx,
			ColorToken: color,
			Halo:       halo,
			HAlign:     hAlign(cmd.TextAlignH),
			VAlign:     vAlign(cmd.TextAlignV),
			OffsetYPx:  float32(cmd.TextVOffset),
		}, true

	default: // OpNull, OpOther
		return nil, false
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
