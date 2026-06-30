//go:build !tile57

package main

import "fmt"

// bakeTile57Bundle is the stub used in the default CGO-free build; --tile57 then
// reports how to get a binary that can bake with the native engine.
func bakeTile57Bundle(_, _ string, _ int, _ func(stage uint8, done, total int)) (int, [4]float64, error) {
	return 0, [4]float64{}, fmt.Errorf("--tile57 requires a binary built with -tags tile57 (run `make build-tile57`)")
}
