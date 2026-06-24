//go:build !embed_s101

// Stub for plain (untagged) builds: no S-101 catalogue is embedded, so the
// baker must be given an external one via --s101. Build with `make` (which syncs
// the catalogue and sets -tags embed_s101) for a self-contained binary.
package s101catalog

import "io/fs"

// Available reports whether an S-101 catalogue is embedded — false here.
func Available() bool { return false }

// PortrayalFS returns nil (no embedded catalogue in an untagged build).
func PortrayalFS() (fs.FS, error) { return nil, nil }

// FeatureCatalogue returns nil (no embedded catalogue in an untagged build).
func FeatureCatalogue() ([]byte, error) { return nil, nil }
