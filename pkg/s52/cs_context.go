package s52

// CSContext provides execution context for CS (Conditional Symbology) procedures.
// It bundles all the data a CS procedure needs: feature attributes, geometry info,
// spatial relationships, and mariner settings.
//
// This context-based design simplifies procedure signatures and provides convenient
// helper methods for common operations like type-safe attribute access.
type CSContext struct {
	// Attributes contains S-57 feature attributes (e.g., DRVAL1, VALSOU, CATLIT)
	Attributes map[string]interface{}

	// GeometryType identifies the feature's geometry type
	// Valid values: "Point", "Line", "Area", "MultiPoint", or "" if unknown
	GeometryType string

	// Spatial provides optional spatial topology information (adjacency, underlying objects, etc.)
	// When nil, procedures use simplified logic that doesn't require spatial analysis
	Spatial *SpatialContext

	// Mariner contains display settings (safety contours, color scheme, etc.)
	// If nil, DefaultMarinerSettings() will be used
	Mariner *MarinerSettings
}

// NewCSContext creates a new CS execution context with the given parameters.
// If mariner is nil, default mariner settings will be used.
func NewCSContext(
	attributes map[string]interface{},
	geometryType string,
	spatial *SpatialContext,
	mariner *MarinerSettings,
) *CSContext {
	if mariner == nil {
		mariner = DefaultMarinerSettings()
	}
	return &CSContext{
		Attributes:   attributes,
		GeometryType: geometryType,
		Spatial:      spatial,
		Mariner:      mariner,
	}
}

// Has returns true if the attribute exists in the context.
func (c *CSContext) Has(key string) bool {
	if c.Attributes == nil {
		return false
	}
	_, exists := c.Attributes[key]
	return exists
}

// Get returns the raw attribute value, or nil if not present.
func (c *CSContext) Get(key string) interface{} {
	if c.Attributes == nil {
		return nil
	}
	return c.Attributes[key]
}

// GetFloat returns the attribute as a float64, or the default value if not present or invalid.
func (c *CSContext) GetFloat(key string, defaultValue float64) float64 {
	val, ok := c.Attributes[key]
	if !ok {
		return defaultValue
	}
	return getFloatValue(val)
}

// GetInt returns the attribute as an int, or the default value if not present or invalid.
func (c *CSContext) GetInt(key string, defaultValue int) int {
	val, ok := c.Attributes[key]
	if !ok {
		return defaultValue
	}
	return getIntValue(val)
}

// GetString returns the attribute as a string, or the default value if not present.
func (c *CSContext) GetString(key string, defaultValue string) string {
	val, ok := c.Attributes[key]
	if !ok {
		return defaultValue
	}

	switch v := val.(type) {
	case string:
		return v
	case int:
		return intToString(v)
	case float64:
		return intToString(int(v))
	default:
		return defaultValue
	}
}

// HasSpatial returns true if spatial context is available.
func (c *CSContext) HasSpatial() bool {
	return c.Spatial != nil
}

// HasAdjacentObjects returns true if spatial context includes adjacent object information.
func (c *CSContext) HasAdjacentObjects() bool {
	return c.Spatial != nil && len(c.Spatial.AdjacentObjects) > 0
}

// HasUnderlyingObjects returns true if spatial context includes underlying object information.
func (c *CSContext) HasUnderlyingObjects() bool {
	return c.Spatial != nil && len(c.Spatial.UnderlyingObjects) > 0
}

// HasComponents returns true if spatial context includes component-level data.
func (c *CSContext) HasComponents() bool {
	return c.Spatial != nil && len(c.Spatial.Components) > 0
}
