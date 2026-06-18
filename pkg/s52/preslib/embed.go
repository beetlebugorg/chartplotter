// Package preslib embeds the IHO S-52 Presentation Library 4.0.4 DAI file so
// the engine carries its symbology source with no runtime dependency. The DAI
// is parsed by package s52.
package preslib

import _ "embed"

// DAI is the raw IHO S-52 Presentation Library Edition 4.0.4 DAI file.
//
//go:embed PresLib_e4.0.4.dai
var DAI []byte
