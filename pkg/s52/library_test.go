package s52

import (
	"testing"
)

// TestLibraryBasics tests basic library loading and stats
func TestLibraryBasics(t *testing.T) {
	lib := loadTestLibrary(t)

	stats := lib.Stats()

	// Basic sanity checks
	if stats.Symbols == 0 {
		t.Error("Expected some symbols")
	}

	if stats.LookupTables == 0 {
		t.Error("Expected some lookup tables")
	}

	if stats.DayColors == 0 {
		t.Error("Expected some day colors")
	}

	t.Logf("Library stats: %+v", stats)
}

// TestSymbolAccess tests symbol data access
func TestSymbolAccess(t *testing.T) {
	lib := loadTestLibrary(t)

	// List symbols
	symbols := lib.ListSymbols()
	if len(symbols) == 0 {
		t.Error("Expected some symbols")
	}

	// Get a specific symbol (if it exists)
	if len(symbols) > 0 {
		symbolID := symbols[0]
		symbol, err := lib.GetSymbol(symbolID)
		if err != nil {
			t.Errorf("Failed to get symbol %s: %v", symbolID, err)
		}

		if symbol.ID != symbolID {
			t.Errorf("Expected symbol ID %s, got %s", symbolID, symbol.ID)
		}

		t.Logf("Symbol %s has %d primitives", symbol.ID, len(symbol.Primitives))
	}
}

// TestColorAccess tests color lookups
func TestColorAccess(t *testing.T) {
	lib := loadTestLibrary(t)

	// List colors
	dayColors := lib.ListColors(ColorSchemeDay)
	if len(dayColors) == 0 {
		t.Error("Expected some day colors")
	}

	// Get a specific color (if it exists)
	if len(dayColors) > 0 {
		token := dayColors[0]

		// Test all schemes
		schemes := []ColorScheme{ColorSchemeDay, ColorSchemeDusk, ColorSchemeNight}
		for _, scheme := range schemes {
			hex, err := lib.GetColorHex(token, scheme)
			if err != nil {
				t.Errorf("Failed to get color %s for scheme %s: %v", token, scheme, err)
			}

			if hex == "" {
				t.Errorf("Expected non-empty hex color for %s/%s", token, scheme)
			}

			t.Logf("Color %s (%s): %s", token, scheme, hex)
		}
	}
}

// TestObjectClasses verifies the library exposes its lookup-table object classes.
func TestObjectClasses(t *testing.T) {
	lib := loadTestLibrary(t)

	// List object classes
	classes := lib.ListObjectClasses()
	if len(classes) == 0 {
		t.Error("Expected some object classes")
	}
}

// TestPatternAccess tests pattern data access
func TestPatternAccess(t *testing.T) {
	lib := loadTestLibrary(t)

	patterns := lib.ListPatterns()
	if len(patterns) == 0 {
		t.Error("Expected some patterns")
	}

	// Get a specific pattern
	if len(patterns) > 0 {
		patternID := patterns[0]
		pattern, err := lib.GetPattern(patternID)
		if err != nil {
			t.Errorf("Failed to get pattern %s: %v", patternID, err)
		}

		if pattern.ID != patternID {
			t.Errorf("Expected pattern ID %s, got %s", patternID, pattern.ID)
		}

		t.Logf("Pattern %s: spacing=%dx%d, primitives=%d",
			pattern.ID, pattern.SpacingX, pattern.SpacingY, len(pattern.Primitives))
	}
}

// TestLibraryMetadata tests library metadata
func TestLibraryMetadata(t *testing.T) {
	lib := loadTestLibrary(t)

	id := lib.LibraryID()
	version := lib.Version()

	t.Logf("Library ID: %s", id)
	t.Logf("Version: %s", version)
}

// TestInvalidFile tests error handling
func TestInvalidFile(t *testing.T) {
	_, err := LoadLibrary("/nonexistent/file.dai")
	if err == nil {
		t.Error("Expected error for nonexistent file")
	}
}

// TestColorSchemeConstants tests color scheme constants
func TestColorSchemeConstants(t *testing.T) {
	if ColorSchemeDay != "day" {
		t.Errorf("Expected ColorSchemeDay='day', got '%s'", ColorSchemeDay)
	}

	if ColorSchemeDusk != "dusk" {
		t.Errorf("Expected ColorSchemeDusk='dusk', got '%s'", ColorSchemeDusk)
	}

	if ColorSchemeNight != "night" {
		t.Errorf("Expected ColorSchemeNight='night', got '%s'", ColorSchemeNight)
	}
}

// TestDepthUnitConfiguration tests depth unit getter/setter
func TestDepthUnitConfiguration(t *testing.T) {
	lib := loadTestLibrary(t)

	// Test default is feet
	if lib.DepthUnit() != DepthUnitFeet {
		t.Errorf("Expected default depth unit to be feet, got %v", lib.DepthUnit())
	}

	// Test changing to meters
	lib.SetDepthUnit(DepthUnitMeters)
	if lib.DepthUnit() != DepthUnitMeters {
		t.Errorf("Expected depth unit to be meters after setting, got %v", lib.DepthUnit())
	}

	// Test changing to fathoms
	lib.SetDepthUnit(DepthUnitFathoms)
	if lib.DepthUnit() != DepthUnitFathoms {
		t.Errorf("Expected depth unit to be fathoms after setting, got %v", lib.DepthUnit())
	}

	// Test changing back to feet
	lib.SetDepthUnit(DepthUnitFeet)
	if lib.DepthUnit() != DepthUnitFeet {
		t.Errorf("Expected depth unit to be feet after setting, got %v", lib.DepthUnit())
	}

	t.Logf("Depth unit configuration test passed")
}
