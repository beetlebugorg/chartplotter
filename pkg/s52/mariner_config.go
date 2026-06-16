package s52

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// MarinerConfig represents the YAML structure for mariner settings
type MarinerConfig struct {
	// Safety settings (meters)
	SafetyContour  *float64 `yaml:"safety_contour"`
	SafetyDepth    *float64 `yaml:"safety_depth"`
	ShallowContour *float64 `yaml:"shallow_contour"`
	DeepContour    *float64 `yaml:"deep_contour"`

	// Display options
	TwoShades       *bool   `yaml:"two_shades"`
	ShallowPattern  *bool   `yaml:"shallow_pattern"`
	ColorScheme     *string `yaml:"color_scheme"`     // "day", "dusk", "night"
	DisplayCategory *string `yaml:"display_category"` // "base", "standard", "all"

	// Symbology options
	SymbolizedBoundaries      *bool `yaml:"symbolized_boundaries"`
	SimplifiedPoints          *bool `yaml:"simplified_points"`
	ShowLightDescriptions     *bool `yaml:"show_light_descriptions"`
	ShowFullLengthSectorLines *bool `yaml:"show_full_length_sector_lines"`
	SafetyContourLabels       *bool `yaml:"safety_contour_labels"`

	// Isolated dangers
	ShowIsolatedDangersInShallowWater *bool `yaml:"show_isolated_dangers_in_shallow_water"`

	// Scale filtering
	EnableSCAMIN *bool   `yaml:"enable_scamin"`
	DisplayScale *uint32 `yaml:"display_scale"`

	// Depth units
	DepthUnits *string `yaml:"depth_units"` // "meters", "feet", "fathoms"

	// Font scaling
	FontScale *int `yaml:"font_scale"` // Percentage: 100=spec, 110=10% larger
}

// LoadMarinerConfig loads mariner settings from a YAML file
func LoadMarinerConfig(path string) (*MarinerSettings, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}

	var config MarinerConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("parse YAML: %w", err)
	}

	// Start with defaults
	settings := DefaultMarinerSettings()

	// Apply overrides from config (only non-nil values)
	if config.SafetyContour != nil {
		settings.SafetyContour = *config.SafetyContour
	}
	if config.SafetyDepth != nil {
		settings.SafetyDepth = *config.SafetyDepth
	}
	if config.ShallowContour != nil {
		settings.ShallowContour = *config.ShallowContour
	}
	if config.DeepContour != nil {
		settings.DeepContour = *config.DeepContour
	}
	if config.TwoShades != nil {
		settings.TwoShades = *config.TwoShades
	}
	if config.ShallowPattern != nil {
		settings.ShallowPattern = *config.ShallowPattern
	}
	if config.SymbolizedBoundaries != nil {
		settings.SymbolizedBoundaries = *config.SymbolizedBoundaries
	}
	if config.SimplifiedPoints != nil {
		settings.SimplifiedPoints = *config.SimplifiedPoints
	}
	if config.ShowLightDescriptions != nil {
		settings.ShowLightDescriptions = *config.ShowLightDescriptions
	}
	if config.ShowFullLengthSectorLines != nil {
		settings.ShowFullLengthSectorLines = *config.ShowFullLengthSectorLines
	}
	if config.SafetyContourLabels != nil {
		settings.SafetyContourLabels = *config.SafetyContourLabels
	}
	if config.ShowIsolatedDangersInShallowWater != nil {
		settings.ShowIsolatedDangersInShallowWater = *config.ShowIsolatedDangersInShallowWater
	}
	if config.EnableSCAMIN != nil {
		settings.EnableSCAMIN = *config.EnableSCAMIN
	}
	if config.DisplayScale != nil {
		settings.DisplayScale = *config.DisplayScale
	}

	// Parse color scheme
	if config.ColorScheme != nil {
		switch *config.ColorScheme {
		case "day":
			settings.ColorScheme = ColorSchemeDay
		case "dusk":
			settings.ColorScheme = ColorSchemeDusk
		case "night":
			settings.ColorScheme = ColorSchemeNight
		default:
			return nil, fmt.Errorf("invalid color_scheme: %s (must be 'day', 'dusk', or 'night')", *config.ColorScheme)
		}
	}

	// Parse display category
	if config.DisplayCategory != nil {
		switch *config.DisplayCategory {
		case "base":
			settings.DisplayCategory = DisplayBase
		case "standard":
			settings.DisplayCategory = DisplayStandard
		case "all":
			settings.DisplayCategory = DisplayOther
		default:
			return nil, fmt.Errorf("invalid display_category: %s (must be 'base', 'standard', or 'all')", *config.DisplayCategory)
		}
	}

	// Parse depth units
	if config.DepthUnits != nil {
		switch *config.DepthUnits {
		case "meters", "m":
			settings.DepthUnits = DepthUnitMeters
		case "feet", "ft":
			settings.DepthUnits = DepthUnitFeet
		case "fathoms", "fath":
			settings.DepthUnits = DepthUnitFathoms
		default:
			return nil, fmt.Errorf("invalid depth_units: %s (must be 'meters', 'feet', or 'fathoms')", *config.DepthUnits)
		}
	}

	// Font scale
	if config.FontScale != nil {
		settings.FontScale = *config.FontScale
	}

	return settings, nil
}
