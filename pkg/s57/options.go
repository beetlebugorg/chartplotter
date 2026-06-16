package s57

import "github.com/spf13/afero"

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

	// Fs is the filesystem to use for reading files.
	// If nil, the OS filesystem is used (afero.NewOsFs()).
	// This allows using custom filesystem implementations for testing
	// (e.g., afero.NewMemMapFs()) or specialized storage systems.
	//
	// Example with in-memory filesystem:
	//   fs := afero.NewMemMapFs()
	//   afero.WriteFile(fs, "/test.000", data, 0644)
	//   opts := s57.ParseOptions{Fs: fs}
	//   parser := s57.NewParser()
	//   chart, err := parser.ParseWithOptions("/test.000", opts)
	Fs afero.Fs
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
