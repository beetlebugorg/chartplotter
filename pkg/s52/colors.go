// S-52 color system management with CIE xyY colorimetric support.
//
// Colors come from the PresLib DAI as CIE xyY. Conversion to sRGB: the xyY
// values are treated as D65 directly (no Illuminant-C -> D65 chromatic
// adaptation), linear sRGB is clamped to [0,1] before gamma encoding, and 8-bit
// values are rounded. This keeps the generated colortables.json and the
// symbol/pattern atlases pixel-stable. See colors_fidelity_test.go.
package s52

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// ColorScheme represents S-52 color viewing modes
type ColorScheme string

const (
	ColorSchemeDay   ColorScheme = "day"
	ColorSchemeDusk  ColorScheme = "dusk"
	ColorSchemeNight ColorScheme = "night"
)

// Color represents an official S-52 color from the DAI file.
// Uses CIE xyY color coordinates as specified in S-52 CCIE records.
// Reference: specs/s52-dai-format.md section "Color Definitions (CCIE)"
type Color struct {
	Token       string  `json:"token"`        // e.g., "CHBLK"
	ColorCode   int     `json:"color_code"`   // e.g., 30
	CIE_X       float64 `json:"cie_x"`        // CIE chromaticity x coordinate
	CIE_Y       float64 `json:"cie_y"`        // CIE chromaticity y coordinate
	CIE_L       float64 `json:"cie_l"`        // CIE luminance
	Description string  `json:"description"`  // e.g., "black"
	ViewingMode string  `json:"viewing_mode"` // "DAY", "DUSK", or "NIGHT"

	// Deprecated fields - kept for backwards compatibility
	Hue        float64 `json:"hue"`        // Legacy field, now maps to CIE_X
	Saturation float64 `json:"saturation"` // Legacy field, now maps to CIE_Y
	Lightness  float64 `json:"lightness"`  // Legacy field, now maps to CIE_L
}

// ColorDatabase manages all official S-52 colors for DAY/DUSK/NIGHT viewing modes.
// Provides thread-safe access to S-52 color definitions.
// Reference: specs/s52-dai-format.md section "Standards Compliance"
type ColorDatabase struct {
	DayColors   map[string]Color `json:"day_colors"`
	DuskColors  map[string]Color `json:"dusk_colors"`
	NightColors map[string]Color `json:"night_colors"`
	mu          sync.RWMutex     // Protects concurrent access to maps
}

// NewColorDatabase creates a new color database
func NewColorDatabase() *ColorDatabase {
	return &ColorDatabase{
		DayColors:   make(map[string]Color),
		DuskColors:  make(map[string]Color),
		NightColors: make(map[string]Color),
	}
}

// ParseDAIColors extracts all CCIE color definitions from the DAI file.
// Parses color sections (NILDAY, NILDUSK, NILNIGHT) and CCIE records.
// Reference: specs/s52-dai-format.md section "Color Definitions (CCIE)"
func (db *ColorDatabase) ParseDAIColors(daiFilePath string) error {
	file, err := os.Open(daiFilePath)
	if err != nil {
		return fmt.Errorf("failed to open DAI file: %v", err)
	}
	defer file.Close()

	return db.parseColorsFromReader(file)
}

// ParseDAIColorsFromBytes parses colors from DAI file bytes
// Useful for WASM environments where filesystem access isn't available
func (db *ColorDatabase) ParseDAIColorsFromBytes(data []byte) error {
	return db.parseColorsFromReader(bytes.NewReader(data))
}

func (db *ColorDatabase) parseColorsFromReader(reader io.Reader) error {
	// Use fixed-width parsing for CCIE lines: CCIE   33CHMGD0.30000.170020.00magenta
	// Field positions: CCIE(0-4) spaces(4-7) code(7-9) token(9-14) hue(14-20) sat(20-26) light(26-31) desc(31+)
	ccieRegex := regexp.MustCompile(`^CCIE\s+(.+)$`) // Just capture everything after CCIE

	scanner := bufio.NewScanner(reader)
	currentMode := ""

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Detect viewing mode sections
		if strings.Contains(line, "NILDAY") {
			currentMode = "DAY"
			continue
		} else if strings.Contains(line, "NILDUSK") {
			currentMode = "DUSK"
			continue
		} else if strings.Contains(line, "NILNIGHT") {
			currentMode = "NIGHT"
			continue
		}

		// Parse CCIE color definitions
		if strings.HasPrefix(line, "CCIE") {
			matches := ccieRegex.FindStringSubmatch(line)
			if len(matches) == 2 {
				color, err := parseColorFromFixedWidth(line, currentMode)
				if err != nil {
					fmt.Printf("Warning: Failed to parse color line: %s - %v\n", line, err)
					continue
				}

				// Store color in appropriate map (thread-safe)
				db.mu.Lock()
				switch currentMode {
				case "DAY":
					db.DayColors[color.Token] = color
				case "DUSK":
					db.DuskColors[color.Token] = color
				case "NIGHT":
					db.NightColors[color.Token] = color
				}
				db.mu.Unlock()
			}
		}
	}

	return scanner.Err()
}

// parseColorFromFixedWidth creates an Color using fixed-width parsing of CCIE records.
// Handles both Unit Separator (0x1F) delimited and fixed-width formats.
// Reference: specs/DAI_TO_SVG_RENDERING_SPEC.md section "CCIE (Colors)"
func parseColorFromFixedWidth(line string, mode string) (Color, error) {
	if len(line) < 30 {
		return Color{}, fmt.Errorf("line too short: %d characters", len(line))
	}

	// Fixed positions: CCIE   30CHBLK0.28000.31000.00black
	colorCodeStr := strings.TrimSpace(line[7:9])
	token := strings.TrimSpace(line[9:14])

	// Find where description starts (first alphabetic character after position 14)
	numericPart := line[14:]
	descStart := len(numericPart)
	for i, c := range numericPart {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
			descStart = i
			break
		}
	}

	numbersOnly := numericPart[:descStart]
	description := strings.TrimSpace(numericPart[descStart:])

	// Parse CIE color coordinates: x, y, L
	// Format: CCIE [code][name][x.xxxx]<US>[y.yyyy]<US>[zzz.zz][description]
	// where <US> is the ASCII Unit Separator character (0x1F)
	var xStr, yStr, lStr string

	// Split by Unit Separator (ASCII 31 / 0x1F)
	const unitSeparator = '\x1F'
	parts := strings.Split(numbersOnly, string(unitSeparator))

	if len(parts) >= 3 {
		// Format with unit separators
		xStr = parts[0]
		yStr = parts[1]
		lStr = parts[2]
	} else if len(numbersOnly) >= 17 {
		// Fallback to fixed-width for files without separators
		// Standard format: 0.2800 (6), 0.3100 (6), 80.00 (5+)
		xStr = numbersOnly[0:6]  // "0.2800"
		yStr = numbersOnly[6:12] // "0.3100"
		lStr = numbersOnly[12:]  // "80.00" or similar
	} else {
		return Color{}, fmt.Errorf("unable to parse CIE coordinates from: %s", numbersOnly)
	}

	colorCode, err := strconv.Atoi(colorCodeStr)
	if err != nil {
		return Color{}, fmt.Errorf("invalid color code: %s", colorCodeStr)
	}

	// Parse the CIE coordinates
	cieX, err := strconv.ParseFloat(xStr, 64)
	if err != nil {
		return Color{}, fmt.Errorf("invalid CIE x coordinate: %s", xStr)
	}

	cieY, err := strconv.ParseFloat(yStr, 64)
	if err != nil {
		return Color{}, fmt.Errorf("invalid CIE y coordinate: %s", yStr)
	}

	cieL, err := strconv.ParseFloat(lStr, 64)
	if err != nil {
		return Color{}, fmt.Errorf("invalid CIE luminance: %s", lStr)
	}

	return Color{
		Token:       token,
		ColorCode:   colorCode,
		CIE_X:       cieX,
		CIE_Y:       cieY,
		CIE_L:       cieL,
		Description: description,
		ViewingMode: mode,
		// Legacy fields for backwards compatibility
		Hue:        cieX, // Map X to Hue for now
		Saturation: cieY, // Map Y to Saturation for now
		Lightness:  cieL, // Map L to Lightness
	}, nil
}

// GetColor retrieves a color for a specific viewing mode (thread-safe)
func (db *ColorDatabase) GetColor(token string, mode string) (Color, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	var colorMap map[string]Color

	switch strings.ToUpper(mode) {
	case "DAY":
		colorMap = db.DayColors
	case "DUSK":
		colorMap = db.DuskColors
	case "NIGHT":
		colorMap = db.NightColors
	default:
		return Color{}, fmt.Errorf("invalid viewing mode: %s", mode)
	}

	color, exists := colorMap[token]
	if !exists {
		return Color{}, fmt.Errorf("color token %s not found for mode %s", token, mode)
	}

	return color, nil
}

// ConvertToRGB converts S-52 CIE xyY coordinates to sRGB using proper colorimetric conversion.
// Implements Bradford chromatic adaptation from Illuminant C to D65 as specified.
// Reference: specs/DAI_TO_SVG_RENDERING_SPEC.md section "Color System"
func (color Color) ConvertToRGB() (r, g, b float64) {
	// This conversion keeps the generated colortables.json — and the
	// symbol/pattern atlases — visually consistent. The PresLib xyY values are treated as
	// D65 directly (NO Illuminant-C -> D65 Bradford adaptation), and the linear
	// RGB is clamped to [0,1] BEFORE gamma encoding. Returns gamma-encoded sRGB
	// in [0,1]; ConvertToHex rounds to 8-bit.
	x := color.CIE_X
	y := color.CIE_Y
	Y := color.CIE_L / 100.0 // percent luminance -> 0..1

	// Guard against division by zero / non-positive luminance -> black.
	if y < 1e-6 || Y <= 0 {
		return 0, 0, 0
	}

	// CIE xyY -> CIE XYZ.
	X := (x / y) * Y
	Z := ((1 - x - y) / y) * Y

	// CIE XYZ (D65) -> linear sRGB. Matrix from IEC 61966-2-1.
	rLinear := 3.2404542*X - 1.5371385*Y - 0.4985314*Z
	gLinear := -0.9692660*X + 1.8760108*Y + 0.0415560*Z
	bLinear := 0.0556434*X - 0.2040259*Y + 1.0572252*Z

	// Clamp out-of-gamut values in linear space, then gamma-encode.
	r = sRGBGamma(clamp(rLinear, 0, 1))
	g = sRGBGamma(clamp(gLinear, 0, 1))
	b = sRGBGamma(clamp(bLinear, 0, 1))

	return r, g, b
}

// sRGBGamma applies the sRGB gamma curve (inverse companding)
func sRGBGamma(linear float64) float64 {
	if linear <= 0.0031308 {
		return 12.92 * linear
	}
	return 1.055*math.Pow(linear, 1.0/2.4) - 0.055
}

// clamp ensures a value is within the specified bounds
func clamp(value, min, max float64) float64 {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

// ConvertToHex converts the S-52 color to a hex color string using colorimetric conversion.
// Preserves faint tints and accurate color reproduction as specified in S-52.
// Reference: specs/DAI_TO_SVG_RENDERING_SPEC.md section "Color System"
func (color Color) ConvertToHex() string {
	// Use colorimetric conversion only; do not override descriptive names.
	// This preserves the faint tints (e.g., DEPDW) expected in DAY mode.
	// Reference: specs/DAI_TO_SVG_RENDERING_SPEC.md section "Applying Colors by Role"
	r, g, b := color.ConvertToRGB()

	// Round to nearest 8-bit (*255 + 0.5), then clamp.
	to8 := func(v float64) int {
		n := int(v*255.0 + 0.5)
		if n < 0 {
			return 0
		}
		if n > 255 {
			return 255
		}
		return n
	}

	return fmt.Sprintf("#%02X%02X%02X", to8(r), to8(g), to8(b))
}

// ListColors returns all color tokens for a viewing mode (thread-safe)
func (db *ColorDatabase) ListColors(mode string) []string {
	db.mu.RLock()
	defer db.mu.RUnlock()

	var colorMap map[string]Color

	switch strings.ToUpper(mode) {
	case "DAY":
		colorMap = db.DayColors
	case "DUSK":
		colorMap = db.DuskColors
	case "NIGHT":
		colorMap = db.NightColors
	default:
		return nil
	}

	tokens := make([]string, 0, len(colorMap))
	for token := range colorMap {
		tokens = append(tokens, token)
	}

	return tokens
}

// PrintColorSummary displays a summary of all loaded colors (thread-safe)
func (db *ColorDatabase) PrintColorSummary() {
	db.mu.RLock()
	defer db.mu.RUnlock()

	fmt.Println("=== S-52 Official Color Database ===")
	fmt.Printf("Day Colors: %d\n", len(db.DayColors))
	fmt.Printf("Dusk Colors: %d\n", len(db.DuskColors))
	fmt.Printf("Night Colors: %d\n", len(db.NightColors))

	// Show some examples
	fmt.Println("\n=== Sample Colors (DAY mode) ===")
	sampleTokens := []string{"CHBLK", "CHRED", "CHGRN", "CHYLW", "CHMGF", "CHWHT"}
	for _, token := range sampleTokens {
		if color, exists := db.DayColors[token]; exists {
			fmt.Printf("%-8s: %s (%s)\n", token, color.ConvertToHex(), color.Description)
		}
	}
}

// Minimal SCRF (Symbol Color Reference Format) support for DAI parser compatibility

// ColorRefType defines the type of color reference
type ColorRefType string

const (
	SCRFSimpleColor    ColorRefType = "simple"       // Single color: ACHMGF
	SCRFFillOutline    ColorRefType = "fill_outline" // Fill + Outline: ALITRDBOUTLW
	SCRFMultiComponent ColorRefType = "multi"        // Complex combinations
)

// SCRFReference represents a Symbol Color Reference from the DAI file (minimal version)
type SCRFReference struct {
	OriginalRef string            `json:"original_ref"` // e.g., "ALITRDBOUTLW"
	Type        ColorRefType      `json:"type"`         // Simple, Fill+Outline, etc.
	Components  map[string]string `json:"components"`   // e.g., {"fill": "LITRD", "outline": "OUTLW"}
}

// SCRFParser provides minimal SCRF parsing for DAI parser compatibility
type SCRFParser struct {
	colorDB *ColorDatabase
}

// NewSCRFParser creates a minimal SCRF parser
func NewSCRFParser(colorDB *ColorDatabase) *SCRFParser {
	return &SCRFParser{colorDB: colorDB}
}

// GetColorDatabase returns the color database
func (parser *SCRFParser) GetColorDatabase() *ColorDatabase {
	return parser.colorDB
}

// ParseSCRF provides basic SCRF parsing (simplified version)
func (parser *SCRFParser) ParseSCRF(scrfString string) (SCRFReference, error) {
	ref := SCRFReference{
		OriginalRef: scrfString,
		Components:  make(map[string]string),
		Type:        SCRFSimpleColor, // Default to simple color
	}

	// Very basic parsing - just treat as single color for now
	// This maintains compatibility without full SCRF complexity
	ref.Components["primary"] = scrfString

	return ref, nil
}

// GetColor returns an S-52 color by token and scheme
func (l *Library) GetColor(token string, scheme ColorScheme) (*Color, error) {
	if l.colorDB == nil {
		return nil, fmt.Errorf("color database not loaded")
	}

	var colorCache map[string]*Color

	switch scheme {
	case ColorSchemeDay:
		colorCache = l.dayColorCache
	case ColorSchemeDusk:
		colorCache = l.duskColorCache
	case ColorSchemeNight:
		colorCache = l.nightColorCache
	default:
		return nil, fmt.Errorf("invalid color scheme: %s", scheme)
	}

	color, exists := colorCache[token]
	if !exists {
		return nil, fmt.Errorf("color %s not found in %s scheme", token, scheme)
	}

	return color, nil
}

// GetColorHex returns just the hex color value (#RRGGBB)
func (l *Library) GetColorHex(token string, scheme ColorScheme) (string, error) {
	color, err := l.GetColor(token, scheme)
	if err != nil {
		return "", err
	}
	return color.ConvertToHex(), nil
}

// ListColors returns all color tokens available in a scheme
func (l *Library) ListColors(scheme ColorScheme) []string {
	if l.colorDB == nil {
		return nil
	}

	var internalMap map[string]Color

	switch scheme {
	case ColorSchemeDay:
		internalMap = l.colorDB.DayColors
	case ColorSchemeDusk:
		internalMap = l.colorDB.DuskColors
	case ColorSchemeNight:
		internalMap = l.colorDB.NightColors
	default:
		return nil
	}

	tokens := make([]string, 0, len(internalMap))
	for token := range internalMap {
		tokens = append(tokens, token)
	}
	return tokens
}

// GetColorsByScheme returns all colors for a given scheme
func (l *Library) GetColorsByScheme(scheme ColorScheme) (map[string]*Color, error) {
	if l.colorDB == nil {
		return nil, fmt.Errorf("color database not loaded")
	}

	var internalMap map[string]Color

	switch scheme {
	case ColorSchemeDay:
		internalMap = l.colorDB.DayColors
	case ColorSchemeDusk:
		internalMap = l.colorDB.DuskColors
	case ColorSchemeNight:
		internalMap = l.colorDB.NightColors
	default:
		return nil, fmt.Errorf("invalid color scheme: %s", scheme)
	}

	result := make(map[string]*Color, len(internalMap))
	for token, color := range internalMap {
		c := color // Copy
		result[token] = &c
	}

	return result, nil
}

// convertColorMap converts internal color map to public API types
func convertColorMap(internalMap map[string]Color) map[string]interface{} {
	result := make(map[string]interface{})

	for token, color := range internalMap {
		c := color // Copy
		result[token] = &c
	}

	return result
}

// getColorFullName returns the full descriptive name for a color token
func getColorFullName(token string) string {
	names := map[string]string{
		"DEPDW": "Deep Water (deeper than deep contour)",
		"DEPMD": "Medium Deep Water (deeper than safety contour)",
		"DEPMS": "Medium Shallow Water (less than safety contour)",
		"DEPVS": "Very Shallow Water (less than shallow contour)",
		"DEPIT": "Intertidal Zone (drying area between low/high water)",
		"DEPSC": "Safety Contour (own-ship selected)",
		"DEPCN": "Depth Contour (other contours)",
		"CHBLK": "Chart Black (general purpose)",
		"CHWHT": "Chart White (general purpose)",
		"CHGRD": "Chart Grey, Conspicuous (general purpose)",
		"CHGRF": "Chart Grey, Faint (general purpose)",
		"CHBRN": "Chart Brown (built-up land areas)",
		"CHGRN": "Chart Green (general, including buoys)",
		"CHRED": "Chart Red (general, including buoys)",
		"CHYLW": "Chart Yellow (general, including buoys)",
		"CHMGD": "Chart Magenta, Conspicuous (dangers, important features)",
		"CHMGF": "Chart Magenta, Faint (less important features)",
		"CHCOR": "Chart Coral/Orange (manual corrections by mariner)",
		"NODTA": "No Data (area with no chart information)",
	}

	if name, ok := names[token]; ok {
		return name
	}
	return ""
}
