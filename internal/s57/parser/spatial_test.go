package parser

import (
	"encoding/binary"
	"testing"
)

// TestParseVectorPointers tests VRPT field parsing
func TestParseVectorPointers(t *testing.T) {
	tests := []struct {
		name     string
		data     []byte
		expected []vectorPointer
	}{
		{
			name:     "empty data",
			data:     []byte{},
			expected: []vectorPointer{},
		},
		{
			name: "single pointer",
			data: func() []byte {
				// Create a single VRPT entry per S-57 §7.7.1.4: 9 bytes
				// RCNM(1) + RCID(4) + ORNT(1) + USAG(1) + TOPI(1) + MASK(1)
				data := make([]byte, 9)
				data[0] = 130                                   // RCNM = Edge (130)
				binary.LittleEndian.PutUint32(data[1:5], 12345) // RCID = 12345
				data[5] = 1                                     // ORNT = Forward
				data[6] = 1                                     // USAG = Exterior
				data[7] = 1                                     // TOPI = Begin node
				data[8] = 255                                   // MASK = NULL
				return data
			}(),
			expected: []vectorPointer{
				{
					TargetRCNM:  130,
					TargetRCID:  12345,
					Orientation: 1,
					Usage:       1,
					Topology:    1,
					Mask:        255,
				},
			},
		},
		{
			name: "multiple pointers",
			data: func() []byte {
				// Create two VRPT entries, 9 bytes each = 18 bytes total
				data := make([]byte, 18)
				// First pointer
				data[0] = 110 // RCNM = IsolatedNode (110)
				binary.LittleEndian.PutUint32(data[1:5], 100)
				data[5] = 1   // ORNT = Forward
				data[6] = 1   // USAG = Exterior
				data[7] = 1   // TOPI = Begin
				data[8] = 255 // MASK = NULL
				// Second pointer
				data[9] = 130 // RCNM = Edge (130)
				binary.LittleEndian.PutUint32(data[10:14], 200)
				data[14] = 2   // ORNT = Reverse
				data[15] = 1   // USAG = Exterior
				data[16] = 2   // TOPI = End
				data[17] = 255 // MASK = NULL
				return data
			}(),
			expected: []vectorPointer{
				{
					TargetRCNM:  110,
					TargetRCID:  100,
					Orientation: 1,
					Usage:       1,
					Topology:    1,
					Mask:        255,
				},
				{
					TargetRCNM:  130,
					TargetRCID:  200,
					Orientation: 2,
					Usage:       1,
					Topology:    2,
					Mask:        255,
				},
			},
		},
		{
			name:     "incomplete data (truncated)",
			data:     []byte{130, 1, 2, 3, 4, 5, 6, 7}, // Only 8 bytes, need 9
			expected: []vectorPointer{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseVectorPointers(tt.data)

			if len(result) != len(tt.expected) {
				t.Errorf("parseVectorPointers() got %d pointers, want %d", len(result), len(tt.expected))
				return
			}

			for i, ptr := range result {
				exp := tt.expected[i]
				if ptr.TargetRCNM != exp.TargetRCNM {
					t.Errorf("pointer[%d].TargetRCNM = %d, want %d", i, ptr.TargetRCNM, exp.TargetRCNM)
				}
				if ptr.TargetRCID != exp.TargetRCID {
					t.Errorf("pointer[%d].TargetRCID = %d, want %d", i, ptr.TargetRCID, exp.TargetRCID)
				}
				if ptr.Orientation != exp.Orientation {
					t.Errorf("pointer[%d].Orientation = %d, want %d", i, ptr.Orientation, exp.Orientation)
				}
			}
		})
	}
}

// TestResolveVectorPointers tests VRPT topology resolution
func TestResolveVectorPointers(t *testing.T) {
	// Create test spatial records
	spatialRecords := map[spatialKey]*spatialRecord{
		// Node with direct coordinates
		{RCNM: int(spatialTypeIsolatedNode), RCID: 100}: {
			ID:         100,
			RecordType: spatialTypeIsolatedNode,
			Coordinates: [][]float64{
				{-71.0, 42.0},
				{-71.1, 42.1},
			},
			VectorPointers: nil,
		},
		// Edge with direct coordinates
		{RCNM: int(spatialTypeEdge), RCID: 200}: {
			ID:         200,
			RecordType: spatialTypeEdge,
			Coordinates: [][]float64{
				{-70.0, 43.0},
				{-70.1, 43.1},
			},
			VectorPointers: nil,
		},
		// Edge with VRPT pointers to node 100
		{RCNM: int(spatialTypeEdge), RCID: 300}: {
			ID:          300,
			RecordType:  spatialTypeEdge,
			Coordinates: nil, // No direct coords
			VectorPointers: []vectorPointer{
				{
					TargetRCNM:  110,
					TargetRCID:  100,
					Orientation: 1, // Forward
					Usage:       1,
					Topology:    1,
				},
			},
		},
		// Edge with reversed pointer to edge 200
		{RCNM: int(spatialTypeEdge), RCID: 400}: {
			ID:          400,
			RecordType:  spatialTypeEdge,
			Coordinates: nil,
			VectorPointers: []vectorPointer{
				{
					TargetRCNM:  130,
					TargetRCID:  200,
					Orientation: 2, // Reverse
					Usage:       1,
					Topology:    1,
				},
			},
		},
	}

	tests := []struct {
		name           string
		spatial        *spatialRecord
		expectedCoords int
		checkReverse   bool
	}{
		{
			name:           "resolve forward pointer",
			spatial:        spatialRecords[spatialKey{RCNM: int(spatialTypeEdge), RCID: 300}],
			expectedCoords: 2, // Should get 2 coords from node 100
			checkReverse:   false,
		},
		{
			name:           "resolve reverse pointer",
			spatial:        spatialRecords[spatialKey{RCNM: int(spatialTypeEdge), RCID: 400}],
			expectedCoords: 2, // Should get 2 coords from edge 200, reversed
			checkReverse:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			coords := resolveVectorPointers(tt.spatial, spatialRecords)

			if len(coords) != tt.expectedCoords {
				t.Errorf("resolveVectorPointers() got %d coords, want %d", len(coords), tt.expectedCoords)
			}

			// For reverse test, verify coordinates are reversed
			if tt.checkReverse && len(coords) > 0 {
				// Edge 200 has coords: {-70.0, 43.0}, {-70.1, 43.1}
				// Reversed should be: {-70.1, 43.1}, {-70.0, 43.0}
				if coords[0][0] != -70.1 || coords[0][1] != 43.1 {
					t.Errorf("First coord not reversed correctly: got [%f, %f], want [-70.1, 43.1]",
						coords[0][0], coords[0][1])
				}
			}
		})
	}
}

// TestCircularReferenceDetection tests that circular VRPT references don't cause infinite loops
func TestCircularReferenceDetection(t *testing.T) {
	// Create circular reference: 100 -> 200 -> 100
	spatialRecords := map[spatialKey]*spatialRecord{
		{RCNM: int(spatialTypeEdge), RCID: 100}: {
			ID:          100,
			RecordType:  spatialTypeEdge,
			Coordinates: [][]float64{{-71.0, 42.0}},
			VectorPointers: []vectorPointer{
				{
					TargetRCNM:  130,
					TargetRCID:  200,
					Orientation: 1,
				},
			},
		},
		{RCNM: int(spatialTypeEdge), RCID: 200}: {
			ID:          200,
			RecordType:  spatialTypeEdge,
			Coordinates: [][]float64{{-70.0, 43.0}},
			VectorPointers: []vectorPointer{
				{
					TargetRCNM:  130,
					TargetRCID:  100, // Circular reference!
					Orientation: 1,
				},
			},
		},
	}

	// This should not hang or panic
	coords := resolveVectorPointers(spatialRecords[spatialKey{RCNM: int(spatialTypeEdge), RCID: 100}], spatialRecords)

	// Should get at least the direct coordinates from 100
	if len(coords) < 1 {
		t.Errorf("Expected at least 1 coordinate, got %d", len(coords))
	}
}
