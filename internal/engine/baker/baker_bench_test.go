package baker

import (
	"os"
	"testing"
)

func goldenCellBytes(tb testing.TB) map[string][]byte {
	data, err := os.ReadFile("../../../testdata/US4MD81M.000")
	if err != nil {
		tb.Skip("no golden cell")
	}
	return map[string][]byte{"US4MD81M.000": data}
}

func TestParallelDeterministic(t *testing.T) {
	cells := goldenCellBytes(t)
	b1, _, _ := BuildBaker(cells, nil)
	a := BakeToPMTiles(b1, nil).Finish()
	b2, _, _ := BuildBaker(cells, nil)
	bb := BakeToPMTiles(b2, nil).Finish()
	if len(a) != len(bb) || string(a) != string(bb) {
		t.Fatalf("non-deterministic output: %d vs %d bytes", len(a), len(bb))
	}
	t.Logf("archive %d bytes", len(a))
}

func BenchmarkBakeGoldenParallel(b *testing.B) {
	cells := goldenCellBytes(b)
	bk, _, _ := BuildBaker(cells, nil)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = BakeToPMTiles(bk, nil).Count()
	}
}
