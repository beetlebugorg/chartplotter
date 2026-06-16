package s52

import (
	"testing"

	"github.com/beetlebugorg/chartplotter-go/pkg/s52/preslib"
)

// TestColorTablesMatchZig pins the xyY->sRGB conversion to the Zig reference
// renderer (s52/src/color.zig). The frontend's colortables.json was generated
// by that renderer; these tokens are a representative spot-check across the
// three viewing schemes. If this drifts, Day/Dusk/Night colours will no longer
// match the reference demo.
func TestColorTablesMatchZig(t *testing.T) {
	lib, err := LoadLibraryFromBytes(preslib.DAI)
	if err != nil {
		t.Fatalf("LoadLibraryFromBytes: %v", err)
	}

	want := map[ColorScheme]map[string]string{
		ColorSchemeDay: {
			"CHBLK": "#000000", "DEPVS": "#61B7FF", "DEPDW": "#C9EDFF",
			"LANDA": "#BFBE8F", "CHGRN": "#52E83B", "DNGHL": "#EA5471",
		},
		ColorSchemeDusk: {
			"CHBLK": "#6B7F89", "DEPVS": "#1E4165", "DEPDW": "#000000",
			"LANDA": "#40402E", "CHGRN": "#2F8E20", "DNGHL": "#9B3549",
		},
		ColorSchemeNight: {
			"CHBLK": "#252D31", "DEPVS": "#071727", "DEPDW": "#000000",
			"LANDA": "#17160E", "CHGRN": "#0C3406", "DNGHL": "#390E16",
		},
	}

	for scheme, toks := range want {
		for tok, expect := range toks {
			got, err := lib.GetColorHex(tok, scheme)
			if err != nil {
				t.Errorf("%s/%s: %v", scheme, tok, err)
				continue
			}
			if got != expect {
				t.Errorf("%s/%s = %s, want %s", scheme, tok, got, expect)
			}
		}
	}
}
