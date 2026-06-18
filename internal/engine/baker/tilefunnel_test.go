package baker_test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/beetlebugorg/chartplotter/internal/engine/bake"
	"github.com/beetlebugorg/chartplotter/internal/engine/baker"
	"github.com/beetlebugorg/chartplotter/internal/engine/tile"
)

// TestTileFunnel bakes one tile from one or more cached cells and logs the
// per-stage primitive funnel (eligible → zMin-gated → emitted) from TileDiag, to
// pinpoint where polygons disappear from a baked tile. Opt-in via env:
//
//	CELLS=$HOME/.cache/chartplotter/ENC_ROOT/US2EC02M/US2EC02M.000 TILE=7/35/53 \
//	  go test -run TestTileFunnel -v ./internal/engine/baker/
//
// CELLS is a comma-separated list of .000 paths; TILE is z/x/y. Skips when unset.
func TestTileFunnel(t *testing.T) {
	cells, tl := os.Getenv("CELLS"), os.Getenv("TILE")
	if cells == "" || tl == "" {
		t.Skip("set CELLS=path[,path] and TILE=z/x/y to run")
	}
	parts := strings.Split(tl, "/")
	if len(parts) != 3 {
		t.Fatalf("TILE must be z/x/y, got %q", tl)
	}
	z, _ := strconv.Atoi(parts[0])
	x, _ := strconv.Atoi(parts[1])
	y, _ := strconv.Atoi(parts[2])

	sess, err := baker.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	for _, cp := range strings.Split(cells, ",") {
		cp = strings.TrimSpace(cp)
		data, err := os.ReadFile(cp)
		if err != nil {
			t.Fatal(err)
		}
		name := strings.TrimSuffix(filepath.Base(cp), ".000")
		if err := sess.AddCellBytes(name, data); err != nil {
			t.Fatalf("AddCellBytes %s: %v", name, err)
		}
		t.Logf("loaded %s (%d bytes)", name, len(data))
	}

	bake.TileDiag = func(s string) { t.Log(s) }
	defer func() { bake.TileDiag = nil }()
	var ts bake.TileScratch
	coord := tile.TileCoord{Z: uint32(z), X: uint32(x), Y: uint32(y)}
	out := sess.Baker.EmitTileInto(coord, baker.MVTExtent, baker.MVTBuffer, &ts)
	t.Logf("tile %d/%d/%d → %d MVT bytes", z, x, y, len(out))
	stored, trueOv := sess.Baker.DebugTilePolyOverlap(coord, baker.MVTBuffer, baker.MVTExtent)
	t.Logf("polygon overlap: storedBBox=%d trueBBox=%d (gap ⇒ cached bbox drops polys)", stored, trueOv)
	for _, l := range decodeMVTLayers(t, out) {
		t.Logf("  layer %-14s features=%d (point=%d line=%d polygon=%d) maxVerts/feature=%d%s",
			l.name, l.total, l.byType[1], l.byType[2], l.byType[3], l.maxVerts,
			map[bool]string{true: "  ⚠ EXCEEDS MapLibre 65535/segment", false: ""}[l.maxVerts > 65535])
	}
}

type mvtLayer struct {
	name     string
	total    int
	byType   map[uint32]int // 1=point 2=line 3=polygon
	maxVerts int            // most vertices in a single feature (the MapLibre fill-segment limit is 65535)
}

// decodeMVTLayers is a minimal Mapbox-Vector-Tile reader: it walks the protobuf
// just enough to count features per layer by geometry type — the ground truth of
// what a PMTiles/MVT viewer sees in this tile.
func decodeMVTLayers(t *testing.T, b []byte) []mvtLayer {
	t.Helper()
	var out []mvtLayer
	p := 0
	for p < len(b) {
		field, wt, n := readTag(b, p)
		p += n
		if wt == 2 { // length-delimited
			l, m := readVarint(b, p)
			p += m
			body := b[p : p+int(l)]
			p += int(l)
			if field == 3 { // Tile.layers
				out = append(out, decodeLayer(body))
			}
		} else {
			_, m := readVarint(b, p)
			p += m
		}
	}
	return out
}

func decodeLayer(b []byte) mvtLayer {
	lay := mvtLayer{byType: map[uint32]int{}}
	p := 0
	for p < len(b) {
		field, wt, n := readTag(b, p)
		p += n
		switch {
		case field == 1 && wt == 2: // name
			l, m := readVarint(b, p)
			p += m
			lay.name = string(b[p : p+int(l)])
			p += int(l)
		case field == 2 && wt == 2: // feature
			l, m := readVarint(b, p)
			p += m
			lay.total++
			gt, nv := featureGeom(b[p : p+int(l)])
			lay.byType[gt]++
			if nv > lay.maxVerts {
				lay.maxVerts = nv
			}
			p += int(l)
		case wt == 2:
			l, m := readVarint(b, p)
			p += m + int(l)
		default:
			_, m := readVarint(b, p)
			p += m
		}
	}
	return lay
}

// featureGeom returns a feature's geometry type (1=point 2=line 3=polygon) and
// its total vertex count (sum of MoveTo/LineTo command counts) — the figure
// MapLibre's fill bucket caps at 65535 per segment.
func featureGeom(b []byte) (gtype uint32, nverts int) {
	p := 0
	for p < len(b) {
		field, wt, n := readTag(b, p)
		p += n
		if field == 3 && wt == 0 { // Feature.type (varint)
			v, m := readVarint(b, p)
			p += m
			gtype = uint32(v)
			continue
		}
		if field == 4 && wt == 2 { // Feature.geometry (packed command ints)
			l, m := readVarint(b, p)
			p += m
			geom := b[p : p+int(l)]
			p += int(l)
			q := 0
			for q < len(geom) {
				cmd, mm := readVarint(geom, q)
				q += mm
				id, count := cmd&7, int(cmd>>3)
				if id == 1 || id == 2 { // MoveTo / LineTo: count points, each 2 varints
					nverts += count
					for k := 0; k < count*2 && q < len(geom); k++ {
						_, mc := readVarint(geom, q)
						q += mc
					}
				}
				// id==7 ClosePath: no operands
			}
			continue
		}
		if wt == 2 {
			l, m := readVarint(b, p)
			p += m + int(l)
		} else {
			_, m := readVarint(b, p)
			p += m
		}
	}
	return gtype, nverts
}

func readTag(b []byte, p int) (field uint32, wt uint32, n int) {
	v, m := readVarint(b, p)
	return uint32(v >> 3), uint32(v & 7), m
}
func readVarint(b []byte, p int) (uint64, int) {
	var v uint64
	var s, n int
	for p+n < len(b) {
		c := b[p+n]
		v |= uint64(c&0x7f) << s
		n++
		if c < 0x80 {
			break
		}
		s += 7
	}
	return v, n
}
