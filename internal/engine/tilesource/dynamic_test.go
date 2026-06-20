package tilesource

import (
	"testing"

	"github.com/beetlebugorg/chartplotter/internal/engine/pmtiles"
)

// TestDynamicEmpty exercises the dynamic backend's plumbing without a heavy S-57
// parse: an empty cell set builds a baker with no features, so every tile bakes to
// blank (nil) and is cached. The real bake path is covered by the baker package.
func TestDynamicEmpty(t *testing.T) {
	d, err := NewDynamic(map[string][]byte{}, 8, nil)
	if err != nil {
		t.Fatalf("NewDynamic: %v", err)
	}
	body, err := d.Tile(5, 1, 2)
	if err != nil {
		t.Fatalf("Tile: %v", err)
	}
	if body != nil {
		t.Fatalf("Tile = %q, want nil (no features)", body)
	}
	// Second call hits the cache and must agree.
	if _, ok := d.cacheGet(pmtiles.ZxyToTileID(5, 1, 2)); !ok {
		t.Fatalf("blank tile was not cached")
	}
	if m := d.Meta(); m.MinZoom != 0 {
		t.Fatalf("empty Meta MinZoom = %d, want 0", m.MinZoom)
	}
}

// TestDynamicLRUEvicts checks the bounded cache drops the least-recently-used id.
func TestDynamicLRUEvicts(t *testing.T) {
	d, err := NewDynamic(map[string][]byte{}, 2, nil)
	if err != nil {
		t.Fatalf("NewDynamic: %v", err)
	}
	d.cachePut(1, []byte("a"))
	d.cachePut(2, []byte("b"))
	d.cacheGet(1)              // touch 1 so 2 is now the LRU
	d.cachePut(3, []byte("c")) // evicts 2
	if _, ok := d.cacheGet(2); ok {
		t.Fatalf("id 2 should have been evicted")
	}
	if _, ok := d.cacheGet(1); !ok {
		t.Fatalf("id 1 should be retained")
	}
	if _, ok := d.cacheGet(3); !ok {
		t.Fatalf("id 3 should be retained")
	}
}
