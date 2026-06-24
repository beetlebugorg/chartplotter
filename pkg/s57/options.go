package s57

import "io/fs"

// ParseOptions configures parsing behavior.
type ParseOptions struct {
	SkipUnknownFeatures bool
	ValidateGeometry    bool
	ObjectClassFilter   []string

	// ApplyUpdates controls whether to automatically discover and apply
	// update files (.001, .002, etc.) when parsing a base cell (.000).
	// Default is true - updates are automatically applied.
	//
	// When true, the parser looks for sequential update files in the same
	// directory as the base file and applies them in order.
	//
	// Set to false to parse only the base cell without updates.
	ApplyUpdates bool

	// ValidateConformance promotes S-57 / ISO-8211 spec deviations from non-fatal
	// warnings (the default; see Chart.Warnings) to a parse error. Default false.
	ValidateConformance bool

	// MaskCoastlineCoincidentBoundaries derives coastline-coincident edge masking
	// for area features (S-57 Appendix B.1 Annex A §17 scenario 2). When true, an
	// area feature's drawn boundary edge whose RCID is also referenced by a COALNE
	// feature is dropped from BoundaryLines (the fill/Coordinates stay intact). The
	// coast-definer LNDARE is exempt. Default is false. The baker enables this.
	MaskCoastlineCoincidentBoundaries bool

	// Fs is the filesystem to use for reading files.
	// If nil, the OS filesystem is used (iso8211.OSFS()).
	// This allows custom io/fs.FS implementations for testing or specialized
	// storage (e.g. iso8211.MemFS for parsing raw bytes).
	//
	// Example with an in-memory filesystem:
	//   fsys := iso8211.MemFS{"/test.000": data}
	//   opts := s57.ParseOptions{Fs: fsys}
	//   chart, err := s57.NewParser().ParseWithOptions("/test.000", opts)
	Fs fs.FS
}

// DefaultParseOptions returns default options.
func DefaultParseOptions() ParseOptions {
	return ParseOptions{
		SkipUnknownFeatures: false,
		ValidateGeometry:    true,
		ObjectClassFilter:   nil,
		ApplyUpdates:        true, // Auto-apply updates by default
	}
}
