// Package catalog loads the static drawing assets of the IHO S-101 Portrayal
// Catalogue — line styles, area fills, and the colour profile — into Go structs
// (see specs/s101-portrayal-backport.md, Workstreams A & C). Symbols (SVG) are
// handled separately by the rasterizer. These are the definitions that
// DrawCommand references (LineInstruction/AreaFillReference/colour tokens) are
// resolved against when lowering S-101 portrayal output to engine primitives.
package catalog

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Dash is one on-segment of a line-style pattern, positioned along the interval.
type Dash struct{ Start, Length float64 }

// PlacedSymbol is a symbol repeated along a line style at a given interval position.
type PlacedSymbol struct {
	Reference string
	Position  float64
}

// LineStyle is one S-101 LineStyles/*.xml definition (S100LineStyle/5.2). Most
// are single-component; a compositeLineStyle (e.g. a double line) carries its
// parallel components in Components, each with its own Offset/pen/pattern.
type LineStyle struct {
	ID             string
	Offset         float64
	IntervalLength float64
	PenWidth       float64
	PenColor       string // colour token, e.g. CHMGD
	Dashes         []Dash
	Symbols        []PlacedSymbol
	Components     []LineStyle // non-empty only for composite line styles
}

// Vec is a 2-D vector in symbol-space mm (used for area-fill tiling basis).
type Vec struct{ X, Y float64 }

// AreaFill is one S-101 AreaFills/*.xml symbolFill definition (S100AreaFill/5.2):
// a symbol tiled across the area on the lattice spanned by V1 and V2.
type AreaFill struct {
	ID        string
	CRS       string // e.g. GlobalGeometry
	SymbolRef string
	V1, V2    Vec
}

// --- XML shapes ---

type xmlLineStyle struct {
	Offset         float64 `xml:"offset,attr"`
	IntervalLength float64 `xml:"intervalLength"`
	Pen            struct {
		Width float64 `xml:"width,attr"`
		Color string  `xml:"color"`
	} `xml:"pen"`
	Dashes []struct {
		Start  float64 `xml:"start"`
		Length float64 `xml:"length"`
	} `xml:"dash"`
	Symbols []struct {
		Reference string  `xml:"reference,attr"`
		Position  float64 `xml:"position"`
	} `xml:"symbol"`
	// Inner <lineStyle> elements, present only for a compositeLineStyle root.
	Components []xmlLineStyle `xml:"lineStyle"`
}

type xmlAreaFill struct {
	CRS    string `xml:"areaCRS"`
	Symbol struct {
		Reference string `xml:"reference,attr"`
	} `xml:"symbol"`
	V1 struct {
		X float64 `xml:"x"`
		Y float64 `xml:"y"`
	} `xml:"v1"`
	V2 struct {
		X float64 `xml:"x"`
		Y float64 `xml:"y"`
	} `xml:"v2"`
}

// LoadLineStyle parses one LineStyles/*.xml (simple or composite) by path; the
// ID is the file stem.
func LoadLineStyle(path string) (*LineStyle, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseLineStyle(stem(path), data)
}

func parseLineStyle(id string, data []byte) (*LineStyle, error) {
	var x xmlLineStyle
	if err := decodeXML(data, &x); err != nil {
		return nil, fmt.Errorf("%s: %w", id, err)
	}
	ls := lineStyleFromXML(x)
	ls.ID = id
	return &ls, nil
}

func lineStyleFromXML(x xmlLineStyle) LineStyle {
	ls := LineStyle{
		Offset:         x.Offset,
		IntervalLength: x.IntervalLength,
		PenWidth:       x.Pen.Width,
		PenColor:       x.Pen.Color,
	}
	for _, d := range x.Dashes {
		ls.Dashes = append(ls.Dashes, Dash{Start: d.Start, Length: d.Length})
	}
	for _, s := range x.Symbols {
		ls.Symbols = append(ls.Symbols, PlacedSymbol{Reference: s.Reference, Position: s.Position})
	}
	for _, c := range x.Components {
		ls.Components = append(ls.Components, lineStyleFromXML(c))
	}
	return ls
}

// LoadAreaFill parses one AreaFills/*.xml symbolFill by path; the ID is the
// file stem.
func LoadAreaFill(path string) (*AreaFill, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseAreaFill(stem(path), data)
}

func parseAreaFill(id string, data []byte) (*AreaFill, error) {
	var x xmlAreaFill
	if err := decodeXML(data, &x); err != nil {
		return nil, fmt.Errorf("%s: %w", id, err)
	}
	return &AreaFill{
		ID:        id,
		CRS:       x.CRS,
		SymbolRef: x.Symbol.Reference,
		V1:        Vec{X: x.V1.X, Y: x.V1.Y},
		V2:        Vec{X: x.V2.X, Y: x.V2.Y},
	}, nil
}

// LoadLineStyles loads every *.xml in a directory (path), keyed by ID.
func LoadLineStyles(dir string) (map[string]*LineStyle, error) {
	return loadLineStylesFS(os.DirFS(dir), ".")
}

// LoadAreaFills loads every *.xml in a directory (path), keyed by ID.
func LoadAreaFills(dir string) (map[string]*AreaFill, error) {
	return loadAreaFillsFS(os.DirFS(dir), ".")
}

func loadLineStylesFS(fsys fs.FS, sub string) (map[string]*LineStyle, error) {
	out := map[string]*LineStyle{}
	err := eachXMLFS(fsys, sub, func(name string, data []byte) error {
		ls, err := parseLineStyle(name, data)
		if err != nil {
			return err
		}
		out[ls.ID] = ls
		return nil
	})
	return out, err
}

func loadAreaFillsFS(fsys fs.FS, sub string) (map[string]*AreaFill, error) {
	out := map[string]*AreaFill{}
	err := eachXMLFS(fsys, sub, func(name string, data []byte) error {
		af, err := parseAreaFill(name, data)
		if err != nil {
			return err
		}
		out[af.ID] = af
		return nil
	})
	return out, err
}

// --- helpers ---

func decodeXML(data []byte, v any) error {
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.CharsetReader = charsetReader // some catalogue files declare ISO-8859-1
	return dec.Decode(v)
}

// charsetReader converts the few non-UTF-8 encodings the catalogue uses.
// ISO-8859-1 (latin1) maps each byte directly to a code point; everything else
// is assumed UTF-8 and passed through.
func charsetReader(label string, input io.Reader) (io.Reader, error) {
	switch strings.ToLower(strings.TrimSpace(label)) {
	case "iso-8859-1", "iso8859-1", "latin1":
		b, err := io.ReadAll(input)
		if err != nil {
			return nil, err
		}
		out := make([]byte, 0, len(b))
		for _, c := range b {
			if c < 0x80 {
				out = append(out, c)
			} else {
				out = append(out, 0xC0|c>>6, 0x80|c&0x3F)
			}
		}
		return bytes.NewReader(out), nil
	default:
		return input, nil
	}
}

// eachXMLFS calls fn(stem, data) for every *.xml in fsys under sub (use "." for
// the root). Works for both embed.FS and os.DirFS.
func eachXMLFS(fsys fs.FS, sub string, fn func(stem string, data []byte) error) error {
	entries, err := fs.ReadDir(fsys, sub)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".xml") {
			continue
		}
		p := e.Name()
		if sub != "." {
			p = sub + "/" + e.Name()
		}
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		stem := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		if err := fn(stem, data); err != nil {
			return err
		}
	}
	return nil
}

func stem(path string) string {
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}
