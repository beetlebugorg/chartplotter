// Package bake routes the cached lat/lon Primitive IR into MVT layers and tiles
// many cells together. Each cell is assigned a zoom band from its compilation
// scale so a coarse overview cell bakes at low zooms and a harbour cell at high
// zooms. Colour stays an S-52 token string — the client restyles Day/Dusk/Night
// for free.
//
// This is the single-archive (provisioned) "spec display" bake: a feature's
// display z-min comes from SCAMIN (S-52 §10.4.2,
// defaulting to z0 so coverage never goes blank on zoom-out), and where cells of
// different scales overlap, emitTile applies best-available-data suppression both
// ways (below the native bands only the coarsest cell's blanket shows; above them
// only the finest cell's chart). A normalized-world bbox reject prunes far
// primitives before projection.
//
// Covers: SCAMIN z-min, native bands + best-available suppression,
// sounding-number grouping, OBSTRN/WRECKS danger-depth carriage, and per-zoom
// sector-light tessellation into the lines layer.
package bake

import (
	"math"
	"strconv"
	"strings"

	"github.com/beetlebugorg/chartplotter/internal/engine/mvt"
	"github.com/beetlebugorg/chartplotter/internal/engine/pmtiles"
	"github.com/beetlebugorg/chartplotter/internal/engine/portrayal"
	"github.com/beetlebugorg/chartplotter/internal/engine/tile"
	"github.com/beetlebugorg/chartplotter/pkg/geo"
	"github.com/beetlebugorg/chartplotter/pkg/s52"
	"github.com/beetlebugorg/chartplotter/pkg/s57"
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

// scaminZoom maps an S-52 SCAMIN (1:N denominator) to the Web-Mercator zoom
// where the object first becomes visible: the zoom whose DISPLAY-scale
// denominator (at the cell's latitude) is nearest SCAMIN. S-52 §8.4 gates on the
// display scale, so we use the cell's latitude (not the equator) and round to
// the nearest integer zoom — otherwise (equator + round-up) features pop in
// ~1 zoom late at mid/high latitudes (e.g. a 1:22k harbour
// sounding wouldn't show until 1:13k instead of ~1:26k). Clamped to the zoom span.
func scaminZoom(scamin uint32, lat float64) uint32 {
	if scamin == 0 {
		return 0
	}
	denomZ0 := 559_082_264.029 * math.Cos(lat*math.Pi/180) // 1:N at z0 at this latitude
	s := float64(scamin)
	if denomZ0 <= s {
		return 0
	}
	z := math.Round(math.Log2(denomZ0 / s))
	if z >= float64(maxBandZ) {
		return maxBandZ
	}
	if z < 0 {
		return 0
	}
	return uint32(z)
}

// specZMin is the single-archive (provisioned/uploaded) display z-min: per S-52
// §10.4.2 a DISPLAYBASE object is always shown and an object without SCAMIN has
// no minimum display scale, so both float to z0; SCAMIN raises it. Coarse-tile
// pile-up is bounded by EmitTile's best-available suppression (a finer cell
// yields only where no coarser cell covers it).
func specZMin(displayCategory int, scamin uint32, lat float64) uint32 {
	if displayCategory == s52.DisplayBase {
		return 0
	}
	if scamin != 0 {
		return scaminZoom(scamin, lat)
	}
	return 0
}

// displayZMin is the per-band display z-min: a feature is
// gated to its own scale band (no down-fill to z0). Used by the per-band
// district archives, NOT the single provisioned archive. Kept for that path.
//
//   - DISPLAYBASE (always drawn once in-band, exempt from SCAMIN): the band min.
//   - SCAMIN (a 1:N denominator): can only RAISE the min (finer zoom), clamped
//     to ≥ the band min.
//   - No SCAMIN: the band min (S-52 §10.3.4 default).
func displayZMin(displayCategory int, scamin, bandMin uint32) uint32 {
	if displayCategory == s52.DisplayBase {
		return bandMin
	}
	if scamin != 0 {
		if z := BandForScale(scamin).ZoomRange().Min; z > bandMin {
			return z
		}
	}
	return bandMin
}

// routed is one primitive ready to tile: target layer, geometry (lat/lon), the
// normalized-world bbox (for the spatial reject + overlap test), the display and
// native zoom spans, and the pre-built MVT attributes (minus geometry).
type routed struct {
	layer string
	kind  mvt.GeomType
	// Geometry pre-projected to normalized-world coordinates (X,Y in [0,1],
	// Web-Mercator) ONCE at add time, so per-tile emit is a cheap affine
	// transform (Projector.ProjectNorm) instead of recomputing log/sin/tan for
	// every tile a primitive appears in.
	nrings [][]tile.FPoint // polygon
	nline  []tile.FPoint   // linestring
	npoint tile.FPoint     // point

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
	cell     string
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
	curCell string // dataset name of the cell currently being added (stamped on each feature)
}

// New returns an empty Baker.
func New() *Baker { return &Baker{bbox: geo.EmptyBox()} }

// Bounds is the union lat/lon bbox of every ingested cell's primitives.
func (b *Baker) Bounds() geo.BoundingBox { return b.bbox }

// AddCell expands every feature of a parsed cell into routed primitives at the
// cell's scale band, with per-feature SCAMIN display z-min.
func (b *Baker) AddCell(chart *s57.Chart, lib *s52.Library, mariner *s52.MarinerSettings) {
	// Cell name (sans the .000/.NNN extension) stamped on every feature for the
	// inspector's source-cell pill.
	b.curCell = chart.DatasetName()
	if i := strings.LastIndexByte(b.curCell, '.'); i > 0 {
		b.curCell = b.curCell[:i]
	}
	band := BandForScale(uint32(chart.CompilationScale()))
	zr := band.ZoomRange()
	cb := chart.Bounds()
	cellLat := (cb.MinLat + cb.MaxLat) / 2 // SCAMIN→zoom uses the cell's display scale
	features := chart.Features()
	for i := range features {
		f := &features[i]
		// Boundary symbolization (S-52 §8.6.1): a style-variant area is built
		// twice (plain bnd=0 / symbolized bnd=1) so the client toggles boundary
		// style live; everything else is one pass tagged bnd=2.
		for _, pass := range portrayal.BuildFeaturePasses(lib, mariner, f) {
			fb := pass.Build
			bnd := int64(pass.Bnd)
			scamin := intAttr(f.Attributes(), "SCAMIN")
			zMin := specZMin(fb.DisplayCategory, scamin, cellLat)
			class := f.ObjectClass()
			drval1, drval2 := depthVals(f.Attributes(), class)
			prims := fb.Primitives
			for pi := 0; pi < len(prims); pi++ {
				// A sounding number (SOUNDG03) emits one digit glyph per column, all at
				// the same anchor (the glyph art carries the column shift). Group a
				// number's consecutive same-anchor digit glyphs into ONE soundings
				// feature so the client renders the whole number and declutter treats
				// it as a single unit.
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
					b.routeSoundingGroup(names, sc, class, fb.DisplayPriority, fb.DisplayCategory, band, zr, zMin, bnd)
					continue
				}
				b.route(prims[pi], class, fb.DisplayPriority, fb.DisplayCategory, band, zr, zMin, bnd, drval1, drval2)
			}
		}
	}
}

// routeSoundingGroup emits one soundings feature for a whole sounding number
// (the comma-joined digit-glyph list), carrying depth + both palette variants so
// the client runs SNDFRM04's safety-depth split live.
func (b *Baker) routeSoundingGroup(names []string, sc portrayal.SymbolCall, class string, drawPrio, cat int, band Band, zr ZoomRange, zMin uint32, bnd int64) {
	joined := strings.Join(names, ",")
	r := routed{layer: "soundings", kind: mvt.GeomPoint, npoint: normPt(sc.Anchor), zMin: zMin, zMax: zr.Max, natMin: zr.Min, natMax: zr.Max}
	attrs := []mvt.KeyValue{
		{Key: "class", Value: mvt.StringVal(class)},
		{Key: "cell", Value: mvt.StringVal(b.curCell)},
		{Key: "draw_prio", Value: mvt.IntVal(int64(drawPrio))},
		{Key: "cat", Value: mvt.IntVal(catRank(cat))},
		{Key: "bnd", Value: mvt.IntVal(bnd)},
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

// catRank maps the S-52 display category (DisplayBase/Standard/Other = 6/7/8)
// to the client's category-filter rank (0/1/2). The frontend's categoryFilter
// tests `cat ∈ {0,1,2}`, so the raw enum values would filter every feature out.
func catRank(displayCategory int) int64 {
	switch displayCategory {
	case s52.DisplayBase:
		return 0
	case s52.DisplayStandard:
		return 1
	default: // DisplayOther
		return 2
	}
}

// bndAlwaysShown is the S-52 boundary-symbolization tag the client's
// boundaryFilter always passes (2 = style-independent), used for geometry that
// isn't a style-variant area boundary (sector lights here; most features via the
// single bnd=2 pass). Style-variant areas instead get the plain (0) / symbolized
// (1) split from portrayal.BuildFeaturePasses, so the boundary-style toggle works.
const bndAlwaysShown int64 = 2

func (b *Baker) add(r routed, bb geo.BoundingBox) {
	b.bbox.ExtendBox(bb)
	r.wMinX = normX(bb.MinLon)
	r.wMaxX = normX(bb.MaxLon)
	r.wMinY = normY(bb.MaxLat) // north -> smaller y
	r.wMaxY = normY(bb.MinLat) // south -> larger y
	b.prims = append(b.prims, r)
}

func (b *Baker) route(p portrayal.Primitive, class string, drawPrio, cat int, band Band, zr ZoomRange, zMin uint32, bnd int64, drval1, drval2 float32) {
	common := func(extra ...mvt.KeyValue) []mvt.KeyValue {
		base := []mvt.KeyValue{
			{Key: "class", Value: mvt.StringVal(class)},
			{Key: "cell", Value: mvt.StringVal(b.curCell)},
			{Key: "draw_prio", Value: mvt.IntVal(int64(drawPrio))},
			{Key: "cat", Value: mvt.IntVal(catRank(cat))},
			{Key: "bnd", Value: mvt.IntVal(bnd)},
		}
		return append(base, extra...)
	}
	r := routed{zMin: zMin, zMax: zr.Max, natMin: zr.Min, natMax: zr.Max}

	switch v := p.(type) {
	case portrayal.FillPolygon:
		r.layer, r.kind, r.nrings = "areas", mvt.GeomPolygon, normRings(v.Rings)
		extra := []mvt.KeyValue{{Key: "color_token", Value: mvt.StringVal(v.ColorToken)}}
		// Depth areas (DEPARE/DRGARE) carry DRVAL1/DRVAL2 so the client runs
		// SEABED01 shading + the safety-contour line + shallow pattern LIVE
		// against the mariner's contours (no re-bake). Other areas don't.
		if !isNaN32(drval1) {
			extra = append(extra,
				mvt.KeyValue{Key: "drval1", Value: mvt.FloatVal(drval1)},
				mvt.KeyValue{Key: "drval2", Value: mvt.FloatVal(drval2)})
		}
		r.attrs = common(extra...)
		b.add(r, ringsBbox(v.Rings))
	case portrayal.PatternFill:
		r.layer, r.kind, r.nrings = "area_patterns", mvt.GeomPolygon, normRings(v.Rings)
		r.attrs = common(mvt.KeyValue{Key: "pattern_name", Value: mvt.StringVal(v.PatternName)})
		b.add(r, ringsBbox(v.Rings))
	case portrayal.StrokeLine:
		r.layer, r.kind, r.nline = "lines", mvt.GeomLineString, normPts(v.Points)
		r.attrs = common(
			mvt.KeyValue{Key: "color_token", Value: mvt.StringVal(v.ColorToken)},
			mvt.KeyValue{Key: "width_px", Value: mvt.IntVal(int64(v.WidthPx + 0.5))},
			mvt.KeyValue{Key: "dash", Value: mvt.StringVal(dashName(v.Dash))},
		)
		b.add(r, ptsBbox(v.Points))
	case portrayal.LinePattern:
		r.layer, r.kind, r.nline = "complex_lines", mvt.GeomLineString, normPts(v.Points)
		extra := []mvt.KeyValue{{Key: "linestyle_name", Value: mvt.StringVal(v.LinestyleName)}}
		if v.ColorToken != "" {
			extra = append(extra, mvt.KeyValue{Key: "color_token", Value: mvt.StringVal(v.ColorToken)})
		}
		r.attrs = common(extra...)
		b.add(r, ptsBbox(v.Points))
	case portrayal.DrawText:
		r.layer, r.kind, r.npoint = "text", mvt.GeomPoint, normPt(v.Anchor)
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
			anchor: v.Anchor, params: v.Sector, class: class, cell: b.curCell,
			drawPrio: drawPrio, cat: cat, band: band, zMin: zMin, natMax: zr.Max,
		})
	}
}

// routeSymbol routes a non-sounding SY symbol to point_symbols. Sounding digits
// are grouped in AddCell, so they never reach here.
func (b *Baker) routeSymbol(v portrayal.SymbolCall, common func(...mvt.KeyValue) []mvt.KeyValue, r routed) {
	r.kind, r.npoint = mvt.GeomPoint, normPt(v.Anchor)
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
	// A sector light's screen-px figure (the 26 mm ring/legs) spills well beyond
	// its anchor tile — up to ~0.3 tile at any zoom. Enumerate every tile that
	// spill touches (per zoom, since the figure is a fixed fraction of a tile)
	// so a neighbour tile with no other primitives is still emitted; otherwise
	// the arc is clipped dead at the tile boundary.
	for i := range b.sectors {
		sp := &b.sectors[i]
		ax, ay := normX(sp.anchor.Lon), normY(sp.anchor.Lat)
		for z := sp.zMin; z <= sp.natMax; z++ {
			r := sectorRadiusNorm(z)
			bb := geo.BoundingBox{
				MinLat: unnormY(ay + r), MinLon: (ax-r)*360 - 180,
				MaxLat: unnormY(ay - r), MaxLon: (ax+r)*360 - 180,
			}
			out = addRange(out, seen, bb, z, z, extent)
		}
	}
	return out
}

// sectorRadiusNorm is the LIGHTS06 sector figure's maximum extent (the 26 mm
// ring) in normalized-world units at zoom z. The geometry is laid out in a
// 256-px-per-tile space (see expandSector's worldPx), so the spill is a fixed
// fraction of a tile at every zoom: 26 mm × px/mm ÷ 256 ÷ 2^z.
func sectorRadiusNorm(z uint32) float64 {
	return 26.0 * float64(portrayal.DefaultPxPerSymbolUnit) * 100.0 / 256.0 / math.Pow(2, float64(z))
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
	var ts TileScratch
	for _, c := range b.TileCoords(extent) {
		if data := b.EmitTileInto(c, extent, buffer, &ts); data != nil {
			pb.AddTile(uint8(c.Z), c.X, c.Y, data)
		}
	}
	// Override the tile-derived bounds (a z0 world tile would make them global)
	// with the real cell-union extent so clients frame to the charts.
	if bb := b.bbox; bb.MinLon <= bb.MaxLon && bb.MinLat <= bb.MaxLat {
		pb.SetBounds(bb.MinLon, bb.MinLat, bb.MaxLon, bb.MaxLat)
	}
	return pb
}

// TileScratch holds the per-tile working buffers EmitTile would otherwise
// allocate fresh every tile — the clipper's ping-pong arrays, the ring
// projection scratch, and the candidate index list. Reuse ONE per goroutine
// across many EmitTileInto calls so the buffers grow once and amortise; this is
// the dominant bake allocation otherwise (the clipper alone is ~⅓ of it).
type TileScratch struct {
	clip     tile.Clipper
	proj     []tile.FPoint
	eligible []int
}

// EmitTile bakes one tile with a throwaway scratch — convenience for the serial
// path / tests. Hot parallel callers should reuse a TileScratch via EmitTileInto.
func (b *Baker) EmitTile(coord tile.TileCoord, extent uint32, buffer float64) []byte {
	return b.EmitTileInto(coord, extent, buffer, &TileScratch{})
}

// EmitTileInto bakes the merged MVT for one tile, or nil if it has no features,
// reusing ts's buffers. band_z is coord.Z (the per-primitive in-band test zoom).
func (b *Baker) EmitTileInto(coord tile.TileCoord, extent uint32, buffer float64, ts *TileScratch) []byte {
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

	eligible := ts.eligible[:0]
	var finestNat uint32
	minNatMin := uint32(math.MaxUint32)
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
		if r.natMin < minNatMin {
			minNatMin = r.natMin
		}
	}

	ts.eligible = eligible // persist the (possibly grown) backing array for reuse
	scratch := ts.proj     // reused per-ring projection buffer (across tiles)
	clip := &ts.clip       // reused clipper (across tiles)
	for _, i := range eligible {
		r := &b.prims[i]
		// Best-available suppression: below its native band, yield only where no
		// coarser cell covers; above its native band, only the finest shows. A
		// prim already at the coarsest native band on this tile can't be
		// suppressed (nothing is coarser), so skip the O(eligible) overlap scan —
		// for a single-band/single-cell bake this elides it entirely.
		if bandZ < r.natMin && r.natMin > minNatMin && b.anyCoarserOverlaps(eligible, r) {
			continue
		}
		if bandZ > r.natMax && r.natMax < finestNat {
			continue
		}
		switch r.kind {
		case mvt.GeomPolygon:
			var outRings [][]mvt.IPoint
			for _, ring := range r.nrings {
				scratch = projectNormRing(ring, proj, scratch)
				clipped := clip.Polygon(scratch, rect)
				if len(clipped) < 3 {
					continue
				}
				outRings = append(outRings, quantizeRing(clipped))
			}
			if len(outRings) > 0 {
				tb.Layer(r.layer).AddPolygon(outRings, r.attrs)
			}
		case mvt.GeomLineString:
			scratch = projectNormRing(r.nline, proj, scratch)
			runs := tile.ClipLine(scratch, rect)
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
			p := proj.ProjectNorm(r.npoint)
			if p.X < 0 || p.X >= e || p.Y < 0 || p.Y >= e {
				continue
			}
			tb.Layer(r.layer).AddPoints([]mvt.IPoint{tile.Quantize(p)}, r.attrs)
		}
	}
	ts.proj = scratch // persist the (possibly grown) projection buffer for reuse

	// Sector lights: tessellate per zoom into the lines layer. Their screen-px
	// geometry can spill into neighbouring tiles, so reject with a margin sized to
	// the largest radius (the ring's 26 mm) plus the clip buffer. The figure is
	// laid out in 256-px-per-tile space, so the radius term divides by 256 (NOT
	// the MVT extent) — matching sectorRadiusNorm and TileCoords' enumeration.
	margin := sectorRadiusNorm(coord.Z) + (buffer/float64(extent))/n
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
				{Key: "cell", Value: mvt.StringVal(sp.cell)},
				{Key: "color_token", Value: mvt.StringVal(st.colorToken)},
				{Key: "width_px", Value: mvt.IntVal(int64(st.widthPx + 0.5))},
				{Key: "dash", Value: mvt.StringVal(dash)},
				{Key: "cat", Value: mvt.IntVal(catRank(sp.cat))},
				{Key: "bnd", Value: mvt.IntVal(bndAlwaysShown)},
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
// sized for integer zoom z (screen-px radii). A ring is one OUTLW-backed
// coloured circle (26 mm); a sector is two dashed CHBLK
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

// normPt / normPts / normRings pre-project geometry to normalized-world
// coordinates ([0,1] Web-Mercator) once, so per-tile emit is a cheap affine
// transform (see Projector.ProjectNorm).
func normPt(ll geo.LatLon) tile.FPoint {
	return tile.FPoint{X: normX(ll.Lon), Y: normY(ll.Lat)}
}

func normPts(pts []geo.LatLon) []tile.FPoint {
	out := make([]tile.FPoint, len(pts))
	for i, p := range pts {
		out[i] = normPt(p)
	}
	return out
}

func normRings(rings [][]geo.LatLon) [][]tile.FPoint {
	out := make([][]tile.FPoint, len(rings))
	for i, r := range rings {
		out[i] = normPts(r)
	}
	return out
}

// projectNormRing affine-projects a normalized-world ring into tile-pixel space,
// writing into scratch (grown as needed) to avoid per-call allocation. The
// returned slice aliases scratch and is valid until the next call.
func projectNormRing(npts []tile.FPoint, proj tile.Projector, scratch []tile.FPoint) []tile.FPoint {
	if cap(scratch) < len(npts) {
		scratch = make([]tile.FPoint, len(npts))
	}
	scratch = scratch[:len(npts)]
	for i, n := range npts {
		scratch[i] = proj.ProjectNorm(n)
	}
	return scratch
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

// depthVals returns a depth area's DRVAL1/DRVAL2 (metres) for the client's live
// SEABED01 shading, or (NaN, NaN) for non-depth areas (so route() omits them).
// DRVAL2 falls back to DRVAL1 when absent. Mirrors the is_depth gate.
func depthVals(attrs map[string]interface{}, class string) (float32, float32) {
	if class != "DEPARE" && class != "DRGARE" {
		return nan32f, nan32f
	}
	d1, ok := floatAttr(attrs, "DRVAL1")
	if !ok {
		return nan32f, nan32f
	}
	d2, ok := floatAttr(attrs, "DRVAL2")
	if !ok {
		d2 = d1
	}
	return float32(d1), float32(d2)
}

var nan32f = float32(math.NaN())

// floatAttr reads a numeric S-57 attribute (int/float/string) as a float64.
func floatAttr(attrs map[string]interface{}, key string) (float64, bool) {
	v, ok := attrs[key]
	if !ok {
		return 0, false
	}
	switch t := v.(type) {
	case float64:
		return t, true
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case string:
		if n, err := strconv.ParseFloat(strings.TrimSpace(t), 64); err == nil {
			return n, true
		}
	}
	return 0, false
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
// client runs SNDFRM04's safety-depth split live.
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
