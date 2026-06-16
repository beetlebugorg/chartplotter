package s52

// SceneSurface is the interface that surfaces must implement for scene-based rendering.
// This is the new primary rendering API - all surfaces should implement this to benefit
// from the scene graph's caching, dirty tracking, and spatial culling.
//
// Backends access S-52 metadata directly from FeatureNode:
// - node.Priority (DisplayPriority 0-9) - for GPU layer batching
// - node.Style.InstructionSet.DisplayCategory - for mariner visibility filtering
// - node.Style.InstructionSet.RadarPriority - for radar overlay handling
// - node.Primitives - pre-computed backend-agnostic drawing commands
//
// Rendering strategies:
// - Simple surfaces: render immediately in the order called (already sorted by priority)
// - GPU surfaces: batch by node.Priority into separate framebuffers/layers
// - Advanced surfaces: use DisplayCategory/ViewingGroup for selective rendering
type SceneSurface interface {
	// BeginScene starts a new scene rendering pass with the given viewport
	BeginScene(viewport Viewport) error

	// RenderNode renders a single feature node
	// The node contains:
	//   - Pre-computed geometry (node.Geometry, node.Rings)
	//   - Resolved style (node.Style with colors, symbols, patterns)
	//   - Render primitives (node.Primitives)
	//   - S-52 metadata (node.Priority, node.Style.InstructionSet)
	//
	// Nodes are provided in priority order (lowest to highest)
	RenderNode(node *FeatureNode) error

	// EndScene completes the scene rendering pass
	// For layered surfaces, this should composite all batched layers in priority order
	EndScene() error
}
