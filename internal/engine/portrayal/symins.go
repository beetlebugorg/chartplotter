package portrayal

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/beetlebugorg/chartplotter/pkg/geo"
	"github.com/beetlebugorg/chartplotter/pkg/s57"
)

// SYMINS02 (S-52 PresLib §13.2.18 / §10.3.3.8) — portray an S-57 NEWOBJ from its
// SYMINS attribute, the producer's explicit "symbol instruction" string. SYMINS is
// a ';'-separated list of S-52 draw instructions — SY()/TX()/TE()/LS()/LC()/AC()/
// AP() — that we render verbatim, instead of routing NEWOBJ to the V-AIS alias
// (the S-101 FeatureCatalogue maps NEWOBJ→VirtualAISAidToNavigation, which would
// stamp a V-AIS mark and ignore the producer's instruction). This is how the S-52
// PresLib "ECDIS Chart 1" labels (164 TX), boundaries (LS/LC), fills (AC/AP) and
// the size-check symbol SY(CHKSYM01) are drawn.
//
// Returns ok=false when the feature has no usable SYMINS, so the caller falls back
// to the default new-object symbology.
func parseSYMINS(f *s57.Feature) (FeatureBuild, bool) {
	attrs := f.Attributes()
	raw, _ := attrs["SYMINS"].(string)
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return FeatureBuild{}, false
	}
	g := geometryOf(f.Geometry())
	anchor, hasAnchor := representativePoint(f)

	var prims []Primitive
	for _, instr := range splitSyminsInstructions(raw) {
		op, params, ok := splitSyminsOp(instr)
		if !ok {
			continue
		}
		switch op {
		case "SY": // point symbol — SY(NAME[,rot])
			if !hasAnchor {
				continue
			}
			args := splitSyminsArgs(params)
			name := strings.TrimSpace(firstOr(args, ""))
			if name == "" {
				continue
			}
			rot := float32(0)
			if len(args) > 1 {
				if v, err := strconv.ParseFloat(strings.TrimSpace(args[1]), 32); err == nil {
					rot = float32(v)
				}
			}
			prims = append(prims, SymbolCall{
				Anchor: anchor, SymbolName: name, RotationDeg: rot,
				Scale: DefaultPxPerSymbolUnit, SoundingDepthM: nan32, DangerDepthM: nan32,
			})
		case "TX", "TE": // text label
			if !hasAnchor {
				continue
			}
			if t, ok := parseSyminsText(op, params, attrs, anchor); ok {
				prims = append(prims, t)
			}
		case "LS": // simple line — LS(style,width,colour)
			args := splitSyminsArgs(params)
			if len(args) < 3 {
				continue
			}
			w, _ := strconv.Atoi(strings.TrimSpace(args[1]))
			if w <= 0 {
				w = 1
			}
			color := strings.TrimSpace(args[2])
			dash := syminsDash(strings.TrimSpace(args[0]))
			for _, line := range syminsLines(g) {
				prims = append(prims, StrokeLine{Points: line, ColorToken: color, WidthPx: float32(w), Dash: dash})
			}
		case "LC": // complex (symbolised) line — LC(LINESTYLE)
			name := strings.TrimSpace(firstOr(splitSyminsArgs(params), ""))
			if name == "" {
				continue
			}
			for _, line := range syminsLines(g) {
				prims = append(prims, LinePattern{Points: line, LinestyleName: name})
			}
		case "AC": // area colour fill — AC(COLOUR[,transp])
			color := strings.TrimSpace(firstOr(splitSyminsArgs(params), ""))
			if color != "" && len(g.area) > 0 {
				prims = append(prims, FillPolygon{Rings: g.area, ColorToken: color})
			}
		case "AP": // area pattern fill — AP(PATTERN)
			name := strings.TrimSpace(firstOr(splitSyminsArgs(params), ""))
			if name != "" && len(g.area) > 0 {
				prims = append(prims, PatternFill{Rings: g.area, PatternName: name})
			}
		}
	}
	if len(prims) == 0 {
		return FeatureBuild{}, false
	}
	return FeatureBuild{Primitives: prims, DisplayPriority: 6, DisplayCategory: displayStandard}, true
}

// parseSyminsText parses a SYMINS TX()/TE() instruction into a DrawText.
//
//	TX(string|attr, hjust, vjust, space, chars, xoffs, yoffs, colour, display)
//	TE(format, attribs, hjust, vjust, space, chars, xoffs, yoffs, colour, display)
func parseSyminsText(op, params string, attrs map[string]any, anchor geo.LatLon) (DrawText, bool) {
	args := splitSyminsArgs(params)
	var text string
	var hjustIdx, vjustIdx, charsIdx, xoffIdx, yoffIdx, colorIdx, displayIdx int
	if op == "TE" {
		if len(args) < 10 {
			return DrawText{}, false
		}
		format := strings.Trim(args[0], "'\"")
		var names []string
		for _, a := range strings.Split(strings.Trim(args[1], "'\""), ",") {
			if a = strings.TrimSpace(a); a != "" {
				names = append(names, a)
			}
		}
		t, ok := formatSubstitute(attrs, format, names)
		if !ok {
			return DrawText{}, false
		}
		text = t
		hjustIdx, vjustIdx, charsIdx, xoffIdx, yoffIdx, colorIdx, displayIdx = 2, 3, 5, 6, 7, 8, 9
	} else { // TX
		if len(args) < 9 {
			return DrawText{}, false
		}
		rawStr := args[0]
		if strings.HasPrefix(rawStr, "'") || strings.HasPrefix(rawStr, "\"") {
			text = strings.Trim(rawStr, "'\"") // literal
		} else { // attribute reference
			v, ok := attrs[strings.TrimSpace(rawStr)]
			if !ok || v == nil {
				return DrawText{}, false
			}
			text = fmt.Sprintf("%v", v)
		}
		hjustIdx, vjustIdx, charsIdx, xoffIdx, yoffIdx, colorIdx, displayIdx = 1, 2, 4, 5, 6, 7, 8
	}
	if text == "" {
		return DrawText{}, false
	}
	color := strings.TrimSpace(argAt(args, colorIdx))
	if color == "" {
		color = "CHBLK"
	}
	hjust, _ := strconv.Atoi(strings.TrimSpace(argAt(args, hjustIdx)))
	vjust, _ := strconv.Atoi(strings.TrimSpace(argAt(args, vjustIdx)))
	group, _ := strconv.Atoi(strings.TrimSpace(argAt(args, displayIdx)))
	xoff, _ := strconv.Atoi(strings.TrimSpace(argAt(args, xoffIdx)))
	yoff, _ := strconv.Atoi(strings.TrimSpace(argAt(args, yoffIdx)))
	fontPx := syminsFontPx(strings.Trim(argAt(args, charsIdx), "'\""))
	var halo *TextHalo
	if fontPx >= 10 {
		halo = &TextHalo{ColorToken: "CHWHT", WidthPx: 1}
	}
	return DrawText{
		Anchor: anchor, Text: text, FontSizePx: fontPx, ColorToken: color, Halo: halo,
		HAlign: syminsHAlign(hjust), VAlign: syminsVAlign(vjust),
		// S-52 §8.3.3.2 XOFFS/YOFFS are in units of the text body size (+x right, +y down).
		OffsetXPx: float32(xoff) * fontPx, OffsetYPx: float32(yoff) * fontPx,
		Group: group,
	}, true
}

// syminsFontPx converts a SYMINS CHARS field (e.g. '15110' = style/weight/slant +
// two-digit body size) to a pixel font size. The body size is in points; one point
// is 0.351 mm, scaled to px at the app's reference pixel pitch (100·DefaultPxPerSymbolUnit
// px/mm). Falls back to the engine default (12 px) on a malformed field.
func syminsFontPx(chars string) float32 {
	if len(chars) >= 5 {
		if body, err := strconv.Atoi(chars[3:5]); err == nil && body > 0 {
			return float32(body) * 0.351 * 100 * float32(DefaultPxPerSymbolUnit)
		}
	}
	return 12
}

// syminsHAlign maps S-52 HJUST (1 centre, 2 right, 3 left) to HAlign.
func syminsHAlign(h int) HAlign {
	switch h {
	case 1:
		return HAlignCenter
	case 2:
		return HAlignRight
	default:
		return HAlignLeft
	}
}

// syminsVAlign maps S-52 VJUST (1 bottom, 2 centre, 3 top) to VAlign.
func syminsVAlign(v int) VAlign {
	switch v {
	case 1:
		return VAlignBottom
	case 3:
		return VAlignTop
	default:
		return VAlignMiddle
	}
}

func syminsDash(style string) Dash {
	switch strings.ToUpper(style) {
	case "DASH":
		return DashDashed
	case "DOTT":
		return DashDotted
	default:
		return DashSolid
	}
}

// syminsLines returns the polyline(s) a line/area instruction (LS/LC) strokes: a
// line feature's polyline, or each ring of an area feature (closed).
func syminsLines(g geom) [][]geo.LatLon {
	switch g.kind {
	case geomLine:
		if len(g.line) >= 2 {
			return [][]geo.LatLon{g.line}
		}
	case geomArea:
		var out [][]geo.LatLon
		for _, r := range g.area {
			if len(r) >= 2 {
				if r[0] != r[len(r)-1] {
					r = append(append([]geo.LatLon(nil), r...), r[0])
				}
				out = append(out, r)
			}
		}
		return out
	}
	return nil
}

// splitSyminsInstructions splits a SYMINS string on ';', honouring quotes and
// nested parens (so a ';' inside TX('a;b',…) or between parens isn't a split).
func splitSyminsInstructions(s string) []string {
	var out []string
	var cur strings.Builder
	depth, inQuote := 0, false
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '\'', '"':
			inQuote = !inQuote
			cur.WriteByte(c)
		case '(':
			if !inQuote {
				depth++
			}
			cur.WriteByte(c)
		case ')':
			if !inQuote {
				depth--
			}
			cur.WriteByte(c)
		case ';':
			if !inQuote && depth == 0 {
				out = append(out, cur.String())
				cur.Reset()
			} else {
				cur.WriteByte(c)
			}
		default:
			cur.WriteByte(c)
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

// splitSyminsOp splits "OP(params)" into the op and the inner params.
func splitSyminsOp(instr string) (op, params string, ok bool) {
	instr = strings.TrimSpace(instr)
	open := strings.IndexByte(instr, '(')
	closeI := strings.LastIndexByte(instr, ')')
	if open <= 0 || closeI < open {
		return "", "", false
	}
	return strings.TrimSpace(instr[:open]), instr[open+1 : closeI], true
}

// splitSyminsArgs splits an instruction's params on ',', honouring single/double
// quotes (so a comma inside a quoted format/string stays in one arg).
func splitSyminsArgs(params string) []string {
	var out []string
	var cur strings.Builder
	inQuote := false
	for i := 0; i < len(params); i++ {
		switch c := params[i]; c {
		case '\'', '"':
			inQuote = !inQuote
			cur.WriteByte(c)
		case ',':
			if inQuote {
				cur.WriteByte(c)
			} else {
				out = append(out, strings.TrimSpace(cur.String()))
				cur.Reset()
			}
		default:
			cur.WriteByte(c)
		}
	}
	out = append(out, strings.TrimSpace(cur.String()))
	return out
}

func firstOr(args []string, def string) string {
	if len(args) > 0 {
		return args[0]
	}
	return def
}

func argAt(args []string, i int) string {
	if i >= 0 && i < len(args) {
		return args[i]
	}
	return ""
}
