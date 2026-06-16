// Package v1 provides a clean API for S-52 presentation library data and lookups.
//
// This library parses IHO S-52 DAI files and provides access to:
//   - Color definitions (day/dusk/night schemes)
//   - Symbol definitions (point symbols with vector commands)
//   - Line style definitions (simple and complex line patterns)
//   - Area pattern definitions (fill patterns)
//   - Lookup tables (feature → symbology instruction mapping)
//   - Display instructions (SY, LC, LS, AC, AP, TX, CS commands)
//
// This is a pure data/lookup library. It does NOT render - it only provides
// the data needed for rendering. Separate rendering libraries consume this data.
//
// S-52 PresLib Edition 4.0.0 (IHO Standard)
package s52

import (
	"fmt"
	"sort"
)

// Library represents a loaded S-52 presentation library
type Library struct {
	// Internal data structures (not exposed to users)
	symbols      map[string]*Symbol
	linestyles   map[string]*Linestyle
	patterns     map[string]*Pattern
	lookupTables []*LookupTable // Use slice for deterministic iteration order
	colorDB      *ColorDatabase

	// Cached color maps for fast lookup (prevents map allocations on every GetColor call)
	dayColorCache   map[string]*Color
	duskColorCache  map[string]*Color
	nightColorCache map[string]*Color

	// Metadata
	libraryID string
	version   string

	// Display preferences
	depthUnit DepthUnit // Preferred depth unit for soundings/depth display
}

// LoadLibrary loads an S-52 presentation library from a DAI file
func LoadLibrary(daiFilePath string) (*Library, error) {
	// Parse DAI file
	config := &ParserConfig{}
	p := NewParser(config)
	parseResult, err := p.ParseFile(daiFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to parse DAI file: %w", err)
	}

	// Load color database
	colorDB := NewColorDatabase()
	if err := colorDB.ParseDAIColors(daiFilePath); err != nil {
		return nil, fmt.Errorf("failed to parse colors: %w", err)
	}

	return buildLibrary(parseResult, colorDB)
}

// LoadLibraryFromBytes loads an S-52 presentation library from DAI file bytes
// This is useful for WASM environments where filesystem access isn't available
func LoadLibraryFromBytes(daiBytes []byte) (*Library, error) {
	// Parse DAI data
	config := &ParserConfig{}
	p := NewParser(config)
	parseResult, err := p.ParseBytes(daiBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse DAI data: %w", err)
	}

	// Load color database from bytes
	colorDB := NewColorDatabase()
	if err := colorDB.ParseDAIColorsFromBytes(daiBytes); err != nil {
		return nil, fmt.Errorf("failed to parse colors: %w", err)
	}

	return buildLibrary(parseResult, colorDB)
}

// buildColorCache converts a color map to pointer map once at load time
func buildColorCache(internalMap map[string]Color) map[string]*Color {
	result := make(map[string]*Color, len(internalMap))
	for token, color := range internalMap {
		c := color // Copy
		result[token] = &c
	}
	return result
}

func buildLibrary(parseResult *ParseResult, colorDB *ColorDatabase) (*Library, error) {

	// Convert lookup tables map to slice
	// Build slice with keys sorted to ensure deterministic iteration
	// (Go map iteration is intentionally randomized)
	lookupKeys := make([]string, 0, len(parseResult.LookupTables))
	for key := range parseResult.LookupTables {
		lookupKeys = append(lookupKeys, key)
	}
	sort.Strings(lookupKeys)

	lookupSlice := make([]*LookupTable, 0, len(parseResult.LookupTables))
	for _, key := range lookupKeys {
		lookupSlice = append(lookupSlice, parseResult.LookupTables[key])
	}

	lib := &Library{
		symbols:      parseResult.Symbols,
		linestyles:   parseResult.Linestyles,
		patterns:     parseResult.Patterns,
		lookupTables: lookupSlice,
		colorDB:      colorDB,
		depthUnit:    DepthUnitFeet, // Default to feet
	}

	// Build color caches once at load time to avoid repeated map allocations
	lib.dayColorCache = buildColorCache(colorDB.DayColors)
	lib.duskColorCache = buildColorCache(colorDB.DuskColors)
	lib.nightColorCache = buildColorCache(colorDB.NightColors)

	// Extract metadata if available
	// TODO: Extract from Header when available in parseResult

	return lib, nil
}

// LibraryID returns the presentation library identifier
func (l *Library) LibraryID() string {
	return l.libraryID
}

// Version returns the presentation library version
func (l *Library) Version() string {
	return l.version
}

// DepthUnit returns the currently configured depth unit for soundings/depth display
func (l *Library) DepthUnit() DepthUnit {
	return l.depthUnit
}

// SetDepthUnit changes the depth unit preference on the fly without reloading the library
func (l *Library) SetDepthUnit(unit DepthUnit) {
	l.depthUnit = unit
}

// Stats returns statistics about the library contents
func (l *Library) Stats() LibraryStats {
	dayColors := 0
	duskColors := 0
	nightColors := 0

	if l.colorDB != nil {
		dayColors = len(l.colorDB.DayColors)
		duskColors = len(l.colorDB.DuskColors)
		nightColors = len(l.colorDB.NightColors)
	}

	return LibraryStats{
		Symbols:      len(l.symbols),
		LineStyles:   len(l.linestyles),
		Patterns:     len(l.patterns),
		LookupTables: len(l.lookupTables),
		DayColors:    dayColors,
		DuskColors:   duskColors,
		NightColors:  nightColors,
	}
}

// LibraryStats contains statistics about library contents
type LibraryStats struct {
	Symbols      int
	LineStyles   int
	Patterns     int
	LookupTables int
	DayColors    int
	DuskColors   int
	NightColors  int
}
