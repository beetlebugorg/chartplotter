package s52

import (
	"fmt"
	"sort"

	s57 "github.com/beetlebugorg/chartplotter/pkg/s57"
)

// Viewport represents the visible area of the chart
type Viewport struct {
	// Geographic bounds of the viewport (lat/lon)
	GeoBounds GeoBounds

	// Display scale (1:scale, e.g., 1:25000)
	Scale float64

	// Projection for converting lat/lon to surface coordinates
	Projection Projection
}

// Projection interface for coordinate conversion
type Projection interface {
	// Project converts geographic coordinates to surface coordinates
	Project(lat, lon float64) (x, y float64)
}

// IdentityProjection is a no-op projection that preserves lat/lon coordinates.
// Used for GeoJSON export where we want geographic coordinates, not projected.
type IdentityProjection struct{}

// Project returns lon, lat unchanged (note: lon=x, lat=y for GeoJSON)
func (p *IdentityProjection) Project(lat, lon float64) (x, y float64) {
	return lon, lat
}

// FilterOptions contains optional filtering rules for visibility
type FilterOptions struct {
	// Optional: only show these object classes (empty = show all)
	ShowObjects []string

	// Optional: hide these object classes (empty = hide none)
	HideObjects []string

	// Optional: only show this specific feature index (-1 = show all)
	FeatureIndex int

	// Optional: only show primitives with this priority level (-1 = show all)
	// Note: Filtering happens at primitive level, not feature level
	PriorityFilter int
}

// ChartScene represents the complete scene graph for a chart.
// It manages feature nodes, spatial indexing, and incremental updates.
type ChartScene struct {
	// All feature nodes in the scene
	Nodes []*FeatureNode

	// Spatial index for O(log n) visibility queries
	SpatialIndex *SpatialIndex

	// S-52 library for style lookups
	Library *Library

	// Current mariner settings
	Settings *MarinerSettings

	// Current viewport (for culling and projection)
	Viewport *Viewport

	// Optional filter options (for debugging/testing)
	FilterOptions *FilterOptions
}

// NewChartScene creates a new empty chart scene
func NewChartScene(library *Library, settings *MarinerSettings) *ChartScene {
	return &ChartScene{
		Nodes:        make([]*FeatureNode, 0),
		SpatialIndex: NewSpatialIndex(),
		Library:      library,
		Settings:     settings,
	}
}

// AddNode adds a feature node to the scene
func (s *ChartScene) AddNode(node *FeatureNode) {
	s.Nodes = append(s.Nodes, node)
	s.SpatialIndex.Insert(node)
}

// BuildFromFeatures builds the scene from a slice of S-57 features
// This creates FeatureNodes and adds them to the scene graph
func (s *ChartScene) BuildFromFeatures(features []s57.Feature) error {
	return s.BuildFromFeaturesWithScales(features, nil)
}

// BuildFromFeaturesWithScales builds the scene from features with optional compilation scales
// scales maps feature index to chart compilation scale for SCAMIN fallback
func (s *ChartScene) BuildFromFeaturesWithScales(features []s57.Feature, scales []uint32) error {
	// Track indices per object class for FeatureIndex filtering
	objectClassIndices := make(map[string]int)
	objectClassCounts := make(map[string]int)

	for i, feature := range features {
		objClass := feature.ObjectClass()
		index := objectClassIndices[objClass]
		objectClassIndices[objClass]++
		objectClassCounts[objClass]++

		// Get compilation scale if provided
		var compilationScale uint32
		if scales != nil && i < len(scales) {
			compilationScale = scales[i]
		}

		// Create scene nodes for this feature
		// For multipoint features like SOUNDG, this creates one node per point
		nodes := s.createFeatureNodes(feature, index, compilationScale)
		for _, node := range nodes {
			s.AddNode(node)
		}
	}

	return nil
}

// createFeatureNodes creates one or more scene nodes from a feature.
// For most features this returns a single node, but for multipoint features like SOUNDG
// this returns one node per point with individual DEPTH attributes.
func (s *ChartScene) createFeatureNodes(feature s57.Feature, index int, compilationScale uint32) []*FeatureNode {
	objClass := feature.ObjectClass()
	geom := feature.Geometry()

	// Special handling for SOUNDG: create one node per sounding point
	// Each node gets its own DEPTH attribute from the Z coordinate
	if objClass == "SOUNDG" && geom.Type == s57.GeometryTypePoint && len(geom.Coordinates) > 0 {
		nodes := make([]*FeatureNode, len(geom.Coordinates))
		for pointIdx, coord := range geom.Coordinates {
			// Clone attributes and add DEPTH from Z coordinate
			attrs := cloneAttributes(feature.Attributes())
			if len(coord) >= 3 {
				attrs["DEPTH"] = coord[2]
			}

			// Single-point geometry for this sounding
			singlePointGeom := s57.Geometry{
				Type:        s57.GeometryTypePoint,
				Coordinates: [][]float64{coord},
			}

			nodes[pointIdx] = &FeatureNode{
				ID:               fmt.Sprintf("%s-%d-%d", objClass, feature.ID(), pointIdx),
				ObjectClass:      objClass,
				Attributes:       attrs,
				GeoBounds:        GeoBounds{MinLon: coord[0], MaxLon: coord[0], MinLat: coord[1], MaxLat: coord[1]},
				GeoGeometry:      singlePointGeom,
				Index:            index,
				CompilationScale: compilationScale,
				Dirty:            GeometryDirty | StyleDirty | PrimitivesDirty | VisibilityDirty,
			}
		}
		return nodes
	}

	// Normal single-node feature
	return []*FeatureNode{{
		ID:               generateFeatureID(feature),
		ObjectClass:      objClass,
		Attributes:       feature.Attributes(),
		GeoBounds:        extractGeoBounds(geom),
		GeoGeometry:      geom,
		Index:            index,
		CompilationScale: compilationScale,
		Dirty:            GeometryDirty | StyleDirty | PrimitivesDirty | VisibilityDirty,
	}}
}

// cloneAttributes creates a shallow copy of an attribute map
func cloneAttributes(attrs map[string]interface{}) map[string]interface{} {
	clone := make(map[string]interface{}, len(attrs))
	for k, v := range attrs {
		clone[k] = v
	}
	return clone
}

// generateFeatureID creates a unique ID for a feature node
func generateFeatureID(feature s57.Feature) string {
	return fmt.Sprintf("%s-%d", feature.ObjectClass(), feature.ID())
}

// extractGeoBounds extracts geographic bounds from S-57 geometry
func extractGeoBounds(geom s57.Geometry) GeoBounds {
	if len(geom.Coordinates) == 0 {
		return GeoBounds{}
	}

	// Initialize with first coordinate
	first := geom.Coordinates[0]
	bounds := GeoBounds{
		MinLon: first[0],
		MaxLon: first[0],
		MinLat: first[1],
		MaxLat: first[1],
	}

	// Expand to include all coordinates
	for _, coord := range geom.Coordinates {
		lon, lat := coord[0], coord[1]
		if lon < bounds.MinLon {
			bounds.MinLon = lon
		}
		if lon > bounds.MaxLon {
			bounds.MaxLon = lon
		}
		if lat < bounds.MinLat {
			bounds.MinLat = lat
		}
		if lat > bounds.MaxLat {
			bounds.MaxLat = lat
		}
	}

	return bounds
}

// UpdateViewport updates the viewport and marks affected nodes as dirty
func (s *ChartScene) UpdateViewport(viewport *Viewport) {
	oldViewport := s.Viewport
	s.Viewport = viewport

	// If viewport changed, mark all nodes for visibility and geometry update
	if oldViewport == nil || viewportChanged(oldViewport, viewport) {
		for _, node := range s.Nodes {
			node.MarkDirty(VisibilityDirty | GeometryDirty | PrimitivesDirty)
		}
	}
}

// UpdateSettings updates mariner settings and marks affected nodes as dirty
func (s *ChartScene) UpdateSettings(settings *MarinerSettings) {
	s.Settings = settings

	// Mark all nodes for style update
	for _, node := range s.Nodes {
		node.MarkDirty(StyleDirty | PrimitivesDirty)
	}
}

// GetVisibleNodes returns all nodes that intersect the current viewport
func (s *ChartScene) GetVisibleNodes() []*FeatureNode {
	if s.Viewport == nil {
		return nil
	}

	// Query spatial index for nodes in viewport
	return s.SpatialIndex.Query(s.Viewport.GeoBounds)
}

// Update updates all dirty nodes in the scene
// This should be called after viewport or settings changes, before rendering
func (s *ChartScene) Update() error {
	if s.Viewport == nil {
		return fmt.Errorf("viewport not set")
	}

	// Get visible nodes
	visibleNodes := s.GetVisibleNodes()

	// Update each visible node
	for _, node := range visibleNodes {
		if err := s.updateNode(node); err != nil {
			return fmt.Errorf("failed to update node %s: %w", node.ID, err)
		}
	}

	return nil
}

// updateNode updates a single node based on its dirty flags
func (s *ChartScene) updateNode(node *FeatureNode) error {
	// Update style FIRST (needed for visibility check)
	// DisplayCategory filtering requires style to be computed
	if node.NeedsStyleUpdate() {
		if err := s.updateNodeStyle(node); err != nil {
			return err
		}
		node.ClearDirty(StyleDirty)
	}

	// Update visibility (now that we have style)
	if node.NeedsVisibilityUpdate() {
		node.Visible = s.isNodeVisible(node)
		node.ClearDirty(VisibilityDirty)
	}

	// Skip further updates if not visible
	if !node.Visible {
		return nil
	}

	// Update geometry (projection)
	if node.NeedsGeometryUpdate() {
		if err := s.updateNodeGeometry(node); err != nil {
			return err
		}
		node.ClearDirty(GeometryDirty)
	}

	// Update primitives (convert to render commands)
	if node.NeedsPrimitiveUpdate() {
		if err := s.updateNodePrimitives(node); err != nil {
			return err
		}
		node.ClearDirty(PrimitivesDirty)
	}

	return nil
}

// isNodeVisible determines if a node should be rendered based on ALL visibility rules.
// This consolidates viewport culling, S-52 compliance, and debugging filters in one place.
func (s *ChartScene) isNodeVisible(node *FeatureNode) bool {
	// SCAMIN GLOBAL BYPASS - if mariner disabled SCAMIN, ALL features are visible
	if !s.Settings.EnableSCAMIN {
		return true
	}

	// 1. VIEWPORT BOUNDS CHECK (spatial culling)
	if !node.GeoBounds.Intersects(s.Viewport.GeoBounds) {
		fmt.Printf("[BOUNDS] %s filtered\n", node.ObjectClass)
		return false
	}

	// 2. OBJECT CLASS FILTERS (debugging - show/hide specific classes)
	if s.FilterOptions != nil {
		objClass := node.ObjectClass

		// If show list exists, only show those objects
		if len(s.FilterOptions.ShowObjects) > 0 {
			found := false
			for _, show := range s.FilterOptions.ShowObjects {
				if objClass == show {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}

		// Hide objects in hide list
		if len(s.FilterOptions.HideObjects) > 0 {
			for _, hide := range s.FilterOptions.HideObjects {
				if objClass == hide {
					return false
				}
			}
		}

		// Feature index filter (for debugging - show only one specific feature)
		if s.FilterOptions.FeatureIndex >= 0 {
			if node.Index != s.FilterOptions.FeatureIndex {
				return false
			}
		}

		// Priority filter (for debugging - render one priority at a time)
		// Note: Filtering now happens at primitive level, not feature level
		// Features are visible if they have ANY primitives at the filtered priority
		// (Actual primitive filtering happens during rendering)
	}

	// 3. DISPLAY CATEGORY CHECK (S-52 IMO requirement)
	// Style must be computed before calling this (updateNode does style first)
	if node.Style != nil && node.Style.InstructionSet != nil {
		displayCategory := node.Style.InstructionSet.DisplayCategory
		if displayCategory > s.Settings.DisplayCategory {
			fmt.Printf("[CAT-FILTER] %s cat=%d > settings=%d\n", node.ObjectClass, displayCategory, s.Settings.DisplayCategory)
			return false
		}
	} else {
		// No style = no rendering instructions, but NOT necessarily invisible
		// Features without rendering instructions fall through to SCAMIN checks
		fmt.Printf("[NO-STYLE] %s (no rendering instructions, checking SCAMIN)\n", node.ObjectClass)
	}

	// 4. SCAMIN CHECK (S-52 Section 10.4 - scale-dependent display)
	// Safety-critical features ALWAYS display, regardless of SCAMIN
	if s.isSafetyCritical(node) {
		return true
	}

	// Mariner can disable SCAMIN filtering
	if !s.Settings.EnableSCAMIN {
		fmt.Printf("[SCAMIN-OFF] %s visible\n", node.ObjectClass)
		return true
	}

	// If DisplayScale is 0, SCAMIN filtering is disabled (show everything)
	// This is the mariner setting, not the viewport zoom scale
	if s.Settings.DisplayScale == 0 {
		return true
	}

	// Check SCAMIN attribute
	scaminVal, ok := node.Attributes["SCAMIN"]
	if !ok {
		// No SCAMIN - use chart compilation scale as fallback
		// Don't show features from overview charts (large scale denominators) at detailed zoom levels (small scale denominators)
		// e.g., don't show 1:3,500,000 chart features when displaying at 1:50
		if node.CompilationScale > 0 {
			// Show chart only at its compilation scale and MORE DETAILED (smaller denominators)
			// e.g., 1:80K chart shows at 1:80K, 1:40K, 1:20K (more detail), but not at 1:500K (overview)
			maxDisplayScale := float64(node.CompilationScale)

			// Use Settings.DisplayScale if it's non-zero (mariner override), otherwise use viewport scale
			displayScale := s.Viewport.Scale
			if s.Settings.DisplayScale > 0 {
				displayScale = float64(s.Settings.DisplayScale)
			}

			result := displayScale <= maxDisplayScale
			return result
		}
		// No compilation scale info, show at all scales
		return true
	}

	// Parse SCAMIN value
	var scamin float64
	switch v := scaminVal.(type) {
	case int:
		scamin = float64(v)
	case float64:
		scamin = v
	case string:
		// Try parsing as number
		var err error
		scamin, err = parseFloat(v)
		if err != nil {
			// Invalid SCAMIN, treat as infinite (display always)
			return true
		}
	default:
		// Unknown type, treat as infinite
		return true
	}

	// S-52 Section 10.4: SCAMIN filtering
	// SCAMIN is the minimum display scale at which the feature should appear
	// Features should display at their SCAMIN scale and larger scales (smaller denominators)
	// e.g., SCAMIN=50000 means show at 1:50,000, 1:25,000, 1:10,000 (zoomed in)
	//       but hide at 1:100,000, 1:500,000 (zoomed out)
	// Display when: current scale denominator <= SCAMIN

	// Use Settings.DisplayScale if it's non-zero (mariner override), otherwise use viewport scale
	displayScale := s.Viewport.Scale
	if s.Settings.DisplayScale > 0 {
		displayScale = float64(s.Settings.DisplayScale)
	}

	result := displayScale <= scamin
	return result
}

// isSafetyCritical checks if a node represents a safety-critical feature
// that should always display regardless of SCAMIN (S-52 Section 10.4.1)
func (s *ChartScene) isSafetyCritical(node *FeatureNode) bool {
	objClass := node.ObjectClass
	attrs := node.Attributes

	// Safety contours always display
	if objClass == "DEPCNT" {
		if valdco, ok := attrs["VALDCO"]; ok {
			switch v := valdco.(type) {
			case float64:
				if v == s.Settings.SafetyContour {
					return true
				}
			case int:
				if float64(v) == s.Settings.SafetyContour {
					return true
				}
			}
		}
	}

	// Isolated dangers in shallow water (depth < SafetyDepth)
	if objClass == "OBSTRN" || objClass == "WRECKS" || objClass == "UWTROC" {
		if valsou, ok := attrs["VALSOU"]; ok {
			switch v := valsou.(type) {
			case float64:
				if v < s.Settings.SafetyDepth {
					return true
				}
			case int:
				if float64(v) < s.Settings.SafetyDepth {
					return true
				}
			}
		}
	}

	// Traffic separation schemes always display
	if objClass == "TSSLPT" || objClass == "TSSBND" ||
		objClass == "TSSCRS" || objClass == "TSSRON" {
		return true
	}

	return false
}

// updateNodeStyle performs S-52 lookup to get rendering instructions
func (s *ChartScene) updateNodeStyle(node *FeatureNode) error {
	// Perform S-52 lookup - need geometry type from node
	geometryType := inferGeometryType(node.GeoGeometry)

	instructionSet := s.Library.LookupFeature(node.ObjectClass, geometryType, node.Attributes, s.Settings)
	if instructionSet == nil {
		// No instructions for this feature
		node.Style = nil
		return nil
	}

	// Update node priority from lookup result
	node.Priority = DisplayPriority(instructionSet.DisplayPriority)

	// Store instruction set (resource resolution happens during primitive generation)
	node.Style = &ResolvedStyle{
		InstructionSet: instructionSet,
	}

	return nil
}

// updateNodeGeometry projects geographic coordinates to surface coordinates
func (s *ChartScene) updateNodeGeometry(node *FeatureNode) error {
	if s.Viewport == nil || s.Viewport.Projection == nil {
		return fmt.Errorf("viewport or projection not set")
	}

	// Extract geometry based on type
	geom, ok := node.GeoGeometry.(s57.Geometry)
	if !ok {
		// Try pointer type
		geomPtr, ok := node.GeoGeometry.(*s57.Geometry)
		if !ok || geomPtr == nil {
			return fmt.Errorf("invalid geometry type")
		}
		geom = *geomPtr
	}

	// Project coordinates to surface space
	switch geom.Type {
	case s57.GeometryTypePoint:
		// Handle both single-point and multipoint features (e.g., SOUNDG with many soundings)
		node.Geometry = make([]Point, len(geom.Coordinates))
		for i, coord := range geom.Coordinates {
			x, y := s.Viewport.Projection.Project(coord[1], coord[0]) // lat, lon
			node.Geometry[i] = Point{X: x, Y: y}
		}

	case s57.GeometryTypeLineString:
		node.Geometry = make([]Point, len(geom.Coordinates))
		for i, coord := range geom.Coordinates {
			x, y := s.Viewport.Projection.Project(coord[1], coord[0]) // lat, lon
			node.Geometry[i] = Point{X: x, Y: y}
		}

	case s57.GeometryTypePolygon:
		// For polygons, use rings structure
		node.Rings = make([][]Point, len(geom.Rings))
		for i, ring := range geom.Rings {
			node.Rings[i] = make([]Point, len(ring.Coordinates))
			for j, coord := range ring.Coordinates {
				x, y := s.Viewport.Projection.Project(coord[1], coord[0]) // lat, lon
				node.Rings[i][j] = Point{X: x, Y: y}
			}
		}

		// Also populate simple geometry for backward compatibility
		if len(geom.Coordinates) > 0 {
			node.Geometry = make([]Point, len(geom.Coordinates))
			for i, coord := range geom.Coordinates {
				x, y := s.Viewport.Projection.Project(coord[1], coord[0]) // lat, lon
				node.Geometry[i] = Point{X: x, Y: y}
			}
		}
	}

	return nil
}

// updateNodePrimitives converts S-52 instructions to render primitives
func (s *ChartScene) updateNodePrimitives(node *FeatureNode) error {
	if node.Style == nil || node.Style.InstructionSet == nil {
		node.Primitives = nil
		return nil
	}

	// Convert instructions to primitives
	primitives := make([]RenderPrimitive, 0)

	// Track sounding symbol positioning for tight spacing
	var soundingOffset float64
	var soundingCount int

	for _, instr := range node.Style.InstructionSet.Instructions {
		prims := s.instructionToPrimitives(instr, node)

		// Check if this is a sounding symbol (SOUNDG or SOUNDS)
		if syInstr, ok := instr.(*SYInstruction); ok {
			if IsSoundingSymbol(syInstr.SymbolID) {
				// Get symbol to check its bounding box
				symbol, err := s.Library.GetSymbol(syInstr.SymbolID)
				if err == nil && symbol != nil {
					// Apply offset to bring digits closer together
					for i := range prims {
						prims[i].Location.X += soundingOffset
					}

					// Calculate next offset using shared spacing function
					soundingOffset += CalculateSoundingSymbolAdvance(symbol)
					soundingCount++
				}
			} else {
				// Reset for non-sounding symbols
				soundingOffset = 0
				soundingCount = 0
			}
		} else {
			// Reset for non-symbol instructions
			soundingOffset = 0
			soundingCount = 0
		}

		primitives = append(primitives, prims...)
	}

	node.Primitives = primitives
	return nil
}

// instructionToPrimitives converts a single instruction to render primitive(s)
// Returns a slice because some instructions (like patterns) may generate multiple primitives
func (s *ChartScene) instructionToPrimitives(instr Instruction, node *FeatureNode) []RenderPrimitive {
	switch typed := instr.(type) {
	case *ACInstruction:
		// Area color fill
		return s.convertACInstruction(typed, node)

	case *APInstruction:
		// Area pattern
		return s.convertAPInstruction(typed, node)

	case *SYInstruction:
		// Point symbol
		return s.convertSYInstruction(typed, node)

	case *LSInstruction:
		// Simple line
		return s.convertLSInstruction(typed, node)

	case *LCInstruction:
		// Complex line
		return s.convertLCInstruction(typed, node)

	case *TXInstruction:
		// Text
		return s.convertTXInstruction(typed, node)

	default:
		// Unknown instruction type - skip
		return nil
	}
}

// convertACInstruction converts an AC (area color) instruction to a primitive
func (s *ChartScene) convertACInstruction(instr *ACInstruction, node *FeatureNode) []RenderPrimitive {
	// Resolve color
	color, err := s.Library.GetColor(instr.Color, s.Settings.ColorScheme)
	if err != nil || color == nil {
		return nil
	}

	prim := RenderPrimitive{
		Type:      RenderPrimitiveAreaFill,
		Priority:  node.Priority, // Inherit feature priority
		Geometry:  node.Geometry,
		Rings:     node.Rings,
		FillColor: color,
	}

	return []RenderPrimitive{prim}
}

// convertAPInstruction converts an AP (area pattern) instruction to a primitive
func (s *ChartScene) convertAPInstruction(instr *APInstruction, node *FeatureNode) []RenderPrimitive {
	// Get pattern from library
	pattern, err := s.Library.GetPattern(instr.PatternID)
	if err != nil || pattern == nil {
		return nil
	}

	prim := RenderPrimitive{
		Type:     RenderPrimitivePattern,
		Priority: node.Priority, // Inherit feature priority
		Geometry: node.Geometry,
		Rings:    node.Rings,
		Pattern:  pattern,
	}

	return []RenderPrimitive{prim}
}

// convertSYInstruction converts an SY (symbol) instruction to a primitive
func (s *ChartScene) convertSYInstruction(instr *SYInstruction, node *FeatureNode) []RenderPrimitive {
	// Get symbol from library
	symbol, err := s.Library.GetSymbol(instr.SymbolID)
	if err != nil || symbol == nil {
		return nil
	}

	// Determine symbol placement based on geometry type
	if geom, ok := node.GeoGeometry.(s57.Geometry); ok {
		if geom.Type == s57.GeometryTypePolygon {
			// For areas, place symbol at centroid
			location := calculateCentroid(node.Geometry, node.Rings)
			return []RenderPrimitive{{
				Type:     RenderPrimitiveSymbol,
				Priority: node.Priority,
				Symbol:   symbol,
				Location: location,
				Rotation: instr.Rotation,
				Scale:    1.0,
			}}
		} else if geom.Type == s57.GeometryTypePoint {
			// For point features, create one primitive per point (handles multipoint like SOUNDG)
			prims := make([]RenderPrimitive, len(node.Geometry))
			for i, location := range node.Geometry {
				prims[i] = RenderPrimitive{
					Type:     RenderPrimitiveSymbol,
					Priority: node.Priority,
					Symbol:   symbol,
					Location: location,
					Rotation: instr.Rotation,
					Scale:    1.0,
				}
			}
			return prims
		} else if len(node.Geometry) > 0 {
			// For lines, use first point
			location := node.Geometry[0]
			return []RenderPrimitive{{
				Type:     RenderPrimitiveSymbol,
				Priority: node.Priority,
				Symbol:   symbol,
				Location: location,
				Rotation: instr.Rotation,
				Scale:    1.0,
			}}
		}
	} else if len(node.Geometry) > 0 {
		// Fallback: use first point if type unknown
		location := node.Geometry[0]
		return []RenderPrimitive{{
			Type:     RenderPrimitiveSymbol,
			Priority: node.Priority,
			Symbol:   symbol,
			Location: location,
			Rotation: instr.Rotation,
			Scale:    1.0,
		}}
	}

	return nil
}

// convertLSInstruction converts an LS (simple line) instruction to a primitive
func (s *ChartScene) convertLSInstruction(instr *LSInstruction, node *FeatureNode) []RenderPrimitive {
	// Resolve color
	color, err := s.Library.GetColor(instr.Color, s.Settings.ColorScheme)
	if err != nil || color == nil {
		return nil
	}

	// Convert width to mm (S-52 width values: 1=0.32mm, 2=0.64mm, 3=0.96mm, 4=1.28mm)
	widthMM := float64(instr.Width) * 0.32

	// Create line style definition
	lineStyle := &LineStyleDef{
		Style: instr.Style,
		Cap:   1, // Round cap
		Join:  1, // Round join
	}

	// Set dash pattern for dashed/dotted lines
	switch instr.Style {
	case "DASH":
		lineStyle.DashPattern = []float64{4.0, 2.0} // 4mm dash, 2mm gap
	case "DOTT":
		lineStyle.DashPattern = []float64{0.5, 2.0} // 0.5mm dot, 2mm gap
	}

	// For polygons, only stroke the outer ring (not holes)
	// node.Geometry contains all rings concatenated, which creates self-crossing lines
	geometry := node.Geometry
	if len(node.Rings) > 0 {
		// Polygon: use only outer ring
		geometry = node.Rings[0]
	}

	prim := RenderPrimitive{
		Type:        RenderPrimitiveLineStroke,
		Priority:    node.Priority, // Inherit feature priority
		Geometry:    geometry,
		StrokeColor: color,
		StrokeWidth: widthMM,
		LineStyle:   lineStyle,
	}

	return []RenderPrimitive{prim}
}

// convertLCInstruction converts an LC (complex line) instruction to a primitive
func (s *ChartScene) convertLCInstruction(instr *LCInstruction, node *FeatureNode) []RenderPrimitive {
	// Get line style from library
	lineStyle, err := s.Library.GetLineStyle(instr.LineStyleID)
	if err != nil || lineStyle == nil {
		return nil
	}

	// For polygons, only stroke the outer ring (not holes)
	geometry := node.Geometry
	if len(node.Rings) > 0 {
		// Polygon: use only outer ring
		geometry = node.Rings[0]
	}

	// Create primitive with complex linestyle reference
	// The rendering engine will expand the linestyle along the path
	prim := RenderPrimitive{
		Type:             RenderPrimitiveLineStroke,
		Priority:         node.Priority,
		Geometry:         geometry,
		ComplexLineStyle: lineStyle,
	}

	return []RenderPrimitive{prim}
}

// convertTXInstruction converts a TX (text) instruction to a primitive
func (s *ChartScene) convertTXInstruction(instr *TXInstruction, node *FeatureNode) []RenderPrimitive {
	// Resolve color
	color, err := s.Library.GetColor(instr.Color, s.Settings.ColorScheme)
	if err != nil || color == nil {
		return nil
	}

	// Get text content
	text := instr.Text
	if instr.IsAttributeReference {
		// Text is an attribute reference - look it up
		val, ok := node.Attributes[instr.Text]
		if !ok {
			// Attribute doesn't exist - don't render anything
			return nil
		}
		text = fmt.Sprintf("%v", val)
	}

	// Use first point as location
	var location Point
	if len(node.Geometry) > 0 {
		location = node.Geometry[0]
	}

	// Apply offsets (convert from 0.01mm to mm)
	location.X += float64(instr.XOffset) * 0.01
	location.Y += float64(instr.YOffset) * 0.01

	// Convert HJUST/VJUST to alignment (S-52 values: 1=left/top, 2=center/middle, 3=right/bottom)
	hAlign := instr.HJust - 1 // 0=left, 1=center, 2=right
	vAlign := instr.VJust - 1 // 0=top, 1=middle, 2=bottom

	// White outline color for better readability (S-52 recommends text halos)
	// Use pure white RGB values directly
	whiteOutline := Color{
		Token: "WHITE",
		CIE_X: 0.310, // D65 white point
		CIE_Y: 0.316,
		CIE_L: 100.0, // Maximum lightness
	}

	textStyle := &TextStyle{
		FontFamily:   "serif",
		FontSize:     instr.Font.BodySizeMM(),
		Bold:         instr.Font.IsBold(),
		Italic:       instr.Font.IsItalic(),
		HAlign:       hAlign,
		VAlign:       vAlign,
		CharSpacing:  float64(instr.Space) * 0.1, // Approximate character spacing
		OutlineColor: &whiteOutline,
		OutlineWidth: 0.5, // Outline width in mm (visible white border)
	}

	prim := RenderPrimitive{
		Type:      RenderPrimitiveText,
		Priority:  Priority8, // S-52 Section 10.3.4.1: "Text must be drawn last, in priority 8"
		Text:      text,
		TextStyle: textStyle,
		Location:  location,
	}

	// Set stroke color for text
	prim.StrokeColor = color

	return []RenderPrimitive{prim}
}

// Render renders all visible nodes to the surface
// This is the main rendering API - works with ANY RenderSurface implementation
func (s *ChartScene) Render(surface SceneSurface) error {
	if s.Viewport == nil {
		return fmt.Errorf("viewport not set")
	}

	// Update any dirty nodes before rendering
	if err := s.Update(); err != nil {
		return fmt.Errorf("failed to update scene: %w", err)
	}

	// Begin scene rendering
	if err := surface.BeginScene(*s.Viewport); err != nil {
		return err
	}

	// Get visible nodes
	visibleNodes := s.GetVisibleNodes()

	// Render primitives in priority order
	// S-52 spec: primitives render by their individual priority, not feature priority
	// Collect all primitives from all visible nodes
	type nodePrimitive struct {
		node      *FeatureNode
		primitive *RenderPrimitive
	}
	var allPrimitives []nodePrimitive

	for _, node := range visibleNodes {
		if !node.Visible {
			continue
		}
		for i := range node.Primitives {
			prim := &node.Primitives[i]
			// If filtering by priority, skip primitives not at that priority
			if s.FilterOptions != nil && s.FilterOptions.PriorityFilter >= 0 {
				if prim.Priority != DisplayPriority(s.FilterOptions.PriorityFilter) {
					continue
				}
			}
			allPrimitives = append(allPrimitives, nodePrimitive{node, prim})
		}
	}

	// Sort primitives by priority, then by type (text last)
	sort.SliceStable(allPrimitives, func(i, j int) bool {
		pi, pj := allPrimitives[i].primitive, allPrimitives[j].primitive

		// First: sort by primitive priority
		if pi.Priority != pj.Priority {
			return pi.Priority < pj.Priority
		}

		// Second: within same priority, text renders last
		iText := pi.Type == RenderPrimitiveText
		jText := pj.Type == RenderPrimitiveText
		if iText != jText {
			return !iText // non-text before text
		}

		// Third: maintain original order
		return false
	})

	// Apply text decluttering if enabled
	declutterConfig := s.Settings.DeclutterConfig
	if declutterConfig == nil {
		declutterConfig = DefaultDeclutterConfig()
	}

	if declutterConfig.Enabled {
		// Extract primitives and object classes for decluttering
		primitives := make([]RenderPrimitive, len(allPrimitives))
		objectClasses := make([]string, len(allPrimitives))
		for i, np := range allPrimitives {
			primitives[i] = *np.primitive
			objectClasses[i] = np.node.ObjectClass
		}

		// Apply decluttering with display scale
		displayScale := s.Settings.DisplayScale
		declutteredPrimitives := DeclutterText(primitives, objectClasses, declutterConfig, displayScale)

		// Rebuild allPrimitives from decluttered result
		// Map back to nodePrimitive structs, preserving nodes
		newAllPrimitives := make([]nodePrimitive, 0, len(declutteredPrimitives))
		primIndex := 0
		for i := range primitives {
			if primIndex >= len(declutteredPrimitives) {
				break
			}
			// Check if this primitive was kept (text primitives may be filtered)
			if primitives[i].Type != RenderPrimitiveText {
				// Non-text always kept in same order
				newAllPrimitives = append(newAllPrimitives, nodePrimitive{
					node:      allPrimitives[i].node,
					primitive: &declutteredPrimitives[primIndex],
				})
				primIndex++
			} else {
				// Text primitive - check if it's in the decluttered output
				// Compare by position to see if it was kept
				found := false
				for j := primIndex; j < len(declutteredPrimitives); j++ {
					if declutteredPrimitives[j].Type == RenderPrimitiveText &&
						declutteredPrimitives[j].Text == primitives[i].Text {
						newAllPrimitives = append(newAllPrimitives, nodePrimitive{
							node:      allPrimitives[i].node,
							primitive: &declutteredPrimitives[j],
						})
						primIndex = j + 1
						found = true
						break
					}
				}
				if !found {
					// Text was filtered out, skip it
					continue
				}
			}
		}
		allPrimitives = newAllPrimitives
	}

	// Render all primitives in sorted order
	for _, np := range allPrimitives {
		filteredNode := *np.node
		filteredNode.Primitives = []RenderPrimitive{*np.primitive}
		if err := surface.RenderNode(&filteredNode); err != nil {
			return fmt.Errorf("failed to render primitive from %s: %w", np.node.ID, err)
		}
	}

	// End scene rendering
	if err := surface.EndScene(); err != nil {
		return err
	}

	return nil
}

// filterPrimitivesByPriority filters primitives to only those with the specified priority
func (s *ChartScene) filterPrimitivesByPriority(primitives []RenderPrimitive, priority DisplayPriority) []RenderPrimitive {
	filtered := make([]RenderPrimitive, 0, len(primitives))
	for _, prim := range primitives {
		if prim.Priority == priority {
			filtered = append(filtered, prim)
		}
	}
	return filtered
}

// Helper functions

// getGeometryOrder returns sort order for geometry types per S-52 Section 10.3.4.1
// Areas render first (bottom), then lines, then points (top)
func getGeometryOrder(geoGeometry interface{}) int {
	if geom, ok := geoGeometry.(s57.Geometry); ok {
		switch geom.Type {
		case s57.GeometryTypePolygon:
			return 0 // Areas on bottom
		case s57.GeometryTypeLineString:
			return 1 // Lines in middle
		case s57.GeometryTypePoint:
			return 2 // Points on top
		}
	}
	return 1 // Default to line if unknown
}

func viewportChanged(old, new *Viewport) bool {
	if old == nil || new == nil {
		return true
	}

	// Check if bounds changed
	if old.GeoBounds != new.GeoBounds {
		return true
	}

	// Check if scale changed significantly (>10% difference)
	scaleRatio := old.Scale / new.Scale
	if scaleRatio < 0.9 || scaleRatio > 1.1 {
		return true
	}

	return false
}

func inferGeometryType(geoGeometry interface{}) string {
	// Check if it's an s57.Geometry
	if geom, ok := geoGeometry.(s57.Geometry); ok {
		switch geom.Type {
		case s57.GeometryTypePoint:
			return "P"
		case s57.GeometryTypeLineString:
			return "L"
		case s57.GeometryTypePolygon:
			return "A"
		}
	}

	// Check if it's a pointer to s57.Geometry
	if geomPtr, ok := geoGeometry.(*s57.Geometry); ok && geomPtr != nil {
		switch geomPtr.Type {
		case s57.GeometryTypePoint:
			return "P"
		case s57.GeometryTypeLineString:
			return "L"
		case s57.GeometryTypePolygon:
			return "A"
		}
	}

	// Default to area if unknown
	return "A"
}

// calculateCentroid calculates the centroid of a polygon for symbol placement
// Uses the outer ring (first ring) for calculation
func calculateCentroid(geometry []Point, rings [][]Point) Point {
	// Use outer ring if available
	var points []Point
	if len(rings) > 0 {
		points = rings[0]
	} else {
		points = geometry
	}

	if len(points) == 0 {
		return Point{}
	}

	// Simple centroid calculation (arithmetic mean of vertices)
	// For more accurate results with complex polygons, could use area-weighted centroid
	var sumX, sumY float64
	for _, p := range points {
		sumX += p.X
		sumY += p.Y
	}

	return Point{
		X: sumX / float64(len(points)),
		Y: sumY / float64(len(points)),
	}
}
