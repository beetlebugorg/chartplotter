//go:build embed_s101

// Package s101catalog provides the S-101 PortrayalCatalog + FeatureCatalogue
// EMBEDDED into the binary at build time.
//
// The catalogue is NOT vendored into the repo. `make` rsyncs it from the
// external $(S101_PC)/$(S101_FC) into the gitignored catalog/ dir, then builds
// with -tags embed_s101 so go:embed bakes it into the binary. A plain
// `go build` (no tag) compiles the embed_off stub instead — Available() is
// false and the baker falls back to an external --s101 dir.
package s101catalog

import (
	"embed"
	"io/fs"
)

//go:embed all:catalog
var files embed.FS

// Available reports whether an S-101 catalogue is embedded in this binary.
func Available() bool { return true }

// PortrayalFS returns the embedded PortrayalCatalog tree (Rules/, Symbols/,
// ColorProfiles/, LineStyles/, AreaFills/, …).
func PortrayalFS() (fs.FS, error) { return fs.Sub(files, "catalog/PortrayalCatalog") }

// FeatureCatalogue returns the embedded FeatureCatalogue.xml bytes.
func FeatureCatalogue() ([]byte, error) { return files.ReadFile("catalog/FeatureCatalogue.xml") }
