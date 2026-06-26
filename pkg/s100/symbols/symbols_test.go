package symbols

import "testing"

// TestRenderEvenOddHole guards the fill-rule="evenodd" handling. Every S-101
// symbol is authored with even-odd winding; the ISODGR01/DANGER0x danger glyphs
// carve their inner shape out of the outer body with a single compound path
// whose inner subpath is a hole. oksvg ignores fill-rule and fills nonzero,
// which would fill that hole solid (the "ISODGR01 with no star in it" bug).
func TestRenderEvenOddHole(t *testing.T) {
	// Outer 8x8 square with a concentric 4x4 inner subpath, both wound the same
	// way. Under nonzero winding the inner square fills solid; under even-odd it
	// is a hole. fill-rule="evenodd" sits on the root <svg> exactly as the
	// catalogue authors it.
	svg := []byte(`<?xml version="1.0"?>
<svg xmlns="http://www.w3.org/2000/svg" viewBox="-5 -5 10 10" fill-rule="evenodd">
  <path class="fX" d="M -4,-4 L 4,-4 L 4,4 L -4,4 L -4,-4 Z M -2,-2 L 2,-2 L 2,2 L -2,2 L -2,-2 Z"/>
</svg>`)
	css := map[string]string{"fX": "fill:#C045D1"}

	const pxPerMM = 10
	r, err := Render(svg, css, pxPerMM)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	// Pivot is the SVG origin (0,0); the hole is centred there.
	cx, cy := int(r.PivotX), int(r.PivotY)
	if _, _, _, a := r.Image.At(cx, cy).RGBA(); a != 0 {
		t.Errorf("centre pixel (%d,%d) alpha=%d, want 0 (even-odd hole filled solid — fill-rule ignored)", cx, cy, a>>8)
	}

	// A pixel in the ring between the two squares must be painted.
	rx, ry := int(r.PivotX+3*pxPerMM), cy // svg (3,0)
	if _, _, _, a := r.Image.At(rx, ry).RGBA(); a == 0 {
		t.Errorf("ring pixel (%d,%d) is transparent, want painted", rx, ry)
	}
}
