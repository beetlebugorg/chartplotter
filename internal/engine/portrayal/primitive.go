// Package portrayal turns one S-57 feature into a stream of viewport-independent
// lat/lon Primitives by running the S-101 portrayal rules and emitting a
// primitive for each drawing instruction, stopping short of projection and colour
// resolution. Colour stays as *token* strings; the tile engine
// projects/clips/encodes the Primitives into MVT and the browser resolves
// Day/Dusk/Night from colortables.json.
//
// The tile engine (internal/engine/bake) projects the Primitive IR directly.
package portrayal

import "github.com/beetlebugorg/chartplotter/pkg/geo"

// DefaultPxPerSymbolUnit is screen px per 0.01-mm PresLib symbol unit at 100%
// zoom — the nominal S-52 feature scale shared by the symbol/linestyle renderers
// and the tile engine's LC/AP/sector sizing. It MUST use the same reference pixel
// pitch the rest of the app measures the screen with (web util.mjs
// DEFAULT_PX_PITCH_MM = 0.26458 mm, the 1/96-inch CSS reference pixel) so a symbol
// renders at its encoded physical size: the S-52 size-check symbol SY(CHKSYM01),
// a 5 mm box, then measures 5 mm (500 units × this = 18.9 px × 0.26458 mm = 5 mm).
// (Previously 0.35278 — the 1/72-inch point — which rendered every symbol ~25% too
// small against the app's 0.26458 mm pixel.)
const DefaultPxPerSymbolUnit float32 = 0.01 / 0.26458

// Dash is a simple line-stroke dash style (LS instruction).
type Dash uint8

const (
	DashSolid Dash = iota
	DashDashed
	DashDotted
)

// HAlign / VAlign are S-52 text anchor alignments (TX/TE instruction).
type HAlign uint8

const (
	HAlignLeft HAlign = iota
	HAlignCenter
	HAlignRight
)

type VAlign uint8

const (
	VAlignTop VAlign = iota
	VAlignMiddle
	VAlignBaseline
	VAlignBottom
)

// Primitive is one viewport-independent draw step. The concrete types below are
// the closed set of variants (the Primitive tagged union). Consumers
// (the bake step) type-switch over them.
type Primitive interface {
	isPrimitive()
}

// FillPolygon is a solid area fill (AC instruction). Rings is the outer ring
// then optional holes; fill rules are even-odd / non-zero, so winding of holes
// is not relied upon.
type FillPolygon struct {
	Rings      [][]geo.LatLon
	ColorToken string
}

// StrokeLine is a simple line stroke (LS instruction).
type StrokeLine struct {
	Points     []geo.LatLon
	ColorToken string
	WidthPx    float32
	Dash       Dash
}

// SymbolHalo is an optional outline drawn under a stamped symbol (used for
// soundings against busy fills).
type SymbolHalo struct {
	ColorToken   string
	ExtraWidthPx float32
}

// SymbolCall stamps a PresLib point symbol (SY instruction). Sounding/danger
// depths travel with the symbol so the client can do the SNDFRM04 bold/faint
// split and the OBSTRN/WRECKS shallow/deep swap against the live safety contour
// without a re-bake. SoundingDepthM / DangerDepthM are NaN for ordinary symbols.
type SymbolCall struct {
	Anchor geo.LatLon
	// CentreOnArea marks the PRIMARY centred-area symbol of an area feature, so the
	// client centres the glyph on the representative point (S-52 PresLib §8.5.1: the
	// primary centred symbol sits at the pivot "so it is evident which area the symbol
	// applies to"). The "…RES" symbols carry a built-in corner-pivot offset meant to
	// FAN OUT additional symbols ("an offset entry-restricted symbol with a subscript
	// !"); applying it to the primary throws its glyph ~100px off its area. Set on the
	// first centred symbol only — additional symbols keep the offset and fan out.
	CentreOnArea bool
	SymbolName   string
	RotationDeg  float32
	// RotationTrueNorth marks RotationDeg as referenced to TRUE NORTH (S-52 PresLib
	// Part I §9.2 ROT case 3 — rotation taken from an S-57 attribute like ORIENT), so
	// the mark must rotate WITH the chart as it turns. False means screen-referenced
	// (ROT cases 1 & 2 — no rotation, or a literal angle like a light flare): the mark
	// stays upright to the screen regardless of chart orientation.
	RotationTrueNorth bool
	Scale             float32
	OffsetXUnits      float32
	OffsetYUnits      float32
	Halo              *SymbolHalo
	SoundingDepthM    float32
	DangerDepthM      float32
	DeepSymbolName    string
}

// PatternFill fills an area with a repeating PresLib pattern (AP instruction).
type PatternFill struct {
	Rings       [][]geo.LatLon
	PatternName string
}

// LinePattern draws a polyline with a complex linestyle (LC instruction).
// Per-segment direction and repeat-step are computed at projection time, so the
// cached primitive is purely viewport-independent.
type LinePattern struct {
	Points        []geo.LatLon
	LinestyleName string
	// ColorToken is the linestyle's primary pen colour token (first PD pen,
	// LCRF-resolved). Baked onto the complex_lines feature so the client colours
	// the dash run; "" when the linestyle has no drawn pen.
	ColorToken string
}

// DrawText is a text label (TX/TE instruction). OffsetXPx/OffsetYPx are the
// S-52 §8.3.3.2 XOFFS/YOFFS pivot offset pre-multiplied to pixels (+x right,
// +y down), applied after projection.
type DrawText struct {
	Anchor     geo.LatLon
	Text       string
	FontSizePx float32
	ColorToken string
	Halo       *TextHalo
	HAlign     HAlign
	VAlign     VAlign
	OffsetXPx  float32
	OffsetYPx  float32
	// Group is the S-52 text grouping (the DISPLAY parameter — last argument of
	// TX()/TE()), per PresLib 4.0 §14.4: 11=important (clearances, bearings),
	// 21/26/29=names, 23=light description, 25=seabed, 27=mag variation, etc.
	// Baked as the `tgrp` tag so the client can toggle text groups (§14.5) live
	// without re-baking.
	Group int
}

// TextHalo is the optional CHWHT outline under chart text (S-52 §10.3.6).
type TextHalo struct {
	ColorToken string
	WidthPx    float32
}

// AugmentedFigure is one stroked element of a screen-space figure the S-101 rule
// CONSTRUCTED via AugmentedRay / ArcByRadius (a light-sector leg or arc/ring) —
// driven by the catalogue's own bearings, radii, colours and widths rather than a
// Go re-derivation from S-57 attributes. One primitive = one stroked element; a
// sectored light emits several (two dashed legs, then a black-backed coloured
// arc). The mm sizes are screen-fixed, so the baker tessellates per-zoom into
// `sector_lines`; it cannot bake as static geographic geometry.
type AugmentedFigure struct {
	Anchor geo.LatLon
	Ray    bool // true: a straight leg (Bearing/Length); false: an arc/ring
	// Ray params (true-north bearing, already from-seaward-reversed by the rule).
	BearingDeg float64
	LengthMM   float64
	// LengthGroundM is a ray leg's length when given as a GROUND distance (metres,
	// from a GeographicCRS sectorLineLength / full-VALNMR leg) rather than display
	// mm — drawn zoom-dependently. 0 ⇒ use LengthMM (display mm). See tessellateFigure.
	LengthGroundM float64
	// Arc params (centred on Anchor); a full 360° sweep is an all-round ring.
	RadiusMM float64
	StartDeg float64
	SweepDeg float64
	// Stroke style, from the rule's LineStyle:_simple_.
	ColorToken string
	WidthMM    float64
	Dash       Dash
	// FullLengthNM is the LIGHTS nominal range (VALNMR); when set on a ray, the
	// baker also emits the "full light lines" leg variant extended to that range
	// (S-52 LIGHTS06 note 1), tagged for the client's live toggle. 0 = no variant.
	FullLengthNM float64
}

func (FillPolygon) isPrimitive()     {}
func (StrokeLine) isPrimitive()      {}
func (SymbolCall) isPrimitive()      {}
func (PatternFill) isPrimitive()     {}
func (LinePattern) isPrimitive()     {}
func (DrawText) isPrimitive()        {}
func (AugmentedFigure) isPrimitive() {}
