package s57

import (
	"io/fs"

	"github.com/beetlebugorg/chartplotter/internal/s57/parser"
)

// Parser parses S-57 Electronic Navigational Chart files.
//
// Create a parser with NewParser and use Parse or ParseWithOptions to read charts.
type Parser interface {
	// Parse reads an S-57 file and returns the parsed chart.
	//
	// The filename should point to an S-57 base cell (.000) or update file (.001, .002, etc.).
	// Returns an error if the file cannot be read or parsed according to S-57 Edition 3.1.
	Parse(filename string) (*Chart, error)

	// ParseWithOptions parses an S-57 file with custom options.
	//
	// Use ParseOptions to control validation, error handling, and feature filtering.
	ParseWithOptions(filename string, opts ParseOptions) (*Chart, error)
}

// NewParser creates a new S-57 parser with default settings.
//
// Example:
//
//	parser := s57.NewParser()
//	chart, err := parser.Parse("US5MA22M.000")
func NewParser() Parser {
	return &parserWrapper{
		internal: parser.NewParser(),
	}
}

// parserWrapper wraps the internal parser and converts types
type parserWrapper struct {
	internal parser.Parser
}

func (p *parserWrapper) Parse(filename string) (*Chart, error) {
	internalChart, err := p.internal.Parse(filename)
	if err != nil {
		return nil, err
	}
	return convertChart(internalChart), nil
}

func (p *parserWrapper) ParseWithOptions(filename string, opts ParseOptions) (*Chart, error) {
	internalOpts := parser.ParseOptions{
		SkipUnknownFeatures: opts.SkipUnknownFeatures,
		ValidateGeometry:    opts.ValidateGeometry,
		ObjectClassFilter:   opts.ObjectClassFilter,
		ApplyUpdates:        opts.ApplyUpdates,

		MaskCoastlineCoincidentBoundaries: opts.MaskCoastlineCoincidentBoundaries,

		ValidateConformance: opts.ValidateConformance,

		Fs: opts.Fs,
	}
	internalChart, err := p.internal.ParseWithOptions(filename, internalOpts)
	if err != nil {
		return nil, err
	}
	return convertChart(internalChart), nil
}

// Parse reads an S-57 file from the OS filesystem and returns the parsed chart.
// This is a convenience function equivalent to:
//
//	parser := s57.NewParser()
//	chart, err := parser.Parse(filename)
//
// Example:
//
//	chart, err := s57.Parse("US5MA22M.000")
func Parse(filename string) (*Chart, error) {
	p := NewParser()
	return p.Parse(filename)
}

// ParseFS reads an S-57 file from a custom io/fs.FS and returns the parsed chart.
// This allows custom filesystem implementations (e.g. iso8211.MemFS for raw
// bytes) for testing or specialized storage systems.
//
// The filesystem is used for both the base file and any update files (.001, .002, etc.)
// if ApplyUpdates is enabled in the options.
//
// Example with an in-memory filesystem:
//
//	fsys := iso8211.MemFS{"/chart.000": data}
//	chart, err := s57.ParseFS(fsys, "/chart.000")
//
// Example with custom options:
//
//	fsys := iso8211.MemFS{"/chart.000": data}
//	opts := s57.DefaultParseOptions()
//	opts.Fs = fsys
//	opts.ApplyUpdates = false
//	chart, err := s57.ParseWithOptions("/chart.000", opts)
func ParseFS(fsys fs.FS, filename string) (*Chart, error) {
	opts := DefaultParseOptions()
	opts.Fs = fsys
	p := NewParser()
	return p.ParseWithOptions(filename, opts)
}

// ParseWithOptions reads an S-57 file with custom options.
// This is a convenience function equivalent to:
//
//	parser := s57.NewParser()
//	chart, err := parser.ParseWithOptions(filename, opts)
//
// Example:
//
//	opts := s57.DefaultParseOptions()
//	opts.SkipUnknownFeatures = true
//	chart, err := s57.ParseWithOptions("US5MA22M.000", opts)
func ParseWithOptions(filename string, opts ParseOptions) (*Chart, error) {
	p := NewParser()
	return p.ParseWithOptions(filename, opts)
}
