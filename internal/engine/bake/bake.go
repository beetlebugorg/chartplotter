// Package bake routes the cached lat/lon Primitive IR into MVT layers and tiles
// many cells together. Each cell is assigned a zoom band from its compilation
// scale so a coarse overview cell bakes at low zooms and a harbour cell at high
// zooms. Colour stays an S-52 token string — the client restyles Day/Dusk/Night
// for free.
//
// Ported (core) from chartplotter/src/bake.zig. Deferred refinements vs the Zig:
// per-feature SCAMIN z-min, DISPLAYBASE down-fill, cross-cell best-available
// suppression, the world-space grid index, sector-light tessellation, and
// grouping a sounding number's digit glyphs into a single feature.
package bake

import (
	"strings"

	"github.com/beetlebugorg/chartplotter-go/internal/engine/mvt"
	"github.com/beetlebugorg/chartplotter-go/internal/engine/portrayal"
	"github.com/beetlebugorg/chartplotter-go/internal/engine/tile"
	"github.com/beetlebugorg/chartplotter-go/pkg/geo"
	"github.com/beetlebugorg/chartplotter-go/pkg/s52"
	"github.com/beetlebugorg/chartplotter-go/pkg/s57"
)

// ZoomRange is a baked [min,max] Web-Mercator zoom span.
type ZoomRange struct{ Min, Max uint32 }

// Band is a NOAA ENC navigational-purpose band. Each bakes over its own zoom
// range and overzooms above max client-side.
type Band uint8

const (
	BandOverview Band = iota
	BandGeneral
	BandCoastal
	BandApproach
	BandHarbor
	BandBerthing
)

// ZoomRange returns the band's baked [minzoom, maxzoom]; min is also the
// SCAMIN/CSCL display z-min.
func (b Band) ZoomRange() ZoomRange {
	switch b {
	case BandOverview:
		return ZoomRange{0, 7}
	case BandGeneral:
		return ZoomRange{7, 9}
	case BandCoastal:
		return ZoomRange{9, 11}
	case BandApproach:
		return ZoomRange{11, 13}
	case BandHarbor:
		return ZoomRange{13, 16}
	default: // berthing
		return ZoomRange{16, 18}
	}
}

// BandForScale maps a compilation-scale denominator (CSCL) to a band.
func BandForScale(cscl uint32) Band {
	n := cscl
	if n == 0 {
		n = 50_000
	}
	switch {
	case n <= 8_000:
		return BandBerthing
	case n <= 32_000:
		return BandHarbor
	case n <= 130_000:
		return BandApproach
	case n <= 500_000:
		return BandCoastal
	case n <= 2_300_000:
		return BandGeneral
	default:
		return BandOverview
	}
}

// routed is one primitive ready to tile: its target layer, geometry (lat/lon),
// bbox, baked zoom span, and the pre-built MVT attributes (minus geometry).
type routed struct {
	layer string
	kind  mvt.GeomType
	rings [][]geo.LatLon // polygon
	line  []geo.LatLon   // linestring
	point geo.LatLon     // point
	bbox  geo.BoundingBox
	zMin  uint32
	zMax  uint32
	attrs []mvt.KeyValue
}

// Baker accumulates routed primitives from many cells, then tiles them.
type Baker struct {
	prims []routed
	bbox  geo.BoundingBox
}

// New returns an empty Baker.
func New() *Baker {
	return &Baker{bbox: geo.EmptyBox()}
}

// Bounds is the union lat/lon bbox of every ingested cell's primitives.
func (b *Baker) Bounds() geo.BoundingBox { return b.bbox }

// AddCell expands every feature of a parsed cell into routed primitives at the
// cell's scale band.
func (b *Baker) AddCell(chart *s57.Chart, lib *s52.Library, mariner *s52.MarinerSettings) {
	band := BandForScale(uint32(chart.CompilationScale()))
	zr := band.ZoomRange()
	features := chart.Features()
	for i := range features {
		f := &features[i]
		fb, ok := portrayal.BuildFeature(lib, mariner, f)
		if !ok {
			continue
		}
		class := f.ObjectClass()
		for _, p := range fb.Primitives {
			b.route(p, class, fb.DisplayPriority, fb.DisplayCategory, band, zr)
		}
	}
}

func (b *Baker) add(r routed) {
	b.bbox.ExtendBox(r.bbox)
	b.prims = append(b.prims, r)
}

func (b *Baker) route(p portrayal.Primitive, class string, drawPrio, cat int, band Band, zr ZoomRange) {
	bnd := int64(band)
	common := func(extra ...mvt.KeyValue) []mvt.KeyValue {
		base := []mvt.KeyValue{
			{Key: "class", Value: mvt.StringVal(class)},
			{Key: "draw_prio", Value: mvt.IntVal(int64(drawPrio))},
			{Key: "cat", Value: mvt.IntVal(int64(cat))},
			{Key: "bnd", Value: mvt.IntVal(bnd)},
		}
		return append(base, extra...)
	}

	switch v := p.(type) {
	case portrayal.FillPolygon:
		b.add(routed{
			layer: "areas", kind: mvt.GeomPolygon, rings: v.Rings, bbox: ringsBbox(v.Rings),
			zMin: zr.Min, zMax: zr.Max,
			attrs: common(mvt.KeyValue{Key: "color_token", Value: mvt.StringVal(v.ColorToken)}),
		})
	case portrayal.PatternFill:
		b.add(routed{
			layer: "area_patterns", kind: mvt.GeomPolygon, rings: v.Rings, bbox: ringsBbox(v.Rings),
			zMin: zr.Min, zMax: zr.Max,
			attrs: common(mvt.KeyValue{Key: "pattern_name", Value: mvt.StringVal(v.PatternName)}),
		})
	case portrayal.StrokeLine:
		b.add(routed{
			layer: "lines", kind: mvt.GeomLineString, line: v.Points, bbox: ptsBbox(v.Points),
			zMin: zr.Min, zMax: zr.Max,
			attrs: common(
				mvt.KeyValue{Key: "color_token", Value: mvt.StringVal(v.ColorToken)},
				mvt.KeyValue{Key: "width_px", Value: mvt.IntVal(int64(v.WidthPx + 0.5))},
				mvt.KeyValue{Key: "dash", Value: mvt.StringVal(dashName(v.Dash))},
			),
		})
	case portrayal.LinePattern:
		b.add(routed{
			layer: "complex_lines", kind: mvt.GeomLineString, line: v.Points, bbox: ptsBbox(v.Points),
			zMin: zr.Min, zMax: zr.Max,
			attrs: common(mvt.KeyValue{Key: "linestyle_name", Value: mvt.StringVal(v.LinestyleName)}),
		})
	case portrayal.DrawText:
		b.add(routed{
			layer: "text", kind: mvt.GeomPoint, point: v.Anchor, bbox: ptBbox(v.Anchor),
			zMin: zr.Min, zMax: zr.Max,
			attrs: common(
				mvt.KeyValue{Key: "text", Value: mvt.StringVal(v.Text)},
				mvt.KeyValue{Key: "font_size_px", Value: mvt.FloatVal(v.FontSizePx)},
				mvt.KeyValue{Key: "color_token", Value: mvt.StringVal(v.ColorToken)},
				mvt.KeyValue{Key: "halign", Value: mvt.StringVal(halignName(v.HAlign))},
				mvt.KeyValue{Key: "valign", Value: mvt.StringVal(valignName(v.VAlign))},
				mvt.KeyValue{Key: "offset_x", Value: mvt.FloatVal(v.OffsetXPx)},
				mvt.KeyValue{Key: "offset_y", Value: mvt.FloatVal(v.OffsetYPx)},
				mvt.KeyValue{Key: "halo_color_token", Value: mvt.StringVal(haloTextColor(v.Halo))},
				mvt.KeyValue{Key: "halo_width", Value: mvt.FloatVal(haloTextWidth(v.Halo))},
			),
		})
	case portrayal.SymbolCall:
		b.routeSymbol(v, common, zr)
	case portrayal.SectorLight:
		// Sector tessellation is deferred; skip for now.
	}
}

func (b *Baker) routeSymbol(v portrayal.SymbolCall, common func(...mvt.KeyValue) []mvt.KeyValue, zr ZoomRange) {
	if strings.HasPrefix(v.SymbolName, "SOUNDG") || strings.HasPrefix(v.SymbolName, "SOUNDS") {
		attrs := common(
			mvt.KeyValue{Key: "symbol_names", Value: mvt.StringVal(v.SymbolName)},
			mvt.KeyValue{Key: "scale", Value: mvt.FloatVal(v.Scale)},
		)
		if !isNaN32(v.SoundingDepthM) {
			attrs = append(attrs,
				mvt.KeyValue{Key: "depth", Value: mvt.FloatVal(v.SoundingDepthM)},
				mvt.KeyValue{Key: "sym_s", Value: mvt.StringVal(soundingVariant(v.SymbolName, 'S'))},
				mvt.KeyValue{Key: "sym_g", Value: mvt.StringVal(soundingVariant(v.SymbolName, 'G'))},
			)
		}
		b.add(routed{layer: "soundings", kind: mvt.GeomPoint, point: v.Anchor, bbox: ptBbox(v.Anchor), zMin: zr.Min, zMax: zr.Max, attrs: attrs})
		return
	}
	attrs := common(
		mvt.KeyValue{Key: "symbol_name", Value: mvt.StringVal(v.SymbolName)},
		mvt.KeyValue{Key: "rotation_deg", Value: mvt.FloatVal(v.RotationDeg)},
		mvt.KeyValue{Key: "scale", Value: mvt.FloatVal(v.Scale)},
		mvt.KeyValue{Key: "offset_x", Value: mvt.FloatVal(v.OffsetXUnits)},
		mvt.KeyValue{Key: "offset_y", Value: mvt.FloatVal(v.OffsetYUnits)},
		mvt.KeyValue{Key: "halo_color_token", Value: mvt.StringVal(haloSymColor(v.Halo))},
		mvt.KeyValue{Key: "halo_width", Value: mvt.FloatVal(haloSymWidth(v.Halo))},
	)
	if !isNaN32(v.DangerDepthM) {
		attrs = append(attrs,
			mvt.KeyValue{Key: "danger_depth", Value: mvt.FloatVal(v.DangerDepthM)},
			mvt.KeyValue{Key: "sym_deep", Value: mvt.StringVal(v.DeepSymbolName)},
		)
	}
	b.add(routed{layer: "point_symbols", kind: mvt.GeomPoint, point: v.Anchor, bbox: ptBbox(v.Anchor), zMin: zr.Min, zMax: zr.Max, attrs: attrs})
}

// TileCoords enumerates every tile (across each primitive's band zooms) that the
// resident primitives touch.
func (b *Baker) TileCoords(extent uint32) []tile.TileCoord {
	seen := map[uint64]struct{}{}
	var out []tile.TileCoord
	for _, r := range b.prims {
		for z := r.zMin; z <= r.zMax; z++ {
			rng := tile.RangeForBbox(z, r.bbox, extent)
			for x := rng.XMin; x <= rng.XMax; x++ {
				for y := rng.YMin; y <= rng.YMax; y++ {
					key := uint64(z)<<40 | uint64(x)<<20 | uint64(y)
					if _, ok := seen[key]; ok {
						continue
					}
					seen[key] = struct{}{}
					out = append(out, tile.TileCoord{Z: z, X: x, Y: y})
				}
			}
		}
	}
	return out
}

// EmitTile bakes the merged MVT for one tile, or nil if it has no features.
func (b *Baker) EmitTile(coord tile.TileCoord, extent uint32, buffer float64) []byte {
	tb := mvt.NewTileBuilder(extent)
	proj := tile.NewProjector(coord, extent)
	rect := tile.RectForTile(extent, buffer)
	e := float64(extent)
	var clip tile.Clipper

	for i := range b.prims {
		r := &b.prims[i]
		if coord.Z < r.zMin || coord.Z > r.zMax {
			continue
		}
		switch r.kind {
		case mvt.GeomPolygon:
			var outRings [][]mvt.IPoint
			for _, ring := range r.rings {
				proj4 := projectRing(ring, proj)
				clipped := clip.Polygon(proj4, rect)
				if len(clipped) < 3 {
					continue
				}
				outRings = append(outRings, quantizeRing(clipped))
			}
			if len(outRings) > 0 {
				tb.Layer(r.layer).AddPolygon(outRings, r.attrs)
			}
		case mvt.GeomLineString:
			projPts := projectRing(r.line, proj)
			runs := tile.ClipLine(projPts, rect)
			if len(runs) == 0 {
				continue
			}
			paths := make([][]mvt.IPoint, 0, len(runs))
			for _, run := range runs {
				if len(run) >= 2 {
					paths = append(paths, quantizeRing(run))
				}
			}
			if len(paths) > 0 {
				tb.Layer(r.layer).AddLines(paths, r.attrs)
			}
		case mvt.GeomPoint:
			p := proj.Project(r.point)
			if p.X < 0 || p.X >= e || p.Y < 0 || p.Y >= e {
				continue
			}
			tb.Layer(r.layer).AddPoints([]mvt.IPoint{tile.Quantize(p)}, r.attrs)
		}
	}

	if tb.IsEmpty() {
		return nil
	}
	return tb.Encode()
}

// -- helpers -----------------------------------------------------------------

func projectRing(ring []geo.LatLon, proj tile.Projector) []tile.FPoint {
	out := make([]tile.FPoint, len(ring))
	for i, p := range ring {
		out[i] = proj.Project(p)
	}
	return out
}

func quantizeRing(pts []tile.FPoint) []mvt.IPoint {
	out := make([]mvt.IPoint, len(pts))
	for i, p := range pts {
		out[i] = tile.Quantize(p)
	}
	return out
}

func ringsBbox(rings [][]geo.LatLon) geo.BoundingBox {
	bb := geo.EmptyBox()
	for _, r := range rings {
		for _, p := range r {
			bb.ExtendPoint(p)
		}
	}
	return bb
}

func ptsBbox(pts []geo.LatLon) geo.BoundingBox {
	bb := geo.EmptyBox()
	for _, p := range pts {
		bb.ExtendPoint(p)
	}
	return bb
}

func ptBbox(p geo.LatLon) geo.BoundingBox {
	return geo.BoundingBox{MinLat: p.Lat, MinLon: p.Lon, MaxLat: p.Lat, MaxLon: p.Lon}
}

func dashName(d portrayal.Dash) string {
	switch d {
	case portrayal.DashDashed:
		return "dashed"
	case portrayal.DashDotted:
		return "dotted"
	default:
		return "solid"
	}
}

func halignName(a portrayal.HAlign) string {
	switch a {
	case portrayal.HAlignCenter:
		return "center"
	case portrayal.HAlignRight:
		return "right"
	default:
		return "left"
	}
}

func valignName(a portrayal.VAlign) string {
	switch a {
	case portrayal.VAlignTop:
		return "top"
	case portrayal.VAlignMiddle:
		return "middle"
	case portrayal.VAlignBaseline:
		return "baseline"
	default:
		return "bottom"
	}
}

func haloTextColor(h *portrayal.TextHalo) string {
	if h == nil {
		return ""
	}
	return h.ColorToken
}

func haloTextWidth(h *portrayal.TextHalo) float32 {
	if h == nil {
		return 0
	}
	return h.WidthPx
}

func haloSymColor(h *portrayal.SymbolHalo) string {
	if h == nil {
		return ""
	}
	return h.ColorToken
}

func haloSymWidth(h *portrayal.SymbolHalo) float32 {
	if h == nil {
		return 0
	}
	return h.ExtraWidthPx
}

// soundingVariant swaps a sounding glyph's palette letter: SOUNDS14 <-> SOUNDG14
// (S = bold/shallow, G = faint/deep), so the client runs SNDFRM04's safety-depth
// split live.
func soundingVariant(name string, letter byte) string {
	if len(name) >= 6 && strings.HasPrefix(name, "SOUND") {
		return name[:5] + string(letter) + name[6:]
	}
	return name
}

func isNaN32(f float32) bool { return f != f }
