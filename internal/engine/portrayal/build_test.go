package portrayal

import (
	"math"
	"testing"

	"github.com/beetlebugorg/chartplotter-go/pkg/s52"
	"github.com/beetlebugorg/chartplotter-go/pkg/s52/preslib"
	"github.com/beetlebugorg/chartplotter-go/pkg/s57"
)

const goldenCell = "../../../testdata/US4MD81M.000"

func loadLib(t *testing.T) *s52.Library {
	t.Helper()
	lib, err := s52.LoadLibraryFromBytes(preslib.DAI)
	if err != nil {
		t.Fatalf("LoadLibraryFromBytes: %v", err)
	}
	return lib
}

// TestBuildGoldenCell runs the full pipeline (s57 parse -> s52 lookup -> portrayal
// IR) over every feature of the Annapolis cell and asserts a sane primitive mix.
func TestBuildGoldenCell(t *testing.T) {
	lib := loadLib(t)
	chart, err := s57.Parse(goldenCell)
	if err != nil {
		t.Fatalf("parse golden cell: %v", err)
	}
	mariner := s52.DefaultMarinerSettings()

	counts := map[string]int{}
	var features, portrayed int
	for _, f := range chart.Features() {
		features++
		fb, ok := BuildFeature(lib, mariner, &f)
		if !ok {
			continue
		}
		if len(fb.Primitives) > 0 {
			portrayed++
		}
		for _, p := range fb.Primitives {
			switch pr := p.(type) {
			case FillPolygon:
				counts["fill"]++
			case StrokeLine:
				counts["stroke"]++
			case SymbolCall:
				counts["symbol"]++
				if isSoundingDigit(pr.SymbolName) && !math.IsNaN(float64(pr.SoundingDepthM)) {
					counts["sounding_digit"]++
				}
			case PatternFill:
				counts["pattern"]++
			case LinePattern:
				counts["linepattern"]++
			case DrawText:
				counts["text"]++
			case SectorLight:
				counts["sector"]++
			}
		}
	}

	t.Logf("features=%d portrayed=%d primitives=%v", features, portrayed, counts)

	if features < 1000 {
		t.Fatalf("expected the golden cell to parse many features, got %d", features)
	}
	if portrayed == 0 {
		t.Fatal("no features produced primitives — lookup/portrayal is broken")
	}
	// DEPARE/LNDARE area fills, soundings, and at least some text labels are all
	// present on this harbour cell.
	for _, kind := range []string{"fill", "symbol", "text"} {
		if counts[kind] == 0 {
			t.Errorf("expected at least one %s primitive, got 0", kind)
		}
	}
	if counts["sounding_digit"] == 0 {
		t.Error("expected sounding-digit symbols carrying depth, got 0")
	}
}

// TestSoundingDepthCarried checks the bake-once invariant: a sounding symbol
// carries its depth so the client can do SNDFRM04 without a re-bake.
func TestSoundingDepthCarried(t *testing.T) {
	lib := loadLib(t)
	chart, err := s57.Parse(goldenCell)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	mariner := s52.DefaultMarinerSettings()
	for _, f := range chart.Features() {
		if f.ObjectClass() != "SOUNDG" {
			continue
		}
		fb, ok := BuildFeature(lib, mariner, &f)
		if !ok {
			continue
		}
		for _, p := range fb.Primitives {
			sc, ok := p.(SymbolCall)
			if !ok {
				continue
			}
			if sc.Halo == nil || sc.Halo.ColorToken != "CHWHT" {
				t.Errorf("sounding symbol %q missing CHWHT halo", sc.SymbolName)
			}
			if math.IsNaN(float64(sc.SoundingDepthM)) {
				t.Errorf("sounding symbol %q has no depth", sc.SymbolName)
			}
			return // one is enough
		}
	}
	t.Skip("no SOUNDG features in golden cell")
}
