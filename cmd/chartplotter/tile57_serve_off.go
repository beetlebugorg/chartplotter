//go:build !tile57

package main

import (
	"fmt"

	"github.com/beetlebugorg/chartplotter/internal/engine/server"
)

// tile57Available is false in the default CGO-free build (no libtile57). The
// tagged build (tile57_serve.go) sets it true.
const tile57Available = false

// registerTile57Set is the stub used when the binary is built without the
// libtile57 backend; --tile57 then reports how to get a capable binary.
func registerTile57Set(_ *server.Server, _, _, _ string) error {
	return fmt.Errorf("--tile57 requires a binary built with -tags tile57 (run `make build-tile57`)")
}
