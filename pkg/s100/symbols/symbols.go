// Package symbols flattens IHO S-101 symbol SVGs (resolving their CSS colour
// classes against a palette stylesheet and stripping the .layout debug boxes)
// and rasterizes them in pure Go (see specs/s101-portrayal-backport.md,
// Workstream B). It is the single source of truth for SVG→raster, used by both
// cmd/svg-raster-test (de-risk) and the sprite-atlas builder.
//
// Two oksvg defects are worked around here: it ignores a non-zero viewBox
// origin (we normalize to "0 0 W H" and wrap the content in a translate), and
// it applies stroke-width in device px without scaling by the draw transform
// (we pre-multiply stroke-width by the px/mm scale).
package symbols

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"image"
	"image/draw"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/srwiley/oksvg"
	"github.com/srwiley/rasterx"
)

var cssRuleRE = regexp.MustCompile(`\.([A-Za-z0-9_]+)\s*\{([^}]*)\}`)

// LoadCSS parses an S-100 *SvgStyle.css into class name -> declaration string
// (e.g. "fCHYLW" -> "fill:#E1E139").
func LoadCSS(data []byte) map[string]string {
	out := map[string]string{}
	for _, m := range cssRuleRE.FindAllStringSubmatch(string(data), -1) {
		decl := strings.TrimSpace(m[2])
		decl = strings.TrimSuffix(decl, ";")
		out[m[1]] = decl
	}
	return out
}

// Rendered is one rasterized symbol: a straight-alpha image plus the pixel
// location of the SVG pivot (the (0,0) origin), used as the sprite anchor.
type Rendered struct {
	Image          *image.NRGBA
	PivotX, PivotY float64  // px, +x right +y down
	Missing        []string // class tokens with no CSS rule (gaps)
}

// Render flattens and rasterizes an S-101 symbol SVG at pxPerMM device pixels
// per millimetre.
func Render(svg []byte, css map[string]string, pxPerMM float64) (*Rendered, error) {
	flat, vb, missing, err := flatten(svg, css, pxPerMM)
	if err != nil {
		return nil, err
	}
	icon, err := oksvg.ReadIconStream(bytes.NewReader(flat))
	if err != nil {
		return nil, err
	}
	w := int(math.Ceil(vb[2] * pxPerMM))
	h := int(math.Ceil(vb[3] * pxPerMM))
	if w < 1 || h < 1 {
		return nil, fmt.Errorf("degenerate viewBox %gx%g", vb[2], vb[3])
	}
	rgba := image.NewRGBA(image.Rect(0, 0, w, h))
	icon.SetTarget(0, 0, float64(w), float64(h))
	scanner := rasterx.NewScannerGV(w, h, rgba, rgba.Bounds())
	icon.Draw(rasterx.NewDasher(w, h, scanner), 1.0)

	// rasterx writes alpha-premultiplied RGBA; draw.Src into NRGBA converts to
	// the straight-alpha layout the sprite atlas expects.
	nrgba := image.NewNRGBA(rgba.Bounds())
	draw.Draw(nrgba, nrgba.Bounds(), rgba, image.Point{}, draw.Src)

	return &Rendered{
		Image:   nrgba,
		PivotX:  -vb[0] * pxPerMM, // origin lands at (-minX,-minY) after normalize
		PivotY:  -vb[1] * pxPerMM,
		Missing: missing,
	}, nil
}

// flatten resolves CSS classes to inline style, strips .layout elements, and
// normalizes the viewBox origin to (0,0). It returns the rewritten SVG, the
// original viewBox [minX,minY,w,h], and any class tokens with no CSS rule.
func flatten(src []byte, css map[string]string, strokeScale float64) (out []byte, vb [4]float64, missing []string, err error) {
	dec := xml.NewDecoder(bytes.NewReader(src))
	var buf bytes.Buffer
	var stack []string
	seenMissing := map[string]bool{}

	for {
		tok, e := dec.Token()
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, vb, missing, e
		}
		if ee, ok := tok.(xml.EndElement); ok {
			if len(stack) > 0 && stack[len(stack)-1] == ee.Name.Local {
				if ee.Name.Local == "svg" {
					buf.WriteString("</g></svg>")
				} else {
					fmt.Fprintf(&buf, "</%s>", ee.Name.Local)
				}
				stack = stack[:len(stack)-1]
			}
			continue
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		name := se.Name.Local
		classes := attrVal(se, "class")
		if hasClass(classes, "layout") || attrVal(se, "display") == "none" ||
			name == "metadata" || name == "title" || name == "desc" {
			_ = dec.Skip()
			continue
		}
		style := resolveStyle(classes, css, seenMissing, &missing)
		switch name {
		case "svg":
			vb = parseViewBox(attrVal(se, "viewBox"))
			fmt.Fprintf(&buf, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %g %g">`, vb[2], vb[3])
			fmt.Fprintf(&buf, `<g transform="translate(%g %g)">`, -vb[0], -vb[1])
			stack = append(stack, name)
		case "g":
			buf.WriteString("<g")
			writeAttrs(&buf, se, style, strokeScale, "class")
			buf.WriteString(">")
			stack = append(stack, name)
		case "path", "rect", "circle", "line", "polygon", "polyline", "ellipse":
			fmt.Fprintf(&buf, "<%s", name)
			writeAttrs(&buf, se, style, strokeScale, "class")
			buf.WriteString("/>")
			_ = dec.Skip()
		}
	}
	return buf.Bytes(), vb, missing, nil
}

func resolveStyle(classes string, css map[string]string, seen map[string]bool, missing *[]string) string {
	if classes == "" {
		return ""
	}
	var decls []string
	for c := range strings.FieldsSeq(classes) {
		if decl, ok := css[c]; ok {
			if decl != "" {
				decls = append(decls, decl)
			}
		} else if c != "f0" && !seen[c] {
			seen[c] = true
			*missing = append(*missing, c)
		}
	}
	return strings.Join(decls, ";")
}

func writeAttrs(out *bytes.Buffer, se xml.StartElement, style string, strokeScale float64, skip ...string) {
	skipped := map[string]bool{}
	for _, s := range skip {
		skipped[s] = true
	}
	for _, a := range se.Attr {
		if a.Name.Local == "" || skipped[a.Name.Local] || a.Name.Space == "xmlns" || a.Name.Local == "xmlns" {
			continue
		}
		if a.Name.Local == "stroke-width" && strokeScale != 1 {
			if v, err := strconv.ParseFloat(a.Value, 64); err == nil {
				fmt.Fprintf(out, ` stroke-width=%q`, strconv.FormatFloat(v*strokeScale, 'g', -1, 64))
				continue
			}
		}
		fmt.Fprintf(out, ` %s=%q`, a.Name.Local, a.Value)
	}
	if style != "" {
		fmt.Fprintf(out, ` style=%q`, style)
	}
}

func parseViewBox(s string) (vb [4]float64) {
	fields := strings.FieldsFunc(s, func(r rune) bool { return r == ' ' || r == ',' || r == '\t' })
	for i := 0; i < 4 && i < len(fields); i++ {
		vb[i], _ = strconv.ParseFloat(fields[i], 64)
	}
	return vb
}

func attrVal(se xml.StartElement, local string) string {
	for _, a := range se.Attr {
		if a.Name.Local == local {
			return a.Value
		}
	}
	return ""
}

func hasClass(classes, want string) bool {
	for c := range strings.FieldsSeq(classes) {
		if c == want {
			return true
		}
	}
	return false
}
