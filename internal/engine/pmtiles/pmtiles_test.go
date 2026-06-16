package pmtiles

import (
	"encoding/binary"
	"testing"
)

func TestZxyToTileID(t *testing.T) {
	cases := []struct {
		z    uint8
		x, y uint32
		want uint64
	}{
		{0, 0, 0, 0},
		{1, 0, 0, 1}, {1, 0, 1, 2}, {1, 1, 1, 3}, {1, 1, 0, 4},
		{2, 0, 0, 5},
	}
	for _, c := range cases {
		if got := ZxyToTileID(c.z, c.x, c.y); got != c.want {
			t.Errorf("ZxyToTileID(%d,%d,%d) = %d, want %d", c.z, c.x, c.y, got, c.want)
		}
	}
}

func TestTinyArchive(t *testing.T) {
	b := New()
	b.AddTile(0, 0, 0, []byte("hello"))   // tile id 0
	b.AddTile(1, 1, 1, []byte("world!!")) // tile id 3
	b.AddTile(5, 0, 0, nil)               // empty -> omitted
	bytes := b.Finish()

	if string(bytes[0:7]) != "PMTiles" || bytes[7] != 3 {
		t.Fatal("bad magic/version")
	}
	rootOff := binary.LittleEndian.Uint64(bytes[8:16])
	dataOff := binary.LittleEndian.Uint64(bytes[56:64])
	dataLen := binary.LittleEndian.Uint64(bytes[64:72])
	if rootOff != 127 {
		t.Errorf("rootOff = %d, want 127", rootOff)
	}
	if n := binary.LittleEndian.Uint64(bytes[80:88]); n != 2 {
		t.Errorf("entries = %d, want 2 (empty dropped)", n)
	}
	if dataLen != 12 {
		t.Fatalf("dataLen = %d, want 12", dataLen)
	}
	if got := string(bytes[dataOff : dataOff+dataLen]); got != "helloworld!!" {
		t.Errorf("data = %q, want helloworld!!", got)
	}
}

func TestDedupResolvesViaDirectory(t *testing.T) {
	b := New()
	b.AddTile(2, 0, 0, []byte("AAAA"))   // id 5
	b.AddTile(3, 1, 2, []byte("BBBBBB")) // higher id
	b.AddTile(4, 3, 3, []byte("AAAA"))   // duplicate bytes -> dedup
	if b.dataLen != 10 {
		t.Fatalf("dataLen = %d, want 10 (deduped)", b.dataLen)
	}
	bytes := b.Finish()
	rootOff := binary.LittleEndian.Uint64(bytes[8:16])
	rootLen := binary.LittleEndian.Uint64(bytes[16:24])
	dataOff := binary.LittleEndian.Uint64(bytes[56:64])
	if bytes[96] != 0 {
		t.Error("expected not-clustered (byte 96 == 0)")
	}
	if n := binary.LittleEndian.Uint64(bytes[80:88]); n != 3 {
		t.Errorf("entries = %d, want 3", n)
	}
	dir := bytes[rootOff : rootOff+rootLen]
	a := dirFind(t, dir, ZxyToTileID(2, 0, 0))
	c := dirFind(t, dir, ZxyToTileID(4, 3, 3))
	bb := dirFind(t, dir, ZxyToTileID(3, 1, 2))
	if string(bytes[dataOff+a.off:dataOff+a.off+a.len]) != "AAAA" {
		t.Error("tile A wrong")
	}
	if string(bytes[dataOff+c.off:dataOff+c.off+c.len]) != "AAAA" {
		t.Error("tile C (dup) wrong")
	}
	if string(bytes[dataOff+bb.off:dataOff+bb.off+bb.len]) != "BBBBBB" {
		t.Error("tile B wrong")
	}
	if a.off != c.off {
		t.Error("dedup: A and C should point at the same blob")
	}
}

type dirHit struct{ off, len uint64 }

// dirFind decodes a single-page root directory and resolves want, as a reader does.
func dirFind(t *testing.T, dir []byte, want uint64) dirHit {
	t.Helper()
	p := 0
	uv := func() uint64 {
		var v uint64
		var sh uint
		for {
			c := dir[p]
			p++
			v |= uint64(c&0x7f) << sh
			if c < 0x80 {
				return v
			}
			sh += 7
		}
	}
	n := int(uv())
	ids := make([]uint64, n)
	lens := make([]uint64, n)
	offs := make([]uint64, n)
	var prev uint64
	for k := 0; k < n; k++ {
		prev += uv()
		ids[k] = prev
	}
	for k := 0; k < n; k++ {
		_ = uv() // run lengths
	}
	for k := 0; k < n; k++ {
		lens[k] = uv()
	}
	for k := 0; k < n; k++ {
		raw := uv()
		if raw == 0 {
			offs[k] = offs[k-1] + lens[k-1]
		} else {
			offs[k] = raw - 1
		}
	}
	for k := 0; k < n; k++ {
		if ids[k] == want {
			return dirHit{off: offs[k], len: lens[k]}
		}
	}
	t.Fatalf("tile %d not found in directory", want)
	return dirHit{}
}
