// Package portrayal turns one S-57 feature into a stream of viewport-independent
// lat/lon Primitives — the S-52 "expand" step (lookup + CSP + instruction walk),
// stopping short of projection and colour resolution. Colour stays as S-52
// *token* strings; the tile engine projects/clips/encodes the Primitives into
// MVT and the browser resolves Day/Dusk/Night from colortables.json.
//
// The legacy page-space DrawCommand projection path is not used — the tile
// engine (internal/engine/bake) projects the Primitive IR directly.
package portrayal

import "github.com/beetlebugorg/chartplotter/pkg/geo"

// DefaultPxPerSymbolUnit is screen px per 0.01-mm PresLib symbol unit at 100%
// zoom — the nominal S-52 feature scale shared by the symbol/linestyle renderers
// and the tile engine's LC/AP/sector sizing. 0.01 / 0.35278 mm-per-pt renders
// every glyph at its encoded physical size.
const DefaultPxPerSymbolUnit float32 = 0.01 / 0.35278

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
	Anchor         geo.LatLon
	SymbolName     string
	RotationDeg    float32
	Scale          float32
	OffsetXUnits   float32
	OffsetYUnits   float32
	Halo           *SymbolHalo
	SoundingDepthM float32
	DangerDepthM   float32
	DeepSymbolName string
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
}

// TextHalo is the optional CHWHT outline under chart text (S-52 §10.3.6).
type TextHalo struct {
	ColorToken string
	WidthPx    float32
}

// SectorLight is LIGHTS06 sector geometry (legs / arc / ring). Only the lat/lon
// anchor plus the S-52 sector parameters are cached; the screen-space
// tessellation happens at projection time because radii are display millimetres.
type SectorLight struct {
	Anchor geo.LatLon
	Sector SectorParams
}

// SectorParams carries the S-52 LIGHTS06 sector parameters. Populated from the
// s52 SectorInstruction the CS procedure emits.
type SectorParams struct {
	StartAngleDeg float64 // SECTR1, 0=North, clockwise
	EndAngleDeg   float64 // SECTR2, 0=North, clockwise
	RadiusNM      float64 // VALNMR nominal range, nautical miles
	ColorToken    string  // LITRD/LITGN/LITYW/...
	Transparency  int     // 0=opaque..3=75%
	ShowLegs      bool
}

func (FillPolygon) isPrimitive() {}
func (StrokeLine) isPrimitive()  {}
func (SymbolCall) isPrimitive()  {}
func (PatternFill) isPrimitive() {}
func (LinePattern) isPrimitive() {}
func (DrawText) isPrimitive()    {}
func (SectorLight) isPrimitive() {}
