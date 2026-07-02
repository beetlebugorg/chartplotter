package baker

// NOAA ENC navigational-purpose bands and the compilation-scale → band mapping.
// This lives in the (surviving, CGO-free) metadata package because grouping cells
// into bands for the tile57 per-band bake needs it independently of any tile
// baker. The slugs + zoom spans MUST match the frontend's CHART_BANDS so each
// per-band archive loads into its chart-<slug> source.

// Band is a NOAA ENC navigational-purpose band. Each bakes over its own zoom
// range and overzooms above max client-side.
type Band uint8

const (
	BandOverview Band = iota
	BandGeneral
	BandCoastal
	BandApproach
	BandHarbor
	BandBerthing
)

// BakeBand is one navigational-purpose band's identity for per-band archive
// baking: its frontend slug and native [Min,Max] zoom span.
type BakeBand struct {
	Slug     string
	Min, Max uint32
}

// BakeBands lists the bands coarse→fine for per-band archive baking — must match
// the frontend's CHART_BANDS (slug + zoom span) so each archive loads into its
// chart-<slug> source. The order matches the Band enum, so BakeBands()[Band] is
// that band's BakeBand.
func BakeBands() []BakeBand {
	return []BakeBand{
		{"overview", 0, 7},
		{"general", 7, 9},
		{"coastal", 9, 11},
		{"approach", 11, 13},
		{"harbor", 13, 16},
		{"berthing", 16, 18},
	}
}

// BandForScale maps a compilation-scale denominator (CSCL) to a band.
func BandForScale(cscl uint32) Band {
	n := cscl
	if n == 0 {
		n = 50_000
	}
	switch {
	case n <= 8_000:
		return BandBerthing
	case n <= 32_000:
		return BandHarbor
	case n <= 130_000:
		return BandApproach
	case n <= 500_000:
		return BandCoastal
	case n <= 2_300_000:
		return BandGeneral
	default:
		return BandOverview
	}
}
