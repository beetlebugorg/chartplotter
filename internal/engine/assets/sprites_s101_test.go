package assets

import (
	"bytes"
	"encoding/json"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func s101SymbolsDir(t *testing.T) string {
	t.Helper()
	dir := os.Getenv("S101_CATALOG")
	if dir == "" {
		dir = "/home/jcollins/Projects/s101-portrayal-catalogue/PortrayalCatalog"
	}
	sym := filepath.Join(dir, "Symbols")
	if _, err := os.Stat(filepath.Join(sym, "BCNCAR01.svg")); err != nil {
		t.Skipf("S-101 symbols not present (%s); set S101_CATALOG to run", sym)
	}
	return sym
}

func TestSpriteAtlasS101(t *testing.T) {
	sym := s101SymbolsDir(t)
	jsonBytes, pngBytes, err := SpriteAtlasS101(sym, filepath.Join(sym, "daySvgStyle.css"))
	if err != nil {
		t.Fatal(err)
	}

	// Atlas PNG decodes and has positive dimensions.
	img, err := png.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		t.Fatalf("atlas PNG: %v", err)
	}
	if img.Bounds().Dx() != s101AtlasWidth || img.Bounds().Dy() < 1 || img.Bounds().Dy() > 4096 {
		t.Errorf("atlas dims = %v (want %d wide, <=4096 tall)", img.Bounds(), s101AtlasWidth)
	}

	// JSON parses; _meta present; a known symbol is packed with a sane cell.
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(jsonBytes, &doc); err != nil {
		t.Fatalf("sprites.json: %v", err)
	}
	if _, ok := doc["_meta"]; !ok {
		t.Error("missing _meta")
	}
	var c struct {
		W      uint32  `json:"w"`
		H      uint32  `json:"h"`
		PivotX float64 `json:"pivot_x"`
		PivotY float64 `json:"pivot_y"`
	}
	raw, ok := doc["BCNCAR01"]
	if !ok {
		t.Fatal("BCNCAR01 not in atlas")
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	if c.W == 0 || c.H == 0 || c.PivotX <= 0 {
		t.Errorf("BCNCAR01 cell looks wrong: %+v", c)
	}
	t.Logf("atlas %dx%d; BCNCAR01 w=%d h=%d pivot=(%.1f,%.1f)", img.Bounds().Dx(), img.Bounds().Dy(), c.W, c.H, c.PivotX, c.PivotY)
}
