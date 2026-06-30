//go:build tile57

package main

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	tile57 "github.com/beetlebugorg/chartplotter-native/bindings/go"
)

// emitS101Assets generates the client asset files (colortables/linestyles/sprite/
// patterns) from the S-101 PortrayalCatalog using the native libtile57 C ABI, so
// the served symbology is produced by the SAME engine that renders the tiles. It
// mirrors assets.EmitS101FS's six outputs and replaces it under -tags tile57.
// catalogFS is rooted at a PortrayalCatalog; cssName selects the palette stylesheet
// under Symbols/ (e.g. "daySvgStyle.css").
func emitS101Assets(catalogFS fs.FS, cssName, dir string) ([]string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	var written []string
	write := func(name string, data []byte) error {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, data, 0o644); err != nil {
			return err
		}
		written = append(written, p)
		return nil
	}

	// colortables.json — from ColorProfiles/colorProfile.xml.
	cp, err := fs.ReadFile(catalogFS, "ColorProfiles/colorProfile.xml")
	if err != nil {
		return nil, fmt.Errorf("colorProfile.xml: %w", err)
	}
	ct, err := tile57.Colortables(cp)
	if err != nil {
		return nil, err
	}
	if err := write("colortables.json", ct); err != nil {
		return nil, err
	}

	// linestyles.json — from every LineStyles/*.xml (id = file stem).
	lines, err := namedFromDir(catalogFS, "LineStyles", ".xml")
	if err != nil {
		return nil, err
	}
	ls, err := tile57.Linestyles(lines)
	if err != nil {
		return nil, err
	}
	if err := write("linestyles.json", ls); err != nil {
		return nil, err
	}

	// sprite atlas — rasterize every Symbols/*.svg against the palette stylesheet.
	css, err := fs.ReadFile(catalogFS, path.Join("Symbols", cssName))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", cssName, err)
	}
	symbols, err := namedFromDir(catalogFS, "Symbols", ".svg")
	if err != nil {
		return nil, err
	}
	spriteJSON, spritePNG, err := tile57.SpriteAtlas(symbols, css)
	if err != nil {
		return nil, err
	}
	if err := write("sprite.json", spriteJSON); err != nil {
		return nil, err
	}
	if err := write("sprite.png", spritePNG); err != nil {
		return nil, err
	}

	// pattern atlas — tile each AreaFills/*.xml's referenced symbol on its lattice.
	fills, err := namedFromDir(catalogFS, "AreaFills", ".xml")
	if err != nil {
		return nil, err
	}
	patJSON, patPNG, err := tile57.PatternAtlas(fills, symbols, css)
	if err != nil {
		return nil, err
	}
	if err := write("patterns.json", patJSON); err != nil {
		return nil, err
	}
	if err := write("patterns.png", patPNG); err != nil {
		return nil, err
	}

	fmt.Printf("tile57: emitted S-101 client assets via libtile57 (%d symbols, %d line styles, %d fills)\n",
		len(symbols), len(lines), len(fills))
	return written, nil
}

// namedFromDir reads every file with ext under subdir of catalogFS into a sorted
// []tile57.NamedBytes keyed by file stem.
func namedFromDir(catalogFS fs.FS, subdir, ext string) ([]tile57.NamedBytes, error) {
	entries, err := fs.ReadDir(catalogFS, subdir)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", subdir, err)
	}
	var out []tile57.NamedBytes
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ext) {
			continue
		}
		b, err := fs.ReadFile(catalogFS, path.Join(subdir, e.Name()))
		if err != nil {
			return nil, err
		}
		stem := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		out = append(out, tile57.NamedBytes{ID: stem, Data: b})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
