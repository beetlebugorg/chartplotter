package bake

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/beetlebugorg/chartplotter/internal/engine/mvt"
	"github.com/beetlebugorg/chartplotter/pkg/s57"
)

// encodeS57Attrs feeds the cursor-pick report (S-52 PresLib §10.8): a compact,
// deterministic acronym→value blob the client decodes against the catalogue.
func TestEncodeS57Attrs(t *testing.T) {
	if got := encodeS57Attrs(nil); got != "" {
		t.Errorf("nil attrs: got %q, want empty", got)
	}
	if got := encodeS57Attrs(map[string]any{"DEPTHS": []float64{1, 2}}); got != "" {
		t.Errorf("synthetic-only attrs: got %q, want empty (DEPTHS dropped)", got)
	}

	attrs := map[string]any{
		"OBJNAM": "  Buoy 7 ", // trimmed
		"COLOUR": "1,3",       // list kept raw for client-side enum decode
		"VALSOU": 10.0,        // minimal number formatting (rule 3: no padding)
		"CATLIT": 4,
		"DEPTHS": []float64{1, 2}, // synthetic — must be dropped
		"BLANK":  "   ",           // empty after trim — dropped
	}
	got := encodeS57Attrs(attrs)

	var m map[string]string
	if err := json.Unmarshal([]byte(got), &m); err != nil {
		t.Fatalf("blob is not valid JSON: %v (%q)", err, got)
	}
	want := map[string]string{"OBJNAM": "Buoy 7", "COLOUR": "1,3", "VALSOU": "10", "CATLIT": "4"}
	if len(m) != len(want) {
		t.Errorf("key set mismatch: got %v, want %v", m, want)
	}
	for k, v := range want {
		if m[k] != v {
			t.Errorf("%s: got %q, want %q", k, m[k], v)
		}
	}
	if _, ok := m["DEPTHS"]; ok {
		t.Error("DEPTHS leaked into blob")
	}

	// Deterministic: same input → byte-identical output (json.Marshal sorts keys).
	if again := encodeS57Attrs(attrs); again != got {
		t.Errorf("non-deterministic encode:\n %q\n %q", got, again)
	}
}

// The pick report needs the s57 attribute blob on real baked features; assert it
// is emitted on at least one layer of the golden cell.
func TestS57BlobBaked(t *testing.T) {
	chart, err := s57.Parse(goldenCell)
	if err != nil {
		t.Fatal(err)
	}
	b := New()
	b.SetPortrayer(testS101Portrayer(t))
	b.AddCell(chart)

	for _, c := range b.TileCoords(mvt.ExtentDefault) {
		data := b.EmitTile(c, mvt.ExtentDefault, 64)
		if data == nil {
			continue
		}
		for _, l := range decodeLayers(data) {
			if slices.Contains(l.keys, "s57") {
				return // found the attribute blob
			}
		}
	}
	t.Fatal("no `s57` attribute blob emitted on any layer of the golden cell")
}
