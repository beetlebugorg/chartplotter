package s52

// CalculateSoundingSymbolAdvance calculates how far to advance the X position
// after placing a sounding digit symbol (SOUNDG/SOUNDS).
//
// For tight spacing without overlaps:
//   - Advance by distance from pivot point to right edge of bbox
//   - Add small gap (0.2mm) to prevent pixel-level overlaps
//
// Returns the advance distance in millimeters.
func CalculateSoundingSymbolAdvance(symbol *Symbol) float64 {
	if symbol == nil {
		return 0
	}

	bbox := symbol.BoundingBox

	// Distance from pivot to right edge (in DAI units, convert to mm)
	pivotToRight := float64(bbox.MaxX-symbol.PivotPoint.X) * 0.01

	// Small gap to prevent overlap (0.2mm)
	gapMM := 0.2

	return pivotToRight + gapMM
}

// IsSoundingSymbol returns true if the given symbol ID is a sounding digit.
func IsSoundingSymbol(symbolID string) bool {
	return len(symbolID) >= 6 && (symbolID[:6] == "SOUNDG" || symbolID[:6] == "SOUNDS")
}
