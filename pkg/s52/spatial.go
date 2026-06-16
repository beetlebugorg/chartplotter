package s52

import (
	"github.com/dhconnelly/rtreego"
)

// SpatialIndex provides O(log n) spatial queries for feature nodes
type SpatialIndex struct {
	tree *rtreego.Rtree
}

// NewSpatialIndex creates a new spatial index
func NewSpatialIndex() *SpatialIndex {
	return &SpatialIndex{
		tree: rtreego.NewTree(2, 25, 50), // 2D, min 25 items, max 50 items per node
	}
}

// nodeEntry wraps a FeatureNode to implement rtreego.Spatial interface
type nodeEntry struct {
	node   *FeatureNode
	bounds rtreego.Rect
}

// Bounds implements rtreego.Spatial
func (e *nodeEntry) Bounds() rtreego.Rect {
	return e.bounds
}

// Insert adds a feature node to the spatial index
func (s *SpatialIndex) Insert(node *FeatureNode) {
	// Calculate width and height
	width := node.GeoBounds.MaxLon - node.GeoBounds.MinLon
	height := node.GeoBounds.MaxLat - node.GeoBounds.MinLat

	// For point geometries (zero width/height), use a tiny epsilon
	// rtreego doesn't handle zero-size rectangles properly
	const epsilon = 0.0000001
	if width == 0 {
		width = epsilon
	}
	if height == 0 {
		height = epsilon
	}

	// Convert GeoBounds to rtreego.Rect
	bounds, err := rtreego.NewRect(
		rtreego.Point{node.GeoBounds.MinLon, node.GeoBounds.MinLat},
		[]float64{width, height},
	)
	if err != nil {
		// Invalid bounds, skip insertion
		return
	}

	entry := &nodeEntry{
		node:   node,
		bounds: bounds,
	}
	s.tree.Insert(entry)
}

// Query returns all nodes that intersect the given geographic bounds
func (s *SpatialIndex) Query(bounds GeoBounds) []*FeatureNode {
	// Convert GeoBounds to rtreego.Rect
	queryBounds, err := rtreego.NewRect(
		rtreego.Point{bounds.MinLon, bounds.MinLat},
		[]float64{
			bounds.MaxLon - bounds.MinLon,
			bounds.MaxLat - bounds.MinLat,
		},
	)
	if err != nil {
		return nil
	}

	// Query the tree
	results := s.tree.SearchIntersect(queryBounds)

	// Extract nodes from entries
	nodes := make([]*FeatureNode, 0, len(results))
	for _, result := range results {
		if entry, ok := result.(*nodeEntry); ok {
			nodes = append(nodes, entry.node)
		}
	}

	return nodes
}

// Size returns the number of nodes in the index
func (s *SpatialIndex) Size() int {
	return s.tree.Size()
}

// Clear removes all nodes from the index
func (s *SpatialIndex) Clear() {
	s.tree = rtreego.NewTree(2, 25, 50)
}
