package baker

import "testing"

// BenchmarkBuildBakerGolden measures the parse + portrayal + routing phase
// (BuildBaker) on the golden cell — the phase BenchmarkBakeGoldenParallel
// excludes by resetting its timer after the build. This is where the S-101 rule
// engine runs, so it's the benchmark to watch for portrayal-side optimizations.
func BenchmarkBuildBakerGolden(b *testing.B) {
	cells := goldenCellBytes(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, _ = BuildBaker(cells, nil)
	}
}
