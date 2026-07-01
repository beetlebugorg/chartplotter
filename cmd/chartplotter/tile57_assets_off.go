//go:build !tile57

package main

import "fmt"

// emitS101Assets is stubbed in the default CGO-free build: the S-101 client asset
// emitter now lives in the native libtile57 engine (tile57_assets.go, -tags
// tile57). The Go asset emitter has been removed, so a CGO-free binary can't emit
// assets — build with `make build-tile57`.
func emitS101Assets(_, _ string) ([]string, error) {
	return nil, fmt.Errorf("emit-assets requires a binary built with -tags tile57 (run `make build-tile57`)")
}
