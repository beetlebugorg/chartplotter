package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	tile57 "github.com/beetlebugorg/chartplotter-native/bindings/go"
	"github.com/beetlebugorg/chartplotter/internal/engine/server"
	"github.com/beetlebugorg/chartplotter/internal/engine/tilesource"
)

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
		TileType: m.TileType,
	}
}

// registerTile57Set opens the ENC inputs under root with libtile57 and registers
// a live tile set (tiles generated on demand from the cells, no prebake) under
// name. The live set gets the same format knob as the bake: it generates the
// engine-default encoding (MLT), and the set's TileJSON/style carry the matching
// `encoding` hint from Meta.TileType — the wire format IS the generated format.
// libtile57's streaming Open reads an ENC_ROOT dir / single .000 from disk on
// demand; a .zip or other input is first staged into a temp ENC dir (kept for the
// source's lifetime). rulesDir is unused — the engine uses its embedded catalogue.
func registerTile57Set(srv *server.Server, name, root, rulesDir string) error {
	_ = rulesDir
	encRoot := root
	if fi, err := os.Stat(root); err != nil || !(fi.IsDir() || encExt(root) == ".000") {
		cells, _, err := collectCells([]string{root})
		if err != nil {
			return err
		}
		if len(cells) == 0 {
			return fmt.Errorf("tile57: no .000 base cells found under %s", root)
		}
		dir, err := os.MkdirTemp("", "cp-tile57-live-")
		if err != nil {
			return err
		}
		for n, cd := range cells { // n == "<stem>.000"
			if err := os.WriteFile(filepath.Join(dir, n), cd.Base, 0o644); err != nil {
				return err
			}
			for un, ub := range cd.Updates {
				if err := os.WriteFile(filepath.Join(dir, filepath.Base(un)), ub, 0o644); err != nil {
					return err
				}
			}
		}
		encRoot = dir
	}
	src, err := tile57.Open(encRoot)
	if err != nil {
		return err
	}
	// Live generation follows the bake default (MLT). Cell-backed charts open
	// generating MVT for embedder back-compat, so opt the live set in explicitly;
	// TILE57_LIVE_FORMAT=mvt keeps the old MVT wire format if ever needed.
	format := tile57.FormatDefault
	if os.Getenv("TILE57_LIVE_FORMAT") == "mvt" {
		format = tile57.FormatMVT
	}
	src.SetTileFormat(format)
	srv.RegisterTileSet(name, tile57Source{src})
	info := src.Info()
	fmt.Printf("tile57: live set %q (libtile57 %s, zoom %d..%d, %s tiles)\n",
		name, tile57.Version(), info.MinZoom, info.MaxZoom, info.TileType.Encoding())
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
