package assets

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/beetlebugorg/chartplotter/pkg/s52"
	"github.com/beetlebugorg/chartplotter/pkg/s52/preslib"
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
	if len(files) != 4 {
		t.Errorf("wrote %d files, want 4", len(files))
	}
	for _, name := range []string{"colortables.json", "linestyles.json", "sprite.json", "sprite.png"} {
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

	// The S-101 colour tables must match what the S-52 library emits (the
	// colours are byte-identical — see cmd/s101-color-diff).
	s101CT := loadColorTables(t, filepath.Join(dir, "colortables.json"))
	lib, err := s52.LoadLibraryFromBytes(preslib.DAI)
	if err != nil {
		t.Fatal(err)
	}
	s52Bytes, err := ColorTablesJSON(lib)
	if err != nil {
		t.Fatal(err)
	}
	var s52CT map[string]map[string]string
	if err := json.Unmarshal(s52Bytes, &s52CT); err != nil {
		t.Fatal(err)
	}
	for _, scheme := range []string{"day", "dusk", "night"} {
		if len(s101CT[scheme]) != len(s52CT[scheme]) {
			t.Errorf("%s: %d tokens vs S-52 %d", scheme, len(s101CT[scheme]), len(s52CT[scheme]))
		}
		for tok, hex := range s52CT[scheme] {
			if s101CT[scheme][tok] != hex {
				t.Errorf("%s/%s: S-101 %q vs S-52 %q", scheme, tok, s101CT[scheme][tok], hex)
			}
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
