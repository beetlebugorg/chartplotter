package pmtiles

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"testing"
)

// roundTrip builds an archive from the given tiles, reads each back, and checks
// the bytes survive. nTiles is chosen by callers to exercise root-only vs leaf dirs.
func TestReaderRoundTrip(t *testing.T) {
	for _, nTiles := range []int{1, 100, leafSize, leafSize + 1, 5000} {
		t.Run(fmt.Sprintf("n=%d", nTiles), func(t *testing.T) {
			b := New()
			want := make(map[[3]uint32][]byte, nTiles)
			for i := 0; i < nTiles; i++ {
				z := uint8(12)
				x := uint32(i & 0xFFF)
				y := uint32(i >> 12)
				body := []byte(fmt.Sprintf("tile-%d-payload", i))
				b.AddTile(z, x, y, body)
				want[[3]uint32{uint32(z), x, y}] = body
			}
			arc := b.Finish()

			rd, err := NewReader(bytes.NewReader(arc), int64(len(arc)))
			if err != nil {
				t.Fatalf("NewReader: %v", err)
			}
			for k, body := range want {
				got, err := rd.Tile(uint8(k[0]), k[1], k[2])
				if err != nil {
					t.Fatalf("Tile(%v): %v", k, err)
				}
				if !bytes.Equal(got, body) {
					t.Fatalf("Tile(%v) = %q, want %q", k, got, body)
				}
			}
			// A tile that was never added reads as blank (nil), not an error.
			if got, err := rd.Tile(12, 4095, 4095); err != nil || got != nil {
				t.Fatalf("missing tile = (%q, %v), want (nil, nil)", got, err)
			}
		})
	}
}

func TestReaderGzippedTiles(t *testing.T) {
	b := New()
	b.SetTilesGzipped()
	plain := []byte("the quick brown fox jumps over the lazy dog, repeatedly, for compression")
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	zw.Write(plain)
	zw.Close()
	b.AddTile(5, 1, 2, gz.Bytes())
	arc := b.Finish()

	rd, err := NewReader(bytes.NewReader(arc), int64(len(arc)))
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	if !rd.Meta().Gzipped {
		t.Fatalf("Meta().Gzipped = false, want true")
	}
	got, err := rd.Tile(5, 1, 2)
	if err != nil {
		t.Fatalf("Tile: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("Tile = %q, want decompressed %q", got, plain)
	}
}

func TestReaderMeta(t *testing.T) {
	b := New()
	b.AddTile(8, 10, 20, []byte("x"))
	b.SetBounds(-76.5, 38.9, -76.3, 39.1)
	arc := b.Finish()
	rd, err := NewReader(bytes.NewReader(arc), int64(len(arc)))
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	m := rd.Meta()
	if m.MaxZoom != 8 || m.MinZoom != 8 {
		t.Fatalf("zoom = [%d,%d], want [8,8]", m.MinZoom, m.MaxZoom)
	}
	// Bounds survive the e7 round-trip to within 1e-7.
	if d := m.W - -76.5; d > 1e-6 || d < -1e-6 {
		t.Fatalf("W = %v, want -76.5", m.W)
	}
}
