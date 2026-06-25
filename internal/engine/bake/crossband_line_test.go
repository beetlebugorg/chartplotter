package bake

import (
	"testing"

	"github.com/beetlebugorg/chartplotter/internal/engine/mvt"
	"github.com/beetlebugorg/chartplotter/internal/engine/tile"
	"github.com/beetlebugorg/chartplotter/pkg/s57"
)

// TestCrossBandLineNoDoubleDraw guards the best-available LINE suppression on the
// real US4MD81M (approach) + US5MD1MC (harbor) overlapping pair. On the client
// both per-band sources render at the overlap zoom (the fine bands carry no
// maxzoom cap), so a coarse line kept in a tile a finer cell also draws into
// DOUBLE-DRAWS — the RESARE/CTNARE boundary, depth contour or coastline appears
// twice (a doubled stroke). Unlike opaque area FILLS, a line does not occlude, so
// the whole-tile (5-corner) seam relaxation must NOT keep it: a coarse line is
// suppressed by the tile CENTRE. The invariant: no tile that carries a line in
// BOTH bands may have its centre covered by a strictly-finer cell (that would be
// an interior double-draw, not an unavoidable ≤1-tile seam sliver).
func TestCrossBandLineNoDoubleDraw(t *testing.T) {
	b := New()
	b.SetPortrayer(testS101Portrayer(t))
	for _, cell := range []string{goldenCell, "../../../testdata/US5MD1MC.000"} {
		chart, err := s57.Parse(cell)
		if err != nil {
			t.Fatalf("parse %s: %v", cell, err)
		}
		b.AddCell(chart)
	}

	const ext = mvt.ExtentDefault
	const buf = 64.0
	const z = 14 // a zoom where the client shows both approach and harbor

	// Coarse (approach) cell compilation scale, for the finer-cover comparison.
	var approachCscl uint32
	for i := range b.covMeta {
		if cm := &b.covMeta[i]; cm.bandMax == BandApproach.ZoomRange().Max {
			approachCscl = cm.cscl
		}
	}
	if approachCscl == 0 {
		t.Fatal("approach cell compilation scale not found in coverage metadata")
	}

	lineLayers := map[string]bool{"lines": true, "complex_lines": true}
	emitLineTiles := func(bandMax uint32) map[tile.TileCoord]bool {
		b.BuildEmitIndexBand(ext, buf, bandMax)
		defer b.ClearEmitIndex()
		out := map[tile.TileCoord]bool{}
		var ts TileScratch
		for _, c := range b.TileCoordsBand(ext, bandMax, bandMax) {
			if c.Z != z {
				continue
			}
			data := b.EmitTileBandInto(c, ext, buf, &ts, bandMax)
			if data == nil {
				continue
			}
			for name, l := range decodeLayers(data) {
				if lineLayers[name] && len(l.feats) > 0 {
					out[c] = true
					break
				}
			}
		}
		return out
	}

	app := emitLineTiles(BandApproach.ZoomRange().Max)
	har := emitLineTiles(BandHarbor.ZoomRange().Max)

	n := float64(int64(1) << uint(z))
	var doubled, interior int
	for c := range app {
		if !har[c] {
			continue
		}
		doubled++
		ctrLon := (float64(c.X)+0.5)/n*360 - 180
		ctrLat := unnormY((float64(c.Y) + 0.5) / n)
		// A strictly-finer cell covers this tile's centre, yet a coarse line was
		// kept here AND the finer band also drew one: an interior double-draw.
		if s := b.coverageScaleAt(ctrLat, ctrLon, z); s != 0 && s < approachCscl {
			interior++
			t.Errorf("interior line double-draw at %v: tile centre covered by finer cell (cscl %d < approach %d)", c, s, approachCscl)
		}
	}
	t.Logf("z%d: %d tiles carry a line in both bands; %d of them are interior (must be 0), the rest are ≤1-tile seam slivers", z, doubled, interior)
}
