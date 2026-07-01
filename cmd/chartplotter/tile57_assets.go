//go:build tile57

package main

import (
	"fmt"
	"os"
	"path/filepath"

	tile57 "github.com/beetlebugorg/chartplotter-native/bindings/go"
)

// emitS101Assets writes the client asset files (colortables/linestyles/sprite/
// patterns) into dir via the native libtile57 asset baker, so the served symbology
// is produced by the SAME engine that renders the tiles. catalogDir "" uses
// libtile57's embedded S-101 catalogue; a path emits from that on-disk
// PortrayalCatalog instead. Returns the files written.
func emitS101Assets(catalogDir, dir string) ([]string, error) {
	a, err := tile57.BakeAssets(catalogDir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	out := []struct {
		name string
		data []byte
	}{
		{"colortables.json", a.Colortables},
		{"linestyles.json", a.Linestyles},
		{"sprite.json", a.SpriteJSON},
		{"sprite.png", a.SpritePNG},
		{"patterns.json", a.PatternJSON},
		{"patterns.png", a.PatternPNG},
	}
	var written []string
	for _, f := range out {
		if len(f.data) == 0 {
			continue // an empty buffer (e.g. no area patterns) — skip
		}
		p := filepath.Join(dir, f.name)
		if err := os.WriteFile(p, f.data, 0o644); err != nil {
			return nil, err
		}
		written = append(written, p)
	}
	src := "libtile57 embedded catalogue"
	if catalogDir != "" {
		src = catalogDir
	}
	fmt.Printf("tile57: emitted %d S-101 client asset file(s) from %s\n", len(written), src)
	return written, nil
}
