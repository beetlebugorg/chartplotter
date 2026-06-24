package portrayal

import (
	"strconv"
	"strings"

	"github.com/beetlebugorg/chartplotter/pkg/geo"
	"github.com/beetlebugorg/chartplotter/pkg/s57"
)

// This file produces the SectorLight primitive for S-57 sectored / directional
// lights. The S-101 LightSectored rule expresses sector legs and arcs as
// AugmentedRay / ArcByRadius geometry-construction instructions whose lengths are
// fixed display millimetres (LocalCRS) — a fixed SCREEN size that can't be baked
// into geographic tile geometry. So instead of lowering those instructions, the
// sector geometry is carried as a SectorLight primitive (anchor + S-52 sector
// parameters) and tessellated per-zoom at bake time into the screen-space
// `sector_lines` layer (bake.expandSector), exactly as the former S-52 LIGHTS06
// path did. The rule's text descriptions still lower normally (OpText).

// sectorLightPrims returns the SectorLight primitive(s) for a LIGHTS feature, or
// nil if it carries no sector/directional figure (a plain light is portrayed by
// its flare symbol, which lowers from the rule's PointInstruction). One S-57
// LIGHTS feature is one sector; multiple sectors at a position are separate
// co-located features (the baker dedupes identical geometry).
func sectorLightPrims(f *s57.Feature, anchor geo.LatLon) []Primitive {
	if f.ObjectClass() != "LIGHTS" {
		return nil
	}
	a := f.Attributes()
	vnr, _ := floatAttr(a, "VALNMR")
	colour := sectorColorToken(stringAttr(a, "COLOUR"))

	s1, ok1 := floatAttr(a, "SECTR1")
	s2, ok2 := floatAttr(a, "SECTR2")
	if ok1 && ok2 {
		// Sectored light: two limits → legs + coloured arc. expandSector reverses
		// the from-seaward bearings itself, so pass SECTR1/SECTR2 unflipped.
		return []Primitive{SectorLight{
			Anchor: anchor,
			Sector: SectorParams{
				StartAngleDeg: s1, EndAngleDeg: s2, RadiusNM: vnr,
				ColorToken: colour, ShowLegs: true,
			},
		}}
	}

	// Directional light of long range (≥10 NM) with no sector limits: the rule
	// draws a full coloured ring (ArcByRadius 0–360). Shorter-range directional
	// lights fall back to the flare symbol, which lowers from the rule's
	// PointInstruction, so they need no SectorLight here.
	if hasListVal(stringAttr(a, "CATLIT"), 1) && vnr >= 10 {
		return []Primitive{SectorLight{
			Anchor: anchor,
			Sector: SectorParams{
				StartAngleDeg: 0, EndAngleDeg: 0, // sweep 0 → ring (expandSector)
				RadiusNM: vnr, ColorToken: colour, ShowLegs: false,
			},
		}}
	}
	return nil
}

// sectorColorToken maps an S-57 COLOUR list to the sector arc's S-52 colour
// token, mirroring the LightSectored rule's colour selection: red→LITRD,
// green→LITGN, white/yellow/orange→LITYW, anything else→the magenta default.
func sectorColorToken(colour string) string {
	c1, c2 := 0, 0
	parts := strings.Split(colour, ",")
	if len(parts) > 0 {
		c1, _ = strconv.Atoi(strings.TrimSpace(parts[0]))
	}
	if len(parts) > 1 {
		c2, _ = strconv.Atoi(strings.TrimSpace(parts[1]))
	}
	switch {
	case c1 == 3 || (c1 == 1 && c2 == 3): // red, or white & red
		return "LITRD"
	case c1 == 4 || (c1 == 1 && c2 == 4): // green, or white & green
		return "LITGN"
	case c1 == 1 || c1 == 6 || c1 == 11: // white, yellow, orange
		return "LITYW"
	default:
		return "CHMGD"
	}
}

// hasListVal reports whether the S-57 comma-separated list value contains want.
func hasListVal(csv string, want int) bool {
	for _, p := range strings.Split(csv, ",") {
		if n, err := strconv.Atoi(strings.TrimSpace(p)); err == nil && n == want {
			return true
		}
	}
	return false
}

// stringAttr returns an attribute's value as a string (S-57 encodes list values
// as a comma-separated string), or "" when absent.
func stringAttr(attrs map[string]interface{}, key string) string {
	if v, ok := attrs[key]; ok {
		if s, ok := encodeAttr(v); ok {
			return s
		}
	}
	return ""
}
