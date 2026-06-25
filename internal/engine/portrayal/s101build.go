package portrayal

import (
	"io/fs"
	"math"
	"os"
	"strconv"
	"strings"

	"github.com/beetlebugorg/chartplotter/internal/engine/s101"
	"github.com/beetlebugorg/chartplotter/pkg/geo"
	"github.com/beetlebugorg/chartplotter/pkg/s100/catalog"
	"github.com/beetlebugorg/chartplotter/pkg/s100/fc"
	"github.com/beetlebugorg/chartplotter/pkg/s100/instructions"
	"github.com/beetlebugorg/chartplotter/pkg/s57"
)

// NewS101Builder assembles a builder from an S-101 PortrayalCatalog directory
// and a FeatureCatalogue.xml path: it loads the feature catalogue (the S-57↔
// S-101 bridge + Lua introspection) and the drawing catalogue (line styles /
// area fills / colours). The Lua engine is created fresh per BuildBatch so its
// per-cell caches (featureCache etc., which are file-local in the catalogue and
// can't be cleared) are freed each cell — otherwise the shared Lua state grows
// without bound across a bake.
func NewS101Builder(portrayalCatalogDir, featureCataloguePath string) (*S101Builder, error) {
	fcBytes, err := os.ReadFile(featureCataloguePath)
	if err != nil {
		return nil, err
	}
	return newS101Builder(os.DirFS(portrayalCatalogDir), fcBytes)
}

// NewS101BuilderFS assembles a builder from an in-memory PortrayalCatalog FS (e.g.
// the build-time embedded catalogue, internal/engine/s101catalog) and the
// FeatureCatalogue.xml bytes — same builder, no on-disk catalogue directory.
func NewS101BuilderFS(catalogFS fs.FS, featureCatalogueXML []byte) (*S101Builder, error) {
	return newS101Builder(catalogFS, featureCatalogueXML)
}

func newS101Builder(catalogFS fs.FS, fcBytes []byte) (*S101Builder, error) {
	cat, err := fc.LoadBytes(fcBytes)
	if err != nil {
		return nil, err
	}
	draw, err := catalog.LoadFS(catalogFS)
	if err != nil {
		return nil, err
	}
	rulesFS, err := fs.Sub(catalogFS, "Rules")
	if err != nil {
		return nil, err
	}
	// Validate the framework loads (fail fast); discard this engine.
	eng, err := s101.NewEngineFS(rulesFS, cat)
	if err != nil {
		return nil, err
	}
	eng.Close()
	return &S101Builder{rulesFS: rulesFS, fcCat: cat, Catalog: draw}, nil
}

// S101Builder is the feature-build seam: it runs the S-101 portrayal rules (via
// the fc-backed Lua engine) for a batch of features, parses each emitted
// instruction stream, and emits a primitive for each draw onto the feature
// geometry to produce the Primitive stream the baker consumes.
type S101Builder struct {
	rulesFS fs.FS
	fcCat   *fc.Catalogue
	Catalog *catalog.Catalog
}

// BuildBatch portrays a whole cell's features in ONE engine pass (one chunk
// compile, one portrayal context) and emits primitives for each onto its
// geometry. A fresh
// Lua state is used and closed here so the per-cell caches don't accumulate.
// Returns featureID → build for every feature.
func (b *S101Builder) BuildBatch(features []*s57.Feature) (map[int64]FeatureBuild, error) {
	return b.BuildBatchOverrides(features, nil)
}

// BuildBatchOverrides is BuildBatch with S-101 context-parameter overrides (e.g.
// {"PlainBoundaries":"true"} or {"SimplifiedSymbols":"true"}), so the baker can
// portray the plain-boundary / simplified-symbol display variants.
func (b *S101Builder) BuildBatchOverrides(features []*s57.Feature, overrides map[string]string) (map[int64]FeatureBuild, error) {
	eng, err := s101.NewEngineFS(b.rulesFS, b.fcCat)
	if err != nil {
		return nil, err
	}
	eng.SetContextOverrides(overrides)
	defer eng.Close()

	depthIdx := BuildDepthIndex(features) // underlying DEPARE/DRGARE for danger depths
	topmarkIdx := buildTopmarkIndex(features)
	batch := make([]s101.Feature, 0, len(features))
	for _, f := range features {
		g := f.Geometry()
		// Skip non-spatial collection/relationship objects (C_AGGR, C_ASSO) — they
		// group other features and carry no geometry, so there's nothing to
		// portray and the rule would error on the missing primitive.
		if len(g.Coordinates) == 0 && len(g.Rings) == 0 {
			continue
		}
		// TOPMAR is folded into its co-located buoy/beacon as the topmark complex
		// attribute (below); the standalone feature has no S-101 class, so skip it
		// rather than portray it as a magenta unknown mark.
		if f.ObjectClass() == "TOPMAR" {
			continue
		}
		prim := primitiveName(g.Type)
		var points [][3]float64
		// Point features carry their vertices (lon,lat,depth) so the host can
		// resolve a REAL point spatial (HostGetSpatial '#P'/'#M'). SOUNDG is a
		// multipoint (the Sounding rule iterates each point's depth); other point
		// features are a single point. This is required even when the geometry is
		// otherwise attached when the Go side emits primitives: a rule that reads feature.Point /
		// feature.Spatial would otherwise hit the framework's GetSpatial infinite
		// recursion (it reads self['Spatial'] right after assigning it nil, which
		// re-fires __index) — the cause of the OBSTRN/WRECKS stack overflows.
		if f.Geometry().Type == s57.GeometryTypePoint {
			points = soundingPoints(f.Geometry())
			if f.ObjectClass() == "SOUNDG" {
				prim = "MultiPoint"
			}
		}
		var topmark map[string]string
		if isTopmarkParent(f.ObjectClass()) {
			if key, ok := pointLocKey(g); ok {
				topmark = topmarkIdx[key]
			}
		}
		batch = append(batch, s101.Feature{
			ID:          strconv.FormatInt(f.ID(), 10),
			ObjectClass: f.ObjectClass(),
			Primitive:   prim,
			Attributes:  stringAttrs(f.Attributes()),
			Derived:     DerivedAttrs(f, depthIdx),
			Points:      points,
			Topmark:     topmark,
		})
	}
	streams, err := eng.Portray(batch)
	if err != nil {
		return nil, err
	}
	out := make(map[int64]FeatureBuild, len(features))
	for _, f := range features {
		out[f.ID()] = b.buildFeature(f, streams[strconv.FormatInt(f.ID(), 10)])
	}
	return out, nil
}

// Build expands one S-57 feature (convenience wrapper over BuildBatch; the bake
// path uses BuildBatch per cell). ok is false only on engine failure.
func (b *S101Builder) Build(f *s57.Feature) (FeatureBuild, bool) {
	m, err := b.BuildBatch([]*s57.Feature{f})
	if err != nil {
		return FeatureBuild{}, false
	}
	return m[f.ID()], true
}

// buildFeature turns one feature's emitted instruction stream into its FeatureBuild.
func (b *S101Builder) buildFeature(f *s57.Feature, stream string) FeatureBuild {
	// Genuinely-unknown object class (no S-101 alias) → the magenta "unknown
	// object" mark (S-52 §10.1.1 parity).
	if strings.HasPrefix(stream, "UNMAPPED:") {
		return unknownObjectBuild(f)
	}
	// A rule error (or no stream) → suppress the feature rather than flood the
	// chart with placeholders. (Most current errors are line/area rules needing
	// the S-57 spatial topology the host doesn't model yet — a tracked gap.)
	if stream == "" || strings.HasPrefix(stream, "ERROR:") {
		return FeatureBuild{DisplayCategory: displayStandard}
	}

	g := geometryOf(f.Geometry())
	anchor, _ := textAnchor(g)
	sg := S101Geometry{Anchor: anchor, Rings: g.area, Lines: strokeRunsFor(g)}

	cmds, _ := instructions.Reduce(instructions.ParseStream(stream))
	var prims []Primitive
	priority := 0
	cat := 0 // unset; resolved from the viewing groups the rule emits
	var dateStart, dateEnd, timeValid string
	for _, c := range cmds {
		if c.Priority > priority {
			priority = c.Priority
		}
		// Date dependency is feature-level (one Date:/TimeValid: pair the rule emits
		// up front, carried onto every draw): capture it once for the FeatureBuild.
		if timeValid == "" && (c.TimeValid != "" || c.DateStart != "" || c.DateEnd != "") {
			dateStart, dateEnd, timeValid = c.DateStart, c.DateEnd, c.TimeValid
		}
		// The shallow-water pattern (SEABED01 emits AreaFillReference:DIAMOND1 in
		// viewing group 90000 on every depth area shallower than the safety
		// contour) is a MARINER SELECTION, not a fixed portrayal. The client owns
		// it: a dedicated shallow-pattern layer applies DIAMOND1 over the depth
		// areas live from the baked drval1 + the mariner's safety contour, toggled
		// by mariner.shallowPattern. Baking it here too made it (a) always visible
		// and (b) double up beside the client layer when the toggle was on — so
		// drop it and let the client's toggle-aware, live-safety-contour layer win.
		if c.Op == instructions.OpAreaFill && c.Reference == "DIAMOND1" {
			continue
		}
		// Display category is a per-viewing-group property (S-101 partitions
		// viewing groups into Base/Standard/Other/quality bands). A feature can
		// emit draws across bands; take the MOST-VISIBLE (lowest enum) so a
		// safety-critical base-display draw is never hidden because the feature
		// also carries a standard/other label.
		if dc := displayCategoryForViewingGroup(c.ViewingGroup); dc != 0 && (cat == 0 || dc < cat) {
			cat = dc
		}
		prims = append(prims, emitPrimitives(c, sg, b.Catalog)...)
	}
	// Soundings: tag each emitted glyph with its depth so the baker emits the
	// numeric depth + S/G palette variants. Without this the client's depth-unit
	// conversion (synthSounding) and SNDFRM04 safety-depth split fall back to the
	// static metric glyphs and never react to those settings.
	if f.ObjectClass() == "SOUNDG" {
		attachSoundingDepths(prims, soundingPoints(f.Geometry()))
	}
	// Sector / directional lights: the rule constructs the legs + arc as screen-space
	// AugmentedFigure elements (emitted above from the AugmentedRay / ArcByRadius
	// instructions). Tag each leg with the light's nominal range (VALNMR) so the
	// baker can also emit the "full light lines" leg variant for the client's live
	// toggle (S-52 LIGHTS06 note 1).
	if f.ObjectClass() == "LIGHTS" {
		if vnr, ok := floatAttr(f.Attributes(), "VALNMR"); ok && vnr > 0 {
			for i := range prims {
				if fig, ok := prims[i].(AugmentedFigure); ok && fig.Ray {
					fig.FullLengthNM = vnr
					prims[i] = fig
				}
			}
		}
	}
	// Centred-area symbol placement (S-52 PresLib §8.5.1): the pivot point is the
	// area's representative point (sg.Anchor), where the FIRST/primary centred symbol
	// sits "so it is evident which area the symbol applies to"; ADDITIONAL symbols
	// keep their catalogue pivot offset to fan out and "prevent overwriting" (the
	// spec's "a centred traffic arrow and an offset entry-restricted symbol"). The
	// "…RES" symbols are authored with a corner pivot for that fan-out, which throws
	// the PRIMARY ~100px off its area; centring the first one restores §8.5.1.
	if g.kind == geomArea {
		for i, p := range prims {
			if sc, ok := p.(SymbolCall); ok && sc.Anchor == sg.Anchor {
				sc.CentreOnArea = true
				prims[i] = sc
				break // only the primary sits on the point; the rest fan out
			}
		}
	}
	if cat == 0 {
		cat = displayStandard // no display-category band emitted (e.g. text-only)
	}
	return FeatureBuild{
		Primitives:      prims,
		DisplayPriority: priority,
		DisplayCategory: cat,
		DateStart:       dateStart,
		DateEnd:         dateEnd,
		TimeValid:       timeValid,
	}
}

// strokeRunsFor returns the drawable polylines an S-101 line draw strokes for a
// feature, honoring S-52 §8.6.2 masking exactly as the S-52 walker does: a line
// feature's drawable parts, or — for an area — its drawable boundary, each with
// coastline-coincident / MASK=1 / data-limit edges already removed by the
// parser. A non-nil (even empty) lineParts/boundary means masking was computed,
// so it is used verbatim (empty ⇒ stroke nothing); nil means it wasn't computed
// (fallback geometry) → stroke the full line / rings. Area FILLS keep the
// complete rings (g.area) regardless.
func strokeRunsFor(g geom) [][]geo.LatLon {
	switch g.kind {
	case geomLine:
		if g.lineParts != nil {
			return g.lineParts
		}
		if len(g.line) >= 2 {
			return [][]geo.LatLon{g.line}
		}
	case geomArea:
		if g.boundary != nil {
			return g.boundary
		}
		return g.area
	}
	return nil
}

// Display-category enum values (DisplayBase/Standard/Other), which the baker's
// catRank switches on. Defined locally so the S-101 builder needn't import
// pkg/s52.
const (
	displayBase     = 6
	displayStandard = 7
	displayOther    = 8
)

// displayCategoryForViewingGroup maps an S-101 viewing-group id to its S-52
// display category. The portrayal catalogue partitions viewing groups into
// bands by leading digit (portrayal_catalogue.xml <viewingGroups>): 1xxxx =
// Display Base, 2xxxx = Display Standard, 3xxxx = Display Other, 9xxxx =
// optional quality/CATZOC overlays (Other, hidden by default). The 5xxxx and
// sub-10000 ids are independent text-group selectors (not display-category
// bands). Returns 0 for an id that carries no display category.
func displayCategoryForViewingGroup(vg int) int {
	switch vg / 10000 {
	case 1:
		return displayBase
	case 2:
		return displayStandard
	case 3, 9:
		return displayOther
	default:
		return 0
	}
}

// soundingPoints extracts a SOUNDG multipoint's vertices as (lon, lat, depth).
// S-57 encodes soundings as 3-D points [lon, lat, depth]; a point missing its Z
// is given depth 0 so the rule still places a "0" sounding rather than dropping.
func soundingPoints(g s57.Geometry) [][3]float64 {
	pts := make([][3]float64, 0, len(g.Coordinates))
	for _, c := range g.Coordinates {
		if len(c) < 2 {
			continue
		}
		var z float64
		if len(c) >= 3 {
			z = c[2]
		}
		pts = append(pts, [3]float64{c[0], c[1], z})
	}
	return pts
}

// attachSoundingDepths sets SoundingDepthM on each emitted sounding glyph from the
// SOUNDG multipoint, matching by anchor (the glyphs are placed at their sounding's
// lon/lat via AugmentedPoint). The depth then reaches the baker, which emits the
// numeric depth + S/G palette variants the client needs for live depth-unit
// conversion and the SNDFRM04 safety-depth split.
func attachSoundingDepths(prims []Primitive, pts [][3]float64) {
	if len(pts) == 0 {
		return
	}
	type key struct{ x, y int64 }
	q := func(v float64) int64 { return int64(math.Round(v * 1e7)) } // ~1cm
	depthAt := make(map[key]float64, len(pts))
	for _, p := range pts {
		depthAt[key{q(p[0]), q(p[1])}] = p[2]
	}
	for i := range prims {
		sc, ok := prims[i].(SymbolCall)
		if !ok {
			continue
		}
		if d, ok := depthAt[key{q(sc.Anchor.Lon), q(sc.Anchor.Lat)}]; ok {
			sc.SoundingDepthM = float32(d)
			prims[i] = sc
		}
	}
}

func primitiveName(t s57.GeometryType) string {
	switch t {
	case s57.GeometryTypeLineString:
		return "Curve"
	case s57.GeometryTypePolygon:
		return "Surface"
	default:
		return "Point"
	}
}

// stringAttrs encodes S-57 attribute values as the strings ConvertEncodedValue
// expects (enumeration/integer → digits, boolean → "1"/"0", text → as-is).
func stringAttrs(attrs map[string]interface{}) map[string]string {
	out := make(map[string]string, len(attrs))
	for k, v := range attrs {
		if s, ok := encodeAttr(v); ok {
			out[k] = s
		}
	}
	return out
}

func encodeAttr(v interface{}) (string, bool) {
	switch t := v.(type) {
	case nil:
		return "", false
	case string:
		return t, true
	case bool:
		if t {
			return "1", true
		}
		return "0", true
	case int:
		return strconv.Itoa(t), true
	case int64:
		return strconv.FormatInt(t, 10), true
	case float64:
		if t == math.Trunc(t) && !math.IsInf(t, 0) {
			return strconv.FormatInt(int64(t), 10), true
		}
		return strconv.FormatFloat(t, 'g', -1, 64), true
	case float32:
		return encodeAttr(float64(t))
	default:
		return "", false
	}
}
