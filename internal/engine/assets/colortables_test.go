package assets

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/beetlebugorg/chartplotter-go/pkg/s52"
	"github.com/beetlebugorg/chartplotter-go/pkg/s52/preslib"
)

// TestColorTablesMatchCopied checks the generated colortables.json equals the
// Zig-generated reference shipped in web/.
func TestColorTablesMatchCopied(t *testing.T) {
	lib, err := s52.LoadLibraryFromBytes(preslib.DAI)
	if err != nil {
		t.Fatal(err)
	}
	gen, err := ColorTablesJSON(lib)
	if err != nil {
		t.Fatal(err)
	}
	var got, want map[string]map[string]string
	if err := json.Unmarshal(gen, &got); err != nil {
		t.Fatal(err)
	}
	ref, err := os.ReadFile("../../../web/colortables.json")
	if err != nil {
		t.Skipf("reference not present: %v", err)
	}
	if err := json.Unmarshal(ref, &want); err != nil {
		t.Fatal(err)
	}
	for scheme, toks := range want {
		for tok, hex := range toks {
			if got[scheme][tok] != hex {
				t.Errorf("%s/%s = %q, want %q", scheme, tok, got[scheme][tok], hex)
			}
		}
	}
}
