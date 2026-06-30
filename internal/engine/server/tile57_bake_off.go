//go:build !tile57

package server

import (
	"github.com/beetlebugorg/chartplotter/internal/engine/baker"
	"github.com/beetlebugorg/chartplotter/pkg/s57"
)

// bakeTile57Available is false in the default CGO-free build (no native baker).
const bakeTile57Available = false

// bakeBundleTile57 is a stub in the default CGO-free build: the native bundle bake
// needs libtile57 (-tags tile57). Returning false makes bakeAndRegister fall
// through to the Go baker, so a stray BakeEngine="tile57" can't disable imports.
func (s *Server) bakeBundleTile57(_, _ string, _ map[string]baker.CellData, _ map[string][]byte, _ *s57.Catalog, _ bool) bool {
	return false
}
