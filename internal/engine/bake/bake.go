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
	"encoding/json"
	"fmt"
	"log"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/beetlebugorg/chartplotter/internal/engine/mvt"
	"github.com/beetlebugorg/chartplotter/internal/engine/pmtiles"
	"github.com/beetlebugorg/chartplotter/internal/engine/portrayal"
	"github.com/beetlebugorg/chartplotter/internal/engine/tile"
	"github.com/beetlebugorg/chartplotter/pkg/geo"
	"github.com/beetlebugorg/chartplotter/pkg/s57"
)

const maxBandZ uint32 = 18

// Display categories (DisplayBase/Standard/Other = 6/7/8). Local copies so the
// bake path needn't import pkg/s52; these enum values are carried through for
// the client filter.
const (
	displayCatBase     = 6
	displayCatStandard = 7
	displayCatOther    = 8
)

// generalOverzoomMin is the lowest zoom the general band displays at (below its
// native min of 7) so general charts don't vanish when zoomed out — all the way
// to the world view. Where an overview cell overlaps, best-available suppression
// defers to it; general only fills the gap. SCAMIN keeps minor features gated, so
// a coarse-zoom tile carries only the skeleton (land/coast/major depth).
const generalOverzoomMin uint32 = 0

// bandBakeCeil is the TOP zoom a per-band archive bakes to (its source's maxzoom;
// the client overzooms above it for free). A band only bakes past its native max
// to sharpen the suppression CUT against the next finer band — so the cut isn't
// stair-stepped at coarse-tile granularity when overzoomed. Beyond that the extra
// levels are pure overzoom buffer the client recreates, and at high zoom they cost
// 4×/16× the tiles, so we don't bake them:
//
//	overview 7, general 9 — capped client-side (lines/patterns don't overzoom), so
//	  no cut to sharpen; base fills overzoom from the native max.
//	coastal 11→13, approach 13→15 — +2 sharpens the cut vs the next finer band.
//	harbor 16 — native max already cuts vs berthing at ~0.4 km; z17/18 would be
//	  pure buffer (this is the big win: drops ~16× of the harbor archive).
//	berthing 18 — finest band, nothing finer to cut against; native detail.
func bandBakeCeil(bandMax uint32) uint32 {
	switch bandMax {
	case 12: // coastal: native max (z12) cuts too coarse; +2 → z14 to sharpen vs approach
		return 14
	default: // everyone else bakes to native max:
		//   overview(8)/general(10) are capped client-side (no overzoom cut to sharpen);
		//   approach(14) cuts vs harbor — fine, and deeper zooms over a whole district
		//     are the dominant size/index cost (Alaska approach: ~1.2 GB → ~80 MB);
		//   harbor(16) cuts vs berthing; berthing(18) is the finest band.
		return bandMax
	}
}

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
		return ZoomRange{0, 8}
	case BandGeneral:
		return ZoomRange{8, 10}
	case BandCoastal:
		return ZoomRange{10, 12}
	case BandApproach:
		return ZoomRange{12, 14}
	case BandHarbor:
		return ZoomRange{14, 16}
	default: // berthing
		return ZoomRange{16, 18}
	}
}

// BakeBand is one navigational-purpose band's identity for per-band archive
// baking: its frontend slug and native [Min,Max] zoom span.
type BakeBand struct {
	Slug     string
	Min, Max uint32
}

// BakeBands lists the bands coarse→fine for per-band archive baking — must match
// the frontend's CHART_BANDS (slug + zoom span) so each archive loads into its
// chart-<slug> source. Max feeds EmitTileBandInto's band filter (natMax == Max).
func BakeBands() []BakeBand {
	return []BakeBand{
		{"overview", 0, 8},
		{"general", 8, 10},
		{"coastal", 10, 12},
		{"approach", 12, 14},
		{"harbor", 14, 16},
		{"berthing", 16, 18},
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
// zoom that keeps the object visible: it shows when the DISPLAY-scale denominator
// (at the cell latitude) is ≤ SCAMIN, i.e. at display zoom ≥ z* = log2(denomZ0/
// SCAMIN). Vector tiles are integer-zoom and the tile serving a fractional display
// zoom d is floor(d), so to keep the object available right down to its SCAMIN
// scale it must live in the floor(z*) tile — hence FLOOR. The client's per-SCAMIN
// bucket layer then applies the EXACT fractional z* as a native layer minzoom, so
// the visible cutoff is exact in both directions. S-52 §8.4: cell latitude, not equator.
//
// SCAMIN is a PRODUCER scale (a real 1:N paper scale), so denomZ0 is the PHYSICAL
// display scale at z0 — MapLibre's true 512-tile geometry (78271.517m/px ÷ OGC
// 0.28mm pixel = 279_541_132), NOT the 256-tile nominal (559M, which would make
// features vanish at ~½ their SCAMIN). This matches the client's scaminDisplayZoom
// so the baked tile floor and the visible cutoff agree, and a 1:N feature survives
// until the screen truly reads 1:N (see chart-sources.scaminDisplayZoom).
func scaminZoom(scamin uint32, lat float64) uint32 {
	if scamin == 0 {
		return 0
	}
	denomZ0 := 279_541_132.0 * math.Cos(lat*math.Pi/180) // 1:N at z0 at this latitude (physical 512-tile scale)
	s := float64(scamin)
	if denomZ0 <= s {
		return 0
	}
	z := math.Floor(math.Log2(denomZ0 / s))
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
//   - DISPLAYBASE: the band min (always shown in-band; SCAMIN never removes base,
//     and base features stay band-gated — the coarse cell shows them at coarse scale).
//   - SCAMIN present: scaminZoom is AUTHORITATIVE and may fall BELOW the band min —
//     a SCAMIN is the producer explicitly saying "keep this visible down to 1:N", so
//     the object stays AVAILABLE down to its SCAMIN scale (crossing into coarser
//     bands). The client's per-SCAMIN bucket layer gates the exact display cutoff;
//     best-available point suppression keeps it from doubling up with a coarse cell.
//   - no SCAMIN: the band min — no over-scale flag ⇒ no display beyond the cell's band.
func bandZMin(displayCategory int, scamin, bandMin uint32, lat float64) uint32 {
	if displayCategory == displayCatBase {
		return bandMin
	}
	if scamin != 0 {
		return scaminZoom(scamin, lat)
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
	nrings [][]tile.UPoint // polygon
	nline  []tile.UPoint   // linestring
	npoint tile.UPoint     // point

	wMinX, wMinY, wMaxX, wMaxY float64 // normalized world bbox [0,1]
	zMin, zMax                 uint32  // display zoom span
	natMin, natMax             uint32  // native band zoom span (for suppression)
	cscl                       uint32  // owning cell's compilation-scale denominator (per-cell best-available)

	// ls (non-nil only for a complex_lines prim) is the linestyle's period
	// geometry; when set the prim is tessellated per zoom at emit (emitComplexLine)
	// instead of drawn as a plain polyline.
	ls *lsInfo

	// attrs is the feature's tag list. For prims routed through route() (the bulk),
	// bcBase is set and attrs holds ONLY the variable tags (color_token, drval…,
	// objnam/light/s57); the always-present base (class/cell/draw_prio/cat/bnd/pts)
	// lives in the compact bc* fields below and is rebuilt at emit (attrsFor) —
	// replacing a ~5–8 entry []KeyValue per feature (the heap-profile hot spot) with
	// a few interned ints. Prims from other constructors keep bcBase=false and a
	// full attrs list.
	attrs    []mvt.KeyValue
	bcClass  uint32 // interned class index (classTab)
	bcCell   uint32 // interned cell index (cellTab)
	bcDrawP  int16
	bcCat    int16
	bcBnd    int8
	bcPts    int16
	bcScamin uint32 // SCAMIN denominator (0 = none); emitted as `scamin` for the client's per-SCAMIN bucket layers
	bcHasPts bool
	bcBase   bool // attrs is variable-only; rebuild the base from bc* at emit
}

// sectorPrim is one constructed sector-light figure element (a dashed leg, or a
// black-backed coloured arc / ring) the S-101 rule emitted via AugmentedRay /
// ArcByRadius. Its mm sizes are screen-px, so it is tessellated per zoom at emit
// time into the sector_lines layer rather than stored as fixed lat/lon geometry —
// driven by the catalogue's bearings/radii/colours, not a Go re-derivation.
type sectorPrim struct {
	fig      portrayal.AugmentedFigure
	class    string
	cell     string
	drawPrio int
	cat      int
	zMin     uint32
	natMax   uint32
	scamin   uint32 // SCAMIN denominator of the parent LIGHTS (0 = none); emitted as `scamin` so the client's per-SCAMIN bucket layer gates the exact display cutoff, same as point symbols/text
	// legNorm is the full-length leg reach (VALNMR nominal range) as a fraction
	// of the normalized world — a fixed GROUND distance, so zoom-independent
	// (unlike the 25 mm short leg / arc, which are screen-px). Drives the tile
	// enumeration + emit margin so the long legs aren't culled near tile edges.
	// 0 for an arc, or a leg whose light has no VALNMR (only the screen-px spills).
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
	// portrayer drives portrayal via the S-101 rule engine. A portrayer is
	// required. Internal (not a user-facing toggle).
	portrayer Portrayer
	bbox      geo.BoundingBox
	curCell   string // dataset name of the cell currently being added (stamped on each feature)
	curCscl   uint32 // compilation-scale denominator of the cell currently being added (per-cell best-available)
	curScamin uint32 // SCAMIN (1:N min display scale) of the feature currently being expanded; 0 = none
	curObjnam string // OBJNAM of the feature currently being expanded (for the inspector)
	curLight  string // light characteristic string of the current LIGHTS feature (e.g. "Fl.R.4s")
	curAttrs  string // compact JSON of the feature's full S-57 attribute set (acronym→value) for the cursor-pick report (S-52 PresLib §10.8); "" when the feature has none
	// Co-located-light combination (S-52 LIGHTS06): when several LIGHTS share a
	// position, the first is "primary" (one flare + a merged multi-line label);
	// the rest are suppressed (flare + text dropped, sectors kept). seenSector
	// dedupes identical sector geometry within the current cell.
	curLightSkip bool                   // current LIGHTS is a non-primary co-located light
	curLightText string                 // merged multi-line characteristic for the primary
	seenSector   map[sectorKey]struct{} // sector dedup for the current cell
	scaminSeen   map[uint32]struct{}    // distinct SCAMIN denominators routed this band → published manifest
	coverage     []CellCoverage         // M_COVR data-coverage polygons of added cells (debug)

	// DATCVR §10.1.9.1 chart scale boundaries: per-cell M_COVR(CATCOV=1) coverage
	// + native band, used by emitScaleBoundaries to draw a line where the
	// navigational purpose changes (a finer cell's coverage edge sitting inside
	// coarser data). Internal seams between same-band cells are suppressed.
	covMeta         []covMeta
	scaleBndEmitted bool
	skipCoverage    bool // AddCell skips M_COVR extraction (covMeta pre-built; streaming bake)

	// Interning for the route() base attributes: `class` (one of ~170 S-57 object
	// classes) and `cell` (the source dataset name) are the same string repeated
	// across millions of features. Store one shared copy here and a uint32 index on
	// each prim (see routed.bcClass/bcCell), rebuilding the KeyValue tags at emit —
	// far cheaper than a per-feature []KeyValue. Built single-threaded in AddCell,
	// read-only (concurrent-safe) during the parallel emit.
	classTab []string
	classIdx map[string]uint32
	cellTab  []string
	cellIdx  map[string]uint32

	// OverzoomAllBands makes EVERY band overzoom DOWN to the world view (like the
	// general band always does), not just BandGeneral. Set on the realtime/upload
	// path so a handful of uploaded cells (e.g. a single large-scale inland ENC)
	// stay visible as a SCAMIN-gated skeleton when you zoom out, instead of
	// vanishing until their native detail zoom. Left false for the prebaked NOAA
	// bake, where thousands of cells would bloat low-zoom tiles (the overview /
	// general bands already supply the zoomed-out skeleton there).
	OverzoomAllBands bool

	// MaxBakeZoom caps the highest zoom tiles are emitted at (0 = uncapped, use each
	// prim's native band max). Large-scale cells over a wide area (e.g. an IENC
	// river network at 1:5000) would otherwise emit tens of millions of z17–18
	// tiles; capping the bake and letting MapLibre overzoom the vector tiles
	// client-side keeps the archive small with no visible detail loss (every
	// feature is already clipped into the capped-zoom tile).
	MaxBakeZoom uint32
}

// clampZMax applies MaxBakeZoom to a prim/sector display max (0 = uncapped).
func (b *Baker) clampZMax(zMax uint32) uint32 {
	if b.MaxBakeZoom != 0 && zMax > b.MaxBakeZoom {
		return b.MaxBakeZoom
	}
	return zMax
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

// covMeta is one cell's data-coverage (M_COVR CATCOV=1) outline plus its native
// scale band, the input to emitScaleBoundaries. rings are GeoJSON
// [ring][point][lon,lat]; bb is their lon/lat bounding box.
type covMeta struct {
	bandMin, bandMax uint32
	cscl             uint32 // compilation-scale denominator (per-cell best-available: finer = smaller wins)
	displayMin       uint32 // lowest zoom this cell's data is shown at (0 for overview/general which overzoom down)
	bb               geo.BoundingBox
	rings            [][][]float64
}

// sectorKey identifies one constructed sector-figure element (anchor + ray/arc
// params + stroke) for dedup — co-located lights often repeat identical sectors.
type sectorKey struct {
	lat, lon   int64
	ray        bool
	p1, p2, p3 int64 // ray: bearing, length, 0 — arc: radius, start, sweep
	col        string
	w          int64
	dashed     bool
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
			ch := BuildLightCharacteristic(features[i].Attributes())
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

// extractCoverage records a cell's M_COVR (CATCOV=1) data-coverage polygons into
// covMeta (keyed to the cell's native band [zr]) — the input to best-available
// suppression and DATCVR scale boundaries.
func (b *Baker) extractCoverage(features []s57.Feature, zr ZoomRange, cell string, cscl, displayMin uint32) {
	for i := range features {
		f := &features[i]
		if f.ObjectClass() != "M_COVR" || intAttr(f.Attributes(), "CATCOV") != 1 {
			continue
		}
		rings := f.Geometry().Rings
		if len(rings) == 0 {
			continue
		}
		cov := CellCoverage{Cell: cell}
		cm := covMeta{bandMin: zr.Min, bandMax: zr.Max, cscl: cscl, displayMin: displayMin, bb: geo.EmptyBox()}
		for _, r := range rings {
			cov.Rings = append(cov.Rings, r.Coordinates)
			cm.rings = append(cm.rings, r.Coordinates)
			for _, pt := range r.Coordinates {
				cm.bb.ExtendPoint(geo.LatLon{Lon: pt[0], Lat: pt[1]})
			}
		}
		b.coverage = append(b.coverage, cov)
		b.covMeta = append(b.covMeta, cm)
		b.bbox.ExtendBox(cm.bb) // so the streaming bake has full bounds after pass 1
	}
}

// cellStem is a cell's dataset name without the .000/.NNN extension.
func cellStem(name string) string {
	if i := strings.LastIndexByte(name, '.'); i > 0 {
		return name[:i]
	}
	return name
}

// AddCellCoverage extracts ONLY a cell's coverage + native band (no feature
// routing) — the streaming bake's first pass, building the global covMeta once so
// each later per-band routing pass can suppress against finer bands without
// re-deriving coverage. Returns the cell's native band.
func (b *Baker) AddCellCoverage(chart *s57.Chart) Band {
	band := BandForScale(uint32(chart.CompilationScale()))
	cscl := uint32(chart.CompilationScale())
	b.extractCoverage(chart.Features(), band.ZoomRange(), cellStem(chart.DatasetName()), cscl, cellDisplayMin(band, band.ZoomRange()))
	return band
}

// cellDisplayMin is the lowest zoom a band's cells are actually drawn at (matches
// the client BAND_DISPLAY_MIN): overview/general overzoom down to z0 to gap-fill, so
// they're "shown" everywhere; the finer bands start at their native min. Used so the
// per-cell suppression only yields to a finer cell that is ACTUALLY displayed at the
// current zoom (a harbor cell doesn't punch holes in approach until harbor zooms in).
func cellDisplayMin(band Band, zr ZoomRange) uint32 {
	if band <= BandGeneral {
		return 0
	}
	return zr.Min
}

// SetSkipCoverage makes AddCell skip M_COVR extraction (covMeta already built by
// AddCellCoverage in the streaming bake's first pass).
func (b *Baker) SetSkipCoverage(v bool) { b.skipCoverage = v }

// ResetPrims drops the routed primitives, sectors, and emit index so the next
// band's cells can be routed into the same Baker, while KEEPING the accumulated
// coverage, bounds, interning tables, and loaded library — so the streaming bake
// holds only one band's geometry at a time.
func (b *Baker) ResetPrims() {
	b.prims = nil
	b.sectors = nil
	b.emitIndex = nil
	b.scaleBndEmitted = false
	b.seenSector = nil
	b.scaminSeen = nil
}

// recordScamin notes a distinct SCAMIN denominator routed into this band, so the
// bake can publish the band's SCAMIN manifest (pmtiles metadata → TileJSON). The
// client builds one native-minzoom bucket layer per value ONCE at load — no
// runtime probe/collect/setStyle needed (the per-frame cost the manifest removes).
func (b *Baker) recordScamin(scamin uint32) {
	if scamin == 0 {
		return
	}
	if b.scaminSeen == nil {
		b.scaminSeen = map[uint32]struct{}{}
	}
	b.scaminSeen[scamin] = struct{}{}
}

// ScaminValues returns this band's distinct SCAMIN denominators, ascending.
func (b *Baker) ScaminValues() []uint32 {
	out := make([]uint32, 0, len(b.scaminSeen))
	for v := range b.scaminSeen {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// AddCell expands every feature of a parsed cell into routed primitives at the
// cell's scale band, with per-feature SCAMIN display z-min.
func (b *Baker) AddCell(chart *s57.Chart) {
	if b.portrayer == nil {
		panic("bake: no S-101 portrayer set — build with `make` (-tags embed_s101) or pass --s101")
	}
	// Cell name (sans the .000/.NNN extension) stamped on every feature for the
	// inspector's source-cell pill.
	b.curCell = chart.DatasetName()
	if i := strings.LastIndexByte(b.curCell, '.'); i > 0 {
		b.curCell = b.curCell[:i]
	}
	if b.linestyles == nil {
		// Complex-line dash/symbol geometry comes from the S-101 catalogue (via the
		// portrayer) now, not the S-52 PresLib.
		if src, ok := b.portrayer.(linestyleSource); ok {
			b.linestyles = src.LinestyleTable()
		}
	}
	band := BandForScale(uint32(chart.CompilationScale()))
	b.curCscl = uint32(chart.CompilationScale()) // per-cell best-available: finer (smaller) wins
	zr := band.ZoomRange()
	// Display range vs native band. General cells overzoom OUT (down to z2) so
	// their data doesn't vanish when you zoom out past z7 with no overview
	// coverage. The native band [zr] still drives best-available suppression, so
	// general yields wherever a coarser (overview) cell actually overlaps — it
	// only fills the gaps. (We don't raise the display ceiling: indexing a
	// cell-wide prim up to high zoom would blow up the emit index.)
	dr := zr
	if band == BandGeneral || b.OverzoomAllBands {
		dr.Min = generalOverzoomMin
	}
	cb := chart.Bounds()
	cellLat := (cb.MinLat + cb.MaxLat) / 2 // SCAMIN→zoom uses the cell's display scale
	features := chart.Features()
	// Cell data-coverage (M_COVR CATCOV=1) → covMeta for best-available suppression
	// and scale boundaries. Skipped when the coverage was already built in a prior
	// pass (the streaming bake builds it once up front, then re-routes per band).
	if !b.skipCoverage {
		b.extractCoverage(features, zr, b.curCell, b.curCscl, cellDisplayMin(band, zr))
	}
	// S-101: portray the whole cell in one engine pass (one Lua chunk + context,
	// fresh Lua state) up front instead of per-feature — the per-feature path
	// recompiled the chunk and leaked the catalogue's file-local caches.
	if bp, ok := b.portrayer.(BatchPortrayer); ok {
		fps := make([]*s57.Feature, len(features))
		for i := range features {
			fps[i] = &features[i]
		}
		bp.Begin(fps)
		defer bp.End()
	}

	// Combine co-located lights (S-52 LIGHTS06): one flare + one merged label.
	lightPrimary, lightSkip := groupCoLocatedLights(features)
	b.seenSector = make(map[sectorKey]struct{})
	for i := range features {
		f := &features[i]
		// Per-feature inspector data: the object name, and (for lights) the S-52
		// light characteristic string ("Fl.R.4s") so the inspector can show the
		// light data, not just the symbol.
		b.curObjnam = stringAttr(f.Attributes(), "OBJNAM")
		b.curAttrs = encodeS57Attrs(f.Attributes())
		b.curLight = ""
		b.curLightSkip = false
		b.curLightText = ""
		if f.ObjectClass() == "LIGHTS" {
			b.curLight = BuildLightCharacteristic(f.Attributes())
			b.curLightSkip = lightSkip[i]
			if merged, ok := lightPrimary[i]; ok {
				b.curLightText = merged
				b.curLight = merged // inspector shows the combined characteristic
			}
		}
		// Boundary symbolization (S-52 §8.6.1): a style-variant area is built
		// twice (plain bnd=0 / symbolized bnd=1) so the client toggles boundary
		// style live; everything else is one pass tagged bnd=2.
		// The portrayer (the build-time embedded catalogue, or --s101) runs the
		// S-101 rules to produce the passes.
		passes := b.portrayer.Passes(f)
		for _, pass := range passes {
			fb := pass.Build
			bnd := int64(pass.Bnd)
			pts := int64(pass.Pts)
			scamin := intAttr(f.Attributes(), "SCAMIN")
			b.curScamin = scamin   // baked as the `scamin` tag → client per-SCAMIN bucket layers
			b.recordScamin(scamin) // publish the band's distinct values (manifest → TileJSON)
			zMin := bandZMin(fb.DisplayCategory, scamin, dr.Min, cellLat)
			class := f.ObjectClass()
			drval1, drval2 := depthVals(f.Attributes(), class)
			valdco := contourValdco(f.Attributes(), class)
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
					b.routeSoundingGroup(names, sc, class, fb.DisplayPriority, fb.DisplayCategory, zr, zMin, dr.Max, bnd, pts)
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
				// DEPCNT contour values are emitted by the rule as SAFCON digit
				// glyphs — fixed metres, and "0" when the contour has no value
				// (the "0 by the shore" labels). Drop them; the client labels the
				// contour from the baked `valdco`, converting to the chosen unit
				// and showing nothing when there is no value.
				if class == "DEPCNT" {
					if sc, ok := p.(portrayal.SymbolCall); ok && strings.HasPrefix(sc.SymbolName, "SAFCON") {
						continue
					}
				}
				b.route(p, class, fb.DisplayPriority, fb.DisplayCategory, zr, zMin, dr.Max, bnd, pts, drval1, drval2, valdco)
			}
		}
	}
}

// routeSoundingGroup emits one soundings feature for a whole sounding number
// (the comma-joined digit-glyph list), carrying depth + both palette variants so
// the client runs SNDFRM04's safety-depth split live.
func (b *Baker) routeSoundingGroup(names []string, sc portrayal.SymbolCall, class string, drawPrio, cat int, zr ZoomRange, zMin, zMax uint32, bnd, pts int64) {
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
	if b.curAttrs != "" {
		attrs = append(attrs, mvt.KeyValue{Key: "s57", Value: mvt.StringVal(b.curAttrs)})
	}
	if b.curScamin != 0 {
		attrs = append(attrs, mvt.KeyValue{Key: "scamin", Value: mvt.IntVal(int64(b.curScamin))})
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
	case displayCatBase:
		return 0
	case displayCatStandard:
		return 1
	default: // displayCatOther
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
	if r.cscl == 0 {
		r.cscl = b.curCscl // owning cell's compilation scale (structural prims may preset it)
	}
	r.wMinX = normX(bb.MinLon)
	r.wMaxX = normX(bb.MaxLon)
	r.wMinY = normY(bb.MaxLat) // north -> smaller y
	r.wMaxY = normY(bb.MinLat) // south -> larger y
	b.prims = append(b.prims, r)
}

func (b *Baker) internClass(s string) uint32 {
	if b.classIdx == nil {
		b.classIdx = map[string]uint32{}
	}
	if i, ok := b.classIdx[s]; ok {
		return i
	}
	i := uint32(len(b.classTab))
	b.classTab = append(b.classTab, s)
	b.classIdx[s] = i
	return i
}

func (b *Baker) internCell(s string) uint32 {
	if b.cellIdx == nil {
		b.cellIdx = map[string]uint32{}
	}
	if i, ok := b.cellIdx[s]; ok {
		return i
	}
	i := uint32(len(b.cellTab))
	b.cellTab = append(b.cellTab, s)
	b.cellIdx[s] = i
	return i
}

// attrsFor returns a prim's full MVT tags. For route() prims (bcBase) it rebuilds
// the base (class/cell/draw_prio/cat/bnd/pts) from the compact fields + interned
// strings into the caller's reused scratch slice (no per-emit allocation), then
// appends the variable tags. Other prims return their full attrs unchanged.
func (b *Baker) attrsFor(r *routed, scratch *[]mvt.KeyValue) []mvt.KeyValue {
	if !r.bcBase {
		return r.attrs
	}
	out := append((*scratch)[:0],
		mvt.KeyValue{Key: "class", Value: mvt.StringVal(b.classTab[r.bcClass])},
		mvt.KeyValue{Key: "cell", Value: mvt.StringVal(b.cellTab[r.bcCell])},
		mvt.KeyValue{Key: "draw_prio", Value: mvt.IntVal(int64(r.bcDrawP))},
		mvt.KeyValue{Key: "cat", Value: mvt.IntVal(int64(r.bcCat))},
		mvt.KeyValue{Key: "bnd", Value: mvt.IntVal(int64(r.bcBnd))},
	)
	if r.bcHasPts {
		out = append(out, mvt.KeyValue{Key: "pts", Value: mvt.IntVal(int64(r.bcPts))})
	}
	if r.bcScamin != 0 {
		out = append(out, mvt.KeyValue{Key: "scamin", Value: mvt.IntVal(int64(r.bcScamin))})
	}
	out = append(out, r.attrs...)
	*scratch = out
	return out
}

// scaminLayer redirects an area/line primitive's source-layer to a dedicated
// "<layer>_scamin" layer when the feature carries SCAMIN (1:N display limit, S-52
// §8.4). The client buckets these *_scamin layers into per-SCAMIN fractional-minzoom
// variants so the feature DISAPPEARS when zoomed out past its 1:N scale; no-SCAMIN
// features stay in the original always-in-band layer (unchanged). Only the four
// area/line layers route here — point_symbols/soundings/text/sector_lines already
// carry `scamin` and are bucketed directly on their own source-layers.
func scaminLayer(layer string, scamin uint32) string {
	if scamin == 0 {
		return layer
	}
	switch layer {
	case "areas", "area_patterns", "lines", "complex_lines":
		return layer + "_scamin"
	}
	return layer
}

func (b *Baker) route(p portrayal.Primitive, class string, drawPrio, cat int, zr ZoomRange, zMin, zMax uint32, bnd, pts int64, drval1, drval2, valdco float32) {
	// The always-present base (class/cell/draw_prio/cat/bnd/pts) is stored compactly
	// (interned class/cell + small ints) and rebuilt at emit by attrsFor; `common`
	// returns only the VARIABLE tags — the per-feature extra plus the sparse
	// inspector/pick fields (objnam/light/s57). Tag ORDER is irrelevant (MVT
	// properties are a map), so appending these after extra is fine.
	common := func(extra ...mvt.KeyValue) []mvt.KeyValue {
		if b.curObjnam != "" {
			extra = append(extra, mvt.KeyValue{Key: "objnam", Value: mvt.StringVal(b.curObjnam)})
		}
		if b.curLight != "" {
			extra = append(extra, mvt.KeyValue{Key: "light", Value: mvt.StringVal(b.curLight)})
		}
		// Full S-57 attribute set for the cursor-pick report (S-52 PresLib §10.8).
		if b.curAttrs != "" {
			extra = append(extra, mvt.KeyValue{Key: "s57", Value: mvt.StringVal(b.curAttrs)})
		}
		return extra
	}
	r := routed{
		zMin: zMin, zMax: zMax, natMin: zr.Min, natMax: zr.Max,
		bcBase:   true,
		bcClass:  b.internClass(class),
		bcCell:   b.internCell(b.curCell),
		bcDrawP:  int16(drawPrio),
		bcCat:    int16(catRank(cat)),
		bcBnd:    int8(bnd),
		bcScamin: b.curScamin,
	}
	// pts is omitted for the common case (2): only paper/simplified variant passes
	// (0/1) carry it, so most features stay lean.
	if pts != ptsAlwaysShown {
		r.bcHasPts, r.bcPts = true, int16(pts)
	}

	switch v := p.(type) {
	case portrayal.FillPolygon:
		r.layer, r.kind, r.nrings = scaminLayer("areas", r.bcScamin), mvt.GeomPolygon, normRings(v.Rings)
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
		r.layer, r.kind, r.nrings = scaminLayer("area_patterns", r.bcScamin), mvt.GeomPolygon, normRings(v.Rings)
		r.attrs = common(mvt.KeyValue{Key: "pattern_name", Value: mvt.StringVal(v.PatternName)})
		b.add(r, ringsBbox(v.Rings))
	case portrayal.StrokeLine:
		r.layer, r.kind, r.nline = scaminLayer("lines", r.bcScamin), mvt.GeomLineString, normPts(v.Points)
		extra := []mvt.KeyValue{
			{Key: "color_token", Value: mvt.StringVal(v.ColorToken)},
			{Key: "width_px", Value: mvt.IntVal(int64(v.WidthPx + 0.5))},
			{Key: "dash", Value: mvt.StringVal(dashName(v.Dash))},
		}
		// DEPCNT depth-contour value (metres) so the client labels the contour in
		// the chosen depth unit (SAFCON01, client-side); only set for contours.
		if !isNaN32(valdco) {
			extra = append(extra, mvt.KeyValue{Key: "valdco", Value: mvt.FloatVal(valdco)})
		}
		r.attrs = common(extra...)
		b.add(r, ptsBbox(v.Points))
	case portrayal.LinePattern:
		// Stored as a polyline + its linestyle; emitComplexLine tessellates the
		// period (dashes → these complex_lines segments, symbols → point_symbols)
		// per zoom at emit time. colour_token drives the live restyle of the dashes.
		r.layer, r.kind, r.nline = scaminLayer("complex_lines", r.bcScamin), mvt.GeomLineString, normPts(v.Points)
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
			// S-52 §14.4 text grouping (DISPLAY param) so the client can toggle
			// text groups (§14.5) live: 11=important, 21/26/29=names, 23=light, …
			mvt.KeyValue{Key: "tgrp", Value: mvt.IntVal(int64(v.Group))},
		)
		b.add(r, ptBbox(v.Anchor))
	case portrayal.SymbolCall:
		b.routeSymbol(v, common, r)
	case portrayal.AugmentedFigure:
		// One constructed sector-figure element (leg / arc). Dedupe identical
		// elements (co-located lights often repeat sectors).
		var p1, p2, p3 int64
		if v.Ray {
			p1, p2 = quantDeg(v.BearingDeg), quantDeg(v.LengthMM)
		} else {
			p1, p2, p3 = quantDeg(v.RadiusMM), quantDeg(v.StartDeg), quantDeg(v.SweepDeg)
		}
		k := sectorKey{
			lat: quantDeg(v.Anchor.Lat), lon: quantDeg(v.Anchor.Lon),
			ray: v.Ray, p1: p1, p2: p2, p3: p3,
			col: v.ColorToken, w: quantDeg(v.WidthMM), dashed: v.Dash == portrayal.DashDashed,
		}
		if b.seenSector != nil {
			if _, dup := b.seenSector[k]; dup {
				return
			}
			b.seenSector[k] = struct{}{}
		}
		b.bbox.ExtendPoint(v.Anchor)
		var legNorm float64
		if v.Ray && v.FullLengthNM > 0 {
			legNorm = sectorLegFullNorm(v.Anchor.Lat, v.FullLengthNM)
		}
		b.sectors = append(b.sectors, sectorPrim{
			fig: v, class: class, cell: b.curCell,
			drawPrio: drawPrio, cat: cat, zMin: zMin, natMax: zr.Max,
			scamin:  b.curScamin,
			legNorm: legNorm,
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
	// rot_north tags a TRUE-NORTH-referenced rotation (ORIENT etc.), so the client
	// routes it to the map-aligned point-symbol layer that turns with the chart.
	// Absent ⇒ screen-referenced (upright), the common case — kept off the tile to
	// stay compact (S-52 PresLib §9.2 ROT 1/2 vs 3).
	if v.RotationTrueNorth {
		attrs = append(attrs, mvt.KeyValue{Key: "rot_north", Value: mvt.IntVal(1)})
	}
	if !isNaN32(v.DangerDepthM) {
		attrs = append(attrs,
			mvt.KeyValue{Key: "danger_depth", Value: mvt.FloatVal(v.DangerDepthM)},
			mvt.KeyValue{Key: "sym_deep", Value: mvt.StringVal(v.DeepSymbolName)},
		)
	}
	r.attrs = attrs
	b.add(r, ptBbox(v.Anchor))
}

// emitScaleBoundaries draws S-52 DATCVR §10.1.9.1 "chart scale boundaries": a
// line where the navigational purpose changes — i.e. along a cell's data-coverage
// (M_COVR CATCOV=1) edge wherever STRICTLY-COARSER data lies just outside it. The
// boundary is emitted once, by the finer cell, and extended DOWN to the coarser
// side's band so it is visible when zoomed out (marking "larger-scale data here").
// Edges shared with a SAME-band cell are internal seams and are suppressed (the
// spec draws only navigational-purpose changes, not minor in-band joins); edges
// facing no data are the end of coverage, shown by the NODATA hatch, not a line.
// Idempotent; runs once before tile enumeration. Adds prims to b.prims.
//
// Outside-side membership is sampled by probing a short perpendicular off each
// segment's midpoint (the side not inside this cell's own coverage), then a
// point-in-coverage test against the other cells — robust to mismatched ring
// tessellation between adjoining cells (no shared-vertex assumption).
func (b *Baker) emitScaleBoundaries() {
	if b.scaleBndEmitted {
		return
	}
	b.scaleBndEmitted = true
	if len(b.covMeta) < 2 {
		return
	}
	const eps = 0.0006 // ~60 m perpendicular probe (degrees, mid-latitude)
	for ci := range b.covMeta {
		cm := &b.covMeta[ci]
		for _, ring := range cm.rings {
			n := len(ring)
			if n < 2 {
				continue
			}
			var run []geo.LatLon
			minOut := cm.bandMin
			flush := func() {
				if len(run) >= 2 {
					b.addScaleBoundary(run, minOut, cm.bandMax)
				}
				run = run[:0]
				minOut = cm.bandMin
			}
			for i := 0; i < n; i++ {
				lon1, lat1 := ring[i][0], ring[i][1]
				lon2, lat2 := ring[(i+1)%n][0], ring[(i+1)%n][1]
				dx, dy := lon2-lon1, lat2-lat1
				plen := math.Hypot(dx, dy)
				if plen == 0 {
					continue
				}
				mx, my := (lon1+lon2)/2, (lat1+lat2)/2
				nx, ny := -dy/plen*eps, dx/plen*eps
				ax, ay := mx+nx, my+ny
				cx, cy := mx-nx, my-ny
				aIn := pointInRings(ax, ay, cm.rings)
				cIn := pointInRings(cx, cy, cm.rings)
				if aIn == cIn { // corner/degenerate — can't tell outside; break the run
					flush()
					continue
				}
				ox, oy := cx, cy
				if cIn {
					ox, oy = ax, ay
				}
				// Classify the outside: a same-band neighbour ⇒ internal seam (drop);
				// the coarsest strictly-coarser neighbour sets how far down to show it.
				seam, coarser := false, false
				coarsestMin := cm.bandMin
				for oj := range b.covMeta {
					if oj == ci {
						continue
					}
					o := &b.covMeta[oj]
					if !o.bb.Contains(geo.LatLon{Lat: oy, Lon: ox}) || !pointInRings(ox, oy, o.rings) {
						continue
					}
					if o.bandMin == cm.bandMin {
						seam = true
						break
					}
					if o.bandMin < cm.bandMin { // smaller band min = coarser
						coarser = true
						if o.bandMin < coarsestMin {
							coarsestMin = o.bandMin
						}
					}
				}
				if seam || !coarser {
					flush()
					continue
				}
				if len(run) == 0 {
					run = append(run, geo.LatLon{Lat: lat1, Lon: lon1})
				}
				run = append(run, geo.LatLon{Lat: lat2, Lon: lon2})
				if coarsestMin < minOut {
					minOut = coarsestMin
				}
			}
			flush()
		}
	}
}

// addScaleBoundary appends one scale-boundary polyline prim (DATCVR §10.1.9.1).
// natMin==zMin and natMax==zMax so best-available suppression never touches it —
// the boundary is a structural line, shown across its whole [zMin,zMax] span.
func (b *Baker) addScaleBoundary(pts []geo.LatLon, zMin, zMax uint32) {
	bb := geo.EmptyBox()
	for _, p := range pts {
		bb.ExtendPoint(p)
	}
	r := routed{
		layer: "scale_boundaries", kind: mvt.GeomLineString, nline: normPts(pts),
		zMin: zMin, zMax: zMax, natMin: zMin, natMax: zMax,
		attrs: []mvt.KeyValue{
			{Key: "class", Value: mvt.StringVal("SCLBDY")},
			{Key: "color_token", Value: mvt.StringVal("CHGRD")},
			{Key: "width_px", Value: mvt.IntVal(2)},
		},
	}
	b.add(r, bb)
}

// TileCoords enumerates every tile (across each primitive's display zooms) that
// the resident primitives touch.
func (b *Baker) TileCoords(extent uint32) []tile.TileCoord {
	b.emitScaleBoundaries() // adds scale-boundary prims; must precede enumeration
	seen := map[uint64]struct{}{}
	var out []tile.TileCoord
	for i := range b.prims {
		r := &b.prims[i]
		bb := geo.BoundingBox{
			MinLat: unnormY(r.wMaxY), MinLon: r.wMinX*360 - 180,
			MaxLat: unnormY(r.wMinY), MaxLon: r.wMaxX*360 - 180,
		}
		out = addRange(out, seen, bb, r.zMin, b.clampZMax(r.zMax), extent)
	}
	// A sector light's screen-px figure (the 26 mm ring/legs) spills well beyond
	// its anchor tile — up to ~0.3 tile at any zoom. Enumerate every tile that
	// spill touches (per zoom, since the figure is a fixed fraction of a tile)
	// so a neighbour tile with no other primitives is still emitted; otherwise
	// the arc is clipped dead at the tile boundary.
	for i := range b.sectors {
		sp := &b.sectors[i]
		ax, ay := normX(sp.fig.Anchor.Lon), normY(sp.fig.Anchor.Lat)
		for z := sp.zMin; z <= b.clampZMax(sp.natMax); z++ {
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

// TileCoordsBand enumerates the tiles for ONE per-band archive: only this band's
// own prims (natMax == bandMax), across [bandMin, bandBakeCeil(bandMax)]. The
// client overzooms above the ceiling.
func (b *Baker) TileCoordsBand(extent, bandMin, bandMax uint32) []tile.TileCoord {
	b.emitScaleBoundaries() // idempotent; adds scale-boundary prims before enumeration
	ceil := b.clampZMax(bandBakeCeil(bandMax))
	seen := map[uint64]struct{}{}
	var out []tile.TileCoord
	for i := range b.prims {
		r := &b.prims[i]
		if r.natMax != bandMax {
			continue
		}
		// lo is the prim's display zMin — normally ≥ bandMin, but a SCAMIN-bearing
		// feature may sit BELOW bandMin (it crosses into coarser bands down to its
		// SCAMIN scale). Emit it into the band archive at that lower zoom so the
		// source can serve it; the client's per-SCAMIN bucket layer gates the exact
		// cutoff. Non-SCAMIN features still have zMin == bandMin (bandZMin floors them).
		lo := r.zMin
		if lo > ceil {
			continue
		}
		bb := geo.BoundingBox{
			MinLat: unnormY(r.wMaxY), MinLon: r.wMinX*360 - 180,
			MaxLat: unnormY(r.wMinY), MaxLon: r.wMaxX*360 - 180,
		}
		out = addRange(out, seen, bb, lo, ceil, extent)
	}
	for i := range b.sectors {
		sp := &b.sectors[i]
		if sp.natMax != bandMax {
			continue
		}
		ax, ay := normX(sp.fig.Anchor.Lon), normY(sp.fig.Anchor.Lat)
		// Like the flare prim (see lo := r.zMin above), a SCAMIN-bearing sector may
		// sit BELOW bandMin — keep it AVAILABLE down to its SCAMIN scale so the
		// client's per-SCAMIN bucket gates the exact cutoff. Non-SCAMIN sectors have
		// zMin == bandMin (bandZMin floors them), so this is a no-op for them.
		lo := sp.zMin
		for z := lo; z <= b.clampZMax(sp.natMax); z++ {
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
	b.buildEmitIndex(extent, buffer, 0)
}

// BuildEmitIndexBand builds the index for ONE band's archive: only that band's own
// prims (natMax == bandMax), keyed up to the band's bake ceiling. Built+freed per
// band (see BakeToPMTilesBands) so the indexes for all six bands are never resident
// at once — the index for a full district is the dominant peak-RAM cost otherwise.
func (b *Baker) BuildEmitIndexBand(extent uint32, buffer float64, bandMax uint32) {
	b.buildEmitIndex(extent, buffer, bandMax)
}

// ClearEmitIndex drops the emit index so its memory is reclaimed between bands.
func (b *Baker) ClearEmitIndex() { b.emitIndex = nil }

// buildEmitIndex builds the tile→prim index. bandMax==0 indexes every prim across
// its full display zoom span (the merged tile); bandMax!=0 indexes only that band's
// own prims (natMax == bandMax) up to the band's bake ceiling.
func (b *Baker) buildEmitIndex(extent uint32, buffer float64, bandMax uint32) {
	b.emitScaleBoundaries() // adds scale-boundary prims; must precede indexing
	idx := map[uint64][]int32{}
	bufFrac := buffer / float64(extent)
	var dropped int // prims skipped for a pathological (corrupt-geometry) bbox
	for i := range b.prims {
		r := &b.prims[i]
		hi := b.clampZMax(r.zMax)
		if bandMax != 0 {
			if r.natMax != bandMax {
				continue // per-band index: skip other bands' prims
			}
			hi = b.clampZMax(bandBakeCeil(bandMax)) // bake the band's overzoom ceiling
		}
		// Guard against a single prim with a corrupt, world-spanning bbox (e.g. a
		// mis-assembled ring with a stray vertex): at a fine zoom it would expand to
		// hundreds of millions of tiles here and blow up memory (a 20 GB+ index)
		// before a single tile is baked. A real chart object never spans this many
		// tiles, so skip+log it rather than OOM the whole bake.
		if z0n := primTileSpan(r, b.clampZMax(hi), bufFrac); z0n > maxPrimTilesPerZoom {
			dropped++
			log.Printf("bake: skipping prim with implausible bbox (corrupt geometry): cell=%s class=%s bbox=[%.4f,%.4f,%.4f,%.4f] ~%d tiles @z%d",
				b.cellTab[r.bcCell], b.classTab[r.bcClass],
				r.wMinX*360-180, unnormY(r.wMaxY), r.wMaxX*360-180, unnormY(r.wMinY), z0n, hi)
			continue
		}
		for z := r.zMin; z <= hi; z++ {
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
	if dropped > 0 {
		log.Printf("bake: dropped %d prim(s) with corrupt world-spanning geometry from the emit index", dropped)
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

// maxPrimTilesPerZoom caps how many tiles a single prim may span at its bake
// ceiling before it's treated as corrupt geometry (a stray vertex inflating the
// bbox toward world scale) and dropped from the emit index. A real chart object —
// even a coarse overview cell at its own coarse ceiling (world ≈ 65 k tiles at z8)
// — stays far below this; only a mis-assembled ring reaches it, and indexing it
// would expand to hundreds of millions of tiles and OOM the bake.
const maxPrimTilesPerZoom int64 = 2_000_000

// primTileSpan is the number of tiles a prim's (buffer-expanded) bbox covers at
// zoom z — the per-prim cost it would add to the emit index at that zoom.
func primTileSpan(r *routed, z uint32, bufFrac float64) int64 {
	n := math.Pow(2, float64(z))
	last := int64(n) - 1
	bufN := bufFrac / n
	xMin := clampTile(int64(math.Ceil((r.wMinX-bufN)*n))-1, last)
	xMax := clampTile(int64(math.Floor((r.wMaxX+bufN)*n)), last)
	yMin := clampTile(int64(math.Ceil((r.wMinY-bufN)*n))-1, last)
	yMax := clampTile(int64(math.Floor((r.wMaxY+bufN)*n)), last)
	return (xMax - xMin + 1) * (yMax - yMin + 1)
}

// sectorRadiusNorm is the sector figure's maximum screen-px extent (the 26 mm
// all-round ring) in normalized-world units at zoom z. The geometry is laid out
// in a 256-px-per-tile space (see tessellateFigure's worldPx), so the spill is a
// fixed fraction of a tile at every zoom: 26 mm × px/mm ÷ 256 ÷ 2^z.
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
	pb.SetScamin(b.ScaminValues()) // SCAMIN manifest in metadata (client builds buckets at load)
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
	attrs    []mvt.KeyValue // reused per feature by attrsFor (no per-emit alloc)
}

// EmitTile bakes one tile with a throwaway scratch — convenience for the serial
// path / tests. Hot parallel callers should reuse a TileScratch via EmitTileInto.
func (b *Baker) EmitTile(coord tile.TileCoord, extent uint32, buffer float64) []byte {
	return b.EmitTileInto(coord, extent, buffer, &TileScratch{})
}

// EmitTileInto bakes the merged (all-band) MVT for one tile, or nil if empty,
// reusing ts's buffers.
func (b *Baker) EmitTileInto(coord tile.TileCoord, extent uint32, buffer float64, ts *TileScratch) []byte {
	return b.emitTileInto(coord, extent, buffer, ts, 0)
}

// EmitTileBandInto bakes ONLY the single navigational band whose native max zoom
// == bandMax (one per-band archive tile), still gap-clipped above that band where
// a finer cell's M_COVR actually covers — so a coarser band's overzoomed fill is
// dropped wherever finer data exists. bandMax==0 emits the merged all-band tile.
func (b *Baker) EmitTileBandInto(coord tile.TileCoord, extent uint32, buffer float64, ts *TileScratch, bandMax uint32) []byte {
	return b.emitTileInto(coord, extent, buffer, ts, bandMax)
}

// emitTileInto bakes one tile (bandMax==0 ⇒ every band merged), or nil if empty,
// reusing ts's buffers.
func (b *Baker) emitTileInto(coord tile.TileCoord, extent uint32, buffer float64, ts *TileScratch, bandMax uint32) []byte {
	// Full-scan / realtime path emits without a prebuilt index, so make sure the
	// finer-coverage cuts exist (idempotent; the prebaked paths already ran it
	// single-threaded in BuildEmitIndex before any parallel worker calls this).
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
	var gatedZMin int // polys that overlap this tile but are hidden below their display zMin (SCAMIN) — diagnostic
	minNatMin := uint32(math.MaxUint32)
	consider := func(i int) {
		r := &b.prims[i]
		// Spatial reject first so the zMin diagnostic below counts only prims that
		// actually overlap this tile (the full-scan path considers every prim).
		if r.wMaxX < tnx0 || r.wMinX > tnx1 || r.wMaxY < tny0 || r.wMinY > tny1 {
			return
		}
		// Per-band archive: keep only this band's own prims. Coarser bands fill the
		// gaps from THEIR sources (whose client overzoom reaches into this band's
		// zooms); they must not be baked into this band's tiles or they'd bleed.
		if bandMax != 0 && r.natMax != bandMax {
			return
		}
		// Lower gate only. The UPPER end is governed by best-available suppression
		// below: the full-scan (wasm) path overzooms a coarse prim UP past its native
		// band to fill where no finer cell exists, suppressed only where a finer cell
		// actually covers. The indexed (prebaked) path lists each prim only across
		// [zMin, zMax], so it never overzooms up — there the per-band sources +
		// MapLibre client overzoom do the same job. r.zMax bounds the index only.
		if coord.Z < r.zMin {
			if r.kind == mvt.GeomPolygon {
				gatedZMin++
			}
			return
		}
		eligible = append(eligible, i)
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
	// Tile centre (used by both the merged-path band test and the per-band cell-scale
	// test) — a coarse line/fill is the same band/scale across the whole small tile.
	ctrLon := (float64(coord.X)+0.5)/n*360 - 180
	ctrLat := unnormY((float64(coord.Y) + 0.5) / n)
	// tileCovBand: finest band whose M_COVR data-coverage contains this tile's centre
	// — the merged-path up-suppression's "is a finer cell actually here" test.
	tileCovBand, tileCovDone := uint32(0), false
	covBandAt := func() uint32 {
		if !tileCovDone {
			tileCovBand, tileCovDone = b.coverageBandAt(ctrLat, ctrLon), true
		}
		return tileCovBand
	}
	var suppDown, suppUp, emptyGeom int // tile-generation diagnostics (see TileDiag)
	var polyElig, polyEmit int          // polygon prims eligible vs actually emitted (diagnostic)
	for _, i := range eligible {
		r := &b.prims[i]
		// Down-fill: a prim displayed BELOW its native band (general/overzoom cells)
		// yields only where no coarser cell covers, so coarse bands stay best-
		// available when zoomed out.
		if bandZ < r.natMin && r.natMin > minNatMin && b.anyCoarserOverlaps(eligible, r) {
			suppDown++
			continue
		}
		// Best-available suppression: a prim yields where a strictly-FINER cell that
		// is actually shown at this zoom covers it.
		//   • MERGED tile (bandMax==0, realtime cp://): band-gated, by band (every band
		//     shares one tile). Points use the per-feature overlap test.
		//   • PER-BAND archive (bandMax!=0): by per-CELL compilation SCALE, so the finer
		//     cell wins both ACROSS bands and BETWEEN same-band cells of different scale
		//     (US1GC09M 1:2.16M vs US2EC02M 1:1.2M, both "general"). coverageScaleAt is
		//     zoom-gated, so a finer cell that isn't drawn yet at this zoom doesn't punch
		//     a hole in the coarser one — which replaces the old `bandZ >= natMax` gate.
		var suppressed bool
		if bandMax == 0 {
			if bandZ >= r.natMax {
				if r.kind == mvt.GeomPoint {
					suppressed = b.anyFinerOverlaps(eligible, r)
				} else {
					suppressed = r.natMax < covBandAt()
				}
			}
		} else if r.cscl != 0 && r.layer != "scale_boundaries" &&
			(r.kind == mvt.GeomPoint || r.kind == mvt.GeomLineString || r.layer == "area_patterns" || r.layer == "areas") {
			if r.kind == mvt.GeomPoint {
				// A point tests its OWN position — a boundary tile keeps coarse points
				// that fall outside the finer coverage.
				if s := b.coverageScaleAt(unnormY(r.wMinY), r.wMinX*360-180, bandZ); s != 0 && s < r.cscl {
					suppressed = true
				}
			} else {
				// Lines/fills SPAN the tile, so testing the tile CENTRE alone punched a
				// hole at cell SEAMS: a tile straddling the boundary between a coarse cell
				// and an adjacent finer cell has its centre in the finer cell, which
				// suppressed the coarse cell's portion on the OTHER side of the seam where
				// the finer cell has NO data (e.g. US4MD1ED's depth fill just north of the
				// 39.0 line it shares with the finer US4MD1DD — the "bottom half disappears"
				// gap). Suppress only when a finer cell covers the WHOLE tile (centre + 4
				// corners); a partially-covered seam tile keeps the coarse fill, and the
				// finer cell — drawn later, on top — covers it where it actually has data.
				// No visible double-draw (finer wins on top); no seam gap.
				wLon, eLon := float64(coord.X)/n*360-180, float64(coord.X+1)/n*360-180
				nLat, sLat := unnormY(float64(coord.Y)/n), unnormY(float64(coord.Y+1)/n)
				suppressed = true
				for _, pt := range [...][2]float64{{ctrLat, ctrLon}, {nLat, wLon}, {nLat, eLon}, {sLat, wLon}, {sLat, eLon}} {
					if s := b.coverageScaleAt(pt[0], pt[1], bandZ); s == 0 || s >= r.cscl {
						suppressed = false // part of the tile has no finer cell — keep the coarse prim
						break
					}
				}
			}
		}
		if suppressed {
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
				tb.Layer(r.layer).AddPolygon(outRings, b.attrsFor(r, &ts.attrs))
				polyEmit++
			} else {
				emptyGeom++
			}
		case mvt.GeomLineString:
			if r.ls != nil {
				// Complex (symbolised) linestyle: tessellate its period per zoom.
				b.emitComplexLine(r, proj, rect, coord.Z, extent, tb, &scratch, &ts.attrs)
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
				tb.Layer(r.layer).AddLines(paths, b.attrsFor(r, &ts.attrs))
			}
		case mvt.GeomPoint:
			p := proj.ProjectNormU(r.npoint)
			if p.X < 0 || p.X >= e || p.Y < 0 || p.Y >= e {
				continue
			}
			tb.Layer(r.layer).AddPoints([]mvt.IPoint{tile.Quantize(p)}, b.attrsFor(r, &ts.attrs))
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
		if bandMax != 0 && sp.natMax != bandMax {
			continue
		}
		margin := math.Max(sectorRadiusNorm(coord.Z), sp.legNorm) + spill
		ax, ay := normX(sp.fig.Anchor.Lon), normY(sp.fig.Anchor.Lat)
		if ax < tnx0-margin || ax > tnx1+margin || ay < tny0-margin || ay > tny1+margin {
			continue
		}
		for _, st := range tessellateFigure(sp, coord.Z) {
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
			// SCAMIN of the parent LIGHTS — so the client's per-SCAMIN bucket layer
			// gates the sector figure at the EXACT display scale, in both directions,
			// matching the light's flare (point_symbols) and characteristic text. Its
			// own `sector_lines` layer keeps the bucket fan-out off the shared `lines`.
			if sp.scamin != 0 {
				attrs = append(attrs, mvt.KeyValue{Key: "scamin", Value: mvt.IntVal(int64(sp.scamin))})
			}
			tb.Layer("sector_lines").AddLines(paths, attrs)
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
				px, py := float64(p.X)/4294967296.0, float64(p.Y)/4294967296.0 // UPoint → [0,1]
				minx, maxx = math.Min(minx, px), math.Max(maxx, px)
				miny, maxy = math.Min(miny, py), math.Max(maxy, py)
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
// empty=true lost everything to suppression or clipping. Off (nil) by default;
// tests set it directly.
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

// tessellateFigure tessellates one constructed sector-figure element (sp.fig) at
// integer zoom z into lat/lon line strokes, screen-px sized (the mm sizes are
// fixed display millimetres, hence per-zoom). A leg (ray) becomes one stroke from
// the anchor along its bearing; when the light has a nominal range it also emits
// the extended "full light lines" leg, the two tagged sleg 0/1 for the client's
// live toggle. An arc/ring becomes one polyline stroke. Colour, width and dash
// all come from the rule's LineStyle — including the black backing under a
// coloured arc and a white light's yellow (LITYW) arc — not a Go re-derivation.
// The rule has already applied the from-seaward +180 bearing reversal, so the
// bearings/angles are used as-is.
func tessellateFigure(sp *sectorPrim, z uint32) []sectorStroke {
	worldPx := 256.0 * math.Pow(2, float64(z))
	ax, ay := normX(sp.fig.Anchor.Lon)*worldPx, normY(sp.fig.Anchor.Lat)*worldPx
	pxPerMM := float64(portrayal.DefaultPxPerSymbolUnit) * 100.0
	widthPx := sp.fig.WidthMM * pxPerMM
	dashed := sp.fig.Dash == portrayal.DashDashed

	if !sp.fig.Ray { // arc / ring
		radius := sp.fig.RadiusMM * pxPerMM
		if radius <= 0 {
			return nil
		}
		sweep := sp.fig.SweepDeg
		if sweep == 0 {
			sweep = 360 // a zero sweep is a full all-round ring
		}
		n := int(math.Ceil(math.Abs(sweep) / 3.0))
		if n < 8 {
			n = 8
		}
		pts := make([]geo.LatLon, n+1)
		for i := range pts {
			brg := sp.fig.StartDeg + sweep*float64(i)/float64(n)
			dx, dy := bearingToScreen(brg)
			pts[i] = sunproject(ax+dx*radius, ay+dy*radius, worldPx)
		}
		// One stroke; the rule emits the black backing and the coloured arc as
		// separate figures, so the double-stroke is preserved by draw order. Arcs/
		// rings carry no leg tag (sleg -1) — always shown, regardless of the toggle.
		return []sectorStroke{{points: pts, colorToken: sp.fig.ColorToken, widthPx: float32(widthPx), dashed: dashed, sleg: -1}}
	}

	// Leg (ray): the rule's length, plus the extended full-length variant when a
	// nominal range is known.
	emit := func(out []sectorStroke, lenPx float64, sleg int) []sectorStroke {
		if lenPx <= 0 {
			return out
		}
		dx, dy := bearingToScreen(sp.fig.BearingDeg)
		pts := []geo.LatLon{
			sunproject(ax, ay, worldPx),
			sunproject(ax+dx*lenPx, ay+dy*lenPx, worldPx),
		}
		return append(out, sectorStroke{points: pts, colorToken: sp.fig.ColorToken, widthPx: float32(widthPx), dashed: dashed, sleg: sleg})
	}
	legShort := sp.fig.LengthMM * pxPerMM
	if sp.fig.FullLengthNM <= 0 {
		return emit(nil, legShort, -1) // can't extend: the leg is always shown
	}
	legFull := sectorLegFullNorm(sp.fig.Anchor.Lat, sp.fig.FullLengthNM) * worldPx
	if legFull < legShort {
		legFull = legShort
	}
	var out []sectorStroke
	out = emit(out, legShort, 0)
	out = emit(out, legFull, 1)
	return out
}

func bearingToScreen(deg float64) (float64, float64) {
	r := deg * math.Pi / 180.0
	return math.Sin(r), -math.Cos(r) // y grows southward: north=(0,-1), east=(1,0)
}

func sunproject(x, y, worldPx float64) geo.LatLon {
	return geo.LatLon{Lat: unnormY(y / worldPx), Lon: x/worldPx*360 - 180}
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
// world bbox overlaps r (AABB only). Gates up-direction suppression of POINT
// features only: a coarse symbol survives unless a finer prim sits on it. Area/
// line fills use coverageBandAt (actual M_COVR coverage) instead.
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

// coverageBandAt returns the finest native band whose M_COVR (CATCOV=1) data
// coverage contains (lat,lon), or 0 if none — the best-available band at a point.
// Gates up-direction suppression of area/line fills: a coarse fill overzoomed
// above its band is hidden only where a strictly-finer cell genuinely carries data.
func (b *Baker) coverageBandAt(lat, lon float64) uint32 {
	var best uint32
	p := geo.LatLon{Lat: lat, Lon: lon}
	for i := range b.covMeta {
		cm := &b.covMeta[i]
		if cm.bandMax <= best || !cm.bb.Contains(p) {
			continue
		}
		if pointInRings(lon, lat, cm.rings) {
			best = cm.bandMax
		}
	}
	return best
}

// coverageScaleAt returns the FINEST (smallest) compilation-scale denominator among
// cells whose M_COVR coverage contains (lat,lon) AND that are displayed at zoom
// bandZ, or 0 if none. This drives per-CELL best-available suppression: a prim is
// hidden where a strictly-finer cell (smaller cscl) covers it — which works both
// across bands AND between cells of different scale that fall in the SAME band (the
// per-band coverageBandAt above can't distinguish those). bandZ-gated so a finer
// cell that isn't shown yet at this zoom doesn't punch a hole in the coarser one.
func (b *Baker) coverageScaleAt(lat, lon float64, bandZ uint32) uint32 {
	var best uint32 // 0 = none found yet; otherwise the finest (smallest) cscl
	p := geo.LatLon{Lat: lat, Lon: lon}
	for i := range b.covMeta {
		cm := &b.covMeta[i]
		if cm.cscl == 0 || cm.displayMin > bandZ {
			continue // unscaled, or this cell isn't drawn at this zoom
		}
		if best != 0 && cm.cscl >= best {
			continue // not finer than the best so far — skip the costly point test
		}
		if !cm.bb.Contains(p) || !pointInRings(lon, lat, cm.rings) {
			continue
		}
		best = cm.cscl
	}
	return best
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
func normPt(ll geo.LatLon) tile.UPoint {
	return tile.UPoint{X: tile.NormU(normX(ll.Lon)), Y: tile.NormU(normY(ll.Lat))}
}

func normPts(pts []geo.LatLon) []tile.UPoint {
	out := make([]tile.UPoint, len(pts))
	for i, p := range pts {
		out[i] = normPt(p)
	}
	return out
}

func normRings(rings [][]geo.LatLon) [][]tile.UPoint {
	out := make([][]tile.UPoint, len(rings))
	for i, r := range rings {
		out[i] = normPts(r)
	}
	return out
}

// projectNormRing affine-projects a normalized-world ring (32-bit fixed point)
// into tile-pixel space, writing into scratch (grown as needed) to avoid per-call
// allocation. The returned slice aliases scratch and is valid until the next call.
func projectNormRing(npts []tile.UPoint, proj tile.Projector, scratch []tile.FPoint) []tile.FPoint {
	if cap(scratch) < len(npts) {
		scratch = make([]tile.FPoint, len(npts))
	}
	scratch = scratch[:len(npts)]
	for i, n := range npts {
		scratch[i] = proj.ProjectNormU(n)
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

// contourValdco returns a DEPCNT depth contour's VALDCO (metres) so the client can
// label it in the chosen depth unit, or NaN for non-contours / contours with no
// value (so route() omits the tag and the client draws no label — not a "0").
func contourValdco(attrs map[string]interface{}, class string) float32 {
	if class != "DEPCNT" {
		return nan32f
	}
	// Only label contours deeper than chart datum. The 0 m contour is the
	// shoreline/drying line; labelling it "0" all along the coast is clutter (the
	// "0 by the shore" the mariner doesn't want), and a missing VALDCO is unknown,
	// not zero.
	if v, ok := floatAttr(attrs, "VALDCO"); ok && v > 0 {
		return float32(v)
	}
	return nan32f
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

// encodeS57Attrs serialises a feature's full S-57 attribute set into the compact
// JSON blob carried in the tile for the cursor-pick report (S-52 PresLib §10.8).
// Keys are S-57 acronyms; values are kept as raw strings (e.g. "3", list "1,3",
// "Fl.R.4s") so the client decodes names/enums/units against the catalogue — and
// numbers are formatted minimally, satisfying the no-padding rule (§10.8 rule 3).
// Returns "" for an attribute-free feature. json.Marshal sorts map keys, so the
// output is deterministic (bake reproducibility).
func encodeS57Attrs(attrs map[string]interface{}) string {
	if len(attrs) == 0 {
		return ""
	}
	out := make(map[string]string, len(attrs))
	for k, v := range attrs {
		if k == "DEPTHS" { // synthetic (SOUNDG Z values), not an S-57 attribute
			continue
		}
		switch t := v.(type) {
		case string:
			if s := strings.TrimSpace(t); s != "" {
				out[k] = s
			}
		case float64:
			out[k] = strconv.FormatFloat(t, 'g', -1, 64)
		case float32:
			out[k] = strconv.FormatFloat(float64(t), 'g', -1, 32)
		case int:
			out[k] = strconv.Itoa(t)
		case int64:
			out[k] = strconv.FormatInt(t, 10)
		default:
			out[k] = fmt.Sprint(t)
		}
	}
	if len(out) == 0 {
		return ""
	}
	buf, err := json.Marshal(out)
	if err != nil {
		return ""
	}
	return string(buf)
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
