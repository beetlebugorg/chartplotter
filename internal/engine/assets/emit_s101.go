package assets

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/beetlebugorg/chartplotter/pkg/s100/catalog"
)

// EmitS101 writes the client asset files from the S-101 Portrayal Catalogue
// (specs/s101-portrayal-backport.md): colortables.json from colorProfile.xml and
// the sprite atlas from the S-101 SVG symbols. Self-contained — no S-52 library.
// portrayalCatalogDir is a PortrayalCatalog directory; cssName selects the
// palette stylesheet (e.g. "daySvgStyle.css", under Symbols/).
//
// TODO: linestyles.json + patterns.{png,json} from the S-101 LineStyles/AreaFills
// (format-matching emitters); until then complex S-101-only lines/fills won't
// render client-side, though point symbols and colours do.
func EmitS101(portrayalCatalogDir, cssName, dir string) ([]string, error) {
	return EmitS101FS(os.DirFS(portrayalCatalogDir), cssName, dir)
}

// EmitS101FS is EmitS101 over an fs.FS rooted at a PortrayalCatalog (e.g. an
// embed.FS sub-tree).
func EmitS101FS(catalogFS fs.FS, cssName, dir string) ([]string, error) {
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

	cp, err := catalog.LoadFS(catalogFS)
	if err != nil {
		return nil, fmt.Errorf("catalogue: %w", err)
	}
	ct, err := colorTablesJSONFromProfile(cp.Colors)
	if err != nil {
		return nil, fmt.Errorf("colortables: %w", err)
	}
	if err := write("colortables.json", ct); err != nil {
		return nil, err
	}

	symbolsFS, err := fs.Sub(catalogFS, "Symbols")
	if err != nil {
		return nil, err
	}
	spriteJSON, spritePNG, err := SpriteAtlasS101FS(symbolsFS, cssName)
	if err != nil {
		return nil, fmt.Errorf("sprites: %w", err)
	}
	if err := write("sprite.json", spriteJSON); err != nil {
		return nil, err
	}
	if err := write("sprite.png", spritePNG); err != nil {
		return nil, err
	}
	return written, nil
}

// colorTablesJSONFromProfile renders colortables.json ({scheme:{token:"#rrggbb"}})
// from the S-101 colour profile — the same shape ColorTablesJSON emits from the
// S-52 library (verified byte-identical by cmd/s101-color-diff).
func colorTablesJSONFromProfile(cp *catalog.ColorProfile) ([]byte, error) {
	out := map[string]map[string]string{
		"day":   paletteHex(cp.Day),
		"dusk":  paletteHex(cp.Dusk),
		"night": paletteHex(cp.Night),
	}
	return json.MarshalIndent(out, "", "  ")
}

func paletteHex(p catalog.Palette) map[string]string {
	m := make(map[string]string, len(p))
	for tok, c := range p {
		m[tok] = fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B)
	}
	return m
}
