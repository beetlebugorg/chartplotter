// Package instructions parses the flat drawing-instruction stream emitted by
// S-101 portrayal rules into structured draw commands that downstream code
// lowers onto the engine's primitive layer.
//
// The stream is a ';'-separated list of "Keyword:arg,arg,..." tokens (or bare
// keywords like NullInstruction). Following the S-100 Part 9 portrayal model,
// tokens are either MODIFIERS that set state (ViewingGroup, DrawingPriority,
// DisplayPlane, LocalOffset, Rotation, LinePlacement, Dash, and an inline
// LineStyle:_simple_ definition) or DRAWS that consume the current state
// (PointInstruction, LineInstruction, ColorFill, AreaFillReference, Text,
// NullInstruction). Reduce folds a parsed stream into one DrawCommand per draw,
// each carrying a snapshot of the state in effect.
package instructions

import (
	"strconv"
	"strings"
)

// Instruction is one parsed token from a drawing-instruction stream.
type Instruction struct {
	Kind string   // token before the first ':' (or the whole bare token)
	Args []string // ','-separated payload after the first ':'
	Raw  string
}

// ParseStream tokenizes a ';'-separated S-101 instruction stream. Empty tokens
// are skipped; surrounding whitespace is trimmed.
func ParseStream(stream string) []Instruction {
	var out []Instruction
	for tok := range strings.SplitSeq(stream, ";") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		kind, rest, hasArgs := strings.Cut(tok, ":")
		ins := Instruction{Kind: strings.TrimSpace(kind), Raw: tok}
		if hasArgs {
			ins.Args = strings.Split(rest, ",")
		}
		out = append(out, ins)
	}
	return out
}

// DrawOp classifies a resolved drawing command.
type DrawOp string

const (
	OpPoint     DrawOp = "Point"     // place a point symbol
	OpLine      DrawOp = "Line"      // draw a line style along the geometry
	OpColorFill DrawOp = "ColorFill" // solid area fill with a colour token
	OpAreaFill  DrawOp = "AreaFill"  // tiled area fill referencing a fill/pattern
	OpText      DrawOp = "Text"      // text label
	OpNull      DrawOp = "Null"      // explicit no-op (suppress)
	OpOther     DrawOp = "Other"     // recognized draw we don't lower yet (gap)
)

// SimpleLine is an inline "LineStyle:_simple_,<dash>,<width>,<colour>" definition,
// referenced by a subsequent "LineInstruction:_simple_".
type SimpleLine struct {
	DashLength float64 // 0 for a solid line
	Width      float64
	Color      string
}

// DrawCommand is a draw instruction resolved against the portrayal state that
// was in effect when it was emitted.
type DrawCommand struct {
	Op           DrawOp
	Reference    string // symbol id / line-style id / colour token / fill id / text
	ViewingGroup int
	Priority     int
	DisplayPlane string // "UnderRadar" | "OverRadar" (empty if unset)
	Offset       [2]float64
	// Anchor / HasAnchor carry an explicit draw location from an AugmentedPoint
	// (GeographicCRS lon,lat) — used for SOUNDG, whose one feature emits a symbol
	// per sounding at its own point. When HasAnchor is false the draw attaches to
	// the feature geometry (the usual case).
	Anchor      [2]float64 // {lon, lat}
	HasAnchor   bool
	Rotation    float64
	HasRotation bool
	// RotationTrueNorth is set when the rotation is in GeographicCRS (referenced
	// to true north, so the symbol turns WITH the chart — e.g. a directional
	// light's orientation); false means PortrayalCRS (screen-referenced, e.g. the
	// 135° light flare), which stays upright to the screen.
	RotationTrueNorth bool

	LinePlacement string      // raw, e.g. "Relative,0.5"
	SimpleLine    *SimpleLine // set when Op==OpLine and Reference=="_simple_"

	// Text style (set on OpText): the resolved text is in Reference.
	FontColor   string  // colour token (e.g. CHBLK)
	FontSizePx  float64 // 0 ⇒ default
	TextAlignH  string  // "Center" | "Left" | "Right"
	TextAlignV  string  // "Top" | "Bottom" | "Center"
	TextVOffset float64

	Raw string // the originating draw token, for debugging
}

// Reduce folds a parsed stream into one DrawCommand per draw instruction,
// snapshotting modifier state. Modifier tokens it does not recognize are
// returned in unsupported (deduped) so callers can surface gaps; recognized
// draws it doesn't yet lower become OpOther commands.
func Reduce(ins []Instruction) (cmds []DrawCommand, unsupported []string) {
	var (
		viewingGroup int
		priority     int
		displayPlane string
		offset       [2]float64
		anchor       [2]float64
		hasAnchor    bool
		rotation     float64
		hasRotation  bool
		rotTrueNorth bool
		linePlace    string
		simple       *SimpleLine
		fontColor    string
		fontSize     float64
		textAlignH   string
		textAlignV   string
		textVOffset  float64
		seenUnsup    = map[string]bool{}
	)
	noteUnsupported := func(kind string) {
		if !seenUnsup[kind] {
			seenUnsup[kind] = true
			unsupported = append(unsupported, kind)
		}
	}
	emit := func(op DrawOp, ref, raw string) {
		c := DrawCommand{
			Op: op, Reference: ref, ViewingGroup: viewingGroup, Priority: priority,
			DisplayPlane: displayPlane, Offset: offset, Anchor: anchor, HasAnchor: hasAnchor,
			Rotation: rotation, HasRotation: hasRotation, RotationTrueNorth: rotTrueNorth,
			LinePlacement: linePlace, Raw: raw,
		}
		if op == OpLine && ref == "_simple_" {
			c.SimpleLine = simple
		}
		if op == OpText {
			c.FontColor, c.FontSizePx = fontColor, fontSize
			c.TextAlignH, c.TextAlignV, c.TextVOffset = textAlignH, textAlignV, textVOffset
		}
		cmds = append(cmds, c)
	}

	for _, in := range ins {
		switch in.Kind {
		// --- modifiers (set state) ---
		case "ViewingGroup":
			viewingGroup = atoi(arg(in, 0))
		case "DrawingPriority":
			priority = atoi(arg(in, 0))
		case "DisplayPlane":
			displayPlane = arg(in, 0)
		case "LocalOffset":
			offset = [2]float64{atof(arg(in, 0)), atof(arg(in, 1))}
		case "AugmentedPoint":
			// "AugmentedPoint:<CRS>,<x>,<y>" places subsequent point draws at the
			// geographic point (x=lon, y=lat) — SOUNDG emits one per sounding.
			anchor, hasAnchor = [2]float64{atof(arg(in, 1)), atof(arg(in, 2))}, true
		case "ClearGeometry":
			// End of an augmented-geometry run: drop the explicit anchor/offset so
			// later draws re-attach to the feature geometry.
			hasAnchor, anchor, offset = false, [2]float64{}, [2]float64{}
		case "Rotation":
			// S-101 form: "Rotation:<CRS>,<angle>" where CRS is GeographicCRS
			// (true-north, rotates with the chart) or PortrayalCRS (screen). A
			// bare "Rotation:<angle>" (no CRS) is tolerated as screen-referenced.
			crs, ang := arg(in, 0), arg(in, 1)
			if ang == "" {
				crs, ang = "", crs
			}
			rotation, hasRotation = atof(ang), true
			rotTrueNorth = crs == "GeographicCRS"
		case "LinePlacement":
			linePlace = strings.Join(in.Args, ",")
		case "Dash":
			// recorded into the next _simple_ line style via LineStyle below;
			// standalone Dash only meaningfully precedes a LineStyle.
		case "LineStyle":
			simple = parseSimpleLine(in.Args)
		// --- text-style modifiers (apply to the next TextInstruction) ---
		case "FontColor":
			fontColor = arg(in, 0)
		case "FontSize":
			fontSize = atof(arg(in, 0))
		case "TextAlignHorizontal":
			textAlignH = arg(in, 0)
		case "TextAlignVertical":
			textAlignV = arg(in, 0)
		case "TextVerticalOffset":
			textVOffset = atof(arg(in, 0))
		// modifiers we intentionally ignore for geometry lowering
		case "ScaleMinimum", "ScaleMaximum", "Date", "Time", "DateTime", "TimeValid",
			"AlertReference", "Warning", "Error", "Hover", "SpatialReference":
			// no-op for primitive lowering

		// --- draws (consume state) ---
		case "PointInstruction":
			emit(OpPoint, arg(in, 0), in.Raw)
		case "LineInstruction", "LineInstructionUnsuppressed":
			emit(OpLine, arg(in, 0), in.Raw)
		case "ColorFill":
			emit(OpColorFill, arg(in, 0), in.Raw)
		case "AreaFillReference":
			emit(OpAreaFill, arg(in, 0), in.Raw)
		case "TextInstruction":
			// Text is DEF-encoded (separators escaped) so it survives the ;/:/,
			// tokenizing; decode it back to the display string.
			emit(OpText, decodeDEF(strings.Join(in.Args, ",")), in.Raw)
		case "NullInstruction":
			emit(OpNull, "", in.Raw)

		// recognized-but-not-yet-lowered draws → gap
		case "AugmentedRay", "AugmentedPath", "ArcByRadius", "CoverageFill":
			emit(OpOther, in.Kind, in.Raw)

		default:
			// Font*/TextAlign* etc. are text-state modifiers; everything else
			// unknown is surfaced for triage.
			if !strings.HasPrefix(in.Kind, "Font") && !strings.HasPrefix(in.Kind, "Text") {
				noteUnsupported(in.Kind)
			}
		}
	}
	return cmds, unsupported
}

// decodeDEF reverses the framework's EncodeDEFString escaping (& ; : , →
// &a &s &c &m). The escape char (&a→&) is decoded last.
func decodeDEF(s string) string {
	if !strings.Contains(s, "&") {
		return s
	}
	s = strings.ReplaceAll(s, "&s", ";")
	s = strings.ReplaceAll(s, "&c", ":")
	s = strings.ReplaceAll(s, "&m", ",")
	s = strings.ReplaceAll(s, "&a", "&")
	return s
}

func parseSimpleLine(args []string) *SimpleLine {
	// LineStyle:_simple_,<dashLength>,<width>,<colour>
	sl := &SimpleLine{}
	if len(args) > 1 {
		sl.DashLength = atof(args[1])
	}
	if len(args) > 2 {
		sl.Width = atof(args[2])
	}
	if len(args) > 3 {
		sl.Color = args[3]
	}
	return sl
}

func arg(in Instruction, i int) string {
	if i < len(in.Args) {
		return strings.TrimSpace(in.Args[i])
	}
	return ""
}

func atoi(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

func atof(s string) float64 {
	f, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return f
}
