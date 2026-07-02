package s57

import (
	"github.com/beetlebugorg/chartplotter/internal/s57/parser"
)

// ParseWithOptions reads an S-57 file with custom options.
//
// The filename should point to an S-57 base cell (.000). Update files
// (.001, .002, …) found beside it are applied when opts.ApplyUpdates is set.
//
// Example:
//
//	opts := s57.DefaultParseOptions()
//	opts.ObjectClassFilter = []string{"M_COVR"}
//	chart, err := s57.ParseWithOptions("US5MA22M.000", opts)
func ParseWithOptions(filename string, opts ParseOptions) (*Chart, error) {
	internalOpts := parser.ParseOptions{
		SkipUnknownFeatures: opts.SkipUnknownFeatures,
		ValidateGeometry:    opts.ValidateGeometry,
		ObjectClassFilter:   opts.ObjectClassFilter,
		ApplyUpdates:        opts.ApplyUpdates,
		Fs:                  opts.Fs,
	}
	internalChart, err := parser.NewParser().ParseWithOptions(filename, internalOpts)
	if err != nil {
		return nil, err
	}
	return convertChart(internalChart), nil
}
