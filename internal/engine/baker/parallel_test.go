package baker

import (
	"crypto/sha256"
	"fmt"
	"testing"
)

// TestMultiCellParallelDeterministic guards the property the parallel bake relies
// on: cells are parsed and portrayed concurrently (completing out of order) but
// routed serially in sorted order, so the archive is identical run to run. A
// single-cell bake (TestParallelDeterministic) only runs one worker and doesn't
// exercise the ordered merge, so this uses several cells.
func TestMultiCellParallelDeterministic(t *testing.T) {
	one := goldenCellBytes(t)["US4MD81M.000"]
	cells := map[string][]byte{}
	for i := 0; i < 4; i++ {
		cells[fmt.Sprintf("US4MD81M_%02d.000", i)] = one
	}
	var first string
	for run := 0; run < 2; run++ {
		b, ok, _ := BuildBaker(cells, nil)
		if len(ok) != len(cells) {
			t.Fatalf("run %d: routed %d/%d cells", run, len(ok), len(cells))
		}
		h := fmt.Sprintf("%x", sha256.Sum256(BakeToPMTiles(b, nil).Finish()))
		if run == 0 {
			first = h
		} else if h != first {
			t.Fatalf("nondeterministic parallel merge: run %d hash %s != run0 %s", run, h, first)
		}
	}
}
