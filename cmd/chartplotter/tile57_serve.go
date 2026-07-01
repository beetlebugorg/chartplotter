//go:build tile57

package main

import (
	"fmt"
	"sort"
	"strings"

	tile57 "github.com/beetlebugorg/chartplotter-native/bindings/go"
	"github.com/beetlebugorg/chartplotter/internal/engine/server"
	"github.com/beetlebugorg/chartplotter/internal/engine/tilesource"
)

// tile57Available reports that this binary embeds the libtile57 backend (built
// with -tags tile57). The CGO-free default build links the stub in
// tile57_serve_off.go, where this is false.
const tile57Available = true

// tile57Source adapts the official libtile57 Go binding's *Source to the host's
// tilesource.TileSource. Tile/Close are promoted from the embedded *Source; only
// Meta needs a shim because the binding returns its own (field-identical) Meta type
// rather than the host's tilesource.TileMeta.
type tile57Source struct{ *tile57.Source }

func (t tile57Source) Meta() tilesource.TileMeta {
	m := t.Source.Meta()
	return tilesource.TileMeta{
		MinZoom: m.MinZoom, MaxZoom: m.MaxZoom,
		W: m.W, S: m.S, E: m.E, N: m.N,
		Gzipped: m.Gzipped, Scamin: m.Scamin,
	}
}

// registerTile57Set opens the ENC inputs under root with libtile57 and registers
// a live tile set (MVT generated on demand from the cells, no prebake) under
// name. rulesDir overrides the S-101 portrayal rules ("" = libtile57's built-in).
func registerTile57Set(srv *server.Server, name, root, rulesDir string) error {
	cells, _, err := collectCells([]string{root})
	if err != nil {
		return err
	}
	if len(cells) == 0 {
		return fmt.Errorf("tile57: no .000 base cells found under %s", root)
	}
	inputs := make([]tile57.CellInput, 0, len(cells))
	for name, cd := range cells {
		inputs = append(inputs, tile57.CellInput{
			Name:    strings.TrimSuffix(name, ".000"), // pick-report "source cell" badge
			Base:    cd.Base,
			Updates: orderedUpdates(cd.Updates),
		})
	}
	src, err := tile57.OpenCells(inputs, rulesDir, tile57.PickInclude)
	if err != nil {
		return err
	}
	srv.RegisterTileSet(name, tile57Source{src})
	mn, mx := src.ZoomRange()
	fmt.Printf("tile57: live set %q from %d cell(s) (libtile57 %s, zoom %d..%d)\n",
		name, len(inputs), tile57.Version(), mn, mx)
	return nil
}

// orderedUpdates returns a cell's update bodies sorted by filename so libtile57
// applies them in sequence (.001, .002, …).
func orderedUpdates(m map[string][]byte) [][]byte {
	if len(m) == 0 {
		return nil
	}
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([][]byte, len(names))
	for i, n := range names {
		out[i] = m[n]
	}
	return out
}
