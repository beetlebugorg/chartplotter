package bake

import (
	"os"
	"strings"
	"testing"

	"github.com/beetlebugorg/chartplotter/internal/engine/mvt"
	"github.com/beetlebugorg/chartplotter/pkg/s57"
)

// TestLightTagFromCatalogue bakes a real cell and confirms the baked `light`
// inspector tag carries the catalogue (LITDSN02) string — no spurious single
// group "(1)", and the aero-light category prefix present — proving the Go
// reimplementation is gone and the value comes from Lua. Run with CELL set.
func TestLightTagFromCatalogue(t *testing.T) {
	cell := os.Getenv("CELL")
	if cell == "" {
		cell = goldenCell // committed harbour cell, rich in lights
	}
	chart, err := s57.Parse(cell)
	if err != nil {
		t.Fatal(err)
	}
	b := New()
	b.SetPortrayer(testS101Portrayer(t))
	b.AddCell(chart)

	lights := map[string]bool{}
	for _, c := range b.TileCoords(mvt.ExtentDefault) {
		data := b.EmitTile(c, mvt.ExtentDefault, 64)
		if data == nil {
			continue
		}
		for _, v := range lightValues(data) {
			lights[v] = true
		}
	}
	if len(lights) == 0 {
		t.Fatal("no baked light tags found")
	}
	var aero bool
	for v := range lights {
		if strings.Contains(v, "(1)") {
			t.Errorf("baked light tag still has spurious single group: %q", v)
		}
		if strings.HasPrefix(v, "Aero ") {
			aero = true
		}
	}
	if !aero {
		t.Error("expected at least one Aero-prefixed light (catalogue category prefix)")
	}
	t.Logf("%d distinct baked light tags, e.g.:", len(lights))
	n := 0
	for v := range lights {
		t.Logf("   %q", v)
		if n++; n >= 12 {
			break
		}
	}
}

// lightValues extracts every `light` string-tag value from an MVT tile.
func lightValues(data []byte) []string {
	var out []string
	r := &rdr{d: data}
	for {
		f, _, b, _, ok := r.next()
		if !ok {
			break
		}
		if f != 3 {
			continue
		}
		var keys []string
		var vals []string
		var valIsStr []bool
		var feats [][]uint32
		lr := &rdr{d: b}
		for {
			lf, _, lb, _, lok := lr.next()
			if !lok {
				break
			}
			switch lf {
			case 2:
				var tags []uint32
				fr := &rdr{d: lb}
				for {
					ff, _, fb, _, fok := fr.next()
					if !fok {
						break
					}
					if ff == 2 {
						tr := &rdr{d: fb}
						for !tr.end() {
							tags = append(tags, uint32(tr.uv()))
						}
					}
				}
				feats = append(feats, tags)
			case 3:
				keys = append(keys, string(lb))
			case 4:
				s, isStr := "", false
				vr := &rdr{d: lb}
				for {
					vf, _, vb, _, vok := vr.next()
					if !vok {
						break
					}
					if vf == 1 {
						isStr, s = true, string(vb)
					}
				}
				vals = append(vals, s)
				valIsStr = append(valIsStr, isStr)
			}
		}
		for _, tags := range feats {
			for i := 0; i+1 < len(tags); i += 2 {
				ki, vi := int(tags[i]), int(tags[i+1])
				if ki < len(keys) && keys[ki] == "light" && vi < len(vals) && valIsStr[vi] {
					out = append(out, vals[vi])
				}
			}
		}
	}
	return out
}
