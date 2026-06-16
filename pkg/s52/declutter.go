package s52

import (
	"math"
	"sort"
)

// ViewGroup defines categories of critical navigation information
// that should be prioritized during decluttering
type ViewGroup int

const (
	ViewGroupDefault        ViewGroup = iota
	ViewGroupNavigation               // Lights, buoys, beacons - always visible
	ViewGroupDepth                    // Depth soundings - high priority
	ViewGroupObstructions             // Wrecks, rocks, obstructions - high priority
	ViewGroupNamesImportant           // Port names, major landmarks
	ViewGroupNamesMinor               // Minor place names
)

// GetViewGroup returns the view group for an object class
func GetViewGroup(objectClass string) ViewGroup {
	switch objectClass {
	case "LIGHTS", "BCNLAT", "BCNCAR", "BOYLAT", "BOYCAR", "BOYINB", "BOYISD", "BOYSPP", "DAYMAR":
		return ViewGroupNavigation
	case "SOUNDG", "DEPARE", "DEPCNT":
		return ViewGroupDepth
	case "WRECKS", "UWTROC", "OBSTRN", "SBDARE":
		return ViewGroupObstructions
	case "LNDMRK", "BUAARE", "ACHBRT", "ACHARE":
		return ViewGroupNamesImportant
	default:
		return ViewGroupDefault
	}
}

// TextBounds represents a bounding box for a text label
type TextBounds struct {
	MinX, MinY, MaxX, MaxY float64
	Priority               DisplayPriority
	ViewGroup              ViewGroup
	ObjectClass            string
	PrimitiveIndex         int // Index in original primitives array
	OriginalLocation       Point
	CurrentLocation        Point
	Placed                 bool
}

// Intersects checks if this bounds intersects with another
func (tb *TextBounds) Intersects(other *TextBounds) bool {
	return !(tb.MaxX < other.MinX || tb.MinX > other.MaxX ||
		tb.MaxY < other.MinY || tb.MinY > other.MaxY)
}

// UpdateBounds updates the bounds based on current location
func (tb *TextBounds) UpdateBounds(width, height float64, hAlign, vAlign int) {
	x, y := tb.CurrentLocation.X, tb.CurrentLocation.Y

	// Adjust for horizontal alignment
	// 0=left, 1=center, 2=right
	var offsetX float64
	switch hAlign {
	case 0: // left
		offsetX = 0
	case 1: // center
		offsetX = -width / 2
	case 2: // right
		offsetX = -width
	}

	// Adjust for vertical alignment
	// 0=top, 1=middle, 2=bottom
	var offsetY float64
	switch vAlign {
	case 0: // top
		offsetY = 0
	case 1: // middle
		offsetY = -height / 2
	case 2: // bottom
		offsetY = -height
	}

	tb.MinX = x + offsetX
	tb.MaxX = x + offsetX + width
	tb.MinY = y + offsetY
	tb.MaxY = y + offsetY + height
}

// AlternatePosition represents an offset for trying alternate label placements
type AlternatePosition struct {
	Name    string
	OffsetX float64
	OffsetY float64
}

// Get 8 cardinal positions for alternate placement (plus center)
var alternatePositions = []AlternatePosition{
	{"center", 0, 0},
	{"right", 5, 0},
	{"left", -5, 0},
	{"top", 0, -5},
	{"bottom", 0, 5},
	{"top-right", 5, -5},
	{"top-left", -5, -5},
	{"bottom-right", 5, 5},
	{"bottom-left", -5, 5},
}

// EstimateTextBounds estimates the bounding box for a text primitive
// This is approximate since we don't have font metrics
func EstimateTextBounds(prim *RenderPrimitive, objectClass string) *TextBounds {
	if prim.Type != RenderPrimitiveText || prim.TextStyle == nil {
		return nil
	}

	// Estimate width based on character count and font size
	// Average character width is approximately 0.6 * font size for most fonts
	charWidth := prim.TextStyle.FontSize * 0.6
	width := float64(len(prim.Text)) * charWidth * (1.0 + prim.TextStyle.CharSpacing)
	height := prim.TextStyle.FontSize * 1.2 // Add some vertical padding

	// Add outline width if present
	if prim.TextStyle.OutlineColor != nil {
		padding := prim.TextStyle.OutlineWidth * 2
		width += padding
		height += padding
	}

	bounds := &TextBounds{
		Priority:         prim.Priority,
		ViewGroup:        GetViewGroup(objectClass),
		ObjectClass:      objectClass,
		OriginalLocation: prim.Location,
		CurrentLocation:  prim.Location,
	}

	bounds.UpdateBounds(width, height, prim.TextStyle.HAlign, prim.TextStyle.VAlign)

	return bounds
}

// DeclutterConfig controls decluttering behavior
type DeclutterConfig struct {
	// Enable text decluttering
	Enabled bool

	// Try alternate positions before hiding labels
	TryAlternatePositions bool

	// Minimum spacing between labels in mm (0 = allow touching)
	MinSpacing float64

	// Overlap tolerance as fraction of label size (0.0-1.0)
	// 0.0 = no overlap allowed, 0.5 = allow 50% overlap, 1.0 = completely disable collision
	OverlapTolerance float64

	// Scale-dependent overlap tolerance adjustment
	// At overview scales (>1:100k), increase tolerance to show more labels
	ScaleDependent bool

	// Always show critical navigation features (lights, buoys)
	AlwaysShowNavigation bool

	// Always show depth soundings (SOUNDG)
	AlwaysShowSoundings bool
}

// DefaultDeclutterConfig returns sensible defaults
// These are conservative - allowing some overlap for better label density
func DefaultDeclutterConfig() *DeclutterConfig {
	return &DeclutterConfig{
		Enabled:               true,
		TryAlternatePositions: true,
		MinSpacing:            0.2,  // 0.2mm minimum spacing (subtle gap)
		OverlapTolerance:      0.15, // Allow 15% overlap (slight touching)
		ScaleDependent:        true, // Adjust for scale
		AlwaysShowNavigation:  true,
		AlwaysShowSoundings:   true, // Depth soundings are critical
	}
}

// ConservativeDeclutterConfig returns more aggressive settings
// Use this for very dense charts or small displays
func ConservativeDeclutterConfig() *DeclutterConfig {
	return &DeclutterConfig{
		Enabled:               true,
		TryAlternatePositions: true,
		MinSpacing:            1.0, // 1mm minimum spacing
		OverlapTolerance:      0.0, // No overlap allowed
		AlwaysShowNavigation:  true,
		AlwaysShowSoundings:   true,
	}
}

// RelaxedDeclutterConfig returns minimal decluttering
// Use this for sparse charts or large displays
func RelaxedDeclutterConfig() *DeclutterConfig {
	return &DeclutterConfig{
		Enabled:               true,
		TryAlternatePositions: false, // Don't move labels
		MinSpacing:            0.0,   // Allow touching
		OverlapTolerance:      0.3,   // Allow 30% overlap
		AlwaysShowNavigation:  true,
		AlwaysShowSoundings:   true,
	}
}

// DeclutterText performs text label decluttering on render primitives
// Returns a new slice of primitives with colliding text removed or repositioned
// displayScale: current display scale denominator (e.g., 50000 for 1:50000), 0 if unknown
func DeclutterText(primitives []RenderPrimitive, objectClasses []string, config *DeclutterConfig, displayScale uint32) []RenderPrimitive {
	if config == nil {
		config = DefaultDeclutterConfig()
	}

	if !config.Enabled {
		return primitives
	}

	// Adjust overlap tolerance based on scale if enabled
	effectiveOverlapTolerance := config.OverlapTolerance
	if config.ScaleDependent && displayScale > 0 {
		// At overview scales (>100k), be much more permissive
		// At detailed scales (<10k), use base tolerance
		// Linear interpolation between 10k and 200k
		switch {
		case displayScale >= 200000:
			// Very overview - allow up to 60% overlap
			effectiveOverlapTolerance = 0.6
		case displayScale >= 100000:
			// Overview - allow up to 45% overlap
			effectiveOverlapTolerance = 0.45
		case displayScale >= 50000:
			// Medium overview - allow up to 30% overlap
			effectiveOverlapTolerance = 0.3
		case displayScale >= 20000:
			// Approach - use 20% overlap
			effectiveOverlapTolerance = 0.2
		case displayScale >= 10000:
			// Detailed - use base tolerance
			effectiveOverlapTolerance = config.OverlapTolerance
		default:
			// Very detailed - even stricter
			effectiveOverlapTolerance = config.OverlapTolerance * 0.5
		}
	}

	// Step 1: Extract text primitives with bounds
	textBounds := make([]*TextBounds, 0)
	textIndices := make([]int, 0) // Map bounds index to primitive index

	for i := range primitives {
		if primitives[i].Type == RenderPrimitiveText {
			objClass := ""
			if i < len(objectClasses) {
				objClass = objectClasses[i]
			}

			bounds := EstimateTextBounds(&primitives[i], objClass)
			if bounds != nil {
				bounds.PrimitiveIndex = i
				textBounds = append(textBounds, bounds)
				textIndices = append(textIndices, i)
			}
		}
	}

	if len(textBounds) == 0 {
		return primitives
	}

	// Step 2: Sort by priority (higher first), then view group, then display priority
	sort.Slice(textBounds, func(i, j int) bool {
		// Navigation features always come first
		if textBounds[i].ViewGroup == ViewGroupNavigation && textBounds[j].ViewGroup != ViewGroupNavigation {
			return true
		}
		if textBounds[i].ViewGroup != ViewGroupNavigation && textBounds[j].ViewGroup == ViewGroupNavigation {
			return false
		}

		// Then by view group priority
		if textBounds[i].ViewGroup != textBounds[j].ViewGroup {
			return textBounds[i].ViewGroup < textBounds[j].ViewGroup
		}

		// Then by display priority (higher = more important)
		return textBounds[i].Priority > textBounds[j].Priority
	})

	// Step 3: Place labels sequentially, tracking placed labels
	placed := make([]*TextBounds, 0, len(textBounds))

	for _, bounds := range textBounds {
		// Navigation features are always shown (if configured)
		if config.AlwaysShowNavigation && bounds.ViewGroup == ViewGroupNavigation {
			bounds.Placed = true
			placed = append(placed, bounds)
			continue
		}

		// Depth soundings are always shown (if configured)
		if config.AlwaysShowSoundings && bounds.ViewGroup == ViewGroupDepth {
			bounds.Placed = true
			placed = append(placed, bounds)
			continue
		}

		// Step 4: Try to place at original position
		if !hasCollision(bounds, placed, config.MinSpacing, effectiveOverlapTolerance) {
			bounds.Placed = true
			placed = append(placed, bounds)
			continue
		}

		// Step 5: Try alternate positions
		if config.TryAlternatePositions {
			foundPosition := false

			for _, altPos := range alternatePositions[1:] { // Skip center (already tried)
				// Update location
				bounds.CurrentLocation = Point{
					X: bounds.OriginalLocation.X + altPos.OffsetX,
					Y: bounds.OriginalLocation.Y + altPos.OffsetY,
				}

				// Recalculate bounds
				prim := &primitives[bounds.PrimitiveIndex]
				width := float64(len(prim.Text)) * prim.TextStyle.FontSize * 0.6 * (1.0 + prim.TextStyle.CharSpacing)
				height := prim.TextStyle.FontSize * 1.2
				if prim.TextStyle.OutlineColor != nil {
					padding := prim.TextStyle.OutlineWidth * 2
					width += padding
					height += padding
				}
				bounds.UpdateBounds(width, height, prim.TextStyle.HAlign, prim.TextStyle.VAlign)

				// Check if this position works
				if !hasCollision(bounds, placed, config.MinSpacing, effectiveOverlapTolerance) {
					bounds.Placed = true
					placed = append(placed, bounds)
					foundPosition = true
					break
				}
			}

			if !foundPosition {
				// Reset to original location (but mark as not placed)
				bounds.CurrentLocation = bounds.OriginalLocation
				bounds.Placed = false
			}
		} else {
			// Not trying alternates, just hide
			bounds.Placed = false
		}
	}

	// Step 6: Build result with placed labels and update positions
	result := make([]RenderPrimitive, 0, len(primitives))
	placedMap := make(map[int]*TextBounds) // Map primitive index to bounds

	for _, bounds := range textBounds {
		if bounds.Placed {
			placedMap[bounds.PrimitiveIndex] = bounds
		}
	}

	for i, prim := range primitives {
		if prim.Type != RenderPrimitiveText {
			// Keep all non-text primitives
			result = append(result, prim)
		} else if bounds, ok := placedMap[i]; ok {
			// Text primitive that was placed - update location if moved
			if bounds.CurrentLocation.X != bounds.OriginalLocation.X ||
				bounds.CurrentLocation.Y != bounds.OriginalLocation.Y {
				// Create copy with updated location
				updatedPrim := prim
				updatedPrim.Location = bounds.CurrentLocation
				result = append(result, updatedPrim)
			} else {
				// Keep original
				result = append(result, prim)
			}
		}
		// Text primitives not in placedMap are omitted (hidden)
	}

	return result
}

// hasCollision checks if bounds collides with any placed bounds
// overlapTolerance: 0.0 = no overlap, 0.5 = allow 50% overlap, 1.0 = disable collision
func hasCollision(bounds *TextBounds, placed []*TextBounds, minSpacing float64, overlapTolerance float64) bool {
	if overlapTolerance >= 1.0 {
		return false // Collision detection disabled
	}

	// Calculate how much we can shrink bounds based on overlap tolerance
	// tolerance of 0.15 means we shrink each dimension by 15% (allowing 15% overlap)
	width := bounds.MaxX - bounds.MinX
	height := bounds.MaxY - bounds.MinY
	shrinkX := width * overlapTolerance
	shrinkY := height * overlapTolerance

	// Create effective bounds: shrunk by overlap tolerance, expanded by min spacing
	expanded := &TextBounds{
		MinX: bounds.MinX - minSpacing + shrinkX,
		MaxX: bounds.MaxX + minSpacing - shrinkX,
		MinY: bounds.MinY - minSpacing + shrinkY,
		MaxY: bounds.MaxY + minSpacing - shrinkY,
	}

	// If bounds became inverted due to high tolerance, no collision
	if expanded.MinX >= expanded.MaxX || expanded.MinY >= expanded.MaxY {
		return false
	}

	for _, placedBounds := range placed {
		if expanded.Intersects(placedBounds) {
			return true
		}
	}

	return false
}

// GetDeclutterStats returns statistics about decluttering results
type DeclutterStats struct {
	TotalTextLabels   int
	PlacedAtOriginal  int
	PlacedAtAlternate int
	Hidden            int
	NavigationLabels  int // Always shown
}

// DeclutterTextWithStats performs decluttering and returns statistics
func DeclutterTextWithStats(primitives []RenderPrimitive, objectClasses []string, config *DeclutterConfig, displayScale uint32) ([]RenderPrimitive, *DeclutterStats) {
	stats := &DeclutterStats{}

	if config == nil {
		config = DefaultDeclutterConfig()
	}

	if !config.Enabled {
		// Count text primitives
		for _, prim := range primitives {
			if prim.Type == RenderPrimitiveText {
				stats.TotalTextLabels++
				stats.PlacedAtOriginal++
			}
		}
		return primitives, stats
	}

	// Extract text bounds
	textBounds := make([]*TextBounds, 0)

	for i := range primitives {
		if primitives[i].Type == RenderPrimitiveText {
			objClass := ""
			if i < len(objectClasses) {
				objClass = objectClasses[i]
			}

			bounds := EstimateTextBounds(&primitives[i], objClass)
			if bounds != nil {
				bounds.PrimitiveIndex = i
				textBounds = append(textBounds, bounds)
				stats.TotalTextLabels++

				if bounds.ViewGroup == ViewGroupNavigation {
					stats.NavigationLabels++
				}
			}
		}
	}

	result := DeclutterText(primitives, objectClasses, config, displayScale)

	// Calculate stats based on placed vs hidden
	placedCount := 0
	for _, prim := range result {
		if prim.Type == RenderPrimitiveText {
			placedCount++
		}
	}

	// Track which were moved
	originalLocations := make(map[int]Point)
	for _, bounds := range textBounds {
		originalLocations[bounds.PrimitiveIndex] = bounds.OriginalLocation
	}

	movedCount := 0
	for i, prim := range result {
		if prim.Type == RenderPrimitiveText {
			if orig, ok := originalLocations[i]; ok {
				if math.Abs(prim.Location.X-orig.X) > 0.01 || math.Abs(prim.Location.Y-orig.Y) > 0.01 {
					movedCount++
				}
			}
		}
	}

	stats.PlacedAtAlternate = movedCount
	stats.PlacedAtOriginal = placedCount - movedCount
	stats.Hidden = stats.TotalTextLabels - placedCount

	return result, stats
}
