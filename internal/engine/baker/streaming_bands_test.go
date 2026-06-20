package baker

import (
	"bytes"
	"os"
	"testing"

	"github.com/beetlebugorg/chartplotter/internal/engine/pmtiles"
)

// multiBandCells loads the two MD testdata cells (US4 approach + US5 harbor),
// which sit in the same region but different bands — enough to exercise the
// per-band streaming path's cross-band coverage handling.
func multiBandCells(tb testing.TB) map[string]CellData {
	out := map[string]CellData{}
	for _, f := range []string{"US4MD81M", "US5MD1MC"} {
		data, err := os.ReadFile("../../../testdata/" + f + ".000")
		if err != nil {
			continue
		}
		out[f+".000"] = CellData{Base: data}
	}
	if len(out) == 0 {
		tb.Skip("no golden cells")
	}
	return out
}

// TestStreamingBandsMatchNonStreaming proves the memory-frugal per-band streaming
// bake produces byte-identical per-band archives to the all-cells-resident path,
// so it's a pure memory optimization with no render change.
func TestStreamingBandsMatchNonStreaming(t *testing.T) {
	cells := multiBandCells(t)

	// Reference: build the all-cells baker, then bake bands from it.
	b, _, err := BuildBakerWithUpdates(cells, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	ref := map[string][]byte{}
	if err := BakeToPMTilesBands(b, nil, func(slug string, pb *pmtiles.Builder) error {
		var buf bytes.Buffer
		if err := pb.WriteArchive(&buf); err != nil {
			return err
		}
		ref[slug] = buf.Bytes()
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// Streaming: re-parse per band, hold one band at a time.
	got := map[string][]byte{}
	if _, _, err := BakeToPMTilesBandsStreaming(cells, 0, nil, nil, func(slug string, pb *pmtiles.Builder) error {
		var buf bytes.Buffer
		if err := pb.WriteArchive(&buf); err != nil {
			return err
		}
		got[slug] = buf.Bytes()
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if len(ref) == 0 {
		t.Fatal("reference produced no band archives")
	}
	if len(got) != len(ref) {
		t.Fatalf("band count mismatch: streaming %d vs reference %d (%v vs %v)", len(got), len(ref), keys(got), keys(ref))
	}
	for slug, want := range ref {
		have, ok := got[slug]
		if !ok {
			t.Errorf("streaming missing band %q", slug)
			continue
		}
		if !bytes.Equal(have, want) {
			t.Errorf("band %q differs: streaming %d bytes vs reference %d bytes", slug, len(have), len(want))
		}
	}
}

func keys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
