package parser

import (
	"testing"
)

// TestVRPTResolver tests the VRPT topology resolver
func TestVRPTResolver(t *testing.T) {
	tests := []struct {
		name           string
		spatialRecords map[spatialKey]*spatialRecord
		edgeRefs       []spatialRef
		expectError    bool
		expectRings    int
		description    string
	}{
		{
			name: "Simple triangle from 3 edges",
			spatialRecords: map[spatialKey]*spatialRecord{
				// Edge 1: node 1 -> node 2
				{RCNM: int(spatialTypeEdge), RCID: 1}: {
					ID:         1,
					RecordType: spatialTypeEdge,
					Coordinates: [][]float64{
						{0.0, 0.0},
						{1.0, 0.0},
					},
					VectorPointers: []vectorPointer{
						{TargetRCNM: int(spatialTypeConnectedNode), TargetRCID: 1}, // start node
						{TargetRCNM: int(spatialTypeConnectedNode), TargetRCID: 2}, // end node
					},
				},
				// Edge 2: node 2 -> node 3
				{RCNM: int(spatialTypeEdge), RCID: 2}: {
					ID:         2,
					RecordType: spatialTypeEdge,
					Coordinates: [][]float64{
						{1.0, 0.0},
						{0.5, 1.0},
					},
					VectorPointers: []vectorPointer{
						{TargetRCNM: int(spatialTypeConnectedNode), TargetRCID: 2}, // start node
						{TargetRCNM: int(spatialTypeConnectedNode), TargetRCID: 3}, // end node
					},
				},
				// Edge 3: node 3 -> node 1
				{RCNM: int(spatialTypeEdge), RCID: 3}: {
					ID:         3,
					RecordType: spatialTypeEdge,
					Coordinates: [][]float64{
						{0.5, 1.0},
						{0.0, 0.0},
					},
					VectorPointers: []vectorPointer{
						{TargetRCNM: int(spatialTypeConnectedNode), TargetRCID: 3}, // start node
						{TargetRCNM: int(spatialTypeConnectedNode), TargetRCID: 1}, // end node
					},
				},
			},
			edgeRefs: []spatialRef{
				{RCID: 1, Orientation: 1},
				{RCID: 2, Orientation: 1},
				{RCID: 3, Orientation: 1},
			},
			expectError: false,
			expectRings: 1,
			description: "Should construct a single triangle ring from 3 connected edges",
		},
		{
			name: "Square polygon (single ring)",
			spatialRecords: map[spatialKey]*spatialRecord{
				// Square edges
				{RCNM: int(spatialTypeEdge), RCID: 10}: {
					ID:         10,
					RecordType: spatialTypeEdge,
					Coordinates: [][]float64{
						{0.0, 0.0},
						{2.0, 0.0},
					},
					VectorPointers: []vectorPointer{
						{TargetRCNM: int(spatialTypeConnectedNode), TargetRCID: 10},
						{TargetRCNM: int(spatialTypeConnectedNode), TargetRCID: 11},
					},
				},
				{RCNM: int(spatialTypeEdge), RCID: 11}: {
					ID:         11,
					RecordType: spatialTypeEdge,
					Coordinates: [][]float64{
						{2.0, 0.0},
						{2.0, 2.0},
					},
					VectorPointers: []vectorPointer{
						{TargetRCNM: int(spatialTypeConnectedNode), TargetRCID: 11},
						{TargetRCNM: int(spatialTypeConnectedNode), TargetRCID: 12},
					},
				},
				{RCNM: int(spatialTypeEdge), RCID: 12}: {
					ID:         12,
					RecordType: spatialTypeEdge,
					Coordinates: [][]float64{
						{2.0, 2.0},
						{0.0, 2.0},
					},
					VectorPointers: []vectorPointer{
						{TargetRCNM: int(spatialTypeConnectedNode), TargetRCID: 12},
						{TargetRCNM: int(spatialTypeConnectedNode), TargetRCID: 13},
					},
				},
				{RCNM: int(spatialTypeEdge), RCID: 13}: {
					ID:         13,
					RecordType: spatialTypeEdge,
					Coordinates: [][]float64{
						{0.0, 2.0},
						{0.0, 0.0},
					},
					VectorPointers: []vectorPointer{
						{TargetRCNM: int(spatialTypeConnectedNode), TargetRCID: 13},
						{TargetRCNM: int(spatialTypeConnectedNode), TargetRCID: 10},
					},
				},
			},
			edgeRefs: []spatialRef{
				{RCID: 10, Orientation: 1}, {RCID: 11, Orientation: 1},
				{RCID: 12, Orientation: 1}, {RCID: 13, Orientation: 1},
			},
			expectError: false,
			expectRings: 1,
			description: "Should construct a square ring from 4 connected edges (real S-57 pattern)",
		},
		{
			name:           "Empty edge references",
			spatialRecords: map[spatialKey]*spatialRecord{},
			edgeRefs:       []spatialRef{},
			expectError:    true,
			expectRings:    0,
			description:    "Should return error for empty edge references",
		},
		{
			name: "Missing edge record - graceful handling",
			spatialRecords: map[spatialKey]*spatialRecord{
				{RCNM: int(spatialTypeEdge), RCID: 1}: {
					ID:         1,
					RecordType: spatialTypeEdge,
					Coordinates: [][]float64{
						{0.0, 0.0},
						{1.0, 0.0},
					},
				},
			},
			edgeRefs: []spatialRef{
				{RCID: 1, Orientation: 1},
				{RCID: 999, Orientation: 1}, // Edge 999 doesn't exist - should skip gracefully
			},
			expectError: false, // Real S-57 parsers skip missing edges gracefully
			expectRings: 1,     // Should still build ring from valid edge
			description: "Should gracefully skip missing edge references (real S-57 behavior)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolver := newPolygonBuilder(tt.spatialRecords)

			rings, err := resolver.resolvePolygon(tt.edgeRefs)

			if tt.expectError {
				if err == nil {
					t.Errorf("%s: expected error but got none", tt.description)
				}
				return
			}

			if err != nil {
				t.Errorf("%s: unexpected error: %v", tt.description, err)
				return
			}

			if len(rings) != tt.expectRings {
				t.Errorf("%s: expected %d rings, got %d", tt.description, tt.expectRings, len(rings))
			}

			// Validate ring closure
			for i, ring := range rings {
				if !isRingClosed(ring) {
					t.Errorf("%s: ring %d is not closed", tt.description, i)
				}
			}
		})
	}
}

// TestLoadEdge tests edge loading and caching
func TestLoadEdge(t *testing.T) {
	spatialRecords := map[spatialKey]*spatialRecord{
		{RCNM: int(spatialTypeEdge), RCID: 100}: {
			ID:         100,
			RecordType: spatialTypeEdge,
			Coordinates: [][]float64{
				{-1.0, 51.0},
				{-0.9, 51.1},
			},
			VectorPointers: []vectorPointer{
				{TargetRCNM: int(spatialTypeConnectedNode), TargetRCID: 1},
				{TargetRCNM: int(spatialTypeConnectedNode), TargetRCID: 2},
			},
		},
		{RCNM: int(spatialTypeIsolatedNode), RCID: 200}: {
			ID:         200,
			RecordType: spatialTypeIsolatedNode, // Not an edge
			Coordinates: [][]float64{
				{0.0, 0.0},
			},
		},
	}

	resolver := newPolygonBuilder(spatialRecords)

	t.Run("Load valid edge", func(t *testing.T) {
		edge, err := resolver.loadEdge(100)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if edge.ID != 100 {
			t.Errorf("Expected edge ID 100, got %d", edge.ID)
		}

		if edge.StartNodeID != 1 || edge.EndNodeID != 2 {
			t.Errorf("Expected nodes 1->2, got %d->%d", edge.StartNodeID, edge.EndNodeID)
		}

		if len(edge.Points) != 2 {
			t.Errorf("Expected 2 points, got %d", len(edge.Points))
		}
	})

	t.Run("Load edge from cache", func(t *testing.T) {
		// Load twice - second should come from cache
		edge1, _ := resolver.loadEdge(100)
		edge2, _ := resolver.loadEdge(100)

		if edge1 != edge2 {
			t.Error("Expected cached edge to be same instance")
		}
	})

	t.Run("Load non-existent edge", func(t *testing.T) {
		_, err := resolver.loadEdge(999)
		if err == nil {
			t.Error("Expected error for non-existent edge")
		}
	})

	t.Run("Load wrong record type", func(t *testing.T) {
		_, err := resolver.loadEdge(200) // Node, not edge
		if err == nil {
			t.Error("Expected error for wrong record type")
		}
	})
}

// TestRingClosure tests ring closure validation
func TestRingClosure(t *testing.T) {
	tests := []struct {
		name     string
		ring     [][2]float64
		expected bool
	}{
		{
			name: "Closed ring",
			ring: [][2]float64{
				{0.0, 0.0},
				{1.0, 0.0},
				{1.0, 1.0},
				{0.0, 0.0}, // Same as first
			},
			expected: true,
		},
		{
			name: "Open ring",
			ring: [][2]float64{
				{0.0, 0.0},
				{1.0, 0.0},
				{1.0, 1.0},
				{0.5, 0.5}, // Different from first
			},
			expected: false,
		},
		{
			name: "Too few points",
			ring: [][2]float64{
				{0.0, 0.0},
				{1.0, 1.0},
			},
			expected: false,
		},
		{
			name:     "Empty ring",
			ring:     [][2]float64{},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isRingClosed(tt.ring)
			if result != tt.expected {
				t.Errorf("Expected %v, got %v for ring: %v", tt.expected, result, tt.ring)
			}
		})
	}
}
