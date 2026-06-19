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
	"fmt"
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

// generalOverzoomMin is the lowest zoom the general band displays at (below its
// native min of 7) so general charts don't vanish when zoomed out — all the way
// to the world view. Where an overview cell overlaps, best-available suppression
// defers to it; general only fills the gap. SCAMIN keeps minor features gated, so
// a coarse-zoom tile carries only the skeleton (land/coast/major depth).
const generalOverzoomMin uint32 = 0

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

// ZoomRange returns the band's native [minzoom, maxzoom] — the scale range the
// band's cells are compiled for. Adjacent bands overlap by one zoom at the
// endpoints. Used for SCAMIN/CSCL context and the frontend's overzoom envelope.
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

// bandZMin is the spec-resolution display z-min for the merged provisioned
// archive: a feature is gated to its native scale band rather than floated to z0.
// This replaces the old float-to-z0 "spec display" z-min, which made every cell —
// a harbor cell included — eligible at every coarse zoom, piling most of all the
// cells' prims onto each coarse tile (the source of the import halt + memory
// blow-up). With band-gating, a coarse tile only sees the few coarse-band cells;
// best-available suppression and the frontend fan still compose bands across the
// band-overlap zooms exactly as before, so tiles stay complete.
//
// Note this is identical to the old z-min for any feature WITH SCAMIN (both use
// scaminZoom) — e.g. soundings. It changes only features WITHOUT SCAMIN (and
// DISPLAYBASE), which previously floated to z0 and now sit at their band min.
// Per S-52 the visible result is unchanged wherever coverage is complete: a coarse
// zoom shows the scale-appropriate (coarser) cell anyway.
//
//   - DISPLAYBASE: the band min (always shown in-band; SCAMIN never removes base).
//   - SCAMIN present: max(bandMin, scaminZoom) — SCAMIN can only RAISE the min.
//   - no SCAMIN: the band min (S-52 §10.3.4 default).
func bandZMin(displayCategory int, scamin, bandMin uint32, lat float64) uint32 {
	if displayCategory == s52.DisplayBase {
		return bandMin
	}
	if scamin != 0 {
		if z := scaminZoom(scamin, lat); z > bandMin {
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

	// ls (non-nil only for a complex_lines prim) is the linestyle's period
	// geometry; when set the prim is tessellated per zoom at emit (emitComplexLine)
	// instead of drawn as a plain polyline.
	ls *lsInfo

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
	// legNorm is the full-length leg reach (VALNMR nominal range) as a fraction
	// of the normalized world — a fixed GROUND distance, so zoom-independent
	// (unlike the 25 mm short legs / ring, which are screen-px). Drives the tile
	// enumeration + emit margin so the long legs aren't culled near tile edges.
	// 0 when the light has no VALNMR (only the screen-px figure spills).
	legNorm float64
}

// Baker accumulates routed primitives from many cells, then tiles them.
type Baker struct {
	prims   []routed
	sectors []sectorPrim
	// emitIndex is an inverted tile→prim-index map (packed z<<40|x<<20|y → prim
	// indices) built once by BuildEmitIndex before the (possibly parallel) emit
	// loop. When present, EmitTileInto iterates only the prims that touch a tile
	// instead of scanning all of b.prims — turning the whole bake from
	// O(#tiles × #prims) into O(Σ prims-on-tile). nil ⇒ EmitTileInto falls back to
	// the full scan (the EmitTile convenience path / tests). Read-only after build.
	emitIndex  map[uint64][]int32
	linestyles map[string]*lsInfo // complex-linestyle period geometry, built once (lazily) from the PresLib
	bbox       geo.BoundingBox
	curCell    string // dataset name of the cell currently being added (stamped on each feature)
	curObjnam string // OBJNAM of the feature currently being expanded (for the inspector)
	curLight  string // light characteristic string of the current LIGHTS feature (e.g. "Fl.R.4s")
	// Co-located-light combination (S-52 LIGHTS06): when several LIGHTS share a
	// position, the first is "primary" (one flare + a merged multi-line label);
	// the rest are suppressed (flare + text dropped, sectors kept). seenSector
	// dedupes identical sector geometry within the current cell.
	curLightSkip bool                  // current LIGHTS is a non-primary co-located light
	curLightText string                // merged multi-line characteristic for the primary
	seenSector   map[sectorKey]struct{} // sector dedup for the current cell
	coverage     []CellCoverage         // M_COVR data-coverage polygons of added cells (debug)
}

// CellCoverage is one M_COVR (CATCOV=1) data-coverage polygon of an added cell,
// in lon/lat — the area the cell ACTUALLY carries data for (vs its rectangular
// bounding box). Drives the debug overlay's coverage-vs-gap diagnosis.
type CellCoverage struct {
	Cell  string        // cell name
	Rings [][][]float64 // GeoJSON Polygon rings: [ring][point][lon,lat]
}

// Coverage returns the M_COVR data-coverage polygons of every cell added so far.
func (b *Baker) Coverage() []CellCoverage { return b.coverage }

// sectorKey identifies a sector light's geometry (anchor + params) for dedup.
type sectorKey struct {
	lat, lon, s1, s2, r int64
	col                 string
}

func quantDeg(f float64) int64 { return int64(math.Round(f * 1e6)) }

// New returns an empty Baker.
func New() *Baker { return &Baker{bbox: geo.EmptyBox()} }

// Bounds is the union lat/lon bbox of every ingested cell's primitives.
func (b *Baker) Bounds() geo.BoundingBox { return b.bbox }

// groupCoLocatedLights finds LIGHTS features that share an exact position and,
// for each such group, returns the merged multi-line characteristic keyed by the
// first (primary) feature index, plus the set of non-primary indices to suppress.
// S-52 PresLib §LIGHTS06: co-located lights combine into one flare + one stacked
// characteristic label rather than drawing N flares/labels on top of each other.
func groupCoLocatedLights(features []s57.Feature) (primaryText map[int]string, skip map[int]bool) {
	type pos struct{ lat, lon float64 }
	groups := map[pos][]int{}
	for i := range features {
		f := &features[i]
		if f.ObjectClass() != "LIGHTS" {
			continue
		}
		g := f.Geometry()
		if g.Type != s57.GeometryTypePoint || len(g.Coordinates) == 0 || len(g.Coordinates[0]) < 2 {
			continue
		}
		c := g.Coordinates[0]
		k := pos{lat: c[1], lon: c[0]}
		groups[k] = append(groups[k], i)
	}
	for _, idxs := range groups {
		if len(idxs) < 2 {
			continue
		}
		var lines []string
		seen := map[string]bool{}
		for _, i := range idxs {
			ch := s52.BuildLightCharacteristic(features[i].Attributes())
			if ch == "" || seen[ch] {
				continue
			}
			seen[ch] = true
			lines = append(lines, ch)
		}
		if primaryText == nil {
			primaryText, skip = map[int]string{}, map[int]bool{}
		}
		primaryText[idxs[0]] = strings.Join(lines, "\n")
		for _, i := range idxs[1:] {
			skip[i] = true
		}
	}
	return primaryText, skip
}

// AddCell expands every feature of a parsed cell into routed primitives at the
// cell's scale band, with per-feature SCAMIN display z-min.
func (b *Baker) AddCell(chart *s57.Chart, lib *s52.Library, mariner *s52.MarinerSettings) {
	// Cell name (sans the .000/.NNN extension) stamped on every feature for the
	// inspector's source-cell pill.
	b.curCell = chart.DatasetName()
	if i := strings.LastIndexByte(b.curCell, '.'); i > 0 {
		b.curCell = b.curCell[:i]
	}
	if b.linestyles == nil {
		b.linestyles = buildLinestyleTable(lib)
	}
	band := BandForScale(uint32(chart.CompilationScale()))
	zr := band.ZoomRange()
	// Display range vs native band. General cells overzoom OUT (down to z2) so
	// their data doesn't vanish when you zoom out past z7 with no overview
	// coverage. The native band [zr] still drives best-available suppression, so
	// general yields wherever a coarser (overview) cell actually overlaps — it
	// only fills the gaps. (We don't raise the display ceiling: indexing a
	// cell-wide prim up to high zoom would blow up the emit index.)
	dr := zr
	if band == BandGeneral {
		dr.Min = generalOverzoomMin
	}
	cb := chart.Bounds()
	cellLat := (cb.MinLat + cb.MaxLat) / 2 // SCAMIN→zoom uses the cell's display scale
	// Per-cell depth-area index, so the danger CSPs (UDWHAZ05/DEPVAL02) can test
	// the water depth underlying a hazard. Built once per cell.
	depthIdx := buildDepthIndex(chart)
	features := chart.Features()
	// Combine co-located lights (S-52 LIGHTS06): one flare + one merged label.
	lightPrimary, lightSkip := groupCoLocatedLights(features)
	b.seenSector = make(map[sectorKey]struct{})
	for i := range features {
		f := &features[i]
		// Per-feature inspector data: the object name, and (for lights) the S-52
		// light characteristic string ("Fl.R.4s") so the inspector can show the
		// light data, not just the symbol.
		b.curObjnam = stringAttr(f.Attributes(), "OBJNAM")
		b.curLight = ""
		b.curLightSkip = false
		b.curLightText = ""
		// Capture the cell's data-coverage polygon (M_COVR, CATCOV=1) for the debug
		// overlay — the real area the cell carries data for, not its bounding box.
		if f.ObjectClass() == "M_COVR" && intAttr(f.Attributes(), "CATCOV") == 1 {
			if rings := f.Geometry().Rings; len(rings) > 0 {
				cov := CellCoverage{Cell: b.curCell}
				for _, r := range rings {
					cov.Rings = append(cov.Rings, r.Coordinates)
				}
				b.coverage = append(b.coverage, cov)
			}
		}
		if f.ObjectClass() == "LIGHTS" {
			b.curLight = s52.BuildLightCharacteristic(f.Attributes())
			b.curLightSkip = lightSkip[i]
			if merged, ok := lightPrimary[i]; ok {
				b.curLightText = merged
				b.curLight = merged // inspector shows the combined characteristic
			}
		}
		// Boundary symbolization (S-52 §8.6.1): a style-variant area is built
		// twice (plain bnd=0 / symbolized bnd=1) so the client toggles boundary
		// style live; everything else is one pass tagged bnd=2.
		for _, pass := range portrayal.BuildFeaturePasses(lib, mariner, f, depthIdx.spatialFor(f)) {
			fb := pass.Build
			bnd := int64(pass.Bnd)
			pts := int64(pass.Pts)
			scamin := intAttr(f.Attributes(), "SCAMIN")
			zMin := bandZMin(fb.DisplayCategory, scamin, dr.Min, cellLat)
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
					b.routeSoundingGroup(names, sc, class, fb.DisplayPriority, fb.DisplayCategory, band, zr, zMin, dr.Max, bnd, pts)
					continue
				}
				// Co-located lights (S-52 LIGHTS06): a non-primary light drops its
				// flare + characteristic text (sectors still emit, deduped); the
				// primary's text becomes the merged multi-line characteristic.
				p := prims[pi]
				if class == "LIGHTS" {
					switch v := p.(type) {
					case portrayal.SymbolCall:
						if b.curLightSkip {
							continue
						}
					case portrayal.DrawText:
						if b.curLightSkip {
							continue
						}
						if b.curLightText != "" {
							v.Text = b.curLightText
							p = v
						}
					}
				}
				b.route(p, class, fb.DisplayPriority, fb.DisplayCategory, band, zr, zMin, dr.Max, bnd, pts, drval1, drval2)
			}
		}
	}
}

// routeSoundingGroup emits one soundings feature for a whole sounding number
// (the comma-joined digit-glyph list), carrying depth + both palette variants so
// the client runs SNDFRM04's safety-depth split live.
func (b *Baker) routeSoundingGroup(names []string, sc portrayal.SymbolCall, class string, drawPrio, cat int, band Band, zr ZoomRange, zMin, zMax uint32, bnd, pts int64) {
	joined := strings.Join(names, ",")
	r := routed{layer: "soundings", kind: mvt.GeomPoint, npoint: normPt(sc.Anchor), zMin: zMin, zMax: zMax, natMin: zr.Min, natMax: zr.Max}
	attrs := []mvt.KeyValue{
		{Key: "class", Value: mvt.StringVal(class)},
		{Key: "cell", Value: mvt.StringVal(b.curCell)},
		{Key: "draw_prio", Value: mvt.IntVal(int64(drawPrio))},
		{Key: "cat", Value: mvt.IntVal(catRank(cat))},
		{Key: "bnd", Value: mvt.IntVal(bnd)},
		{Key: "symbol_names", Value: mvt.StringVal(joined)},
		{Key: "scale", Value: mvt.FloatVal(sc.Scale)},
	}
	if pts != ptsAlwaysShown {
		attrs = append(attrs, mvt.KeyValue{Key: "pts", Value: mvt.IntVal(pts)})
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

// ptsAlwaysShown is the point-symbol-style tag the client's pointStyleFilter
// always passes (2 = style-independent). Emitted only for the paper/simplified
// variant passes (0/1) of point features that actually differ between the two
// LUP tables; every other feature omits `pts` and the client coalesces to 2.
const ptsAlwaysShown int64 = 2

func (b *Baker) add(r routed, bb geo.BoundingBox) {
	b.bbox.ExtendBox(bb)
	r.wMinX = normX(bb.MinLon)
	r.wMaxX = normX(bb.MaxLon)
	r.wMinY = normY(bb.MaxLat) // north -> smaller y
	r.wMaxY = normY(bb.MinLat) // south -> larger y
	b.prims = append(b.prims, r)
}

func (b *Baker) route(p portrayal.Primitive, class string, drawPrio, cat int, band Band, zr ZoomRange, zMin, zMax uint32, bnd, pts int64, drval1, drval2 float32) {
	common := func(extra ...mvt.KeyValue) []mvt.KeyValue {
		base := []mvt.KeyValue{
			{Key: "class", Value: mvt.StringVal(class)},
			{Key: "cell", Value: mvt.StringVal(b.curCell)},
			{Key: "draw_prio", Value: mvt.IntVal(int64(drawPrio))},
			{Key: "cat", Value: mvt.IntVal(catRank(cat))},
			{Key: "bnd", Value: mvt.IntVal(bnd)},
		}
		// pts is omitted for the common case (2): only paper/simplified variant
		// passes (0/1) carry it, so most features stay lean.
		if pts != ptsAlwaysShown {
			base = append(base, mvt.KeyValue{Key: "pts", Value: mvt.IntVal(pts)})
		}
		// Inspector extras — only when present, to avoid bloating every feature.
		if b.curObjnam != "" {
			base = append(base, mvt.KeyValue{Key: "objnam", Value: mvt.StringVal(b.curObjnam)})
		}
		if b.curLight != "" {
			base = append(base, mvt.KeyValue{Key: "light", Value: mvt.StringVal(b.curLight)})
		}
		return append(base, extra...)
	}
	r := routed{zMin: zMin, zMax: zMax, natMin: zr.Min, natMax: zr.Max}

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
		// Stored as a polyline + its linestyle; emitComplexLine tessellates the
		// period (dashes → these complex_lines segments, symbols → point_symbols)
		// per zoom at emit time. colour_token drives the live restyle of the dashes.
		r.layer, r.kind, r.nline = "complex_lines", mvt.GeomLineString, normPts(v.Points)
		r.ls = b.linestyles[v.LinestyleName]
		ct := v.ColorToken
		if ct == "" && r.ls != nil {
			ct = r.ls.colorToken
		}
		r.attrs = common(
			mvt.KeyValue{Key: "linestyle_name", Value: mvt.StringVal(v.LinestyleName)},
			mvt.KeyValue{Key: "color_token", Value: mvt.StringVal(ct)},
		)
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
		// Dedupe identical sector geometry (co-located lights often repeat sectors).
		k := sectorKey{quantDeg(v.Anchor.Lat), quantDeg(v.Anchor.Lon),
			quantDeg(v.Sector.StartAngleDeg), quantDeg(v.Sector.EndAngleDeg), quantDeg(v.Sector.RadiusNM), v.Sector.ColorToken}
		if b.seenSector != nil {
			if _, dup := b.seenSector[k]; dup {
				return
			}
			b.seenSector[k] = struct{}{}
		}
		b.bbox.ExtendPoint(v.Anchor)
		b.sectors = append(b.sectors, sectorPrim{
			anchor: v.Anchor, params: v.Sector, class: class, cell: b.curCell,
			drawPrio: drawPrio, cat: cat, band: band, zMin: zMin, natMax: zr.Max,
			legNorm: sectorLegFullNorm(v.Anchor.Lat, v.Sector.RadiusNM),
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
			// Full-length legs (sp.legNorm, a fixed ground distance) can reach far
			// past the screen-px figure, so enumerate every tile they cross.
			r := math.Max(sectorRadiusNorm(z), sp.legNorm)
			bb := geo.BoundingBox{
				MinLat: unnormY(ay + r), MinLon: (ax-r)*360 - 180,
				MaxLat: unnormY(ay - r), MaxLon: (ax+r)*360 - 180,
			}
			out = addRange(out, seen, bb, z, z, extent)
		}
	}
	return out
}

// BuildEmitIndex builds the inverted tile→prim index (b.emitIndex) for the given
// extent and clip buffer, so EmitTileInto can iterate only the prims that touch a
// tile rather than scanning every b.prims entry. Call once after all cells are
// added and before the emit loop; the index is read-only thereafter (safe for the
// parallel EmitTileInto workers). The index keys each tile a prim's
// buffer-expanded bbox covers across its display-zoom span, so it is a strict
// superset of EmitTileInto's in-tile reject — the reject still runs and trims the
// boundary-tile over-inclusion, so behaviour is identical to the full scan.
func (b *Baker) BuildEmitIndex(extent uint32, buffer float64) {
	idx := make(map[uint64][]int32, len(b.prims))
	bufFrac := buffer / float64(extent)
	for i := range b.prims {
		r := &b.prims[i]
		for z := r.zMin; z <= r.zMax; z++ {
			n := math.Pow(2, float64(z))
			last := int64(n) - 1
			bufN := bufFrac / n
			// A prim is eligible on tile x iff its bbox intersects the tile window
			// expanded by the render buffer: x ∈ [ceil((wMin-buf)·n)-1, floor((wMax+buf)·n)].
			xMin := clampTile(int64(math.Ceil((r.wMinX-bufN)*n))-1, last)
			xMax := clampTile(int64(math.Floor((r.wMaxX+bufN)*n)), last)
			yMin := clampTile(int64(math.Ceil((r.wMinY-bufN)*n))-1, last)
			yMax := clampTile(int64(math.Floor((r.wMaxY+bufN)*n)), last)
			for x := xMin; x <= xMax; x++ {
				for y := yMin; y <= yMax; y++ {
					key := uint64(z)<<40 | uint64(x)<<20 | uint64(y)
					idx[key] = append(idx[key], int32(i))
				}
			}
		}
	}
	b.emitIndex = idx
}

func clampTile(v, last int64) int64 {
	if v < 0 {
		return 0
	}
	if v > last {
		return last
	}
	return v
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
	b.BuildEmitIndex(extent, buffer)
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
	var gatedZMin int // polys that overlap this tile but are hidden below their display zMin (SCAMIN) — diagnostic
	minNatMin := uint32(math.MaxUint32)
	consider := func(i int) {
		r := &b.prims[i]
		// Spatial reject first so the zMin diagnostic below counts only prims that
		// actually overlap this tile (the full-scan path considers every prim).
		if r.wMaxX < tnx0 || r.wMinX > tnx1 || r.wMaxY < tny0 || r.wMinY > tny1 {
			return
		}
		// Lower gate only: below zMin the feature isn't shown at all. The UPPER end
		// is governed by best-available suppression below (a coarse prim stays
		// visible when zoomed in past its native band — overzoomed — except where a
		// strictly-finer cell actually overlaps it). So once data is on screen,
		// zooming in never drops it unless something better takes its place. (The
		// indexed bake path only lists prims at their native zooms, so it's
		// unaffected; overzoom relies on the full-scan path used by the realtime
		// baker.) r.zMax is retained for the index build's range only.
		if coord.Z < r.zMin {
			if r.kind == mvt.GeomPolygon {
				gatedZMin++
			}
			return
		}
		eligible = append(eligible, i)
		if r.natMax != math.MaxUint32 && r.natMax > finestNat {
			finestNat = r.natMax
		}
		if r.natMin < minNatMin {
			minNatMin = r.natMin
		}
	}
	if b.emitIndex != nil {
		// Indexed path: only the prims whose buffer-expanded bbox covers this tile.
		key := uint64(coord.Z)<<40 | uint64(coord.X)<<20 | uint64(coord.Y)
		for _, ci := range b.emitIndex[key] {
			consider(int(ci))
		}
	} else {
		// Fallback (EmitTile / tests, no prebuilt index): scan all prims.
		for i := range b.prims {
			consider(i)
		}
	}

	ts.eligible = eligible // persist the (possibly grown) backing array for reuse
	scratch := ts.proj     // reused per-ring projection buffer (across tiles)
	clip := &ts.clip       // reused clipper (across tiles)
	var suppDown, suppUp, emptyGeom int    // tile-generation diagnostics (see TileDiag)
	var polyElig, polyEmit int             // polygon prims eligible vs actually emitted (diagnostic)
	for _, i := range eligible {
		r := &b.prims[i]
		// Best-available suppression: below its native band, yield only where no
		// coarser cell covers; above its native band, only the finest shows. A
		// prim already at the coarsest native band on this tile can't be
		// suppressed (nothing is coarser), so skip the O(eligible) overlap scan —
		// for a single-band/single-cell bake this elides it entirely.
		if bandZ < r.natMin && r.natMin > minNatMin && b.anyCoarserOverlaps(eligible, r) {
			suppDown++
			continue
		}
		// Symmetric up-direction gate: a coarse prim shown above its native band is
		// suppressed only where a strictly-finer eligible prim actually *overlaps*
		// it — not merely because some finer cell touches the tile. Without the
		// overlap test a coarse feature (e.g. a light the finer cell doesn't carry)
		// vanishes wherever a finer cell shares its tile. The r.natMax < finestNat
		// short-circuit means a prim already at the finest band on the tile pays no
		// scan, mirroring the down path's minNatMin guard.
		if bandZ > r.natMax && r.natMax < finestNat && b.anyFinerOverlaps(eligible, r) {
			suppUp++
			continue
		}
		switch r.kind {
		case mvt.GeomPolygon:
			polyElig++
			var outRings [][]mvt.IPoint
			for _, ring := range r.nrings {
				scratch = projectNormRing(ring, proj, scratch)
				clipped := clip.Polygon(scratch, rect)
				if len(clipped) < 3 {
					continue
				}
				q := quantizeRing(clipped)
				if len(q) < 3 {
					// DP over-collapsed a small ring — keep it unsimplified rather
					// than drop a still-renderable polygon. Truly degenerate rings
					// (sub-grid) are skipped.
					if q = quantizeRingExact(clipped); len(q) < 3 {
						continue
					}
				}
				outRings = append(outRings, q)
			}
			if len(outRings) > 0 {
				tb.Layer(r.layer).AddPolygon(outRings, r.attrs)
				polyEmit++
			} else {
				emptyGeom++
			}
		case mvt.GeomLineString:
			if r.ls != nil {
				// Complex (symbolised) linestyle: tessellate its period per zoom.
				b.emitComplexLine(r, proj, rect, coord.Z, extent, tb, &scratch)
				continue
			}
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
	// the largest radius (the ring's 26 mm, or the longer full-length leg) plus the
	// clip buffer. The figure is laid out in 256-px-per-tile space, so the radius
	// term divides by 256 (NOT the MVT extent) — matching sectorRadiusNorm and
	// TileCoords' enumeration. The full-leg term is per-sector (its VALNMR reach).
	spill := (buffer / float64(extent)) / n
	for i := range b.sectors {
		sp := &b.sectors[i]
		if coord.Z < sp.zMin || coord.Z > sp.natMax {
			continue
		}
		margin := math.Max(sectorRadiusNorm(coord.Z), sp.legNorm) + spill
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
			attrs := []mvt.KeyValue{
				{Key: "class", Value: mvt.StringVal(sp.class)},
				{Key: "cell", Value: mvt.StringVal(sp.cell)},
				{Key: "color_token", Value: mvt.StringVal(st.colorToken)},
				{Key: "width_px", Value: mvt.IntVal(int64(st.widthPx + 0.5))},
				{Key: "dash", Value: mvt.StringVal(dash)},
				{Key: "cat", Value: mvt.IntVal(catRank(sp.cat))},
				{Key: "bnd", Value: mvt.IntVal(bndAlwaysShown)},
				{Key: "draw_prio", Value: mvt.IntVal(int64(sp.drawPrio))},
			}
			// Leg-length variant tag — only on the short/full legs (0/1); arcs and
			// rings (sleg -1) stay untagged so the client always shows them.
			if st.sleg >= 0 {
				attrs = append(attrs, mvt.KeyValue{Key: "sleg", Value: mvt.IntVal(int64(st.sleg))})
			}
			tb.Layer("lines").AddLines(paths, attrs)
		}
	}

	if TileDiag != nil {
		TileDiag(fmt.Sprintf("tile %d/%d/%d: eligible=%d gatedZMin=%d polyElig=%d polyEmit=%d suppDown=%d suppUp=%d emptyGeom=%d empty=%t",
			coord.Z, coord.X, coord.Y, len(eligible), gatedZMin, polyElig, polyEmit, suppDown, suppUp, emptyGeom, tb.IsEmpty()))
	}
	if tb.IsEmpty() {
		return nil
	}
	return tb.Encode()
}

// DebugTilePolyOverlap counts polygon prims overlapping a tile two ways: by the
// fast cached-bbox reject the bake actually uses (stored), and by their TRUE
// vertex bbox recomputed from the normalized rings (trueOv). A gap (trueOv >
// stored) means a cached prim bbox is wrong and the bake is silently dropping
// polygons that belong in the tile — the one polygon-loss the TileDiag funnel
// can't see (the reject happens before any counter). Diagnostic only.
func (b *Baker) DebugTilePolyOverlap(coord tile.TileCoord, buffer float64, extent uint32) (stored, trueOv int) {
	n := math.Pow(2, float64(coord.Z))
	bufN := (buffer / float64(extent)) / n
	tnx0, tnx1 := float64(coord.X)/n-bufN, float64(coord.X+1)/n+bufN
	tny0, tny1 := float64(coord.Y)/n-bufN, float64(coord.Y+1)/n+bufN
	overlaps := func(minx, miny, maxx, maxy float64) bool {
		return !(maxx < tnx0 || minx > tnx1 || maxy < tny0 || miny > tny1)
	}
	for i := range b.prims {
		r := &b.prims[i]
		if r.kind != mvt.GeomPolygon || coord.Z < r.zMin {
			continue
		}
		if overlaps(r.wMinX, r.wMinY, r.wMaxX, r.wMaxY) {
			stored++
		}
		minx, miny, maxx, maxy := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
		for _, ring := range r.nrings {
			for _, p := range ring {
				minx, maxx = math.Min(minx, p.X), math.Max(maxx, p.X)
				miny, maxy = math.Min(miny, p.Y), math.Max(maxy, p.Y)
			}
		}
		if overlaps(minx, miny, maxx, maxy) {
			trueOv++
		}
	}
	return
}

// TileDiag, when non-nil, receives one line per EmitTile call with the
// per-stage primitive counts (eligible → suppressed → empty-geometry → empty).
// It's the hook for debugging tiles that bake empty: a tile with eligible>0 but
// empty=true lost everything to suppression or clipping. Off (nil) by default —
// the wasm baker turns it on via cpSetTileDiag; tests set it directly.
var TileDiag func(string)

// earthCircumM is the Web-Mercator equatorial circumference (m), used to convert
// a VALNMR nominal range (nautical miles) into a normalized-world fraction.
const earthCircumM = 40075016.686

// sectorLegFullNorm is the full-length sector-leg reach (VALNMR nautical miles) as
// a fraction of the normalized world at the light's latitude — a ground distance,
// so zoom-independent. 0 when no/!positive VALNMR.
func sectorLegFullNorm(lat, radiusNM float64) float64 {
	if radiusNM <= 0 {
		return 0
	}
	cosLat := math.Cos(lat * math.Pi / 180.0)
	if cosLat < 1e-6 {
		return 0
	}
	return radiusNM * 1852.0 / (cosLat * earthCircumM)
}

// sectorStroke is one tessellated piece of sector geometry: a lat/lon polyline
// plus the S-52 pen token + width the lines layer carries. sleg tags leg-length
// variants for the client's full-length-sector toggle: -1 = no tag / always shown
// (arcs, rings), 0 = the 25 mm short leg, 1 = the full VALNMR-length leg.
type sectorStroke struct {
	points     []geo.LatLon
	colorToken string
	widthPx    float32
	dashed     bool
	sleg       int
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
	legShort := 25.0 * pxPerMM
	// Full leg extends to the VALNMR nominal range (S-52 LIGHTS06 note 1). Never
	// shorter than the 25 mm default, so the "full length" toggle only ever grows
	// the leg. Both variants are baked (tagged sleg 0/1); the client shows one.
	legFull := sectorLegFullNorm(anchor.Lat, p.RadiusNM) * worldPx
	if legFull < legShort {
		legFull = legShort
	}
	var out []sectorStroke
	out = emitLeg(out, ax, ay, worldPx, a1, legShort, 0)
	out = emitLeg(out, ax, ay, worldPx, a2, legShort, 0)
	out = emitLeg(out, ax, ay, worldPx, a1, legFull, 1)
	out = emitLeg(out, ax, ay, worldPx, a2, legFull, 1)
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

func emitLeg(out []sectorStroke, ax, ay, worldPx, bearingDeg, lenPx float64, sleg int) []sectorStroke {
	if lenPx <= 0 {
		return out
	}
	dx, dy := bearingToScreen(bearingDeg)
	pts := []geo.LatLon{
		sunproject(ax, ay, worldPx),
		sunproject(ax+dx*lenPx, ay+dy*lenPx, worldPx),
	}
	return append(out, sectorStroke{points: pts, colorToken: "CHBLK", widthPx: 1, dashed: true, sleg: sleg})
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
	// OUTLW underlay (4 px) beneath, then the coloured arc (2 px) on top. Arcs/
	// rings carry no leg tag (sleg -1) — always shown, regardless of the toggle.
	pts2 := make([]geo.LatLon, len(pts))
	copy(pts2, pts)
	out = append(out, sectorStroke{points: pts, colorToken: "OUTLW", widthPx: 4, dashed: false, sleg: -1})
	out = append(out, sectorStroke{points: pts2, colorToken: color, widthPx: 2, dashed: false, sleg: -1})
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

// anyFinerOverlaps reports whether a strictly-finer-band eligible primitive's
// world bbox overlaps r (AABB only). Gates up-direction suppression — the mirror
// of anyCoarserOverlaps. A finer band has the larger native-max zoom.
func (b *Baker) anyFinerOverlaps(eligible []int, r *routed) bool {
	for _, qi := range eligible {
		q := &b.prims[qi]
		if q.natMax <= r.natMax {
			continue // not finer than r
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

// simplifyTolerance is the Douglas-Peucker tolerance for per-tile geometry, in MVT
// extent units. At MVTExtent=4096 over a ~512px tile that's 8 units/screen-px, so
// ~4 units ≈ ½ px — below visible resolution, yet it collapses the dense S-57
// coastlines that otherwise carry 100k+ vertices into one tile.
const simplifyTolerance = 4.0

// quantizeRing simplifies a clipped ring/line to tile resolution, then snaps it to
// the MVT integer grid: first Douglas-Peucker (drops vertices within ½ px of the
// kept line — the only step that meaningfully thins a crenulated coastline), then
// exact consecutive-duplicate and collinear-midpoint removal during quantization.
// Without this a single dense polygon can carry 100k+ vertices into one tile and
// blow MapLibre's 65535-vertex-per-fill-segment cap, so the whole polygon silently
// fails to render. Endpoints are always kept (closed rings keep their seam vertex).
func quantizeRing(pts []tile.FPoint) []mvt.IPoint {
	return quantizePts(douglasPeucker(pts, simplifyTolerance))
}

// quantizeRingExact is quantizeRing without Douglas-Peucker — the fallback for a
// ring that DP would collapse below 3 points. A small polygon is only a few
// vertices, so it can't trip the fill-segment cap; keeping it unsimplified means
// simplification never deletes a whole (still-renderable) polygon.
func quantizeRingExact(pts []tile.FPoint) []mvt.IPoint { return quantizePts(pts) }

// quantizePts snaps to the MVT integer grid, dropping exact consecutive duplicates
// and collinear midpoints as it goes (both lossless at integer resolution).
func quantizePts(pts []tile.FPoint) []mvt.IPoint {
	out := make([]mvt.IPoint, 0, len(pts))
	for _, p := range pts {
		q := tile.Quantize(p)
		if n := len(out); n > 0 && out[n-1] == q {
			continue // exact consecutive duplicate
		}
		if n := len(out); n >= 2 {
			a, b := out[n-2], out[n-1]
			// b is collinear with a→q when the cross product is zero; collapse it
			// (replace the midpoint with q) so straight runs reduce to endpoints.
			if int64(b.X-a.X)*int64(q.Y-a.Y) == int64(b.Y-a.Y)*int64(q.X-a.X) {
				out[n-1] = q
				continue
			}
		}
		out = append(out, q)
	}
	return out
}

// douglasPeucker returns the subset of pts that approximates the polyline within
// eps (perpendicular distance, in the input's units), always keeping the first and
// last vertex. Iterative (explicit stack) to avoid deep recursion on long rings.
func douglasPeucker(pts []tile.FPoint, eps float64) []tile.FPoint {
	n := len(pts)
	if n < 3 {
		return pts
	}
	keep := make([]bool, n)
	keep[0], keep[n-1] = true, true
	eps2 := eps * eps
	stack := [][2]int{{0, n - 1}}
	for len(stack) > 0 {
		top := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		s, e := top[0], top[1]
		if e <= s+1 {
			continue
		}
		ax, ay := pts[s].X, pts[s].Y
		dx, dy := pts[e].X-ax, pts[e].Y-ay
		den := dx*dx + dy*dy
		maxD, maxI := -1.0, -1
		for i := s + 1; i < e; i++ {
			ex, ey := pts[i].X-ax, pts[i].Y-ay
			var d float64
			if den == 0 {
				d = ex*ex + ey*ey // degenerate segment: distance to the point
			} else {
				num := ex*dy - ey*dx // cross product (= perp dist × √den)
				d = num * num / den
			}
			if d > maxD {
				maxD, maxI = d, i
			}
		}
		if maxD > eps2 {
			keep[maxI] = true
			stack = append(stack, [2]int{s, maxI}, [2]int{maxI, e})
		}
	}
	out := make([]tile.FPoint, 0, n)
	for i, k := range keep {
		if k {
			out = append(out, pts[i])
		}
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

// stringAttr returns the trimmed string value of a string attribute, or "".
func stringAttr(attrs map[string]interface{}, key string) string {
	if s, ok := attrs[key].(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
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
