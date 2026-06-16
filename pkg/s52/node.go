package s52

// DirtyFlags represents what aspects of a node need updating
type DirtyFlags uint32

const (
	// CleanNode means nothing needs updating
	CleanNode DirtyFlags = 0
	// GeometryDirty means geometry needs reprojection (viewport transform changed)
	GeometryDirty DirtyFlags = 1 << 0
	// StyleDirty means style needs re-lookup (mariner settings changed)
	StyleDirty DirtyFlags = 1 << 1
	// VisibilityDirty means visibility needs re-evaluation (viewport moved)
	VisibilityDirty DirtyFlags = 1 << 2
	// PrimitivesDirty means render primitives need rebuilding
	PrimitivesDirty DirtyFlags = 1 << 3
)

// IsDirty returns true if any dirty flag is set
func (d DirtyFlags) IsDirty() bool {
	return d != CleanNode
}

// Has returns true if the specified flag is set
func (d DirtyFlags) Has(flag DirtyFlags) bool {
	return (d & flag) != 0
}

// Set sets the specified flag
func (d *DirtyFlags) Set(flag DirtyFlags) {
	*d |= flag
}

// Clear clears the specified flag
func (d *DirtyFlags) Clear(flag DirtyFlags) {
	*d &^= flag
}

// ClearAll clears all flags
func (d *DirtyFlags) ClearAll() {
	*d = CleanNode
}

// GeoBounds represents a geographic bounding box (lat/lon)
type GeoBounds struct {
	MinLat, MinLon, MaxLat, MaxLon float64
}

// Contains returns true if the given point is within the bounds
func (b GeoBounds) Contains(lat, lon float64) bool {
	return lat >= b.MinLat && lat <= b.MaxLat &&
		lon >= b.MinLon && lon <= b.MaxLon
}

// Intersects returns true if this bounds intersects with another
func (b GeoBounds) Intersects(other GeoBounds) bool {
	return !(b.MaxLat < other.MinLat || b.MinLat > other.MaxLat ||
		b.MaxLon < other.MinLon || b.MinLon > other.MaxLon)
}

// DisplayPriority represents S-52 rendering priority (0-9)
// S-52 pslb04_0_part1.pdf page 20: "Display priorities range from 0 to 9"
// Lower numbers render first (bottom), higher numbers render on top
type DisplayPriority int

// S-52 display priority values (from LUPT DPRI field, stored in DAI DisplayPriority)
const (
	Priority0 DisplayPriority = 0 // Lowest priority
	Priority1 DisplayPriority = 1
	Priority2 DisplayPriority = 2
	Priority3 DisplayPriority = 3
	Priority4 DisplayPriority = 4
	Priority5 DisplayPriority = 5
	Priority6 DisplayPriority = 6
	Priority7 DisplayPriority = 7
	Priority8 DisplayPriority = 8
	Priority9 DisplayPriority = 9 // Highest priority (text labels, etc.)
)

// FeatureNode represents a single S-57 feature in the scene graph.
// It caches computed rendering data and tracks what needs updating.
type FeatureNode struct {
	// Unique identifier for this node
	ID string

	// Source data (immutable after creation)
	ObjectClass      string                 // S-57 object class (e.g., "DEPARE", "LNDARE")
	Attributes       map[string]interface{} // S-57 attributes
	GeoBounds        GeoBounds              // Geographic bounding box
	GeoGeometry      interface{}            // Original geographic geometry (lat/lon)
	Index            int                    // Index of this feature in the original feature list
	CompilationScale uint32                 // Chart compilation scale (for SCAMIN fallback)

	// Cached computed data (invalidated by dirty flags)
	Geometry   []Point           // Projected geometry (screen/surface coords)
	Rings      [][]Point         // For complex polygons
	Style      *ResolvedStyle    // S-52 lookup result
	Primitives []RenderPrimitive // Backend-agnostic drawing commands

	// State tracking
	Visible  bool            // Whether this feature should be rendered
	Dirty    DirtyFlags      // What needs updating
	Priority DisplayPriority // S-52 display priority (for draw order)

	// Surface-specific cached data (e.g., GPU ops for Gio)
	SurfaceData interface{} // Opaque data managed by RenderSurface implementation
}

// ResolvedStyle represents the result of S-52 lookup for a feature.
// It contains the instruction set and metadata from the lookup.
// Resource resolution (colors, symbols, patterns) happens during primitive generation.
type ResolvedStyle struct {
	// S-52 instruction set (includes priority, category, and parsed commands)
	InstructionSet *InstructionSet
}

// MarkDirty marks the node as dirty with the specified flags
func (n *FeatureNode) MarkDirty(flags DirtyFlags) {
	n.Dirty.Set(flags)
}

// ClearDirty clears the specified dirty flags
func (n *FeatureNode) ClearDirty(flags DirtyFlags) {
	n.Dirty.Clear(flags)
}

// IsClean returns true if the node has no dirty flags
func (n *FeatureNode) IsClean() bool {
	return !n.Dirty.IsDirty()
}

// NeedsStyleUpdate returns true if the style needs re-lookup
func (n *FeatureNode) NeedsStyleUpdate() bool {
	return n.Dirty.Has(StyleDirty)
}

// NeedsGeometryUpdate returns true if geometry needs reprojection
func (n *FeatureNode) NeedsGeometryUpdate() bool {
	return n.Dirty.Has(GeometryDirty)
}

// NeedsPrimitiveUpdate returns true if primitives need rebuilding
func (n *FeatureNode) NeedsPrimitiveUpdate() bool {
	return n.Dirty.Has(PrimitivesDirty)
}

// NeedsVisibilityUpdate returns true if visibility needs re-evaluation
func (n *FeatureNode) NeedsVisibilityUpdate() bool {
	return n.Dirty.Has(VisibilityDirty)
}
