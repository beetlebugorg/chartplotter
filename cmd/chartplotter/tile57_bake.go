//go:build tile57

package main

import tile57 "github.com/beetlebugorg/chartplotter-native/bindings/go"

// bakeTile57Bundle bakes an on-disk ENC input (a .000 cell or a directory of cells)
// into a self-contained chart bundle under outDir via the native libtile57 engine.
// maxZoom caps the highest baked zoom (0 = no cap). progress nil uses the lib's
// built-in console progress. Returns the cell count + bbox (west,south,east,north).
func bakeTile57Bundle(input, outDir string, maxZoom int, progress func(tile57.BakeProgress)) (int, [4]float64, error) {
	mz := uint8(24) // ABI: 0/24 means "no clamp"; only narrow when the user caps it
	if maxZoom > 0 && maxZoom < 24 {
		mz = uint8(maxZoom)
	}
	return tile57.BakeBundle(input, outDir, "", "", "", 0, mz, tile57.PickInclude, progress)
}
