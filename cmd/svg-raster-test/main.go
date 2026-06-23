// Command svg-raster-test is the Phase 3 / Workstream B de-risk for the S-101
// backport (specs/s101-portrayal-backport.md). It proves the one remaining
// technical unknown: can we rasterize S-101 symbol SVGs in pure Go *and*
// resolve their CSS colour-class tokens (fXXX/sXXX) against a palette, which no
// off-the-shelf rasterizer does.
//
// Pipeline per symbol: read SVG -> resolve <?xml-stylesheet?> CSS classes into
// inline style + strip .layout debug boxes -> oksvg/rasterx -> PNG.
//
// Usage:
//
//	go run ./cmd/svg-raster-test --scale 12 --out /tmp/s101-symbols [SYMBOL ...]
package main

import (
	"bytes"
	"encoding/xml"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/srwiley/oksvg"
	"github.com/srwiley/rasterx"
)

// strokeScale compensates for oksvg not scaling stroke-width by the draw
// transform; set from the --scale flag in main.
var strokeScale = 1.0

// representative set spanning fills, strokes, multi-path, and a <g>-wrapped one.
var defaultSymbols = []string{
	"BCNCAR01", "BOYCAN10", "LIGHTS11", "ACHARE02", "CHINFO11", "WRECKS04",
	"BCNLAT15", "CBLARE52", "ISODGR01", "SOUNDSA2", "TOPMAR21", "DANGER02",
}

func main() {
	symbolsDir := flag.String("symbols", "/home/jcollins/Projects/s101-portrayal-catalogue/PortrayalCatalog/Symbols", "S-101 Symbols dir (also holds the *SvgStyle.css)")
	cssName := flag.String("css", "daySvgStyle.css", "stylesheet to resolve classes against (day/dusk/night)")
	outDir := flag.String("out", "/tmp/s101-symbols", "output PNG directory")
	scale := flag.Float64("scale", 12, "pixels per mm")
	bg := flag.String("bg", "#dfe6ea", "background fill (#rrggbb, or 'none' for transparent)")
	flag.Parse()

	strokeScale = *scale
	css, err := loadCSS(filepath.Join(*symbolsDir, *cssName))
	if err != nil {
		fatal("load css: %v", err)
	}
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		fatal("mkdir: %v", err)
	}

	names := flag.Args()
	if len(names) == 0 {
		names = defaultSymbols
	}

	unresolved := map[string]bool{}
	ok, fail := 0, 0
	for _, name := range names {
		path := filepath.Join(*symbolsDir, name+".svg")
		flat, missing, err := flatten(path, css)
		for c := range missing {
			unresolved[c] = true
		}
		if err != nil {
			fmt.Printf("  %-10s SKIP  %v\n", name, err)
			fail++
			continue
		}
		_ = os.WriteFile(filepath.Join(*outDir, name+".flat.svg"), flat, 0o644) // for reference-rasterizer comparison
		w, h, err := rasterize(flat, *scale, *bg, filepath.Join(*outDir, name+".png"))
		if err != nil {
			fmt.Printf("  %-10s FAIL  %v\n", name, err)
			fail++
			continue
		}
		fmt.Printf("  %-10s OK    %dx%d px\n", name, w, h)
		ok++
	}

	fmt.Printf("\nRendered %d/%d symbols to %s (scale %.0f px/mm, css %s).\n", ok, ok+fail, *outDir, *scale, *cssName)
	if len(unresolved) > 0 {
		var u []string
		for c := range unresolved {
			u = append(u, c)
		}
		sort.Strings(u)
		fmt.Printf("Classes with no CSS rule (ignored): %s\n", strings.Join(u, " "))
	}
	if fail > 0 {
		os.Exit(1)
	}
	fmt.Println("RESULT: PASS — S-101 SVG + CSS-class colour resolution works in pure Go.")
}

// --- CSS ---

var cssRuleRE = regexp.MustCompile(`\.([A-Za-z0-9_]+)\s*\{([^}]*)\}`)

// loadCSS returns class name -> resolved declaration string (e.g. "fill:#000000").
func loadCSS(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, m := range cssRuleRE.FindAllStringSubmatch(string(data), -1) {
		decl := strings.TrimSpace(m[2])
		decl = strings.TrimSuffix(decl, ";")
		out[m[1]] = decl
	}
	return out, nil
}

// --- SVG flattening ---

// flatten reads an S-101 symbol SVG and returns an equivalent SVG with CSS
// classes resolved to inline style and .layout debug elements removed. It also
// reports class tokens that had no CSS rule.
func flatten(path string, css map[string]string) (svg []byte, missing map[string]bool, err error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	missing = map[string]bool{}
	dec := xml.NewDecoder(bytes.NewReader(src))
	var out bytes.Buffer
	var stack []string // open container element names we emitted (svg, g)

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, missing, err
		}
		se, isStart := tok.(xml.StartElement)
		if ee, isEnd := tok.(xml.EndElement); isEnd {
			if len(stack) > 0 && stack[len(stack)-1] == ee.Name.Local {
				if ee.Name.Local == "svg" {
					out.WriteString("</g></svg>") // close the normalizing wrapper
				} else {
					fmt.Fprintf(&out, "</%s>", ee.Name.Local)
				}
				stack = stack[:len(stack)-1]
			}
			continue
		}
		if !isStart {
			continue
		}
		name := se.Name.Local
		classes := attrVal(se, "class")

		// Drop debug overlays and non-rendering metadata wholesale.
		if hasClass(classes, "layout") || attrVal(se, "display") == "none" ||
			name == "metadata" || name == "title" || name == "desc" {
			_ = dec.Skip()
			continue
		}

		style := resolveStyle(classes, css, missing)
		switch name {
		case "svg":
			// Normalize the viewBox to a (0,0) origin and translate the content
			// to match. oksvg's SetTarget mishandles a non-zero viewBox origin
			// (it offsets in pixel space by unscaled viewBox units), and every
			// S-101 symbol has a negative origin. Wrapping sidesteps the bug.
			mx, my, vw, vh := parseViewBox(attrVal(se, "viewBox"))
			fmt.Fprintf(&out, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %g %g">`, vw, vh)
			fmt.Fprintf(&out, `<g transform="translate(%g %g)">`, -mx, -my)
			stack = append(stack, name)
		case "g":
			out.WriteString("<g")
			writeAttrs(&out, se, style, "class")
			out.WriteString(">")
			stack = append(stack, name)
		case "path", "rect", "circle", "line", "polygon", "polyline", "ellipse":
			fmt.Fprintf(&out, "<%s", name)
			writeAttrs(&out, se, style, "class")
			out.WriteString("/>")
			_ = dec.Skip() // self-closing in source; consume its EndElement
		default:
			// unknown element: keep children but drop the wrapper
		}
	}
	return out.Bytes(), missing, nil
}

// resolveStyle concatenates the CSS declarations for each class token; unknown
// tokens are recorded in missing and skipped.
func resolveStyle(classes string, css map[string]string, missing map[string]bool) string {
	if classes == "" {
		return ""
	}
	var decls []string
	for c := range strings.FieldsSeq(classes) {
		if decl, ok := css[c]; ok {
			if decl != "" {
				decls = append(decls, decl)
			}
		} else if c != "f0" { // f0 is fill:none and always present; others noted
			missing[c] = true
		}
	}
	return strings.Join(decls, ";")
}

// writeAttrs copies se's attributes (by local name) except those in skip, then
// appends a style attribute when style is non-empty.
func writeAttrs(out *bytes.Buffer, se xml.StartElement, style string, skip ...string) {
	skipped := map[string]bool{}
	for _, s := range skip {
		skipped[s] = true
	}
	for _, a := range se.Attr {
		if a.Name.Local == "" || skipped[a.Name.Local] || a.Name.Space == "xmlns" || a.Name.Local == "xmlns" {
			continue
		}
		// oksvg applies stroke-width in device px without scaling by the
		// SetTarget transform (geometry IS scaled). Pre-multiply so strokes
		// render at the correct on-screen weight.
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

// parseViewBox parses "minX minY width height" (space- or comma-separated).
func parseViewBox(s string) (minX, minY, w, h float64) {
	fields := strings.FieldsFunc(s, func(r rune) bool { return r == ' ' || r == ',' || r == '\t' })
	v := make([]float64, 4)
	for i := 0; i < 4 && i < len(fields); i++ {
		v[i], _ = strconv.ParseFloat(fields[i], 64)
	}
	return v[0], v[1], v[2], v[3]
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

// --- rasterization ---

func rasterize(svg []byte, scale float64, bg, outPath string) (int, int, error) {
	icon, err := oksvg.ReadIconStream(bytes.NewReader(svg))
	if err != nil {
		return 0, 0, err
	}
	w := int(math.Ceil(icon.ViewBox.W * scale))
	h := int(math.Ceil(icon.ViewBox.H * scale))
	if w < 1 || h < 1 {
		return 0, 0, fmt.Errorf("degenerate viewBox %vx%v", icon.ViewBox.W, icon.ViewBox.H)
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	if c, ok := parseHex(bg); ok {
		fill(img, c)
	}
	icon.SetTarget(0, 0, float64(w), float64(h))
	scanner := rasterx.NewScannerGV(w, h, img, img.Bounds())
	raster := rasterx.NewDasher(w, h, scanner)
	icon.Draw(raster, 1.0)

	f, err := os.Create(outPath)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()
	return w, h, png.Encode(f, img)
}

func fill(img *image.RGBA, c color.RGBA) {
	for y := img.Rect.Min.Y; y < img.Rect.Max.Y; y++ {
		for x := img.Rect.Min.X; x < img.Rect.Max.X; x++ {
			img.SetRGBA(x, y, c)
		}
	}
}

func parseHex(s string) (color.RGBA, bool) {
	if len(s) != 7 || s[0] != '#' {
		return color.RGBA{}, false
	}
	v, err := strconv.ParseUint(s[1:], 16, 32)
	if err != nil {
		return color.RGBA{}, false
	}
	return color.RGBA{R: byte(v >> 16), G: byte(v >> 8), B: byte(v), A: 255}, true
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "svg-raster-test: "+format+"\n", args...)
	os.Exit(1)
}
