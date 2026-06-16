package s52

// MarinerSettings contains user-configurable display parameters for chart rendering.
// These settings affect how Conditional Symbology (CS) procedures render features.
//
// S-52 Section 9: Mariner's Selections
type MarinerSettings struct {
	// Safety Contour (meters) - The contour selected by the mariner which is of
	// special significance for the safe navigation of the vessel.
	// Typically 30m. Range: 0-99m in whole meters.
	// Used by: DEPARE03, DEPCNT03
	SafetyContour float64

	// Safety Depth (meters) - The depth selected by the mariner below which
	// soundings are considered dangerous and displayed in bold.
	// Typically 28m. Range: 0-99m in 0.1m increments.
	// Used by: DEPARE03, SOUNDG03, OBSTRN07
	SafetyDepth float64

	// Shallow Contour (meters) - The depth contour which distinguishes between
	// shallow water and deeper water. Typically 10m.
	// Used by: DEPARE03 (via SEABED01)
	ShallowContour float64

	// Deep Contour (meters) - The depth contour which distinguishes between
	// medium depth water and deep water. Typically 30m.
	// Used by: DEPARE03 (via SEABED01)
	DeepContour float64

	// Two Shades - If true, use only two depth shades (shallow/deep).
	// If false, use four depth shades (very shallow/shallow/medium/deep).
	// Used by: DEPARE03 (via SEABED01)
	TwoShades bool

	// Shallow Pattern - If true, display diagonal line pattern in shallow areas.
	// Used by: DEPARE03
	ShallowPattern bool

	// Display Category - Controls which features are displayed:
	//   DisplayBase (6) - minimum safe navigation
	//   DisplayStandard (7) - typical navigation (default)
	//   DisplayOther (8) - all features
	// S-52 Section 10.2.1
	DisplayCategory int

	// Color Scheme - Day, Dusk, or Night color palette.
	// Values: ColorSchemeDay, ColorSchemeDusk, ColorSchemeNight
	// S-52 Section 5
	ColorScheme ColorScheme

	// Symbolized Boundaries - Display ECDIS symbol boundaries (IMO requirement).
	// S-52 Section 11.3.1
	SymbolizedBoundaries bool

	// Simplified Points - Use simplified point symbols where available.
	// S-52 Section 11.3.2
	SimplifiedPoints bool

	// Show Isolated Dangers in Shallow Water - Display ISODGR01 for features
	// in water shallower than safety contour (between 0m and safety contour).
	// Used by: UDWHAZ05 (called by OBSTRN07, WRECKS05)
	ShowIsolatedDangersInShallowWater bool

	// Show Light Descriptions - Display light characteristics text.
	// Used by: LIGHTS06
	ShowLightDescriptions bool

	// Show Full Length Sector Lines - Extend light sector leg lines to nominal range (VALNMR).
	// If false, leg lines are limited to 25mm to avoid clutter (default).
	// S-52 CSP LIGHTS06 page 23 note 1
	// Used by: LIGHTS06
	ShowFullLengthSectorLines bool

	// Safety Contour Labels - Display depth labels on safety contours.
	// Used by: DEPCNT03
	SafetyContourLabels bool

	// Enable SCAMIN - If false, ignore SCAMIN attribute (show all features).
	// S-52 Section 10.4.2 requires this mariner override option.
	EnableSCAMIN bool

	// Display Scale - Current display scale denominator (e.g., 20000 for 1:20,000).
	// If 0, no SCAMIN filtering is applied. Used for scale-dependent display of features.
	// S-52 Section 10.4
	DisplayScale uint32

	// Depth Units - Unit of measurement for depth/sounding display.
	// Options: DepthUnitMeters, DepthUnitFeet, DepthUnitFathoms
	// Used by: SOUNDG03 (via SNDFRM04), depth contour labels
	DepthUnits DepthUnit

	// FontScale - Scale factor for text size as a percentage.
	// 100 = S-52 specification size, 110 = 10% larger, 90 = 10% smaller.
	// This allows the mariner to adjust text size for readability while
	// maintaining the relative sizing specified in S-52.
	FontScale int

	// DeclutterConfig - Controls text label decluttering behavior.
	// If nil, uses default configuration (enabled with alternate positions).
	DeclutterConfig *DeclutterConfig
}

// Display category constants (S-52 Section 10.2.1)
const (
	DisplayBase     = 6 // Minimum features for safe navigation
	DisplayStandard = 7 // Standard display (typical)
	DisplayOther    = 8 // All other features
)

// Display category string values (from DAI DISC records)
const (
	DisplayCategoryBase     = "DisplayBase"
	DisplayCategoryStandard = "Standard"
	DisplayCategoryOther    = "Other"
	DisplayCategoryMariners = "Mariners"
)

// defaultMarinerSettings returns the built-in default values for mariner settings.
func defaultMarinerSettings() *MarinerSettings {
	return &MarinerSettings{
		SafetyContour:                     10.0,  // 10m (typical for small craft, matches OpenCPN default)
		SafetyDepth:                       10.0,  // 10m (matches safety contour)
		ShallowContour:                    2.0,   // 2m (very shallow water warning, matches OpenCPN)
		DeepContour:                       30.0,  // 30m (deep water, no hazards expected)
		TwoShades:                         false, // Use 4 depth shades
		ShallowPattern:                    false, // No pattern by default
		DisplayCategory:                   DisplayStandard,
		ColorScheme:                       ColorSchemeDay,
		SymbolizedBoundaries:              true,          // IMO requirement
		SimplifiedPoints:                  false,         // Full symbols by default
		ShowIsolatedDangersInShallowWater: false,         // Off by default
		ShowLightDescriptions:             true,          // Full light info
		ShowFullLengthSectorLines:         false,         // 25mm default (avoid clutter per S-52)
		SafetyContourLabels:               false,         // No labels by default
		EnableSCAMIN:                      true,          // SCAMIN filtering enabled by default
		DisplayScale:                      0,             // No scale filtering by default (0 = show all)
		DepthUnits:                        DepthUnitFeet, // Feet by default (US preference)
		FontScale:                         100,           // 100% = S-52 spec size
	}
}

// DefaultMarinerSettings returns sensible default values for mariner settings.
// Based on S-52 recommended defaults and typical usage.
//
// NOTE: XDG config loading is available via FindMarinerSettings() + LoadMarinerConfig(),
// but not done automatically here to avoid performance issues. Applications should
// explicitly load from XDG if desired.
func DefaultMarinerSettings() *MarinerSettings {
	return defaultMarinerSettings()
}
