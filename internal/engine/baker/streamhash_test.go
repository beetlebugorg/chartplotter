package baker

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"testing"

	"github.com/beetlebugorg/chartplotter/internal/engine/pmtiles"
)

// TestStreamingBakeDeterministic guards the parallel streaming bake (the UI
// import path): cells are parsed+portrayed concurrently in both passes but
// merged/routed serially in order, so each band archive is identical run to run.
func TestStreamingBakeDeterministic(t *testing.T) {
	one := goldenCellBytes(t)["US4MD81M.000"]
	cells := map[string]CellData{}
	for i := 0; i < 4; i++ {
		cells[fmt.Sprintf("US4MD81M_%02d.000", i)] = CellData{Base: one}
	}
	bake := func() string {
		var lines []string
		_, _, err := BakeToPMTilesBandsStreaming(cells, 0, nil, nil,
			func(slug string, pb *pmtiles.Builder) error {
				arc := pb.Finish()
				lines = append(lines, fmt.Sprintf("%s=%x", slug, sha256.Sum256(arc)))
				return nil
			})
		if err != nil {
			t.Fatal(err)
		}
		sort.Strings(lines)
		return fmt.Sprint(lines)
	}
	if a, b := bake(), bake(); a != b {
		t.Fatalf("nondeterministic streaming bake:\n%s\n%s", a, b)
	}
}
