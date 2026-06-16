package s52

import (
	"testing"
)

func TestPattern_GetTileInfo(t *testing.T) {
	tests := []struct {
		name           string
		pattern        Pattern
		expectedSpaceX float64
		expectedSpaceY float64
		expectedLinear bool
	}{
		{
			name: "Linear pattern with zero spacing",
			pattern: Pattern{
				PatternType: "LINCON",
				SpacingX:    0, // DAI units (0 * 0.01 = 0mm)
				SpacingY:    0,
				BoundingBox: BoundingBox{
					MinX: 0, MinY: 0,
					MaxX: 1000, // DAI units (1000 * 0.01 = 10mm)
					MaxY: 800,  // DAI units (800 * 0.01 = 8mm)
				},
			},
			expectedSpaceX: 10.0, // Edge-to-edge using bbox width
			expectedSpaceY: 8.0,  // Edge-to-edge using bbox height
			expectedLinear: true,
		},
		{
			name: "Staggered pattern with spacing",
			pattern: Pattern{
				PatternType: "STAGGER",
				SpacingX:    200, // DAI units (200 * 0.01 = 2mm gap between symbols)
				SpacingY:    150, // DAI units (150 * 0.01 = 1.5mm gap between symbols)
				BoundingBox: BoundingBox{
					MinX: 0, MinY: 0,
					MaxX: 1000, // DAI units (1000 * 0.01 = 10mm)
					MaxY: 800,  // DAI units (800 * 0.01 = 8mm)
				},
			},
			expectedSpaceX: 12.0, // gap (2mm) + bbox (10mm) = 12mm center-to-center
			expectedSpaceY: 9.5,  // gap (1.5mm) + bbox (8mm) = 9.5mm center-to-center
			expectedLinear: false,
		},
		{
			name: "Linear pattern with spacing (non-zero)",
			pattern: Pattern{
				PatternType: "LIN",
				SpacingX:    100, // DAI units (100 * 0.01 = 1mm gap between symbols)
				SpacingY:    100,
				BoundingBox: BoundingBox{
					MinX: 0, MinY: 0,
					MaxX: 500, // DAI units (500 * 0.01 = 5mm)
					MaxY: 500,
				},
			},
			expectedSpaceX: 6.0, // gap (1mm) + bbox (5mm) = 6mm center-to-center
			expectedSpaceY: 6.0,
			expectedLinear: true,
		},
		{
			name: "Pattern with very small bbox (sanity check)",
			pattern: Pattern{
				PatternType: "STAGGER",
				SpacingX:    0,
				SpacingY:    0,
				BoundingBox: BoundingBox{
					MinX: 0, MinY: 0,
					MaxX: 10, // DAI units (10 * 0.01 = 0.1mm - too small)
					MaxY: 10,
				},
			},
			expectedSpaceX: 5.0, // Minimum enforced
			expectedSpaceY: 5.0,
			expectedLinear: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := tt.pattern.GetTileInfo()

			if info.SpacingX != tt.expectedSpaceX {
				t.Errorf("GetTileInfo().SpacingX = %.2f, expected %.2f", info.SpacingX, tt.expectedSpaceX)
			}
			if info.SpacingY != tt.expectedSpaceY {
				t.Errorf("GetTileInfo().SpacingY = %.2f, expected %.2f", info.SpacingY, tt.expectedSpaceY)
			}
			if info.IsLinear != tt.expectedLinear {
				t.Errorf("GetTileInfo().IsLinear = %v, expected %v", info.IsLinear, tt.expectedLinear)
			}
		})
	}
}

// NOTE: Pattern tile position calculation has been moved to canvas52/main/pkg/v1/engine.go
// See Engine.RenderPattern() for the refactored implementation.
// This allows pattern rendering to work with arbitrary polygon shapes and be a
// composable high-level operation in the rendering engine.
