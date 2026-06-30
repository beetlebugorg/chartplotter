//go:build !tile57

package main

import (
	"io/fs"

	"github.com/beetlebugorg/chartplotter/internal/engine/assets"
)

// emitS101Assets uses the pure-Go S-101 asset emitter in the default CGO-free
// build. The tile57 build (tile57_assets.go) generates the same files via the
// libtile57 C ABI instead.
func emitS101Assets(catalogFS fs.FS, cssName, dir string) ([]string, error) {
	return assets.EmitS101FS(catalogFS, cssName, dir)
}
