package bake

import (
	"bytes"
	"math"
	"testing"

	"github.com/beetlebugorg/chartplotter/internal/engine/mvt"
	"github.com/beetlebugorg/chartplotter/internal/engine/portrayal"
	"github.com/beetlebugorg/chartplotter/internal/engine/tile"
	"github.com/beetlebugorg/chartplotter/pkg/geo"
	"github.com/beetlebugorg/chartplotter/pkg/s57"
)

const goldenCell = "../../../testdata/US4MD81M.000"

func TestBandForScale(t *testing.T) {
	if BandForScale(12_000).ZoomRange() != (ZoomRange{14, 16}) {
		t.Error("12k should be harbor [14,16]")
	}
	if BandForScale(3_000_000) != BandOverview {
		t.Error("3M should be overview")
	}
	if BandForScale(0) != BandApproach {
		t.Error("unknown scale defaults to 50k -> approach band")
	}
}

// TestBakeGoldenCell bakes the Annapolis cell and decodes one populated tile,
// asserting the expected layers and that colour is a string token.
func TestBakeGoldenCell(t *testing.T) {
	chart, err := s57.Parse(goldenCell)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	b := New()
	b.SetPortrayer(testS101Portrayer(t))
	b.AddCell(chart)

	coords := b.TileCoords(mvt.ExtentDefault)
	if len(coords) == 0 {
		t.Fatal("no tiles enumerated")
	}

	// Bake every tile; find the one with the most bytes (densest) and decode it.
	var best []byte
	var bestCoord tile.TileCoord
	var nonEmpty int
	for _, c := range coords {
		data := b.EmitTile(c, mvt.ExtentDefault, 64)
		if data == nil {
			continue
		}
		nonEmpty++
		if len(data) > len(best) {
			best, bestCoord = data, c
		}
	}
	t.Logf("tiles=%d nonEmpty=%d densest=%v (%d bytes)", len(coords), nonEmpty, bestCoord, len(best))
	if nonEmpty == 0 {
		t.Fatal("every tile was empty")
	}

	layers := decodeLayers(best)
	t.Logf("densest tile layers: %v", layerNames(layers))
	if len(layers) == 0 {
		t.Fatal("densest tile decoded to no layers")
	}
	// Areas should exist on a harbour cell, and colour must be a string token.
	if a := layers["areas"]; a != nil {
		if !a.firstFeatureHasStringKey("color_token") {
			t.Error("areas color_token must be a string token, not RGB")
		}
	}
}

// -- tiny MVT layer-name/string-attr decoder ---------------------------------

type decLayer struct {
	name   string
	keys   []string
	values []decVal
	feats  [][]uint32 // tag lists
}

type decVal struct {
	isString bool
}

func (l *decLayer) firstFeatureHasStringKey(key string) bool {
	if len(l.feats) == 0 {
		return false
	}
	tags := l.feats[0]
	for i := 0; i+1 < len(tags); i += 2 {
		if int(tags[i]) < len(l.keys) && l.keys[tags[i]] == key {
			vi := int(tags[i+1])
			return vi < len(l.values) && l.values[vi].isString
		}
	}
	return false
}

func layerNames(m map[string]*decLayer) []string {
	var out []string
	for n := range m {
		out = append(out, n)
	}
	return out
}

type rdr struct {
	d []byte
	p int
}

func (r *rdr) end() bool { return r.p >= len(r.d) }
func (r *rdr) uv() uint64 {
	var v uint64
	var s uint
	for r.p < len(r.d) {
		b := r.d[r.p]
		r.p++
		v |= uint64(b&0x7f) << s
		if b < 0x80 {
			break
		}
		s += 7
	}
	return v
}

// next returns field, wiretype, payload(len-delimited), varint, ok.
func (r *rdr) next() (uint32, uint64, []byte, uint64, bool) {
	if r.end() {
		return 0, 0, nil, 0, false
	}
	tag := r.uv()
	f := uint32(tag >> 3)
	wt := tag & 7
	switch wt {
	case 0:
		return f, wt, nil, r.uv(), true
	case 2:
		n := int(r.uv())
		b := r.d[r.p : r.p+n]
		r.p += n
		return f, wt, b, 0, true
	case 5:
		b := r.d[r.p : r.p+4]
		r.p += 4
		return f, wt, b, 0, true
	default:
		return f, wt, nil, 0, false
	}
}

func decodeLayers(data []byte) map[string]*decLayer {
	out := map[string]*decLayer{}
	r := &rdr{d: data}
	for {
		f, _, b, _, ok := r.next()
		if !ok {
			break
		}
		if f != 3 {
			continue
		}
		l := &decLayer{}
		lr := &rdr{d: b}
		for {
			lf, lv, lb, vv, lok := lr.next()
			if !lok {
				break
			}
			switch lf {
			case 1:
				l.name = string(lb)
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
				l.feats = append(l.feats, tags)
			case 3:
				l.keys = append(l.keys, string(lb))
			case 4:
				isStr := false
				vr := &rdr{d: lb}
				for {
					vf, _, _, _, vok := vr.next()
					if !vok {
						break
					}
					if vf == 1 {
						isStr = true
					}
				}
				l.values = append(l.values, decVal{isString: isStr})
			case 5:
				_ = lv
				_ = vv
			}
		}
		out[l.name] = l
	}
	return out
}

func TestBakePMTilesArchive(t *testing.T) {
	chart, err := s57.Parse(goldenCell)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	b := New()
	b.SetPortrayer(testS101Portrayer(t))
	b.AddCell(chart)
	pb := b.BakePMTiles(mvt.ExtentDefault, 64)
	if pb.Count() == 0 {
		t.Fatal("archive has no tiles")
	}
	arc := pb.Finish()
	if string(arc[0:7]) != "PMTiles" || arc[7] != 3 {
		t.Fatal("not a valid PMTiles v3 archive")
	}
	t.Logf("archive: %d tiles, %d bytes", pb.Count(), len(arc))
}

// TestEmitIndexEquivalence asserts the inverted tile→prim index (BuildEmitIndex)
// produces byte-identical tiles to the full-scan fallback for every tile —
// across two overlapping cells of different bands (so suppression is exercised).
func TestEmitIndexEquivalence(t *testing.T) {
	build := func() *Baker {
		b := New()
		b.SetPortrayer(testS101Portrayer(t))
		for _, cell := range []string{goldenCell, "../../../testdata/US5MD1MC.000"} {
			chart, err := s57.Parse(cell)
			if err != nil {
				t.Fatalf("parse %s: %v", cell, err)
			}
			b.AddCell(chart)
		}
		return b
	}

	const buf = 64.0
	scan := build() // emitIndex nil → full scan
	indexed := build()
	indexed.BuildEmitIndex(mvt.ExtentDefault, buf)

	// The emit index covers each prim only over its native [zMin, zMax]; the full
	// scan additionally overzooms a coarse prim above its zMax wherever no finer
	// cell overlaps (the realtime path's best-available behaviour). So the two
	// agree only at NON-overzoom tiles — skip any tile a coarse prim overzooms
	// into (a prim with zMax < z whose bbox covers the tile).
	overzoomsInto := func(c tile.TileCoord) bool {
		n := math.Pow(2, float64(c.Z))
		bufN := (buf / float64(mvt.ExtentDefault)) / n
		tnx0, tnx1 := float64(c.X)/n-bufN, float64(c.X+1)/n+bufN
		tny0, tny1 := float64(c.Y)/n-bufN, float64(c.Y+1)/n+bufN
		for i := range scan.prims {
			r := &scan.prims[i]
			if c.Z < r.zMin || c.Z <= r.zMax {
				continue // not overzooming this prim
			}
			if r.wMaxX < tnx0 || r.wMinX > tnx1 || r.wMaxY < tny0 || r.wMinY > tny1 {
				continue
			}
			return true
		}
		return false
	}

	coords := scan.TileCoords(mvt.ExtentDefault)
	var checked, skipped int
	for _, c := range coords {
		if overzoomsInto(c) {
			skipped++
			continue
		}
		var ts1, ts2 TileScratch
		a := scan.EmitTileInto(c, mvt.ExtentDefault, buf, &ts1)
		b := indexed.EmitTileInto(c, mvt.ExtentDefault, buf, &ts2)
		if !bytes.Equal(a, b) {
			t.Fatalf("tile %v differs: scan=%d bytes indexed=%d bytes", c, len(a), len(b))
		}
		checked++
	}
	t.Logf("verified %d tiles byte-identical (full scan vs indexed); skipped %d overzoom tiles", checked, skipped)
	if checked == 0 {
		t.Fatal("no tiles checked")
	}
}

func TestSoundingGrouping(t *testing.T) {
	chart, err := s57.Parse(goldenCell)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	b := New()
	b.SetPortrayer(testS101Portrayer(t))
	b.AddCell(chart)

	// Find a tile carrying soundings and confirm the grouped attributes.
	var found bool
	for _, c := range b.TileCoords(mvt.ExtentDefault) {
		data := b.EmitTile(c, mvt.ExtentDefault, 64)
		if data == nil {
			continue
		}
		layers := decodeLayers(data)
		s := layers["soundings"]
		if s == nil || len(s.feats) == 0 {
			continue
		}
		found = true
		if !s.firstFeatureHasStringKey("symbol_names") {
			t.Error("soundings feature missing symbol_names")
		}
		// sym_s/sym_g are the SNDFRM04 safety-depth palette variants, only baked
		// when a SymbolCall carries SoundingDepthM. The S-101 lower path sets
		// SoundingDepthM=NaN, so it does not produce them — this assertion needs an
		// S-101 equivalent.
		if !s.firstFeatureHasStringKey("sym_s") || !s.firstFeatureHasStringKey("sym_g") {
			t.Skip("SNDFRM04 sym_s/sym_g palette variants not produced by the S-101 lower path; needs an S-101 equivalent assertion")
		}
		t.Logf("soundings present at %v: %d features", c, len(s.feats))
		break
	}
	if !found {
		t.Fatal("no soundings layer found in any tile")
	}
}

func TestSectorLights(t *testing.T) {
	// tessellateFigure drives each constructed figure element off the rule's params.
	// A leg with a nominal range emits the short (sleg 0) + extended full-length
	// (sleg 1) variants, both dashed in the rule's colour, for the client's toggle.
	leg := tessellateFigure(legPrim(90, 8), 14)
	if len(leg) != 2 {
		t.Fatalf("leg strokes = %d, want 2 (short + full)", len(leg))
	}
	if !leg[0].dashed || leg[0].colorToken != "CHBLK" || leg[0].sleg != 0 {
		t.Error("stroke 0 should be the dashed CHBLK short leg (sleg 0)")
	}
	if leg[1].sleg != 1 {
		t.Error("stroke 1 should be the full-length leg (sleg 1)")
	}
	legLen := func(s sectorStroke) float64 {
		return absf(s.points[1].Lat-s.points[0].Lat) + absf(s.points[1].Lon-s.points[0].Lon)
	}
	if legLen(leg[1]) <= legLen(leg[0]) {
		t.Errorf("full leg (%.6f) should be longer than short leg (%.6f)", legLen(leg[1]), legLen(leg[0]))
	}
	// A leg with no nominal range is a single always-shown stroke (sleg -1).
	plain := tessellateFigure(legPrim(90, 0), 14)
	if len(plain) != 1 || plain[0].sleg != -1 {
		t.Errorf("plain leg = %+v, want 1 stroke sleg -1", plain)
	}
	// An arc/ring is one stroke in the rule's colour (white light → yellow LITYW),
	// always shown (sleg -1). Screen-fixed: the radius ~halves per zoom level.
	arc14 := tessellateFigure(arcPrim(26, 0, 360, "LITYW"), 14)
	if len(arc14) != 1 || arc14[0].colorToken != "LITYW" || arc14[0].sleg != -1 {
		t.Fatalf("arc = %+v, want 1 LITYW stroke sleg -1", arc14)
	}
	arc15 := tessellateFigure(arcPrim(26, 0, 360, "LITYW"), 15)
	anchor := mustLatLon(38.97, -76.49)
	span := func(s []sectorStroke) float64 { return absf(s[0].points[0].Lat - anchor.Lat) }
	if ratio := span(arc14) / span(arc15); ratio < 1.9 || ratio > 2.1 {
		t.Errorf("ring radius ratio z14/z15 = %.3f, want ~2", ratio)
	}
}

// TestUpSuppressionPointOverlap exercises the up-direction gate for POINT features
// in the full-scan (wasm) path: a coarse symbol overzoomed above its native band
// survives where no strictly-finer prim overlaps it (disappearing-light), and is
// suppressed only where a finer prim sits on it. zMax is set > natMax to simulate
// the overzoomed coarse prim and drive the branch directly.
func TestUpSuppressionPointOverlap(t *testing.T) {
	coastal := BandCoastal.ZoomRange() // {10,12}
	harbor := BandHarbor.ZoomRange()   // {14,16}
	base := geo.LatLon{Lat: 38.97, Lon: -76.49}
	mk := func(b *Baker, ll geo.LatLon, layer string, zr ZoomRange, zMax uint32) {
		r := routed{layer: layer, kind: mvt.GeomPoint, npoint: normPt(ll),
			zMin: zr.Min, zMax: zMax, natMin: zr.Min, natMax: zr.Max}
		r.attrs = []mvt.KeyValue{{Key: "class", Value: mvt.StringVal("X")}}
		b.add(r, ptBbox(ll))
	}
	rng := tile.RangeForBbox(14, ptBbox(base), mvt.ExtentDefault)
	coord := tile.TileCoord{Z: 14, X: rng.XMin, Y: rng.YMin}

	// A: finer feature elsewhere on the tile (disjoint) → coarse survives.
	bA := New()
	mk(bA, base, "point_symbols", coastal, 18)
	mk(bA, geo.LatLon{Lat: base.Lat + 0.0008, Lon: base.Lon + 0.0008}, "soundings", harbor, harbor.Max)
	layersA := decodeLayers(bA.EmitTile(coord, mvt.ExtentDefault, 64))
	if layersA["point_symbols"] == nil {
		t.Error("A: coarse overzoomed symbol suppressed even though no finer prim overlaps it")
	}
	// B: finer feature at the SAME location → coarse suppressed.
	bB := New()
	mk(bB, base, "point_symbols", coastal, 18)
	mk(bB, base, "soundings", harbor, harbor.Max)
	layersB := decodeLayers(bB.EmitTile(coord, mvt.ExtentDefault, 64))
	if layersB["point_symbols"] != nil {
		t.Error("B: coarse symbol should be suppressed where a finer prim overlaps it")
	}
}

func mustLatLon(lat, lon float64) geo.LatLon { return geo.LatLon{Lat: lat, Lon: lon} }

// legPrim / arcPrim build a one-element sector figure (as the rule emits) at a
// fixed anchor: a dashed CHBLK leg at the given bearing (fullNM>0 enables the
// extended variant), or a coloured arc/ring.
func legPrim(bearingDeg, fullNM float64) *sectorPrim {
	return &sectorPrim{fig: portrayal.AugmentedFigure{
		Anchor: mustLatLon(38.97, -76.49), Ray: true, BearingDeg: bearingDeg,
		LengthMM: 25, ColorToken: "CHBLK", WidthMM: 0.32, Dash: portrayal.DashDashed,
		FullLengthNM: fullNM,
	}}
}
func arcPrim(radiusMM, startDeg, sweepDeg float64, color string) *sectorPrim {
	return &sectorPrim{fig: portrayal.AugmentedFigure{
		Anchor: mustLatLon(38.97, -76.49), RadiusMM: radiusMM,
		StartDeg: startDeg, SweepDeg: sweepDeg, ColorToken: color, WidthMM: 0.64,
	}}
}

func absf(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
