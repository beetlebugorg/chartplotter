package s52

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
