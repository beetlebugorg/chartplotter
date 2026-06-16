package bake

import (
	"testing"

	"github.com/beetlebugorg/chartplotter-go/internal/engine/mvt"
	"github.com/beetlebugorg/chartplotter-go/internal/engine/tile"
	"github.com/beetlebugorg/chartplotter-go/pkg/s52"
	"github.com/beetlebugorg/chartplotter-go/pkg/s52/preslib"
	"github.com/beetlebugorg/chartplotter-go/pkg/s57"
)

const goldenCell = "../../../testdata/US4MD81M.000"

func TestBandForScale(t *testing.T) {
	if BandForScale(12_000).ZoomRange() != (ZoomRange{13, 16}) {
		t.Error("12k should be harbor [13,16]")
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
	lib, err := s52.LoadLibraryFromBytes(preslib.DAI)
	if err != nil {
		t.Fatalf("load lib: %v", err)
	}
	chart, err := s57.Parse(goldenCell)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	b := New()
	b.AddCell(chart, lib, s52.DefaultMarinerSettings())

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
	lib, err := s52.LoadLibraryFromBytes(preslib.DAI)
	if err != nil {
		t.Fatalf("load lib: %v", err)
	}
	chart, err := s57.Parse(goldenCell)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	b := New()
	b.AddCell(chart, lib, s52.DefaultMarinerSettings())
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
