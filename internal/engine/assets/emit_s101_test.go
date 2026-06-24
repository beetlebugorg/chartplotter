package assets

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestEmitS101(t *testing.T) {
	pc := os.Getenv("S101_CATALOG")
	if pc == "" {
		pc = "/home/jcollins/Projects/s101-portrayal-catalogue/PortrayalCatalog"
	}
	if _, err := os.Stat(filepath.Join(pc, "Symbols", "BCNCAR01.svg")); err != nil {
		t.Skipf("S-101 catalogue not present; set S101_CATALOG")
	}

	dir := t.TempDir()
	files, err := EmitS101(pc, "daySvgStyle.css", dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 6 {
		t.Errorf("wrote %d files, want 6", len(files))
	}
	for _, name := range []string{"colortables.json", "linestyles.json", "sprite.json", "sprite.png", "patterns.json", "patterns.png"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("missing %s", name)
		}
	}

	// ACHARE51 (anchorage boundary: dashed magenta + placed symbols) must come
	// through with a real period and its embedded symbols.
	lsData, err := os.ReadFile(filepath.Join(dir, "linestyles.json"))
	if err != nil {
		t.Fatal(err)
	}
	var lsDoc map[string]struct {
		PeriodPx   float64 `json:"period_px"`
		ColorToken string  `json:"color_token"`
		Symbols    []struct {
			N string `json:"n"`
		} `json:"symbols"`
	}
	if err := json.Unmarshal(lsData, &lsDoc); err != nil {
		t.Fatalf("linestyles.json: %v", err)
	}
	if ach, ok := lsDoc["ACHARE51"]; !ok {
		t.Error("ACHARE51 missing from linestyles.json")
	} else if ach.PeriodPx <= 0 || ach.ColorToken != "CHMGD" || len(ach.Symbols) == 0 {
		t.Errorf("ACHARE51 looks wrong: %+v", ach)
	}

	// The S-101 colour tables carry a populated Day/Dusk/Night palette with
	// well-formed #rrggbb hex (colorTablesJSONFromProfile over the colour
	// profile). DEPDW (deep-water fill) is a representative always-present token.
	s101CT := loadColorTables(t, filepath.Join(dir, "colortables.json"))
	for _, scheme := range []string{"day", "dusk", "night"} {
		pal := s101CT[scheme]
		if len(pal) == 0 {
			t.Errorf("%s: empty palette", scheme)
			continue
		}
		for tok, hex := range pal {
			if len(hex) != 7 || hex[0] != '#' {
				t.Errorf("%s/%s: malformed hex %q", scheme, tok, hex)
			}
		}
		if _, ok := pal["DEPDW"]; !ok {
			t.Errorf("%s: DEPDW token missing", scheme)
		}
	}
}

func loadColorTables(t *testing.T, path string) map[string]map[string]string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var ct map[string]map[string]string
	if err := json.Unmarshal(data, &ct); err != nil {
		t.Fatal(err)
	}
	return ct
}
