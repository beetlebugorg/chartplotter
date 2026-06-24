package assets

import (
	"image"
	"testing"

	"github.com/beetlebugorg/chartplotter/pkg/s100/catalog"
	"github.com/beetlebugorg/chartplotter/pkg/s100/symbols"
)

// TestSeamlessTileStagger: a straight lattice (v2.x==0) yields a one-row tile
// (height = v2.y), while a staggered lattice (v2.x != 0) yields a TWO-row
// half-drop tile (height = 2·v2.y) — matching the S-52 STG atlas. Regression:
// the staggered fill was emitted single-row, so it repeated at half the period
// and the data-quality pattern clipped.
func TestSeamlessTileStagger(t *testing.T) {
	// A tiny opaque 4×4 symbol, pivot at its centre.
	img := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	for i := range img.Pix {
		img.Pix[i] = 255
	}
	sym := &symbols.Rendered{Image: img, PivotX: 2, PivotY: 2}

	rowH := int(roundf(10 * s101PxPerMM))

	straight, ok := seamlessTile(sym, catalog.Vec{X: 10, Y: 10}, catalog.Vec{X: 0, Y: 10})
	if !ok {
		t.Fatal("straight tile not built")
	}
	if got := straight.Rect.Dy(); got != rowH {
		t.Errorf("straight tile height = %d, want one row (%d)", got, rowH)
	}

	staggered, ok := seamlessTile(sym, catalog.Vec{X: 10, Y: 10}, catalog.Vec{X: 5, Y: 10})
	if !ok {
		t.Fatal("staggered tile not built")
	}
	if got := staggered.Rect.Dy(); got != rowH*2 {
		t.Errorf("staggered tile height = %d, want two rows (%d)", got, rowH*2)
	}
	if straight.Rect.Dx() != staggered.Rect.Dx() {
		t.Errorf("stagger changed width: %d vs %d", straight.Rect.Dx(), staggered.Rect.Dx())
	}
}

func roundf(f float64) int {
	if f < 0 {
		return int(f - 0.5)
	}
	return int(f + 0.5)
}
