package assets

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/beetlebugorg/chartplotter/pkg/s52"
)

// Emit writes the six generated S-52 asset files into dir, returning the list
// of files written: colortables.json (token -> hex per scheme), linestyles.json
// (complex-line dash data), and the sprite/pattern atlases
// (sprite.{png,json}, patterns.{png,json}). All are derived from the embedded
// PresLib; colour is the only place RGB appears.
func Emit(lib *s52.Library, dir string) ([]string, error) {
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

	ct, err := ColorTablesJSON(lib)
	if err != nil {
		return nil, fmt.Errorf("colortables: %w", err)
	}
	if err := write("colortables.json", ct); err != nil {
		return nil, err
	}

	ls, err := LinestylesJSON(lib)
	if err != nil {
		return nil, fmt.Errorf("linestyles: %w", err)
	}
	if err := write("linestyles.json", ls); err != nil {
		return nil, err
	}

	spriteJSON, spritePNG, err := SpriteAtlas(lib)
	if err != nil {
		return nil, fmt.Errorf("sprites: %w", err)
	}
	if err := write("sprite.json", spriteJSON); err != nil {
		return nil, err
	}
	if err := write("sprite.png", spritePNG); err != nil {
		return nil, err
	}

	patJSON, patPNG, err := PatternAtlas(lib)
	if err != nil {
		return nil, fmt.Errorf("patterns: %w", err)
	}
	if err := write("patterns.json", patJSON); err != nil {
		return nil, err
	}
	if err := write("patterns.png", patPNG); err != nil {
		return nil, err
	}

	return written, nil
}
