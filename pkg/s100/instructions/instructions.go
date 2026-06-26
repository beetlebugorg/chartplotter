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
	// OpAugmentedLine strokes a screen-space figure the rule CONSTRUCTED via an
	// AugmentedRay / ArcByRadius instruction (a light-sector leg or arc/ring),
	// rather than the feature's own geometry. The mm sizes are screen-fixed, so the
	// figure is carried (see DrawCommand.Augmented) for per-zoom tessellation.
	OpAugmentedLine DrawOp = "AugmentedLine"
	OpOther         DrawOp = "Other" // recognized draw we don't emit yet (gap)
)

// AugGeomKind is the kind of constructed figure element a DrawCommand.Augmented
// carries.
type AugGeomKind uint8

const (
	AugRay AugGeomKind = iota // a straight leg from the anchor (AugmentedRay)
	AugArc                    // a circular arc/ring centred on the anchor (ArcByRadius)
)

// AugmentedGeom is one screen-space figure element the rule constructed and a
// LineInstruction then strokes — a light-sector leg (ray) or its arc/ring. All
// sizes are display millimetres (the rule emits them in LocalCRS), so the baker
// tessellates per-zoom; they cannot bake as static geographic geometry.
type AugmentedGeom struct {
	Kind AugGeomKind
	// Ray ("AugmentedRay:<crs>,<bearing>,<lenCRS>,<lenMM>"): a leg from the anchor
	// at BearingDeg (true-north; the rule has already applied the from-seaward
	// +180 reversal) of length LengthMM.
	BearingDeg float64
	LengthMM   float64
	// LengthGroundM is the leg length when the rule gave it in GeographicCRS — a
	// fixed GROUND distance in metres (a sectorLineLength or full-VALNMR leg),
	// rendered zoom-dependently. Mutually exclusive with LengthMM (display mm).
	LengthGroundM float64
	// Arc ("ArcByRadius:<cx>,<cy>,<radiusMM>,<startDeg>,<sweepDeg>"): centred on the
	// anchor, RadiusMM, from StartDeg sweeping SweepDeg degrees clockwise. A full
	// 360° sweep is an all-round ring.
	RadiusMM float64
	StartDeg float64
	SweepDeg float64
}

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
	SimpleLine    *SimpleLine // set when Op==OpLine/OpAugmentedLine and Reference=="_simple_"

	// Augmented is set when Op==OpAugmentedLine: the constructed ray/arc this draw
	// strokes (with the SimpleLine style). The baker tessellates it per-zoom.
	Augmented *AugmentedGeom

	// Text style (set on OpText): the resolved text is in Reference.
	FontColor   string  // colour token (e.g. CHBLK)
	FontSizePx  float64 // 0 ⇒ default
	TextAlignH  string  // "Center" | "Left" | "Right"
	TextAlignV  string  // "Top" | "Bottom" | "Center"
	TextVOffset float64

	// Date dependency (S-101 §ProcessFixedAndPeriodicDates): a Date:/TimeValid:
	// modifier pair the rule emits for a feature with a fixed (DATSTA/DATEND) or
	// periodic (PERSTA/PEREND) date range. DateStart/DateEnd are S-57 date strings
	// — full "YYYYMMDD" for a fixed range, or an S-57 partial "--MMDD" recurring
	// each year for a periodic one (either may be empty for a semi-open interval).
	// TimeValid is the interval kind ("closedInterval" | "geSemiInterval" |
	// "leSemiInterval"). Empty when the feature carries no date dependency. Carried
	// so a date-aware consumer can show/hide the feature against the current date.
	DateStart string
	DateEnd   string
	TimeValid string

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
		dateStart    string
		dateEnd      string
		timeValid    string
		curAug       *AugmentedGeom // current constructed figure (ray/arc), if any
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
		if op == OpAugmentedLine {
			c.SimpleLine, c.Augmented = simple, curAug
		}
		c.DateStart, c.DateEnd, c.TimeValid = dateStart, dateEnd, timeValid
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
			// End of an augmented-geometry run: drop the explicit anchor/offset and
			// the constructed figure so later draws re-attach to the feature geometry.
			hasAnchor, anchor, offset = false, [2]float64{}, [2]float64{}
			curAug = nil
		// --- geometry construction (screen-space figures the rule builds) ---
		case "AugmentedRay":
			// "AugmentedRay:<bearingCRS>,<bearing>,<lenCRS>,<len>" — a leg from the
			// anchor. The rule emits the bearing already from-seaward-reversed. The
			// LENGTH's CRS (arg 2) decides its unit: LocalCRS ⇒ display mm (the 25 mm
			// short sector leg); GeographicCRS ⇒ ground metres (a sectorLineLength /
			// full-VALNMR leg — a fixed ground distance, NOT mm). Conflating the two
			// rendered geographic legs at metres-as-mm, ~10× too long ("shooting out").
			ar := &AugmentedGeom{Kind: AugRay, BearingDeg: atof(arg(in, 1))}
			if arg(in, 2) == "GeographicCRS" {
				ar.LengthGroundM = atof(arg(in, 3))
			} else {
				ar.LengthMM = atof(arg(in, 3))
			}
			curAug = ar
		case "ArcByRadius":
			// "ArcByRadius:<cx>,<cy>,<radiusMM>,<startDeg>,<sweepDeg>" — an arc/ring
			// centred on the anchor (the cx,cy offset is 0 for sector figures).
			curAug = &AugmentedGeom{Kind: AugArc, RadiusMM: atof(arg(in, 2)), StartDeg: atof(arg(in, 3)), SweepDeg: atof(arg(in, 4))}
		case "AugmentedPath":
			// Declares the CRS sequence stitching the preceding ray/arc into one path;
			// each constructed element is already carried by curAug, so this is a no-op.
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
		// --- date-dependency modifiers (annotate subsequent draws) ---
		case "Date":
			// "Date:<start>,<end>" (either side may be empty for a semi-open
			// interval, e.g. "Date:,--1201"). A bare "Date:<start>" sets the start.
			dateStart, dateEnd = arg(in, 0), arg(in, 1)
		case "TimeValid":
			timeValid = arg(in, 0)
		// modifiers we intentionally ignore when emitting primitives
		case "ScaleMinimum", "ScaleMaximum", "Time", "DateTime",
			"AlertReference", "Warning", "Error", "Hover", "SpatialReference",
			// area-placement / scale-factor modifiers: meaningful only for area-fill
			// placement, not for the point / fill / line / text / augmented draws we
			// emit. They ride along on the AddDateDependentSymbol geometry-reset
			// preamble, so ignore them rather than report them as gaps.
			"AreaPlacement", "AreaCRS", "ScaleFactor":
			// no-op for primitive emission

		// --- draws (consume state) ---
		case "PointInstruction":
			emit(OpPoint, arg(in, 0), in.Raw)
		case "LineInstruction", "LineInstructionUnsuppressed":
			// When a figure (ray/arc) is current, the line strokes THAT (screen-space
			// sector geometry); otherwise it strokes the feature's own geometry.
			if curAug != nil {
				emit(OpAugmentedLine, arg(in, 0), in.Raw)
			} else {
				emit(OpLine, arg(in, 0), in.Raw)
			}
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

		// recognized-but-not-yet-emitted draws → gap
		case "CoverageFill":
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
