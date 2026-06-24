package portrayal

import (
	"math"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/beetlebugorg/chartplotter/internal/engine/s101"
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
	cat, err := fc.Load(featureCataloguePath)
	if err != nil {
		return nil, err
	}
	draw, err := catalog.Load(portrayalCatalogDir)
	if err != nil {
		return nil, err
	}
	rulesDir := filepath.Join(portrayalCatalogDir, "Rules")
	// Validate the framework loads (fail fast); discard this engine.
	eng, err := s101.NewEngine(rulesDir, cat)
	if err != nil {
		return nil, err
	}
	eng.Close()
	return &S101Builder{rulesDir: rulesDir, fcCat: cat, Catalog: draw}, nil
}

// S101Builder is the S-101 replacement for the S-52 BuildFeature seam: it runs
// the S-101 portrayal rules (via the fc-backed Lua engine) for a batch of
// features, parses each emitted instruction stream, and lowers each draw onto
// the feature geometry to produce the same Primitive stream the baker consumes.
// (specs/s101-portrayal-backport.md — the cutover that replaces lookup+CSPs.)
type S101Builder struct {
	rulesDir string
	fcCat    *fc.Catalogue
	Catalog  *catalog.Catalog
}

// BuildBatch portrays a whole cell's features in ONE engine pass (one chunk
// compile, one portrayal context) and lowers each onto its geometry. A fresh
// Lua state is used and closed here so the per-cell caches don't accumulate.
// Returns featureID → build for every feature.
func (b *S101Builder) BuildBatch(features []*s57.Feature) (map[int64]FeatureBuild, error) {
	eng, err := s101.NewEngine(b.rulesDir, b.fcCat)
	if err != nil {
		return nil, err
	}
	defer eng.Close()

	batch := make([]s101.Feature, len(features))
	for i, f := range features {
		batch[i] = s101.Feature{
			ID:          strconv.FormatInt(f.ID(), 10),
			ObjectClass: f.ObjectClass(),
			Primitive:   primitiveName(f.Geometry().Type),
			Attributes:  stringAttrs(f.Attributes()),
		}
	}
	streams, err := eng.Portray(batch)
	if err != nil {
		return nil, err
	}
	out := make(map[int64]FeatureBuild, len(features))
	for _, f := range features {
		out[f.ID()] = b.lower(f, streams[strconv.FormatInt(f.ID(), 10)])
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

// lower turns one feature's emitted instruction stream into its FeatureBuild.
func (b *S101Builder) lower(f *s57.Feature, stream string) FeatureBuild {
	// Genuinely-unknown object class (no S-101 alias) → the magenta "unknown
	// object" mark (S-52 §10.1.1 parity).
	if strings.HasPrefix(stream, "UNMAPPED:") {
		return unknownObjectBuild(f)
	}
	// A rule error (or no stream) → suppress the feature rather than flood the
	// chart with placeholders. (Most current errors are line/area rules needing
	// the S-57 spatial topology the host doesn't model yet — a tracked gap.)
	if stream == "" || strings.HasPrefix(stream, "ERROR:") {
		return FeatureBuild{DisplayCategory: s52DisplayStandard}
	}

	g := geometryOf(f.Geometry())
	anchor, _ := textAnchor(g)
	sg := S101Geometry{Anchor: anchor, Points: g.line, Rings: g.area}

	cmds, _ := instructions.Reduce(instructions.ParseStream(stream))
	var prims []Primitive
	priority := 0
	for _, c := range cmds {
		if c.Priority > priority {
			priority = c.Priority
		}
		if p, ok := LowerS101(c, sg, b.Catalog); ok {
			prims = append(prims, p)
		}
	}
	return FeatureBuild{
		Primitives:      prims,
		DisplayPriority: priority,
		DisplayCategory: s52DisplayStandard, // TODO: derive from S-101 viewing group
	}
}

// s52DisplayStandard mirrors s52.DisplayStandard without importing s52 (the
// cutover is dropping that dependency). The baker treats this as "standard".
const s52DisplayStandard = 1

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
