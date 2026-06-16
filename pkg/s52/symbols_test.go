package s52

import (
	"testing"
)

// TestGetSymbol tests symbol retrieval
func TestGetSymbol(t *testing.T) {
	lib := &Library{
		symbols: map[string]*Symbol{
			"ACHARE51": {
				ID:          "ACHARE51",
				Description: "Anchorage area",
				ColorRef:    "ACHGRNBOUTLW",
				BoundingBox: BoundingBox{
					MinX: 0,
					MinY: 0,
					MaxX: 100,
					MaxY: 100,
				},
				PivotPoint: Point{X: 50, Y: 50},
				VectorCommands: []VectorCommand{
					{Type: "PD", Role: 'A', Points: []Point{{X: 0, Y: 0}, {X: 100, Y: 100}}, StrokeWidth: 1},
				},
			},
		},
	}

	symbol, err := lib.GetSymbol("ACHARE51")
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if symbol.ID != "ACHARE51" {
		t.Errorf("Expected ID 'ACHARE51', got '%s'", symbol.ID)
	}

	if symbol.Description != "Anchorage area" {
		t.Errorf("Expected description 'Anchorage area', got '%s'", symbol.Description)
	}

	if symbol.ColorRef != "ACHGRNBOUTLW" {
		t.Errorf("Expected ColorRef 'ACHGRNBOUTLW', got '%s'", symbol.ColorRef)
	}

	if symbol.BoundingBox.Width() != 100 {
		t.Errorf("Expected width 100, got %.2f", symbol.BoundingBox.Width())
	}

	if symbol.PivotPoint.X != 50 {
		t.Errorf("Expected pivot X 50, got %.2f", symbol.PivotPoint.X)
	}

	if len(symbol.Primitives) != 1 {
		t.Errorf("Expected 1 primitive, got %d", len(symbol.Primitives))
	}

	if symbol.Primitives[0].Type != PrimitiveLine {
		t.Errorf("Expected primitive type LINE, got '%s'", symbol.Primitives[0].Type)
	}
}

// TestGetSymbol_NotFound tests missing symbol
func TestGetSymbol_NotFound(t *testing.T) {
	lib := &Library{
		symbols: make(map[string]*Symbol),
	}

	_, err := lib.GetSymbol("NONEXISTENT")
	if err == nil {
		t.Error("Expected error for non-existent symbol")
	}
}

// TestListSymbols tests symbol listing
func TestListSymbols(t *testing.T) {
	lib := &Library{
		symbols: map[string]*Symbol{
			"ACHARE51": {ID: "ACHARE51"},
			"BOYLAT12": {ID: "BOYLAT12"},
			"LIGHTS11": {ID: "LIGHTS11"},
		},
	}

	symbols := lib.ListSymbols()

	if len(symbols) != 3 {
		t.Errorf("Expected 3 symbols, got %d", len(symbols))
	}

	// Check all symbols are present
	found := make(map[string]bool)
	for _, id := range symbols {
		found[id] = true
	}

	expected := []string{"ACHARE51", "BOYLAT12", "LIGHTS11"}
	for _, exp := range expected {
		if !found[exp] {
			t.Errorf("Expected to find symbol %s", exp)
		}
	}
}

// TestListSymbols_Empty tests empty symbol list
func TestListSymbols_Empty(t *testing.T) {
	lib := &Library{
		symbols: make(map[string]*Symbol),
	}

	symbols := lib.ListSymbols()
	if len(symbols) != 0 {
		t.Errorf("Expected empty list, got %d symbols", len(symbols))
	}
}

// TestGetPattern tests pattern retrieval
func TestGetPattern(t *testing.T) {
	lib := &Library{
		patterns: map[string]*Pattern{
			"DIAMOND1": {
				ID:          "DIAMOND1",
				Description: "Diamond pattern",
				ColorRef:    "ACHDGRNOUTLW",
				PatternType: "STGCON",
				SpacingX:    100,
				SpacingY:    100,
				VectorCommands: []VectorCommand{
					{Type: "POLYGON_FILLED", Role: 'A', Rings: [][]Point{{{X: 50, Y: 0}, {X: 100, Y: 50}, {X: 50, Y: 100}, {X: 0, Y: 50}}}, StrokeWidth: 1},
				},
			},
		},
	}

	pattern, err := lib.GetPattern("DIAMOND1")
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if pattern.ID != "DIAMOND1" {
		t.Errorf("Expected ID 'DIAMOND1', got '%s'", pattern.ID)
	}

	if pattern.Description != "Diamond pattern" {
		t.Errorf("Expected description 'Diamond pattern', got '%s'", pattern.Description)
	}

	if pattern.PatternType != "STGCON" {
		t.Errorf("Expected FillType 'STGCON', got '%s'", pattern.PatternType)
	}

	if pattern.SpacingX != 100 {
		t.Errorf("Expected SpacingX 100, got %d", pattern.SpacingX)
	}

	if len(pattern.Primitives) != 1 {
		t.Errorf("Expected 1 primitive, got %d", len(pattern.Primitives))
	}
}

// TestGetPattern_NotFound tests missing pattern
func TestGetPattern_NotFound(t *testing.T) {
	lib := &Library{
		patterns: make(map[string]*Pattern),
	}

	_, err := lib.GetPattern("NONEXISTENT")
	if err == nil {
		t.Error("Expected error for non-existent pattern")
	}
}

// TestListPatterns tests pattern listing
func TestListPatterns(t *testing.T) {
	lib := &Library{
		patterns: map[string]*Pattern{
			"DIAMOND1": {ID: "DIAMOND1"},
			"CROSS1":   {ID: "CROSS1"},
			"DQUALA21": {ID: "DQUALA21"},
		},
	}

	patterns := lib.ListPatterns()

	if len(patterns) != 3 {
		t.Errorf("Expected 3 patterns, got %d", len(patterns))
	}

	found := make(map[string]bool)
	for _, id := range patterns {
		found[id] = true
	}

	expected := []string{"DIAMOND1", "CROSS1", "DQUALA21"}
	for _, exp := range expected {
		if !found[exp] {
			t.Errorf("Expected to find pattern %s", exp)
		}
	}
}

// TestGetLineStyle tests line style retrieval
func TestGetLineStyle(t *testing.T) {
	lib := &Library{
		linestyles: map[string]*Linestyle{
			"SOLD": {
				ID:          "SOLD",
				Description: "Solid line",
			},
		},
	}

	style, err := lib.GetLineStyle("SOLD")
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if style.ID != "SOLD" {
		t.Errorf("Expected ID 'SOLD', got '%s'", style.ID)
	}

	if style.Description != "Solid line" {
		t.Errorf("Expected description 'Solid line', got '%s'", style.Description)
	}
}

// TestGetLineStyle_NotFound tests missing line style
func TestGetLineStyle_NotFound(t *testing.T) {
	lib := &Library{
		linestyles: make(map[string]*Linestyle),
	}

	_, err := lib.GetLineStyle("NONEXISTENT")
	if err == nil {
		t.Error("Expected error for non-existent line style")
	}
}

// TestListLineStyles tests line style listing
func TestListLineStyles(t *testing.T) {
	lib := &Library{
		linestyles: map[string]*Linestyle{
			"SOLD": {ID: "SOLD"},
			"DASH": {ID: "DASH"},
			"DOTT": {ID: "DOTT"},
		},
	}

	styles := lib.ListLineStyles()

	if len(styles) != 3 {
		t.Errorf("Expected 3 line styles, got %d", len(styles))
	}

	found := make(map[string]bool)
	for _, id := range styles {
		found[id] = true
	}

	expected := []string{"SOLD", "DASH", "DOTT"}
	for _, exp := range expected {
		if !found[exp] {
			t.Errorf("Expected to find line style %s", exp)
		}
	}
}

// TestPrimitiveConversion tests HPGL to primitive conversion
func TestPrimitiveConversion(t *testing.T) {
	// Test LINE primitive from PD command
	pdCmd := VectorCommand{
		Type:        "PD",
		Role:        'A',
		Points:      []Point{{X: 0, Y: 0}, {X: 100, Y: 100}},
		StrokeWidth: 2,
	}

	// Test CIRCLE primitive from CI command
	ciCmd := VectorCommand{
		Type:        "CI",
		Role:        'B',
		Points:      []Point{{X: 50, Y: 0}}, // Radius stored as X
		Center:      &Point{X: 100, Y: 100},
		StrokeWidth: 1,
	}

	// Test FILL primitive from POLYGON_FILLED
	fillCmd := VectorCommand{
		Type: "POLYGON_FILLED",
		Role: 'C',
		Rings: [][]Point{
			{{X: 0, Y: 0}, {X: 100, Y: 0}, {X: 100, Y: 100}, {X: 0, Y: 100}},
		},
		StrokeWidth: 1,
	}

	primitives := parseHPGLtoPrimitives([]VectorCommand{pdCmd, ciCmd, fillCmd})

	if len(primitives) != 3 {
		t.Fatalf("Expected 3 primitives, got %d", len(primitives))
	}

	// Check LINE primitive
	if primitives[0].Type != PrimitiveLine {
		t.Errorf("Expected LINE primitive, got %s", primitives[0].Type)
	}
	if primitives[0].ColorRole != 'A' {
		t.Errorf("Expected role A, got %c", primitives[0].ColorRole)
	}
	if len(primitives[0].Path) != 2 {
		t.Errorf("Expected 2 points in path, got %d", len(primitives[0].Path))
	}

	// Check CIRCLE primitive
	if primitives[1].Type != PrimitiveCircle {
		t.Errorf("Expected CIRCLE primitive, got %s", primitives[1].Type)
	}
	if primitives[1].Radius != 50 {
		t.Errorf("Expected radius 50, got %.2f", primitives[1].Radius)
	}
	if primitives[1].Center == nil {
		t.Fatal("Expected center to be non-nil")
	}
	if primitives[1].Center.X != 100 || primitives[1].Center.Y != 100 {
		t.Errorf("Expected center (100,100), got (%.2f,%.2f)", primitives[1].Center.X, primitives[1].Center.Y)
	}

	// Check FILL primitive
	if primitives[2].Type != PrimitiveFill {
		t.Errorf("Expected FILL primitive, got %s", primitives[2].Type)
	}
	if len(primitives[2].Rings) != 1 {
		t.Errorf("Expected 1 ring, got %d", len(primitives[2].Rings))
	}
	if len(primitives[2].Rings[0]) != 4 {
		t.Errorf("Expected 4 points in ring, got %d", len(primitives[2].Rings[0]))
	}
}
