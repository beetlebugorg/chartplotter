// Package instructions parses the flat drawing-instruction stream emitted by
// S-101 portrayal rules (see specs/s101-portrayal-backport.md) into structured
// draw commands that downstream code lowers onto the engine's primitive layer.
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
	Rotation     float64
	HasRotation  bool

	LinePlacement string      // raw, e.g. "Relative,0.5"
	SimpleLine    *SimpleLine // set when Op==OpLine and Reference=="_simple_"

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
		rotation     float64
		hasRotation  bool
		linePlace    string
		simple       *SimpleLine
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
			DisplayPlane: displayPlane, Offset: offset, Rotation: rotation,
			HasRotation: hasRotation, LinePlacement: linePlace, Raw: raw,
		}
		if op == OpLine && ref == "_simple_" {
			c.SimpleLine = simple
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
		case "Rotation":
			rotation, hasRotation = atof(arg(in, 0)), true
		case "LinePlacement":
			linePlace = strings.Join(in.Args, ",")
		case "Dash":
			// recorded into the next _simple_ line style via LineStyle below;
			// standalone Dash only meaningfully precedes a LineStyle.
		case "LineStyle":
			simple = parseSimpleLine(in.Args)
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
			emit(OpText, strings.Join(in.Args, ","), in.Raw)
		case "NullInstruction":
			emit(OpNull, "", in.Raw)

		// recognized-but-not-yet-lowered draws → gap
		case "AugmentedRay", "AugmentedPath", "AugmentedPoint", "ArcByRadius", "CoverageFill":
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
