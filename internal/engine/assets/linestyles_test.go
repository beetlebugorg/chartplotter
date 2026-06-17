package assets

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/beetlebugorg/chartplotter/pkg/s52"
	"github.com/beetlebugorg/chartplotter/pkg/s52/preslib"
)

// TestLinestylesMatchReference compares generated linestyles.json against the
// reference output shipped in web/linestyles.json (structural equality of
// the parsed JSON, so key ordering/whitespace don't matter).
func TestLinestylesMatchReference(t *testing.T) {
	ref, err := os.ReadFile("../../../web/linestyles.json")
	if err != nil {
		t.Skipf("reference not available: %v", err)
	}
	lib, err := s52.LoadLibraryFromBytes(preslib.DAI)
	if err != nil {
		t.Fatal(err)
	}
	got, err := LinestylesJSON(lib)
	if err != nil {
		t.Fatal(err)
	}

	var gotM, refM map[string]any
	if err := json.Unmarshal(got, &gotM); err != nil {
		t.Fatalf("got not valid JSON: %v", err)
	}
	if err := json.Unmarshal(ref, &refM); err != nil {
		t.Fatalf("ref not valid JSON: %v", err)
	}
	if len(gotM) != len(refM) {
		t.Errorf("linestyle count: got %d, ref %d", len(gotM), len(refM))
	}
	for name, refV := range refM {
		gotV, ok := gotM[name]
		if !ok {
			t.Errorf("%s: missing in generated output", name)
			continue
		}
		if !reflect.DeepEqual(gotV, refV) {
			t.Errorf("%s mismatch:\n got %v\n ref %v", name, gotV, refV)
		}
	}
	for name := range gotM {
		if _, ok := refM[name]; !ok {
			t.Errorf("%s: extra in generated output", name)
		}
	}
}
