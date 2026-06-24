package bake

import (
	"testing"

	"github.com/beetlebugorg/chartplotter/internal/engine/mvt"
	"github.com/beetlebugorg/chartplotter/pkg/s57"
)

func featHasKey(l *decLayer, tags []uint32, key string) bool {
	for i := 0; i+1 < len(tags); i += 2 {
		if int(tags[i]) < len(l.keys) && l.keys[tags[i]] == key {
			return true
		}
	}
	return false
}

// rot_north tags a point symbol whose rotation is referenced to TRUE NORTH (an
// S-57 attribute like ORIENT, or a complex-line edge tangent), so the client
// routes it to the map-aligned layer that turns with the chart; its absence means
// the symbol is screen-up (no rotation, or a literal-angle flare) — S-52 6.1.1
// §3.1.6 / PresLib §9.2 ROT. The split must be SELECTIVE: an approach cell carries
// both kinds, so the baked flag must appear on some point symbols and not others.
func TestRotNorthSelective(t *testing.T) {
	chart, err := s57.Parse(goldenCell)
	if err != nil {
		t.Fatal(err)
	}
	b := New()
	b.SetPortrayer(testS101Portrayer(t))
	b.AddCell(chart)

	var total, withNorth int
	for _, c := range b.TileCoords(mvt.ExtentDefault) {
		data := b.EmitTile(c, mvt.ExtentDefault, 64)
		if data == nil {
			continue
		}
		ps := decodeLayers(data)["point_symbols"]
		if ps == nil {
			continue
		}
		for _, f := range ps.feats {
			total++
			if featHasKey(ps, f, "rot_north") {
				withNorth++
			}
		}
	}
	t.Logf("point_symbols feats=%d  true-north=%d  screen-up=%d", total, withNorth, total-withNorth)
	if total == 0 {
		t.Fatal("no point_symbols features baked from golden cell")
	}
	if withNorth == 0 {
		t.Error("no point symbol got rot_north — ORIENT/tangent symbols are not flagged true-north")
	}
	if withNorth == total {
		t.Error("every point symbol got rot_north — plain symbols must stay screen-up (rot_north absent)")
	}
}
