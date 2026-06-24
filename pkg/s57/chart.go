package s57

import (
	"github.com/beetlebugorg/chartplotter/internal/s57/parser"
	"github.com/dhconnelly/rtreego"
)

// Chart represents a parsed S-57 Electronic Navigational Chart.
//
// A chart contains metadata (cell name, edition, dates, etc.) and a collection
// of navigational features (depth contours, buoys, lights, hazards, etc.).
//
// Access metadata via methods like DatasetName(), Edition(), IssueDate().
// Access features via Features(), FeaturesInBounds(), or FeatureCount().
//
// All fields are private to maintain encapsulation.
type Chart struct {
	features     []Feature     // All features
	spatialIndex *spatialIndex // Fast spatial queries
	bounds       Bounds        // Chart coverage area

	datasetName        string
	edition            string
	updateNumber       string
	updateDate         string
	issueDate          string
	s57Edition         string
	producingAgency    int
	comment            string
	exchangePurpose    string
	productSpec        string
	applicationProfile string
	usageBand          UsageBand

	// Coordinate system metadata (S-57 §7.3.2, 31Main.pdf p66)
	coordinateUnits  CoordinateUnits // COUN field from DSPM record
	depthUnits       DepthUnits      // DUNI field from DSPM record
	horizontalDatum  int             // HDAT field from DSPM record
	compilationScale int32           // CSCL field from DSPM record

	warnings []ConformanceWarning // Non-fatal S-57/ISO-8211 spec deviations
}

// ConformanceWarning is one non-fatal S-57 / ISO-8211 spec deviation detected
// while parsing. See Chart.Warnings.
type ConformanceWarning struct {
	Clause  string // governing spec clause
	Code    string // short stable identifier
	Message string // human-readable detail
	Count   int    // times this code was hit during the parse
}

// Warnings returns the non-fatal spec-conformance deviations detected while
// parsing this chart. Empty when the cell is fully conformant. Use
// ParseOptions.ValidateConformance to turn these into a parse error instead.
func (c *Chart) Warnings() []ConformanceWarning {
	return c.warnings
}

// CoordinateUnits indicates how coordinates are encoded in the chart.
//
// S-57 §7.3.2.1 (31Main.pdf p66): COUN field in DSPM record defines coordinate units.
// Reference: S-57 Part 3 Table 3.2
type CoordinateUnits int

const (
	// CoordinateUnitsLatLon indicates coordinates are in latitude/longitude (WGS-84).
	// This is the most common format for ENC charts.
	// Coordinates are decimal degrees, typically scaled by 10^7.
	CoordinateUnitsLatLon CoordinateUnits = 1

	// CoordinateUnitsEastNorth indicates coordinates are in projected Easting/Northing.
	// Less common; requires DSPR record to specify projection parameters.
	CoordinateUnitsEastNorth CoordinateUnits = 2

	// CoordinateUnitsUnknown indicates coordinate units are not specified.
	// Treat as lat/lon by default (S-57 default assumption).
	CoordinateUnitsUnknown CoordinateUnits = 0
)

// String returns a human-readable name for the coordinate units.
func (c CoordinateUnits) String() string {
	switch c {
	case CoordinateUnitsLatLon:
		return "Latitude/Longitude (WGS-84)"
	case CoordinateUnitsEastNorth:
		return "Easting/Northing (Projected)"
	default:
		return "Unknown"
	}
}

// DepthUnits indicates the units used for depth measurements in the chart.
//
// S-57 §7.3.2.1 (31Main.pdf p66): DUNI field in DSPM record defines depth units.
type DepthUnits int

const (
	// DepthUnitsMeters indicates depths are in meters.
	// This is the standard S-57 depth unit.
	DepthUnitsMeters DepthUnits = 1

	// DepthUnitsFathomsAndFeet indicates depths are in fathoms and feet.
	// Whole fathoms for depths >= 11 fathoms, fathoms and feet for shallower.
	DepthUnitsFathomsAndFeet DepthUnits = 2

	// DepthUnitsFeet indicates depths are in feet.
	// Common for US NOAA charts.
	DepthUnitsFeet DepthUnits = 3

	// DepthUnitsFathomsAndFractions indicates depths are in fathoms and fractions.
	DepthUnitsFathomsAndFractions DepthUnits = 4

	// DepthUnitsUnknown indicates depth units are not specified.
	// Assume meters (S-57 default).
	DepthUnitsUnknown DepthUnits = 0
)

// String returns a human-readable name for the depth units.
func (d DepthUnits) String() string {
	switch d {
	case DepthUnitsMeters:
		return "Meters"
	case DepthUnitsFathomsAndFeet:
		return "Fathoms and Feet"
	case DepthUnitsFeet:
		return "Feet"
	case DepthUnitsFathomsAndFractions:
		return "Fathoms and Fractions"
	default:
		return "Unknown (assuming Meters)"
	}
}

// UsageBand defines the ENC usage band (navigational purpose) of the chart.
//
// ENC cells are organized by usage band, which determines the level of detail
// and appropriate display scale. Applications should load the appropriate band
// based on the current zoom level.
//
// Reference: S-57 Part 3 §7.3.1.1 (31Main.pdf p64, INTU field) and S-52 Section 3.4
type UsageBand int

const (
	// UsageBandUnknown indicates the band is not specified.
	UsageBandUnknown UsageBand = 0

	// UsageBandOverview - For overview navigation (≥ 1:1,500,000).
	// Provides general context and route planning.
	UsageBandOverview UsageBand = 1

	// UsageBandGeneral - For general navigation (1:350,000 - 1:1,500,000).
	// Used for open ocean and offshore navigation.
	UsageBandGeneral UsageBand = 2

	// UsageBandCoastal - For coastal navigation (1:90,000 - 1:350,000).
	// Used for navigation along coastlines and approaching ports.
	UsageBandCoastal UsageBand = 3

	// UsageBandApproach - For approach navigation (1:22,000 - 1:90,000).
	// Used when approaching ports, harbours, and pilot stations.
	UsageBandApproach UsageBand = 4

	// UsageBandHarbour - For harbour navigation (1:4,000 - 1:22,000).
	// Used for navigation within harbours and restricted waters.
	UsageBandHarbour UsageBand = 5

	// UsageBandBerthing - For berthing (≤ 1:4,000).
	// Used for final approach to berth and detailed harbour navigation.
	UsageBandBerthing UsageBand = 6
)

// String returns the human-readable name of the usage band.
func (ub UsageBand) String() string {
	switch ub {
	case UsageBandOverview:
		return "Overview"
	case UsageBandGeneral:
		return "General"
	case UsageBandCoastal:
		return "Coastal"
	case UsageBandApproach:
		return "Approach"
	case UsageBandHarbour:
		return "Harbour"
	case UsageBandBerthing:
		return "Berthing"
	default:
		return "Unknown"
	}
}

// ScaleRange returns the recommended scale range for this usage band.
//
// Returns (minScale, maxScale) where scales are denominators (e.g., 1:90000 returns 90000).
// For overview and berthing (open-ended ranges), one value may be 0.
func (ub UsageBand) ScaleRange() (min, max int) {
	switch ub {
	case UsageBandOverview:
		return 1500000, 0 // 1:1,500,000 and smaller (larger denominators)
	case UsageBandGeneral:
		return 350000, 1500000
	case UsageBandCoastal:
		return 90000, 350000
	case UsageBandApproach:
		return 22000, 90000
	case UsageBandHarbour:
		return 4000, 22000
	case UsageBandBerthing:
		return 0, 4000 // 1:4,000 and larger (smaller denominators)
	default:
		return 0, 0
	}
}

// spatialIndex provides O(log n) spatial queries using R-tree.
// Dramatically faster than linear O(n) scan for large charts.
type spatialIndex struct {
	rtree *rtreego.Rtree // R-tree for fast spatial queries
}

// indexedFeature wraps a feature for R-tree storage.
type indexedFeature struct {
	feature Feature
	bounds  Bounds
}

// Bounds implements rtreego.Spatial interface.
func (f *indexedFeature) Bounds() rtreego.Rect {
	point := rtreego.Point{f.bounds.MinLon, f.bounds.MinLat}

	// Calculate lengths, ensuring minimum size for point features
	// R-tree requires non-zero dimensions
	lonLength := f.bounds.MaxLon - f.bounds.MinLon
	latLength := f.bounds.MaxLat - f.bounds.MinLat

	// For point features (zero-area), use small epsilon (~11 meters at equator)
	const epsilon = 0.0001
	if lonLength < epsilon {
		lonLength = epsilon
	}
	if latLength < epsilon {
		latLength = epsilon
	}

	lengths := []float64{lonLength, latLength}
	rect, _ := rtreego.NewRect(point, lengths)
	return rect
}

// Features returns all features in the chart.
//
// Features include depth contours, buoys, lights, hazards, restricted areas,
// and all other navigational objects defined in the S-57 Object Catalogue.
//
// Each feature contains ObjectClass, Attributes, and Geometry needed for
// S-52 presentation library symbology lookup and rendering.
func (c *Chart) Features() []Feature {
	return c.features
}

// FeatureCount returns the number of features in the chart.
func (c *Chart) FeatureCount() int {
	return len(c.features)
}

// Bounds returns the geographic coverage area of the chart.
//
// This represents the minimum bounding box containing all features.
func (c *Chart) Bounds() Bounds {
	return c.bounds
}

// FeaturesInBounds returns all features that intersect the given bounding box.
//
// This is the primary method for viewport-based rendering. Only features that
// could be visible in the viewport are returned.
//
// Example:
//
//	viewport := s57.Bounds{
//	    MinLon: -71.5, MaxLon: -71.0,
//	    MinLat: 42.0, MaxLat: 42.5,
//	}
//	visibleFeatures := chart.FeaturesInBounds(viewport)
//	for _, feature := range visibleFeatures {
//	    render(feature)
//	}
func (c *Chart) FeaturesInBounds(bounds Bounds) []Feature {
	if c.spatialIndex == nil || c.spatialIndex.rtree == nil {
		// No spatial index, fallback to linear search
		return c.featuresInBoundsLinear(bounds)
	}

	// Query R-tree: O(log n) instead of O(n)
	point := rtreego.Point{bounds.MinLon, bounds.MinLat}
	lengths := []float64{
		bounds.MaxLon - bounds.MinLon,
		bounds.MaxLat - bounds.MinLat,
	}
	queryRect, _ := rtreego.NewRect(point, lengths)

	// Search R-tree for intersecting features
	spatials := c.spatialIndex.rtree.SearchIntersect(queryRect)

	// Extract features from indexed wrappers
	result := make([]Feature, 0, len(spatials))
	for _, spatial := range spatials {
		indexed := spatial.(*indexedFeature)
		result = append(result, indexed.feature)
	}

	return result
}

// featuresInBoundsLinear performs linear search when no spatial index exists.
func (c *Chart) featuresInBoundsLinear(bounds Bounds) []Feature {
	result := make([]Feature, 0, len(c.features)/10)
	for _, feature := range c.features {
		fb := featureBounds(feature)
		if bounds.Intersects(fb) {
			result = append(result, feature)
		}
	}
	return result
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

// UpdateDate returns the update application date in YYYYMMDD format.
//
// All updates dated on or before this date must be applied for current data.
func (c *Chart) UpdateDate() string { return c.updateDate }

// IssueDate returns the chart issue date in YYYYMMDD format.
//
// This is when the dataset was released by the producing agency.
func (c *Chart) IssueDate() string { return c.issueDate }

// S57Edition returns the S-57 standard edition used.
//
// Example: "03.1" for S-57 Edition 3.1
func (c *Chart) S57Edition() string { return c.s57Edition }

// ProducingAgency returns the producing agency code.
//
// Example: 550 = NOAA (United States)
//
// Full agency list available in IHO S-57 Appendix A.
func (c *Chart) ProducingAgency() int { return c.producingAgency }

// Comment returns the metadata comment field.
func (c *Chart) Comment() string { return c.comment }

// ExchangePurpose returns human-readable exchange purpose.
//
// Returns "New" for new datasets or "Revision" for updates.
func (c *Chart) ExchangePurpose() string { return c.exchangePurpose }

// ProductSpecification returns human-readable product specification.
//
// Typically "ENC" for Electronic Navigational Charts.
func (c *Chart) ProductSpecification() string { return c.productSpec }

// ApplicationProfile returns human-readable application profile.
//
// Examples: "EN (ENC New)", "ER (ENC Revision)"
func (c *Chart) ApplicationProfile() string { return c.applicationProfile }

// UsageBand returns the ENC usage band of this chart.
//
// This indicates the intended usage and appropriate scale range:
//   - Overview: ≥1:1,500,000 (route planning)
//   - General: 1:350,000-1:1,500,000 (open ocean)
//   - Coastal: 1:90,000-1:350,000 (coastal navigation)
//   - Approach: 1:22,000-1:90,000 (approaching ports)
//   - Harbour: 1:4,000-1:22,000 (harbour navigation)
//   - Berthing: ≤1:4,000 (final approach)
//
// Applications should load the appropriate band based on zoom level.
func (c *Chart) UsageBand() UsageBand { return c.usageBand }

// CoordinateUnits returns the coordinate system used in the chart.
//
// Most ENC charts use CoordinateUnitsLatLon (lat/lon in WGS-84).
// Some charts may use CoordinateUnitsEastNorth for projected coordinates.
//
// S-57 §7.3.2.1 (31Main.pdf p66): COUN field in DSPM record.
func (c *Chart) CoordinateUnits() CoordinateUnits { return c.coordinateUnits }

// HorizontalDatum returns the horizontal geodetic datum code.
//
// Common values:
//   - 2: WGS-84 (most common for modern ENCs)
//   - Other values defined in S-57 Part 3 Table 3.1
//
// S-57 §7.3.2.1 (31Main.pdf p66): HDAT field in DSPM record.
func (c *Chart) HorizontalDatum() int { return c.horizontalDatum }

// CompilationScale returns the compilation scale denominator of the chart.
//
// For example, a value of 50000 indicates the chart was compiled at 1:50,000 scale.
// This helps determine appropriate display scales and SCAMIN filtering.
//
// S-57 §7.3.2.1 (31Main.pdf p66): CSCL field in DSPM record.
// Returns 0 if not specified.
func (c *Chart) CompilationScale() int32 { return c.compilationScale }

// DepthUnits returns the depth units used in this chart (DUNI from DSPM).
func (c *Chart) DepthUnits() DepthUnits { return c.depthUnits }

// Feature represents a navigational object from an S-57 chart.
//
// Features include depth contours, buoys, lights, hazards, restricted areas,
// and all other objects defined in the S-57 Object Catalogue.
//
// Access feature data via methods:
//   - ID() returns the unique identifier
//   - ObjectClass() returns the S-57 object class (e.g., "DEPCNT", "LIGHTS")
//   - Geometry() returns the spatial representation
//   - Attributes() returns all attributes
//   - Attribute(name) returns a specific attribute value
type Feature struct {
	id          int64
	objectClass string
	geometry    Geometry
	attributes  map[string]interface{}
}

// ID returns the unique feature identifier.
func (f *Feature) ID() int64 {
	return f.id
}

// ObjectClass returns the S-57 object class code.
//
// Common examples:
//   - "DEPCNT": Depth contour
//   - "DEPARE": Depth area
//   - "BOYCAR": Buoy, cardinal
//   - "LIGHTS": Light
//   - "OBSTRN": Obstruction
//   - "RESARE": Restricted area
func (f *Feature) ObjectClass() string {
	return f.objectClass
}

// Geometry returns the spatial representation of the feature.
func (f *Feature) Geometry() Geometry {
	return f.geometry
}

// Attributes returns all feature attributes as a map.
//
// Common attributes:
//   - "DRVAL1": Depth range value 1 (minimum depth)
//   - "DRVAL2": Depth range value 2 (maximum depth)
//   - "COLOUR": Color code
//   - "OBJNAM": Object name
//
// Attribute meanings are defined in the S-57 Object Catalogue.
func (f *Feature) Attributes() map[string]interface{} {
	return f.attributes
}

// Attribute returns a specific attribute value by name.
//
// Returns the value and true if the attribute exists, or nil and false if not found.
//
// Example:
//
//	if depth, ok := feature.Attribute("DRVAL1"); ok {
//	    fmt.Printf("Depth: %v meters\n", depth)
//	}
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
	// For Polygon: Array of coordinate pairs forming a closed ring (DEPRECATED - use Rings)
	//
	// Note: Coordinates follow GeoJSON convention [lon, lat], not [lat, lon].
	// DEPRECATED for Polygon geometry: Use Rings field instead to access ring structure.
	Coordinates [][]float64

	// Rings contains polygon ring data with usage indicators.
	// Only populated for Polygon geometry type.
	// First ring(s) with Usage=1 or 3 are exterior boundaries.
	// Rings with Usage=2 are interior boundaries (holes).
	Rings []Ring

	// BoundaryLines holds the polylines of a polygon's DRAWABLE boundary edges —
	// those NOT masked (FSPT MASK={1}) and NOT data-limit/cell-boundary edges
	// (USAG={3}). Per S-52 PresLib §8.6.2 those edges must not be drawn, though
	// the fill keeps them (§8.6.3). Renderers stroke the border from these (one
	// polyline per drawable edge); empty ⇒ stroke the Rings instead.
	BoundaryLines [][][]float64

	// Lines holds the DRAWABLE polylines of a LINE feature with masked (FSPT
	// MASK={1}) and data-limit (USAG={3}) edges removed (S-52 PresLib §8.6.2).
	// Multi-part: dropping a mid-line edge splits the line, so each element is one
	// contiguous drawn polyline of [lon,lat] points. The flat Coordinates field
	// keeps the full concatenation for backward compatibility. Empty/nil ⇒ no
	// masking applied → stroke Coordinates.
	Lines [][][]float64
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
		attributes := f.Attributes

		// Special handling for SOUNDG (Sounding) features:
		// Extract Z coordinates (depths) from geometry and add as DEPTHS attribute
		// SOUNDG features are multipoint with Z values containing depth soundings
		if f.ObjectClass == "SOUNDG" && len(f.Geometry.Coordinates) > 0 {
			depths := make([]float64, 0, len(f.Geometry.Coordinates))
			for _, coord := range f.Geometry.Coordinates {
				// Coordinates are [lon, lat, depth] for 3D points
				if len(coord) >= 3 {
					depths = append(depths, coord[2])
				}
			}
			if len(depths) > 0 {
				// Make a copy of attributes map and add DEPTHS
				attrs := make(map[string]interface{}, len(attributes)+1)
				for k, v := range attributes {
					attrs[k] = v
				}
				attrs["DEPTHS"] = depths
				attributes = attrs
			}
		}

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
				Type:          GeometryType(f.Geometry.Type),
				Coordinates:   f.Geometry.Coordinates,
				Rings:         rings,
				BoundaryLines: f.Geometry.BoundaryLines,
				Lines:         f.Geometry.Lines,
			},
			attributes: attributes,
		}
	}

	chart := &Chart{
		features:           features,
		datasetName:        internal.DatasetName(),
		edition:            internal.Edition(),
		updateNumber:       internal.UpdateNumber(),
		updateDate:         internal.UpdateDate(),
		issueDate:          internal.IssueDate(),
		s57Edition:         internal.S57Edition(),
		producingAgency:    internal.ProducingAgency(),
		comment:            internal.Comment(),
		exchangePurpose:    internal.ExchangePurpose(),
		productSpec:        internal.ProductSpecification(),
		applicationProfile: internal.ApplicationProfile(),
		usageBand:          UsageBand(internal.IntendedUsage()),
		// Coordinate system metadata from DSPM record
		coordinateUnits:  CoordinateUnits(internal.CoordinateUnits()),
		depthUnits:       DepthUnits(internal.DepthUnits()),
		horizontalDatum:  internal.HorizontalDatum(),
		compilationScale: internal.CompilationScale(),
	}

	// Carry over non-fatal conformance warnings.
	for _, w := range internal.Warnings() {
		chart.warnings = append(chart.warnings, ConformanceWarning{
			Clause: w.Clause, Code: w.Code, Message: w.Message, Count: w.Count,
		})
	}

	// Build spatial index for fast viewport queries
	chart.buildSpatialIndex()

	return chart
}

// buildSpatialIndex creates an R-tree spatial index for O(log n) bounding box queries.
// This provides 100× faster viewport queries compared to linear O(n) scan.
func (c *Chart) buildSpatialIndex() {
	if len(c.features) == 0 {
		return
	}

	// Create R-tree (2D, min=25 children, max=50 children)
	// These parameters are optimal for most use cases
	rtree := rtreego.NewTree(2, 25, 50)

	// Calculate bounds - prefer M_COVR (Meta Coverage) feature if available
	// M_COVR defines the official coverage area of the chart
	var chartBounds *Bounds

	// First pass: look for M_COVR features
	var foundMCOVR bool
	for _, feature := range c.features {
		if feature.ObjectClass() == "M_COVR" {
			foundMCOVR = true
			fb := featureBounds(feature)
			if chartBounds == nil {
				chartBounds = &fb
			} else {
				// Expand with M_COVR bounds
				if fb.MinLon < chartBounds.MinLon {
					chartBounds.MinLon = fb.MinLon
				}
				if fb.MaxLon > chartBounds.MaxLon {
					chartBounds.MaxLon = fb.MaxLon
				}
				if fb.MinLat < chartBounds.MinLat {
					chartBounds.MinLat = fb.MinLat
				}
				if fb.MaxLat > chartBounds.MaxLat {
					chartBounds.MaxLat = fb.MaxLat
				}
			}
		}
	}

	// Second pass: insert features into R-tree and calculate fallback bounds if no M_COVR
	for _, feature := range c.features {
		fb := featureBounds(feature)

		// Insert feature into R-tree
		indexed := &indexedFeature{
			feature: feature,
			bounds:  fb,
		}
		rtree.Insert(indexed)

		// Only calculate bounds from features if no M_COVR was found
		if !foundMCOVR {
			if chartBounds == nil {
				// Initialize with first feature bounds
				chartBounds = &fb
			} else {
				// Expand chart bounds to include this feature
				if fb.MinLon < chartBounds.MinLon {
					chartBounds.MinLon = fb.MinLon
				}
				if fb.MaxLon > chartBounds.MaxLon {
					chartBounds.MaxLon = fb.MaxLon
				}
				if fb.MinLat < chartBounds.MinLat {
					chartBounds.MinLat = fb.MinLat
				}
				if fb.MaxLat > chartBounds.MaxLat {
					chartBounds.MaxLat = fb.MaxLat
				}
			}
		}
	}

	// Assign R-tree to spatial index
	c.spatialIndex = &spatialIndex{
		rtree: rtree,
	}

	if chartBounds != nil {
		c.bounds = *chartBounds
	}
}
