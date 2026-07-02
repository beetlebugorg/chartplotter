package s57

import (
	"github.com/beetlebugorg/chartplotter/internal/s57/parser"
)

// Chart represents a parsed S-57 Electronic Navigational Chart.
//
// A chart carries the header metadata the chart library needs (cell name,
// edition, compilation scale, coverage bounds) plus the parsed features.
// The native libtile57 engine does all tiling and portrayal; this parse
// exists only for cell metadata extraction and feature access (e.g. the
// NMEA simulator's water mask).
type Chart struct {
	features []Feature
	bounds   Bounds // Chart coverage area (M_COVR preferred)

	datasetName      string
	edition          string
	updateNumber     string
	issueDate        string
	producingAgency  int
	compilationScale int32 // CSCL field from DSPM record
}

// Features returns all features in the chart.
func (c *Chart) Features() []Feature {
	return c.features
}

// Bounds returns the geographic coverage area of the chart.
//
// Derived from M_COVR features when present, otherwise the bounding box of
// all feature geometry.
func (c *Chart) Bounds() Bounds {
	return c.bounds
}

// DatasetName returns the chart's dataset name (cell identifier).
//
// Example: "US5MA22M", "GB5X01NE"
func (c *Chart) DatasetName() string { return c.datasetName }

// Edition returns the chart's edition number.
func (c *Chart) Edition() string { return c.edition }

// UpdateNumber returns the chart's update number.
//
// "0" indicates a base cell, higher numbers indicate applied updates.
func (c *Chart) UpdateNumber() string { return c.updateNumber }

// IssueDate returns the chart issue date in YYYYMMDD format.
func (c *Chart) IssueDate() string { return c.issueDate }

// ProducingAgency returns the producing agency code.
//
// Example: 550 = NOAA (United States)
func (c *Chart) ProducingAgency() int { return c.producingAgency }

// CompilationScale returns the compilation scale denominator of the chart.
//
// For example, a value of 50000 indicates the chart was compiled at 1:50,000
// scale. S-57 §7.3.2.1: CSCL field in DSPM record. Returns 0 if not specified.
func (c *Chart) CompilationScale() int32 { return c.compilationScale }

// Feature represents a navigational object from an S-57 chart.
type Feature struct {
	id          int64
	objectClass string
	geometry    Geometry
	attributes  map[string]interface{}
}

// NewFeature constructs a Feature. Useful for tests and for synthesizing
// features outside the parser; the parser builds them directly.
func NewFeature(id int64, objectClass string, geometry Geometry, attributes map[string]interface{}) Feature {
	return Feature{id: id, objectClass: objectClass, geometry: geometry, attributes: attributes}
}

// ID returns the unique feature identifier.
func (f *Feature) ID() int64 {
	return f.id
}

// ObjectClass returns the S-57 object class code.
//
// Common examples: "DEPCNT" (depth contour), "DEPARE" (depth area),
// "LIGHTS" (light), "M_COVR" (coverage meta object).
func (f *Feature) ObjectClass() string {
	return f.objectClass
}

// Geometry returns the spatial representation of the feature.
func (f *Feature) Geometry() Geometry {
	return f.geometry
}

// Attributes returns all feature attributes as a map.
//
// Attribute meanings are defined in the S-57 Object Catalogue, e.g.
// "DRVAL1" (minimum depth), "OBJNAM" (object name).
func (f *Feature) Attributes() map[string]interface{} {
	return f.attributes
}

// Attribute returns a specific attribute value by name.
//
// Returns the value and true if the attribute exists, or nil and false if not found.
func (f *Feature) Attribute(name string) (interface{}, bool) {
	val, ok := f.attributes[name]
	return val, ok
}

// Ring represents a polygon ring (exterior boundary or interior hole).
//
// S-57 §2.2.8: USAG subfield indicates ring type per IHO S-57 specification.
type Ring struct {
	// Usage indicates the ring type:
	//   1 = Exterior boundary (outer ring)
	//   2 = Interior boundary (hole)
	//   3 = Exterior boundary truncated at data limit
	Usage int

	// Coordinates contains [longitude, latitude] pairs forming the ring.
	// First and last coordinates are identical (closed ring).
	Coordinates [][]float64
}

// Geometry represents the spatial representation of a feature.
//
// Coordinates follow GeoJSON convention: [longitude, latitude] pairs.
// All coordinates are in WGS-84 decimal degrees.
type Geometry struct {
	// Type indicates the geometry type (Point, LineString, or Polygon).
	Type GeometryType

	// Coordinates contains [longitude, latitude] pairs.
	//
	// For Point: Single coordinate pair
	// For LineString: Array of coordinate pairs forming a line
	// For Polygon: the flat concatenation of all ring coordinates (use Rings
	// for the ring structure).
	Coordinates [][]float64

	// Rings contains polygon ring data with usage indicators.
	// Only populated for Polygon geometry type.
	// Ring(s) with Usage=1 or 3 are exterior boundaries.
	// Rings with Usage=2 are interior boundaries (holes).
	Rings []Ring
}

// GeometryType represents the type of geometry.
type GeometryType int

const (
	// GeometryTypePoint represents a single point location.
	GeometryTypePoint GeometryType = iota

	// GeometryTypeLineString represents a line composed of connected points.
	GeometryTypeLineString

	// GeometryTypePolygon represents a closed polygon area.
	GeometryTypePolygon
)

// String returns the string representation of the geometry type.
func (g GeometryType) String() string {
	switch g {
	case GeometryTypePoint:
		return "Point"
	case GeometryTypeLineString:
		return "LineString"
	case GeometryTypePolygon:
		return "Polygon"
	default:
		return "Unknown"
	}
}

// convertChart converts internal chart to public API chart
func convertChart(internal *parser.Chart) *Chart {
	features := make([]Feature, len(internal.Features))
	for i, f := range internal.Features {
		// Convert internal rings to public Ring type
		rings := make([]Ring, len(f.Geometry.Rings))
		for j, internalRing := range f.Geometry.Rings {
			rings[j] = Ring{
				Usage:       internalRing.Usage,
				Coordinates: internalRing.Coordinates,
			}
		}

		features[i] = Feature{
			id:          f.ID,
			objectClass: f.ObjectClass,
			geometry: Geometry{
				Type:        GeometryType(f.Geometry.Type),
				Coordinates: f.Geometry.Coordinates,
				Rings:       rings,
			},
			attributes: f.Attributes,
		}
	}

	chart := &Chart{
		features:         features,
		datasetName:      internal.DatasetName(),
		edition:          internal.Edition(),
		updateNumber:     internal.UpdateNumber(),
		issueDate:        internal.IssueDate(),
		producingAgency:  internal.ProducingAgency(),
		compilationScale: internal.CompilationScale(),
	}
	chart.bounds = computeBounds(features)

	return chart
}

// computeBounds derives the chart coverage box, preferring M_COVR (Meta
// Coverage) features — they define the official coverage area — and falling
// back to the union of all feature geometry when a cell carries none.
func computeBounds(features []Feature) Bounds {
	var bounds *Bounds
	expand := func(fb Bounds) {
		if bounds == nil {
			b := fb
			bounds = &b
			return
		}
		if fb.MinLon < bounds.MinLon {
			bounds.MinLon = fb.MinLon
		}
		if fb.MaxLon > bounds.MaxLon {
			bounds.MaxLon = fb.MaxLon
		}
		if fb.MinLat < bounds.MinLat {
			bounds.MinLat = fb.MinLat
		}
		if fb.MaxLat > bounds.MaxLat {
			bounds.MaxLat = fb.MaxLat
		}
	}

	for i := range features {
		if features[i].objectClass == "M_COVR" {
			expand(featureBounds(features[i]))
		}
	}
	if bounds == nil {
		for i := range features {
			expand(featureBounds(features[i]))
		}
	}
	if bounds == nil {
		return Bounds{}
	}
	return *bounds
}
