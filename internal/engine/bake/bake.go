// Package bake routes the cached lat/lon Primitive IR into MVT layers and tiles
// many cells together. Each cell is assigned a zoom band from its compilation
// scale so a coarse overview cell bakes at low zooms and a harbour cell at high
// zooms. Colour stays an S-52 token string — the client restyles Day/Dusk/Night
// for free.
//
// This is the single-archive (provisioned) bake the Zig reference calls
// `spec_display`: a feature's display z-min comes from SCAMIN (S-52 §10.4.2,
// defaulting to z0 so coverage never goes blank on zoom-out), and where cells of
// different scales overlap, emitTile applies best-available-data suppression both
// ways (below the native bands only the coarsest cell's blanket shows; above them
// only the finest cell's chart). A normalized-world bbox reject prunes far
// primitives before projection.
//
// Ported from chartplotter/src/bake.zig (+ sectorlights.zig): SCAMIN z-min,
// native bands + best-available suppression, sounding-number grouping,
// OBSTRN/WRECKS danger-depth carriage, and per-zoom sector-light tessellation
// into the lines layer.
package bake

import (
	"math"
	"strconv"
	"strings"

	"github.com/beetlebugorg/chartplotter-go/internal/engine/mvt"
	"github.com/beetlebugorg/chartplotter-go/internal/engine/pmtiles"
	"github.com/beetlebugorg/chartplotter-go/internal/engine/portrayal"
	"github.com/beetlebugorg/chartplotter-go/internal/engine/tile"
	"github.com/beetlebugorg/chartplotter-go/pkg/geo"
	"github.com/beetlebugorg/chartplotter-go/pkg/s52"
	"github.com/beetlebugorg/chartplotter-go/pkg/s57"
)

const maxBandZ uint32 = 18

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

// scaminZoom maps an S-52 SCAMIN (1:N denominator) to the lowest Web-Mercator
// zoom whose display-scale denominator is <= SCAMIN — where the object first
// becomes visible (OGC/equator scale set). Clamped to the baked zoom span.
func scaminZoom(scamin uint32) uint32 {
	if scamin == 0 {
		return 0
	}
	const denomZ0 = 559_082_264.029 // 1:N at z0, equator, OGC 0.28 mm px
	s := float64(scamin)
	if denomZ0 <= s {
		return 0
	}
	z := math.Ceil(math.Log2(denomZ0 / s))
	if z >= float64(maxBandZ) {
		return maxBandZ
	}
	if z < 0 {
		return 0
	}
	return uint32(z)
}

// specZMin is the single-archive display z-min: DISPLAYBASE objects and objects
// without SCAMIN have no minimum display scale (z0); SCAMIN raises it. Coarse-tile
// pile-up is bounded by emitTile's best-available suppression.
func specZMin(displayCategory int, scamin uint32) uint32 {
	if displayCategory == s52.DisplayBase {
		return 0
	}
	if scamin != 0 {
		return scaminZoom(scamin)
	}
	return 0
}

// routed is one primitive ready to tile: target layer, geometry (lat/lon), the
// normalized-world bbox (for the spatial reject + overlap test), the display and
// native zoom spans, and the pre-built MVT attributes (minus geometry).
type routed struct {
	layer string
	kind  mvt.GeomType
	rings [][]geo.LatLon // polygon
	line  []geo.LatLon   // linestring
	point geo.LatLon     // point

	wMinX, wMinY, wMaxX, wMaxY float64 // normalized world bbox [0,1]
	zMin, zMax                 uint32  // display zoom span
	natMin, natMax             uint32  // native band zoom span (for suppression)

	attrs []mvt.KeyValue
}

// sectorPrim is a LIGHTS06 sector light. Its geometry (dashed legs, OUTLW-backed
// coloured arc / ring) is screen-px sized, so it is tessellated per zoom at emit
// time into the lines layer rather than stored as fixed lat/lon geometry.
type sectorPrim struct {
	anchor   geo.LatLon
	params   portrayal.SectorParams
	class    string
	drawPrio int
	cat      int
	band     Band
	zMin     uint32
	natMax   uint32
}

// Baker accumulates routed primitives from many cells, then tiles them.
type Baker struct {
	prims   []routed
	sectors []sectorPrim
	bbox    geo.BoundingBox
}

// New returns an empty Baker.
func New() *Baker { return &Baker{bbox: geo.EmptyBox()} }

// Bounds is the union lat/lon bbox of every ingested cell's primitives.
func (b *Baker) Bounds() geo.BoundingBox { return b.bbox }

// AddCell expands every feature of a parsed cell into routed primitives at the
// cell's scale band, with per-feature SCAMIN display z-min.
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
		scamin := intAttr(f.Attributes(), "SCAMIN")
		zMin := specZMin(fb.DisplayCategory, scamin)
		class := f.ObjectClass()
		prims := fb.Primitives
		for pi := 0; pi < len(prims); pi++ {
			// A sounding number (SOUNDG03) emits one digit glyph per column, all at
			// the same anchor (the glyph art carries the column shift). Group a
			// number's consecutive same-anchor digit glyphs into ONE soundings
			// feature so the client renders the whole number and declutter treats
			// it as a single unit (matches bake.zig).
			if sc, ok := prims[pi].(portrayal.SymbolCall); ok && isSoundingName(sc.SymbolName) {
				names := []string{sc.SymbolName}
				for pi+1 < len(prims) {
					nsc, ok := prims[pi+1].(portrayal.SymbolCall)
					if !ok || !isSoundingName(nsc.SymbolName) || nsc.Anchor != sc.Anchor {
						break
					}
					names = append(names, nsc.SymbolName)
					pi++
				}
				b.routeSoundingGroup(names, sc, class, fb.DisplayPriority, fb.DisplayCategory, band, zr, zMin)
				continue
			}
			b.route(prims[pi], class, fb.DisplayPriority, fb.DisplayCategory, band, zr, zMin)
		}
	}
}

// routeSoundingGroup emits one soundings feature for a whole sounding number
// (the comma-joined digit-glyph list), carrying depth + both palette variants so
// the client runs SNDFRM04's safety-depth split live.
func (b *Baker) routeSoundingGroup(names []string, sc portrayal.SymbolCall, class string, drawPrio, cat int, band Band, zr ZoomRange, zMin uint32) {
	joined := strings.Join(names, ",")
	r := routed{layer: "soundings", kind: mvt.GeomPoint, point: sc.Anchor, zMin: zMin, zMax: zr.Max, natMin: zr.Min, natMax: zr.Max}
	attrs := []mvt.KeyValue{
		{Key: "class", Value: mvt.StringVal(class)},
		{Key: "draw_prio", Value: mvt.IntVal(int64(drawPrio))},
		{Key: "cat", Value: mvt.IntVal(int64(cat))},
		{Key: "bnd", Value: mvt.IntVal(int64(band))},
		{Key: "symbol_names", Value: mvt.StringVal(joined)},
		{Key: "scale", Value: mvt.FloatVal(sc.Scale)},
	}
	if !isNaN32(sc.SoundingDepthM) {
		attrs = append(attrs,
			mvt.KeyValue{Key: "depth", Value: mvt.FloatVal(sc.SoundingDepthM)},
			mvt.KeyValue{Key: "sym_s", Value: mvt.StringVal(soundingVariant(joined, 'S'))},
			mvt.KeyValue{Key: "sym_g", Value: mvt.StringVal(soundingVariant(joined, 'G'))},
		)
	}
	r.attrs = attrs
	b.add(r, ptBbox(sc.Anchor))
}

func (b *Baker) add(r routed, bb geo.BoundingBox) {
	b.bbox.ExtendBox(bb)
	r.wMinX = normX(bb.MinLon)
	r.wMaxX = normX(bb.MaxLon)
	r.wMinY = normY(bb.MaxLat) // north -> smaller y
	r.wMaxY = normY(bb.MinLat) // south -> larger y
	b.prims = append(b.prims, r)
}

func (b *Baker) route(p portrayal.Primitive, class string, drawPrio, cat int, band Band, zr ZoomRange, zMin uint32) {
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
	r := routed{zMin: zMin, zMax: zr.Max, natMin: zr.Min, natMax: zr.Max}

	switch v := p.(type) {
	case portrayal.FillPolygon:
		r.layer, r.kind, r.rings = "areas", mvt.GeomPolygon, v.Rings
		r.attrs = common(mvt.KeyValue{Key: "color_token", Value: mvt.StringVal(v.ColorToken)})
		b.add(r, ringsBbox(v.Rings))
	case portrayal.PatternFill:
		r.layer, r.kind, r.rings = "area_patterns", mvt.GeomPolygon, v.Rings
		r.attrs = common(mvt.KeyValue{Key: "pattern_name", Value: mvt.StringVal(v.PatternName)})
		b.add(r, ringsBbox(v.Rings))
	case portrayal.StrokeLine:
		r.layer, r.kind, r.line = "lines", mvt.GeomLineString, v.Points
		r.attrs = common(
			mvt.KeyValue{Key: "color_token", Value: mvt.StringVal(v.ColorToken)},
			mvt.KeyValue{Key: "width_px", Value: mvt.IntVal(int64(v.WidthPx + 0.5))},
			mvt.KeyValue{Key: "dash", Value: mvt.StringVal(dashName(v.Dash))},
		)
		b.add(r, ptsBbox(v.Points))
	case portrayal.LinePattern:
		r.layer, r.kind, r.line = "complex_lines", mvt.GeomLineString, v.Points
		r.attrs = common(mvt.KeyValue{Key: "linestyle_name", Value: mvt.StringVal(v.LinestyleName)})
		b.add(r, ptsBbox(v.Points))
	case portrayal.DrawText:
		r.layer, r.kind, r.point = "text", mvt.GeomPoint, v.Anchor
		r.attrs = common(
			mvt.KeyValue{Key: "text", Value: mvt.StringVal(v.Text)},
			mvt.KeyValue{Key: "font_size_px", Value: mvt.FloatVal(v.FontSizePx)},
			mvt.KeyValue{Key: "color_token", Value: mvt.StringVal(v.ColorToken)},
			mvt.KeyValue{Key: "halign", Value: mvt.StringVal(halignName(v.HAlign))},
			mvt.KeyValue{Key: "valign", Value: mvt.StringVal(valignName(v.VAlign))},
			mvt.KeyValue{Key: "offset_x", Value: mvt.FloatVal(v.OffsetXPx)},
			mvt.KeyValue{Key: "offset_y", Value: mvt.FloatVal(v.OffsetYPx)},
			mvt.KeyValue{Key: "halo_color_token", Value: mvt.StringVal(haloTextColor(v.Halo))},
			mvt.KeyValue{Key: "halo_width", Value: mvt.FloatVal(haloTextWidth(v.Halo))},
		)
		b.add(r, ptBbox(v.Anchor))
	case portrayal.SymbolCall:
		b.routeSymbol(v, common, r)
	case portrayal.SectorLight:
		b.bbox.ExtendPoint(v.Anchor)
		b.sectors = append(b.sectors, sectorPrim{
			anchor: v.Anchor, params: v.Sector, class: class,
			drawPrio: drawPrio, cat: cat, band: band, zMin: zMin, natMax: zr.Max,
		})
	}
}

// routeSymbol routes a non-sounding SY symbol to point_symbols. Sounding digits
// are grouped in AddCell, so they never reach here.
func (b *Baker) routeSymbol(v portrayal.SymbolCall, common func(...mvt.KeyValue) []mvt.KeyValue, r routed) {
	r.kind, r.point = mvt.GeomPoint, v.Anchor
	r.layer = "point_symbols"
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
	r.attrs = attrs
	b.add(r, ptBbox(v.Anchor))
}

// TileCoords enumerates every tile (across each primitive's display zooms) that
// the resident primitives touch.
func (b *Baker) TileCoords(extent uint32) []tile.TileCoord {
	seen := map[uint64]struct{}{}
	var out []tile.TileCoord
	for i := range b.prims {
		r := &b.prims[i]
		bb := geo.BoundingBox{
			MinLat: unnormY(r.wMaxY), MinLon: r.wMinX*360 - 180,
			MaxLat: unnormY(r.wMinY), MaxLon: r.wMaxX*360 - 180,
		}
		out = addRange(out, seen, bb, r.zMin, r.zMax, extent)
	}
	// Sector lights anchor a small screen-px figure; their own anchor tile (plus
	// the bbox reject margin in EmitTile) covers the spill.
	for i := range b.sectors {
		sp := &b.sectors[i]
		bb := geo.BoundingBox{MinLat: sp.anchor.Lat, MinLon: sp.anchor.Lon, MaxLat: sp.anchor.Lat, MaxLon: sp.anchor.Lon}
		out = addRange(out, seen, bb, sp.zMin, sp.natMax, extent)
	}
	return out
}

func addRange(out []tile.TileCoord, seen map[uint64]struct{}, bb geo.BoundingBox, zMin, zMax, extent uint32) []tile.TileCoord {
	for z := zMin; z <= zMax; z++ {
		rng := tile.RangeForBbox(z, bb, extent)
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
	return out
}

// BakePMTiles bakes every resident tile into a PMTiles archive builder (empty
// tiles dropped, identical tiles deduped). Call WriteTo/Finish on the result.
func (b *Baker) BakePMTiles(extent uint32, buffer float64) *pmtiles.Builder {
	pb := pmtiles.New()
	for _, c := range b.TileCoords(extent) {
		if data := b.EmitTile(c, extent, buffer); data != nil {
			pb.AddTile(uint8(c.Z), c.X, c.Y, data)
		}
	}
	return pb
}

// EmitTile bakes the merged MVT for one tile, or nil if it has no features.
// band_z is coord.Z (the per-primitive in-band test zoom).
func (b *Baker) EmitTile(coord tile.TileCoord, extent uint32, buffer float64) []byte {
	tb := mvt.NewTileBuilder(extent)
	proj := tile.NewProjector(coord, extent)
	rect := tile.RectForTile(extent, buffer)
	e := float64(extent)
	bandZ := coord.Z

	// Spatial reject in normalized-world coords, then in-display-range filter.
	n := math.Pow(2, float64(coord.Z))
	bufN := (buffer / float64(extent)) / n
	tnx0, tnx1 := float64(coord.X)/n-bufN, float64(coord.X+1)/n+bufN
	tny0, tny1 := float64(coord.Y)/n-bufN, float64(coord.Y+1)/n+bufN

	var eligible []int
	var finestNat uint32
	for i := range b.prims {
		r := &b.prims[i]
		if coord.Z < r.zMin || coord.Z > r.zMax {
			continue
		}
		if r.wMaxX < tnx0 || r.wMinX > tnx1 || r.wMaxY < tny0 || r.wMinY > tny1 {
			continue
		}
		eligible = append(eligible, i)
		if r.natMax != math.MaxUint32 && r.natMax > finestNat {
			finestNat = r.natMax
		}
	}

	for _, i := range eligible {
		r := &b.prims[i]
		// Best-available suppression: below its native band, yield only where no
		// coarser cell covers; above its native band, only the finest shows.
		if bandZ < r.natMin && b.anyCoarserOverlaps(eligible, r) {
			continue
		}
		if bandZ > r.natMax && r.natMax < finestNat {
			continue
		}
		switch r.kind {
		case mvt.GeomPolygon:
			var outRings [][]mvt.IPoint
			var clip tile.Clipper
			for _, ring := range r.rings {
				clipped := clip.Polygon(projectRing(ring, proj), rect)
				if len(clipped) < 3 {
					continue
				}
				outRings = append(outRings, quantizeRing(clipped))
			}
			if len(outRings) > 0 {
				tb.Layer(r.layer).AddPolygon(outRings, r.attrs)
			}
		case mvt.GeomLineString:
			runs := tile.ClipLine(projectRing(r.line, proj), rect)
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

	// Sector lights: tessellate per zoom into the lines layer. Their screen-px
	// geometry can spill into neighbouring tiles, so reject with a margin sized to
	// the largest radius (the ring's 26 mm).
	margin := (26.0*float64(portrayal.DefaultPxPerSymbolUnit)*100.0 + buffer) / float64(extent) / n
	for i := range b.sectors {
		sp := &b.sectors[i]
		if coord.Z < sp.zMin || coord.Z > sp.natMax {
			continue
		}
		ax, ay := normX(sp.anchor.Lon), normY(sp.anchor.Lat)
		if ax < tnx0-margin || ax > tnx1+margin || ay < tny0-margin || ay > tny1+margin {
			continue
		}
		for _, st := range expandSector(sp.anchor, sp.params, coord.Z) {
			runs := tile.ClipLine(projectRing(st.points, proj), rect)
			paths := make([][]mvt.IPoint, 0, len(runs))
			for _, run := range runs {
				if len(run) >= 2 {
					paths = append(paths, quantizeRing(run))
				}
			}
			if len(paths) == 0 {
				continue
			}
			dash := "solid"
			if st.dashed {
				dash = "dashed"
			}
			tb.Layer("lines").AddLines(paths, []mvt.KeyValue{
				{Key: "class", Value: mvt.StringVal(sp.class)},
				{Key: "color_token", Value: mvt.StringVal(st.colorToken)},
				{Key: "width_px", Value: mvt.IntVal(int64(st.widthPx + 0.5))},
				{Key: "dash", Value: mvt.StringVal(dash)},
				{Key: "cat", Value: mvt.IntVal(int64(sp.cat))},
				{Key: "bnd", Value: mvt.IntVal(int64(sp.band))},
				{Key: "draw_prio", Value: mvt.IntVal(int64(sp.drawPrio))},
			})
		}
	}

	if tb.IsEmpty() {
		return nil
	}
	return tb.Encode()
}

// sectorStroke is one tessellated piece of sector geometry: a lat/lon polyline
// plus the S-52 pen token + width the lines layer carries.
type sectorStroke struct {
	points     []geo.LatLon
	colorToken string
	widthPx    float32
	dashed     bool
}

// expandSector tessellates a LIGHTS06 sector at anchor into lat/lon line strokes
// sized for integer zoom z (screen-px radii). Ported from sectorlights.zig: a
// ring is one OUTLW-backed coloured circle (26 mm); a sector is two dashed CHBLK
// legs (25 mm) plus an OUTLW-backed coloured arc (20 mm). SECTR1/2 are from
// seaward, so bearings are reversed +180.
func expandSector(anchor geo.LatLon, p portrayal.SectorParams, z uint32) []sectorStroke {
	worldPx := 256.0 * math.Pow(2, float64(z))
	ax, ay := normX(anchor.Lon)*worldPx, normY(anchor.Lat)*worldPx
	pxPerMM := float64(portrayal.DefaultPxPerSymbolUnit) * 100.0
	color := p.ColorToken
	if color == "" {
		color = "LITRD"
	}

	sweep := p.EndAngleDeg - p.StartAngleDeg
	isRing := math.Abs(sweep) < 1e-6 || math.Abs(math.Abs(sweep)-360) < 1e-6
	if isRing {
		return emitArc(nil, ax, ay, worldPx, 26.0, color, 0, 360, pxPerMM)
	}
	a1 := p.StartAngleDeg + 180.0
	a2 := p.EndAngleDeg + 180.0
	if a2 <= a1 {
		a2 += 360.0
	}
	legLen := 25.0 * pxPerMM
	var out []sectorStroke
	out = emitLeg(out, ax, ay, worldPx, a1, legLen)
	out = emitLeg(out, ax, ay, worldPx, a2, legLen)
	out = emitArc(out, ax, ay, worldPx, 20.0, color, a1, a2, pxPerMM)
	return out
}

func bearingToScreen(deg float64) (float64, float64) {
	r := deg * math.Pi / 180.0
	return math.Sin(r), -math.Cos(r) // y grows southward: north=(0,-1), east=(1,0)
}

func sunproject(x, y, worldPx float64) geo.LatLon {
	return geo.LatLon{Lat: unnormY(y / worldPx), Lon: x/worldPx*360 - 180}
}

func emitLeg(out []sectorStroke, ax, ay, worldPx, bearingDeg, lenPx float64) []sectorStroke {
	if lenPx <= 0 {
		return out
	}
	dx, dy := bearingToScreen(bearingDeg)
	pts := []geo.LatLon{
		sunproject(ax, ay, worldPx),
		sunproject(ax+dx*lenPx, ay+dy*lenPx, worldPx),
	}
	return append(out, sectorStroke{points: pts, colorToken: "CHBLK", widthPx: 1, dashed: true})
}

func emitArc(out []sectorStroke, ax, ay, worldPx, radiusMM float64, color string, a1, a2, pxPerMM float64) []sectorStroke {
	radius := radiusMM * pxPerMM
	sweep := a2 - a1
	if radius <= 0 || sweep <= 0 {
		return out
	}
	n := int(math.Ceil(sweep / 3.0))
	if n < 8 {
		n = 8
	}
	pts := make([]geo.LatLon, n+1)
	for i := range pts {
		brg := a1 + sweep*float64(i)/float64(n)
		dx, dy := bearingToScreen(brg)
		pts[i] = sunproject(ax+dx*radius, ay+dy*radius, worldPx)
	}
	// OUTLW underlay (4 px) beneath, then the coloured arc (2 px) on top.
	pts2 := make([]geo.LatLon, len(pts))
	copy(pts2, pts)
	out = append(out, sectorStroke{points: pts, colorToken: "OUTLW", widthPx: 4, dashed: false})
	out = append(out, sectorStroke{points: pts2, colorToken: color, widthPx: 2, dashed: false})
	return out
}

// anyCoarserOverlaps reports whether a strictly-coarser-band eligible primitive's
// world bbox overlaps r (AABB only). Gates down-fill suppression.
func (b *Baker) anyCoarserOverlaps(eligible []int, r *routed) bool {
	for _, qi := range eligible {
		q := &b.prims[qi]
		if q.natMin >= r.natMin {
			continue // not coarser than r
		}
		if q.wMinX <= r.wMaxX && q.wMaxX >= r.wMinX && q.wMinY <= r.wMaxY && q.wMaxY >= r.wMinY {
			return true
		}
	}
	return false
}

// -- helpers -----------------------------------------------------------------

func normX(lon float64) float64 { return (lon + 180.0) / 360.0 }

func normY(lat float64) float64 {
	sin := math.Sin(lat * math.Pi / 180.0)
	return 0.5 - math.Log((1.0+sin)/(1.0-sin))/(4.0*math.Pi)
}

func unnormY(y float64) float64 {
	// Inverse of normY: solve for latitude (Web-Mercator).
	return math.Atan(math.Sinh((0.5-y)*2.0*math.Pi)) * 180.0 / math.Pi
}

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

func intAttr(attrs map[string]interface{}, key string) uint32 {
	v, ok := attrs[key]
	if !ok {
		return 0
	}
	switch t := v.(type) {
	case int:
		if t > 0 {
			return uint32(t)
		}
	case int64:
		if t > 0 {
			return uint32(t)
		}
	case float64:
		if t > 0 {
			return uint32(t)
		}
	case string:
		if n, err := strconv.ParseFloat(strings.TrimSpace(t), 64); err == nil && n > 0 {
			return uint32(n)
		}
	}
	return 0
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

func isSoundingName(name string) bool {
	return strings.HasPrefix(name, "SOUNDG") || strings.HasPrefix(name, "SOUNDS")
}

// soundingVariant forces every SOUND? glyph token in a comma-joined sounding name
// list to the given palette letter (S = bold/shallow, G = faint/deep), so the
// client runs SNDFRM04's safety-depth split live. Matches soundingVariant in
// bake.zig.
func soundingVariant(names string, letter byte) string {
	parts := strings.Split(names, ",")
	for i, p := range parts {
		if len(p) >= 6 && strings.HasPrefix(p, "SOUND") {
			parts[i] = p[:5] + string(letter) + p[6:]
		}
	}
	return strings.Join(parts, ",")
}

func isNaN32(f float32) bool { return f != f }
