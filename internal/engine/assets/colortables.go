// Package assets generates the client-side asset files baked from the S-52
// Presentation Library: colortables.json (token -> hex per scheme), the
// linestyles.json dash data, and the sprite/pattern PNG atlases. Colour is the
// only place RGB appears; everything in the tiles stays a token.
package assets

import (
	"encoding/json"
	"strings"

	"github.com/beetlebugorg/chartplotter/pkg/s52"
)

// schemes is the set emitted to colortables.json (the client resolves tokens
// against the active one for free Day/Dusk/Night switching).
var schemes = []struct {
	name   string
	scheme s52.ColorScheme
}{
	{"day", s52.ColorSchemeDay},
	{"dusk", s52.ColorSchemeDusk},
	{"night", s52.ColorSchemeNight},
}

// ColorTablesJSON renders colortables.json: {scheme: {token: "#rrggbb"}}.
func ColorTablesJSON(lib *s52.Library) ([]byte, error) {
	out := map[string]map[string]string{}
	for _, s := range schemes {
		cols, err := lib.GetColorsByScheme(s.scheme)
		if err != nil {
			return nil, err
		}
		m := make(map[string]string, len(cols))
		for tok, c := range cols {
			m[tok] = strings.ToLower(c.ConvertToHex())
		}
		out[s.name] = m
	}
	return json.MarshalIndent(out, "", "  ")
}
